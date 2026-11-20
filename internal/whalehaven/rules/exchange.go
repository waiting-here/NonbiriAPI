package rules

import (
	"errors"
	"math"
)

type ExchangeQuote struct {
	WalletCapBlocked     bool  `json:"wallet_cap_blocked"`
	Lots                 int64 `json:"lots"`
	AvailableLots        int64 `json:"available_lots"`
	Pearls               int64 `json:"pearls"`
	GrossMilli           int64 `json:"gross_milli"`
	NetMilli             int64 `json:"net_milli"`
	PlatformMilli        int64 `json:"platform_milli"`
	WelfareMilli         int64 `json:"welfare_milli"`
	ThursdayMilli        int64 `json:"thursday_milli"`
	RemainingDailyPearls int64 `json:"remaining_daily_pearls"`
	WalletCapMilli       int64 `json:"wallet_cap_milli"`
}

// Each destination receives the floor of its cut for ONE lot. Batching lots
// must be equivalent to performing the same exchanges one at a time.
func fraction(value, bp int64) int64 {
	return value/10_000*bp + value%10_000*bp/10_000
}

// Quote prices an executable exchange against the exact current site day and
// wallet. Zero lots requests availability only. When the host wallet exceeds
// int64, the caller can pass MaxInt64 (above every supported wallet cap).
func (s *State) Quote(lots, generalMilli int64, p Parameters) (ExchangeQuote, error) {
	q := ExchangeQuote{Lots: lots, WalletCapMilli: p.Value("wallet_cap_milli"),
		RemainingDailyPearls: max(0, p.Value("daily_exchange_pearls")-s.Wallet.DailyPearlExchanged)}
	price, gross := p.Value("lot_pearls"), p.Value("lot_gross_milli")
	pbp, wbp, tbp := p.Value("platform_cut_bp"), p.Value("welfare_cut_bp"), p.Value("thursday_cut_bp")
	if price < 1 || gross < 1 || pbp < 0 || wbp < 0 || tbp < 0 || pbp+wbp+tbp >= 10_000 {
		return q, errors.New("兑换参数无效。")
	}
	platform, welfare, thursday := fraction(gross, pbp), fraction(gross, wbp), fraction(gross, tbp)
	net := gross - platform - welfare - thursday
	room := int64(math.MaxInt64)
	if generalMilli >= q.WalletCapMilli-math.MaxInt64 {
		room = max(0, q.WalletCapMilli-generalMilli)
	}
	q.WalletCapBlocked = room < net
	q.AvailableLots = min(q.RemainingDailyPearls/price, s.Wallet.Pearls/price, room/net)
	if lots < 0 || lots > q.AvailableLots || lots > math.MaxInt64/gross {
		return q, errors.New("手数超过当前可兑额度。")
	}
	q.Pearls = lots * price
	q.GrossMilli = lots * gross
	q.NetMilli = lots * net
	q.PlatformMilli = lots * platform
	q.WelfareMilli = lots * welfare
	q.ThursdayMilli = lots * thursday
	return q, nil
}
