package resources

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/waiting-here/NonbiriAPI/internal/db"
)

const (
	routeCatalog              = "/api/endpoints/{id}/keys/{keyId}/models"
	routeDiscovery            = "/api/endpoints/{id}/keys/{keyId}/models/refresh"
	routeManualCatalog        = "/api/endpoints/{id}/keys/{keyId}/models/manual"
	routeManualEntry          = "/api/endpoints/{id}/keys/{keyId}/models/manual/{entryId}"
	discoveryCleanupTimeout   = 5 * time.Second
	maxDiscoveryRecoveryBatch = 100
)

type discoveryRow struct {
	endpointID, keyID                       int64
	connectorType, baseURL                  string
	state                                   string
	revision                                int64
	operationHash                           []byte
	safeClass, safeDiag                     string
	startedAt, completedAt                  sql.NullInt64
	fetchedCount                            int64
	lastSuccessAt, addedCount, removedCount sql.NullInt64
}

type discoveryJob struct {
	input    DiscoveryClaimInput
	revision int64
}

func (row discoveryRow) evidence() (DiscoveryEvidence, error) {
	if row.revision < 1 || row.fetchedCount < 0 {
		return DiscoveryEvidence{}, ErrUnavailable
	}
	evidence := DiscoveryEvidence{
		State: row.state, Revision: strconv.FormatInt(row.revision, 10),
		SafeClass: row.safeClass,
	}
	if row.lastSuccessAt.Valid {
		value := row.lastSuccessAt.Int64
		evidence.LastSuccessAt = &value
	}
	if row.state == "succeeded" && row.addedCount.Valid && row.removedCount.Valid {
		added, removed := strconv.FormatInt(row.addedCount.Int64, 10), strconv.FormatInt(row.removedCount.Int64, 10)
		evidence.AddedCount, evidence.RemovedCount = &added, &removed
	}
	switch row.state {
	case "unknown":
		if row.operationHash != nil || row.safeClass != "none" || row.startedAt.Valid || row.completedAt.Valid || row.fetchedCount != 0 {
			return DiscoveryEvidence{}, ErrUnavailable
		}
	case "checking":
		if len(row.operationHash) != sha256.Size || row.safeClass != "none" || !row.startedAt.Valid || row.completedAt.Valid {
			return DiscoveryEvidence{}, ErrUnavailable
		}
		observed := row.startedAt.Int64
		evidence.ObservedAt = &observed
	case "succeeded":
		if row.operationHash != nil || row.safeClass != "none" || !row.completedAt.Valid {
			return DiscoveryEvidence{}, ErrUnavailable
		}
		result := "nonempty"
		if row.fetchedCount == 0 {
			result = "empty"
		}
		count := strconv.FormatInt(row.fetchedCount, 10)
		observed := row.completedAt.Int64
		evidence.Result, evidence.Count, evidence.ObservedAt = &result, &count, &observed
	case "failed":
		if row.operationHash != nil || !validFailureClass(DiscoveryFailureClass(row.safeClass)) || !row.completedAt.Valid {
			return DiscoveryEvidence{}, ErrUnavailable
		}
		observed := row.completedAt.Int64
		evidence.ObservedAt = &observed
	default:
		return DiscoveryEvidence{}, ErrUnavailable
	}
	return evidence, nil
}

func scanDiscovery(scanner interface{ Scan(...any) error }) (discoveryRow, error) {
	var row discoveryRow
	err := scanner.Scan(&row.endpointID, &row.keyID, &row.connectorType, &row.baseURL,
		&row.state, &row.revision, &row.operationHash, &row.safeClass, &row.safeDiag,
		&row.startedAt, &row.completedAt, &row.fetchedCount, &row.lastSuccessAt, &row.addedCount, &row.removedCount)
	return row, err
}

func discoveryOwnerTx(ctx context.Context, tx *sql.Tx, userID, endpointID, keyID int64) (discoveryRow, error) {
	row, err := scanDiscovery(tx.QueryRowContext(ctx, `
SELECT e.id,k.id,e.connector_type,e.base_url,
       d.state,d.revision,d.operation_hash,d.safe_class,d.safe_diag,d.started_at,d.completed_at,d.fetched_count,
       success.completed_at,changes.added_count,changes.removed_count
FROM endpoints e
JOIN endpoint_keys k ON k.endpoint_id=e.id
JOIN model_discovery_evidence d ON d.endpoint_key_id=k.id
LEFT JOIN endpoint_key_discovery_sources ks ON ks.endpoint_key_id=k.id
LEFT JOIN discovery_source_states st ON st.source_revision_id=ks.source_revision_id
LEFT JOIN discovery_runs success ON success.id=st.last_successful_run_id
LEFT JOIN discovery_run_changes changes ON changes.run_id=st.current_run_id
WHERE e.user_id=? AND e.id=? AND k.id=?`, userID, endpointID, keyID))
	if errors.Is(err, sql.ErrNoRows) {
		return discoveryRow{}, ErrNotFound
	}
	if err != nil {
		return discoveryRow{}, fmt.Errorf("resources: read discovery evidence: %w", err)
	}
	return row, nil
}

