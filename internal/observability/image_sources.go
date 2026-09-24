package observability

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/waiting-here/NonbiriAPI/internal/db"
)

// RecordImageSourceTx records only the authenticated submission's bounded
// ingress facts; background polling never creates another user observation.
func (r *Repository) RecordImageSourceTx(ctx context.Context, tx *sql.Tx, taskID string, userID, at int64) error {
	source, ok := SourceFromContext(ctx)
	if !ok {
		return nil
	}
	if tx == nil || !db.ValidateOpaqueID(taskID, "img_") || userID <= 0 || at < 0 || at > 253402300799 {
		return ErrInvalid
	}
	encoded, err := json.Marshal(source)
	if err != nil {
		return ErrInvalid
	}
	if _, err = ParseSource(encoded); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO image_task_sources(task_id,user_id,effective_ip,ip_quality,source_json,occurred_at)
 SELECT id,user_id,?,?,?,? FROM image_activity_tasks WHERE id=? AND user_id=? AND finance_state='reserved'
 ON CONFLICT(task_id) DO NOTHING`, source.EffectiveIP, source.IPQuality, string(encoded), at, taskID, userID)
	if err != nil {
		return err
	}
	if n, err := result.RowsAffected(); err != nil || n != 1 {
		return ErrInvalid
	}
	return nil
}

// DeleteImageSourcesTx runs before task anonymization in the account-deletion
// transaction so delayed callbacks cannot retain identifiable diagnostic roots.
func (r *Repository) DeleteImageSourcesTx(ctx context.Context, tx *sql.Tx, userID int64) error {
	if tx == nil || userID <= 0 {
		return ErrInvalid
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM request_error_bodies WHERE task_id IN (SELECT id FROM image_activity_tasks WHERE user_id=?)`, userID); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `DELETE FROM image_task_sources WHERE user_id=?`, userID)
	return err
}
