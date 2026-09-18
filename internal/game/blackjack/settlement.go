package blackjack

import (
	"math/big"

	"github.com/waiting-here/NonbiriAPI/internal/game/blackjack/config"
	"github.com/waiting-here/NonbiriAPI/internal/game/blackjack/engine"
)

type HandSettlement struct {
	Stake    int64  `json:"stake_milli,string"`
	Gross    int64  `json:"gross_milli,string"`
	Net      int64  `json:"net_milli,string"`
	Platform int64  `json:"platform_milli,string"`
	Welfare  int64  `json:"welfare_milli,string"`
	Thursday int64  `json:"thursday_milli,string"`
	Outcome  string `json:"outcome"`
}

func SettleHand(base int64, hand engine.Hand, rates config.Rates) (HandSettlement, error) {
	if base <= 0 || base > config.MaxStakeMilli || !rates.Valid() || hand.Units < 1 || hand.Units > 2 || !hand.Stood {
		return HandSettlement{}, engine.ErrState
	}
	switch hand.Outcome {
	case "loss", "push", "win", "natural":
	default:
		return HandSettlement{}, engine.ErrState
	}
	r := HandSettlement{Stake: base * int64(hand.Units), Gross: base * int64(hand.ReturnHalves()) / 2, Outcome: hand.Outcome}
	cut := func(bp int) int64 {
		product := new(big.Int).Mul(big.NewInt(r.Gross), big.NewInt(int64(bp)))
		return product.Quo(product, big.NewInt(10000)).Int64()
	}
	r.Platform, r.Welfare, r.Thursday = cut(rates.Platform), cut(rates.Welfare), cut(rates.Thursday)
	r.Net = r.Gross - r.Platform - r.Welfare - r.Thursday
	return r, nil
}
