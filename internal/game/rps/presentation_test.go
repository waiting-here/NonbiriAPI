package rps

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"strconv"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/game"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
)

func presentationWire(t *testing.T, value any) map[string]any {
	t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var object map[string]any
	if err := json.Unmarshal(body, &object); err != nil {
		t.Fatal(err)
	}
	return object
}

func TestQuickPresentationPersistsRevealedGesturesAndOwnTransfers(t *testing.T) {
	fixture := newRPSFixture(t)
	users, bindings := fixture.startThree(game.RPSModeQuick, 100_000, 1900)
	record := fixture.sessionForUser(users[0])
	gestures := [3]string{GestureRock, GestureScissors, GestureScissors}
	key := 1910
	fixture.playGestures(record.ID, bindings, gestures, &key)
	for _, userID := range users {
		tx := fixture.mustReadTx()
		pending, found, err := loadPending(context.Background(), tx, userID)
		_ = tx.Rollback()
		if err != nil || !found {
			t.Fatalf("pending: found=%v err=%v", found, err)
		}
		wire := presentationWire(t, pending)
		seat := record.Seats[pending.OwnSeatNo]
		if wire["own_buy_in"] != formatMilli(seat.StartingBalance.Big()) {
			t.Fatalf("buy-in=%v", wire["own_buy_in"])
		}
		outsideMatch := new(big.Int).Sub(big.NewInt(100_000), seat.StartingBalance.Big())
		cashOut := new(big.Int).Sub(fixture.balance(userID), outsideMatch)
		if wire["own_cash_out"] != formatMilli(cashOut) {
			t.Fatalf("cash-out=%v wallet return=%s", wire["own_cash_out"], cashOut)
		}
		for index, raw := range wire["seats"].([]any) {
			projected := raw.(map[string]any)
			if projected["gesture"] != gestures[index] {
				t.Fatalf("seat %d gesture=%v", index, projected["gesture"])
			}
			if len(projected) != 3 {
				t.Fatalf("unexpected peer fields: %v", projected)
			}
		}
		tx = fixture.mustReadTx()
		exported, finalizer, err := fixture.service.Lifecycle().ExportTx(context.Background(), tx, userID, fixture.clock.Load(), 100)
		_ = tx.Rollback()
		if err != nil || finalizer != nil || len(exported.Summaries) != 1 || exported.Pending == nil {
			t.Fatalf("export=%+v err=%v", exported, err)
		}
		own := exported.Summaries[0].OwnSeat
		if own.SeatNo != pending.OwnSeatNo || own.OwnBuyIn == nil || *own.OwnBuyIn != *pending.OwnBuyIn ||
			own.OwnCashOut == nil || *own.OwnCashOut != *pending.OwnCashOut || own.WalletNet != pending.OwnWalletNet {
			t.Fatalf("summary transfers=%+v pending=%+v", own, pending)
		}
	}
}

func TestLegacyPoolPresentationStaysUnknownUntilDefiniteNewPool(t *testing.T) {
	for _, missing := range []bool{true, false} {
		t.Run(strconv.FormatBool(missing), func(t *testing.T) {
			fixture := newRPSFixture(t)
			users, bindings := fixture.startThree(game.RPSModeStandard, 100_000, 1940)
			record := fixture.sessionForUser(users[0])
			query := `UPDATE game_rps_presentation SET pool_tie_count=NULL WHERE session_id=?`
			if missing {
				query = `DELETE FROM game_rps_presentation WHERE session_id=?`
			}
			if _, err := fixture.database.Exec(query, record.ID); err != nil {
				t.Fatal(err)
			}
			key := 1950
			fixture.playGestures(record.ID, bindings, [3]string{GestureRock, GestureRock, GestureRock}, &key)
			fixture.dealerDecision(record.ID, bindings, "no_raise", "", &key)
			current := fixture.sessionByID(record.ID)
			if current.Phase != PhasePaidPoolGesture || current.Presentation.PoolTieCount != nil {
				t.Fatalf("old pool phase=%s count=%v", current.Phase, current.Presentation.PoolTieCount)
			}
			fixture.playGestures(record.ID, bindings, [3]string{GestureRock, GestureScissors, GestureScissors}, &key)
			current = fixture.sessionByID(record.ID)
			if current.Phase != PhaseGesture || current.Presentation.PoolTieCount == nil || current.Presentation.PoolTieCount.Big().Sign() != 0 {
				t.Fatalf("new pool phase=%s count=%v", current.Phase, current.Presentation.PoolTieCount)
			}
		})
	}
}

