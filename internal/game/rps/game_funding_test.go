package rps

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math/big"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/game"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
)

func (fixture *rpsFixture) fundGameWallet(userID, amount int64) {
	fixture.t.Helper()
	ctx := context.Background()
	tx, err := fixture.database.BeginTx(ctx, nil)
	if err != nil {
		fixture.t.Fatal(err)
	}
	defer tx.Rollback()
	wallet, err := ledger.CreateUserAssetAccount(ctx, tx, userID, ledger.Game, fixture.clock.Load())
	if err != nil {
		fixture.t.Fatal(err)
	}
	external, err := ledger.CodedAssetAccount(ctx, tx, "external", ledger.Game)
	if err != nil {
		fixture.t.Fatal(err)
	}
	plan, err := ledger.NewAdminGameAdjustment(ledger.Meta{OperationID: fixture.mustID("op_"), ActorUserID: fixture.adminID, CreatedAt: fixture.clock.Load()}, wallet.ID, external.ID, ledger.AmountFromMilli(amount), "game wallet funding")
	if err != nil {
		fixture.t.Fatal(err)
	}
	if _, err := ledger.Apply(ctx, tx, plan); err != nil {
		fixture.t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		fixture.t.Fatal(err)
	}
}

func (fixture *rpsFixture) enqueueVersion(userID int64, mode string, version, key int) QueueMutationResult {
	fixture.t.Helper()
	raw := make([]byte, 32)
	raw[0], raw[1] = byte(userID), byte(userID>>8)
	result, err := fixture.service.enqueue(context.Background(), EnqueueInput{UserID: userID, Mode: mode,
		DeviceToken: base64.RawURLEncoding.EncodeToString(raw), CanonicalSourceIP: [16]byte{14: byte(userID >> 8), 15: byte(userID)},
		DeathmatchConfirmed: mode == game.RPSModeDeathmatch, IdempotencyKey: fixture.key(key)}, version)
	if err != nil {
		fixture.t.Fatal(err)
	}
	return result
}

func (fixture *rpsFixture) assertWallets(userID, general, gameAmount int64) {
	fixture.t.Helper()
	tx := fixture.mustReadTx()
	defer tx.Rollback()
	for _, want := range []struct {
		asset  ledger.Asset
		amount int64
	}{{ledger.General, general}, {ledger.Game, gameAmount}} {
		actual, err := ledger.UserAssetAccount(context.Background(), tx, userID, want.asset)
		if err != nil || actual.Balance.Big().Cmp(big.NewInt(want.amount)) != 0 {
			fixture.t.Fatalf("wallet %s: %+v err=%v want=%d", want.asset, actual, err, want.amount)
		}
	}
}

func (fixture *rpsFixture) assertLedgerRecovery() {
	fixture.t.Helper()
	tx := fixture.mustReadTx()
	defer tx.Rollback()
	if err := ledger.ValidateRecovery(context.Background(), tx); err != nil {
		fixture.t.Fatal(err)
	}
}

func TestMixedQueueRefundPreservesOriginalAssets(t *testing.T) {
	fixture := newRPSFixture(t)
	user, _ := fixture.seedUser("mixed-refund", 4_000)
	fixture.fundGameWallet(user, 3_000)
	queued := fixture.enqueueVersion(user, game.RPSModeStandard, 2, 90001)
	if queued.Queue.RulesVersion != 2 || queued.Queue.Payment == nil || *queued.Queue.Payment != (Payment{General: "2", Game: "3"}) {
		t.Fatalf("payment=%+v", queued.Queue)
	}
	fixture.assertWallets(user, 2_000, 0)
	// A later adjustment must not cause cancellation to select a new split.
	ctx := context.Background()
	tx, err := fixture.database.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	wallet, err := ledger.UserAssetAccount(ctx, tx, user, ledger.Game)
	if err != nil {
		t.Fatal(err)
	}
	external, err := ledger.CodedAssetAccount(ctx, tx, "external", ledger.Game)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := ledger.NewAdminGameAdjustment(ledger.Meta{OperationID: fixture.mustID("op_"), ActorUserID: fixture.adminID, CreatedAt: fixture.clock.Load()}, wallet.ID, external.ID, ledger.AmountFromMilli(-2_000), "adjustment after entry")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.Apply(ctx, tx, plan); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	input := CancelInput{UserID: user, QueueID: queued.Queue.ID, ExpectedRevision: queued.Queue.Revision, IdempotencyKey: fixture.key(90002)}
	if _, err := fixture.service.Cancel(ctx, input); err != nil {
		t.Fatal(err)
	}
	if replay, err := fixture.service.Cancel(ctx, input); err != nil || !replay.IdempotentReplay {
		t.Fatalf("cancel replay=%+v %v", replay, err)
	}
	fixture.assertWallets(user, 4_000, 1_000)
	if fixture.scalar("SELECT COUNT(*) FROM game_onboarding_holds") != 0 || fixture.scalar("SELECT COUNT(*) FROM game_onboarding_completions") != 0 {
		t.Fatal("cancel retained a reward or hold")
	}
	if fixture.scalar(`SELECT COUNT(*) FROM credit_operations WHERE kind='rps_queue_release'`) != 1 {
		t.Fatal("duplicate refund")
	}
	fixture.assertLedgerRecovery()
}

