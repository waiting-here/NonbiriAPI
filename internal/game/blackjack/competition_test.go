package blackjack_test

import (
	"encoding/json"
	"errors"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/game/blackjack"
	"github.com/waiting-here/NonbiriAPI/internal/game/blackjack/engine"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
)

func orderedCards(t *testing.T, values ...int) [engine.DeckSize]engine.Card {
	t.Helper()
	var deck [engine.DeckSize]engine.Card
	used := [engine.DeckSize]bool{}
	index := 0
	for _, value := range values {
		found := false
		for c := range engine.DeckSize {
			if !used[c] && engine.Card(c).Value() == value {
				deck[index] = engine.Card(c)
				used[c] = true
				index++
				found = true
				break
			}
		}
		if !found {
			t.Fatal("invalid fixture prefix")
		}
	}
	for c := range engine.DeckSize {
		if !used[c] {
			deck[index] = engine.Card(c)
			index++
		}
	}
	return deck
}
func (f *fixture) installPairTable() string {
	f.t.Helper()
	f.clock.Store(125)
	h := f.read(0)
	if h.Phase != "decision" {
		f.t.Fatal("fixture did not enter decision")
	}
	s, err := engine.Deal(orderedCards(f.t, 8, 10, 6, 8, 7, 10, 3, 2, 10, 10, 5), []int{0, 1}, 2)
	if err != nil {
		f.t.Fatal(err)
	}
	body, err := json.Marshal(s)
	if err != nil {
		f.t.Fatal(err)
	}
	f.exec(`UPDATE game_blackjack_sessions SET state_json=?,revision=revision+1 WHERE id=?`, string(body), h.Table.ID)
	return h.Table.ID
}
func (f *fixture) intent(user, hand int, kind string) blackjack.ActionInput {
	f.t.Helper()
	h := f.read(user)
	for _, seat := range h.Table.Fact.Cards.Seats {
		if seat.Number == *h.YourSeat {
			return blackjack.ActionInput{Identity: f.users[user], Key: f.id("op_"), SessionID: h.Table.ID, Hand: hand, Revision: strconv.FormatInt(seat.Hands[hand].Revision, 10), Action: kind}
		}
	}
	f.t.Fatal("missing hand")
	return blackjack.ActionInput{}
}
func (f *fixture) action(user, hand int, kind string) {
	f.t.Helper()
	r, err := f.s.Act(f.ctx, f.intent(user, hand, kind))
	if err != nil || r.Status != 202 {
		f.t.Fatal("action failed", kind, r.Status, err)
	}
	f.clock.Add(1)
	f.read(user)
}
func (f *fixture) balance(user int, asset ledger.Asset) int64 {
	f.t.Helper()
	tx, err := f.db.Begin()
	if err != nil {
		f.t.Fatal(err)
	}
	defer tx.Rollback()
	wallet, err := ledger.UserAssetAccount(f.ctx, tx, f.users[user].UserID, asset)
	if err != nil {
		f.t.Fatal(err)
	}
	return wallet.Balance.Big().Int64()
}

func TestSplitBothDoublesActualFourStakeLedgerAndCommittedRestart(t *testing.T) {
	f := newFixture(t, 2)
	f.join(0)
	f.join(1)
	f.installPairTable()
	f.action(0, 0, "split")
	f.action(0, 0, "double")
	f.action(0, 1, "double")
	if h := f.read(0); h.You.Payment.Game != "20000" || h.You.Payment.General != "0" {
		t.Fatal("four payments not reserved", h.You)
	}
	f.action(1, 0, "stand")
	h := f.read(0)
	if h.Phase != "result" || h.Table.Fact.Settlements[0].Hands[0].Net != 9_700_000 || h.Table.Fact.Settlements[0].Hands[1].Net != 0 {
		t.Fatal("incorrect two-hand result", h.Table.Fact.Settlements)
	}
	if f.balance(0, ledger.Game) != 5_000_000 || f.balance(0, ledger.General) != 109_700_000 {
		t.Fatal("incorrect normal currency conversion")
	}
	var operations int
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM credit_operations WHERE kind IN ('blackjack_reserve','blackjack_settle','blackjack_release')`).Scan(&operations); err != nil {
		t.Fatal(err)
	}
	f.s.Close()
	f.s = f.service()
	if f.read(0).Table.Phase != "result" || f.balance(0, ledger.General) != 109_700_000 {
		t.Fatal("restart reversed committed result")
	}
	var after int
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM credit_operations WHERE kind IN ('blackjack_reserve','blackjack_settle','blackjack_release')`).Scan(&after); err != nil || after != operations {
		t.Fatal("restart duplicated ledger", err)
	}
	f.recovery()
}

