package resources

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strconv"
)

// EnsureManualEntryInTransaction preserves an existing entry's metadata and
// inserts an absent model with an empty note. The caller owns the transaction.
func (r *Repository) EnsureManualEntryInTransaction(ctx context.Context, tx *sql.Tx, userID, endpointID, keyID int64, modelID string) error {
	if r == nil || tx == nil || !validateCatalogText(modelID, 1, 512) {
		return ErrInvalidRequest
	}
	if err := r.finalAuth.AuthorizeUserMutation(ctx, tx, userID); err != nil {
		return err
	}
	if _, err := discoveryOwnerTx(ctx, tx, userID, endpointID, keyID); err != nil {
		return err
	}
	locked, err := endpointKeyLockedTx(ctx, tx, keyID)
	if err != nil {
		return err
	}
	if locked {
		return ErrResourceLocked
	}
	var exists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM model_catalog_entries WHERE endpoint_key_id=? AND source_type='manual' AND normalized_model_id=?)`, keyID, modelID).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return nil
	}
	now, err := r.nowUnix()
	if err != nil {
		return err
	}
	before, present, err := readPairStateTx(ctx, tx, keyID, modelID)
	if err != nil {
		return err
	}
	revision, err := addManualSupportTx(ctx, tx, keyID, modelID, now)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO model_catalog_entries(endpoint_key_id,source_type,source_identity,normalized_model_id,provider,source_revision,created_at,updated_at)
VALUES(?,'manual',?,?,'',?,?,?)`, keyID, modelID, modelID, revision, now, now); err != nil {
		return conflictOrError("ensure manual catalog entry", err)
	}
	if present && before.eligible() {
		return nil
	}
	modelIDs, err := modelsUsingKeyPairsTx(ctx, tx, userID, keyID, []string{modelID})
	if err != nil {
		return err
	}
	return r.reconcileRoutingModelIDsTx(ctx, tx, userID, modelIDs)
}

// RefreshDiscoveryAndWait uses the shared discovery worker and claim rail.
// Cancellation stops upstream work; only bounded evidence cleanup can finish
// after this call returns. The returned revision identifies this discovery.
func (r *Repository) RefreshDiscoveryAndWait(ctx context.Context, userID, endpointID, keyID int64) (int64, error) {
	if r == nil {
		return 0, ErrUnavailable
	}
	key, err := r.operationID()
	if err != nil {
		return 0, ErrUnavailable
	}
	out, err := r.refreshDiscovery(ctx, userID, endpointID, keyID, ControlMutation{
		IdempotencyKey: key, Method: http.MethodPost, Route: routeDiscovery,
		PathIDs: []string{strconv.FormatInt(endpointID, 10), strconv.FormatInt(keyID, 10)}, CanonicalBody: []byte("{}"),
	}, true)
	if errors.Is(err, errDiscoveryWorkerUnavailable) {
		return 0, ErrUnavailable
	}
	if err != nil {
		return 0, err
	}
	revision, err := strconv.ParseInt(out.Value.Evidence.Revision, 10, 64)
	if err != nil || revision < 1 {
		return 0, errors.New("resources: invalid completed discovery revision")
	}
	return revision, nil
}