func TestQueueMatchKeepsRuleVersionsSeparate(t *testing.T) {
	fixture := newRPSFixture(t)
	users := make([]int64, 6)
	for i := range users {
		users[i], _ = fixture.seedUser("version-"+string(rune('a'+i)), 4_000)
		fixture.fundGameWallet(users[i], 3_000)
		fixture.enqueueVersion(users[i], game.RPSModeQuick, 1+i%2, 90100+i)
		fixture.clock.Add(1)
	}
	for version := 1; version <= 2; version++ {
		matched, err := fixture.service.MatchOnce(context.Background(), game.RPSModeQuick)
		if err != nil || !matched {
			t.Fatalf("match %d: %v %v", version, matched, err)
		}
		record := fixture.sessionForUser(users[version-1])
		if record.RulesVersion != version {
			t.Fatalf("version=%d", record.RulesVersion)
		}
		for _, seat := range record.Seats {
			index := -1
			for i, user := range users {
				if seat.UserID != nil && user == *seat.UserID {
					index = i
					break
				}
			}
			if index < 0 || 1+index%2 != version {
				t.Fatal("mixed-version match")
			}
		}
	}
	fixture.assertLedgerRecovery()
}

func TestMixedStandardInputsTerminalRetryAndOriginalFunding(t *testing.T) {
	for _, deleted := range []bool{false, true} {
		name := "surviving"
		if deleted {
			name = "deidentified"
		}
		t.Run(name, func(t *testing.T) {
			fixture := newRPSFixture(t)
			fixture.setRPSConfig(true, 1_000, PumpsBP{Platform: 9_000})
			users := make([]int64, 3)
			bindings := map[int64]string{}
			for i := range users {
				user, binding := fixture.seedUser(name+string(rune('a'+i)), 4_000)
				users[i], bindings[user] = user, binding
				fixture.fundGameWallet(users[i], 3_000)
				fixture.enqueueVersion(users[i], game.RPSModeStandard, 2, 90200+i)
			}
			thursdayPools := fixture.scalar(`SELECT COUNT(*) FROM shared_pools WHERE pool_type='thursday'`)
			matched, err := fixture.service.MatchOnce(context.Background(), game.RPSModeStandard)
			if err != nil || !matched {
				t.Fatalf("match=%v %v", matched, err)
			}
			record := fixture.sessionForUser(users[0])
			for _, seat := range record.Seats {
				if seat.GameBuyIn.Decimal() != "3000" || seat.GameRemaining.Decimal() != "2000" || seat.CurrentBalance.Decimal() != "4000" {
					t.Fatalf("initial seat=%+v", seat)
				}
			}
			if fixture.scalar(`SELECT COUNT(*) FROM shared_pools WHERE pool_type='thursday'`) != thursdayPools {
				t.Fatal("zero cuts created Thursday pool")
			}
			projected, err := projectState(record, users[0], fixture.clock.Load())
			if err != nil {
				t.Fatal(err)
			}
			for _, seat := range projected.Seats {
				if seat.Viewer == "self" {
					if seat.Funding == nil || *seat.Funding != (Funding{"2", "3", "2", "2"}) {
						t.Fatalf("funding=%+v", seat.Funding)
					}
				} else if seat.Funding != nil {
					t.Fatal("opponent funding disclosed")
				}
			}
			key := 90210
			fixture.playGestures(record.ID, bindings, [3]string{GestureRock, GestureScissors, GesturePaper}, &key)
			fixture.dealerDecision(record.ID, bindings, "raise", "4", &key)
			record = fixture.sessionForUser(users[0])
			dealer := *record.DealerSeat
			if record.Seats[dealer].GameRemaining.Decimal() != "0" || record.Seats[dealer].CurrentBalance.Decimal() != "0" {
				t.Fatal("raise did not spend game principal first")
			}
			followers := []int{}
			for seat := range record.Seats {
				if seat != dealer {
					followers = append(followers, seat)
				}
			}
			fixture.service.beforeTerminalCommit = func() error { return errors.New("injected terminal failure") }
			for i, seat := range followers {
				current := fixture.sessionForUser(users[0])
				user := *current.Seats[seat].UserID
				decision := FollowerCall
				if i == 1 {
					decision = FollowerSurrender
				}
				_, err := fixture.service.Action(context.Background(), ActionInput{UserID: user, SessionBinding: bindings[user], SessionID: current.ID,
					PhaseSeq: current.PhaseSeq.Decimal(), ExpectedRevision: current.Revision.Decimal(), Action: "follower_decision", FollowerDecision: decision, IdempotencyKey: fixture.key(key)})
				key++
				if err != nil {
					t.Fatal(err)
				}
			}
			processing := fixture.sessionForUser(users[0])
			if processing.State != StateTerminalProcessing || *processing.TerminalReason != TerminalStandardInsufficient {
				t.Fatalf("terminal state=%s phase=%s reason=%v", processing.State, processing.Phase, processing.TerminalReason)
			}
			surrender := followers[1]
			if processing.Seats[surrender].GameRemaining.Decimal() != "2000" || processing.Seats[surrender].TerminalReturn.Decimal() != "4000" {
				t.Fatal("unused game principal lost before cashout")
			}
			if fixture.scalar(`SELECT COUNT(*) FROM credit_operations WHERE kind='rps_terminal'`) != 0 || fixture.scalar(`SELECT COUNT(*) FROM game_rps_pending_results`) != 0 || fixture.scalar("SELECT COUNT(*) FROM game_onboarding_completions") != 0 ||
				fixture.scalar("SELECT COUNT(*) FROM credit_operations WHERE kind='game_onboarding_reward'") != 0 {
				t.Fatal("failed terminal committed money or pending facts")
			}
			fixture.assertLedgerRecovery()
			deletedUser := int64(0)
			if deleted {
				deletedUser = *processing.Seats[surrender].UserID
				tx, err := fixture.database.BeginTx(context.Background(), nil)
				if err != nil {
					t.Fatal(err)
				}
				finalizer, err := fixture.service.Lifecycle().PrepareUserDeletion(context.Background(), tx, deletedUser, fixture.clock.Load())
				if err != nil {
					_ = tx.Rollback()
					t.Fatal(err)
				}
				if err := tx.Commit(); err != nil {
					t.Fatal(err)
				}
				if !finalizer.Commit() {
					t.Fatal("deletion finalizer")
				}
			}
			fixture.service.beforeTerminalCommit = nil
			completed, err := fixture.service.runTerminalOne(context.Background(), record.ID, *processing.TerminalNextRetryAt)
			if err != nil || !completed {
				t.Fatalf("retry=%v %v", completed, err)
			}
			if completed, err := fixture.service.runTerminalOne(context.Background(), record.ID, *processing.TerminalNextRetryAt); err != nil || completed {
				t.Fatalf("repeated terminal=%v %v", completed, err)
			}
			for _, seat := range processing.Seats {
				user := *seat.UserID
				want := int64(2_000)
				if user != deletedUser {
					want += seat.TerminalReturn.Big().Int64() + 2_000_000
				}
				fixture.assertWallets(user, want, 0)
				tx := fixture.mustReadTx()
				pending, found, err := loadPending(context.Background(), tx, user)
				_ = tx.Rollback()
				if user == deletedUser {
					if err != nil || found {
						t.Fatal("deleted seat got a pending result")
					}
					continue
				}
				if err != nil || !found || pending.RulesVersion != 2 || pending.OwnBuyInGeneral == nil || *pending.OwnBuyInGeneral != "2" ||
					pending.OwnBuyInGame == nil || *pending.OwnBuyInGame != "3" || pending.OwnReturnedGeneral == nil || *pending.OwnReturnedGeneral != formatMilli(seat.TerminalReturn.Big()) {
					t.Fatalf("pending=%+v err=%v", pending, err)
				}
				wire, err := json.Marshal(HomeState{Kind: "pending_result", Result: &pending})
				if err != nil {
					t.Fatal(err)
				}
				if _, err := decodeHomeState(wire); err != nil {
					t.Fatal(err)
				}
			}
			fixture.assertLedgerRecovery()
		})
	}
}

