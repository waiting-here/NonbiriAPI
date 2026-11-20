package donationquota

import (
	"context"
	"database/sql"
	"errors"
	"math/big"

	"github.com/waiting-here/NonbiriAPI/internal/db"
)

// Consumption uses the same call, price and token amounts as the containing key.
type Consumption struct {
	TokenVector
	Calls      int64
	PriceMilli int64
}

func (c Consumption) valid() bool { return c.TokenVector.Valid() && c.Calls >= 0 && c.PriceMilli >= 0 }

func (c Consumption) amounts() Amounts {
	a := c.TokenVector.Amounts()
	a.Calls, _ = db.U128FromBig(big.NewInt(c.Calls))
	a.Credits, _ = db.U128FromBig(big.NewInt(c.PriceMilli))
	return a
}

func (b modelBudget) adjusted(previous, next Amounts) ([len(modelMetrics)]db.U128, error) {
	result := [len(modelMetrics)]db.U128{}
	old, newValues := modelValues(previous), modelValues(next)
	for i := range modelMetrics {
		without, err := subtract(b.reserved[i], old[i])
		if err != nil {
			return result, err
		}
		occupied := new(big.Int).Add(b.used[i].Big(), without.Big())
		total := new(big.Int).Add(occupied, newValues[i].Big())
		if limit := b.limits[i]; limit != nil {
			metric := modelMetrics[i]
			if (metric == "input_tokens" || metric == "output_tokens") && !next.TokenBreakdown {
				return result, ErrLimited
			}
			if occupied.Cmp(limit.Big()) >= 0 || total.Cmp(limit.Big()) > 0 {
				return result, ErrLimited
			}
		}
		if total.BitLen() > 128 {
			return result, ErrInvariant
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
func ReplaceModelReservation(ctx context.Context, tx *sql.Tx, keyID, now int64, model string, previous, next Consumption) error {
	// Receipts created before exact-model tracking have no model counter.
	if model == "" {
		return nil
	}
	if !ValidModel(model) || keyID <= 0 || !validNow(now) {
		return ErrInvalid
	}
	b, err := readModelBudget(ctx, tx, keyID, model)
	if err != nil {
		return err
	}
	if !previous.valid() || !next.valid() {
		return ErrInvariant
	}
	reserved, err := b.adjusted(previous.amounts(), next.amounts())
	if err != nil {
		return err
	}
	if b.created == nil {
		_, err = tx.ExecContext(ctx, `INSERT INTO donation_model_quotas(donation_key_id,upstream_model_id,tokens_reserved,input_tokens_reserved,output_tokens_reserved,calls_reserved,price_reserved_mag,created_at) VALUES(?,?,?,?,?,?,?,?)`,
			keyID, model, db.EncodeU128(reserved[0]), db.EncodeU128(reserved[1]), db.EncodeU128(reserved[2]), db.EncodeU128(reserved[3]), db.EncodeU128(reserved[4]), now)
		return err
	}
	return oneRow(tx.ExecContext(ctx, `UPDATE donation_model_quotas SET tokens_reserved=?,input_tokens_reserved=?,output_tokens_reserved=?,calls_reserved=?,price_reserved_mag=?
 WHERE donation_key_id=? AND upstream_model_id=? AND tokens_reserved=? AND input_tokens_reserved=? AND output_tokens_reserved=? AND calls_reserved=? AND price_reserved_mag=?`,
		db.EncodeU128(reserved[0]), db.EncodeU128(reserved[1]), db.EncodeU128(reserved[2]), db.EncodeU128(reserved[3]), db.EncodeU128(reserved[4]), keyID, model, db.EncodeU128(b.reserved[0]), db.EncodeU128(b.reserved[1]), db.EncodeU128(b.reserved[2]), db.EncodeU128(b.reserved[3]), db.EncodeU128(b.reserved[4])))
}

func SettleModelConsumption(ctx context.Context, tx *sql.Tx, keyID int64, model string, previous, actual Consumption) error {
	if model == "" {
		return nil
	}
	if !previous.valid() || !actual.valid() {
		return ErrInvariant
	}
	b, err := readModelBudget(ctx, tx, keyID, model)
	if err != nil {
		return err
	}
	if b.created == nil {
		return ErrInvariant
	}
	old, newValues := modelValues(previous.amounts()), modelValues(actual.amounts())
	for i := range modelMetrics {
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
	return oneRow(tx.ExecContext(ctx, `UPDATE donation_model_quotas SET tokens_used=?,input_tokens_used=?,output_tokens_used=?,calls_used=?,price_used_mag=?,tokens_reserved=?,input_tokens_reserved=?,output_tokens_reserved=?,calls_reserved=?,price_reserved_mag=?,unattributed_total_tokens=?
 WHERE donation_key_id=? AND upstream_model_id=?`, db.EncodeU128(b.used[0]), db.EncodeU128(b.used[1]), db.EncodeU128(b.used[2]), db.EncodeU128(b.used[3]), db.EncodeU128(b.used[4]), db.EncodeU128(b.reserved[0]), db.EncodeU128(b.reserved[1]), db.EncodeU128(b.reserved[2]), db.EncodeU128(b.reserved[3]), db.EncodeU128(b.reserved[4]), db.EncodeU128(b.unattributed), keyID, model))
}

func ModelAvailable(ctx context.Context, q Reader, keyID, now int64, model string, reservation Amounts) (bool, error) {
	b, err := readModelBudget(ctx, q, keyID, model)
	if err != nil {
		return false, err
	}
	if _, err = b.adjusted(Amounts{}, reservation); errors.Is(err, ErrLimited) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	rules, err := ModelViews(ctx, q, keyID, now, model)
	if err != nil {
		return false, err
	}
	return viewsAvailable(rules, reservation)
}
