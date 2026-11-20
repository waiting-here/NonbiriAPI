package whalehaven

import (
	"math"

	"github.com/waiting-here/NonbiriAPI/internal/activities"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
	"github.com/waiting-here/NonbiriAPI/internal/whalehaven/rules"
)

func purchase(w *world, r *resident, op string, cost int64) error {
	wallet, err := ledger.UserAssetAccount(w.ctx, w.tx, r.id, ledger.Game)
	if err != nil {
		return err
	}
	platform, err := ledger.CodedAssetAccount(w.ctx, w.tx, "platform", ledger.Game)
	if err != nil {
		return err
	}
	plan, err := ledger.NewWhaleFeedPurchase(ledger.Meta{OperationID: op, ActorUserID: r.id, CreatedAt: w.now / 1000}, wallet.ID, platform.ID, ledger.AmountFromMilli(cost))
	if err != nil {
		return err
	}
	_, err = ledger.Apply(w.ctx, w.tx, plan)
	return err
}
func (w *world) quote(r *resident, lots int64) (rules.ExchangeQuote, error) {
	if !w.master || !w.config.Enabled || !w.config.ExchangeEnabled {
		return rules.ExchangeQuote{}, &problem{"feature_disabled", "鲸币兑出当前已关闭。"}
	}
	if !r.exists {
		return rules.ExchangeQuote{}, conflict("领养小鲸后才可兑出。")
	}
	wallet, err := ledger.UserAccount(w.ctx, w.tx, r.id)
	if err != nil {
		return rules.ExchangeQuote{}, err
	}
	b := wallet.Balance.Big()
	var balance int64
	switch {
	case b.IsInt64():
		balance = b.Int64()
	case b.Sign() > 0:
		balance = math.MaxInt64
	default:
		balance = math.MinInt64
	}
	q, err := r.state.Quote(lots, balance, w.config)
	if err != nil {
		return q, conflict(err.Error())
	}
	return q, nil
}
func (w *world) quoteView(r *resident, q rules.ExchangeQuote) map[string]any {
	return map[string]any{"quantity": key(q.Lots), "max_lots": key(q.AvailableLots), "settings_revision": key(w.configRevision), "profile_revision": key(r.revision),
		"source_pearls": key(q.Pearls), "gross_general_milli": key(q.GrossMilli), "platform_milli": key(q.PlatformMilli), "welfare_milli": key(q.WelfareMilli),
		"thursday_milli": key(q.ThursdayMilli), "net_general_milli": key(q.NetMilli), "daily_source_remaining": key(q.RemainingDailyPearls),
		"general_wallet_cap_milli": key(q.WalletCapMilli), "wallet_cap_blocked": q.WalletCapBlocked}
}
func (s *Service) cashOut(w *world, r *resident, lots int64, op string) (facts activities.PublishFacts, err error) {
	if lots < 1 {
		return facts, invalid("请选择至少一整手。")
	}
	q, err := w.quote(r, lots)
	if err != nil {
		return facts, err
	}
	wallet, err := ledger.UserAccount(w.ctx, w.tx, r.id)
	if err != nil {
		return facts, err
	}
	external, err := ledger.CodedAccount(w.ctx, w.tx, "external")
	if err != nil {
		return facts, err
	}
	var platform, welfare, thursday int64
	var destinations []activities.PoolDestination
	var dest activities.PoolDestination
	if q.PlatformMilli > 0 {
		p, e := ledger.CodedAccount(w.ctx, w.tx, "platform")
		if e != nil {
			return facts, e
		}
		platform = p.ID
	}
	if q.WelfareMilli > 0 {
		dest, err = s.pools.Repository().WelfareDestination(w.ctx, w.tx)
		if err != nil {
			return facts, err
		}
		welfare = dest.AccountID
		destinations = append(destinations, dest)
	}
	if q.ThursdayMilli > 0 {
		dest, err = s.pools.Repository().ThursdayDestination(w.ctx, w.tx, w.now/1000)
		if err != nil {
			return facts, err
		}
		thursday = dest.AccountID
		destinations = append(destinations, dest)
	}
	plan, err := ledger.NewWhaleExchange(ledger.Meta{OperationID: op, ActorUserID: r.id, CreatedAt: w.now / 1000}, wallet.ID, external.ID, platform, welfare, thursday, ledger.AmountFromMilli(q.NetMilli), ledger.AmountFromMilli(q.PlatformMilli), ledger.AmountFromMilli(q.WelfareMilli), ledger.AmountFromMilli(q.ThursdayMilli))
	if err != nil {
		return facts, err
	}
	if _, err = ledger.Apply(w.ctx, w.tx, plan); err != nil {
		return facts, err
	}
	if len(destinations) > 0 {
		facts, err = s.pools.Repository().RecordPoolTransfers(w.ctx, w.tx, w.now/1000, destinations...)
		if err != nil {
			return facts, err
		}
	}
	r.state.Wallet.Pearls -= q.Pearls
	r.state.Wallet.DailyPearlExchanged += q.Pearls
	w.keepDay(r)
	r.dirty = true
	w.remember(event{user: r.id, at: w.now, kind: "exchange", text: "兑出 " + key(q.Pearls) + " 枚小鲸币", pearls: -q.Pearls})
	return facts, nil
}