func TestMixedPaidTiesUseGameOnceAndLeaveFreeRoundsUncharged(t *testing.T) {
	fixture := newRPSFixture(t)
	fixture.setRPSConfig(true, 1_000, PumpsBP{})
	users := make([]int64, 3)
	bindings := map[int64]string{}
	for i := range users {
		user, binding := fixture.seedUser("paid-game-"+string(rune('a'+i)), 4_000)
		users[i], bindings[user] = user, binding
		fixture.fundGameWallet(user, 3_000)
		fixture.enqueueVersion(user, game.RPSModeStandard, 2, 90300+i)
	}
	if matched, err := fixture.service.MatchOnce(context.Background(), game.RPSModeStandard); err != nil || !matched {
		t.Fatal(matched, err)
	}
	record := fixture.sessionForUser(users[0])
	tie := [3]string{GestureRock, GestureRock, GestureRock}
	key := 90310
	fixture.playGestures(record.ID, bindings, tie, &key)
	fixture.dealerDecision(record.ID, bindings, "no_raise", "", &key)
	record = fixture.sessionForUser(users[0])
	for _, seat := range record.Seats {
		if seat.GameRemaining.Decimal() != "1000" {
			t.Fatal("paid continuation source")
		}
	}
	fixture.playGestures(record.ID, bindings, tie, &key)
	record = fixture.sessionForUser(users[0])
	for _, seat := range record.Seats {
		if seat.GameRemaining.Decimal() != "0" || seat.CurrentBalance.Decimal() != "2000" {
			t.Fatal("game principal not exhausted exactly")
		}
	}
	for paid := 0; paid < 3; paid++ {
		fixture.playGestures(record.ID, bindings, tie, &key)
	}
	record = fixture.sessionForUser(users[0])
	if record.Phase != PhaseFreePoolGesture {
		t.Fatalf("phase=%s", record.Phase)
	}
	operations := fixture.scalar(`SELECT COUNT(*) FROM credit_operations WHERE kind='rps_round_cut'`)
	if operations != 3 {
		t.Fatalf("conversion operations=%d want=3", operations)
	}
	fixture.playGestures(record.ID, bindings, tie, &key)
	if fixture.scalar(`SELECT COUNT(*) FROM credit_operations WHERE kind='rps_round_cut'`) != operations {
		t.Fatal("free round charged")
	}
	fixture.assertLedgerRecovery()
}

