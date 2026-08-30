package runtime

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/game/fishing"
)

func TestFishingWorkerExhaustionRecoverAndACK(t *testing.T) {
	fixture := newGameFixture(t, &scriptedSource{})
	userID := fixture.seedUser("worker", fixtureFunding)
	fixture.service.beforeSettlement = func(string) error { return errInjected }
	_, pending, err := fixture.service.StartFishing(context.Background(), StartInput{
		UserID: userID, Bait: string(fishing.BaitWorm), Count: 1, IdempotencyKey: validTestKey(200),
	})
	if err != nil || pending == nil || pending.NextAttemptAt == nil || pending.RetryExhausted {
		t.Fatalf("initial pending = (%#v,%v)", pending, err)
	}
	if *pending.NextAttemptAt != fixtureNow+int64(firstRetryDelay.Seconds()) {
		t.Fatalf("first retry = %d", *pending.NextAttemptAt)
	}
	randomCalls := fixture.random.callCount()
	next := *pending.NextAttemptAt
	for attempt := 1; attempt <= 10; attempt++ {
		fixture.clock.Store(next)
		processed, runErr := fixture.service.runDue(context.Background(), next, true)
		if runErr != nil || processed != 1 {
			t.Fatalf("attempt %d run = (%d,%v)", attempt, processed, runErr)
		}
		var storedAttempt, exhausted int
		var storedNext *int64
		if err = fixture.database.QueryRow(`SELECT attempt_count,next_attempt_at,retry_exhausted FROM game_fishing_batches WHERE id=?`, pending.BatchID).Scan(&storedAttempt, &storedNext, &exhausted); err != nil {
			t.Fatal(err)
		}
		if storedAttempt != attempt {
			t.Fatalf("stored attempt = %d, want %d", storedAttempt, attempt)
		}
		if attempt < 10 {
			if exhausted != 0 || storedNext == nil || *storedNext <= next {
				t.Fatalf("attempt %d schedule = next %v exhausted %d", attempt, storedNext, exhausted)
			}
			next = *storedNext
		} else if exhausted != 1 || storedNext != nil {
			t.Fatalf("terminal retry = next %v exhausted %d", storedNext, exhausted)
		}
	}
	if fixture.random.callCount() != randomCalls {
		t.Fatal("settlement retries consumed new randomness")
	}
	if fixture.scalar(`SELECT COUNT(*) FROM admin_alerts WHERE kind='fishing_retry_exhausted' AND ref=?`, pending.BatchID) != 1 {
		t.Fatal("retry exhaustion did not emit exactly one safe alert")
	}
	if processed, runErr := fixture.service.runDue(context.Background(), next+86400, true); runErr != nil || processed != 0 {
		t.Fatalf("exhausted worker run = (%d,%v)", processed, runErr)
	}

	_, failed, err := fixture.service.RecoverFishing(context.Background(), RecoverInput{UserID: userID, BatchID: pending.BatchID, IdempotencyKey: validTestKey(201)})
	if err != nil || failed == nil || !failed.RetryExhausted || failed.NextAttemptAt != nil {
		t.Fatalf("failed manual recovery = (%#v,%v)", failed, err)
	}
	var operationID string
	if err = fixture.database.QueryRow(`SELECT operation_id FROM game_fishing_batches WHERE id=?`, pending.BatchID).Scan(&operationID); err != nil {
		t.Fatal(err)
	}

	fixture.service.beforeSettlement = nil
	result, stillPending, err := fixture.service.RecoverFishing(context.Background(), RecoverInput{UserID: userID, BatchID: pending.BatchID, IdempotencyKey: validTestKey(202)})
	if err != nil || stillPending != nil || result == nil || result.BatchID != pending.BatchID {
		t.Fatalf("successful manual recovery = (%#v,%#v,%v)", result, stillPending, err)
	}
	var terminalOperation string
	if err = fixture.database.QueryRow(`SELECT id FROM credit_operations WHERE kind='fishing_settle' AND source_id=?`, pending.BatchID).Scan(&terminalOperation); err != nil {
		t.Fatal(err)
	}
	if terminalOperation != operationID {
		t.Fatalf("terminal operation = %s, frozen %s", terminalOperation, operationID)
	}
	if fixture.scalar(`SELECT COUNT(*) FROM credit_operations WHERE source_type='fishing_batch' AND source_id=?`, pending.BatchID) != 2 {
		t.Fatal("manual recovery wrote duplicate economic operations")
	}

	state, err := fixture.service.FishingState(context.Background(), userID)
	if err != nil || state.SettlementPending != nil || state.Unrevealed == nil || state.Unrevealed.BatchID != pending.BatchID {
		t.Fatalf("state before ACK = (%#v,%v)", state, err)
	}
	if err = fixture.service.AcknowledgeFishing(context.Background(), userID, pending.BatchID); err != nil {
		t.Fatal(err)
	}
	if err = fixture.service.AcknowledgeFishing(context.Background(), userID, pending.BatchID); err != nil {
		t.Fatalf("idempotent ACK: %v", err)
	}
	state, err = fixture.service.FishingState(context.Background(), userID)
	if err != nil || state.Unrevealed != nil || state.SettlementPending != nil || state.HasMoreUnrevealed {
		t.Fatalf("state after ACK = (%#v,%v)", state, err)
	}
	second, secondPending, err := fixture.service.StartFishing(context.Background(), StartInput{UserID: userID, Bait: string(fishing.BaitLure), Count: 1, IdempotencyKey: validTestKey(203)})
	if err != nil || secondPending != nil || second == nil {
		t.Fatalf("new start after ACK = (%#v,%#v,%v)", second, secondPending, err)
	}
}