func (r *Repository) GetCatalog(ctx context.Context, userID, endpointID, keyID int64, limit int, cursor string) (CatalogView, error) {
	limit = normalizePageLimit(limit)
	if r == nil || userID <= 0 || endpointID <= 0 || keyID <= 0 || !validPageLimit(limit) {
		return CatalogView{}, ErrInvalidRequest
	}
	tx, err := beginTx(ctx, r.db)
	if err != nil {
		return CatalogView{}, err
	}
	defer tx.Rollback()
	owner, err := discoveryOwnerTx(ctx, tx, userID, endpointID, keyID)
	if err != nil {
		return CatalogView{}, err
	}
	evidence, err := owner.evidence()
	if err != nil {
		return CatalogView{}, err
	}
	now, err := r.nowUnix()
	if err != nil {
		return CatalogView{}, err
	}
	var afterID uint64
	if cursor != "" {
		cursorOwner := catalogCursorOwner(userID, endpointID, keyID)
		atoms, err := r.cursors.decode(cursor, "endpoint-catalog", cursorOwner, uint64(now), db.CursorUint)
		if err != nil {
			return CatalogView{}, err
		}
		afterID = atoms[0].Uint
		if afterID == 0 || afterID > uint64(^uint64(0)>>1) {
			return CatalogView{}, ErrInvalidRequest
		}
	}
	rows, err := tx.QueryContext(ctx, `
SELECT c.id,c.source_type,c.normalized_model_id,c.provider,c.source_revision,p.pair_revision,c.created_at,c.updated_at
FROM endpoint_key_catalog_entries c
JOIN model_pair_catalog p ON p.endpoint_key_id=c.endpoint_key_id AND p.normalized_model_id=c.normalized_model_id
WHERE c.endpoint_key_id=? AND (?=0 OR c.id>?)
ORDER BY c.id LIMIT ?`, keyID, afterID, afterID, limit+1)
	if err != nil {
		return CatalogView{}, fmt.Errorf("resources: list catalog: %w", err)
	}
	defer rows.Close()
	all := make([]CatalogEntry, 0, limit+1)
	for rows.Next() {
		entry, err := scanCatalogEntry(rows)
		if err != nil {
			return CatalogView{}, fmt.Errorf("resources: scan catalog: %w", err)
		}
		all = append(all, entry)
	}
	if err := rows.Err(); err != nil {
		return CatalogView{}, fmt.Errorf("resources: list catalog: %w", err)
	}
	view := CatalogView{Evidence: evidence, AutomaticEntries: []CatalogEntry{}, ManualEntries: []CatalogEntry{}}
	visible := all
	if len(all) > limit {
		visible = all[:limit]
		lastID, _ := parseDecimalID(visible[len(visible)-1].ID)
		next, err := r.cursors.encode("endpoint-catalog", catalogCursorOwner(userID, endpointID, keyID), uint64(now+cursorLifetime), []db.CursorAtom{{Kind: db.CursorUint, Uint: uint64(lastID)}})
		if err != nil {
			return CatalogView{}, err
		}
		view.NextCursor = &next
	}
	for _, entry := range visible {
		if entry.SourceType == "automatic" {
			view.AutomaticEntries = append(view.AutomaticEntries, entry)
		} else {
			view.ManualEntries = append(view.ManualEntries, entry)
		}
	}
	return view, nil
}

func catalogCursorOwner(userID, endpointID, keyID int64) string {
	return strconv.FormatInt(userID, 10) + ":" + strconv.FormatInt(endpointID, 10) + ":" + strconv.FormatInt(keyID, 10)
}

func (r *Repository) RefreshDiscovery(ctx context.Context, userID, endpointID, keyID int64, mutation ControlMutation) (MutationResult[DiscoveryAccepted], error) {
	return r.refreshDiscovery(ctx, userID, endpointID, keyID, mutation, false)
}

func (r *Repository) refreshDiscovery(ctx context.Context, userID, endpointID, keyID int64, mutation ControlMutation, wait bool) (MutationResult[DiscoveryAccepted], error) {
	if r == nil || userID <= 0 || endpointID <= 0 || keyID <= 0 || mutation.Route != routeDiscovery || mutation.Method != http.MethodPost || !mutationPathIDs(mutation, endpointID, keyID) || mutation.Query != "" {
		return MutationResult[DiscoveryAccepted]{}, ErrInvalidRequest
	}
	return r.startDiscovery(ctx, userID, endpointID, keyID, mutation, wait, nil)
}