func TestMixedDeathmatchAllInIgnoresDebtInOtherWallet(t *testing.T) {
	fixture := newRPSFixture(t)
	amounts := [3][2]int64{{-200, 1_000}, {400, 600}, {1_000, -200}}
	users := [3]int64{}
	bindings := map[int64]string{}
	for i, amount := range amounts {
		user, binding := fixture.seedUser("all-in-"+string(rune('a'+i)), amount[0])
		users[i], bindings[user] = user, binding
		fixture.fundGameWallet(user, amount[1])
		queue := fixture.enqueueVersion(user, game.RPSModeDeathmatch, 2, 90400+i)
		if queue.Queue.Payment == nil || *queue.Queue.Payment != (Payment{General: game.FormatAmount(max(0, amount[0])), Game: game.FormatAmount(max(0, amount[1]))}) {
			t.Fatalf("all-in payment=%+v", queue.Queue.Payment)
		}
		fixture.assertWallets(user, min(0, amount[0]), min(0, amount[1]))
	}
	if matched, err := fixture.service.MatchOnce(context.Background(), game.RPSModeDeathmatch); err != nil || !matched {
		t.Fatal(matched, err)
	}
	record := fixture.sessionForUser(users[0])
	if record.Phase != PhaseUltimateGesture {
		t.Fatalf("all-in phase=%s", record.Phase)
	}
	for _, seat := range record.Seats {
		if seat.GameRemaining.Big().Sign() != 0 || seat.CurrentBalance.Big().Sign() != 0 {
			t.Fatal("all-in left spendable principal")
		}
	}
	key := 90410
	fixture.playGestures(record.ID, bindings, [3]string{GestureRock, GestureScissors, GestureScissors}, &key)
	if fixture.scalar("SELECT COUNT(*) FROM game_rps_pending_results") != 3 ||
		fixture.scalar("SELECT COUNT(*) FROM game_onboarding_completions") != 3 ||
		fixture.scalar("SELECT SUM(award_milli) FROM game_onboarding_completions") != 15_000_000 {
		t.Fatal("all-in terminal results or independent rewards missing")
	}
	fixture.assertLedgerRecovery()
}

