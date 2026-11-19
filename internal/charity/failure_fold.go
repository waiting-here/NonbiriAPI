package charity

import (
	"context"
	"database/sql"
	"math/big"

	"github.com/waiting-here/NonbiriAPI/internal/adminalerts"
	"github.com/waiting-here/NonbiriAPI/internal/db"
)

type failureFold struct {
	next, streak db.U128
	disabled     int
	crossing     *adminalerts.FailureCrossing
}

// Consume exactly the contiguous terminal prefix. A missing or reserved result
// stops both reducers, even when later results have already completed. Rows are
// closed before the caller updates its cursor in the same transaction.
func foldFailureRows(ctx context.Context, tx *sql.Tx, rows *sql.Rows, next, streak, threshold db.U128, disabled int) (failureFold, error) {
	out := failureFold{next: next, streak: streak, disabled: disabled}
	defer rows.Close()
	for rows.Next() {
		var sequence []byte
		var claimID, state string
		var success sql.NullInt64
		var disposition, origin sql.NullString
		var apply bool
		if err := rows.Scan(&claimID, &sequence, &state, &success, &disposition, &origin, &apply); err != nil {
			return out, err
		}
		sequenceNumber, err := decodeU128(sequence)
		if err != nil {
			return out, err
		}
		if sequenceNumber != out.next || state == "reserved" {
			break
		}
		before, wasDisabled := out.streak, out.disabled
		var effect string
		out.streak, out.disabled, effect, err = foldFailureOutcome(before, wasDisabled, threshold, state, success, disposition, origin, apply)
		if err != nil {
			return out, err
		}
		// Only the authoritative cursor writes the receipt. A service attempt
		// also advances its physical key cursor, without applying to that key.
		if apply && state == "committed" {
			_, err = tx.ExecContext(ctx, `UPDATE request_attempts SET streak_effect=?,streak_before=?,streak_after=?
WHERE claim_id=? AND streak_effect='pending'`, effect, before.Decimal(), out.streak.Decimal(), claimID)
			if err != nil {
				return out, err
			}
		}
		if wasDisabled == 0 && out.disabled == 1 {
			out.crossing = &adminalerts.FailureCrossing{ClaimID: claimID, Fold: out.next, Before: before, After: out.streak, Threshold: threshold}
		}
		out.next, err = addU128(out.next, big.NewInt(1))
		if err != nil {
			return out, err
		}
	}
	return out, rows.Err()
}
