package ledger

import (
	"math/big"
	"sort"

	"github.com/waiting-here/NonbiriAPI/internal/db"
)

type DuelRates struct{ Platform, Welfare, Thursday int }
type DuelCuts struct{ Platform, Welfare, Thursday, Prize Amount }

// DuelAmounts floors each fee independently. All remaining fractional milli
// credits stay in the winner's prize; multiplication uses wide integers.
func DuelAmounts(ticket Amount, rates DuelRates) (DuelCuts, error) {
	if !positive(ticket) || !validPrimitive(ticket) || rates.Platform < 0 || rates.Platform > 9999 || rates.Welfare < 0 || rates.Welfare > 9999 || rates.Thursday < 0 || rates.Thursday > 9999 || rates.Platform+rates.Welfare+rates.Thursday >= 10000 {
		return DuelCuts{}, ErrInvalidPlan
	}
	cut := func(bp int) Amount {
		n := new(big.Int).Mul(ticket.Big(), big.NewInt(int64(bp)))
		n.Quo(n, big.NewInt(10000))
		a, _ := AmountFromBig(n)
		return a
	}
	r := DuelCuts{Platform: cut(rates.Platform), Welfare: cut(rates.Welfare), Thursday: cut(rates.Thursday)}
	prize := ticket.Big()
	prize.Sub(prize, r.Platform.Big()).Sub(prize, r.Welfare.Big()).Sub(prize, r.Thursday.Big())
	r.Prize, _ = AmountFromBig(prize)
	return r, nil
}

func NewDuelQueueReserve(meta Meta, id string, wallets, escrow AccountPair, payment Payment) (Plan, error) {
	total, err := paymentTotal(payment)
	if err != nil || !positive(total) || !validPrimitive(total) || !wallets.valid() || !escrow.valid() {
		return Plan{}, ErrInvalidPlan
	}
	p, err := newPlan(meta, KindDuelQueueReserve, sourceDuelQueue, id, db.U128{})
	if err != nil {
		return Plan{}, err
	}
	p = p.add(userRole(wallets.General), negate(payment.General)).add(reserveRole(escrow.General, "duel-queue:"+id), payment.General).
		add(roleForAsset(userRole(wallets.Game), Game), negate(payment.Game)).add(roleForAsset(reserveRole(escrow.Game, "duel-queue:"+id), Game), payment.Game)
	return requirePaymentAvailable(p, wallets, payment), nil
}

func NewDuelQueueRelease(meta Meta, id string, escrow, wallets AccountPair, payment Payment) (Plan, error) {
	total, err := paymentTotal(payment)
	if err != nil || !positive(total) || !validPrimitive(total) || !wallets.valid() || !escrow.valid() {
		return Plan{}, ErrInvalidPlan
	}
	ref, err := DuelQueueReservation(id)
	if err != nil {
		return Plan{}, err
	}
	p, err := newPlan(meta, KindDuelQueueRelease, sourceDuelQueue, id, db.U128{})
	if err != nil {
		return Plan{}, err
	}
	p = p.add(reserveRole(escrow.General, "duel-queue:"+id), negate(payment.General)).add(userRole(wallets.General), payment.General).
		add(roleForAsset(reserveRole(escrow.Game, "duel-queue:"+id), Game), negate(payment.Game)).add(roleForAsset(userRole(wallets.Game), Game), payment.Game)
	return p.consume(ref).requireZero(escrow.General).requireZero(escrow.Game), nil
}

type DuelQueuePayment struct {
	QueueID  string
	Accounts AccountPair
	Payment  Payment
}

// Start consumes one of the two queue holds, releases the other, and creates
// one session hold. It never requests a third future operation.
func NewDuelSessionStart(meta Meta, id string, escrow AccountPair, queues [2]DuelQueuePayment) (Plan, error) {
	if !escrow.valid() || meta.ActorUserID != 0 {
		return Plan{}, ErrInvalidPlan
	}
	ref, err := DuelSessionReservation(id)
	if err != nil {
		return Plan{}, err
	}
	p, err := newPlan(meta, KindDuelSessionStart, sourceDuelSession, id, db.U128{})
	if err != nil {
		return Plan{}, err
	}
	sort.Slice(queues[:], func(i, j int) bool { return queues[i].QueueID < queues[j].QueueID })
	total := Payment{}
	var ticket Amount
	for i, q := range queues {
		amount, err := paymentTotal(q.Payment)
		if err != nil || !positive(amount) || !validPrimitive(amount) || !q.Accounts.valid() || duelIDGame(q.QueueID, true) != duelIDGame(id, false) || i > 0 && (q.QueueID == queues[i-1].QueueID || amount.Big().Cmp(ticket.Big()) != 0) {
			return Plan{}, ErrInvalidPlan
		}
		ticket = amount
		queueRef, err := DuelQueueReservation(q.QueueID)
		if err != nil {
			return Plan{}, err
		}
		total.General, err = addAmounts(total.General, q.Payment.General)
		if err != nil {
			return Plan{}, err
		}
		total.Game, err = addAmounts(total.Game, q.Payment.Game)
		if err != nil {
			return Plan{}, err
		}
		p = p.add(reserveRole(q.Accounts.General, "duel-queue:"+q.QueueID), negate(q.Payment.General)).
			add(roleForAsset(reserveRole(q.Accounts.Game, "duel-queue:"+q.QueueID), Game), negate(q.Payment.Game)).requireZero(q.Accounts.General).requireZero(q.Accounts.Game)
		if i == 0 {
			p = p.consume(queueRef)
		} else {
			p.spec.capacity.releaseAll = append(p.spec.capacity.releaseAll, queueRef)
		}
	}
	p = p.add(reserveRole(escrow.General, "duel-session:"+id), total.General).
		add(roleForAsset(reserveRole(escrow.Game, "duel-session:"+id), Game), total.Game)
	one, _ := db.U128FromBig(big.NewInt(1))
	p.spec.capacity.reserve = &futureReservation{ref: ref, rows: one}
	return p, nil
}