func TestSettlementWorkerAndAuthorityRaceConverges(t *testing.T) {
	fixture := newGameFixture(t, &scriptedSource{})
	userID := fixture.seedUser("race", fixtureFunding)
	fixture.service.beforeSettlement = func(string) error { return errInjected }
	_, pending, err := fixture.service.StartFishing(context.Background(), StartInput{UserID: userID, Bait: "worm", Count: 10, IdempotencyKey: validTestKey(210)})
	if err != nil || pending == nil {
		t.Fatalf("pending = (%#v,%v)", pending, err)
	}
	fixture.service.beforeSettlement = nil
	if _, err = fixture.database.Exec(`UPDATE game_fishing_batches SET next_attempt_at=? WHERE id=?`, fixture.clock.Load(), pending.BatchID); err != nil {
		t.Fatal(err)
	}

	start := make(chan struct{})
	var wait sync.WaitGroup
	wait.Add(3)
	errorsSeen := make(chan error, 3)
	go func() {
		defer wait.Done()
		<-start
		result, settleErr := fixture.service.settle(context.Background(), pending.BatchID, userID, fixture.clock.Load(), false)
		if settleErr == nil && result == nil {
			errorsSeen <- errors.New("settle returned nil result and nil error")
			return
		}
		if settleErr != nil && !errors.Is(settleErr, ErrConflict) && !errors.Is(settleErr, ErrServiceUnavailable) {
			errorsSeen <- settleErr
		}
	}()
	go func() {
		defer wait.Done()
		<-start
		_, runErr := fixture.service.runDue(context.Background(), fixture.clock.Load(), true)
		if runErr != nil {
			errorsSeen <- runErr
		}
	}()
	go func() {
		defer wait.Done()
		<-start
		result, authorityPending, authorityErr := fixture.service.loadAuthority(context.Background(), userID, pending.BatchID, true)
		if authorityErr == nil && (result == nil) == (authorityPending == nil) {
			errorsSeen <- errors.New("authority returned an invalid result union")
		}
	}()
	close(start)
	wait.Wait()
	close(errorsSeen)
	for raceErr := range errorsSeen {
		t.Errorf("race result: %v", raceErr)
	}
	result, authorityPending, err := fixture.service.loadAuthority(context.Background(), userID, pending.BatchID, true)
	if err != nil || result == nil || authorityPending != nil {
		t.Fatalf("race authority = (%#v,%#v,%v)", result, authorityPending, err)
	}
	if fixture.scalar(`SELECT COUNT(*) FROM credit_operations WHERE kind='fishing_settle' AND source_id=?`, pending.BatchID) != 1 {
		t.Fatal("race wrote more than one settlement")
	}
}

