package issues

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

type rebuildPhase string

const (
	phaseModelDiscovery rebuildPhase = "model_discovery"
	phaseRouting        rebuildPhase = "routing_projection"
	phaseValidation     rebuildPhase = "resource_validator"
)

type rebuildCursor struct {
	phase          rebuildPhase
	afterID        int64
	providerCursor string
}

func (service *Service) RebuildBatch(ctx context.Context, userID int64) (RebuildResult, error) {
	if service == nil || service.repository == nil || userID <= 0 {
		return RebuildResult{}, ErrInvalidRequest
	}
	repository := service.repository
	now, err := repository.nowUnix()
	if err != nil {
		return RebuildResult{}, err
	}
	tx, err := beginTx(ctx, repository.db)
	if err != nil {
		return RebuildResult{}, err
	}
	committed := false
	defer finishTx(tx, &committed)
	var incomplete, generation int64
	var persisted sql.NullString
	err = tx.QueryRowContext(ctx, `
SELECT projection_incomplete,rebuild_generation,rebuild_cursor
FROM user_issue_projection_state WHERE user_id=?`, userID).Scan(&incomplete, &generation, &persisted)
	if errors.Is(err, sql.ErrNoRows) {
		if err := commitTx(tx, &committed); err != nil {
			return RebuildResult{}, err
		}
		return RebuildResult{UserID: userID, Complete: true}, nil
	}
	if err != nil {
		return RebuildResult{}, fmt.Errorf("issues: read rebuild checkpoint: %w", err)
	}
	if incomplete == 0 {
		if persisted.Valid {
			return RebuildResult{}, ErrUnavailable
		}
		if err := commitTx(tx, &committed); err != nil {
			return RebuildResult{}, err
		}
		return RebuildResult{UserID: userID, Generation: generation, Complete: true}, nil
	}
	if incomplete != 1 || generation < 1 {
		return RebuildResult{}, ErrUnavailable
	}
	cursorText := initialRebuildCursor
	if persisted.Valid {
		cursorText = persisted.String
	}
	cursor, err := parseRebuildCursor(cursorText)
	if err != nil {
		return RebuildResult{}, err
	}
	processed := 0
	completeCoverage := false
	capacityLimited := false
rebuildLoop:
	for processed < maxPageLimit && !completeCoverage {
		remaining := maxPageLimit - processed
		switch cursor.phase {
		case phaseModelDiscovery:
			ids, done, err := scanDiscoveryKeys(ctx, tx, userID, cursor.afterID, remaining)
			if err != nil {
				return RebuildResult{}, err
			}
			for _, keyID := range ids {
				if err := service.sources.reconcileModelDiscoveryTracked(ctx, tx, userID, keyID, now, false, &capacityLimited); err != nil {
					return RebuildResult{}, err
				}
				processed++
				cursor.afterID = keyID
			}
			if done {
				if err := repository.scrubMissingResourcesTx(ctx, tx, userID, SourceModelDiscovery, ResourceEndpointKey, now); err != nil {
					return RebuildResult{}, err
				}
				cursor = rebuildCursor{phase: phaseRouting}
			}
		case phaseRouting:
			ids, done, err := scanModels(ctx, tx, userID, cursor.afterID, remaining)
			if err != nil {
				return RebuildResult{}, err
			}
			for _, modelID := range ids {
				if err := service.sources.reconcileRoutingTracked(ctx, tx, userID, modelID, now, false, &capacityLimited); err != nil {
					return RebuildResult{}, err
				}
				processed++
				cursor.afterID = modelID
			}
			if done {
				if err := repository.scrubMissingResourcesTx(ctx, tx, userID, SourceRoutingProjection, ResourceModel, now); err != nil {
					return RebuildResult{}, err
				}
				cursor = rebuildCursor{phase: phaseValidation}
			}
		case phaseValidation:
			batch, err := repository.resourceValidation.Scan(ctx, tx, userID, cursor.providerCursor, remaining)
			if err != nil {
				return RebuildResult{}, fmt.Errorf("issues: scan resource validation authority: %w", err)
			}
			if len(batch.Items) > remaining || (!batch.Done && (batch.NextCursor == "" || batch.NextCursor == cursor.providerCursor)) ||
				batch.Done && batch.NextCursor != "" || !validProviderCursor(batch.NextCursor) {
				return RebuildResult{}, ErrUnavailable
			}
			for _, target := range batch.Items {
				if !validTuple(SourceResourceValidator, target.ResourceKind, target.RootCause) || target.ResourceID <= 0 {
					return RebuildResult{}, ErrUnavailable
				}
				if err := service.sources.reconcileResourceValidationTracked(ctx, tx, userID, target.ResourceKind, target.ResourceID, target.RootCause, now, false, &capacityLimited); err != nil {
					return RebuildResult{}, err
				}
				processed++
			}
			emptyContinuation := len(batch.Items) == 0 && !batch.Done
			cursor.providerCursor = batch.NextCursor
			if batch.Done {
				if err := repository.scrubAllMissingValidatorResourcesTx(ctx, tx, userID, now); err != nil {
					return RebuildResult{}, err
				}
				completeCoverage = true
			}
			// A provider may advance over an empty authority page. Persist that
			// progress and yield instead of following an unbounded cursor chain
			// inside one fixed rebuild batch.
			if emptyContinuation {
				break rebuildLoop
			}
		default:
			return RebuildResult{}, ErrUnavailable
		}
	}
	complete := false
	nextCursor := encodeRebuildCursor(cursor)
	if completeCoverage && !capacityLimited {
		var current int64
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM user_issues WHERE user_id=? AND state='current'`, userID).Scan(&current); err != nil {
			return RebuildResult{}, fmt.Errorf("issues: count rebuilt current projection: %w", err)
		}
		if current <= maxCurrentPerUser {
			result, err := tx.ExecContext(ctx, `
UPDATE user_issue_projection_state SET projection_incomplete=0,rebuild_cursor=NULL,updated_at=?
WHERE user_id=? AND projection_incomplete=1 AND rebuild_generation=? AND rebuild_cursor IS ?`,
				now, userID, generation, nullableCursorValue(persisted))
			if err != nil {
				return RebuildResult{}, fmt.Errorf("issues: complete rebuild checkpoint: %w", err)
			}
			if !exactlyOne(result) {
				return RebuildResult{}, ErrConflict
			}
			if _, err := tx.ExecContext(ctx, `
UPDATE admin_alerts SET resolved=1,resolved_at=COALESCE(resolved_at,?)
WHERE kind='issue_projection_incomplete' AND subject_user_id=? AND resolved=0`, now, userID); err != nil {
				return RebuildResult{}, fmt.Errorf("issues: resolve projection alert: %w", err)
			}
			complete = true
		}
	} else if completeCoverage {
		nextCursor = initialRebuildCursor
	}
	if !complete {
		result, err := tx.ExecContext(ctx, `
UPDATE user_issue_projection_state SET rebuild_cursor=?,updated_at=?
WHERE user_id=? AND projection_incomplete=1 AND rebuild_generation=? AND rebuild_cursor IS ?`,
			nextCursor, now, userID, generation, nullableCursorValue(persisted))
		if err != nil {
			return RebuildResult{}, fmt.Errorf("issues: advance rebuild checkpoint: %w", err)
		}
		if !exactlyOne(result) {
			return RebuildResult{}, ErrConflict
		}
	}
	if err := commitTx(tx, &committed); err != nil {
		return RebuildResult{}, err
	}
	return RebuildResult{UserID: userID, Generation: generation, Processed: processed, Complete: complete}, nil
}

func (service *Service) RebuildIncomplete(ctx context.Context, accountLimit int) ([]RebuildResult, error) {
	if service == nil || service.repository == nil || accountLimit < 1 || accountLimit > 100 {
		return nil, ErrInvalidRequest
	}
	rows, err := service.repository.db.QueryContext(ctx, `
SELECT user_id FROM user_issue_projection_state WHERE projection_incomplete=1 ORDER BY updated_at,user_id LIMIT ?`, accountLimit)
	if err != nil {
		return nil, fmt.Errorf("issues: list incomplete accounts: %w", err)
	}
	ids := make([]int64, 0, accountLimit)
	for rows.Next() {
		var userID int64
		if err := rows.Scan(&userID); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("issues: scan incomplete account: %w", err)
		}
		ids = append(ids, userID)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("issues: close incomplete account rows: %w", err)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("issues: list incomplete accounts: %w", err)
	}
	results := make([]RebuildResult, 0, len(ids))
	for _, userID := range ids {
		result, err := service.RebuildBatch(ctx, userID)
		if err != nil {
			return results, err
		}
		results = append(results, result)
	}
	return results, nil
}

func scanDiscoveryKeys(ctx context.Context, tx *sql.Tx, userID, afterID int64, limit int) ([]int64, bool, error) {
	rows, err := tx.QueryContext(ctx, `
SELECT k.id FROM endpoint_keys k JOIN endpoints e ON e.id=k.endpoint_id
WHERE e.user_id=? AND k.id>? ORDER BY k.id LIMIT ?`, userID, afterID, limit+1)
	if err != nil {
		return nil, false, fmt.Errorf("issues: scan discovery authorities: %w", err)
	}
	defer rows.Close()
	ids := make([]int64, 0, limit+1)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, false, fmt.Errorf("issues: scan discovery authority: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, false, fmt.Errorf("issues: scan discovery authorities: %w", err)
	}
	done := len(ids) <= limit
	if !done {
		ids = ids[:limit]
	}
	return ids, done, nil
}

func scanModels(ctx context.Context, tx *sql.Tx, userID, afterID int64, limit int) ([]int64, bool, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id FROM models WHERE user_id=? AND id>? ORDER BY id LIMIT ?`, userID, afterID, limit+1)
	if err != nil {
		return nil, false, fmt.Errorf("issues: scan routing authorities: %w", err)
	}
	defer rows.Close()
	ids := make([]int64, 0, limit+1)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, false, fmt.Errorf("issues: scan routing authority: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, false, fmt.Errorf("issues: scan routing authorities: %w", err)
	}
	done := len(ids) <= limit
	if !done {
		ids = ids[:limit]
	}
	return ids, done, nil
}