func TestAdditionalPaymentFailureLeavesHandAndReservationUnchanged(t *testing.T) {
	f := newFixture(t, 2)
	for i := range 2 {
		_, err := f.s.Enqueue(f.ctx, blackjack.EnqueueInput{Identity: f.users[i], Key: f.id("op_"), Stake: "50000", ConfigHash: f.read(i).ConfigHash})
		if err != nil {
			t.Fatal(err)
		}
	}
	f.installPairTable()
	f.action(0, 0, "split")
	before := f.read(0)
	_, err := f.s.Act(f.ctx, f.intent(0, 0, "double"))
	if !errors.Is(err, blackjack.ErrInsufficient) {
		t.Fatal("unfunded additional payment accepted", err)
	}
	after := f.read(0)
	if after.You.Pending || after.You.Payment != before.You.Payment || after.Table.Revision != before.Table.Revision {
		t.Fatal("failed double mutated hand/payment")
	}
	f.s.Close()
	f.s = f.service()
	if f.balance(0, ledger.Game) != 25_000_000 || f.balance(0, ledger.General) != 100_000_000 {
		t.Fatal("mixed abnormal refund changed assets")
	}
	f.recovery()
}

func TestRestartRefundsUnappliedAdditionalPaymentAndPreservesSummary(t *testing.T) {
	f := newFixture(t, 2)
	f.join(0)
	f.join(1)
	id := f.installPairTable()
	if _, err := f.s.Act(f.ctx, f.intent(0, 0, "split")); err != nil {
		t.Fatal(err)
	}
	f.recovery()
	f.s.Close()
	f.s = f.service()
	history, err := f.s.HistoryDetail(f.ctx, f.users[0], id)
	if err != nil {
		t.Fatal(err)
	}
	if history.Summary.TotalStake != "10000" || len(history.Table.Fact.Refunds) != 2 || history.Table.Fact.Refunds[0].Amount != "10000" {
		t.Fatal("pending split omitted from refund", history)
	}
	if f.balance(0, ledger.Game) != 25_000_000 {
		t.Fatal("pending payment not refunded")
	}
	f.recovery()
}

func TestEmoteVisibilityExpiryAndSpectatorOwnership(t *testing.T) {
	f := newFixture(t, 3)
	f.join(0)
	f.join(1)
	id := f.installPairTable()
	if _, err := f.s.Emote(f.ctx, f.users[2], f.id("op_"), id, "hello"); !errors.Is(err, blackjack.ErrNotFound) {
		t.Fatal("spectator emote", err)
	}
	f.action(0, 0, "stand")
	f.action(1, 0, "stand")
	if _, err := f.s.Emote(f.ctx, f.users[0], f.id("op_"), id, "gg"); err != nil {
		t.Fatal(err)
	}
	if f.read(2).Table.Fact.Seats[0].Emote != "gg" {
		t.Fatal("result emote invisible")
	}
	if _, err := f.s.Emote(f.ctx, f.users[0], f.id("op_"), id, "wow"); !errors.Is(err, blackjack.ErrRateLimited) {
		t.Fatal("emote throttle", err)
	}
	f.clock.Add(10)
	if f.read(2).Table.Fact.Seats[0].Emote != "" {
		t.Fatal("expired emote remained")
	}
}

func TestConcurrentWorkersCommitOnlyOneSettlementAndQueueOrder(t *testing.T) {
	f := newFixture(t, 10)
	for i := range 10 {
		f.join(i)
	}
	f.clock.Store(125)
	f.read(0)
	f.clock.Store(145)
	var wg sync.WaitGroup
	errors := make(chan error, 12)
	for range 12 {
		wg.Go(func() { _, err := f.s.RecoverBeforeListen(f.ctx, 145, 128, time.Now().Add(time.Minute)); errors <- err })
	}
	wg.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	if h := f.read(9); h.Phase != "result" || h.You.State != "waiting" || h.You.Position != "1" {
		t.Fatal("worker altered queue/table", h)
	}
	var resolved, paid int
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM game_blackjack_payments WHERE state='settled'`).Scan(&paid); err != nil {
		t.Fatal(err)
	}
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM game_blackjack_events WHERE kind='result'`).Scan(&resolved); err != nil || resolved != 1 || paid != 9 {
		t.Fatal("duplicate/missing terminal facts", resolved, paid, err)
	}
	f.recovery()
}
