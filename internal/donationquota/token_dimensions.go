package donationquota

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"math/big"

	"github.com/waiting-here/NonbiriAPI/internal/db"
)

// A nil breakdown represents a legacy scalar estimate, never known zero usage.
type TokenVector struct {
	Total         int64
	Input, Output *int64
}

func (v TokenVector) Valid() bool {
	if v.Total < 0 || (v.Input == nil) != (v.Output == nil) {
		return false
	}
	return v.Input == nil || (*v.Input >= 0 && *v.Output >= 0 && *v.Input <= math.MaxInt64-*v.Output && *v.Input+*v.Output == v.Total)
}

func (v TokenVector) Amounts() Amounts {
	var a Amounts
	a.Tokens, _ = db.U128FromBig(big.NewInt(v.Total))
	if v.Input != nil && v.Output != nil {
		a.TokenBreakdown = true
		a.InputTokens, _ = db.U128FromBig(big.NewInt(*v.Input))
		a.OutputTokens, _ = db.U128FromBig(big.NewInt(*v.Output))
	}
	return a
}

type TokenBudget struct {
	Reservation TokenVector
	used        [2]db.U128
	reserved    [2]db.U128
	limits      [2]*db.U128
}

func ReadTokenBudget(ctx context.Context, q Reader, keyID int64) (TokenBudget, error) {
	var b TokenBudget
	var used, reserved, limits [2][]byte
	var required bool
	err := q.QueryRowContext(ctx, `SELECT token_reserve,input_token_reserve,output_token_reserve,
input_tokens_used,output_tokens_used,input_tokens_reserved,output_tokens_reserved,input_token_limit_mag,output_token_limit_mag,
EXISTS(SELECT 1 FROM donation_quota_rules r JOIN donation_quota_epochs e ON e.rule_id=r.id AND e.epoch=r.current_epoch
 WHERE r.donation_key_id=donation_keys.id AND e.metric IN ('input_tokens','output_tokens'))
FROM donation_keys WHERE id=?`, keyID).Scan(&b.Reservation.Total, &b.Reservation.Input, &b.Reservation.Output,
		&used[0], &used[1], &reserved[0], &reserved[1], &limits[0], &limits[1], &required)
	if errors.Is(err, sql.ErrNoRows) {
		return b, ErrConflict
	}
	if err != nil {
		return b, err
	}
	if (b.Reservation.Input == nil) != (b.Reservation.Output == nil) {
		return b, ErrInvariant
	}
	if b.Reservation.Input != nil {
		i, o := *b.Reservation.Input, *b.Reservation.Output
		if i < 0 || o < 0 || i > math.MaxInt64-o || (i == 0 && o == 0) {
			return b, ErrInvariant
		}
		b.Reservation.Total = i + o
	} else if required || limits[0] != nil || limits[1] != nil {
		return b, ErrInvariant
	}
	if !b.Reservation.Valid() {
		return b, ErrInvariant
	}
	for n := range 2 {
		if b.used[n], err = magnitude(used[n]); err != nil {
			return b, err
		}
		if b.reserved[n], err = magnitude(reserved[n]); err != nil {
			return b, err
		}
		if limits[n] != nil {
			limit, err := magnitude(limits[n])
			if err != nil {
				return b, err
			}
			b.limits[n] = &limit
		}
	}
	return b, nil
}

func (b TokenBudget) adjusted(previous TokenVector) ([2]db.U128, error) {
	var next [2]db.U128
	if !previous.Valid() || !b.Reservation.Valid() {
		return next, ErrInvariant
	}
	oldValues := previous.Amounts()
	newValues := b.Reservation.Amounts()
	old := [2]db.U128{oldValues.InputTokens, oldValues.OutputTokens}
	additions := [2]db.U128{newValues.InputTokens, newValues.OutputTokens}
	for n := range 2 {
		remaining, err := subtract(b.reserved[n], old[n])
		if err != nil {
			return next, err
		}
		occupied, err := add(b.used[n], remaining)
		if err != nil {
			return next, err
		}
		total, err := add(occupied, additions[n])
		if err != nil {
			return next, err
		}
		if b.limits[n] != nil && (occupied.Big().Cmp(b.limits[n].Big()) >= 0 || total.Big().Cmp(b.limits[n].Big()) > 0) {
			return next, ErrLimited
		}
		if next[n], err = add(remaining, additions[n]); err != nil {
			return next, err
		}
	}
	return next, nil
}

func (b TokenBudget) Available() (bool, error) {
	_, err := b.adjusted(TokenVector{})
	if errors.Is(err, ErrLimited) {
		return false, nil
	}
	return err == nil, err
}

// ReplaceReservation runs in the same transaction as the total-token update
// and both claim receipts. Its compare-and-swap rejects a stale budget.
func (b TokenBudget) ReplaceReservation(ctx context.Context, tx *sql.Tx, keyID int64, previous TokenVector) error {
	next, err := b.adjusted(previous)
	if err != nil {
		return err
	}
	return oneRow(tx.ExecContext(ctx, `UPDATE donation_keys SET input_tokens_reserved=?,output_tokens_reserved=?
WHERE id=? AND input_tokens_reserved=? AND output_tokens_reserved=? AND input_tokens_used=? AND output_tokens_used=?`,
		db.EncodeU128(next[0]), db.EncodeU128(next[1]), keyID, db.EncodeU128(b.reserved[0]), db.EncodeU128(b.reserved[1]), db.EncodeU128(b.used[0]), db.EncodeU128(b.used[1])))
}

// SettleTokenVector uses only frozen receipts, so removing or lowering limits
// cannot prevent releasing occupancy or recording a larger actual usage.
func SettleTokenVector(ctx context.Context, tx *sql.Tx, keyID int64, previous, actual TokenVector) error {
	if !previous.Valid() || !actual.Valid() {
		return ErrInvariant
	}
	var blobs [5][]byte
	err := tx.QueryRowContext(ctx, `SELECT input_tokens_used,output_tokens_used,input_tokens_reserved,output_tokens_reserved,unattributed_total_tokens FROM donation_keys WHERE id=?`, keyID).
		Scan(&blobs[0], &blobs[1], &blobs[2], &blobs[3], &blobs[4])
	if err != nil {
		return err
	}
	var values [5]db.U128
	for n := range values {
		if values[n], err = magnitude(blobs[n]); err != nil {
			return err
		}
	}
	p, a := previous.Amounts(), actual.Amounts()
	if values[0], err = add(values[0], a.InputTokens); err != nil {
		return err
	}
	if values[1], err = add(values[1], a.OutputTokens); err != nil {
		return err
	}
	if values[2], err = subtract(values[2], p.InputTokens); err != nil {
		return err
	}
	if values[3], err = subtract(values[3], p.OutputTokens); err != nil {
		return err
	}
	if actual.Input == nil {
		if values[4], err = add(values[4], a.Tokens); err != nil {
			return err
		}
	}
	return oneRow(tx.ExecContext(ctx, `UPDATE donation_keys SET input_tokens_used=?,output_tokens_used=?,input_tokens_reserved=?,output_tokens_reserved=?,unattributed_total_tokens=?
WHERE id=? AND input_tokens_used=? AND output_tokens_used=? AND input_tokens_reserved=? AND output_tokens_reserved=? AND unattributed_total_tokens=?`,
		db.EncodeU128(values[0]), db.EncodeU128(values[1]), db.EncodeU128(values[2]), db.EncodeU128(values[3]), db.EncodeU128(values[4]),
		keyID, blobs[0], blobs[1], blobs[2], blobs[3], blobs[4]))
}
