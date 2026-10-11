package requestattempt

import (
	"context"
	"database/sql"
	"errors"
)

// Streaming retains the caller's validated mode before any upstream adaptation.
func Streaming(ctx context.Context, value bool) {
	if a := get(ctx); a != nil {
		mode := uint32(1)
		if value {
			mode = 2
		}
		a.streaming.CompareAndSwap(0, mode)
	}
}
func RequestedStreaming(ctx context.Context) *bool {
	if a := get(ctx); a != nil {
		switch a.streaming.Load() {
		case 1:
			return new(false)
		case 2:
			return new(true)
		}
	}
	return nil
}

const (
	ActionShortDeduction = 1 << iota
	ActionShortBan
	ActionShortSuspend
	ActionRPMBan
	ActionClientBan
	ActionContextPenalty
)

// AutomaticActionsTx writes only effects committed in this same transaction.
// OR keeps retries idempotent; NULL on older rows means no complete history.
func AutomaticActionsTx(ctx context.Context, tx *sql.Tx, requestID string, actions int) error {
	if tx == nil || actions <= 0 || actions > 63 {
		return errors.New("invalid automatic request actions")
	}
	result, err := tx.ExecContext(ctx, `UPDATE request_logs SET automatic_actions=COALESCE(automatic_actions,0) | ? WHERE logical_request_id=?`, actions, requestID)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return errors.New("automatic request actions have no request log")
	}
	return nil
}
