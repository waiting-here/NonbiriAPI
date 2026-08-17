package db

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func newAccountTestStore(t *testing.T) *Store {
	t.Helper()
	st := openTestStore(t, filepath.Join(t.TempDir(), "account.db"))
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func seedFullyPopulatedUser(t *testing.T, st *Store, discordID string) int64 {
	t.Helper()
	ctx := context.Background()
	uid := seedUserRaw(t, st, discordID)
	// Caller key + session.
	if _, err := st.RegenerateCallerKey(uid); err != nil {
		t.Fatalf("regen caller key: %v", err)
	}
	if _, _, err := st.CreateUserSession(uid); err != nil {
		t.Fatalf("create session: %v", err)
	}
	// Endpoint + key + fetched model + binding + model.
	eid := seedEndpointRaw(t, st, uid, true)
	kid := seedEndpointKeyRaw(t, st, eid, true)
	seedFetchedModelRaw(t, st, kid, "upstream-1")
	mid := seedModelRaw(t, st, uid, "prov", "m")
	seedForwardBinding(t, st, mid, kid, "upstream-1", 1)
	// Request log + usage accumulator.
	if err := st.RecordRequest(ctx, RequestLogInput{
		AttemptID: "att-" + discordID, UserID: uid, Model: "prov/m",
		EndpointKeyID: kid, UpstreamModelID: "upstream-1",
		StatusCode: 200, DurationMs: 5, StartedAt: time.Unix(testNow, 0), CompletedAt: time.Unix(testNow+1, 0),
		PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15,
	}); err != nil {
		t.Fatalf("record request: %v", err)
	}
	// User issue + admin alert about the user.
	if _, err := st.RecordUserIssue(ctx, UserIssueInput{
		UserID: uid, Kind: "manual_issue", Message: " seeded ", Ref: "r",
		CreatedAt: time.Unix(testNow, 0),
	}); err != nil {
		t.Fatalf("record user issue: %v", err)
	}
	if _, err := st.RecordAdminAlert(ctx, AdminAlertInput{
		Kind: "user_alert", Message: "about user", Ref: "u",
		SubjectUserID: uid, CreatedAt: time.Unix(testNow, 0),
	}); err != nil {
		t.Fatalf("record admin alert: %v", err)
	}
	// A site-wide (NULL-subject) alert must survive user deletion.
	if _, err := st.RecordAdminAlert(ctx, AdminAlertInput{
		Kind: "site_wide", Message: "global", Ref: "",
		SubjectUserID: 0, CreatedAt: time.Unix(testNow, 0),
	}); err != nil {
		t.Fatalf("record site-wide alert: %v", err)
	}
	return uid
}

func countRows(t *testing.T, st *Store, query string, args ...any) int {
	t.Helper()
	var n int
	if err := st.DB().QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatalf("count %q: %v", query, err)
	}
	return n
}

func assertUserGone(t *testing.T, st *Store, uid int64) {
	t.Helper()
	if got := countRows(t, st, `SELECT COUNT(*) FROM users WHERE id=?`, uid); got != 0 {
		t.Fatalf("users rows=%d, want 0 (resurrection)", got)
	}
	for _, table := range []string{"sessions", "caller_keys", "endpoints", "endpoint_keys",
		"fetched_models", "model_bindings", "models", "request_logs", "user_issues"} {
		// user_issues/request_logs/sessions/caller_keys/endpoints/models key on user_id;
		// endpoint_keys/fetched_models/model_bindings are reached via cascade and have no
		// user_id column, so count them globally after a user delete (they must be empty
		// because the seeded user owned all of them).
		var n int
		var err error
		switch table {
		case "endpoint_keys", "fetched_models", "model_bindings":
			n, err = countRowsErr(st, `SELECT COUNT(*) FROM `+table)
		default:
			n, err = countRowsErr(st, fmt.Sprintf(`SELECT COUNT(*) FROM %s WHERE user_id=?`, table), uid)
		}
		if err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		if n != 0 {
			t.Fatalf("%s rows=%d after delete, want 0 (orphan)", table, n)
		}
	}
	if got := countRows(t, st, `SELECT COUNT(*) FROM admin_alerts WHERE subject_user_id=?`, uid); got != 0 {
		t.Fatalf("admin_alerts orphan rows=%d, want 0", got)
	}
}

