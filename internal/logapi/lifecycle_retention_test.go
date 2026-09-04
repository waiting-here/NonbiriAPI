package logapi

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/db"
)

type requestLogHeldReadStub struct {
	allow bool
	calls int
	id    int64
}

func (stub *requestLogHeldReadStub) AuthorizeHeldRequestLogRead(
	_ context.Context,
	_ *sql.Tx,
	requestLogID int64,
	_ int64,
) (bool, error) {
	stub.calls++
	stub.id = requestLogID
	return stub.allow, nil
}

func TestRequestLogOrdinaryCutoffAndKnownIDHeldRead(t *testing.T) {
	fixture := newLifecycleLogDatabase(t)
	userID := seedLifecycleLogUser(t, fixture.store.DB(), "held-read-log-owner", false)
	decisionNow := int64(1_790_000_000)
	requestID, err := db.GenerateOpaqueID("req_")
	if err != nil {
		t.Fatal(err)
	}
	completedAt := decisionNow - requestLogRetentionSeconds
	logID := insertLifecycleRequestLog(t, fixture.store.DB(), requestID, userID,
		string(ResultSuccess), 200, nil, completedAt-10, completedAt, 10, 0)
	fixture.repo.now = func() time.Time { return time.Unix(decisionNow, 0).UTC() }
	hook := &requestLogHeldReadStub{allow: true}
	if err := fixture.repo.AttachAdminHeldReadAuthorizer(hook); err != nil {
		t.Fatal(err)
	}

	if _, err := fixture.repo.GetUser(context.Background(), userID, requestID, AttemptFilter{Limit: 10}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("user detail at retention boundary = %v, want not found", err)
	}
	userPage, err := fixture.repo.ListUser(context.Background(), userID, ListFilter{Limit: 10})
	if err != nil || len(userPage.Data) != 0 {
		t.Fatalf("user list at retention boundary = %d, %v", len(userPage.Data), err)
	}
	adminPage, err := fixture.repo.ListAdmin(context.Background(), ListFilter{Limit: 10})
	if err != nil || len(adminPage.Data) != 0 {
		t.Fatalf("admin ordinary list at retention boundary = %d, %v", len(adminPage.Data), err)
	}
	exported, err := fixture.repo.ExportAdmin(context.Background(), ListFilter{})
	if err != nil || len(exported) != 0 {
		t.Fatalf("admin ordinary export at retention boundary = %d, %v", len(exported), err)
	}
	if _, err := fixture.repo.GetAdmin(context.Background(), requestID, AttemptFilter{Limit: 10}); err != nil {
		t.Fatalf("active held known-ID request-log read = %v", err)
	}
	if hook.calls != 1 || hook.id != logID {
		t.Fatalf("held request-log hook calls=%d id=%d", hook.calls, hook.id)
	}
	hook.allow = false
	if _, err := fixture.repo.GetAdmin(context.Background(), requestID, AttemptFilter{Limit: 10}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unheld known-ID request-log read at retention boundary = %v, want not found", err)
	}
}

func insertTerminalLifecycleLog(
	t *testing.T,
	fixture *lifecycleLogDatabase,
	userID, completedAt int64,
	withAttempt bool,
) int64 {
	t.Helper()
	requestID, err := db.GenerateOpaqueID("req_")
	if err != nil {
		t.Fatal(err)
	}
	logID := insertLifecycleRequestLog(
		t, fixture.store.DB(), requestID, userID,
		string(ResultSuccess), 200, nil, completedAt-10, completedAt, 10, 0,
	)
	if withAttempt {
		insertLifecycleAttempt(t, fixture.store.DB(), logID, completedAt-9, completedAt-8)
	}
	return logID
}

