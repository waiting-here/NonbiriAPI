package resources

import (
	"context"
	"time"
)

func (r *Repository) RecoverStaleDiscoveriesAt(ctx context.Context, decisionNow int64, limit int, budgetDeadline time.Time) (DiscoveryRecoveryResult, error) {
	if r == nil || r.db == nil || ctx == nil || decisionNow < 0 || decisionNow > maxUnixSecond || limit < 1 || limit > maxDiscoveryRecoveryBatch {
		return DiscoveryRecoveryResult{}, ErrInvalidRequest
	}
	ctx, cancel := discoveryRecoveryContext(ctx, budgetDeadline)
	defer cancel()
	tx, err := beginTx(ctx, r.db)
	if err != nil {
		return DiscoveryRecoveryResult{}, err
	}
	committed := false
	defer finishTx(tx, &committed)
	threshold := decisionNow - staleDiscoverySecond
	rows, err := tx.QueryContext(ctx, `SELECT operation_id FROM discovery_runs WHERE state='checking' AND started_at<=? ORDER BY started_at,id LIMIT ?`, threshold, limit)
	if err != nil {
		return DiscoveryRecoveryResult{}, err
	}
	operations := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return DiscoveryRecoveryResult{}, err
		}
		operations = append(operations, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return DiscoveryRecoveryResult{}, err
	}
	if err := rows.Close(); err != nil {
		return DiscoveryRecoveryResult{}, err
	}
	processed := 0
	for _, id := range operations {
		run, err := loadDiscoveryRunTx(ctx, tx, id)
		if err != nil {
			return DiscoveryRecoveryResult{}, err
		}
		if err := r.finishDiscoveryRunTx(ctx, tx, run, DiscoveryClaimResult{FailureClass: DiscoveryFailureInterrupted}, decisionNow); err != nil {
			return DiscoveryRecoveryResult{}, err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE accepted_operations SET state='completed',checkpoint='',last_error_class=NULL,terminal_at=? WHERE id=? AND kind='model_discovery' AND state IN ('accepted','running')`, decisionNow, id); err != nil {
			return DiscoveryRecoveryResult{}, err
		}
		processed++
	}
	if remaining := limit - processed; remaining > 0 {
		rows, err := tx.QueryContext(ctx, `SELECT op.id FROM accepted_operations op WHERE op.kind='model_discovery' AND op.state IN ('accepted','running') AND op.created_at<=?
 AND NOT EXISTS(SELECT 1 FROM discovery_runs run WHERE run.operation_id=op.id AND run.state='checking') ORDER BY op.created_at,op.id LIMIT ?`, threshold, remaining)
		if err != nil {
			return DiscoveryRecoveryResult{}, err
		}
		ids := []string{}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return DiscoveryRecoveryResult{}, err
			}
			ids = append(ids, id)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return DiscoveryRecoveryResult{}, err
		}
		if err := rows.Close(); err != nil {
			return DiscoveryRecoveryResult{}, err
		}
		for _, id := range ids {
			result, err := tx.ExecContext(ctx, `UPDATE accepted_operations SET state='completed',checkpoint='',last_error_class=NULL,terminal_at=? WHERE id=? AND kind='model_discovery' AND state IN ('accepted','running') AND created_at<=?
 AND NOT EXISTS(SELECT 1 FROM discovery_runs run WHERE run.operation_id=accepted_operations.id AND run.state='checking')`, decisionNow, id, threshold)
			if err != nil {
				return DiscoveryRecoveryResult{}, err
			}
			changed, err := result.RowsAffected()
			if err != nil || changed < 0 || changed > 1 {
				return DiscoveryRecoveryResult{}, ErrUnavailable
			}
			processed += int(changed)
		}
	}
	var more bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM discovery_runs WHERE state='checking' AND started_at<=?)
 OR EXISTS(SELECT 1 FROM accepted_operations op WHERE op.kind='model_discovery' AND op.state IN ('accepted','running') AND op.created_at<=?
 AND NOT EXISTS(SELECT 1 FROM discovery_runs run WHERE run.operation_id=op.id AND run.state='checking'))`, threshold, threshold).Scan(&more); err != nil {
		return DiscoveryRecoveryResult{}, err
	}
	if err := commitTx(tx, &committed); err != nil {
		return DiscoveryRecoveryResult{}, err
	}
	return DiscoveryRecoveryResult{Processed: processed, More: more}, nil
}
