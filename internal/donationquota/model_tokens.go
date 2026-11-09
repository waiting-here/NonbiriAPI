package donationquota

import (
	"context"
	"database/sql"
	"errors"

	"github.com/waiting-here/NonbiriAPI/internal/db"
)

func (b modelBudget) adjusted(previous, next TokenVector) ([3]db.U128, error) {
	result := [3]db.U128{}
	if !previous.Valid() || !next.Valid() {
		return result, ErrInvariant
	}
	old, newValues := modelValues(previous), modelValues(next)
	for i := range 3 {
		without, err := subtract(b.reserved[i], old[i])
		if err != nil {
			return result, err
		}
		occupied, err := add(b.used[i], without)
		if err != nil {
			return result, err
		}
		total, err := add(occupied, newValues[i])
		if err != nil {
			return result, err
		}
		if b.limits[i] != nil {
			if i > 0 && next.Input == nil {
				return result, ErrLimited
			}
			if occupied.Big().Cmp(b.limits[i].Big()) >= 0 || total.Big().Cmp(b.limits[i].Big()) > 0 {
				return result, ErrLimited
			}
		}
		result[i], err = add(without, newValues[i])
		if err != nil {
			return result, err
		}
	}
	return result, nil
}

// ReplaceModelReservation shares the caller's key/claim transaction. Removing a
// model limit preserves cumulative usage and outstanding reservations.
func ReplaceModelReservation(ctx context.Context, tx *sql.Tx, keyID, now int64, model string, previous, next TokenVector) error {
	if model == "" {
		return nil
	} // Existing claims have no historical model counter.
	if !ValidModel(model) || keyID <= 0 || !validNow(now) {
		return ErrInvalid
	}
	b, err := readModelBudget(ctx, tx, keyID, model)
	if err != nil {
		return err
	}
	reserved, err := b.adjusted(previous, next)
	if err != nil {
		return err
	}
	if b.created == nil {
		_, err = tx.ExecContext(ctx, `INSERT INTO donation_model_quotas(donation_key_id,upstream_model_id,tokens_reserved,input_tokens_reserved,output_tokens_reserved,created_at) VALUES(?,?,?,?,?,?)`,
			keyID, model, db.EncodeU128(reserved[0]), db.EncodeU128(reserved[1]), db.EncodeU128(reserved[2]), now)
		return err
	}
	return oneRow(tx.ExecContext(ctx, `UPDATE donation_model_quotas SET tokens_reserved=?,input_tokens_reserved=?,output_tokens_reserved=?
 WHERE donation_key_id=? AND upstream_model_id=? AND tokens_reserved=? AND input_tokens_reserved=? AND output_tokens_reserved=?`,
		db.EncodeU128(reserved[0]), db.EncodeU128(reserved[1]), db.EncodeU128(reserved[2]), keyID, model, db.EncodeU128(b.reserved[0]), db.EncodeU128(b.reserved[1]), db.EncodeU128(b.reserved[2])))
}

func SettleModelTokens(ctx context.Context, tx *sql.Tx, keyID int64, model string, previous, actual TokenVector) error {
	if model == "" {
		return nil
	}
	if !previous.Valid() || !actual.Valid() {
		return ErrInvariant
	}
	b, err := readModelBudget(ctx, tx, keyID, model)
	if err != nil {
		return err
	}
	if b.created == nil {
		return ErrInvariant
	}
	old, newValues := modelValues(previous), modelValues(actual)
	for i := range 3 {
		if b.reserved[i], err = subtract(b.reserved[i], old[i]); err != nil {
			return err
		}
		if b.used[i], err = add(b.used[i], newValues[i]); err != nil {
			return err
		}
	}
	if actual.Input == nil {
		if b.unattributed, err = add(b.unattributed, newValues[0]); err != nil {
			return err
		}
	}
	return oneRow(tx.ExecContext(ctx, `UPDATE donation_model_quotas SET tokens_used=?,input_tokens_used=?,output_tokens_used=?,tokens_reserved=?,input_tokens_reserved=?,output_tokens_reserved=?,unattributed_total_tokens=?
 WHERE donation_key_id=? AND upstream_model_id=?`, db.EncodeU128(b.used[0]), db.EncodeU128(b.used[1]), db.EncodeU128(b.used[2]), db.EncodeU128(b.reserved[0]), db.EncodeU128(b.reserved[1]), db.EncodeU128(b.reserved[2]), db.EncodeU128(b.unattributed), keyID, model))
}

func ModelAvailable(ctx context.Context, q Reader, keyID, now int64, model string, reservation TokenVector) (bool, error) {
	b, err := readModelBudget(ctx, q, keyID, model)
	if err != nil {
		return false, err
	}
	if _, err = b.adjusted(TokenVector{}, reservation); errors.Is(err, ErrLimited) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	rules, err := ModelViews(ctx, q, keyID, now, model)
	if err != nil {
		return false, err
	}
	return viewsAvailable(rules, reservation.Amounts())
}
