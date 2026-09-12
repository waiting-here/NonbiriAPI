package rps

import (
	"context"
	"encoding/base64"
	"errors"
	"math"
	"math/big"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/game"
)

func TestOnboardingAutomaticQuickCompletionAndReplay(t *testing.T) {
	fixture := newRPSFixture(t)
	users, bindings := [3]int64{}, map[int64]string{}
	returned := map[int64]int64{}
	for i := range users {
		user, binding := fixture.seedUser("automatic-newcomer-"+string(rune('a'+i)), 4_000)
		users[i], bindings[user] = user, binding
		fixture.fundGameWallet(user, 3_000)
	}
	for round := 0; round < 2; round++ {
		for i, user := range users {
			fixture.enqueueVersion(user, game.RPSModeQuick, 2, 90600+round*10+i)
		}
		wantHolds := int64(3)
		if round == 1 {
			wantHolds = 0
		}
		if fixture.scalar("SELECT COUNT(*) FROM game_onboarding_holds WHERE rps_queue_id IS NOT NULL") != wantHolds {
			t.Fatal("queue reward holds")
		}
		if matched, err := fixture.service.MatchOnce(context.Background(), game.RPSModeQuick); err != nil || !matched {
			t.Fatal(matched, err)
		}
		record := fixture.sessionForUser(users[0])
		if fixture.scalar("SELECT COUNT(*) FROM game_onboarding_holds WHERE rps_queue_id IS NOT NULL") != 0 ||
			fixture.scalar("SELECT COUNT(*) FROM game_onboarding_holds WHERE rps_session_id=?", record.ID) != wantHolds {
			t.Fatal("queue hold transfer")
		}
		fixture.clock.Store(*record.PhaseDeadline)
		if processed, err := fixture.service.runDeadlineOne(context.Background(), record.ID, fixture.clock.Load()); err != nil || !processed {
			t.Fatal("automatic terminal", processed, err)
		}
		for _, user := range users {
			tx := fixture.mustReadTx()
			pending, found, err := loadPending(context.Background(), tx, user)
			_ = tx.Rollback()
			if err != nil || !found || pending.OwnReturnedGeneral == nil {
				t.Fatal("automatic cashout facts", err)
			}
			cashout, err := game.ParseAmount(*pending.OwnReturnedGeneral)
			if err != nil {
				t.Fatal(err)
			}
			returned[user] += cashout
			fixture.assertWallets(user, 4_000+1_000_000+returned[user], 3_000-int64(round+1)*1_000)
			if _, err := fixture.service.ACK(context.Background(), ACKInput{UserID: user, SessionBinding: bindings[user], SessionID: record.ID}); err != nil {
				t.Fatal(err)
			}
		}
		if fixture.scalar("SELECT COUNT(*) FROM game_onboarding_completions") != 3 ||
			fixture.scalar("SELECT COUNT(*) FROM credit_operations WHERE kind='game_onboarding_reward'") != 3 ||
			fixture.scalar("SELECT COUNT(*) FROM game_onboarding_holds") != 0 {
			t.Fatal("automatic lifetime rewards repeated or leaked")
		}
		fixture.assertLedgerRecovery()
	}
}

