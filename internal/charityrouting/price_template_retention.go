package charityrouting

import (
	"context"
	"fmt"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/lifecycle"
)

const auditRetentionSeconds int64 = 90 * 24 * 60 * 60

// Retain removes expired template maintenance facts.
func (s *Service) Retain(ctx context.Context, decisionNow int64, limit int, budgetDeadline time.Time) (lifecycle.WorkResult, error) {
	if s == nil || s.db == nil || ctx == nil || decisionNow < 0 || decisionNow > 253402300799 || limit < 1 || limit > lifecycle.WorkerBatchLimit {
		return lifecycle.WorkResult{}, ErrInvalidRequest
	}
	if decisionNow < auditRetentionSeconds {
		return lifecycle.WorkResult{}, nil
	}
	workCtx := ctx
	var cancel context.CancelFunc
	if !budgetDeadline.IsZero() {
		workCtx, cancel = context.WithDeadline(ctx, budgetDeadline)
		defer cancel()
	}
	tx, err := s.db.BeginTx(workCtx, nil)
	if err != nil {
		return lifecycle.WorkResult{}, fmt.Errorf("charity routing: begin audit retention: %w", err)
	}
	defer tx.Rollback()
	cutoff := decisionNow - auditRetentionSeconds
	result, err := tx.ExecContext(workCtx, `DELETE FROM charity_price_template_audits WHERE id IN (
 SELECT id FROM charity_price_template_audits
 WHERE created_at<=?
 ORDER BY created_at,id LIMIT ?)`, cutoff, limit)
	if err != nil {
		return lifecycle.WorkResult{}, fmt.Errorf("charity routing: retain audits: %w", err)
	}
	processed, err := result.RowsAffected()
	if err != nil {
		return lifecycle.WorkResult{}, ErrUnavailable
	}
	var more bool
	if err := tx.QueryRowContext(workCtx, `SELECT EXISTS(SELECT 1 FROM charity_price_template_audits
 WHERE created_at<=? LIMIT 1)`, cutoff).Scan(&more); err != nil {
		return lifecycle.WorkResult{}, fmt.Errorf("charity routing: inspect remaining audit retention: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return lifecycle.WorkResult{}, fmt.Errorf("charity routing: commit audit retention: %w", err)
	}
	return lifecycle.WorkResult{Processed: int(processed), More: more}, nil
}
