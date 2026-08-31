package reports

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"math"
	"net/netip"
	"strconv"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/db"
)

const (
	caseCursorScope     = "report-cases/v1"
	materialCursorScope = "report-materials/v1"
	targetCursorScope   = "report-targets/v1"
)

func validCaseStatus(status string, allowEmpty bool) bool {
	if status == "" {
		return allowEmpty
	}
	switch status {
	case "pending_indexing", "pending_review", "approved_processing", "approved", "rejected", "expired":
		return true
	default:
		return false
	}
}

func validPageLimit(limit int) bool { return limit >= 1 && limit <= maxPageLimit }

func (repository *Repository) beginAdminRead(ctx context.Context, actor authz.Actor) (*sql.Tx, *bool, error) {
	if ctx == nil {
		return nil, nil, ErrInvalidRequest
	}
	tx, err := repository.beginTx(ctx)
	if err != nil {
		return nil, nil, err
	}
	committed := new(bool)
	if _, err := repository.authorizeAdminTx(ctx, tx, actor); err != nil {
		_ = tx.Rollback()
		return nil, nil, err
	}
	return tx, committed, nil
}

const legalHoldRetentionSeconds int64 = 400 * 24 * 60 * 60

func reportCaseOrdinarilyVisible(status string, createdAt, now int64) (bool, error) {
	if !validCaseStatus(status, false) || createdAt < 0 || createdAt > maxUnixSecond || now < 0 || now > maxUnixSecond {
		return false, ErrInvariant
	}
	switch status {
	case "pending_indexing", "pending_review", "approved_processing":
		return true, nil
	default:
		return createdAt <= maxUnixSecond-caseRetentionSeconds && createdAt+caseRetentionSeconds > now, nil
	}
}

// prepareReportObjectReadTx materializes a due report hold and records the
// bounded object-read rollup. It returns true only while an active hold may
// extend this known-ID read beyond the report's ordinary retention cutoff.
func prepareReportObjectReadTx(
	ctx context.Context,
	tx *sql.Tx,
	caseID string,
	adminUserID int64,
	now int64,
) (bool, error) {
	if ctx == nil || tx == nil || !db.ValidateOpaqueID(caseID, "rpc_") || adminUserID <= 0 || now < 0 || now > maxUnixSecond {
		return false, ErrInvalidRequest
	}
	var holdID, state string
	var revision, expiresAt int64
	var marker int
	err := tx.QueryRowContext(ctx, `SELECT h.id,h.state,h.revision,h.expires_at,c.legal_hold_consumed
FROM legal_holds h JOIN report_cases c ON c.id=h.object_ref
WHERE h.object_kind='report_case' AND h.object_ref=?`, caseID).Scan(&holdID, &state, &revision, &expiresAt, &marker)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("reports: read report legal hold: %w", err)
	}
	if !db.ValidateOpaqueID(holdID, "lgh_") || marker != 1 || revision < 1 || expiresAt < 0 || expiresAt > maxUnixSecond {
		return false, ErrInvariant
	}
	if state != "active" {
		if state != "released" && state != "expired" {
			return false, ErrInvariant
		}
		return false, nil
	}
	if expiresAt <= now {
		if revision == math.MaxInt64 || expiresAt > maxUnixSecond-legalHoldRetentionSeconds {
			return false, ErrInvariant
		}
		retainUntil := expiresAt + legalHoldRetentionSeconds
		result, err := tx.ExecContext(ctx, `UPDATE legal_holds SET
state='expired',revision=revision+1,ended_by_user_id=NULL,ended_at=expires_at,
end_reason='expired',retain_until=?
WHERE id=? AND state='active' AND revision=? AND expires_at<=?`, retainUntil, holdID, revision, now)
		if err != nil {
			return false, fmt.Errorf("reports: expire report legal hold: %w", err)
		}
		changed, _ := result.RowsAffected()
		if changed != 1 {
			return false, ErrConflict
		}
		result, err = tx.ExecContext(ctx, `UPDATE legal_hold_audits SET retain_until=?
WHERE hold_id_text=? AND action='create' AND retain_until IS NULL`, retainUntil, holdID)
		if err != nil {
			return false, fmt.Errorf("reports: retain report hold create audit: %w", err)
		}
		changed, _ = result.RowsAffected()
		if changed != 1 {
			return false, ErrInvariant
		}
		if _, err := tx.ExecContext(ctx, `UPDATE legal_hold_read_audits SET retain_until=?
WHERE hold_id_text=? AND retain_until IS NULL`, retainUntil, holdID); err != nil {
			return false, fmt.Errorf("reports: retain report hold read audits: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO legal_hold_audits(
hold_id_text,actor_user_id,action,reason,created_at,retain_until)
VALUES(?,NULL,'expire','expired',?,?)`, holdID, expiresAt, retainUntil); err != nil {
			return false, fmt.Errorf("reports: append report hold expiry: %w", err)
		}
		return false, nil
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO legal_hold_read_audits(
hold_id_text,admin_user_id,read_kind,first_read_at,last_read_at,read_count,retain_until)
VALUES(?,?,'object',?,?,1,NULL)
ON CONFLICT(hold_id_text,admin_user_id,read_kind) DO UPDATE SET
last_read_at=excluded.last_read_at,read_count=legal_hold_read_audits.read_count+1
WHERE legal_hold_read_audits.retain_until IS NULL
 AND excluded.last_read_at>=legal_hold_read_audits.last_read_at`, holdID, adminUserID, now, now)
	if err != nil {
		return false, fmt.Errorf("reports: record report hold object read: %w", err)
	}
	changed, _ := result.RowsAffected()
	if changed != 1 {
		return false, ErrInvariant
	}
	return true, nil
}

