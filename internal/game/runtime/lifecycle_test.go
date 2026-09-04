package runtime

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
)

func TestFishingDeletionDualOrderConverges(t *testing.T) {
	t.Run("delete wins reserved race", func(t *testing.T) {
		fixture := newGameFixture(t, &scriptedSource{})
		userID := fixture.seedUser("delete-first", fixtureFunding)
		fixture.service.beforeSettlement = func(string) error { return errInjected }
		_, pending, err := fixture.service.StartFishing(context.Background(), StartInput{UserID: userID, Bait: "worm", Count: 1, IdempotencyKey: validTestKey(300)})
		if err != nil || pending == nil {
			t.Fatalf("pending = (%#v,%v)", pending, err)
		}
		guard, err := fixture.service.Lifecycle().BeginUserDeletion(userID)
		if err != nil {
			t.Fatal(err)
		}
		calls := fixture.random.callCount()
		if _, _, startErr := fixture.service.StartFishing(context.Background(), StartInput{UserID: userID, Bait: "lure", Count: 1, IdempotencyKey: validTestKey(301)}); !errors.Is(startErr, ErrConflict) {
			t.Fatalf("start during deletion error = %v", startErr)
		}
		if fixture.random.callCount() != calls {
			t.Fatal("deletion gate allowed random generation")
		}
		tx, err := fixture.database.BeginTx(context.Background(), nil)
		if err != nil {
			t.Fatal(err)
		}
		if err = guard.Prepare(context.Background(), tx, fixture.clock.Load()+1); err != nil {
			_ = tx.Rollback()
			t.Fatal(err)
		}
		if err = tx.Commit(); err != nil {
			t.Fatal(err)
		}
		if !guard.Commit() || guard.Commit() {
			t.Fatal("deletion guard did not commit exactly once")
		}
		if fixture.scalar(`SELECT COUNT(*) FROM game_fishing_batches WHERE id=?`, pending.BatchID) != 0 {
			t.Fatal("delete-first left batch")
		}
		if fixture.scalar(`SELECT COUNT(*) FROM credit_operations WHERE kind='fishing_release' AND source_id=?`, pending.BatchID) != 1 || fixture.scalar(`SELECT COUNT(*) FROM credit_operations WHERE kind='fishing_settle' AND source_id=?`, pending.BatchID) != 0 {
			t.Fatal("delete-first chose an invalid terminal operation")
		}
		tx, err = fixture.database.BeginTx(context.Background(), nil)
		if err != nil {
			t.Fatal(err)
		}
		wallet, err := ledger.UserAccount(context.Background(), tx, userID)
		if err != nil {
			_ = tx.Rollback()
			t.Fatal(err)
		}
		if wallet.Balance.Decimal() != "1000000000" {
			t.Fatalf("released wallet = %s", wallet.Balance.Decimal())
		}
		_ = tx.Rollback()
		fixture.service.beforeSettlement = nil
		if _, settleErr := fixture.service.settle(context.Background(), pending.BatchID, userID, fixture.clock.Load()+2, false); !errors.Is(settleErr, ErrNotFound) {
			t.Fatalf("late settlement error = %v", settleErr)
		}
	})

	t.Run("settle wins before deletion", func(t *testing.T) {
		fixture := newGameFixture(t, &scriptedSource{max: true})
		userID := fixture.seedUser("settle-first", fixtureFunding)
		result, pending, err := fixture.service.StartFishing(context.Background(), StartInput{UserID: userID, Bait: "worm", Count: 1, IdempotencyKey: validTestKey(302)})
		if err != nil || pending != nil || result == nil {
			t.Fatalf("settled = (%#v,%#v,%v)", result, pending, err)
		}
		guard, err := fixture.service.Lifecycle().BeginUserDeletion(userID)
		if err != nil {
			t.Fatal(err)
		}
		tx, err := fixture.database.BeginTx(context.Background(), nil)
		if err != nil {
			t.Fatal(err)
		}
		if err = guard.Prepare(context.Background(), tx, fixture.clock.Load()+1); err != nil {
			_ = tx.Rollback()
			t.Fatal(err)
		}
		if err = tx.Commit(); err != nil {
			t.Fatal(err)
		}
		if !guard.Commit() {
			t.Fatal("commit deletion guard")
		}
		for _, table := range []string{"game_fishing_batches", "game_fishing_best", "game_fishing_rank_facts", "game_fishing_rank_aggregates"} {
			if fixture.scalar(`SELECT COUNT(*) FROM `+table+` WHERE user_id=?`, userID) != 0 {
				t.Fatalf("settle-first left %s", table)
			}
		}
		if fixture.scalar(`SELECT COUNT(*) FROM credit_operations WHERE kind='fishing_settle' AND source_id=?`, result.BatchID) != 1 || fixture.scalar(`SELECT COUNT(*) FROM credit_operations WHERE kind='fishing_release' AND source_id=?`, result.BatchID) != 0 {
			t.Fatal("settle-first changed the terminal winner")
		}
		if _, settleErr := fixture.service.settle(context.Background(), result.BatchID, userID, fixture.clock.Load()+2, false); !errors.Is(settleErr, ErrNotFound) {
			t.Fatalf("late settle after cleanup error = %v", settleErr)
		}
	})
}

