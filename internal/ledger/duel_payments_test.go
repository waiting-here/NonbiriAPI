package ledger

import (
	"math/big"
	"testing"
)

func TestDuelTerminalConservesAssetsAndOriginalPrincipal(t *testing.T) {
	meta := Meta{OperationID: mustLedgerID(t, "op_"), CreatedAt: ledgerTestNow}
	for _, ticket := range []int64{1, 7, 5_000_000, 9_000_000_000_000_000} {
		for _, g0 := range []int64{0, ticket / 2, ticket} {
			for _, g1 := range []int64{0, ticket / 3, ticket} {
				for winner := -1; winner < 2; winner++ {
					for _, rates := range []DuelRates{{}, {100, 100, 100}, {3333, 3333, 3333}} {
						seats := [2]DuelSeatPayment{{AccountPair{1, 2}, Payment{AmountFromMilli(ticket - g0), AmountFromMilli(g0)}}, {AccountPair{3, 4}, Payment{AmountFromMilli(ticket - g1), AmountFromMilli(g1)}}}
						var winning *int
						if winner >= 0 {
							winning = &winner
						}
						p, err := NewDuelTerminal(meta, mustLedgerID(t, "bid_"), AccountPair{5, 6}, seats, winning, rates, DuelDestinations{AccountPair{7, 8}, 9, 10, 11})
						if err != nil {
							t.Fatal(err)
						}
						if err := validatePlan(p); err != nil {
							t.Fatal("invalid sealed terminal plan", err)
						}
						assetSums := map[Asset]*big.Int{General: new(big.Int), Game: new(big.Int)}
						accounts := map[int64]*big.Int{}
						for _, entry := range p.spec.entries {
							assetSums[entry.role.asset].Add(assetSums[entry.role.asset], entry.delta.Big())
							if accounts[entry.role.id] == nil {
								accounts[entry.role.id] = new(big.Int)
							}
							accounts[entry.role.id].Add(accounts[entry.role.id], entry.delta.Big())
						}
						for _, sum := range assetSums {
							if sum.Sign() != 0 {
								t.Fatal("asset exchange is not zero-sum")
							}
						}
						amount := func(id int64) *big.Int {
							if accounts[id] == nil {
								return new(big.Int)
							}
							return accounts[id]
						}
						if amount(5).Cmp(big.NewInt(-2*ticket+g0+g1)) != 0 || amount(6).Cmp(big.NewInt(-g0-g1)) != 0 {
							t.Fatal("escrow not exhausted exactly")
						}
						cuts, err := DuelAmounts(AmountFromMilli(ticket), rates)
						if err != nil {
							t.Fatal(err)
						}
						for seat, payment := range seats {
							general, game := new(big.Int), new(big.Int)
							if winner == -1 || winner == seat {
								general = payment.Payment.General.Big()
								game = payment.Payment.Game.Big()
							}
							if winner == seat {
								general.Add(general, cuts.Prize.Big())
							}
							if amount(payment.Wallets.General).Cmp(general) != 0 || amount(payment.Wallets.Game).Cmp(game) != 0 {
								t.Fatal("incorrect original principal or prize")
							}
						}
						converted := int64(0)
						if winner >= 0 {
							converted = []int64{g0, g1}[1-winner]
						}
						if amount(7).Cmp(big.NewInt(-converted)) != 0 || amount(8).Cmp(big.NewInt(converted)) != 0 {
							t.Fatal("winner principal was converted")
						}
						if len(p.spec.entries) > 13 || p.spec.kind != KindDuelTerminal || p.spec.capacity.consume == nil {
							t.Fatal("terminal operation is not bounded and reserved")
						}
					}
				}
			}
		}
	}
}

func TestDuelFeeRoundingAndMatchCapacity(t *testing.T) {
	for _, tc := range []struct {
		ticket int64
		rates  DuelRates
		cuts   [4]int64
	}{{1, DuelRates{100, 100, 100}, [4]int64{0, 0, 0, 1}}, {7, DuelRates{3333, 3333, 3333}, [4]int64{2, 2, 2, 1}}, {5_000_000, DuelRates{100, 100, 100}, [4]int64{50_000, 50_000, 50_000, 4_850_000}}} {
		v, err := DuelAmounts(AmountFromMilli(tc.ticket), tc.rates)
		if err != nil {
			t.Fatal(err)
		}
		for i, a := range []Amount{v.Platform, v.Welfare, v.Thursday, v.Prize} {
			if a.Big().Cmp(big.NewInt(tc.cuts[i])) != 0 {
				t.Fatal("fee floor changed")
			}
		}
	}
	meta := Meta{OperationID: mustLedgerID(t, "op_"), CreatedAt: ledgerTestNow}
	queues := [2]DuelQueuePayment{{mustLedgerID(t, "likq_"), AccountPair{1, 2}, Payment{AmountFromMilli(3), AmountFromMilli(4)}}, {mustLedgerID(t, "likq_"), AccountPair{3, 4}, Payment{AmountFromMilli(5), AmountFromMilli(2)}}}
	p, err := NewDuelSessionStart(meta, mustLedgerID(t, "lik_"), AccountPair{5, 6}, queues)
	if err != nil {
		t.Fatal(err)
	}
	capacity := p.spec.capacity
	if capacity.consume == nil || len(capacity.releaseAll) != 1 || capacity.reserve == nil || capacity.reserve.rows.Big().Int64() != 1 {
		t.Fatal("two queue holds must become one consumed row and one session hold")
	}
	if _, err := NewDuelSessionStart(meta, mustLedgerID(t, "bid_"), AccountPair{5, 6}, queues); err == nil {
		t.Fatal("cross-game queues accepted")
	}
	queues[1] = queues[0]
	if _, err := NewDuelSessionStart(meta, mustLedgerID(t, "lik_"), AccountPair{5, 6}, queues); err == nil {
		t.Fatal("duplicate queue accepted")
	}
	for _, rates := range []DuelRates{{-1, 0, 0}, {10000, 0, 0}, {4000, 3000, 3000}} {
		if _, err := DuelAmounts(AmountFromMilli(7), rates); err == nil {
			t.Fatal("invalid rates accepted")
		}
	}
}
