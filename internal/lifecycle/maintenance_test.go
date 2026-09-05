package lifecycle

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func recoveryAdaptersWithRecorder(record func(string)) RecoveryAdapters {
	makeAdapter := func(name string) RecoveryAdapter {
		return testRecoveryAdapter{name: name, run: func(_ context.Context, _ int64, limit int, deadline time.Time) (WorkResult, error) {
			if limit != WorkerBatchLimit || deadline.IsZero() {
				return WorkResult{}, ErrInvariant
			}
			record("recovery:" + name)
			return WorkResult{}, nil
		}}
	}
	return RecoveryAdapters{
		Idempotency: makeAdapter("idempotency"), Discovery: makeAdapter("discovery"), Claims: makeAdapter("claims"),
		Thursday: makeAdapter("thursday"), Reports: makeAdapter("reports"), Fishing: makeAdapter("fishing"),
		LinkLink: makeAdapter("linklink"), RPS: makeAdapter("rps"), Donations: makeAdapter("donations"), Secrets: makeAdapter("secrets"),
	}
}

func retentionAdaptersWithRecorder(record func(string)) RetentionAdapters {
	makeAdapter := func(name string) RetentionAdapter {
		return testRetentionAdapter{name: name, run: func(_ context.Context, _ int64, limit int, deadline time.Time) (WorkResult, error) {
			if limit != WorkerBatchLimit || deadline.IsZero() {
				return WorkResult{}, ErrInvariant
			}
			record("retention:" + name)
			return WorkResult{}, nil
		}}
	}
	return RetentionAdapters{
		Sessions: makeAdapter("sessions"), RequestLogs: makeAdapter("request_logs"), Audits: makeAdapter("audits"),
		Issues: makeAdapter("issues"), Fishing: makeAdapter("fishing"), LinkLink: makeAdapter("linklink"),
		RPS: makeAdapter("rps"), Reports: makeAdapter("reports"), Donations: makeAdapter("donations"),
		Charity: makeAdapter("charity"), Idempotency: makeAdapter("idempotency"), Secrets: makeAdapter("secrets"),
	}
}

func TestMaintenanceRunsFrozenRecoveryThenRetentionOrder(t *testing.T) {
	fixture := newLifecycleTestFixture(t, 100)
	var calls []string
	config := fixture.config
	config.Recovery = recoveryAdaptersWithRecorder(func(value string) { calls = append(calls, value) })
	config.Retention = retentionAdaptersWithRecorder(func(value string) { calls = append(calls, value) })
	coordinator := mustNewLifecycleCoordinator(t, config)
	if err := coordinator.RunMaintenance(context.Background()); err != nil {
		t.Fatalf("RunMaintenance: %v", err)
	}
	want := []string{
		"recovery:idempotency", "recovery:discovery", "recovery:claims", "recovery:thursday", "recovery:reports",
		"recovery:fishing", "recovery:linklink", "recovery:rps", "recovery:donations", "recovery:secrets",
		"retention:sessions", "retention:request_logs", "retention:audits", "retention:issues", "retention:fishing",
		"retention:linklink", "retention:rps", "retention:reports", "retention:donations", "retention:charity",
		"retention:idempotency", "retention:secrets",
	}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("maintenance order = %v, want %v", calls, want)
	}
}

func TestRecoveryContinuesAfterDomainFailureAndRejectsNonprogress(t *testing.T) {
	fixture := newLifecycleTestFixture(t, 100)
	var later atomic.Bool
	config := fixture.config
	config.Recovery.Idempotency = testRecoveryAdapter{run: func(context.Context, int64, int, time.Time) (WorkResult, error) {
		return WorkResult{More: true}, nil
	}}
	config.Recovery.Discovery = testRecoveryAdapter{run: func(context.Context, int64, int, time.Time) (WorkResult, error) {
		later.Store(true)
		return WorkResult{}, errors.New("later failure")
	}}
	coordinator := mustNewLifecycleCoordinator(t, config)
	err := coordinator.RecoverBeforeListener(context.Background())
	if !errors.Is(err, ErrInvariant) || !later.Load() || err == nil || !strings.Contains(err.Error(), "later failure") {
		t.Fatalf("RecoverBeforeListener error=%v later=%v", err, later.Load())
	}
}

