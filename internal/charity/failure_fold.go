package charity

import (
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
func foldFailureRows(rows *sql.Rows, next, streak, threshold db.U128, disabled int) (failureFold, error) {
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
		var justDisabled bool
		before := out.streak
		out.streak, out.disabled, justDisabled, err = foldFailureOutcome(out.streak, out.disabled, threshold, state, success, disposition, origin, apply)
		if err != nil {
			return out, err
		}
		if justDisabled {
			out.crossing = &adminalerts.FailureCrossing{ClaimID: claimID, Fold: out.next, Before: before, After: out.streak, Threshold: threshold}
		}
		out.next, err = addU128(out.next, big.NewInt(1))
		if err != nil {
			return out, err
		}
	}
	return out, rows.Err()
}