func (r *Repository) runDiscovery(ctx context.Context, job discoveryJob) error {
	markContext, cancelMark := context.WithTimeout(context.Background(), discoveryCleanupTimeout)
	markErr := r.markDiscoveryRunning(markContext, job.input.OperationID)
	cancelMark()

	claimResult := DiscoveryClaimResult{FailureClass: DiscoveryFailureInterrupted}
	if markErr == nil && job.input.Authorize != nil {
		check, err := beginTx(ctx, r.db)
		if err == nil {
			err = job.input.Authorize(ctx, check)
			_ = check.Rollback()
		}
		markErr = err
	}
	if markErr == nil && ctx.Err() == nil {
		result, claimErr := r.discoveryRail.Discover(ctx, job.input)
		if claimErr == nil && ctx.Err() == nil {
			if validDiscoveryOutcome(result) {
				claimResult = result
			} else {
				// The rail is fed by an untrusted upstream. An invalid typed
				// projection is a protocol failure, not a reason to leave an
				// accepted operation checking until stale recovery.
				claimResult = DiscoveryClaimResult{FailureClass: DiscoveryFailureProtocol}
			}
		}
	}
	completionContext, cancelCompletion := context.WithTimeout(context.Background(), discoveryCleanupTimeout)
	defer cancelCompletion()
	// No network retry is attempted; stale recovery fails closed.
	return r.completeDiscoveryGuarded(completionContext, job.input.EndpointKeyID, job.input.OperationID, job.revision, claimResult, job.input.Authorize)
}

func (r *Repository) markDiscoveryRunning(ctx context.Context, operationID string) error {
	if r == nil || !db.ValidateOpaqueID(operationID, "op_") {
		return ErrInvalidRequest
	}
	result, err := r.db.ExecContext(ctx, `
UPDATE accepted_operations SET state='running'
WHERE id=? AND kind='model_discovery' AND state='accepted'`, operationID)
	if err != nil {
		return fmt.Errorf("resources: start discovery operation: %w", err)
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("resources: observe discovery operation start: %w", err)
	}
	if updated != 1 {
		return ErrConflict
	}
	return nil
}

func (r *Repository) completeDiscovery(ctx context.Context, keyID int64, operationID string, revision int64, outcome DiscoveryClaimResult) error {
	return r.completeDiscoveryGuarded(ctx, keyID, operationID, revision, outcome, nil)
}

func validDiscoveryOutcome(outcome DiscoveryClaimResult) bool {
	if outcome.Succeeded {
		if outcome.FailureClass != "" || outcome.SafeDiagnostic != "" || len(outcome.Models) > maxDiscoveryRows {
			return false
		}
		counts := make(map[string]int)
		for _, model := range outcome.Models {
			if !validateCatalogText(model.UpstreamModelID, 1, 512) || !validateCatalogText(model.Provider, 0, 128) {
				return false
			}
			counts[model.UpstreamModelID]++
			if counts[model.UpstreamModelID] > maxCatalogPairRows {
				return false
			}
		}
		return true
	}
	return len(outcome.Models) == 0 && validFailureClass(outcome.FailureClass) && validSafeDiagnostic(outcome.SafeDiagnostic)
}

func validFailureClass(value DiscoveryFailureClass) bool {
	switch value {
	case DiscoveryFailureAuth, DiscoveryFailureRateLimit, DiscoveryFailureTimeout,
		DiscoveryFailureProtocol, DiscoveryFailureTransport, DiscoveryFailureInterrupted, DiscoveryFailureRegion, DiscoveryFailureUnavailable:
		return true
	default:
		return false
	}
}

func validSafeDiagnostic(value string) bool {
	if !utf8.ValidString(value) || len(value) > 4096 {
		return false
	}
	for _, runeValue := range value {
		if unicode.IsControl(runeValue) || runeValue == 0x7f {
			return false
		}
	}
	return true
}

type DiscoveryRecoveryResult struct {
	Processed int
	More      bool
}

// RecoverStaleDiscoveries retains the startup compatibility entrypoint while
// delegating to the bounded lifecycle seam with one frozen decision time.
func (r *Repository) RecoverStaleDiscoveries(ctx context.Context) (int64, error) {
	if r == nil || ctx == nil {
		return 0, ErrUnavailable
	}
	decisionNow, err := r.nowUnix()
	if err != nil {
		return 0, err
	}
	var total int64
	for {
		result, err := r.RecoverStaleDiscoveriesAt(
			ctx, decisionNow, maxDiscoveryRecoveryBatch, time.Time{},
		)
		if err != nil {
			return total, err
		}
		total += int64(result.Processed)
		if !result.More {
			return total, nil
		}
		if result.Processed == 0 {
			return total, ErrUnavailable
		}
	}
}

// RecoverStaleDiscoveriesAt terminalizes at most limit stale discovery roots
// and orphaned accepted operations. It never retries network discovery. A
// zero budget deadline is reserved for the compatibility wrapper; lifecycle
// always supplies an explicit deadline.
func discoveryRecoveryContext(ctx context.Context, deadline time.Time) (context.Context, context.CancelFunc) {
	if deadline.IsZero() {
		return ctx, func() {}
	}
	return context.WithDeadline(ctx, deadline)
}

func validateCatalogText(value string, minRunes, maxRunes int) bool {
	return validateExactText(value, minRunes, maxRunes)
}