func TestMixedCancelWaitsForMatchCommitOrRollback(t *testing.T) {
	for _, rollback := range []bool{false, true} {
		name := "commit"
		if rollback {
			name = "rollback"
		}
		t.Run(name, func(t *testing.T) {
			fixture := newRPSFixture(t)
			users := [3]int64{}
			queues := [3]QueueMutationResult{}
			for i := range users {
				users[i], _ = fixture.seedUser("match-cancel-"+string(rune('a'+i)), 700)
				fixture.fundGameWallet(users[i], 600)
				queues[i] = fixture.enqueueVersion(users[i], game.RPSModeQuick, 2, 90500+i)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			reached, release := make(chan struct{}), make(chan struct{})
			fixture.service.beforeMatchCommit = func() error {
				close(reached)
				select {
				case <-release:
				case <-ctx.Done():
					return ctx.Err()
				}
				if rollback {
					return errors.New("injected match rollback")
				}
				return nil
			}
			matchDone := make(chan error, 1)
			go func() {
				matched, err := fixture.service.MatchOnce(ctx, game.RPSModeQuick)
				if err == nil && !matched {
					err = errors.New("match was unexpectedly empty")
				}
				matchDone <- err
			}()
			select {
			case <-reached:
			case err := <-matchDone:
				t.Fatalf("match failed before commit hook: %v", err)
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			cancelStarted, cancelDone := make(chan struct{}), make(chan error, 1)
			input := CancelInput{UserID: users[0], QueueID: queues[0].Queue.ID, ExpectedRevision: queues[0].Queue.Revision, IdempotencyKey: fixture.key(90510)}
			go func() {
				close(cancelStarted)
				_, err := fixture.service.Cancel(ctx, input)
				cancelDone <- err
			}()
			<-cancelStarted
			close(release)
			matchErr, cancelErr := <-matchDone, <-cancelDone
			if rollback {
				if !errors.Is(matchErr, ErrServiceUnavailable) || cancelErr != nil {
					t.Fatalf("rollback match=%v cancel=%v", matchErr, cancelErr)
				}
				fixture.assertWallets(users[0], 700, 600)
				if fixture.scalar("SELECT COUNT(*) FROM credit_operations WHERE kind='rps_queue_release'") != 1 ||
					fixture.scalar("SELECT COUNT(*) FROM game_rps_sessions") != 0 {
					t.Fatal("rollback refund did not converge")
				}
			} else {
				if matchErr != nil || !errors.Is(cancelErr, ErrNotFound) {
					t.Fatalf("commit match=%v cancel=%v", matchErr, cancelErr)
				}
				fixture.assertWallets(users[0], 300, 0)
				if fixture.scalar("SELECT COUNT(*) FROM credit_operations WHERE kind='rps_queue_release'") != 0 ||
					fixture.scalar("SELECT COUNT(*) FROM game_rps_sessions") != 1 {
					t.Fatal("matched queue was refunded")
				}
			}
			fixture.assertLedgerRecovery()
		})
	}
}
