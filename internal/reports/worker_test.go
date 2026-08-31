package reports

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
	sharedworker "github.com/waiting-here/NonbiriAPI/internal/worker"
)

func approvePreparedCase(t *testing.T, environment *reportTestEnvironment, caseID string, adminKey int) (int64, authz.Actor) {
	t.Helper()
	admin := environment.seedActor(t, true, 1)
	_, _, materialVersion, targetVersion := environment.caseState(t, caseID)
	result, err := environment.repository.Approve(context.Background(), admin, caseID, ApproveCommand{
		ExpectedMaterialVersion: materialVersion,
		ExpectedTargetVersion:   targetVersion,
		Reason:                  "confirmed compromised credential",
		Confirmation:            true,
		IdempotencyKey:          reportTestKey(adminKey),
	})
	if err != nil {
		t.Fatalf("approve report case: %v", err)
	}
	if result.Status != 202 || result.Value.Status != "approved_processing" {
		t.Fatalf("approval result=%+v", result)
	}
	return targetVersion, admin
}

func TestWorkerCheckpointsIndexingAndApprovalAtOneHundredRows(t *testing.T) {
	environment := newReportTestEnvironment(t)
	// Race instrumentation makes the deliberate 100-row deletion batch exceed
	// the production two-second transaction budget. This test isolates cursor
	// semantics, so give only this repository a longer bounded budget.
	environment.repository.workerLimit = 30 * time.Second
	owner := environment.seedActor(t, false, 1)
	endpointID := environment.seedEndpoint(t, owner.UserID, reportTestConnector, reportTestBaseURL)
	for index := 0; index < workerBatchLimit+1; index++ {
		environment.seedEndpointKey(t, endpointID, "checkpoint-secret", reportTestNow)
	}
	environment.accept(t, "checkpoint-secret", "checkpoint material", 2000, 1, nil)
	caseID := environment.caseIDForSecret(t, "checkpoint-secret")

	first, err := environment.repository.RunWorkerOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !first.More || first.CasesProcessed != 1 {
		t.Fatalf("first indexing result=%+v", first)
	}
	status, progress, _, _ := environment.caseState(t, caseID)
	if status != "pending_indexing" || progress != "in_progress" {
		t.Fatalf("first indexing state=%s/%s", status, progress)
	}
	if got := environment.rowCount(t, `SELECT COUNT(*) FROM report_targets WHERE case_id=?`, caseID); got != workerBatchLimit {
		t.Fatalf("first indexing target count=%d", got)
	}

	second, err := environment.repository.RunWorkerOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if second.More || second.CasesProcessed != 1 {
		t.Fatalf("second indexing result=%+v", second)
	}
	status, progress, _, _ = environment.caseState(t, caseID)
	if status != "pending_review" || progress != "complete" {
		t.Fatalf("completed indexing state=%s/%s", status, progress)
	}
	if got := environment.rowCount(t, `SELECT target_count FROM report_cases WHERE id=?`, caseID); got != workerBatchLimit+1 {
		t.Fatalf("completed target count=%d", got)
	}

	_, _ = approvePreparedCase(t, environment, caseID, 2001)
	third, err := environment.repository.RunWorkerOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !third.More || third.CasesProcessed != 1 {
		t.Fatalf("first approval result=%+v", third)
	}
	status, progress, _, _ = environment.caseState(t, caseID)
	if status != "approved_processing" || progress != "in_progress" {
		t.Fatalf("first approval state=%s/%s", status, progress)
	}
	if got := environment.rowCount(t, `SELECT processed_target_count FROM report_cases WHERE id=?`, caseID); got != workerBatchLimit {
		t.Fatalf("first approval processed=%d", got)
	}

	fourth, err := environment.repository.RunWorkerOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if fourth.More || fourth.CasesProcessed != 1 {
		t.Fatalf("second approval result=%+v", fourth)
	}
	status, progress, _, _ = environment.caseState(t, caseID)
	if status != "approved" || progress != "complete" {
		t.Fatalf("completed approval state=%s/%s", status, progress)
	}
	if got := environment.rowCount(t, `SELECT COUNT(*) FROM endpoint_keys WHERE endpoint_id=?`, endpointID); got != 0 {
		t.Fatalf("approved keys remaining=%d", got)
	}
	if environment.deletions.callCount() != workerBatchLimit+1 {
		t.Fatalf("deletion calls=%d", environment.deletions.callCount())
	}
}

