package reports

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	sharedworker "github.com/waiting-here/NonbiriAPI/internal/worker"
)

type deadlineReportProjection struct {
	calls            int
	partialTargets   int
	deadlineObserved bool
}

func (hook *deadlineReportProjection) ReconcileReportReason(ctx context.Context, tx *sql.Tx, _, _ int64) error {
	hook.calls++
	if hook.calls != 2 {
		return nil
	}
	if _, bounded := ctx.Deadline(); !bounded {
		return errors.New("projection expected a bounded worker context")
	}
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM report_targets`).Scan(&hook.partialTargets); err != nil {
		return err
	}
	<-ctx.Done()
	hook.deadlineObserved = errors.Is(context.Cause(ctx), context.DeadlineExceeded)
	return context.Cause(ctx)
}

func TestIndexingDeadlineRollsBackAndResumesAfterBackoff(t *testing.T) {
	hook := &deadlineReportProjection{}
	environment := newReportTestEnvironmentWith(t, reportTestOptions{issueProjection: hook})
	if environment.repository.workerLimit != 2*time.Second {
		t.Fatalf("production transaction budget=%s", environment.repository.workerLimit)
	}
	owner := environment.seedActor(t, false, 1)
	endpointID := environment.seedEndpoint(t, owner.UserID, reportTestConnector, reportTestBaseURL)
	environment.seedEndpointKey(t, endpointID, "deadline-secret", reportTestNow)
	environment.accept(t, "deadline-secret", "deadline material", 3600, 1, nil)
	caseID := environment.caseIDForSecret(t, "deadline-secret")
	hook.calls = 0
	// Add live targets after acceptance so the worker must restore their
	// report suspensions and project both transitions inside its transaction.
	for range 2 {
		environment.seedEndpointKey(t, endpointID, "deadline-secret", reportTestNow)
	}

	first, err := environment.repository.RunWorkerOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if first.CasesProcessed != 1 || first.More || !hook.deadlineObserved || hook.partialTargets != 3 {
		t.Fatalf("deadline result=%+v observed=%v partial targets=%d calls=%d", first, hook.deadlineObserved, hook.partialTargets, hook.calls)
	}
	for table, want := range map[string]int64{"report_targets": 0, "endpoint_key_suspensions": 1} {
		if got := environment.rowCount(t, `SELECT COUNT(*) FROM `+table); got != want {
			t.Fatalf("%s after deadline rollback=%d want=%d", table, got, want)
		}
	}
	var status, progress, errorClass, operationState string
	var cursorSource sql.NullString
	var cursorID sql.NullInt64
	var attempt, nextRetry, targetCount, processedCount int64
	if err := environment.store.DB().QueryRow(`SELECT status,progress_state,cursor_source,cursor_id,
retry_attempt_count,next_retry_at,last_error_class,target_count,processed_target_count
FROM report_cases WHERE id=?`, caseID).Scan(&status, &progress, &cursorSource, &cursorID,
		&attempt, &nextRetry, &errorClass, &targetCount, &processedCount); err != nil {
		t.Fatal(err)
	}
	if err := environment.store.DB().QueryRow(`SELECT state FROM accepted_operations
WHERE kind='report_indexing' AND checkpoint=?`, caseID).Scan(&operationState); err != nil {
		t.Fatal(err)
	}
	if status != "pending_indexing" || progress != "in_progress" || cursorSource.Valid || cursorID.Valid ||
		attempt != 1 || nextRetry != reportTestNow+30 || errorClass != string(sharedworker.ErrorRetryable) ||
		targetCount != 0 || processedCount != 0 || operationState != "failed_retryable" {
		t.Fatalf("deadline state=%s/%s cursor=%v/%v retry=%d/%d/%s counts=%d/%d operation=%s",
			status, progress, cursorSource, cursorID, attempt, nextRetry, errorClass, targetCount, processedCount, operationState)
	}
	t.Logf("deadline result=%+v rollback targets=%d cursor=%v/%v retry=%d/%d/%s operation=%s",
		first, targetCount, cursorSource, cursorID, attempt, nextRetry, errorClass, operationState)

	environment.setNow(nextRetry - 1)
	beforeDue, err := environment.repository.RunWorkerOnce(context.Background())
	if err != nil || beforeDue.CasesProcessed != 0 || beforeDue.More || hook.calls != 2 {
		t.Fatalf("retry before due=%+v calls=%d err=%v", beforeDue, hook.calls, err)
	}
	environment.setNow(nextRetry)
	recovered, err := environment.repository.RecoverBeforeListener(context.Background())
	if err != nil || recovered.CasesProcessed != 1 || !recovered.More {
		t.Fatalf("recovery pass=%+v err=%v", recovered, err)
	}
	environment.runUntilIdle(t, 3)
	status, progress, _, _ = environment.caseState(t, caseID)
	if status != "pending_review" || progress != "complete" {
		t.Fatalf("recovered state=%s/%s", status, progress)
	}
	for _, table := range []string{"report_targets", "endpoint_key_suspensions"} {
		if got := environment.rowCount(t, `SELECT COUNT(*) FROM `+table); got != 3 {
			t.Fatalf("recovered %s count=%d", table, got)
		}
	}
	if got := environment.rowCount(t, `SELECT COUNT(*) FROM accepted_operations WHERE kind='report_indexing' AND checkpoint=?`, caseID); got != 0 {
		t.Fatalf("completed operation remains=%d", got)
	}
}
