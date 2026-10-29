package riskaudit

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"
)

// Load each reader's schema before testing short worker deadlines. Hold every
// connection until all have been prepared so the pool cannot reuse just one.
func prepareScanReaders(t *testing.T, f *auditFixture) {
	t.Helper()
	reader := f.store.ReadDB()
	connections := make([]*sql.Conn, 0, reader.Stats().MaxOpenConnections)
	defer func() {
		for _, conn := range connections {
			_ = conn.Close()
		}
	}()
	for range cap(connections) {
		conn, err := reader.Conn(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		connections = append(connections, conn)
		var count int
		if err := conn.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM risk_client_scans").Scan(&count); err != nil {
			t.Fatal(err)
		}
	}
}

func TestScanBatchDeadlinePreservesCheckpointAndFailureBudget(t *testing.T) {
	f := newAuditFixture(t)
	scanRuleFixture(t, f)
	f.source(f.user(1), "self", "192.0.2.1", "direct_peer", "Example/1", "model", "success", 0)
	scan, err := f.repository.CreateScan(context.Background(), Actor{Admin: true, UserID: f.admin}, scanInput("deadline_batch_scan"))
	if err != nil {
		t.Fatal(err)
	}
	// Delay the final checkpoint write inside the transaction, after a result
	// has been inserted. A timeout must roll both back, even on repeated tries.
	_, err = f.store.DB().Exec(`CREATE TRIGGER slow_scan_checkpoint BEFORE UPDATE ON risk_client_scans WHEN NEW.scanned>OLD.scanned BEGIN
SELECT (WITH RECURSIVE busy(n) AS (SELECT 1 UNION ALL SELECT n+1 FROM busy WHERE n<1000000000) SELECT sum(n) FROM busy);
END`)
	if err != nil {
		t.Fatal(err)
	}
	prepareScanReaders(t, f)
	for range 3 {
		progress, err := f.repository.ProcessScanBatch(context.Background())
		var workError *scanWorkError
		if progress || !errors.Is(err, context.DeadlineExceeded) || !errors.As(err, &workError) || workError.phase != "batch" {
			t.Fatalf("deadline progress=%v error=%v", progress, err)
		}
		var state string
		var failures, scanned, matches int
		if err := f.store.DB().QueryRow(`SELECT state,failures,scanned,(SELECT COUNT(*) FROM risk_scan_results WHERE scan_id=?) FROM risk_client_scans WHERE id=?`, scan.ID, scan.ID).Scan(&state, &failures, &scanned, &matches); err != nil {
			t.Fatal(err)
		}
		if state != "queued" || failures != 0 || scanned != 0 || matches != 0 {
			t.Fatalf("retry changed checkpoint: state=%s failures=%d scanned=%d matches=%d", state, failures, scanned, matches)
		}
	}
	if _, err := f.store.DB().Exec(`DROP TRIGGER slow_scan_checkpoint`); err != nil {
		t.Fatal(err)
	}
	prepareScanReaders(t, f)
	finishScan(t, f.repository)
	var state string
	if err := f.store.DB().QueryRow(`SELECT state FROM risk_client_scans WHERE id=?`, scan.ID).Scan(&state); err != nil || state != "completed" {
		t.Fatalf("recovered state=%s err=%v", state, err)
	}
}

