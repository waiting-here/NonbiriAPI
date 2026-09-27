package continuity

import (
	"context"
	"database/sql"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/lifecycle"
)

var _ lifecycle.ContinuityLifecycle = (*Service)(nil)

func (service *Service) PrepareDelete(ctx context.Context, tx *sql.Tx, request lifecycle.DeleteRequest) (lifecycle.DeleteFinalizer, error) {
	if !request.Source.Valid() {
		return nil, ErrInvalid
	}
	if err := service.PreserveEligibilityTx(ctx, tx, request.UserID, request.DecisionNow); err != nil {
		return nil, err
	}
	service.mu.RLock()
	owners := append([]WindowPreserver(nil), service.windows...)
	service.mu.RUnlock()
	for _, owner := range owners {
		if err := owner.PreserveWindowTx(ctx, tx, request.UserID, request.DecisionNow); err != nil {
			return nil, err
		}
	}
	return nil, nil
}

func (service *Service) ExportContinuity(ctx context.Context, tx *sql.Tx, request lifecycle.ExportRequest) ([]lifecycle.ContinuityEligibilityExport, error) {
	if request.Limit < 1 || request.Limit > lifecycle.CollectionLimit || request.DecisionNow < 0 || request.DecisionNow > maxUnixSecond {
		return nil, ErrInvalid
	}
	key, err := service.UserKeyTx(ctx, tx, request.UserID)
	if err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT kind,scope,window_key,expires_at FROM identity_continuity_facts WHERE identity_key=? AND kind<>'abuse_state' AND (expires_at IS NULL OR expires_at>?) ORDER BY kind,scope,window_key LIMIT ?`, key[:], request.DecisionNow, request.Limit+1)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []lifecycle.ContinuityEligibilityExport{}
	for rows.Next() {
		var item lifecycle.ContinuityEligibilityExport
		if err := rows.Scan(&item.Kind, &item.Scope, &item.Window, &item.ExpiresAt); err != nil {
			return nil, err
		}
		item.State = "completed"
		result = append(result, item)
		if len(result) > request.Limit {
			return nil, lifecycle.ErrTooLarge
		}
	}
	return result, rows.Err()
}

// Retain removes only expired finite facts and windows in a bounded batch.
// Permanent reward qualifications never enter these predicates.
func (service *Service) Retain(ctx context.Context, now int64, limit int, deadline time.Time) (lifecycle.WorkResult, error) {
	if service == nil || ctx == nil || now < 0 || now > maxUnixSecond || limit < 1 || limit > 1000 || deadline.IsZero() {
		return lifecycle.WorkResult{}, ErrInvalid
	}
	if bound := time.Now().Add(2 * time.Second); deadline.After(bound) {
		deadline = bound
	}
	ctx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	tx, err := service.database.BeginTx(ctx, nil)
	if err != nil {
		return lifecycle.WorkResult{}, err
	}
	defer tx.Rollback()
	processed := 0
	for _, query := range []struct {
		sql string
		at  int64
	}{
		{`DELETE FROM identity_continuity_facts WHERE (identity_key,kind,scope,window_key) IN (SELECT identity_key,kind,scope,window_key FROM identity_continuity_facts WHERE expires_at IS NOT NULL AND expires_at<=? ORDER BY expires_at,identity_key,kind,scope,window_key LIMIT ?)`, now},
		{`DELETE FROM identity_window_events WHERE (identity_key,kind,scope,event_key) IN (SELECT identity_key,kind,scope,event_key FROM identity_window_events WHERE expires_at_ms<=? ORDER BY expires_at_ms,identity_key,kind,scope,event_key LIMIT ?)`, now * 1000},
		{`DELETE FROM self_deletion_duel_aborts WHERE id IN (SELECT id FROM self_deletion_duel_aborts WHERE expires_at<=? ORDER BY expires_at,id LIMIT ?)`, now},
	} {
		if processed == limit {
			break
		}
		result, err := tx.ExecContext(ctx, query.sql, query.at, limit-processed)
		if err != nil {
			return lifecycle.WorkResult{}, err
		}
		count, err := result.RowsAffected()
		if err != nil {
			return lifecycle.WorkResult{}, err
		}
		processed += int(count)
	}
	var more bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM identity_continuity_facts WHERE expires_at IS NOT NULL AND expires_at<=?) OR EXISTS(SELECT 1 FROM identity_window_events WHERE expires_at_ms<=?) OR EXISTS(SELECT 1 FROM self_deletion_duel_aborts WHERE expires_at<=?)`, now, now*1000, now).Scan(&more); err != nil {
		return lifecycle.WorkResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return lifecycle.WorkResult{}, err
	}
	return lifecycle.WorkResult{Processed: processed, More: more}, nil
}