func countRowsErr(st *Store, query string, args ...any) (int, error) {
	var n int
	if err := st.DB().QueryRow(query, args...).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

func TestDeleteUserAccountCascadesAllUserTables(t *testing.T) {
	st := newAccountTestStore(t)
	ctx := context.Background()
	uid := seedFullyPopulatedUser(t, st, "cascade-user")

	if err := st.DeleteUserAccount(ctx, uid); err != nil {
		t.Fatalf("DeleteUserAccount: %v", err)
	}
	assertUserGone(t, st, uid)
	// The site-wide (NULL-subject) alert survives: it is not about the user.
	if got := countRows(t, st, `SELECT COUNT(*) FROM admin_alerts WHERE subject_user_id IS NULL`); got != 1 {
		t.Fatalf("site-wide alerts=%d, want 1 (NULL-subject alerts must survive)", got)
	}
}

func TestDeleteUserAccountProtectsAdminRow(t *testing.T) {
	st := newAccountTestStore(t)
	ctx := context.Background()
	admin, err := st.EnsureAdminUser("root")
	if err != nil {
		t.Fatalf("ensure admin: %v", err)
	}
	// An alert about the admin must survive the refused delete (the orphan
	// cleanup runs inside the same transaction and is rolled back).
	if _, err := st.RecordAdminAlert(ctx, AdminAlertInput{
		Kind: "admin_alert", Message: "about admin", SubjectUserID: admin.ID,
		CreatedAt: time.Unix(testNow, 0),
	}); err != nil {
		t.Fatalf("record admin alert: %v", err)
	}

	if err := st.DeleteUserAccount(ctx, admin.ID); !errors.Is(err, ErrAdminProtected) {
		t.Fatalf("delete admin err=%v, want ErrAdminProtected", err)
	}
	// Admin row still present.
	if got, err := st.GetUserByID(admin.ID); err != nil || got == nil || !got.IsAdmin {
		t.Fatalf("admin row missing after refused delete: %v %v", got, err)
	}
	// The orphan alert cleanup was rolled back with the refused delete.
	if got := countRows(t, st, `SELECT COUNT(*) FROM admin_alerts WHERE subject_user_id=?`, admin.ID); got != 1 {
		t.Fatalf("admin alert rows=%d, want 1 (rollback must restore it)", got)
	}
}

func TestDeleteUserAccountIdempotentNotFound(t *testing.T) {
	st := newAccountTestStore(t)
	ctx := context.Background()
	uid := seedFullyPopulatedUser(t, st, "idem-user")

	if err := st.DeleteUserAccount(ctx, uid); err != nil {
		t.Fatalf("first delete: %v", err)
	}
	// A second delete is a stable not-found: no resurrection, no partial work.
	if err := st.DeleteUserAccount(ctx, uid); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second delete err=%v, want ErrNotFound", err)
	}
	// Invalid id is also not-found.
	if err := st.DeleteUserAccount(ctx, 0); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete id=0 err=%v, want ErrNotFound", err)
	}
}

func TestRecordAdminAlertSuppressesOnDeletedUser(t *testing.T) {
	st := newAccountTestStore(t)
	ctx := context.Background()
	uid := seedFullyPopulatedUser(t, st, "suppress-user")

	if err := st.DeleteUserAccount(ctx, uid); err != nil {
		t.Fatalf("delete: %v", err)
	}
	inserted, err := st.RecordAdminAlert(ctx, AdminAlertInput{
		Kind: "late_alert", Message: "too late", Ref: "r",
		SubjectUserID: uid, CreatedAt: time.Unix(testNow, 0),
	})
	if err != nil {
		t.Fatalf("late RecordAdminAlert: %v", err)
	}
	if inserted {
		t.Fatalf("late alert was inserted, want suppressed no-op")
	}
	if got := countRows(t, st, `SELECT COUNT(*) FROM admin_alerts WHERE subject_user_id=?`, uid); got != 0 {
		t.Fatalf("orphan alert rows=%d, want 0", got)
	}
}