func parseRebuildCursor(value string) (rebuildCursor, error) {
	if value == "" || len(value) > maxRebuildCursorLen {
		return rebuildCursor{}, ErrUnavailable
	}
	if strings.HasPrefix(value, "model_discovery:") {
		id, err := parseNonnegativeID(strings.TrimPrefix(value, "model_discovery:"))
		return rebuildCursor{phase: phaseModelDiscovery, afterID: id}, err
	}
	if strings.HasPrefix(value, "routing_projection:") {
		id, err := parseNonnegativeID(strings.TrimPrefix(value, "routing_projection:"))
		return rebuildCursor{phase: phaseRouting, afterID: id}, err
	}
	if strings.HasPrefix(value, "resource_validator:") {
		encoded := strings.TrimPrefix(value, "resource_validator:")
		if encoded == "" {
			return rebuildCursor{phase: phaseValidation}, nil
		}
		decoded, err := base64.RawURLEncoding.DecodeString(encoded)
		if err != nil || !validProviderCursor(string(decoded)) || base64.RawURLEncoding.EncodeToString(decoded) != encoded {
			return rebuildCursor{}, ErrUnavailable
		}
		return rebuildCursor{phase: phaseValidation, providerCursor: string(decoded)}, nil
	}
	return rebuildCursor{}, ErrUnavailable
}

