package logapi

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
)

const (
	lifecycleExportLimit       = 10_000
	requestLogRetentionSeconds = int64(30 * 24 * 60 * 60)
)

// LifecycleLogSummary is the only request-log projection available to
// account export. It contains aggregate safe facts and never individual log
// rows, attempts, bodies, diagnostics, or upstream material.
type LifecycleLogSummary struct {
	TotalLogs        string
	LogsLast30Days   string
	ErrorLogs        string
	UsageUnknownLogs string
	AverageDuration  string
}

// ExportLifecycleSummary applies the ordinary terminal+30-day cutoff inside
// the coordinator-owned snapshot. A legal hold never widens this summary.
func (repository *Repository) ExportLifecycleSummary(
	ctx context.Context,
	tx *sql.Tx,
	userID, decisionNow int64,
	limit int,
) (LifecycleLogSummary, error) {
	if repository == nil || repository.db == nil || ctx == nil || tx == nil || userID <= 0 ||
		decisionNow < 0 || decisionNow > maxUnixSecond || limit < 1 || limit > lifecycleExportLimit {
		return LifecycleLogSummary{}, ErrInvalid
	}
	cutoff := decisionNow - requestLogRetentionSeconds
	var total, recent, failed, unknown, average int64
	err := tx.QueryRowContext(ctx, `
SELECT COUNT(*),
       COALESCE(SUM(CASE WHEN started_at>=? THEN 1 ELSE 0 END),0),
       COALESCE(SUM(CASE WHEN caller_result_class='failed' THEN 1 ELSE 0 END),0),
       COALESCE(SUM(CASE WHEN usage_unknown=1 THEN 1 ELSE 0 END),0),
       COALESCE(CAST(AVG(duration_ms) AS INTEGER),0)
FROM request_logs
WHERE user_id=? AND (completed_at IS NULL OR completed_at>?)`, cutoff, userID, cutoff).Scan(
		&total, &recent, &failed, &unknown, &average,
	)
	if err != nil {
		return LifecycleLogSummary{}, fmt.Errorf("logapi: export lifecycle summary: %w", err)
	}
	if total < 0 || recent < 0 || recent > total || failed < 0 || failed > total ||
		unknown < 0 || unknown > total || average < 0 || average > 86_400_000 {
		return LifecycleLogSummary{}, ErrInvariant
	}
	return LifecycleLogSummary{
		TotalLogs: strconv.FormatInt(total, 10), LogsLast30Days: strconv.FormatInt(recent, 10),
		ErrorLogs: strconv.FormatInt(failed, 10), UsageUnknownLogs: strconv.FormatInt(unknown, 10),
		AverageDuration: strconv.FormatInt(average, 10),
	}, nil
}

// PrepareLifecycleAccountDeletion deletes every non-held request-log root;
// attempts follow by the aggregate FK. An active held root and all of its
// attempts remain byte-for-byte untouched for the later user FK projection
// clear performed by the shared account deletion transaction.
func (repository *Repository) PrepareLifecycleAccountDeletion(
	ctx context.Context,
	tx *sql.Tx,
	userID, decisionNow int64,
) error {
	if repository == nil || repository.db == nil || ctx == nil || tx == nil || userID <= 0 ||
		decisionNow < 0 || decisionNow > maxUnixSecond {
		return ErrInvalid
	}
	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE id=? AND is_admin=0)`, userID).Scan(&exists); err != nil {
		return fmt.Errorf("logapi: read lifecycle deletion owner: %w", err)
	}
	if exists != 1 {
		return ErrNotFound
	}
	if _, err := tx.ExecContext(ctx, `
DELETE FROM request_logs
WHERE user_id=?
  AND NOT EXISTS(
    SELECT 1 FROM legal_holds h
    WHERE h.object_kind='request_log'
      AND h.object_ref=CAST(request_logs.id AS TEXT)
      AND h.state='active'
  )`, userID); err != nil {
		return fmt.Errorf("logapi: delete non-held lifecycle logs: %w", err)
	}
	return nil
}