func requireRequestLogAggregateCounts(t *testing.T, fixture *lifecycleLogDatabase, logID int64, roots, attempts int) {
	t.Helper()
	var gotRoots, gotAttempts int
	if err := fixture.store.DB().QueryRow(`SELECT COUNT(*) FROM request_logs WHERE id=?`, logID).Scan(&gotRoots); err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.DB().QueryRow(`SELECT COUNT(*) FROM request_attempts WHERE request_log_id=?`, logID).Scan(&gotAttempts); err != nil {
		t.Fatal(err)
	}
	if gotRoots != roots || gotAttempts != attempts {
		t.Fatalf("request-log aggregate %d = roots:%d attempts:%d, want roots:%d attempts:%d",
			logID, gotRoots, gotAttempts, roots, attempts)
	}
}

func TestLifecycleRequestLogRetentionBoundariesAggregateHoldAndNonterminal(t *testing.T) {
	fixture := newLifecycleLogDatabase(t)
	userID := seedLifecycleLogUser(t, fixture.store.DB(), "retention-owner", false)
	adminID := seedLifecycleLogUser(t, fixture.store.DB(), "", true)
	decisionNow := int64(1_800_000_000)
	deadline := time.Now().Add(5 * time.Second)

	exactID := insertTerminalLifecycleLog(
		t, fixture, userID, decisionNow-requestLogRetentionSeconds, true,
	)
	before, err := fixture.repo.RetainLifecycleRequestLogs(
		context.Background(), decisionNow-1, 100, deadline,
	)
	if err != nil || before != (LifecycleRetentionResult{}) {
		t.Fatalf("request-log retention before boundary = %+v, %v", before, err)
	}
	requireRequestLogAggregateCounts(t, fixture, exactID, 1, 1)

	atBoundary, err := fixture.repo.RetainLifecycleRequestLogs(
		context.Background(), decisionNow, 100, deadline,
	)
	if err != nil || atBoundary != (LifecycleRetentionResult{
		RequestLogsDeleted: 1, Processed: 1,
	}) {
		t.Fatalf("request-log retention at boundary = %+v, %v", atBoundary, err)
	}
	requireRequestLogAggregateCounts(t, fixture, exactID, 0, 0)

	afterID := insertTerminalLifecycleLog(
		t, fixture, userID, decisionNow-requestLogRetentionSeconds+1, true,
	)
	pastID := insertTerminalLifecycleLog(
		t, fixture, userID, decisionNow-requestLogRetentionSeconds-1, true,
	)
	heldID := insertTerminalLifecycleLog(
		t, fixture, userID, decisionNow-requestLogRetentionSeconds-2, true,
	)
	pendingRequestID, err := db.GenerateOpaqueID("req_")
	if err != nil {
		t.Fatal(err)
	}
	pendingID := insertLifecycleRequestLog(
		t, fixture.store.DB(), pendingRequestID, userID,
		nil, nil, nil, decisionNow-requestLogRetentionSeconds-100, nil, 0, 0,
	)
	insertRequestLogLegalHold(t, fixture.store.DB(), heldID, adminID, decisionNow-100)
	tx, err := fixture.store.DB().BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.repo.ConsumeMarker(context.Background(), tx, fmt.Sprintf("%d", heldID)); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	retained, err := fixture.repo.RetainLifecycleRequestLogs(
		context.Background(), decisionNow, 100, deadline,
	)
	if err != nil || retained != (LifecycleRetentionResult{
		RequestLogsDeleted: 1, Processed: 1,
	}) {
		t.Fatalf("request-log mixed retention = %+v, %v", retained, err)
	}
	requireRequestLogAggregateCounts(t, fixture, pastID, 0, 0)
	requireRequestLogAggregateCounts(t, fixture, afterID, 1, 1)
	requireRequestLogAggregateCounts(t, fixture, heldID, 1, 1)
	requireRequestLogAggregateCounts(t, fixture, pendingID, 1, 0)

	afterBoundary, err := fixture.repo.RetainLifecycleRequestLogs(
		context.Background(), decisionNow+1, 100, deadline,
	)
	if err != nil || afterBoundary != (LifecycleRetentionResult{
		RequestLogsDeleted: 1, Processed: 1,
	}) {
		t.Fatalf("request-log retention after boundary = %+v, %v", afterBoundary, err)
	}
	requireRequestLogAggregateCounts(t, fixture, afterID, 0, 0)
	requireRequestLogAggregateCounts(t, fixture, heldID, 1, 1)
	requireRequestLogAggregateCounts(t, fixture, pendingID, 1, 0)
}