func (repository *Repository) Badge(ctx context.Context, actor authz.Actor) (BadgeResponse, error) {
	if err := repository.admit(); err != nil {
		return BadgeResponse{}, err
	}
	defer repository.release()
	tx, committed, err := repository.beginAdminRead(ctx, actor)
	if err != nil {
		return BadgeResponse{}, err
	}
	defer rollbackUnlessCommitted(tx, committed)
	var indexing, review, processing int64
	if err := tx.QueryRowContext(ctx, `SELECT
COALESCE(SUM(CASE WHEN status='pending_indexing' THEN 1 ELSE 0 END),0),
COALESCE(SUM(CASE WHEN status='pending_review' THEN 1 ELSE 0 END),0),
COALESCE(SUM(CASE WHEN status='approved_processing' THEN 1 ELSE 0 END),0)
FROM report_cases WHERE status IN ('pending_indexing','pending_review','approved_processing')`).Scan(&indexing, &review, &processing); err != nil {
		return BadgeResponse{}, fmt.Errorf("reports: read badge: %w", err)
	}
	if indexing < 0 || review < 0 || processing < 0 {
		return BadgeResponse{}, ErrInvariant
	}
	response := BadgeResponse{
		Total: strconv.FormatInt(indexing+review+processing, 10),
		ByStatus: map[string]string{
			"pending_indexing":    strconv.FormatInt(indexing, 10),
			"pending_review":      strconv.FormatInt(review, 10),
			"approved_processing": strconv.FormatInt(processing, 10),
		},
	}
	if err := commit(tx, committed); err != nil {
		return BadgeResponse{}, err
	}
	return response, nil
}

type summaryScanner interface{ Scan(...any) error }

func scanCaseSummary(scanner summaryScanner) (CaseSummary, error) {
	var (
		response                                                 CaseSummary
		materialVersion, targetVersion                           int64
		materials, targets, owners, processed, deleted, released int64
		retryAttempt                                             int64
		nextRetry, terminal                                      sql.NullInt64
		lastError                                                sql.NullString
	)
	if err := scanner.Scan(
		&response.ID, &response.Status, &response.ProgressState, &response.ConnectorType,
		&response.CanonicalBaseURL, &materialVersion, &targetVersion, &response.Deadline,
		&materials, &targets, &owners, &processed, &deleted, &released,
		&retryAttempt, &nextRetry, &lastError, &response.CreatedAt, &terminal); err != nil {
		return CaseSummary{}, err
	}
	if !db.ValidateOpaqueID(response.ID, "rpc_") || !validCaseStatus(response.Status, false) ||
		materialVersion < 1 || targetVersion < 1 || materials < 0 || targets < 0 || owners < 0 ||
		processed < 0 || deleted < 0 || released < 0 || processed > targets || deleted > targets || released > targets {
		return CaseSummary{}, ErrInvariant
	}
	response.MaterialVersion = strconv.FormatInt(materialVersion, 10)
	response.TargetVersion = strconv.FormatInt(targetVersion, 10)
	response.Counts = ReportCounts{
		Materials: strconv.FormatInt(materials, 10), Targets: strconv.FormatInt(targets, 10),
		DistinctOwners: strconv.FormatInt(owners, 10), Processed: strconv.FormatInt(processed, 10),
		Deleted: strconv.FormatInt(deleted, 10), Released: strconv.FormatInt(released, 10),
	}
	if retryAttempt == 0 {
		if nextRetry.Valid || lastError.Valid {
			return CaseSummary{}, ErrInvariant
		}
	} else {
		if !nextRetry.Valid || !lastError.Valid {
			return CaseSummary{}, ErrInvariant
		}
		response.Retry = &ReportRetry{
			AttemptCount: strconv.FormatInt(retryAttempt, 10), NextAttemptAt: nextRetry.Int64,
			LastErrorClass: lastError.String,
		}
	}
	if terminal.Valid {
		value := terminal.Int64
		response.TerminalAt = &value
	}
	return response, nil
}

