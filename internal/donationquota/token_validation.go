package donationquota

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/waiting-here/NonbiriAPI/internal/db"
)

func validateTokenReservations(ctx context.Context, tx *sql.Tx) error {
	var mismatch bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM donation_usage_reservations u
 JOIN dispatch_claims c ON c.id=u.claim_id WHERE u.tokens_reserved<>c.reserved_tokens
 OR u.input_tokens_reserved IS NOT c.reserved_input_tokens OR u.output_tokens_reserved IS NOT c.reserved_output_tokens)`).Scan(&mismatch); err != nil {
		return err
	}
	if mismatch {
		return fmt.Errorf("%w: token receipt mismatch", ErrInvariant)
	}
	// Streaming sorted rows keeps memory constant even when many active claims
	// share a key. Sum all 128 bits rather than SQLite's signed integer SUM.
	rows, err := tx.QueryContext(ctx, `SELECT k.id,k.input_tokens_reserved,k.output_tokens_reserved,u.claim_id,u.tokens_reserved,u.input_tokens_reserved,u.output_tokens_reserved
 FROM donation_keys k LEFT JOIN donation_usage_reservations u ON u.donation_key_id=k.id AND u.state='reserved' ORDER BY k.id,u.claim_id`)
	if err != nil {
		return err
	}
	defer rows.Close()
	var keyID int64
	var expected, sum [2]db.U128
	check := func() error {
		if keyID != 0 && expected != sum {
			return fmt.Errorf("%w: token occupancy mismatch", ErrInvariant)
		}
		return nil
	}
	for rows.Next() {
		var id int64
		var left, right []byte
		var claimID *string
		var total sql.NullInt64
		var input, output *int64
		if err := rows.Scan(&id, &left, &right, &claimID, &total, &input, &output); err != nil {
			return err
		}
		if id != keyID {
			if err := check(); err != nil {
				return err
			}
			keyID = id
			sum = [2]db.U128{}
			if expected[0], err = magnitude(left); err != nil {
				return err
			}
			if expected[1], err = magnitude(right); err != nil {
				return err
			}
		}
		if claimID == nil {
			continue
		}
		vector := TokenVector{Total: total.Int64, Input: input, Output: output}
		if !total.Valid || !vector.Valid() {
			return ErrInvariant
		}
		amounts := vector.Amounts()
		if sum[0], err = add(sum[0], amounts.InputTokens); err != nil {
			return err
		}
		if sum[1], err = add(sum[1], amounts.OutputTokens); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	return check()
}