func TestOnboardingRewardUsesItsAcceptedCapacityAtFinalSequence(t *testing.T) {
	fixture := newRPSFixture(t)
	users := [3]int64{}
	for i := range users {
		users[i], _ = fixture.seedUser("last-capacity-"+string(rune('a'+i)), 0)
		fixture.fundGameWallet(users[i], 1_000)
		fixture.enqueueVersion(users[i], game.RPSModeQuick, 2, 90700+i)
	}
	if matched, err := fixture.service.MatchOnce(context.Background(), game.RPSModeQuick); err != nil || !matched {
		t.Fatal(matched, err)
	}
	record := fixture.sessionForUser(users[0])
	one, _ := db.U128FromBig(big.NewInt(1))
	four, _ := db.U128FromBig(big.NewInt(4))
	// Model a valid ledger prefix ending four sequence numbers from capacity:
	// three independent accepted rewards and the game's last terminal row.
	if _, err := fixture.database.Exec("UPDATE game_rps_sessions SET ledger_rows_remaining=? WHERE id=?", db.EncodeU128(one), record.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.database.Exec("UPDATE credit_capacity SET last_ledger_seq=?,reserved_future_rows=? WHERE id=1", int64(math.MaxInt64-4), db.EncodeU128(four)); err != nil {
		t.Fatal(err)
	}
	fixture.clock.Store(*record.PhaseDeadline)
	if processed, err := fixture.service.runDeadlineOne(context.Background(), record.ID, fixture.clock.Load()); err != nil || !processed {
		t.Fatal(processed, err)
	}
	if fixture.scalar("SELECT last_ledger_seq FROM credit_capacity WHERE id=1") != math.MaxInt64 ||
		fixture.scalar("SELECT COUNT(*) FROM game_onboarding_completions") != 3 ||
		fixture.scalar("SELECT COUNT(*) FROM game_onboarding_holds") != 0 ||
		fixture.scalar("SELECT COUNT(*) FROM game_rps_sessions") != 0 {
		t.Fatal("accepted obligations failed at capacity boundary")
	}
	var remaining []byte
	if err := fixture.database.QueryRow("SELECT reserved_future_rows FROM credit_capacity WHERE id=1").Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	value, err := db.DecodeU128(remaining)
	if err != nil || value.Big().Sign() != 0 {
		t.Fatal("capacity not fully consumed", err)
	}
}

func TestOnboardingAdmissionCapacityFailureRollsBackPayment(t *testing.T) {
	fixture := newRPSFixture(t)
	user, _ := fixture.seedUser("admission-capacity", 4_000)
	fixture.fundGameWallet(user, 3_000)
	operations := fixture.scalar("SELECT COUNT(*) FROM credit_operations")
	if _, err := fixture.database.Exec("UPDATE credit_capacity SET last_ledger_seq=? WHERE id=1", int64(math.MaxInt64-2)); err != nil {
		t.Fatal(err)
	}
	raw := make([]byte, 32)
	raw[0] = 1
	_, err := fixture.service.enqueue(context.Background(), EnqueueInput{UserID: user, Mode: game.RPSModeQuick,
		DeviceToken: base64.RawURLEncoding.EncodeToString(raw), IdempotencyKey: fixture.key(90800)}, 2)
	if !errors.Is(err, ErrServiceUnavailable) {
		t.Fatalf("admission error=%v", err)
	}
	if fixture.scalar("SELECT COUNT(*) FROM game_rps_queue") != 0 || fixture.scalar("SELECT COUNT(*) FROM game_onboarding_holds") != 0 ||
		fixture.scalar("SELECT COUNT(*) FROM credit_operations") != operations {
		t.Fatal("failed admission left partial effects")
	}
	fixture.assertWallets(user, 4_000, 3_000)
}

func TestOnboardingAwardsRollBackWithFailedTerminalFacts(t *testing.T) {
	fixture := newRPSFixture(t)
	users := [3]int64{}
	for i := range users {
		users[i], _ = fixture.seedUser("terminal-atomic-"+string(rune('a'+i)), 4_000)
		fixture.fundGameWallet(users[i], 3_000)
		fixture.enqueueVersion(users[i], game.RPSModeQuick, 2, 90900+i)
	}
	if matched, err := fixture.service.MatchOnce(context.Background(), game.RPSModeQuick); err != nil || !matched {
		t.Fatal(matched, err)
	}
	record := fixture.sessionForUser(users[0])
	// This is after all reward operations, inside ordinary terminal settlement.
	if _, err := fixture.database.Exec("CREATE TRIGGER reject_terminal_facts BEFORE INSERT ON game_rps_summaries BEGIN SELECT RAISE(ABORT,'injected terminal fact failure'); END"); err != nil {
		t.Fatal(err)
	}
	fixture.clock.Store(*record.PhaseDeadline)
	if processed, err := fixture.service.runDeadlineOne(context.Background(), record.ID, fixture.clock.Load()); err != nil || !processed {
		t.Fatal(processed, err)
	}
	retry := fixture.sessionForUser(users[0])
	if retry.State != StateTerminalProcessing || retry.TerminalNextRetryAt == nil ||
		fixture.scalar("SELECT COUNT(*) FROM game_onboarding_completions") != 0 ||
		fixture.scalar("SELECT COUNT(*) FROM credit_operations WHERE kind='game_onboarding_reward'") != 0 ||
		fixture.scalar("SELECT COUNT(*) FROM credit_operations WHERE kind='rps_terminal'") != 0 ||
		fixture.scalar("SELECT COUNT(*) FROM game_onboarding_holds") != 3 {
		t.Fatal("failed terminal retained reward effects")
	}
	for _, user := range users {
		fixture.assertWallets(user, 4_000, 2_000)
	}
	fixture.assertLedgerRecovery()
	if _, err := fixture.database.Exec("DROP TRIGGER reject_terminal_facts"); err != nil {
		t.Fatal(err)
	}
	if completed, err := fixture.service.runTerminalOne(context.Background(), record.ID, *retry.TerminalNextRetryAt); err != nil || !completed {
		t.Fatal(completed, err)
	}
	if fixture.scalar("SELECT COUNT(*) FROM game_onboarding_completions") != 3 ||
		fixture.scalar("SELECT COUNT(*) FROM credit_operations WHERE kind='game_onboarding_reward'") != 3 ||
		fixture.scalar("SELECT COUNT(*) FROM game_onboarding_holds") != 0 {
		t.Fatal("retry did not converge")
	}
	fixture.assertLedgerRecovery()
}

func TestOnboardingOtherNormalTerminalReasonsAndRetention(t *testing.T) {
	for _, test := range []struct {
		mode, reason string
		reward       int64
	}{
		{game.RPSModeStandard, TerminalStandardRoundLimit, 2_000_000},
		{game.RPSModeDeathmatch, TerminalDeathmatchExhausted, 5_000_000},
		{game.RPSModeStandard, TerminalFreeTieLimit, 2_000_000},
	} {
		t.Run(test.reason, func(t *testing.T) {
			fixture := newRPSFixture(t)
			users := [3]int64{}
			bindings := map[int64]string{}
			for i := range users {
				user, binding := fixture.seedUser("normal-terminal-"+string(rune('a'+i)), 0)
				users[i], bindings[user] = user, binding
				funding := int64(5_000)
				if test.mode == game.RPSModeDeathmatch {
					funding = int64(i+1) * 1_000
				}
				fixture.fundGameWallet(user, funding)
				fixture.enqueueVersion(user, test.mode, 2, 91000+i)
			}
			if matched, err := fixture.service.MatchOnce(context.Background(), test.mode); err != nil || !matched {
				t.Fatal(matched, err)
			}
			record := fixture.sessionForUser(users[0])
			key := 91100
			for step := 0; ; step++ {
				current, found := fixture.maybeSession(record.ID)
				if !found {
					break
				}
				if step == 16 {
					t.Fatal("normal game did not terminate")
				}
				gestures := [3]string{GestureRock, GestureRock, GestureRock}
				if test.reason != TerminalFreeTieLimit {
					gestures = [3]string{GestureScissors, GestureScissors, GestureScissors}
					winner := step % 3
					gestures[winner] = GestureRock
					if test.reason == TerminalDeathmatchExhausted {
						loser := 0
						for i := range current.Seats {
							if current.Seats[i].StartingBalance.Big().Cmp(current.Seats[loser].StartingBalance.Big()) < 0 {
								loser = i
							}
						}
						// Two winners leave the all-in losing seat with no return.
						gestures = [3]string{GestureRock, GestureRock, GestureRock}
						gestures[loser] = GestureScissors
					}
				}
				fixture.playGestures(record.ID, bindings, gestures, &key)
				if next, exists := fixture.maybeSession(record.ID); exists && next.Phase == PhaseDealerRaise {
					fixture.dealerDecision(record.ID, bindings, "no_raise", "", &key)
				}
			}
			for _, user := range users {
				tx := fixture.mustReadTx()
				pending, found, err := loadPending(context.Background(), tx, user)
				_ = tx.Rollback()
				if err != nil || !found || pending.TerminalReason != test.reason || pending.OwnReturnedGeneral == nil {
					t.Fatal(pending, found, err)
				}
				cashout, err := game.ParseAmount(*pending.OwnReturnedGeneral)
				if err != nil {
					t.Fatal(err)
				}
				fixture.assertWallets(user, cashout+test.reward, 0)
				if _, err := fixture.service.ACK(context.Background(), ACKInput{UserID: user, SessionBinding: bindings[user], SessionID: record.ID}); err != nil {
					t.Fatal(err)
				}
			}
			if fixture.scalar("SELECT COUNT(*) FROM game_onboarding_completions WHERE game_key='rps' AND task_key=?", test.mode) != 3 ||
				fixture.scalar("SELECT SUM(award_milli) FROM game_onboarding_completions") != test.reward*3 ||
				fixture.scalar("SELECT COUNT(*) FROM credit_operations WHERE kind='game_onboarding_reward'") != 3 ||
				fixture.scalar("SELECT COUNT(*) FROM game_onboarding_holds") != 0 {
				t.Fatal("normal terminal did not grant exact independent rewards")
			}
			if test.reason == TerminalStandardRoundLimit {
				var deleteAt int64
				if err := fixture.database.QueryRow("SELECT delete_at FROM game_rps_summaries WHERE session_id=?", record.ID).Scan(&deleteAt); err != nil {
					t.Fatal(err)
				}
				work, err := fixture.service.Lifecycle().Retain(context.Background(), deleteAt, 20, time.Now().Add(5*time.Second))
				if err != nil || work.More || fixture.scalar("SELECT COUNT(*) FROM game_rps_summaries") != 0 ||
					fixture.scalar("SELECT COUNT(*) FROM game_rps_rank_facts") != 0 ||
					fixture.scalar("SELECT COUNT(*) FROM game_onboarding_completions") != 3 {
					t.Fatal("history cleanup changed lifetime qualification", work, err)
				}
			}
			fixture.assertLedgerRecovery()
		})
	}
}
