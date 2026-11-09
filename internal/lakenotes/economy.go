package lakenotes

import (
	"context"
	"database/sql"
	"errors"
	"math/big"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
	"github.com/waiting-here/NonbiriAPI/internal/lakenotes/rules"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
)

func quoteAmounts(p Settings, in QuoteInput) (Quote, error) {
	var out Quote
	if in.Direction.Stored() == "" {
		return out, ErrInvalid
	}
	quantity, e := db.ParseU128Decimal(in.Quantity)
	if e != nil || quantity.Decimal() != in.Quantity || quantity.Big().Sign() <= 0 {
		return out, ErrInvalid
	}
	v := p.Exchanges[in.Direction]
	if !v.Enabled {
		return out, ErrClosed
	}
	a, e := db.ParseU128Decimal(v.SourceAmount)
	if e != nil {
		return out, ErrInvariant
	}
	b, e := db.ParseU128Decimal(v.TargetAmount)
	if e != nil {
		return out, ErrInvariant
	}
	source, e := db.U128FromBig(new(big.Int).Mul(a.Big(), quantity.Big()))
	if e != nil {
		return out, ErrInvalid
	}
	target, e := db.U128FromBig(new(big.Int).Mul(b.Big(), quantity.Big()))
	if e != nil {
		return out, ErrInvalid
	}
	credit := target
	if in.Direction == GeneralToCoins || in.Direction == GameToCoins {
		credit = source
	}
	if credit.Big().Cmp(big.NewInt(db.MaxMoneyMilli)) > 0 {
		return out, ErrInvalid
	}

	return Quote{Direction: in.Direction, Quantity: in.Quantity, SettingsRevision: p.Revision, SourceAmount: source.Decimal(), TargetAmount: target.Decimal(), SourceLot: a.Decimal(), TargetLot: b.Decimal()}, nil
}
func (s *Service) Quote(ctx context.Context, user int64, in QuoteInput) (Quote, error) {
	now, e := s.clock()
	if e != nil {
		return Quote{}, e
	}
	tx, e := s.readDatabase.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return Quote{}, e
	}
	defer tx.Rollback()
	if e = s.authorize(ctx, tx, user, false); e != nil {
		return Quote{}, e
	}
	p, e := s.qualifiedTx(ctx, tx, user, now.Unix())
	if e != nil {
		return Quote{}, e
	}
	q, e := quoteAmounts(p, in)
	if e != nil {
		return q, e
	}
	row, e := profileTx(ctx, tx, user, false, now.Unix())
	if e != nil {
		return q, e
	}
	if e = dailyAllowanceTx(ctx, tx, user, p.Exchanges[in.Direction], now.Unix(), &q); e != nil {
		return q, e
	}
	q.Coins, q.ProfileRevision = string(row.profile.Coins), rev(row.revision)
	q.Wallet, e = walletTx(ctx, tx, user)
	if e != nil {
		return q, e
	}
	return q, tx.Commit()
}
func (s *Service) Exchange(ctx context.Context, user int64, key string, in ExchangeInput) (MutationResult[ExchangeResult], error) {
	pr, e := revision(in.ExpectedSettingsRevision, false)
	if e != nil {
		return MutationResult[ExchangeResult]{}, e
	}
	rr, e := revision(in.ExpectedProfileRevision, false)
	if e != nil {
		return MutationResult[ExchangeResult]{}, e
	}
	return mutate(ctx, s, user, false, key, "POST", baseRoute+"/exchange", in, func(tx *sql.Tx, now time.Time, _ []byte) (ExchangeResult, error) {
		p, e := s.qualifiedTx(ctx, tx, user, now.Unix())
		if e != nil {
			return ExchangeResult{}, e
		}
		if p.Revision != rev(pr) {
			return ExchangeResult{}, ErrConflict
		}
		hash, _ := idempotency.KeyHash(key)
		var previous bool
		if e = tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM lake_notes_exchange_receipts WHERE user_id=? AND operation_key_hash=?)", user, hash[:]).Scan(&previous); e != nil {
			return ExchangeResult{}, e
		}
		if previous {
			return ExchangeResult{}, ErrConflict
		}
		q, e := quoteAmounts(p, in.QuoteInput)
		if e != nil {
			return ExchangeResult{}, e
		}
		if e = dailyAllowanceTx(ctx, tx, user, p.Exchanges[in.Direction], now.Unix(), &q); e != nil {
			return ExchangeResult{}, e
		}
		if q.DailySourceRemaining != nil {
			remaining, _ := rules.ParseAmount(*q.DailySourceRemaining)
			source, _ := rules.ParseAmount(q.SourceAmount)
			if source.Cmp(remaining) > 0 {
				return ExchangeResult{}, ErrDailyLimit
			}
		}
		row, e := profileTx(ctx, tx, user, true, now.Unix())
		if e != nil {
			return ExchangeResult{}, e
		}
		if row.revision != rr {
			return ExchangeResult{}, ErrConflict
		}
		source, _ := rules.ParseAmount(q.SourceAmount)
		target, _ := rules.ParseAmount(q.TargetAmount)
		creditText := q.TargetAmount
		asset := ledger.General
		toCoin := false
		if in.Direction == GeneralToCoins || in.Direction == GameToCoins {
			toCoin = true
			creditText = q.SourceAmount
		}
		if in.Direction == GameToCoins || in.Direction == CoinsToGame {
			asset = ledger.Game
		}
		if toCoin {
			row.profile.Coins, e = row.profile.Coins.Add(target)
		} else {
			row.profile.Coins, e = row.profile.Coins.Sub(source)
		}
		if e != nil {
			if toCoin {
				return ExchangeResult{}, e
			}
			return ExchangeResult{}, ledger.ErrInsufficientBalance
		}
		amount, e := ledger.ParseAmount(creditText)
		if e != nil {
			return ExchangeResult{}, ErrInvalid
		}
		if toCoin {
			amount, _ = ledger.AmountFromBig(new(big.Int).Neg(amount.Big()))
		}
		wallet, e := ledger.UserAssetAccount(ctx, tx, user, asset)
		if e != nil {
			return ExchangeResult{}, e
		}
		external, e := ledger.CodedAssetAccount(ctx, tx, "external", asset)
		if e != nil {
			return ExchangeResult{}, e
		}
		op, e := db.GenerateOpaqueID("op_")
		if e != nil {
			return ExchangeResult{}, e
		}
		plan, e := ledger.NewLakeExchange(ledger.Meta{OperationID: op, ActorUserID: user, CreatedAt: now.Unix()}, wallet.ID, external.ID, asset, amount)
		if e != nil {
			return ExchangeResult{}, e
		}
		posted, e := ledger.Apply(ctx, tx, plan)
		if e != nil {
			return ExchangeResult{}, e
		}
		if q.DailySourceRemaining != nil {
			// Pin site-day boundaries with the first successful limited exchange,
			// before an asynchronous audit projection can observe its receipt.
			if e = db.FreezeSiteTimezoneTx(ctx, tx, now.Unix()); e != nil {
				return ExchangeResult{}, e
			}
		}
		if e = saveProfileTx(ctx, tx, user, &row, now.Unix()); e != nil {
			return ExchangeResult{}, e
		}
		id, e := db.GenerateOpaqueID("lne_")
		if e != nil {
			return ExchangeResult{}, e
		}
		quantity, _ := db.ParseU128Decimal(q.Quantity)
		a, _ := db.ParseU128Decimal(q.SourceLot)
		b, _ := db.ParseU128Decimal(q.TargetLot)
		sa, _ := db.ParseU128Decimal(q.SourceAmount)
		ta, _ := db.ParseU128Decimal(q.TargetAmount)
		_, e = tx.ExecContext(ctx, "INSERT INTO lake_notes_exchange_receipts(id,user_id,config_revision,direction,quantity_mag,source_lot,target_lot,source_amount_mag,target_amount_mag,operation_key_hash,ledger_operation_id,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)", id, user, pr, in.Direction.Stored(), db.EncodeU128(quantity), db.EncodeU128(a), db.EncodeU128(b), db.EncodeU128(sa), db.EncodeU128(ta), hash[:], op, now.Unix())
		if e != nil {
			return ExchangeResult{}, e
		}
		if q.DailySourceRemaining != nil {
			remaining, _ := rules.ParseAmount(*q.DailySourceRemaining)
			remaining, _ = remaining.Sub(source)
			value := string(remaining)
			q.DailySourceRemaining = &value
		}
		q.Coins, q.ProfileRevision = string(row.profile.Coins), rev(row.revision)
		q.Wallet, e = walletTx(ctx, tx, user)
		if e != nil {
			return ExchangeResult{}, e
		}
		// Ledger.Apply invalidates the earlier account balance. q.Wallet was
		// read after posting; no later write changes it or the saved profile.
		v, e := s.profileViewFromTx(ctx, tx, user, now, profileProjection{row: &row, wallet: &q.Wallet, settings: &p})
		return ExchangeResult{ExchangeReceipt{ID: id, Quote: q, OperationID: op, LedgerSeq: rev(posted.LedgerSeq), CreatedAt: now.Unix()}, v}, e
	})
}