func TestApprovalBatchFailureRollsBackEveryDatabaseEffect(t *testing.T) {
	hook := &reportTestDeletionHook{failOnCall: map[int]bool{2: true}}
	environment := newReportTestEnvironmentWith(t, reportTestOptions{deleteHook: hook})
	_, keyIDs, caseID := prepareReview(t, environment, "rollback-secret", 3)
	_, _ = approvePreparedCase(t, environment, caseID, 2100)

	result, err := environment.repository.RunWorkerOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.CasesProcessed != 1 {
		t.Fatalf("failed pass result=%+v", result)
	}
	if hook.callCount() != 2 {
		t.Fatalf("deletion calls before rollback=%d", hook.callCount())
	}
	if got := environment.rowCount(t, `SELECT COUNT(*) FROM endpoint_keys WHERE id IN (?,?,?)`, keyIDs[0], keyIDs[1], keyIDs[2]); got != 3 {
		t.Fatalf("keys surviving rollback=%d", got)
	}
	if got := environment.rowCount(t, `SELECT COUNT(*) FROM endpoint_key_secrets WHERE orphaned_at IS NOT NULL`); got != 0 {
		t.Fatalf("orphan markers surviving rollback=%d", got)
	}
	if got := environment.rowCount(t, `SELECT COUNT(*) FROM report_targets WHERE case_id=? AND state='protected'`, caseID); got != 3 {
		t.Fatalf("protected targets after rollback=%d", got)
	}
	var attempt, nextRetry int64
	var errorClass string
	if err := environment.store.DB().QueryRow(`SELECT retry_attempt_count,next_retry_at,last_error_class
FROM report_cases WHERE id=?`, caseID).Scan(&attempt, &nextRetry, &errorClass); err != nil {
		t.Fatal(err)
	}
	if attempt != 1 || nextRetry != reportTestNow+30 || errorClass != string(sharedworker.ErrorRetryable) {
		t.Fatalf("retry state attempt=%d next=%d class=%s", attempt, nextRetry, errorClass)
	}

	hook.mu.Lock()
	hook.failOnCall = nil
	hook.mu.Unlock()
	environment.setNow(nextRetry)
	environment.runUntilIdle(t, 3)
	status, progress, _, _ := environment.caseState(t, caseID)
	if status != "approved" || progress != "complete" {
		t.Fatalf("recovered state=%s/%s", status, progress)
	}
}

func TestApprovalRetryBackoffAlertAndAdminResume(t *testing.T) {
	hook := &reportTestDeletionHook{failAlways: true}
	environment := newReportTestEnvironmentWith(t, reportTestOptions{deleteHook: hook})
	_, _, caseID := prepareReview(t, environment, "retry-secret", 1)
	targetVersion, admin := approvePreparedCase(t, environment, caseID, 2200)

	now := reportTestNow
	for attempt := int64(1); attempt <= 10; attempt++ {
		environment.setNow(now)
		if _, err := environment.repository.RunWorkerOnce(context.Background()); err != nil {
			t.Fatalf("retry pass %d: %v", attempt, err)
		}
		var persistedAttempt, nextRetry int64
		var class, operationState string
		if err := environment.store.DB().QueryRow(`SELECT retry_attempt_count,next_retry_at,last_error_class
FROM report_cases WHERE id=?`, caseID).Scan(&persistedAttempt, &nextRetry, &class); err != nil {
			t.Fatal(err)
		}
		if err := environment.store.DB().QueryRow(`SELECT state FROM accepted_operations
WHERE kind='report_approved_processing' AND checkpoint=? ORDER BY created_at DESC,id DESC LIMIT 1`, caseID).Scan(&operationState); err != nil {
			t.Fatal(err)
		}
		wantNext := now + sharedworker.RetryDelay(attempt)
		if persistedAttempt != attempt || nextRetry != wantNext || class != string(sharedworker.ErrorRetryable) || operationState != "failed_retryable" {
			t.Fatalf("attempt %d persisted=%d next=%d want=%d class=%s operation=%s",
				attempt, persistedAttempt, nextRetry, wantNext, class, operationState)
		}
		now = nextRetry
	}
	if got := environment.rowCount(t, `SELECT COUNT(*) FROM admin_alerts
WHERE kind='report_retry_exhausted' AND ref=? AND resolved=0`, caseID); got != 1 {
		t.Fatalf("retry exhausted alerts=%d", got)
	}
	var message string
	if err := environment.store.DB().QueryRow(`SELECT message FROM admin_alerts
WHERE kind='report_retry_exhausted' AND ref=?`, caseID).Scan(&message); err != nil {
		t.Fatal(err)
	}
	if message != "report_approved_processing:internal_retryable:attempt_10" {
		t.Fatalf("retry alert message=%q", message)
	}

	hook.mu.Lock()
	hook.failAlways = false
	hook.mu.Unlock()
	resumeNow := environment.clock.Load()
	if _, err := environment.store.DB().Exec(`UPDATE sessions SET last_seen_at=?,expires_at=?,absolute_expires_at=?
WHERE token_hash=?`, resumeNow, resumeNow+3600, resumeNow+7200, admin.SessionTokenHash); err != nil {
		t.Fatal(err)
	}
	resume, err := environment.repository.Resume(context.Background(), admin, caseID, ResumeCommand{
		ExpectedTargetVersion: targetVersion,
		IdempotencyKey:        reportTestKey(2201),
	})
	if err != nil {
		t.Fatal(err)
	}
	if resume.Status != 202 || resume.Value.Status != "approved_processing" {
		t.Fatalf("resume result=%+v", resume)
	}
	replay, err := environment.repository.Resume(context.Background(), admin, caseID, ResumeCommand{
		ExpectedTargetVersion: targetVersion,
		IdempotencyKey:        reportTestKey(2201),
	})
	if err != nil || !replay.Replayed {
		t.Fatalf("resume replay=%+v err=%v", replay, err)
	}
	environment.runUntilIdle(t, 3)
	status, progress, _, _ := environment.caseState(t, caseID)
	if status != "approved" || progress != "complete" {
		t.Fatalf("resumed state=%s/%s", status, progress)
	}
	if got := environment.rowCount(t, `SELECT COUNT(*) FROM report_decisions
WHERE case_id=? AND action='resume_processing'`, caseID); got != 1 {
		t.Fatalf("resume decisions=%d", got)
	}
}

