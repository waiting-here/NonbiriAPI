package claim

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/observability"
)

func awaitCompletionCondition(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for !condition() {
		select {
		case <-tick.C:
		case <-deadline.C:
			t.Fatal("completion condition was not reached")
		}
	}
}

func awaitCompletionResult(t *testing.T, ch <-chan error) error {
	t.Helper()
	select {
	case err := <-ch:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("completion did not finish")
		return nil
	}
}

func pendingCompletions(s *Service) int {
	s.completion.mu.Lock()
	defer s.completion.mu.Unlock()
	return s.completion.pending.Len()
}

func submitTestCompletion(s *Service, ctx context.Context, fn func(context.Context, *sql.Tx) (any, error)) <-chan error {
	done := make(chan error, 1)
	go func() {
		_, err := s.submitCompletion(ctx, "claim_complete", fn)
		done <- err
	}()
	return done
}

func TestCompletionAcknowledgesOnlyAfterCommit(t *testing.T) {
	f := newClaimFixture(t)
	t.Cleanup(func() { _ = f.service.Close() })
	if _, err := f.db.Exec(`CREATE TABLE completed_rows(id INTEGER PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	releaseOnce := sync.OnceFunc(func() { close(release) })
	defer releaseOnce()
	done := submitTestCompletion(f.service, t.Context(), func(ctx context.Context, tx *sql.Tx) (any, error) {
		_, err := tx.ExecContext(ctx, `INSERT INTO completed_rows VALUES(1)`)
		close(entered)
		<-release
		return 1, err
	})
	<-entered
	var n int
	if err := f.store.ReadDB().QueryRow(`SELECT COUNT(*) FROM completed_rows`).Scan(&n); err != nil || n != 0 {
		releaseOnce()
		t.Fatalf("uncommitted result visible: %d %v", n, err)
	}
	select {
	case err := <-done:
		releaseOnce()
		t.Fatalf("acknowledged before commit: %v", err)
	default:
	}
	releaseOnce()
	if err := awaitCompletionResult(t, done); err != nil {
		t.Fatal(err)
	}
	if err := f.store.ReadDB().QueryRow(`SELECT COUNT(*) FROM completed_rows`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("acknowledgement preceded visibility: %d %v", n, err)
	}
}

func TestCompletionSavepointAndCommitFailures(t *testing.T) {
	for _, deferredConstraint := range []bool{false, true} {
		name := "body failure"
		if deferredConstraint {
			name = "outer commit failure"
		}
		t.Run(name, func(t *testing.T) {
			f := newClaimFixture(t)
			t.Cleanup(func() { _ = f.service.Close() })
			p := observability.NewPerformance()
			ctx := p.Context(t.Context())
			if _, err := f.db.Exec(`CREATE TABLE completed_parent(id INTEGER PRIMARY KEY); INSERT INTO completed_parent VALUES(1);
CREATE TABLE completed_child(id INTEGER PRIMARY KEY,parent INTEGER REFERENCES completed_parent(id) DEFERRABLE INITIALLY DEFERRED)`); err != nil {
				t.Fatal(err)
			}
			blocker, err := f.db.Conn(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer blocker.Close()
			waits := f.db.Stats().WaitCount
			injected := errors.New("body failed")
			var results []<-chan error
			for i := range 4 {
				results = append(results, submitTestCompletion(f.service, ctx, func(ctx context.Context, tx *sql.Tx) (any, error) {
					parent := 1
					if i == 1 && deferredConstraint {
						parent = 9
					}
					_, err := tx.ExecContext(ctx, `INSERT INTO completed_child VALUES(?,?)`, i+1, parent)
					if err == nil && i == 1 && !deferredConstraint {
						err = injected
					}
					return i, err
				}))
				if i == 0 {
					awaitCompletionCondition(t, func() bool { return f.db.Stats().WaitCount > waits })
				} else {
					awaitCompletionCondition(t, func() bool { return pendingCompletions(f.service) == i })
				}
			}
			blocker.Close()
			for i, result := range results {
				err := awaitCompletionResult(t, result)
				if (err != nil) != (i == 1) || i == 1 && !deferredConstraint && !errors.Is(err, injected) {
					t.Fatalf("item %d: %v", i, err)
				}
			}
			f.service.Close()
			var n int
			if err := f.db.QueryRow(`SELECT COUNT(*) FROM completed_child WHERE id<>2`).Scan(&n); err != nil || n != 3 {
				t.Fatalf("successful siblings lost: %d %v", n, err)
			}
			if err := f.db.QueryRow(`SELECT COUNT(*) FROM completed_child WHERE id=2`).Scan(&n); err != nil || n != 0 {
				t.Fatalf("failed item persisted: %d %v", n, err)
			}
			fallbacks := int64(0)
			for _, row := range p.Drain() {
				if row.Active != 0 {
					t.Fatalf("unfinished measurement: %+v", row)
				}
				if row.Route == "completion.fallback" {
					fallbacks += row.Completed
				}
			}
			if deferredConstraint && fallbacks == 0 {
				t.Fatal("commit failure did not use independent recovery")
			}
		})
	}
}

func TestCompletionQueuedCancellationAndClose(t *testing.T) {
	f := newClaimFixture(t)
	blocker, err := f.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Close()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	waits := f.db.Stats().WaitCount
	run := func(context.Context, *sql.Tx) (any, error) { return true, nil }
	first := submitTestCompletion(f.service, t.Context(), run)
	awaitCompletionCondition(t, func() bool { return f.db.Stats().WaitCount > waits })
	second := submitTestCompletion(f.service, ctx, func(context.Context, *sql.Tx) (any, error) {
		t.Error("canceled queued body executed")
		return nil, nil
	})
	awaitCompletionCondition(t, func() bool { return pendingCompletions(f.service) == 1 })
	cancel()
	if err := awaitCompletionResult(t, second); !errors.Is(err, context.Canceled) || pendingCompletions(f.service) != 0 {
		t.Fatal("canceled queue entry retained", err)
	}
	expired, expire := context.WithCancel(t.Context())
	expire()
	if err := f.service.CloseContext(expired); !errors.Is(err, context.Canceled) {
		t.Fatalf("close did not retain pending drain: %v", err)
	}
	if _, err := f.service.submitCompletion(t.Context(), "claim_complete", run); !errors.Is(err, ErrDependencyUnavailable) {
		t.Fatalf("closed queue accepted work: %v", err)
	}
	blocker.Close()
	if err := awaitCompletionResult(t, first); err != nil {
		t.Fatal("close lost accepted work", err)
	}
	if err := f.service.CloseContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	if f.db.Stats().InUse != 0 || pendingCompletions(f.service) != 0 {
		t.Fatal("close leaked work or a connection")
	}
}

func TestCompletionRunningCancellationPreservesPeer(t *testing.T) {
	f := newClaimFixture(t)
	t.Cleanup(func() { _ = f.service.Close() })
	if _, err := f.db.Exec(`CREATE TABLE completed_cancel(id INTEGER PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	blocker, err := f.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Close()
	waits := f.db.Stats().WaitCount
	entered := make(chan struct{})
	first := submitTestCompletion(f.service, ctx, func(ctx context.Context, tx *sql.Tx) (any, error) {
		if _, err := tx.ExecContext(ctx, `INSERT INTO completed_cancel VALUES(1)`); err != nil {
			return nil, err
		}
		close(entered)
		var n int
		err := tx.QueryRowContext(ctx, `WITH RECURSIVE n(x) AS(VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x<100000000) SELECT SUM(x) FROM n`).Scan(&n)
		return n, err
	})
	awaitCompletionCondition(t, func() bool { return f.db.Stats().WaitCount > waits })
	second := submitTestCompletion(f.service, t.Context(), func(ctx context.Context, tx *sql.Tx) (any, error) {
		_, err := tx.ExecContext(ctx, `INSERT INTO completed_cancel VALUES(2)`)
		return 2, err
	})
	awaitCompletionCondition(t, func() bool { return pendingCompletions(f.service) == 1 })
	blocker.Close()
	<-entered
	cancel()
	if err := awaitCompletionResult(t, first); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := awaitCompletionResult(t, second); err != nil {
		t.Fatal(err)
	}
	f.service.Close()
	var id int
	if err := f.db.QueryRow(`SELECT id FROM completed_cancel`).Scan(&id); err != nil || id != 2 {
		t.Fatal("canceled changes persisted or peer was lost", id, err)
	}
}

func TestCompletionOverflowUsesSynchronousPathAndJoins(t *testing.T) {
	f := newClaimFixture(t)
	p := observability.NewPerformance()
	ctx := p.Context(t.Context())
	blocker, err := f.db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Close()
	waits := f.db.Stats().WaitCount
	run := func(context.Context, *sql.Tx) (any, error) { return 1, nil }
	results := []<-chan error{submitTestCompletion(f.service, ctx, run)}
	awaitCompletionCondition(t, func() bool { return f.db.Stats().WaitCount > waits })
	for range completionQueueLimit {
		results = append(results, submitTestCompletion(f.service, ctx, run))
	}
	awaitCompletionCondition(t, func() bool { return pendingCompletions(f.service) == completionQueueLimit })
	results = append(results, submitTestCompletion(f.service, ctx, run))
	awaitCompletionCondition(t, func() bool {
		f.service.completion.mu.Lock()
		defer f.service.completion.mu.Unlock()
		return f.service.completion.fallbacks == 1
	})
	blocker.Close()
	if err := f.service.CloseContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	for _, result := range results {
		if err := awaitCompletionResult(t, result); err != nil {
			t.Fatal("overflow rejected accepted work", err)
		}
	}
	var overflow int64
	for _, row := range p.Drain() {
		if row.Active != 0 {
			t.Fatalf("close failed to join measured work: %+v", row)
		}
		if row.Route == "completion.overflow" {
			overflow = row.Completed
		}
	}
	if overflow != 1 || f.db.Stats().InUse != 0 {
		t.Fatalf("overflow observation or cleanup: count=%d stats=%+v", overflow, f.db.Stats())
	}
}

func TestCompletionClosedDatabaseReturnsFailure(t *testing.T) {
	f := newClaimFixture(t)
	f.db.Close()
	done := submitTestCompletion(f.service, t.Context(), func(context.Context, *sql.Tx) (any, error) {
		t.Error("closed database executed a completion")
		return nil, nil
	})
	if err := awaitCompletionResult(t, done); err == nil {
		t.Fatal("closed database reported success")
	}
	if err := f.service.CloseContext(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestCompletionConcurrentReplayKeepsOneAccountingResult(t *testing.T) {
	f := newClaimFixture(t)
	owner := f.seedUser("completion-owner", false)
	key := f.seedKey(owner, "completion-key")
	request := f.acceptSelf(owner, 1)
	handle, err := f.service.Claim(t.Context(), ClaimInput{RequestID: request.ID, ActorUserID: owner, AttemptSeq: 1, Purpose: PurposeSelf, Candidate: key.candidate})
	if err != nil {
		t.Fatal(err)
	}
	dispatch, err := f.service.TakeForDispatch(t.Context(), handle)
	if err != nil {
		t.Fatal(err)
	}
	dispatch.Clear()
	outcome := AttemptOutcome{Kind: ResultResponse, UpstreamStatus: 200, ProtocolSuccess: true}
	attempts := make(chan Attempt, 8)
	errs := make(chan error, 8)
	for range 8 {
		go func() {
			attempt, err := f.service.CompleteAttempt(t.Context(), handle, outcome)
			attempts <- attempt
			errs <- err
		}()
	}
	var prior Attempt
	for i := range 8 {
		attempt := <-attempts
		if err := awaitCompletionResult(t, errs); err != nil || i > 0 && attempt != prior {
			t.Fatalf("attempt replay: %+v %v", attempt, err)
		}
		prior = attempt
	}
	input := CompleteRequestInput{RequestID: request.ID, Caller: CallerResult{Class: ResultSuccess, Status: 200}, Disposition: AccountingCommit, ActualChargeMilli: 100}
	for range 8 {
		go func() {
			result, err := f.service.CompleteRequest(t.Context(), input)
			if err == nil && result.State != RequestTerminal {
				err = errors.New("completion did not return terminal state")
			}
			errs <- err
		}()
	}
	for range 8 {
		if err := awaitCompletionResult(t, errs); err != nil {
			t.Fatal(err)
		}
	}
	f.requireCapacity(request.ID, 0, 0, 2)
	input.Caller = CallerResult{Class: ResultCancelled}
	if _, err := f.service.CompleteRequest(t.Context(), input); !errors.Is(err, ErrConflict) {
		t.Fatal("conflicting replay accepted", err)
	}
	f.requireCapacity(request.ID, 0, 0, 2)
}
