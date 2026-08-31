package issues

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strconv"
)

func (adapter *SourceAdapter) PrepareEndpointDeletion(ctx context.Context, tx *sql.Tx, ownerUserID, endpointID, decisionNow int64) error {
	if adapter == nil || adapter.repository == nil || tx == nil || ownerUserID <= 0 || endpointID <= 0 || !validDecisionTime(decisionNow) {
		return ErrInvalidRequest
	}
	if _, err := readOwnerProjection(ctx, tx, ownerUserID, ResourceEndpoint, endpointID); err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, `SELECT k.id FROM endpoint_keys k JOIN endpoints e ON e.id=k.endpoint_id WHERE e.id=? AND e.user_id=? ORDER BY k.id`, endpointID, ownerUserID)
	if err != nil {
		return fmt.Errorf("issues: list endpoint keys before deletion: %w", err)
	}
	keyIDs := make([]int64, 0, 8)
	for rows.Next() {
		var keyID int64
		if err := rows.Scan(&keyID); err != nil {
			_ = rows.Close()
			return fmt.Errorf("issues: scan endpoint key before deletion: %w", err)
		}
		keyIDs = append(keyIDs, keyID)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("issues: close endpoint key rows: %w", err)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("issues: list endpoint keys before deletion: %w", err)
	}
	if err := adapter.repository.scrubResourceTx(ctx, tx, ownerUserID, ResourceEndpoint, endpointID, decisionNow); err != nil {
		return err
	}
	for _, keyID := range keyIDs {
		if err := adapter.repository.scrubResourceTx(ctx, tx, ownerUserID, ResourceEndpointKey, keyID, decisionNow); err != nil {
			return err
		}
	}
	return trimClosedTx(ctx, tx, ownerUserID)
}

// PrepareEndpointKeyDeletion is a transaction-local hook suitable for both
// owner deletion and approved report deletion. It must run before the key row
// is physically deleted so ownership can be revalidated.
func (adapter *SourceAdapter) PrepareEndpointKeyDeletion(ctx context.Context, tx *sql.Tx, ownerUserID int64, keyIDs []int64, decisionNow int64) error {
	if adapter == nil || adapter.repository == nil || tx == nil || ownerUserID <= 0 || len(keyIDs) == 0 || !validDecisionTime(decisionNow) {
		return ErrInvalidRequest
	}
	unique := make(map[int64]struct{}, len(keyIDs))
	for _, keyID := range keyIDs {
		if keyID <= 0 {
			return ErrInvalidRequest
		}
		unique[keyID] = struct{}{}
	}
	ordered := make([]int64, 0, len(unique))
	for keyID := range unique {
		ordered = append(ordered, keyID)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i] < ordered[j] })
	for _, keyID := range ordered {
		if _, err := readOwnerProjection(ctx, tx, ownerUserID, ResourceEndpointKey, keyID); err != nil {
			return err
		}
		if err := adapter.repository.scrubResourceTx(ctx, tx, ownerUserID, ResourceEndpointKey, keyID, decisionNow); err != nil {
			return err
		}
	}
	return trimClosedTx(ctx, tx, ownerUserID)
}

func (adapter *SourceAdapter) PrepareApprovedEndpointKeyDeletion(ctx context.Context, tx *sql.Tx, ownerUserID, keyID, decisionNow int64) error {
	return adapter.PrepareEndpointKeyDeletion(ctx, tx, ownerUserID, []int64{keyID}, decisionNow)
}

func (adapter *SourceAdapter) PrepareModelDeletion(ctx context.Context, tx *sql.Tx, ownerUserID, modelID, decisionNow int64) error {
	if adapter == nil || adapter.repository == nil || tx == nil || ownerUserID <= 0 || modelID <= 0 || !validDecisionTime(decisionNow) {
		return ErrInvalidRequest
	}
	if _, err := readOwnerProjection(ctx, tx, ownerUserID, ResourceModel, modelID); err != nil {
		return err
	}
	if err := adapter.repository.scrubResourceTx(ctx, tx, ownerUserID, ResourceModel, modelID, decisionNow); err != nil {
		return err
	}
	return trimClosedTx(ctx, tx, ownerUserID)
}

// AffectedModelsForEndpointKeys returns a frozen owner-scoped list for the
// caller to reconcile after its same-transaction key deletion has cascaded.
func (adapter *SourceAdapter) AffectedModelsForEndpointKeys(ctx context.Context, tx *sql.Tx, ownerUserID int64, keyIDs []int64) ([]int64, error) {
	if adapter == nil || adapter.repository == nil || tx == nil || ownerUserID <= 0 || len(keyIDs) == 0 {
		return nil, ErrInvalidRequest
	}
	for _, keyID := range keyIDs {
		if _, err := readOwnerProjection(ctx, tx, ownerUserID, ResourceEndpointKey, keyID); err != nil {
			return nil, err
		}
	}
	return modelsUsingKeys(ctx, tx, ownerUserID, keyIDs)
}

func (adapter *SourceAdapter) PrepareAccountDeletion(ctx context.Context, tx *sql.Tx, userID, decisionNow int64) error {
	if adapter == nil || adapter.repository == nil || tx == nil || userID <= 0 || !validDecisionTime(decisionNow) {
		return ErrInvalidRequest
	}
	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE id=? AND is_admin=0)`, userID).Scan(&exists); err != nil {
		return fmt.Errorf("issues: read account before deletion: %w", err)
	}
	if exists != 1 {
		return ErrNotFound
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM user_issues WHERE user_id=?`, userID); err != nil {
		return fmt.Errorf("issues: delete account issue projection: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM user_issue_projection_state WHERE user_id=?`, userID); err != nil {
		return fmt.Errorf("issues: delete account issue checkpoint: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE admin_alerts SET subject_user_id=NULL,resolved=1,resolved_at=COALESCE(resolved_at,?)
WHERE kind='issue_projection_incomplete' AND subject_user_id=?`, decisionNow, userID); err != nil {
		return fmt.Errorf("issues: scrub account projection alert: %w", err)
	}
	return nil
}

func (repository *Repository) scrubResourceTx(ctx context.Context, tx *sql.Tx, userID int64, kind ResourceKind, resourceID, now int64) error {
	resourceRef := strconv.FormatInt(resourceID, 10)
	rows, err := tx.QueryContext(ctx, `
SELECT id FROM user_issues WHERE user_id=? AND resource_kind=? AND resource_ref=? ORDER BY id`, userID, kind, resourceRef)
	if err != nil {
		return fmt.Errorf("issues: list resource projections for scrub: %w", err)
	}
	ids := make([]string, 0, 8)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return fmt.Errorf("issues: scan resource projection for scrub: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("issues: close resource scrub rows: %w", err)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("issues: list resource projections for scrub: %w", err)
	}
	for _, id := range ids {
		scrubbed, err := repository.scrubReference(id)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `
UPDATE user_issues SET
 state='closed',
 closed_at=CASE WHEN state='current' THEN ? ELSE closed_at END,
 retain_until=CASE WHEN state='current' THEN ? ELSE retain_until END,
 resource_ref=?,safe_detail='',deep_link_kind=NULL,deep_link_ref=NULL
WHERE id=? AND user_id=?`, now, now+closedRetention, scrubbed, id, userID)
		if err != nil {
			return fmt.Errorf("issues: scrub deleted resource projection: %w", err)
		}
	}
	return nil
}

func validDecisionTime(value int64) bool {
	return value >= 0 && value <= maxUnixSecond-closedRetention
}
