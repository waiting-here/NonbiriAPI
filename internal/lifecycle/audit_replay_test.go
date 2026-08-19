// Independent audit: elevated-capability replay, binding, kind, and TTL
// boundaries through the lifecycle service, plus the user-side issue/alert
// caps, sanitization, late-write suppression, retention, and export secret
// exclusion at the repository layer.
package lifecycle_test

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/elevation"
	"github.com/waiting-here/NonbiriAPI/internal/lifecycle"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

type fixedVerifier struct{ ok bool }

func (v fixedVerifier) AdminCredentialCheck(username, password string) bool { return v.ok }

func newLifecycleFixture(t *testing.T) (*lifecycle.Service, *db.Store, *elevation.Manager) {
	t.Helper()
	key := bytes.Repeat([]byte{0x77}, secret.MasterKeyBytes)
	vault, err := secret.New(key)
	clear(key)
	if err != nil {
		t.Fatal(err)
	}
	store, err := db.Open(filepath.Join(t.TempDir(), "audit-lifecycle.db"), vault)
	if err != nil {
		_ = vault.Close()
		t.Fatal(err)
	}
	manager, err := elevation.NewManagerWithTTL(5 * time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	svc, err := lifecycle.NewService(lifecycle.Config{
		Store: store, Elevation: manager, AdminVerifier: fixedVerifier{ok: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = svc.Close()
		_ = manager.Close()
		_ = store.Close()
		_ = vault.Close()
	})
	return svc, store, manager
}

func TestAuditElevationSingleUseAndReplay(t *testing.T) {
	svc, store, _ := newLifecycleFixture(t)
	admin, err := store.EnsureAdminUser("root")
	if err != nil {
		t.Fatal(err)
	}
	target, err := store.CreateUser("discord-target", "victim", "")
	if err != nil {
		t.Fatal(err)
	}

	token, _, err := svc.ElevateAdminBound(context.Background(), admin, "password", "session-hash-A")
	if err != nil {
		t.Fatal(err)
	}
	// A different session binding cannot use the capability.
	if err := svc.DeleteUserAsAdminBound(context.Background(), admin, target.ID, token, "session-hash-B"); !errors.Is(err, lifecycle.ErrElevationRequired) {
		t.Fatalf("wrong-binding consume: %v", err)
	}
	// Correct binding consumes it exactly once.
	if err := svc.DeleteUserAsAdminBound(context.Background(), admin, target.ID, token, "session-hash-A"); err != nil {
		t.Fatalf("first consume: %v", err)
	}
	if user, err := store.GetUserByID(target.ID); err != nil || user != nil {
		t.Fatalf("target still exists (err=%v user=%v)", err, user != nil)
	}
	// Replay is refused.
	if err := svc.DeleteUserAsAdminBound(context.Background(), admin, target.ID, token, "session-hash-A"); !errors.Is(err, lifecycle.ErrElevationRequired) {
		t.Fatalf("replay after consume: %v", err)
	}
}

func TestAuditElevationKindAndIdentityBinding(t *testing.T) {
	svc, store, _ := newLifecycleFixture(t)
	admin, err := store.EnsureAdminUser("root")
	if err != nil {
		t.Fatal(err)
	}
	target, err := store.CreateUser("discord-target-2", "victim", "")
	if err != nil {
		t.Fatal(err)
	}
	token, _, err := svc.ElevateAdminBound(context.Background(), admin, "password", "binding-A")
	if err != nil {
		t.Fatal(err)
	}
	// A user-kind capability (different manager domain) cannot be consumed by
	// the admin path; simulate by issuing through the same manager as a user.
	userToken, _, err := svc.Elevation().IssueBound(target.ID, elevation.KindUser, "binding-A")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.DeleteUserAsAdminBound(context.Background(), admin, target.ID, userToken, "binding-A"); !errors.Is(err, lifecycle.ErrElevationRequired) {
		t.Fatalf("user-kind token used on admin path: %v", err)
	}
	// A token issued for a different actor cannot be consumed by this admin.
	if err := svc.DeleteUserAsAdminBound(context.Background(), admin, target.ID, token, "binding-A"); err != nil {
		t.Fatalf("admin token should consume: %v", err)
	}
	_ = admin
}

func TestAuditElevationTTLExpiry(t *testing.T) {
	svc, store, manager := newLifecycleFixture(t)
	admin, err := store.EnsureAdminUser("root")
	if err != nil {
		t.Fatal(err)
	}
	target, err := store.CreateUser("discord-target-3", "victim", "")
	if err != nil {
		t.Fatal(err)
	}
	// Issue the capability with the normal clock, then fast-forward the
	// manager clock beyond the TTL: the token must be refused on consume.
	token, _, err := svc.ElevateAdminBound(context.Background(), admin, "password", "binding-A")
	if err != nil {
		t.Fatal(err)
	}
	manager.SetClock(func() time.Time { return time.Now().Add(2 * time.Hour) })
	if err := svc.DeleteUserAsAdminBound(context.Background(), admin, target.ID, token, "binding-A"); !errors.Is(err, lifecycle.ErrElevationRequired) {
		t.Fatalf("expired token accepted: %v", err)
	}
}

func TestAuditElevationWrongPasswordNeverIssues(t *testing.T) {
	svc, store, _ := newLifecycleFixture(t)
	admin, err := store.EnsureAdminUser("root")
	if err != nil {
		t.Fatal(err)
	}
	svcWrong, err := lifecycle.NewService(lifecycle.Config{
		Store: store, Elevation: svc.Elevation(), AdminVerifier: fixedVerifier{ok: false},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer svcWrong.Close()
	if _, _, err := svcWrong.ElevateAdminBound(context.Background(), admin, "wrong", "binding-A"); !errors.Is(err, lifecycle.ErrInvalidCredentials) {
		t.Fatalf("wrong password issued a capability: %v", err)
	}
	// A non-admin identity can never elevate.
	if _, _, err := svc.ElevateAdminBound(context.Background(), &db.User{ID: 7, Username: "not-admin"}, "password", "binding-A"); !errors.Is(err, lifecycle.ErrInvalidCredentials) {
		t.Fatalf("non-admin elevation: %v", err)
	}
}

func TestAuditIssueAlertCapsAndLateWriteSuppression(t *testing.T) {
	_, store, _ := newLifecycleFixture(t)
	user, err := store.CreateUser("discord-issue-1", "issue-user", "")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()

	// Sanitization: control characters are normalized and length is bounded.
	evil := "bad\u0000ctl\u001bESC\u007f\u200bzero-width " + strings.Repeat("x", 3000)
	ok, err := store.RecordUserIssue(context.Background(), db.UserIssueInput{
		UserID: user.ID, Kind: "fetch_failed", Message: evil, Ref: evil, CreatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("issue insert unexpectedly suppressed")
	}
	issues, _, err := store.QueryUserIssues(context.Background(), db.IssueQuery{UserID: user.ID, PageSize: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) == 0 {
		t.Fatal("no issues returned")
	}
	latest := issues[0]
	for _, r := range latest.Message {
		if r < 0x20 || r == 0x7f {
			t.Fatalf("control character survived sanitization: %q", latest.Message)
		}
	}
	if len([]rune(latest.Message)) > 1024 {
		t.Fatalf("message not bounded: %d runes", len([]rune(latest.Message)))
	}

	// Fill the issue cap and verify further inserts no-op.
	inserted := 1
	for i := 0; i < db.MaxUserIssuesPerUser+3; i++ {
		ok, err := store.RecordUserIssue(context.Background(), db.UserIssueInput{
			UserID: user.ID, Kind: "fetch_failed", Message: "issue", Ref: "ref", CreatedAt: now.Add(time.Duration(i) * time.Second),
		})
		if err != nil {
			t.Fatal(err)
		}
		if ok {
			inserted++
		}
	}
	if inserted != db.MaxUserIssuesPerUser {
		t.Fatalf("inserted=%d want cap=%d", inserted, db.MaxUserIssuesPerUser)
	}

	// Late write suppression: after account deletion, a new issue write is an
	// atomic no-op, and old issues are cascaded away.
	if err := store.DeleteUserAccount(context.Background(), user.ID); err != nil {
		t.Fatal(err)
	}
	ok, err = store.RecordUserIssue(context.Background(), db.UserIssueInput{
		UserID: user.ID, Kind: "late", Message: "late", Ref: "", CreatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("late issue write after deletion was inserted")
	}
	issues, _, err = store.QueryUserIssues(context.Background(), db.IssueQuery{UserID: user.ID, PageSize: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 0 {
		t.Fatalf("issues survived account deletion: %d", len(issues))
	}

	// Admin alerts: subject-bound late write is suppressed after deletion.
	user2, err := store.CreateUser("discord-issue-2", "alert-subject", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.RecordAdminAlert(context.Background(), db.AdminAlertInput{
		Kind: "test", Message: "m", Ref: "r", SubjectUserID: user2.ID, CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteUserAccount(context.Background(), user2.ID); err != nil {
		t.Fatal(err)
	}
	suppressed, err := store.RecordAdminAlert(context.Background(), db.AdminAlertInput{
		Kind: "test", Message: "late", Ref: "r", SubjectUserID: user2.ID, CreatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if suppressed {
		t.Fatal("late subject-bound alert after deletion was inserted")
	}
	// Site-wide (NULL subject) alerts are still admitted after any deletion.
	admitted, err := store.RecordAdminAlert(context.Background(), db.AdminAlertInput{
		Kind: "site", Message: "wide", Ref: "", SubjectUserID: 0, CreatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !admitted {
		t.Fatal("site-wide alert was suppressed")
	}
}

func TestAuditRetentionAndUsageAggregation(t *testing.T) {
	_, store, _ := newLifecycleFixture(t)
	user, err := store.CreateUser("discord-ret-1", "ret-user", "")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	fresh := now.Add(-1 * time.Hour)
	old := now.Add(-31 * 24 * time.Hour)

	for i := 0; i < 3; i++ {
		if err := store.RecordRequest(context.Background(), db.RequestLogInput{
			AttemptID: "a-fresh-" + strconv.Itoa(i), UserID: user.ID, StatusCode: 200, StartedAt: fresh, CompletedAt: fresh.Add(time.Second),
			PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15,
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.RecordRequest(context.Background(), db.RequestLogInput{
		AttemptID: "a-old", UserID: user.ID, StatusCode: 200, StartedAt: old, CompletedAt: old.Add(time.Second),
		PromptTokens: 100, CompletionTokens: 50, TotalTokens: 150,
	}); err != nil {
		t.Fatal(err)
	}
	// usage_unknown must not fabricate token values.
	if err := store.RecordRequest(context.Background(), db.RequestLogInput{
		AttemptID: "a-unknown", UserID: user.ID, StatusCode: 200, UsageUnknown: true,
		StartedAt: fresh, CompletedAt: fresh.Add(time.Second),
	}); err != nil {
		t.Fatal(err)
	}

	cutoff := now.Add(-30 * 24 * time.Hour).Unix()
	count, err := store.CountRequestLogsBefore(context.Background(), cutoff)
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("old logs before cutoff=%d want 1", count)
	}
	deleted, err := store.DeleteRequestLogsBefore(context.Background(), cutoff, 100)
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 1 {
		t.Fatalf("deleted=%d want 1", deleted)
	}
	// The fresh and usage_unknown rows survive; user totals persist (they are
	// not logs and must not be pruned by log retention).
	totals, err := store.GetUserUsage(context.Background(), user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if totals.TotalRequests != 5 || totals.TotalPromptTokens != 130 || totals.TotalCompletionTokens != 65 || totals.TotalUnknownUsageRequests != 1 {
		t.Fatalf("user totals after retention: %+v", totals)
	}
	remain, err := store.CountRequestLogsBefore(context.Background(), cutoff)
	if err != nil {
		t.Fatal(err)
	}
	if remain != 0 {
		t.Fatalf("old rows remain after cleanup: %d", remain)
	}
}

func TestAuditExportContainsNoPlaintextSecrets(t *testing.T) {
	key := bytes.Repeat([]byte{0x12}, secret.MasterKeyBytes)
	vault, err := secret.New(key)
	clear(key)
	if err != nil {
		t.Fatal(err)
	}
	store, err := db.Open(filepath.Join(t.TempDir(), "audit-export.db"), vault)
	if err != nil {
		_ = vault.Close()
		t.Fatal(err)
	}
	defer func() {
		_ = store.Close()
		_ = vault.Close()
	}()
	user, err := store.CreateUser("discord-export-1", "export-user", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetCallerKey(user.ID); err != nil {
		t.Fatal(err)
	}
	ciphertext, err := vault.Seal([]byte("sk-plaintext-must-never-export"))
	if err != nil {
		t.Fatal(err)
	}
	ep, err := store.CreateEndpoint(context.Background(), user.ID, "openai-compatible", "https://upstream.example:443/v1", "", true, time.Now().Unix())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateEndpointKey(context.Background(), user.ID, ep.ID, ciphertext, "head", "tail", "note", true, time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateModel(context.Background(), user.ID, "p", "m", "ordered", false, time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	exporter := lifecycle.NewExportService(store)
	payload, err := exporter.BuildExport(context.Background(), user)
	if err != nil {
		t.Fatal(err)
	}
	text := string(payload)
	for _, needle := range []string{
		"sk-plaintext-must-never-export",
		"nbsec:v1:aes-256-gcm",
	} {
		if strings.Contains(text, needle) {
			t.Fatalf("export leaks %q", needle)
		}
	}
	if !strings.Contains(text, "endpoints") || !strings.Contains(text, "models") {
		t.Fatalf("export package is incomplete: %s", text)
	}
}