func TestConcurrentDueTriggerAndMaintenanceShareOneCoveringPass(t *testing.T) {
	fixture := newLifecycleTestFixture(t, 100)
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	var recoveryCalls atomic.Int64
	var retentionCalls atomic.Int64
	blocking := testRecoveryAdapter{run: func(ctx context.Context, _ int64, _ int, _ time.Time) (WorkResult, error) {
		call := recoveryCalls.Add(1)
		if call == 1 {
			once.Do(func() { close(entered) })
			select {
			case <-release:
			case <-ctx.Done():
				return WorkResult{}, ctx.Err()
			}
		}
		return WorkResult{}, nil
	}}
	config := fixture.config
	config.Recovery.Idempotency = blocking
	config.Retention.Sessions = testRetentionAdapter{run: func(context.Context, int64, int, time.Time) (WorkResult, error) {
		retentionCalls.Add(1)
		return WorkResult{}, nil
	}}
	coordinator := mustNewLifecycleCoordinator(t, config)
	result := make(chan error, 1)
	go func() { result <- coordinator.RunDue(context.Background()) }()
	<-entered
	// Capture the second caller's arrival while the first pass is blocked.
	// Starting a goroutine alone does not prove that it observed this generation
	// before release; a later arrival correctly starts a separate maintenance pass.
	coordinator.runMu.Lock()
	observedGeneration := coordinator.runGeneration
	coordinator.runMu.Unlock()
	maintenanceResult := make(chan error, 1)
	go func() {
		maintenanceResult <- coordinator.runObservedSchedule(context.Background(), true, observedGeneration)
	}()
	close(release)
	if err := <-result; err != nil {
		t.Fatalf("RunDue: %v", err)
	}
	if err := <-maintenanceResult; err != nil {
		t.Fatalf("merged RunMaintenance: %v", err)
	}
	if recoveryCalls.Load() != 1 {
		t.Fatalf("recovery calls = %d, want 1", recoveryCalls.Load())
	}
	if retentionCalls.Load() != 1 {
		t.Fatalf("retention calls = %d, want 1", retentionCalls.Load())
	}
	if err := coordinator.RunMaintenance(context.Background()); err != nil {
		t.Fatalf("later RunMaintenance: %v", err)
	}
	if recoveryCalls.Load() != 2 || retentionCalls.Load() != 2 {
		t.Fatalf("later arrival calls recovery=%d retention=%d, want two each", recoveryCalls.Load(), retentionCalls.Load())
	}
}

func TestMaintenanceSuccessSchedulesBothWorkers(t *testing.T) {
	fixture := newLifecycleTestFixture(t, 100)
	var recoveryCalls atomic.Int64
	var retentionCalls atomic.Int64
	config := fixture.config
	config.Recovery.Idempotency = testRecoveryAdapter{run: func(context.Context, int64, int, time.Time) (WorkResult, error) {
		recoveryCalls.Add(1)
		return WorkResult{}, nil
	}}
	config.Retention.Sessions = testRetentionAdapter{run: func(context.Context, int64, int, time.Time) (WorkResult, error) {
		retentionCalls.Add(1)
		return WorkResult{}, nil
	}}
	coordinator := mustNewLifecycleCoordinator(t, config)
	if err := coordinator.RunDue(context.Background()); err != nil {
		t.Fatalf("first RunDue: %v", err)
	}
	if err := coordinator.RunDue(context.Background()); err != nil {
		t.Fatalf("second RunDue: %v", err)
	}
	if recoveryCalls.Load() != 1 || retentionCalls.Load() != 1 {
		t.Fatalf("calls recovery=%d retention=%d, want one each", recoveryCalls.Load(), retentionCalls.Load())
	}
	for _, testCase := range []struct {
		workerKey string
		interval  time.Duration
	}{
		{workerKey: lifecycleRecoveryWorkerKey, interval: WorkerSweepInterval},
		{workerKey: lifecycleRetentionWorkerKey, interval: MaintenanceInterval},
	} {
		var attempt, next int64
		var lastError string
		if err := fixture.store.DB().QueryRow(`
SELECT attempt_count,next_attempt_at,last_error_class
FROM worker_checkpoints WHERE worker_key=?`, testCase.workerKey).Scan(&attempt, &next, &lastError); err != nil {
			t.Fatalf("read %s checkpoint: %v", testCase.workerKey, err)
		}
		if attempt != 0 || next != 100+int64(testCase.interval/time.Second) || lastError != "" {
			t.Fatalf("%s checkpoint attempt=%d next=%d error=%q", testCase.workerKey, attempt, next, lastError)
		}
	}
	var alerts int
	if err := fixture.store.DB().QueryRow(`
SELECT COUNT(*) FROM admin_alerts WHERE kind='worker_checkpoint_failed'`).Scan(&alerts); err != nil {
		t.Fatalf("count worker alerts: %v", err)
	}
	if alerts != 0 {
		t.Fatalf("worker alerts = %d, want 0", alerts)
	}
}

