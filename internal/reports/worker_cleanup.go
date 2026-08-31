package reports

import (
	"context"
	"fmt"
)

func (repository *Repository) cleanupReportData(ctx context.Context, now int64) error {
	batchCtx, cancel := repository.boundedWorkerContext(ctx)
	defer cancel()
	tx, err := repository.beginTx(batchCtx)
	if err != nil {
		return err
	}
	committed := false
	defer rollbackUnlessCommitted(tx, &committed)
	if _, err := tx.ExecContext(batchCtx, `DELETE FROM idempotency_records WHERE rowid IN (
SELECT rowid FROM idempotency_records WHERE scope='credential_report' AND expires_at<=?
ORDER BY expires_at,rowid LIMIT 100)`, now); err != nil {
		return fmt.Errorf("reports: clean acceptance replay: %w", err)
	}
	if _, err := tx.ExecContext(batchCtx, `DELETE FROM report_rate_buckets WHERE rowid IN (
SELECT rowid FROM report_rate_buckets WHERE expires_at<=? ORDER BY expires_at,scope LIMIT 100)`, now); err != nil {
		return fmt.Errorf("reports: clean report rate buckets: %w", err)
	}
	if _, err := tx.ExecContext(batchCtx, `DELETE FROM accepted_operations WHERE checkpoint IN (
SELECT c.id FROM report_cases c
WHERE c.status IN ('approved','rejected','expired') AND c.created_at+?<=?
ORDER BY c.created_at,c.id LIMIT 100)
AND kind IN ('report_indexing','report_approved_processing')`, caseRetentionSeconds, now); err != nil {
		return fmt.Errorf("reports: clean retained report operations: %w", err)
	}
	if _, err := tx.ExecContext(batchCtx, `DELETE FROM report_cases WHERE id IN (
SELECT c.id FROM report_cases c
WHERE c.status IN ('approved','rejected','expired') AND c.created_at+?<=?
 AND NOT EXISTS(SELECT 1 FROM legal_holds h
  WHERE h.object_kind='report_case' AND h.object_ref=c.id AND h.state='active')
ORDER BY c.created_at,c.id LIMIT 100)`, caseRetentionSeconds, now); err != nil {
		return fmt.Errorf("reports: clean retained report cases: %w", err)
	}
	return commit(tx, &committed)
}