func TestFishingSettlementAndDeletionPrepareRaceConverges(t *testing.T) {
	fixture := newGameFixture(t, &scriptedSource{})
	userID := fixture.seedUser("delete-settle-race", fixtureFunding)
	fixture.service.beforeSettlement = func(string) error { return errInjected }
	_, pending, err := fixture.service.StartFishing(context.Background(), StartInput{UserID: userID, Bait: "worm", Count: 1, IdempotencyKey: validTestKey(305)})
	if err != nil || pending == nil {
		t.Fatalf("pending = (%#v,%v)", pending, err)
	}
	fixture.service.beforeSettlement = nil

	guard, err := fixture.service.Lifecycle().BeginUserDeletion(userID)
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	settleDone := make(chan error, 1)
	deleteDone := make(chan error, 1)
	go func() {
		<-start
		for attempt := 0; attempt < 20; attempt++ {
			result, settleErr := fixture.service.settle(context.Background(), pending.BatchID, userID, fixture.clock.Load()+1, false)
			if settleErr == nil {
				if result == nil {
					settleDone <- errors.New("settlement returned nil result and nil error")
					return
				}
				settleDone <- nil
				return
			}
			if errors.Is(settleErr, ErrNotFound) || errors.Is(settleErr, ErrConflict) {
				settleDone <- nil
				return
			}
			if !errors.Is(settleErr, ErrServiceUnavailable) {
				settleDone <- settleErr
				return
			}
			time.Sleep(time.Millisecond)
		}
		settleDone <- errors.New("settlement BUSY retry budget exhausted")
	}()
	go func(initial *DeletionGuard) {
		<-start
		current := initial
		for attempt := 0; attempt < 20; attempt++ {
			tx, beginErr := fixture.database.BeginTx(context.Background(), nil)
			if beginErr != nil {
				if !errors.Is(classifyDB(beginErr), ErrServiceUnavailable) {
					deleteDone <- beginErr
					return
				}
				time.Sleep(time.Millisecond)
				continue
			}
			prepareErr := current.Prepare(context.Background(), tx, fixture.clock.Load()+2)
			if prepareErr != nil {
				_ = tx.Rollback()
				if !errors.Is(prepareErr, ErrServiceUnavailable) && !errors.Is(prepareErr, ErrConflict) {
					_ = current.Abort()
					deleteDone <- prepareErr
					return
				}
				time.Sleep(time.Millisecond)
				continue
			}
			if commitErr := tx.Commit(); commitErr == nil {
				if !current.Commit() {
					deleteDone <- errors.New("deletion guard failed to commit")
					return
				}
				deleteDone <- nil
				return
			} else if !errors.Is(classifyDB(commitErr), ErrServiceUnavailable) {
				_ = current.Abort()
				deleteDone <- commitErr
				return
			}
			if !current.Abort() {
				deleteDone <- errors.New("deletion guard failed to abort after BUSY commit")
				return
			}
			current, beginErr = fixture.service.Lifecycle().BeginUserDeletion(userID)
			if beginErr != nil {
				deleteDone <- beginErr
				return
			}
			time.Sleep(time.Millisecond)
		}
		_ = current.Abort()
		deleteDone <- errors.New("deletion BUSY retry budget exhausted")
	}(guard)
	close(start)
	if settleErr, deleteErr := <-settleDone, <-deleteDone; settleErr != nil || deleteErr != nil {
		t.Fatalf("race errors: settlement=%v deletion=%v", settleErr, deleteErr)
	}

	settled := fixture.scalar(`SELECT COUNT(*) FROM credit_operations WHERE kind='fishing_settle' AND source_id=?`, pending.BatchID)
	released := fixture.scalar(`SELECT COUNT(*) FROM credit_operations WHERE kind='fishing_release' AND source_id=?`, pending.BatchID)
	if settled+released != 1 {
		t.Fatalf("terminal operations: settle=%d release=%d", settled, released)
	}
	for _, table := range []string{"game_fishing_batches", "game_fishing_outcomes"} {
		if fixture.scalar(fmt.Sprintf(`SELECT COUNT(*) FROM %s WHERE %s=?`, table, map[string]string{"game_fishing_batches": "id", "game_fishing_outcomes": "batch_id"}[table]), pending.BatchID) != 0 {
			t.Fatalf("race left %s rows", table)
		}
	}
	if fixture.scalar(`SELECT COUNT(*) FROM game_fishing_best WHERE user_id=?`, userID) != 0 || fixture.scalar(`SELECT COUNT(*) FROM game_fishing_rank_facts WHERE user_id=?`, userID) != 0 || fixture.scalar(`SELECT COUNT(*) FROM game_fishing_rank_aggregates WHERE user_id=?`, userID) != 0 {
		t.Fatal("race left leaderboard state")
	}

	tx, err := fixture.database.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	reservations, err := ledger.RecoverNonterminal(context.Background(), tx)
	if err != nil {
		t.Fatal(err)
	}
	if len(reservations) != 0 {
		t.Fatalf("race left reservations: %#v", reservations)
	}
	capacity, err := ledger.ReadCapacity(context.Background(), tx)
	if err != nil || capacity.ReservedFutureRows != (db.U128{}) {
		t.Fatalf("capacity after race = %#v, %v", capacity, err)
	}
	wallet, err := ledger.UserAccount(context.Background(), tx, userID)
	if err != nil {
		t.Fatal(err)
	}
	wantWallet := int64(fixtureFunding)
	if settled == 1 {
		wantWallet -= 2_500_000
	}
	if wallet.Balance.Decimal() != fmt.Sprintf("%d", wantWallet) {
		t.Fatalf("wallet after race = %s, want %d", wallet.Balance.Decimal(), wantWallet)
	}
	reserve, err := ledger.CodedAccount(context.Background(), tx, "game_fishing_reserve")
	if err != nil || reserve.Balance.Decimal() != "0" {
		t.Fatalf("fishing reserve after race = %#v, %v", reserve, err)
	}
}