// Old receipts used exactly these JSON shapes; the stored body is deliberately
// retained across another action call instead of being regenerated on replay.
func TestLegacyPresentationReceiptsReplayWithoutInventingHistory(t *testing.T) {
	for _, terminal := range []bool{false, true} {
		t.Run(strconv.FormatBool(terminal), func(t *testing.T) {
			fixture := newRPSFixture(t)
			users, bindings := fixture.startThree(game.RPSModeQuick, 100_000, 1960)
			record := fixture.sessionForUser(users[0])
			if _, err := fixture.database.Exec(`DELETE FROM game_rps_presentation WHERE session_id=?`, record.ID); err != nil {
				t.Fatal(err)
			}
			var input ActionInput
			var first MutationResult
			seats := 1
			if terminal {
				seats = 3
			}
			for seat := 0; seat < seats; seat++ {
				current := fixture.sessionByID(record.ID)
				userID := *current.Seats[seat].UserID
				input = ActionInput{UserID: userID, SessionBinding: bindings[userID], SessionID: record.ID,
					PhaseSeq: current.PhaseSeq.Decimal(), ExpectedRevision: current.Revision.Decimal(),
					Action: "gesture", Gesture: GestureRock, IdempotencyKey: fixture.key(1970 + seat)}
				var err error
				first, err = fixture.service.Action(context.Background(), input)
				if err != nil {
					t.Fatal(err)
				}
			}
			if terminal {
				if _, err := fixture.database.Exec(`DELETE FROM game_rps_pending_presentation WHERE user_id=?`, input.UserID); err != nil {
					t.Fatal(err)
				}
				if _, err := fixture.database.Exec(`DELETE FROM game_rps_summary_presentation WHERE session_id=?`, record.ID); err != nil {
					t.Fatal(err)
				}
				first.State.Result.OwnBuyIn, first.State.Result.OwnCashOut = nil, nil
				for seat := range first.State.Result.Seats {
					first.State.Result.Seats[seat].Gesture = nil
				}
			}
			current, err := json.Marshal(first.State)
			if err != nil {
				t.Fatal(err)
			}
			legacy := append([]byte(nil), current...)
			for _, member := range []string{`,"pool_tie_count":null`, `,"gesture":null`, `,"own_buy_in":null`, `,"own_cash_out":null`} {
				legacy = bytes.ReplaceAll(legacy, []byte(member), nil)
			}
			if bytes.Equal(current, legacy) {
				t.Fatal("legacy fixture did not remove presentation members")
			}
			input.IdempotencyKey = fixture.key(1980)
			action, err := canonicalAction(input)
			if err != nil {
				t.Fatal(err)
			}
			actor, digest, err := requestDecision(input.UserID, http.MethodPost, RouteActions, []string{record.ID}, action)
			if err != nil {
				t.Fatal(err)
			}
			tx, err := fixture.database.BeginTx(context.Background(), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			decision, err := idempotency.Begin(context.Background(), tx, idempotency.BeginInput{
				Scope: idempotency.ScopeGameRPS, ActorHash: actor, Key: input.IdempotencyKey, RequestHash: digest, DecisionNow: fixture.clock.Load(),
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := idempotency.Complete(context.Background(), tx, decision, http.StatusOK, legacy); err != nil {
				t.Fatal(err)
			}
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
			replay, err := fixture.service.Action(context.Background(), input)
			if err != nil || !replay.IdempotentReplay {
				t.Fatalf("legacy replay=%+v err=%v", replay, err)
			}
			body, err := json.Marshal(replay.State)
			if err != nil || !bytes.Equal(body, current) {
				t.Fatalf("replay changed old facts: %s err=%v", body, err)
			}
			if terminal {
				partial := bytes.Replace(current, []byte(`,"own_cash_out":null`), nil, 1)
				if _, err := decodeHomeState(partial); !errors.Is(err, ErrInvariant) {
					t.Fatalf("partial presentation omission accepted: %v", err)
				}
				partial = bytes.Replace(current, []byte(`,"gesture":null`), nil, 1)
				if _, err := decodeHomeState(partial); !errors.Is(err, ErrInvariant) {
					t.Fatalf("partial seat omission accepted: %v", err)
				}
				tx := fixture.mustReadTx()
				exported, _, err := fixture.service.Lifecycle().ExportTx(context.Background(), tx, input.UserID, fixture.clock.Load(), 100)
				_ = tx.Rollback()
				if err != nil || len(exported.Summaries) != 1 || exported.Summaries[0].OwnSeat.OwnBuyIn != nil || exported.Summaries[0].OwnSeat.OwnCashOut != nil {
					t.Fatalf("old summary invented transfers: %+v err=%v", exported, err)
				}
				if fixture.scalar(`SELECT COUNT(*) FROM credit_operations WHERE kind='rps_terminal' AND source_id=?`, record.ID) != 1 {
					t.Fatal("legacy replay repeated settlement")
				}
				if _, err := fixture.service.ACK(context.Background(), ACKInput{UserID: input.UserID, SessionBinding: input.SessionBinding, SessionID: record.ID}); err != nil {
					t.Fatal(err)
				}
				if _, err := fixture.service.Action(context.Background(), input); !errors.Is(err, ErrConflict) {
					t.Fatalf("legacy pending replay after ACK=%v", err)
				}
			}
		})
	}
}

func TestPoolPresentationCountsAcrossPaidAndFreeTies(t *testing.T) {
	for _, mode := range []string{game.RPSModeStandard, game.RPSModeDeathmatch} {
		t.Run(mode, func(t *testing.T) {
			fixture := newRPSFixture(t)
			users, bindings := fixture.startThree(mode, 5_000, 1920)
			record := fixture.sessionForUser(users[0])
			key := 1930
			ties := 0
			freeObserved := false
			for ties < 16 {
				current, found := fixture.maybeSession(record.ID)
				if !found {
					break
				}
				state, err := projectState(current, users[0], fixture.clock.Load())
				if err != nil {
					t.Fatal(err)
				}
				count := presentationWire(t, state.RoundSummary)["pool_tie_count"]
				if count != strconv.Itoa(ties) {
					t.Fatalf("phase=%s count=%v ties=%d", current.Phase, count, ties)
				}
				if current.Phase == PhaseFreePoolGesture {
					freeObserved = true
				}
				fixture.playGestures(record.ID, bindings, [3]string{GestureRock, GestureRock, GestureRock}, &key)
				if current.Phase == PhaseGesture {
					fixture.dealerDecision(record.ID, bindings, "no_raise", "", &key)
				}
				ties++
			}
			if !freeObserved || ties >= 16 {
				t.Fatalf("free=%v ties=%d", freeObserved, ties)
			}
		})
	}
}

func TestStandardPresentationDistinguishesWalletTransfersFromRoundTotals(t *testing.T) {
	fixture := newRPSFixture(t)
	users, bindings := fixture.startThree(game.RPSModeStandard, 100_000, 1990)
	record := fixture.sessionForUser(users[0])
	key := 2000
	for round := 0; round < 9; round++ {
		gestures := [3]string{GestureScissors, GestureScissors, GestureScissors}
		gestures[round%3] = GestureRock
		fixture.playGestures(record.ID, bindings, gestures, &key)
		fixture.dealerDecision(record.ID, bindings, "no_raise", "", &key)
	}
	for _, userID := range users {
		tx := fixture.mustReadTx()
		pending, found, err := loadPending(context.Background(), tx, userID)
		_ = tx.Rollback()
		if err != nil || !found || pending.TerminalReason != TerminalStandardRoundLimit {
			t.Fatalf("completed standard=%+v found=%v err=%v", pending, found, err)
		}
		starting := record.Seats[pending.OwnSeatNo].StartingBalance.Big()
		cashOut := new(big.Int).Sub(fixture.balance(userID), new(big.Int).Sub(big.NewInt(100_000), starting))
		if pending.OwnBuyIn == nil || *pending.OwnBuyIn != formatMilli(starting) ||
			pending.OwnCashOut == nil || *pending.OwnCashOut != formatMilli(cashOut) {
			t.Fatalf("wallet transfers=%+v", pending)
		}
		if pending.OwnInput == *pending.OwnBuyIn || pending.OwnReturned == *pending.OwnCashOut {
			t.Fatalf("fixture did not distinguish recycled stakes from wallet transfers: %+v", pending)
		}
		for _, seat := range pending.Seats {
			if seat.Gesture != nil {
				t.Fatal("standard result retained a quick-only gesture")
			}
		}
	}
}