func encodeRebuildCursor(cursor rebuildCursor) string {
	switch cursor.phase {
	case phaseModelDiscovery:
		return "model_discovery:" + strconv.FormatInt(cursor.afterID, 10)
	case phaseRouting:
		return "routing_projection:" + strconv.FormatInt(cursor.afterID, 10)
	case phaseValidation:
		return "resource_validator:" + base64.RawURLEncoding.EncodeToString([]byte(cursor.providerCursor))
	default:
		return ""
	}
}

func parseNonnegativeID(value string) (int64, error) {
	if value == "" || (len(value) > 1 && value[0] == '0') {
		return 0, ErrUnavailable
	}
	for index := range value {
		if value[index] < '0' || value[index] > '9' {
			return 0, ErrUnavailable
		}
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed < 0 {
		return 0, ErrUnavailable
	}
	return parsed, nil
}

func validProviderCursor(value string) bool {
	if len(value) > maxCursorBytes {
		return false
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

func nullableCursorValue(value sql.NullString) any {
	if value.Valid {
		return value.String
	}
	return nil
}

type missingResourceCandidate struct{ id, ref string }

type missingResourceRows interface {
	Next() bool
	Scan(...any) error
	Close() error
	Err() error
}

func collectMissingResourceCandidates(rows missingResourceRows, subject string) ([]missingResourceCandidate, error) {
	items := make([]missingResourceCandidate, 0)
	for rows.Next() {
		var item missingResourceCandidate
		if err := rows.Scan(&item.id, &item.ref); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("issues: scan %s resource: %w", subject, err)
		}
		items = append(items, item)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("issues: close %s rows: %w", subject, err)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("issues: iterate %s rows: %w", subject, err)
	}
	return items, nil
}

func (repository *Repository) scrubMissingResourcesTx(ctx context.Context, tx *sql.Tx, userID int64, source Source, kind ResourceKind, now int64) error {
	rows, err := tx.QueryContext(ctx, `SELECT id,resource_ref FROM user_issues WHERE user_id=? AND source=? AND resource_kind=?`, userID, source, kind)
	if err != nil {
		return fmt.Errorf("issues: list missing source resources: %w", err)
	}
	candidates, err := collectMissingResourceCandidates(rows, "missing source")
	if err != nil {
		return err
	}
	for _, item := range candidates {
		resourceID, err := strconv.ParseInt(item.ref, 10, 64)
		if err != nil || resourceID <= 0 {
			continue
		}
		var exists int
		switch kind {
		case ResourceEndpointKey:
			err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM endpoint_keys k JOIN endpoints e ON e.id=k.endpoint_id WHERE k.id=? AND e.user_id=?)`, resourceID, userID).Scan(&exists)
		case ResourceModel:
			err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM models WHERE id=? AND user_id=?)`, resourceID, userID).Scan(&exists)
		default:
			return ErrUnavailable
		}
		if err != nil {
			return fmt.Errorf("issues: verify missing source resource: %w", err)
		}
		if exists == 0 {
			if err := repository.scrubIssueIDTx(ctx, tx, userID, item.id, now); err != nil {
				return err
			}
		}
	}
	return trimClosedTx(ctx, tx, userID)
}