type DuelSeatPayment struct {
	Wallets AccountPair
	Payment Payment
}
type DuelDestinations struct {
	External                    AccountPair
	Platform, Welfare, Thursday int64
}

// A nil winner refunds both original payments. Otherwise only the loser's
// game payment is exchanged; the winner's principal keeps its currency.
func NewDuelTerminal(meta Meta, id string, escrow AccountPair, seats [2]DuelSeatPayment, winner *int, rates DuelRates, dest DuelDestinations) (Plan, error) {
	if !escrow.valid() || meta.ActorUserID != 0 || winner != nil && (*winner < 0 || *winner > 1) {
		return Plan{}, ErrInvalidPlan
	}
	ref, err := DuelSessionReservation(id)
	if err != nil {
		return Plan{}, err
	}
	var ticket Amount
	for i, seat := range seats {
		total, err := paymentTotal(seat.Payment)
		if err != nil || !positive(total) || !validPrimitive(total) || !seat.Wallets.valid() || i > 0 && (total.Big().Cmp(ticket.Big()) != 0 || seat.Wallets.General == seats[0].Wallets.General || seat.Wallets.Game == seats[0].Wallets.Game) {
			return Plan{}, ErrInvalidPlan
		}
		ticket = total
	}
	cuts, err := DuelAmounts(ticket, rates)
	if err != nil {
		return Plan{}, err
	}
	p, err := newPlan(meta, KindDuelTerminal, sourceDuelSession, id, db.U128{})
	if err != nil {
		return Plan{}, err
	}
	refund := func(seat DuelSeatPayment) {
		p = p.add(reserveRole(escrow.General, "duel-session:"+id), negate(seat.Payment.General)).add(userRole(seat.Wallets.General), seat.Payment.General).
			add(roleForAsset(reserveRole(escrow.Game, "duel-session:"+id), Game), negate(seat.Payment.Game)).add(roleForAsset(userRole(seat.Wallets.Game), Game), seat.Payment.Game)
	}
	if winner == nil {
		for _, seat := range seats {
			refund(seat)
		}
	} else {
		loser := seats[1-*winner]
		if positive(loser.Payment.Game) {
			if !dest.External.valid() {
				return Plan{}, ErrInvalidPlan
			}
			p = p.add(roleForAsset(reserveRole(escrow.Game, "duel-session:"+id), Game), negate(loser.Payment.Game)).
				add(roleForAsset(externalRole(dest.External.Game), Game), loser.Payment.Game).
				add(externalRole(dest.External.General), negate(loser.Payment.Game)).add(reserveRole(escrow.General, "duel-session:"+id), loser.Payment.Game)
		}
		refund(seats[*winner])
		p = p.add(reserveRole(escrow.General, "duel-session:"+id), negate(ticket)).add(userRole(seats[*winner].Wallets.General), cuts.Prize)
		for _, cut := range []struct {
			amount Amount
			role   accountRole
		}{{cuts.Platform, platformRole(dest.Platform)}, {cuts.Welfare, poolRole(dest.Welfare)}, {cuts.Thursday, poolRole(dest.Thursday)}} {
			if positive(cut.amount) {
				if cut.role.id <= 0 {
					return Plan{}, ErrInvalidPlan
				}
				p = p.add(cut.role, cut.amount)
			}
		}
	}
	p = p.consume(ref).requireZero(escrow.General).requireZero(escrow.Game)
	// Refunds, exchange and prize can share an account. The sealed ledger
	// requires one posting per account; conflicting roles are rejected.
	entries := []entrySpec{}
	positions := map[int64]int{}
	for _, entry := range p.spec.entries {
		if index, ok := positions[entry.role.id]; ok {
			if entries[index].role != entry.role {
				return Plan{}, ErrInvalidPlan
			}
			n, err := addAmounts(entries[index].delta, entry.delta)
			if err != nil {
				return Plan{}, err
			}
			entries[index].delta = n
		} else {
			positions[entry.role.id] = len(entries)
			entries = append(entries, entry)
		}
	}
	p.spec.entries = entries
	return p, nil
}