const caseSummaryColumns = `id,status,progress_state,connector_type,canonical_base_url,
material_version,target_version,deadline,material_count,target_count,distinct_owner_count,
processed_target_count,deleted_target_count,released_target_count,retry_attempt_count,next_retry_at,
last_error_class,created_at,terminal_at`

func (repository *Repository) ListCases(
	ctx context.Context,
	actor authz.Actor,
	status string,
	cursor string,
	limit int,
) (Page[CaseSummary], error) {
	if err := repository.admit(); err != nil {
		return Page[CaseSummary]{}, err
	}
	defer repository.release()
	if !validCaseStatus(status, true) || !validPageLimit(limit) {
		return Page[CaseSummary]{}, ErrInvalidRequest
	}
	now, err := repository.nowUnix()
	if err != nil {
		return Page[CaseSummary]{}, err
	}
	owner := status
	if owner == "" {
		owner = "all"
	}
	var cursorTime int64
	cursorID := ""
	if cursor != "" {
		cursorTime, cursorID, err = repository.cursors.decode(cursor, caseCursorScope, owner, now)
		if err != nil || !db.ValidateOpaqueID(cursorID, "rpc_") {
			return Page[CaseSummary]{}, ErrInvalidRequest
		}
	}
	tx, committed, err := repository.beginAdminRead(ctx, actor)
	if err != nil {
		return Page[CaseSummary]{}, err
	}
	defer rollbackUnlessCommitted(tx, committed)
	query := `SELECT ` + caseSummaryColumns + ` FROM report_cases
WHERE (?='' OR status=?)
 AND (status IN ('pending_indexing','pending_review','approved_processing') OR created_at+?>?)`
	args := []any{status, status, caseRetentionSeconds, now}
	if cursor != "" {
		query += ` AND (created_at<? OR (created_at=? AND id<?))`
		args = append(args, cursorTime, cursorTime, cursorID)
	}
	query += ` ORDER BY created_at DESC,id DESC LIMIT ?`
	args = append(args, limit+1)
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return Page[CaseSummary]{}, fmt.Errorf("reports: list cases: %w", err)
	}
	defer rows.Close()
	items := make([]CaseSummary, 0, limit+1)
	for rows.Next() {
		item, err := scanCaseSummary(rows)
		if err != nil {
			return Page[CaseSummary]{}, fmt.Errorf("reports: scan case: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return Page[CaseSummary]{}, fmt.Errorf("reports: list cases: %w", err)
	}
	page := Page[CaseSummary]{Data: items}
	if len(items) > limit {
		last := items[limit-1]
		token, err := repository.cursors.encode(caseCursorScope, owner, last.CreatedAt, last.ID, now)
		if err != nil {
			return Page[CaseSummary]{}, err
		}
		page.Data = items[:limit]
		page.NextCursor = &token
	}
	if err := commit(tx, committed); err != nil {
		return Page[CaseSummary]{}, err
	}
	return page, nil
}

func (repository *Repository) CaseDetail(
	ctx context.Context,
	actor authz.Actor,
	caseID string,
	materialsCursor string,
	materialsLimit int,
) (CaseDetail, error) {
	if err := repository.admit(); err != nil {
		return CaseDetail{}, err
	}
	defer repository.release()
	if !db.ValidateOpaqueID(caseID, "rpc_") || !validPageLimit(materialsLimit) {
		return CaseDetail{}, ErrInvalidRequest
	}
	now, err := repository.nowUnix()
	if err != nil {
		return CaseDetail{}, err
	}
	var cursorTime, cursorID int64
	if materialsCursor != "" {
		var encodedID string
		cursorTime, encodedID, err = repository.cursors.decode(materialsCursor, materialCursorScope, caseID, now)
		if err != nil {
			return CaseDetail{}, ErrInvalidRequest
		}
		cursorID, err = strconv.ParseInt(encodedID, 10, 64)
		if err != nil || cursorID <= 0 {
			return CaseDetail{}, ErrInvalidRequest
		}
	}
	tx, committed, err := repository.beginAdminRead(ctx, actor)
	if err != nil {
		return CaseDetail{}, err
	}
	defer rollbackUnlessCommitted(tx, committed)
	var caseStatus string
	var caseCreatedAt int64
	err = tx.QueryRowContext(ctx, `SELECT status,created_at FROM report_cases WHERE id=?`, caseID).Scan(
		&caseStatus, &caseCreatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return CaseDetail{}, ErrNotFound
	}
	if err != nil {
		return CaseDetail{}, fmt.Errorf("reports: inspect case detail: %w", err)
	}
	ordinaryVisible, err := reportCaseOrdinarilyVisible(caseStatus, caseCreatedAt, now)
	if err != nil {
		return CaseDetail{}, err
	}
	heldVisible, err := prepareReportObjectReadTx(ctx, tx, caseID, actor.UserID, now)
	if err != nil {
		return CaseDetail{}, err
	}
	if !ordinaryVisible && !heldVisible {
		if err := commit(tx, committed); err != nil {
			return CaseDetail{}, err
		}
		return CaseDetail{}, ErrNotFound
	}
	summary, err := scanCaseSummary(tx.QueryRowContext(ctx, `SELECT `+caseSummaryColumns+`
FROM report_cases WHERE id=?`, caseID))
	if errors.Is(err, sql.ErrNoRows) {
		return CaseDetail{}, ErrNotFound
	}
	if err != nil {
		return CaseDetail{}, fmt.Errorf("reports: read case detail: %w", err)
	}
	query := `SELECT id,material_hash,note_text,reporter_user_id,reporter_discord_id,source_ip_envelope,created_at
FROM report_materials WHERE case_id=?`
	args := []any{caseID}
	if materialsCursor != "" {
		query += ` AND (created_at>? OR (created_at=? AND id>?))`
		args = append(args, cursorTime, cursorTime, cursorID)
	}
	query += ` ORDER BY created_at,id LIMIT ?`
	args = append(args, materialsLimit+1)
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return CaseDetail{}, fmt.Errorf("reports: list materials: %w", err)
	}
	materials := make([]Material, 0, materialsLimit+1)
	for rows.Next() {
		var (
			id                     int64
			materialHash, envelope []byte
			note                   string
			reporterID             sql.NullInt64
			reporterDiscord        sql.NullString
			createdAt              int64
		)
		if err := rows.Scan(&id, &materialHash, &note, &reporterID, &reporterDiscord, &envelope, &createdAt); err != nil {
			_ = rows.Close()
			return CaseDetail{}, fmt.Errorf("reports: scan material: %w", err)
		}
		if id <= 0 || len(materialHash) != 32 || len(envelope) != envelopeBytes {
			clear(materialHash)
			clear(envelope)
			_ = rows.Close()
			return CaseDetail{}, ErrInvariant
		}
		var hash [32]byte
		copy(hash[:], materialHash)
		clear(materialHash)
		ip, openErr := repository.keys.openSourceIP(caseID, hash, envelope)
		clear(envelope)
		if openErr != nil {
			_ = rows.Close()
			if alertErr := insertInvariantAlertTx(ctx, tx, caseID, "report_source_ip_envelope_invalid", now); alertErr != nil {
				return CaseDetail{}, alertErr
			}
			if err := commit(tx, committed); err != nil {
				return CaseDetail{}, err
			}
			return CaseDetail{}, ErrInvariant
		}
		address := netip.AddrFrom16(ip).Unmap().String()
		material := Material{ID: strconv.FormatInt(id, 10), NoteText: note, SourceIP: address, CreatedAt: createdAt}
		if reporterID.Valid {
			if !reporterDiscord.Valid || reporterDiscord.String == "" {
				_ = rows.Close()
				return CaseDetail{}, ErrInvariant
			}
			material.Reporter = &Reporter{UserID: strconv.FormatInt(reporterID.Int64, 10), DiscordID: reporterDiscord.String}
		} else if reporterDiscord.Valid {
			_ = rows.Close()
			return CaseDetail{}, ErrInvariant
		}
		materials = append(materials, material)
	}
	if err := rows.Close(); err != nil {
		return CaseDetail{}, fmt.Errorf("reports: close materials: %w", err)
	}
	materialPage := Page[Material]{Data: materials}
	if len(materials) > materialsLimit {
		last := materials[materialsLimit-1]
		id, _ := strconv.ParseInt(last.ID, 10, 64)
		token, err := repository.cursors.encode(materialCursorScope, caseID, last.CreatedAt, strconv.FormatInt(id, 10), now)
		if err != nil {
			return CaseDetail{}, err
		}
		materialPage.Data = materials[:materialsLimit]
		materialPage.NextCursor = &token
	}
	var decision Decision
	var actorID sql.NullInt64
	err = tx.QueryRowContext(ctx, `SELECT action,reason,actor_user_id,created_at FROM report_decisions
WHERE case_id=? AND action IN ('approve','reject','expire') ORDER BY id DESC LIMIT 1`, caseID).Scan(
		&decision.Action, &decision.Reason, &actorID, &decision.CreatedAt)
	var decisionPointer *Decision
	if err == nil {
		if actorID.Valid {
			value := strconv.FormatInt(actorID.Int64, 10)
			decision.ActorUserID = &value
		}
		decisionPointer = &decision
	} else if !errors.Is(err, sql.ErrNoRows) {
		return CaseDetail{}, fmt.Errorf("reports: read decision: %w", err)
	}
	if err := commit(tx, committed); err != nil {
		return CaseDetail{}, err
	}
	return CaseDetail{CaseSummary: summary, Materials: materialPage, Decision: decisionPointer}, nil
}