func TestMaintenanceFailureBackoffDeduplicatesAndResolvesAlert(t *testing.T) {
	fixture := newLifecycleTestFixture(t, 100)
	var clock atomic.Int64
	clock.Store(100)
	var fail atomic.Bool
	fail.Store(true)
	var recoveryCalls atomic.Int64
	config := fixture.config
	config.Now = func() time.Time { return time.Unix(clock.Load(), 0) }
	config.Recovery.Idempotency = testRecoveryAdapter{run: func(context.Context, int64, int, time.Time) (WorkResult, error) {
		recoveryCalls.Add(1)
		if fail.Load() {
			return WorkResult{}, errors.New("database is busy")
		}
		return WorkResult{}, nil
	}}
	coordinator := mustNewLifecycleCoordinator(t, config)
	if err := coordinator.RunDue(context.Background()); err == nil {
		t.Fatal("first RunDue unexpectedly succeeded")
	}
	assertLifecycleWorkerState(t, fixture, lifecycleRecoveryWorkerKey, 1, 130, "db_busy")
	assertLifecycleWorkerAlerts(t, fixture, 1, 1, 0)

	clock.Store(129)
	if err := coordinator.RunDue(context.Background()); err != nil {
		t.Fatalf("RunDue before retry: %v", err)
	}
	if recoveryCalls.Load() != 1 {
		t.Fatalf("recovery calls before due = %d, want 1", recoveryCalls.Load())
	}

	clock.Store(130)
	if err := coordinator.RunDue(context.Background()); err == nil {
		t.Fatal("second due RunDue unexpectedly succeeded")
	}
	assertLifecycleWorkerState(t, fixture, lifecycleRecoveryWorkerKey, 2, 190, "db_busy")
	assertLifecycleWorkerAlerts(t, fixture, 1, 1, 0)

	fail.Store(false)
	clock.Store(190)
	if err := coordinator.RunDue(context.Background()); err != nil {
		t.Fatalf("successful retry RunDue: %v", err)
	}
	assertLifecycleWorkerState(t, fixture, lifecycleRecoveryWorkerKey, 0,
		190+int64(WorkerSweepInterval/time.Second), "")
	assertLifecycleWorkerAlerts(t, fixture, 1, 0, 1)
	if recoveryCalls.Load() != 3 {
		t.Fatalf("recovery calls = %d, want 3", recoveryCalls.Load())
	}
}

func assertLifecycleWorkerState(t *testing.T, fixture *lifecycleTestFixture, workerKey string,
	wantAttempt, wantNext int64, wantError string) {
	t.Helper()
	var attempt, next int64
	var lastError string
	if err := fixture.store.DB().QueryRow(`
SELECT attempt_count,next_attempt_at,last_error_class
FROM worker_checkpoints WHERE worker_key=?`, workerKey).Scan(&attempt, &next, &lastError); err != nil {
		t.Fatalf("read %s checkpoint: %v", workerKey, err)
	}
	if attempt != wantAttempt || next != wantNext || lastError != wantError {
		t.Fatalf("%s checkpoint attempt=%d next=%d error=%q, want %d/%d/%q",
			workerKey, attempt, next, lastError, wantAttempt, wantNext, wantError)
	}
}

func assertLifecycleWorkerAlerts(t *testing.T, fixture *lifecycleTestFixture, wantTotal, wantOpen, wantResolved int) {
	t.Helper()
	var total, open, resolved int
	if err := fixture.store.DB().QueryRow(`
SELECT COUNT(*),
 COALESCE(SUM(CASE WHEN resolved=0 THEN 1 ELSE 0 END),0),
 COALESCE(SUM(CASE WHEN resolved=1 THEN 1 ELSE 0 END),0)
FROM admin_alerts WHERE kind='worker_checkpoint_failed' AND ref=?`, lifecycleRecoveryWorkerKey).Scan(
		&total, &open, &resolved); err != nil {
		t.Fatalf("read worker alerts: %v", err)
	}
	if total != wantTotal || open != wantOpen || resolved != wantResolved {
		t.Fatalf("worker alerts total/open/resolved=%d/%d/%d, want %d/%d/%d",
			total, open, resolved, wantTotal, wantOpen, wantResolved)
	}
}