const dailyAllowanceBudget = 250 * time.Millisecond

const dailySourceAmountsSQL = `SELECT source_amount_mag FROM lake_notes_exchange_receipts
WHERE user_id=? AND direction=? AND created_at>=? AND created_at<?
ORDER BY created_at DESC`

// Receipts retain the actual amount paid at its historical rate. Only the
// selected user's current site day participates; an exhausted limit can stop
// as soon as the threshold is reached, without summing any older history.
func dailyAllowanceTx(ctx context.Context, tx *sql.Tx, user int64, setting ExchangeSetting, now int64, q *Quote) error {
	if setting.DailySourceLimit == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, dailyAllowanceBudget)
	defer cancel()
	offset, e := db.ResolveSiteTimezoneTx(ctx, tx)
	if e != nil {
		if errors.Is(e, db.ErrTimezoneUnavailable) {
			return ErrInvariant
		}
		return e
	}
	day := db.SiteDayKey(now, int64(offset))
	q.DailyResetAt = day + 86400
	remaining, e := db.ParseU128Decimal(setting.DailySourceLimit)
	if e != nil {
		return ErrInvariant
	}
	available := remaining.Big()
	if available.Sign() > 0 {
		rows, e := tx.QueryContext(ctx, dailySourceAmountsSQL, user, q.Direction.Stored(), day, day+86400)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var raw []byte
			if e = rows.Scan(&raw); e != nil {
				return e
			}
			amount, e := db.DecodeU128(raw)
			if e != nil {
				return ErrInvariant
			}
			available.Sub(available, amount.Big())
			if available.Sign() <= 0 {
				available.SetInt64(0)
				break
			}
		}
		if e = rows.Err(); e != nil {
			return e
		}
	}
	value := available.String()
	q.DailySourceRemaining = &value
	return nil
}
