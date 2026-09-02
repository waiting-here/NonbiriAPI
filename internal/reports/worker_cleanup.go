package reports

import (
	"context"
	"fmt"
	"time"
)

func (repository *Repository) cleanupReportData(ctx context.Context, now int64) error {
	batchCtx, cancel := repository.boundedWorkerContext(ctx)
	defer cancel()
	_, err := repository.cleanupReportDataBatch(batchCtx, now, workerBatchLimit)
	return err
}

// RetainLifecycle performs one report-owned retention transaction using the
// coordinator's frozen time, row budget, and deadline. It keeps report SQL and
// aggregate ordering inside this package.
func (repository *Repository) RetainLifecycle(
	ctx context.Context,
	decisionNow int64,
	limit int,
	budgetDeadline time.Time,
) (LifecycleWorkResult, error) {
	if err := repository.admit(); err != nil {
		return LifecycleWorkResult{}, err
	}
	defer repository.release()
	if ctx == nil || ctx.Err() != nil || decisionNow < 0 ||
		decisionNow > maxUnixSecond-caseRetentionSeconds || limit < 1 ||
		limit > workerBatchLimit || budgetDeadline.IsZero() {
		return LifecycleWorkResult{}, ErrInvalidRequest
	}
	batchCtx, cancel := context.WithDeadline(ctx, budgetDeadline)
	defer cancel()
	processed, err := repository.cleanupReportDataBatch(batchCtx, decisionNow, limit)
	if err != nil {
		return LifecycleWorkResult{}, err
	}
	return LifecycleWorkResult{Processed: processed, More: processed == limit}, nil
}

func (repository *Repository) cleanupReportDataBatch(ctx context.Context, now int64, limit int) (int, error) {
	tx, err := repository.beginTx(ctx)
	if err != nil {
		return 0, err
	}
	committed := false
	defer rollbackUnlessCommitted(tx, &committed)
	processed := 0
	deleteBatch := func(label, query string, args ...any) error {
		remaining := limit - processed
		if remaining == 0 {
			return nil
		}
		args = append(args, remaining)
		result, err := tx.ExecContext(ctx, query, args...)
		if err != nil {
			return fmt.Errorf("reports: %s: %w", label, err)
		}
		changed, err := result.RowsAffected()
		if err != nil || changed < 0 || changed > int64(remaining) {
			return ErrInvariant
		}
		processed += int(changed)
		return nil
	}
	if err := deleteBatch("clean acceptance replay", `DELETE FROM idempotency_records WHERE rowid IN (
SELECT rowid FROM idempotency_records WHERE scope='credential_report' AND expires_at<=?
ORDER BY expires_at,rowid LIMIT ?)`, now); err != nil {
		return 0, err
	}
	if err := deleteBatch("clean report rate buckets", `DELETE FROM report_rate_buckets WHERE rowid IN (
SELECT rowid FROM report_rate_buckets WHERE expires_at<=? ORDER BY expires_at,scope LIMIT ?)`, now); err != nil {
		return 0, err
	}
	if err := deleteBatch("clean retained report operations", `DELETE FROM accepted_operations WHERE id IN (
SELECT o.id FROM accepted_operations o JOIN report_cases c ON c.id=o.checkpoint
WHERE o.kind IN ('report_indexing','report_approved_processing')
 AND c.status IN ('approved','rejected','expired') AND c.created_at+?<=?
ORDER BY c.created_at,c.id,o.id LIMIT ?)`, caseRetentionSeconds, now); err != nil {
		return 0, err
	}
	if err := deleteBatch("clear expired report fingerprints", `UPDATE donation_keys SET report_fingerprint=NULL,updated_at=?
WHERE id IN (SELECT id FROM donation_keys
WHERE report_fingerprint IS NOT NULL AND report_match_until IS NOT NULL AND report_match_until<=?
ORDER BY report_match_until,id LIMIT ?)`, now, now); err != nil {
		return 0, err
	}
	if err := deleteBatch("clean retained report cases", `DELETE FROM report_cases WHERE id IN (
SELECT c.id FROM report_cases c
WHERE c.status IN ('approved','rejected','expired') AND c.created_at+?<=?
 AND NOT EXISTS(SELECT 1 FROM legal_holds h
  WHERE h.object_kind='report_case' AND h.object_ref=c.id AND h.state='active')
ORDER BY c.created_at,c.id LIMIT ?)`, caseRetentionSeconds, now); err != nil {
		return 0, err
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if err := commit(tx, &committed); err != nil {
		return 0, err
	}
	return processed, nil
}