func TestScanDeterministicFailureStillStopsAtLimit(t *testing.T) {
	f := newAuditFixture(t)
	scanRuleFixture(t, f)
	f.source(f.user(1), "self", "192.0.2.1", "direct_peer", "Example/1", "model", "success", 0)
	scan, err := f.repository.CreateScan(context.Background(), Actor{Admin: true, UserID: f.admin}, scanInput("failed_batch_scan"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.DB().Exec(`CREATE TRIGGER reject_scan_checkpoint BEFORE UPDATE ON risk_client_scans WHEN NEW.scanned>OLD.scanned BEGIN SELECT RAISE(ABORT,'invalid scan checkpoint'); END`); err != nil {
		t.Fatal(err)
	}
	prepareScanReaders(t, f)
	for range 3 {
		if progress, err := f.repository.ProcessScanBatch(context.Background()); progress || err == nil || scanRetry(err) {
			t.Fatalf("deterministic failure: progress=%v err=%v", progress, err)
		}
	}
	var state, reason string
	var failures int
	if err := f.store.DB().QueryRow(`SELECT state,reason,failures FROM risk_client_scans WHERE id=?`, scan.ID).Scan(&state, &reason, &failures); err != nil || state != "failed" || reason != "scan_failed" || failures != 3 {
		t.Fatalf("state=%s reason=%s failures=%d err=%v", state, reason, failures, err)
	}
}

func TestScanWorkersWakeAfterCommitAndStop(t *testing.T) {
	f := newAuditFixture(t)
	scanRuleFixture(t, f)
	f.source(f.user(1), "self", "192.0.2.1", "direct_peer", "Example/1", "model", "success", 0)
	actor := Actor{Admin: true, UserID: f.admin}
	// A rolled-back creation must not wake workers before durable work exists.
	if _, err := f.store.DB().Exec(`CREATE TRIGGER reject_scan_creation BEFORE INSERT ON risk_client_scans BEGIN SELECT RAISE(ABORT,'reject creation'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.repository.CreateScan(context.Background(), actor, scanInput("rollback_scan_wake")); err == nil || len(f.repository.scanWake[0])+len(f.repository.scanWake[1]) != 0 {
		t.Fatalf("failed creation woke workers: %v", err)
	}
	if _, err := f.store.DB().Exec(`DROP TRIGGER reject_scan_creation`); err != nil {
		t.Fatal(err)
	}
	scans := make([]ClientScan, 0, 2)
	for _, token := range []string{"committed_scan_one", "committed_scan_two"} {
		scan, err := f.repository.CreateScan(context.Background(), actor, scanInput(token))
		if err != nil {
			t.Fatal(err)
		}
		scans = append(scans, scan)
	}
	if len(f.repository.scanWake[0]) != 1 || len(f.repository.scanWake[1]) != 1 {
		t.Fatal("creation did not notify both workers")
	}
	prepareScanReaders(t, f)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { f.repository.RunScans(ctx); close(done) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("scan workers did not stop")
		}
	})
	deadline := time.Now().Add(2 * time.Second)
	for {
		var completed int
		if err := f.store.DB().QueryRow(`SELECT COUNT(*) FROM risk_client_scans WHERE id IN (?,?) AND state='completed'`, scans[0].ID, scans[1].ID).Scan(&completed); err != nil {
			t.Fatal(err)
		}
		if completed == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("workers did not process notified scans")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestIdleScanWorkerAvoidsPollingAndWakesForCommittedWork(t *testing.T) {
	f := newAuditFixture(t)
	scanRuleFixture(t, f)
	f.source(f.user(1), "self", "192.0.2.1", "direct_peer", "Example/1", "model", "success", 0)
	readDB := f.store.ReadDB()
	readDB.SetMaxOpenConns(1)
	prepareScanReaders(t, f)
	conn, err := readDB.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	waits := readDB.Stats().WaitCount
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { f.repository.runScanWorker(ctx, false, f.repository.scanWake[1]); close(done) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("idle worker did not stop")
		}
	})
	deadline := time.Now().Add(time.Second)
	for readDB.Stats().WaitCount == waits {
		if time.Now().After(deadline) {
			t.Fatal("worker did not start its initial queue read")
		}
		time.Sleep(time.Millisecond)
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	// The worker already owns the returned connection for its empty queue read.
	// Acquiring it again places this test after that read has finished.
	conn, err = readDB.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	waits = readDB.Stats().WaitCount
	time.Sleep(1200 * time.Millisecond)
	if readDB.Stats().WaitCount != waits {
		t.Fatal("idle worker polled again after one second")
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	scan, err := f.repository.CreateScan(context.Background(), Actor{Admin: true, UserID: f.admin}, scanInput("idle_committed_scan"))
	if err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(time.Second)
	for {
		var state string
		if err := f.store.DB().QueryRow(`SELECT state FROM risk_client_scans WHERE id=?`, scan.ID).Scan(&state); err != nil {
			t.Fatal(err)
		}
		if state == "completed" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("committed work waited for the idle polling interval")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