func TestWorkerInvariantBlocksUntilExplicitResume(t *testing.T) {
	hook := &reportTestDeletionHook{failAlways: true, failure: ErrInvariant}
	environment := newReportTestEnvironmentWith(t, reportTestOptions{deleteHook: hook})
	_, _, caseID := prepareReview(t, environment, "invariant-secret", 1)
	targetVersion, admin := approvePreparedCase(t, environment, caseID, 2300)
	if _, err := environment.repository.RunWorkerOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	var attempt, nextRetry int64
	var class, operationState string
	if err := environment.store.DB().QueryRow(`SELECT retry_attempt_count,next_retry_at,last_error_class
FROM report_cases WHERE id=?`, caseID).Scan(&attempt, &nextRetry, &class); err != nil {
		t.Fatal(err)
	}
	if err := environment.store.DB().QueryRow(`SELECT state FROM accepted_operations
WHERE kind='report_approved_processing' AND checkpoint=?`, caseID).Scan(&operationState); err != nil {
		t.Fatal(err)
	}
	if attempt != 1 || nextRetry != blockedRetryAt || class != string(sharedworker.ErrorInvariant) || operationState != "failed_blocked" {
		t.Fatalf("blocked state attempt=%d next=%d class=%s operation=%s", attempt, nextRetry, class, operationState)
	}
	if got := environment.rowCount(t, `SELECT COUNT(*) FROM admin_alerts
WHERE kind='invariant_violation' AND ref=? AND resolved=0`, caseID); got != 1 {
		t.Fatalf("invariant alerts=%d", got)
	}
	if _, err := environment.repository.Resume(context.Background(), admin, caseID, ResumeCommand{
		ExpectedTargetVersion: targetVersion,
		IdempotencyKey:        reportTestKey(2301),
	}); err != nil {
		t.Fatal(err)
	}
	if got := environment.rowCount(t, `SELECT COUNT(*) FROM accepted_operations
WHERE kind='report_approved_processing' AND checkpoint=?`, caseID); got != 2 {
		t.Fatalf("operations after blocked resume=%d", got)
	}
	if _, err := environment.repository.RunWorkerOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := environment.rowCount(t, `SELECT COUNT(*) FROM accepted_operations
WHERE kind='report_approved_processing' AND checkpoint=? AND state='failed_blocked'`, caseID); got != 2 {
		t.Fatalf("blocked operation history=%d", got)
	}
	hook.mu.Lock()
	hook.failAlways = false
	hook.mu.Unlock()
	if _, err := environment.repository.Resume(context.Background(), admin, caseID, ResumeCommand{
		ExpectedTargetVersion: targetVersion,
		IdempotencyKey:        reportTestKey(2302),
	}); err != nil {
		t.Fatal(err)
	}
	environment.runUntilIdle(t, 3)
	status, progress, _, _ := environment.caseState(t, caseID)
	if status != "approved" || progress != "complete" {
		t.Fatalf("repeated blocked resume state=%s/%s", status, progress)
	}
	if got := environment.rowCount(t, `SELECT COUNT(*) FROM accepted_operations
WHERE kind='report_approved_processing' AND checkpoint=?`, caseID); got != 0 {
		t.Fatalf("operation history after completion=%d", got)
	}
}

