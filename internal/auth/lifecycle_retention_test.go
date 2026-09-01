package auth

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestRetainSessionsAtUsesFrozenDecisionAndExactMore(t *testing.T) {
	fixture := newRuntimeFixture(t, nil)
	_ = loginUser(t, fixture, "retention-user", "")
	if _, _, err := fixture.runtime.ensureAdminAndSession(context.Background()); err != nil {
		t.Fatal(err)
	}
	var userID, adminID int64
	if err := fixture.store.DB().QueryRow(`SELECT id FROM users WHERE is_admin=0`).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.DB().QueryRow(`SELECT id FROM users WHERE is_admin=1`).Scan(&adminID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.DB().Exec(`DELETE FROM sessions`); err != nil {
		t.Fatal(err)
	}

	decisionNow := authTestNow + 100
	insertRetentionSession(t, fixture, "due-user", userID, decisionNow-1)
	insertRetentionSession(t, fixture, "due-admin", adminID, decisionNow)
	insertRetentionSession(t, fixture, "future-user", userID, decisionNow+1)
	fixture.clock.Add(24 * time.Hour)

	first, err := fixture.runtime.RetainSessionsAt(
		context.Background(), decisionNow, 1, time.Now().Add(time.Minute),
	)
	if err != nil {
		t.Fatal(err)
	}
	if first != (LifecycleRetentionResult{Processed: 1, More: true}) {
		t.Fatalf("first result=%+v", first)
	}
	second, err := fixture.runtime.RetainSessionsAt(
		context.Background(), decisionNow, 100, time.Now().Add(time.Minute),
	)
	if err != nil {
		t.Fatal(err)
	}
	if second != (LifecycleRetentionResult{Processed: 1, More: false}) {
		t.Fatalf("second result=%+v", second)
	}
	assertRetentionSessionCount(t, fixture, 1)

	boundary, err := fixture.runtime.RetainSessionsAt(
		context.Background(), decisionNow+1, 100, time.Now().Add(time.Minute),
	)
	if err != nil {
		t.Fatal(err)
	}
	if boundary != (LifecycleRetentionResult{Processed: 1, More: false}) {
		t.Fatalf("boundary result=%+v", boundary)
	}
	assertRetentionSessionCount(t, fixture, 0)
}

func TestRetainSessionsAtValidatesBoundsAndHonorsDeadline(t *testing.T) {
	fixture := newRuntimeFixture(t, nil)
	_ = loginUser(t, fixture, "retention-deadline", "")
	decisionNow := authTestNow + int64(DefaultSessionIdleTTL/time.Second)

	for _, limit := range []int{0, lifecycleRetentionBatchLimit + 1} {
		if _, err := fixture.runtime.RetainSessionsAt(
			context.Background(), decisionNow, limit, time.Now().Add(time.Minute),
		); !errors.Is(err, ErrLifecycleInvalid) {
			t.Fatalf("limit %d error=%v", limit, err)
		}
	}
	if _, err := fixture.runtime.RetainSessionsAt(
		context.Background(), decisionNow, 1, time.Time{},
	); !errors.Is(err, ErrLifecycleInvalid) {
		t.Fatalf("zero deadline error=%v", err)
	}

	_, err := fixture.runtime.RetainSessionsAt(
		context.Background(), decisionNow, 1, time.Now().Add(-time.Second),
	)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expired deadline error=%v", err)
	}
	assertRetentionSessionCount(t, fixture, 1)
}

func insertRetentionSession(t *testing.T, fixture *runtimeFixture, tokenHash string, userID, expiresAt int64) {
	t.Helper()
	lastSeen := authTestNow
	if expiresAt < lastSeen {
		lastSeen = expiresAt
	}
	if _, err := fixture.store.DB().Exec(`
INSERT INTO sessions(
 token_hash,user_id,oauth_state,last_seen_at,expires_at,absolute_expires_at,created_at,cred_gen
) VALUES(?,?,'',?,?,?,?,'retention-generation')`,
		tokenHash, userID, lastSeen, expiresAt, expiresAt+100, lastSeen,
	); err != nil {
		t.Fatal(err)
	}
}

func assertRetentionSessionCount(t *testing.T, fixture *runtimeFixture, want int) {
	t.Helper()
	var got int
	if err := fixture.store.DB().QueryRow(`SELECT COUNT(*) FROM sessions`).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("session count=%d want=%d", got, want)
	}
}
