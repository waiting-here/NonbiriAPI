package issues

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestIssueLifecycleRetentionUsesFrozenTimeLimitAndDeadline(t *testing.T) {
	environment := newIssueTestEnvironment(t)
	userID := environment.seedUser(t, "lifecycle-issues")
	decisionNow := issueTestNow
	insertClosed := func(sequence uint64, resourceRef string) string {
		t.Helper()
		id := issueOpaqueID("iss_", sequence)
		closedAt := decisionNow - closedRetention
		if _, err := environment.store.DB().Exec(`
INSERT INTO user_issues(
 id,user_id,source,resource_kind,resource_ref,root_cause,generation,state,summary_code,safe_detail,
 first_seen_at,last_seen_at,count,closed_at,retain_until
) VALUES(?,?,'resource_validator','endpoint',?,'credential_invalid',1,'closed','credential_invalid','',
 0,0,1,?,?)`, id, userID, resourceRef, closedAt, decisionNow); err != nil {
			t.Fatalf("insert closed lifecycle issue: %v", err)
		}
		return id
	}
	firstID := insertClosed(9101, "9101")
	secondID := insertClosed(9102, "9102")

	environment.clock.Store(decisionNow + closedRetention)
	before, err := environment.service.RetainLifecycleClosed(
		context.Background(), decisionNow-1, 1, time.Now().Add(time.Second),
	)
	if err != nil || before.ClosedDeleted != 0 {
		t.Fatalf("retention before frozen boundary: result=%+v err=%v", before, err)
	}

	first, err := environment.service.RetainLifecycleClosed(
		context.Background(), decisionNow, 1, time.Now().Add(time.Second),
	)
	if err != nil || first.ClosedDeleted != 1 {
		t.Fatalf("first bounded issue retention: result=%+v err=%v", first, err)
	}
	var remaining int
	if err := environment.store.DB().QueryRow(`SELECT count(*) FROM user_issues WHERE id IN (?,?)`, firstID, secondID).Scan(&remaining); err != nil {
		t.Fatalf("count issue retention remainder: %v", err)
	}
	if remaining != 1 {
		t.Fatalf("issue retention ignored total limit: remaining=%d", remaining)
	}

	second, err := environment.service.RetainLifecycleClosed(
		context.Background(), decisionNow, 1, time.Now().Add(time.Second),
	)
	if err != nil || second.ClosedDeleted != 1 {
		t.Fatalf("second bounded issue retention: result=%+v err=%v", second, err)
	}

	thirdID := insertClosed(9103, "9103")
	deadlineResult, err := environment.service.RetainLifecycleClosed(
		context.Background(), decisionNow, 1, time.Now().Add(-time.Second),
	)
	if !errors.Is(err, context.DeadlineExceeded) || deadlineResult != (RetentionResult{}) {
		t.Fatalf("expired issue retention deadline: result=%+v err=%v", deadlineResult, err)
	}
	if err := environment.store.DB().QueryRow(`SELECT count(*) FROM user_issues WHERE id=?`, thirdID).Scan(&remaining); err != nil || remaining != 1 {
		t.Fatalf("deadline-mutated issue count=%d err=%v", remaining, err)
	}
}