func TestRecoverBeforeListenFailsClosedWhenRetryCheckpointCannotPersist(t *testing.T) {
	fixture := newGameFixture(t, &scriptedSource{})
	userID := fixture.seedUser("recovery-checkpoint", fixtureFunding)
	fixture.service.beforeSettlement = func(string) error { return errInjected }
	_, pending, err := fixture.service.StartFishing(context.Background(), StartInput{UserID: userID, Bait: "worm", Count: 1, IdempotencyKey: validTestKey(220)})
	if err != nil || pending == nil || pending.NextAttemptAt == nil {
		t.Fatalf("pending = (%#v,%v)", pending, err)
	}
	fixture.clock.Store(*pending.NextAttemptAt)
	if _, err = fixture.database.Exec(`CREATE TRIGGER fail_fishing_checkpoint BEFORE UPDATE ON game_fishing_batches WHEN NEW.attempt_count>OLD.attempt_count BEGIN SELECT RAISE(ABORT,'database is locked'); END`); err != nil {
		t.Fatal(err)
	}
	err = fixture.service.RecoverBeforeListen(context.Background())
	if !errors.Is(err, ErrServiceUnavailable) {
		t.Fatalf("recovery checkpoint error = %v", err)
	}
	if fixture.service.recovered.Load() {
		t.Fatal("failed recovery marked the service recovered")
	}
	if startErr := fixture.service.StartWorker(context.Background()); startErr == nil {
		t.Fatal("worker started after failed pre-listen recovery")
	}
	var attempt int
	var next int64
	if err = fixture.database.QueryRow(`SELECT attempt_count,next_attempt_at FROM game_fishing_batches WHERE id=?`, pending.BatchID).Scan(&attempt, &next); err != nil {
		t.Fatal(err)
	}
	if attempt != 0 || next != *pending.NextAttemptAt {
		t.Fatalf("failed checkpoint partially changed state: attempt=%d next=%d", attempt, next)
	}
}

func TestRecoverBeforeListenFailsClosedWhenCheckpointCASMakesNoProgress(t *testing.T) {
	fixture := newGameFixture(t, &scriptedSource{})
	userID := fixture.seedUser("recovery-checkpoint-cas", fixtureFunding)
	fixture.service.beforeSettlement = func(string) error { return errInjected }
	_, pending, err := fixture.service.StartFishing(context.Background(), StartInput{UserID: userID, Bait: "worm", Count: 1, IdempotencyKey: validTestKey(221)})
	if err != nil || pending == nil || pending.NextAttemptAt == nil {
		t.Fatalf("pending = (%#v,%v)", pending, err)
	}
	fixture.clock.Store(*pending.NextAttemptAt)
	if _, err = fixture.database.Exec(`CREATE TRIGGER ignore_fishing_checkpoint BEFORE UPDATE ON game_fishing_batches WHEN NEW.attempt_count>OLD.attempt_count BEGIN SELECT RAISE(IGNORE); END`); err != nil {
		t.Fatal(err)
	}
	err = fixture.service.RecoverBeforeListen(context.Background())
	if !errors.Is(err, ErrServiceUnavailable) {
		t.Fatalf("recovery zero-progress error = %v", err)
	}
	if fixture.service.recovered.Load() {
		t.Fatal("zero-progress recovery marked the service recovered")
	}
	var attempt int
	var next int64
	if err = fixture.database.QueryRow(`SELECT attempt_count,next_attempt_at FROM game_fishing_batches WHERE id=?`, pending.BatchID).Scan(&attempt, &next); err != nil {
		t.Fatal(err)
	}
	if attempt != 0 || next != *pending.NextAttemptAt {
		t.Fatalf("ignored checkpoint changed state: attempt=%d next=%d", attempt, next)
	}
}