func TestLifecycleRequestLogRetentionLimitAndExactMore(t *testing.T) {
	fixture := newLifecycleLogDatabase(t)
	userID := seedLifecycleLogUser(t, fixture.store.DB(), "retention-limit", false)
	decisionNow := int64(1_810_000_000)
	for index := int64(0); index < 3; index++ {
		insertTerminalLifecycleLog(
			t, fixture, userID, decisionNow-requestLogRetentionSeconds-10-index, true,
		)
	}
	deadline := time.Now().Add(5 * time.Second)
	first, err := fixture.repo.RetainLifecycleRequestLogs(context.Background(), decisionNow, 2, deadline)
	if err != nil || first != (LifecycleRetentionResult{
		RequestLogsDeleted: 2, Processed: 2, More: true,
	}) {
		t.Fatalf("first bounded request-log retention = %+v, %v", first, err)
	}
	second, err := fixture.repo.RetainLifecycleRequestLogs(context.Background(), decisionNow, 2, deadline)
	if err != nil || second != (LifecycleRetentionResult{
		RequestLogsDeleted: 1, Processed: 1,
	}) {
		t.Fatalf("second bounded request-log retention = %+v, %v", second, err)
	}
	var roots, attempts int
	if err := fixture.store.DB().QueryRow(`SELECT COUNT(*) FROM request_logs`).Scan(&roots); err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.DB().QueryRow(`SELECT COUNT(*) FROM request_attempts`).Scan(&attempts); err != nil {
		t.Fatal(err)
	}
	if roots != 0 || attempts != 0 {
		t.Fatalf("bounded request-log retention left roots=%d attempts=%d", roots, attempts)
	}
}

func TestLifecycleRequestLogRetentionDeadlineAndRollback(t *testing.T) {
	fixture := newLifecycleLogDatabase(t)
	userID := seedLifecycleLogUser(t, fixture.store.DB(), "retention-rollback", false)
	decisionNow := int64(1_820_000_000)
	firstID := insertTerminalLifecycleLog(
		t, fixture, userID, decisionNow-requestLogRetentionSeconds-2, true,
	)
	secondID := insertTerminalLifecycleLog(
		t, fixture, userID, decisionNow-requestLogRetentionSeconds-1, true,
	)

	expired, err := fixture.repo.RetainLifecycleRequestLogs(
		context.Background(), decisionNow, 2, time.Now().Add(-time.Second),
	)
	if !errors.Is(err, context.DeadlineExceeded) || expired != (LifecycleRetentionResult{}) {
		t.Fatalf("expired request-log budget = %+v, %v", expired, err)
	}
	requireRequestLogAggregateCounts(t, fixture, firstID, 1, 1)
	requireRequestLogAggregateCounts(t, fixture, secondID, 1, 1)

	trigger := fmt.Sprintf(`CREATE TRIGGER test_request_log_retention_rollback
BEFORE DELETE ON request_logs WHEN OLD.id=%d
BEGIN SELECT RAISE(ABORT,'request retention rollback'); END`, secondID)
	if _, err := fixture.store.DB().Exec(trigger); err != nil {
		t.Fatal(err)
	}
	rolledBack, err := fixture.repo.RetainLifecycleRequestLogs(
		context.Background(), decisionNow, 2, time.Now().Add(5*time.Second),
	)
	if err == nil || rolledBack != (LifecycleRetentionResult{}) {
		t.Fatalf("failed request-log retention = %+v, %v", rolledBack, err)
	}
	requireRequestLogAggregateCounts(t, fixture, firstID, 1, 1)
	requireRequestLogAggregateCounts(t, fixture, secondID, 1, 1)
}
