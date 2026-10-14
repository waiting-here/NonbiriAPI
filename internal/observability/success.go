package observability

import (
	"context"
	"database/sql"
	"strings"

	"github.com/waiting-here/NonbiriAPI/internal/db"
)

type SuccessRate struct {
	WindowStart        int64    `json:"window_start"`
	AsOf               int64    `json:"as_of"`
	Success            int64    `json:"success"`
	Failure            int64    `json:"failure"`
	Cancelled          int64    `json:"cancelled"`
	SampleCount        int64    `json:"sample_count"`
	Rate               *float64 `json:"rate"`
	InsufficientSample bool     `json:"insufficient_sample"`
	CaptureStartedAt   int64    `json:"capture_started_at"`
}

func (r *Repository) RecordOutcomeTx(ctx context.Context, tx *sql.Tx, requestID string, modelID int64, result string, dispatched bool, at int64) error {
	if tx == nil || !db.ValidateOpaqueID(requestID, "req_") || modelID <= 0 || at < 0 || (result != "success" && result != "failure" && result != "cancelled") {
		return ErrInvalid
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO charity_request_outcomes(request_log_id,model_id,completed_at,dispatched,result) SELECT id,?,?,?,? FROM request_logs WHERE logical_request_id=? AND completed_at IS NOT NULL AND EXISTS(SELECT 1 FROM charity_models WHERE id=?) ON CONFLICT(request_log_id) DO NOTHING`, modelID, at, dispatched, result, requestID, modelID)
	return err
}

func (r *Repository) RecentSuccess(ctx context.Context, modelID, at int64) (SuccessRate, error) {
	result := SuccessRate{WindowStart: at - 86400, AsOf: at, InsufficientSample: true}
	if r == nil || modelID <= 0 || at < 0 {
		return result, ErrInvalid
	}
	if result.WindowStart < 0 {
		result.WindowStart = 0
	}
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	result, err = RecentSuccessTx(ctx, tx, modelID, at)
	if err != nil {
		return result, err
	}
	return result, tx.Commit()
}

// RecentSuccessTx lets a caller keep model authorization and statistics in
// one read snapshot. It never reads or returns individual request details.
func RecentSuccessTx(ctx context.Context, tx *sql.Tx, modelID, at int64) (SuccessRate, error) {
	result := SuccessRate{WindowStart: max(0, at-86400), AsOf: at, InsufficientSample: true}
	if tx == nil || ctx == nil || modelID <= 0 || at < 0 {
		return result, ErrInvalid
	}
	var err error
	if err = tx.QueryRowContext(ctx, `SELECT capture_started_at FROM observability_state WHERE id=1`).Scan(&result.CaptureStartedAt); err != nil {
		return result, err
	}
	if err = tx.QueryRowContext(ctx, `SELECT COALESCE(SUM(result='success'),0),COALESCE(SUM(result='failure'),0),COALESCE(SUM(result='cancelled'),0) FROM charity_request_outcomes WHERE model_id=? AND dispatched=1 AND completed_at>=? AND completed_at<?`, modelID, result.WindowStart, at).Scan(&result.Success, &result.Failure, &result.Cancelled); err != nil {
		return result, err
	}
	result.SampleCount = result.Success + result.Failure
	result.InsufficientSample = result.SampleCount < 20
	if result.SampleCount > 0 {
		rate := float64(result.Success) / float64(result.SampleCount)
		result.Rate = &rate
	}
	return result, nil
}

// RecentSuccessBatchTx reads one shared capture start and aggregates the
// caller's page of model IDs in the existing authorization snapshot.
// Callers supply model IDs from their validated, bounded page.
func RecentSuccessBatchTx(ctx context.Context, tx *sql.Tx, modelIDs []int64, at int64) (map[int64]SuccessRate, error) {
	if tx == nil || ctx == nil || at < 0 {
		return nil, ErrInvalid
	}
	rates := make(map[int64]SuccessRate, len(modelIDs))
	if len(modelIDs) == 0 {
		return rates, nil
	}
	base := SuccessRate{WindowStart: max(0, at-86400), AsOf: at, InsufficientSample: true}
	if err := tx.QueryRowContext(ctx, `SELECT capture_started_at FROM observability_state WHERE id=1`).Scan(&base.CaptureStartedAt); err != nil {
		return nil, err
	}
	args := make([]any, 0, len(modelIDs)+2)
	for _, id := range modelIDs {
		rates[id] = base
		args = append(args, id)
	}
	args = append(args, base.WindowStart, at)
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(modelIDs)), ",")
	rows, err := tx.QueryContext(ctx, `SELECT model_id,SUM(result='success'),SUM(result='failure'),SUM(result='cancelled')
FROM charity_request_outcomes WHERE model_id IN (`+placeholders+`) AND dispatched=1 AND completed_at>=? AND completed_at<?
GROUP BY model_id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, success, failure, cancelled int64
		if err := rows.Scan(&id, &success, &failure, &cancelled); err != nil {
			return nil, err
		}
		result := rates[id]
		result.Success, result.Failure, result.Cancelled = success, failure, cancelled
		result.SampleCount = result.Success + result.Failure
		result.InsufficientSample = result.SampleCount < 20
		if result.SampleCount > 0 {
			rate := float64(result.Success) / float64(result.SampleCount)
			result.Rate = &rate
		}
		rates[id] = result
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return rates, nil
}