func TestFishingPrepareDeleteTxUsesCoordinatorOwnedRetirement(t *testing.T) {
	fixture := newGameFixture(t, &scriptedSource{})
	userID := fixture.seedUser("delete-tx-boundary", fixtureFunding)
	fixture.service.beforeSettlement = func(string) error { return errInjected }
	_, pending, err := fixture.service.StartFishing(context.Background(), StartInput{
		UserID: userID, Bait: "worm", Count: 1, IdempotencyKey: validTestKey(307),
	})
	if err != nil || pending == nil {
		t.Fatalf("pending = (%#v,%v)", pending, err)
	}
	commit, abort, err := fixture.service.limiter.BeginUserDeletion(userID)
	if err != nil {
		t.Fatal(err)
	}
	finished := false
	defer func() {
		if !finished {
			_ = abort()
		}
	}()
	tx, err := fixture.database.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.service.Lifecycle().PrepareDeleteTx(context.Background(), tx, userID, fixture.clock.Load()+1); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if !commit() {
		t.Fatal("commit coordinator-owned retirement")
	}
	finished = true
	if fixture.scalar(`SELECT COUNT(*) FROM game_fishing_batches WHERE id=?`, pending.BatchID) != 0 {
		t.Fatal("transaction deletion left reserved batch")
	}
	if fixture.scalar(`SELECT COUNT(*) FROM credit_operations WHERE kind='fishing_release' AND source_id=?`, pending.BatchID) != 1 {
		t.Fatal("transaction deletion did not release reservation")
	}
}