func TestRecoverBeforeListenerResetsCrashLeftOperations(t *testing.T) {
	t.Run("indexing", func(t *testing.T) {
		environment := newReportTestEnvironment(t)
		owner := environment.seedActor(t, false, 1)
		endpointID := environment.seedEndpoint(t, owner.UserID, reportTestConnector, reportTestBaseURL)
		environment.seedEndpointKey(t, endpointID, "recover-indexing", reportTestNow)
		environment.accept(t, "recover-indexing", "", 2400, 1, nil)
		caseID := environment.caseIDForSecret(t, "recover-indexing")
		if _, err := environment.store.DB().Exec(`UPDATE accepted_operations SET state='running'
WHERE kind='report_indexing' AND checkpoint=?`, caseID); err != nil {
			t.Fatal(err)
		}
		result, err := environment.repository.RecoverBeforeListener(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if result.CasesProcessed != 1 {
			t.Fatalf("recovery result=%+v", result)
		}
		status, progress, _, _ := environment.caseState(t, caseID)
		if status != "pending_review" || progress != "complete" {
			t.Fatalf("recovered indexing state=%s/%s", status, progress)
		}
	})

	t.Run("approval", func(t *testing.T) {
		environment := newReportTestEnvironment(t)
		_, _, caseID := prepareReview(t, environment, "recover-approval", 1)
		_, _ = approvePreparedCase(t, environment, caseID, 2410)
		if _, err := environment.store.DB().Exec(`UPDATE accepted_operations SET state='running'
WHERE kind='report_approved_processing' AND checkpoint=?`, caseID); err != nil {
			t.Fatal(err)
		}
		result, err := environment.repository.RecoverBeforeListener(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if result.CasesProcessed != 1 {
			t.Fatalf("recovery result=%+v", result)
		}
		status, progress, _, _ := environment.caseState(t, caseID)
		if status != "approved" || progress != "complete" {
			t.Fatalf("recovered approval state=%s/%s", status, progress)
		}
	})
}

func TestWorkerAndMutationsRejectClosedRepository(t *testing.T) {
	environment := newReportTestEnvironment(t)
	if err := environment.repository.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := environment.repository.RunWorkerOnce(context.Background()); !errors.Is(err, ErrClosed) {
		t.Fatalf("worker after close error=%v", err)
	}
}

func TestBackgroundWorkerKickDuplicateStartAndClose(t *testing.T) {
	environment := newReportTestEnvironment(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := environment.repository.StartWorker(ctx); err != nil {
		t.Fatal(err)
	}
	if err := environment.repository.StartWorker(ctx); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate worker start error=%v", err)
	}
	owner := environment.seedActor(t, false, 1)
	endpointID := environment.seedEndpoint(t, owner.UserID, reportTestConnector, reportTestBaseURL)
	environment.seedEndpointKey(t, endpointID, "background-worker", reportTestNow)
	environment.accept(t, "background-worker", "", 2500, 1, nil)
	caseID := environment.caseIDForSecret(t, "background-worker")
	deadline := time.After(5 * time.Second)
	for {
		var status string
		if err := environment.store.DB().QueryRow(`SELECT status FROM report_cases WHERE id=?`, caseID).Scan(&status); err != nil {
			t.Fatal(err)
		}
		if status == "pending_review" {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("background worker did not process case; status=%s", status)
		case <-time.After(10 * time.Millisecond):
		}
	}
	if err := environment.repository.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := environment.repository.RunWorkerOnce(context.Background()); !errors.Is(err, ErrClosed) {
		t.Fatalf("worker after background close error=%v", err)
	}
}