func TestRecordUserIssueSuppressesOnDeletedUser(t *testing.T) {
	st := newAccountTestStore(t)
	ctx := context.Background()
	uid := seedFullyPopinatedIssueUser(t, st)

	if err := st.DeleteUserAccount(ctx, uid); err != nil {
		t.Fatalf("delete: %v", err)
	}
	inserted, err := st.RecordUserIssue(ctx, UserIssueInput{
		UserID: uid, Kind: "late_issue", Message: "too late", Ref: "r",
		CreatedAt: time.Unix(testNow, 0),
	})
	if err != nil {
		t.Fatalf("late RecordUserIssue: %v", err)
	}
	if inserted {
		t.Fatalf("late issue was inserted, want suppressed no-op")
	}
	if got := countRows(t, st, `SELECT COUNT(*) FROM user_issues WHERE user_id=?`, uid); got != 0 {
		t.Fatalf("orphan issue rows=%d, want 0", got)
	}
}

func seedFullyPopinatedIssueUser(t *testing.T, st *Store) int64 {
	t.Helper()
	uid := seedUserRaw(t, st, "issue-user")
	if _, err := st.RecordUserIssue(context.Background(), UserIssueInput{
		UserID: uid, Kind: "seed_issue", Message: "seed", Ref: "r",
		CreatedAt: time.Unix(testNow, 0),
	}); err != nil {
		t.Fatalf("seed issue: %v", err)
	}
	return uid
}

func TestRecordAdminAlertBoundsAndSanitizes(t *testing.T) {
	st := newAccountTestStore(t)
	ctx := context.Background()
	uid := seedUserRaw(t, st, "bound-user")
	long := make([]byte, 0, maxAlertMessageRunes*4)
	for i := 0; i < cap(long); i++ {
		long = append(long, 'x')
	}
	inserted, err := st.RecordAdminAlert(ctx, AdminAlertInput{
		Kind: "kind", Message: "a\nb\tc\x00d", Ref: string(long),
		SubjectUserID: uid, CreatedAt: time.Unix(testNow, 0),
	})
	if err != nil || !inserted {
		t.Fatalf("insert bounded alert: inserted=%v err=%v", inserted, err)
	}
	var msg, ref string
	if err := st.DB().QueryRow(`SELECT message, ref FROM admin_alerts WHERE subject_user_id=?`, uid).Scan(&msg, &ref); err != nil {
		t.Fatalf("read alert: %v", err)
	}
	// \n and \t become spaces; the NUL byte (C0 control) is stripped, so c and
	// d are adjacent.
	if msg != "a b cd" {
		t.Fatalf("message not sanitized: %q", msg)
	}
	if len(ref) > maxAlertRefRunes {
		t.Fatalf("ref not bounded: %d", len(ref))
	}
}

func TestRecordAdminAlertRejectsMissingKind(t *testing.T) {
	st := newAccountTestStore(t)
	ctx := context.Background()
	if _, err := st.RecordAdminAlert(ctx, AdminAlertInput{Kind: "", SubjectUserID: 0, CreatedAt: time.Unix(testNow, 0)}); err == nil {
		t.Fatalf("empty kind accepted")
	}
}