func TestLifecycleExportAndCleanupBoundaries(t *testing.T) {
	fixture := newGameFixture(t, &scriptedSource{max: true})
	terminalUser := fixture.seedUser("export-terminal", fixtureFunding)
	terminal, pending, err := fixture.service.StartFishing(context.Background(), StartInput{UserID: terminalUser, Bait: "worm", Count: 1, IdempotencyKey: validTestKey(310)})
	if err != nil || pending != nil || terminal == nil {
		t.Fatalf("terminal = (%#v,%#v,%v)", terminal, pending, err)
	}
	pendingUser := fixture.seedUser("export-pending", fixtureFunding)
	fixture.service.beforeSettlement = func(string) error { return errInjected }
	_, waiting, err := fixture.service.StartFishing(context.Background(), StartInput{UserID: pendingUser, Bait: "lure", Count: 1, IdempotencyKey: validTestKey(311)})
	if err != nil || waiting == nil {
		t.Fatalf("pending = (%#v,%v)", waiting, err)
	}

	tx, err := fixture.database.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	exported, err := fixture.service.Lifecycle().ExportUser(context.Background(), tx, terminalUser, fixture.clock.Load())
	if err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	_ = tx.Rollback()
	if len(exported.Pending) != 0 || len(exported.Terminal) != 1 || exported.Terminal[0].BatchID != terminal.BatchID ||
		exported.Terminal[0].RevealedAt != nil || exported.Single == nil || exported.Total == nil {
		t.Fatalf("terminal export = %#v", exported)
	}
	if err := fixture.service.AcknowledgeFishing(context.Background(), terminalUser, terminal.BatchID); err != nil {
		t.Fatalf("ack terminal: %v", err)
	}
	tx, err = fixture.database.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	revealedExport, err := fixture.service.Lifecycle().ExportTx(context.Background(), tx, terminalUser, fixture.clock.Load(), 10_000)
	if err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	_ = tx.Rollback()
	if len(revealedExport.Terminal) != 1 || revealedExport.Terminal[0].RevealedAt == nil ||
		*revealedExport.Terminal[0].RevealedAt != fixture.clock.Load() {
		t.Fatalf("revealed export = %#v", revealedExport.Terminal)
	}
	tx, err = fixture.database.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	pendingExport, err := fixture.service.Lifecycle().ExportUser(context.Background(), tx, pendingUser, fixture.clock.Load())
	if err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	_ = tx.Rollback()
	if len(pendingExport.Pending) != 1 || pendingExport.Pending[0].BatchID != waiting.BatchID || len(pendingExport.Terminal) != 0 {
		t.Fatalf("pending export = %#v", pendingExport)
	}

	window := int64(rankWindow.Seconds())
	deadline := time.Now().Add(time.Second)
	retained, err := fixture.service.Lifecycle().Retain(context.Background(), terminal.SettledAt+window-1, 1, deadline)
	if err != nil || retained.Processed != 0 || retained.More || fixture.scalar(`SELECT COUNT(*) FROM game_fishing_batches WHERE id=?`, terminal.BatchID) != 1 {
		t.Fatalf("retention -1 = %#v err %v", retained, err)
	}
	retained, err = fixture.service.Lifecycle().Retain(context.Background(), terminal.SettledAt+window, 1, time.Now().Add(time.Second))
	if err != nil || retained.Processed != 1 || !retained.More || fixture.scalar(`SELECT COUNT(*) FROM game_fishing_batches WHERE id=?`, terminal.BatchID) != 1 {
		t.Fatalf("retention boundary rank pass = %#v err %v", retained, err)
	}
	retained, err = fixture.service.Lifecycle().Retain(context.Background(), terminal.SettledAt+window, 1, time.Now().Add(time.Second))
	if err != nil || retained.Processed != 1 || retained.More || fixture.scalar(`SELECT COUNT(*) FROM game_fishing_batches WHERE id=?`, terminal.BatchID) != 0 {
		t.Fatalf("retention boundary batch pass = %#v err %v", retained, err)
	}
	if fixture.scalar(`SELECT COUNT(*) FROM game_fishing_best WHERE user_id=? AND batch_id IS NULL AND ordinal IS NULL`, terminalUser) != 1 {
		t.Fatal("cleanup did not preserve denormalized best snapshot")
	}
	if fixture.scalar(`SELECT COUNT(*) FROM game_fishing_batches WHERE id=? AND state='reserved'`, waiting.BatchID) != 1 {
		t.Fatal("retention removed a reserved batch")
	}
}

func TestFishingExportTxAppliesTerminalCollectionLimit(t *testing.T) {
	fixture := newGameFixture(t, &scriptedSource{max: true})
	userID := fixture.seedUser("export-limit", fixtureFunding)
	for index := 0; index < 2; index++ {
		result, pending, err := fixture.service.StartFishing(context.Background(), StartInput{
			UserID: userID, Bait: "worm", Count: 1, IdempotencyKey: validTestKey(320 + index),
		})
		if err != nil || pending != nil || result == nil {
			t.Fatalf("start %d = (%#v,%#v,%v)", index, result, pending, err)
		}
	}
	tx, err := fixture.database.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := fixture.service.Lifecycle().ExportTx(context.Background(), tx, userID, fixture.clock.Load(), 1); !errors.Is(err, ErrLifecycleResourceLimit) {
		t.Fatalf("ExportTx error = %v", err)
	}
}