func (repository *Repository) Targets(
	ctx context.Context,
	actor authz.Actor,
	caseID string,
	cursor string,
	limit int,
) (Page[Target], error) {
	if err := repository.admit(); err != nil {
		return Page[Target]{}, err
	}
	defer repository.release()
	if !db.ValidateOpaqueID(caseID, "rpc_") || !validPageLimit(limit) {
		return Page[Target]{}, ErrInvalidRequest
	}
	now, err := repository.nowUnix()
	if err != nil {
		return Page[Target]{}, err
	}
	var cursorSequence int64
	cursorID := ""
	if cursor != "" {
		cursorSequence, cursorID, err = repository.cursors.decode(cursor, targetCursorScope, caseID, now)
		if err != nil || !db.ValidateOpaqueID(cursorID, "rpt_") {
			return Page[Target]{}, ErrInvalidRequest
		}
	}
	tx, committed, err := repository.beginAdminRead(ctx, actor)
	if err != nil {
		return Page[Target]{}, err
	}
	defer rollbackUnlessCommitted(tx, committed)
	var caseStatus string
	var caseCreatedAt int64
	err = tx.QueryRowContext(ctx, `SELECT status,created_at FROM report_cases WHERE id=?`, caseID).Scan(
		&caseStatus, &caseCreatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Page[Target]{}, ErrNotFound
	}
	if err != nil {
		return Page[Target]{}, fmt.Errorf("reports: inspect target case: %w", err)
	}
	ordinaryVisible, err := reportCaseOrdinarilyVisible(caseStatus, caseCreatedAt, now)
	if err != nil {
		return Page[Target]{}, err
	}
	heldVisible, err := prepareReportObjectReadTx(ctx, tx, caseID, actor.UserID, now)
	if err != nil {
		return Page[Target]{}, err
	}
	if !ordinaryVisible && !heldVisible {
		if err := commit(tx, committed); err != nil {
			return Page[Target]{}, err
		}
		return Page[Target]{}, ErrNotFound
	}
	query := `SELECT id,target_seq,state,endpoint_key_id,key_ref,owner_user_id,owner_discord_id,owner_display_name,
connector_type,canonical_base_url,key_display_head,key_display_tail,discovered_version,decided_version,created_at,updated_at
FROM report_targets WHERE case_id=?`
	args := []any{caseID}
	if cursor != "" {
		query += ` AND (target_seq>? OR (target_seq=? AND id>?))`
		args = append(args, cursorSequence, cursorSequence, cursorID)
	}
	query += ` ORDER BY target_seq,id LIMIT ?`
	args = append(args, limit+1)
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return Page[Target]{}, fmt.Errorf("reports: list targets: %w", err)
	}
	defer rows.Close()
	items := make([]Target, 0, limit+1)
	for rows.Next() {
		var (
			target                              Target
			sequence, discovered                int64
			endpointKeyID, ownerUserID, decided sql.NullInt64
			ownerDiscord, ownerDisplay          sql.NullString
			keyRef                              []byte
		)
		if err := rows.Scan(
			&target.ID, &sequence, &target.State, &endpointKeyID, &keyRef, &ownerUserID, &ownerDiscord, &ownerDisplay,
			&target.Endpoint.ConnectorType, &target.Endpoint.CanonicalBaseURL, &target.Endpoint.DisplayHead,
			&target.Endpoint.DisplayTail, &discovered, &decided, &target.CreatedAt, &target.UpdatedAt); err != nil {
			return Page[Target]{}, fmt.Errorf("reports: scan target: %w", err)
		}
		if !db.ValidateOpaqueID(target.ID, "rpt_") || sequence < 1 || discovered < 1 || len(keyRef) != 32 {
			clear(keyRef)
			return Page[Target]{}, ErrInvariant
		}
		target.TargetSequence = strconv.FormatInt(sequence, 10)
		target.KeyRef = base64.RawURLEncoding.EncodeToString(keyRef)
		clear(keyRef)
		target.DiscoveredVersion = strconv.FormatInt(discovered, 10)
		if endpointKeyID.Valid {
			value := strconv.FormatInt(endpointKeyID.Int64, 10)
			target.EndpointKeyID = &value
		}
		if ownerUserID.Valid {
			if ownerUserID.Int64 <= 0 || !ownerDiscord.Valid || ownerDiscord.String == "" || !ownerDisplay.Valid {
				return Page[Target]{}, ErrInvariant
			}
			owner := TargetOwner{
				UserID:      strconv.FormatInt(ownerUserID.Int64, 10),
				DiscordID:   ownerDiscord.String,
				DisplayName: ownerDisplay.String,
			}
			target.Owner = &owner
		} else if ownerDiscord.Valid || ownerDisplay.Valid {
			return Page[Target]{}, ErrInvariant
		}
		if decided.Valid {
			value := strconv.FormatInt(decided.Int64, 10)
			target.DecidedVersion = &value
		}
		items = append(items, target)
	}
	if err := rows.Err(); err != nil {
		return Page[Target]{}, fmt.Errorf("reports: list targets: %w", err)
	}
	page := Page[Target]{Data: items}
	if len(items) > limit {
		last := items[limit-1]
		sequence, _ := strconv.ParseInt(last.TargetSequence, 10, 64)
		token, err := repository.cursors.encode(targetCursorScope, caseID, sequence, last.ID, now)
		if err != nil {
			return Page[Target]{}, err
		}
		page.Data = items[:limit]
		page.NextCursor = &token
	}
	if err := commit(tx, committed); err != nil {
		return Page[Target]{}, err
	}
	return page, nil
}

func insertInvariantAlertTx(ctx context.Context, tx *sql.Tx, ref, code string, now int64) error {
	if ctx == nil || tx == nil || ref == "" || code == "" || now < 0 || now > maxUnixSecond {
		return ErrInvariant
	}
	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM admin_alerts
WHERE kind='invariant_violation' AND ref=? AND message=? AND resolved=0)`, ref, code).Scan(&exists); err != nil {
		return fmt.Errorf("reports: inspect invariant alert: %w", err)
	}
	if exists == 1 {
		return nil
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO admin_alerts(kind,message,ref,created_at,resolved)
VALUES('invariant_violation',?,?,?,0)`, code, ref, now); err != nil {
		return fmt.Errorf("reports: write invariant alert: %w", err)
	}
	return nil
}