// TestDeleteVsLateCallbacksLinearized is the race/stress proof: many goroutines
// race to either delete the same user or write a late callback (admin alert,
// user issue, request log) against it. After all finish, the user is gone and
// ZERO rows reference it in any user-associated table — no orphan, no
// resurrection, no cross-user write, regardless of which goroutine won.
//
// Run with `go test -race -count=20` (race-check.sh) to exercise the
// single-writer serialization repeatedly.
func TestDeleteVsLateCallbacksLinearized(t *testing.T) {
	st := newAccountTestStore(t)
	ctx := context.Background()
	uid := seedFullyPopulatedUser(t, st, "race-user")

	var deletes atomic.Int64
	var deleteOK atomic.Int64
	var deleteNotFound atomic.Int64
	var callbacks atomic.Int64
	const workers = 64
	const callbacksPerWorker = 40

	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(seed int) {
			defer wg.Done()
			<-start
			// One designated deleter per worker group; the rest are callers.
			if seed == 0 {
				deletes.Add(1)
				err := st.DeleteUserAccount(ctx, uid)
				switch {
				case err == nil:
					deleteOK.Add(1)
				case errors.Is(err, ErrNotFound):
					deleteNotFound.Add(1)
				default:
					t.Errorf("unexpected delete error: %v", err)
				}
				return
			}
			for j := 0; j < callbacksPerWorker; j++ {
				callbacks.Add(1)
				switch (seed + j) % 3 {
				case 0:
					_, err := st.RecordAdminAlert(ctx, AdminAlertInput{
						Kind: "race_alert", Message: "m", Ref: "r",
						SubjectUserID: uid, CreatedAt: time.Unix(testNow, 0),
					})
					if err != nil {
						t.Errorf("late alert: %v", err)
						return
					}
				case 1:
					_, err := st.RecordUserIssue(ctx, UserIssueInput{
						UserID: uid, Kind: "race_issue", Message: "m", Ref: "r",
						CreatedAt: time.Unix(testNow, 0),
					})
					if err != nil {
						t.Errorf("late issue: %v", err)
						return
					}
				case 2:
					// Distinct attempt ids so the at-most-once index does not
					// itself no-op duplicates; the linearization under test is
					// the user-existence gate, not the attempt index.
					err := st.RecordRequest(ctx, RequestLogInput{
						AttemptID: fmt.Sprintf("race-att-%d-%d", seed, j), UserID: uid,
						Model: "prov/m", StatusCode: 200, DurationMs: 1,
						StartedAt: time.Unix(testNow, 0), CompletedAt: time.Unix(testNow+1, 0),
						PromptTokens: 1, CompletionTokens: 1, TotalTokens: 2,
					})
					if err != nil {
						t.Errorf("late request: %v", err)
						return
					}
				}
			}
		}(i)
	}
	close(start)
	wg.Wait()

	// Exactly one delete succeeded; the rest saw not-found.
	if deleteOK.Load() != 1 || deletes.Load() != 1 {
		t.Fatalf("deleteOK=%d deletes=%d, want exactly 1 successful delete", deleteOK.Load(), deletes.Load())
	}
	// The user is gone and no table references it: the linearization invariant.
	assertUserGone(t, st, uid)
	// The site-wide alert seeded in seedFullyPopulatedUser still survives.
	if got := countRows(t, st, `SELECT COUNT(*) FROM admin_alerts WHERE subject_user_id IS NULL`); got != 1 {
		t.Fatalf("site-wide alerts=%d, want 1", got)
	}
}

// TestRepeatedDeleteAndCallbackShuffle runs the linearization scenario many
// times with fresh stores to surface any ordering-dependent orphan. This is the
// shuffle gate complementing the -race run.
func TestRepeatedDeleteAndCallbackShuffle(t *testing.T) {
	for iter := 0; iter < 40; iter++ {
		st := newAccountTestStore(t)
		ctx := context.Background()
		uid := seedFullyPopulatedUser(t, st, "shuffle-user")

		var wg sync.WaitGroup
		const racers = 12
		start := make(chan struct{})
		for i := 0; i < racers; i++ {
			wg.Add(1)
			go func(role int) {
				defer wg.Done()
				<-start
				if role == 0 {
					_ = st.DeleteUserAccount(ctx, uid)
					return
				}
				_, _ = st.RecordAdminAlert(ctx, AdminAlertInput{
					Kind: "s_alert", Message: "m", Ref: "r",
					SubjectUserID: uid, CreatedAt: time.Unix(testNow, 0),
				})
				_, _ = st.RecordUserIssue(ctx, UserIssueInput{
					UserID: uid, Kind: "s_issue", Message: "m", Ref: "r",
					CreatedAt: time.Unix(testNow, 0),
				})
				_ = st.RecordRequest(ctx, RequestLogInput{
					AttemptID: fmt.Sprintf("s-att-%d", role), UserID: uid,
					Model: "prov/m", StatusCode: 200, DurationMs: 1,
					StartedAt: time.Unix(testNow, 0), CompletedAt: time.Unix(testNow+1, 0),
					PromptTokens: 1, CompletionTokens: 1, TotalTokens: 2,
				})
			}(i)
		}
		close(start)
		wg.Wait()
		assertUserGone(t, st, uid)
		_ = st.Close()
	}
}
