package fishing

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

const (
	HomeStateSettlementPending = "settlement_pending"
	HomeStateRecoveryRequired  = "recovery_required"
	maxHomePendingResults      = 100
)

var (
	ErrHomeInvalid       = errors.New("fishing: invalid home summary request")
	ErrHomeInvariant     = errors.New("fishing: invalid home summary state")
	ErrHomeResourceLimit = errors.New("fishing: home summary resource limit")
	ErrHomeUnavailable   = errors.New("fishing: home summary unavailable")
)

type HomeContinue struct {
	ResourceID string
	State      string
}

type HomePendingResult struct {
	ResourceID string
	CreatedAt  int64
}

type HomeSummary struct {
	Continue       []HomeContinue
	PendingResults []HomePendingResult
}

// HomeSummaryTx reads only the Fishing-owned rows used by the home summary.
// The caller owns the transaction so all game domains share one snapshot.
func HomeSummaryTx(ctx context.Context, tx *sql.Tx, userID int64) (HomeSummary, error) {
	if ctx == nil || tx == nil || userID <= 0 {
		return HomeSummary{}, ErrHomeInvalid
	}
	result := HomeSummary{Continue: []HomeContinue{}, PendingResults: []HomePendingResult{}}

	var (
		batchID        string
		retryExhausted int
		nextAttemptAt  sql.NullInt64
	)
	err := tx.QueryRowContext(ctx, `
SELECT id,retry_exhausted,next_attempt_at
FROM game_fishing_batches
WHERE user_id=? AND state='reserved'`, userID).Scan(&batchID, &retryExhausted, &nextAttemptAt)
	if err == nil {
		state := ""
		switch {
		case retryExhausted == 0 && nextAttemptAt.Valid && validHomeTime(nextAttemptAt.Int64):
			state = HomeStateSettlementPending
		case retryExhausted == 1 && !nextAttemptAt.Valid:
			state = HomeStateRecoveryRequired
		default:
			return HomeSummary{}, ErrHomeInvariant
		}
		result.Continue = append(result.Continue, HomeContinue{ResourceID: batchID, State: state})
	} else if !errors.Is(err, sql.ErrNoRows) {
		return HomeSummary{}, classifyHomeDB(err)
	}

	rows, err := tx.QueryContext(ctx, `
SELECT id,created_at
FROM game_fishing_batches
WHERE user_id=? AND state='committed' AND revealed_at IS NULL
ORDER BY created_at,id
LIMIT ?`, userID, maxHomePendingResults+1)
	if err != nil {
		return HomeSummary{}, classifyHomeDB(err)
	}
	defer rows.Close()
	for rows.Next() {
		var pending HomePendingResult
		if err := rows.Scan(&pending.ResourceID, &pending.CreatedAt); err != nil {
			return HomeSummary{}, classifyHomeDB(err)
		}
		if !validHomeTime(pending.CreatedAt) {
			return HomeSummary{}, ErrHomeInvariant
		}
		result.PendingResults = append(result.PendingResults, pending)
		if len(result.PendingResults) > maxHomePendingResults {
			return HomeSummary{}, ErrHomeResourceLimit
		}
	}
	if err := rows.Err(); err != nil {
		return HomeSummary{}, classifyHomeDB(err)
	}
	return result, nil
}

func validHomeTime(value int64) bool {
	return value >= 0 && value <= 253402300799
}

func classifyHomeDB(err error) error {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	message := strings.ToLower(err.Error())
	if strings.Contains(message, "database is locked") || strings.Contains(message, "database is busy") || strings.Contains(message, "sqlite_busy") {
		return ErrHomeUnavailable
	}
	return err
}