func (repository *Repository) scrubAllMissingValidatorResourcesTx(ctx context.Context, tx *sql.Tx, userID, now int64) error {
	for _, kind := range []ResourceKind{ResourceEndpoint, ResourceEndpointKey} {
		rows, err := tx.QueryContext(ctx, `SELECT id,resource_ref FROM user_issues WHERE user_id=? AND source='resource_validator' AND resource_kind=?`, userID, kind)
		if err != nil {
			return fmt.Errorf("issues: list missing validator resources: %w", err)
		}
		items, err := collectMissingResourceCandidates(rows, "missing validator")
		if err != nil {
			return err
		}
		for _, item := range items {
			resourceID, err := strconv.ParseInt(item.ref, 10, 64)
			if err != nil || resourceID <= 0 {
				continue
			}
			_, ownerErr := readOwnerProjection(ctx, tx, userID, kind, resourceID)
			if errors.Is(ownerErr, ErrNotFound) {
				if err := repository.scrubIssueIDTx(ctx, tx, userID, item.id, now); err != nil {
					return err
				}
			} else if ownerErr != nil {
				return ownerErr
			}
		}
	}
	return trimClosedTx(ctx, tx, userID)
}

func (repository *Repository) scrubIssueIDTx(ctx context.Context, tx *sql.Tx, userID int64, issueID string, now int64) error {
	scrubbed, err := repository.scrubReference(issueID)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `
UPDATE user_issues SET state='closed',
 closed_at=CASE WHEN state='current' THEN ? ELSE closed_at END,
 retain_until=CASE WHEN state='current' THEN ? ELSE retain_until END,
 resource_ref=?,safe_detail='',deep_link_kind=NULL,deep_link_ref=NULL
WHERE id=? AND user_id=?`, now, now+closedRetention, scrubbed, issueID, userID)
	if err != nil {
		return fmt.Errorf("issues: scrub missing resource projection: %w", err)
	}
	return nil
}

func exactlyOne(result sql.Result) bool {
	if result == nil {
		return false
	}
	rows, err := result.RowsAffected()
	return err == nil && rows == 1
}
