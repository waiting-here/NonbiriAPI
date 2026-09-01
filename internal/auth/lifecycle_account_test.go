package auth

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

type lifecycleInvalidationObserver struct{ userIDs []int64 }

func (observer *lifecycleInvalidationObserver) UserSessionInvalidated(userID int64) {
	observer.userIDs = append(observer.userIDs, userID)
}

func TestLifecycleIdentityUsesFrozenDecisionAndClosedFields(t *testing.T) {
	fixture := newRuntimeFixture(t, nil)
	_ = loginUser(t, fixture, "lifecycle-export", "")
	var userID int64
	if err := fixture.store.DB().QueryRow(`SELECT id FROM users WHERE discord_id='discord-1'`).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	decisionNow := authTestNow + 50
	if _, err := fixture.store.DB().Exec(`
UPDATE users SET is_banned=1,banned_reason='HOSTILE-BAN-REASON',banned_until=? WHERE id=?`, decisionNow+10, userID); err != nil {
		t.Fatal(err)
	}
	fixture.clock.Add(24 * 60 * 60 * 1e9)
	tx, err := fixture.store.DB().BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	identity, usage, err := fixture.runtime.ExportLifecycleIdentity(context.Background(), tx, userID, decisionNow, 1)
	if err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if !identity.IsBanned || identity.BannedUntil == nil || *identity.BannedUntil != decisionNow+10 {
		t.Fatalf("identity did not use frozen decision time: %+v", identity)
	}
	if identity.ID == "" || identity.Username == "" || usage.TotalRequests == "" || usage.TotalPromptTokens == "" {
		t.Fatalf("incomplete lifecycle projection: identity=%+v usage=%+v", identity, usage)
	}
	encoded, err := json.Marshal(struct {
		Identity LifecycleIdentity
		Usage    LifecycleUsage
	}{identity, usage})
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"HOSTILE-BAN-REASON", "revision", "session", "token_hash"} {
		if strings.Contains(strings.ToLower(string(encoded)), strings.ToLower(forbidden)) {
			t.Fatalf("forbidden lifecycle identity material %q in %s", forbidden, encoded)
		}
	}
	if _, _, err := fixture.runtime.ExportLifecycleIdentity(context.Background(), nil, userID, decisionNow, 1); !errors.Is(err, ErrLifecycleInvalid) {
		t.Fatalf("nil transaction error=%v", err)
	}
	if _, _, err := fixture.runtime.ExportLifecycleIdentity(context.Background(), tx, userID, decisionNow, 0); !errors.Is(err, ErrLifecycleInvalid) {
		t.Fatalf("zero limit error=%v", err)
	}
}

func TestLifecycleSessionDeletionRollsBackAndNotifiesOnlyAfterCommit(t *testing.T) {
	observer := &lifecycleInvalidationObserver{}
	fixture := newRuntimeFixture(t, func(config *RuntimeConfig) {})
	if err := fixture.runtime.AttachUserSessionInvalidationObserver(observer); err != nil {
		t.Fatal(err)
	}
	_ = loginUser(t, fixture, "lifecycle-delete", "")
	var userID int64
	if err := fixture.store.DB().QueryRow(`SELECT id FROM users WHERE discord_id='discord-1'`).Scan(&userID); err != nil {
		t.Fatal(err)
	}

	tx, err := fixture.store.DB().BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	rolledBack, err := fixture.runtime.PrepareLifecycleAccountDeletion(context.Background(), tx, userID, authTestNow)
	if err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	var sessions int
	if err := tx.QueryRow(`SELECT count(*) FROM sessions WHERE user_id=?`, userID).Scan(&sessions); err != nil || sessions != 0 {
		t.Fatalf("prepared sessions=%d err=%v", sessions, err)
	}
	if len(observer.userIDs) != 0 {
		t.Fatalf("observer called before commit: %v", observer.userIDs)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if !rolledBack.Abort() || rolledBack.Commit() {
		t.Fatal("rollback finalizer was not one-shot")
	}
	if err := fixture.store.DB().QueryRow(`SELECT count(*) FROM sessions WHERE user_id=?`, userID).Scan(&sessions); err != nil || sessions != 1 {
		t.Fatalf("rolled-back sessions=%d err=%v", sessions, err)
	}
	if len(observer.userIDs) != 0 {
		t.Fatalf("observer called after rollback: %v", observer.userIDs)
	}

	tx, err = fixture.store.DB().BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	committed, err := fixture.runtime.PrepareLifecycleAccountDeletion(context.Background(), tx, userID, authTestNow)
	if err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if len(observer.userIDs) != 0 {
		t.Fatalf("observer called before finalizer commit: %v", observer.userIDs)
	}
	if !committed.Commit() || committed.Commit() || committed.Abort() {
		t.Fatal("commit finalizer was not one-shot")
	}
	if len(observer.userIDs) != 1 || observer.userIDs[0] != userID {
		t.Fatalf("observer user ids=%v", observer.userIDs)
	}
	if err := fixture.store.DB().QueryRow(`SELECT count(*) FROM sessions WHERE user_id=?`, userID).Scan(&sessions); err != nil || sessions != 0 {
		t.Fatalf("committed sessions=%d err=%v", sessions, err)
	}
}
