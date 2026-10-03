package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/adminalerts"
	"github.com/waiting-here/NonbiriAPI/internal/auth"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/lifecyclegate"
)

func adminDeletionElevation(t *testing.T, f gameWireFixture) string {
	t.Helper()
	response := testApplicationRequest(t, f.app.handler, http.MethodPost, auditAdminHost, "/admin/api/auth/elevate",
		`{"password":"correct horse battery staple"}`, f.adminCookies,
		map[string]string{"Content-Type": "application/json", "Origin": "http://" + auditAdminHost})
	var body auth.ElevationResponse
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &body) != nil || body.Token == "" {
		t.Fatalf("elevation: %d %s", response.Code, response.Body.String())
	}
	return body.Token
}

func adminDeletionHeaders(key, token string) map[string]string {
	headers := map[string]string{"Content-Type": "application/json", "Origin": "http://" + auditAdminHost,
		"Idempotency-Key": key}
	if token != "" {
		headers["X-Elevated-Token"] = token
	}
	return headers
}

func TestAdminAccountDeletionHTTPAuthorityRevisionAndHistory(t *testing.T) {
	f := newGameWireFixture(t)
	const revision = "18446744073709551616"
	wide, _ := db.ParseU128Decimal(revision)
	if _, err := f.store.DB().Exec(`UPDATE users SET revision=? WHERE id=?`, db.EncodeU128(wide), f.userID); err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("/admin/api/users/%d", f.userID)
	body := `{"expected_revision":"` + revision + `","confirmation":"DELETE"}`
	key := strings.Repeat("D", 22)
	call := func(path, body string, cookies []*http.Cookie, headers map[string]string, want int) *httptest.ResponseRecorder {
		t.Helper()
		response := testApplicationRequest(t, f.app.handler, http.MethodDelete, auditAdminHost, path, body, cookies, headers)
		if response.Code != want {
			t.Fatalf("delete %s: %d %s; want %d", path, response.Code, response.Body.String(), want)
		}
		return response
	}
	call(path, body, f.adminCookies, adminDeletionHeaders(key, ""), http.StatusForbidden)
	call(path, body, f.cookies, adminDeletionHeaders(key, ""), http.StatusUnauthorized)
	token := adminDeletionElevation(t, f)
	// Rejected preflight attempts must leave the single-use token available.
	call(fmt.Sprintf("/admin/api/users/%d", f.adminID), body, f.adminCookies, adminDeletionHeaders(key, token), http.StatusForbidden)
	call(path, `{"expected_revision":"0","confirmation":"DELETE"}`, f.adminCookies, adminDeletionHeaders(key, token), http.StatusConflict)
	wrongOrigin := adminDeletionHeaders(key, token)
	wrongOrigin["Origin"] = "https://other.invalid"
	call(path, body, f.adminCookies, wrongOrigin, http.StatusForbidden)
	var sessions, histories int
	if err := f.store.DB().QueryRow(`SELECT count(*) FROM sessions WHERE user_id=?`, f.userID).Scan(&sessions); err != nil || sessions != 1 {
		t.Fatalf("rejected deletion changed sessions: %d %v", sessions, err)
	}
	if err := f.store.DB().QueryRow(`SELECT count(*) FROM admin_account_deletions WHERE former_user_id=?`, f.userID).Scan(&histories); err != nil || histories != 0 {
		t.Fatalf("rejected deletion recorded history: %d %v", histories, err)
	}
	response := call(path, body, f.adminCookies, adminDeletionHeaders(key, token), http.StatusNoContent)
	if response.Body.Len() != 0 || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("delete response: %v %s", response.Header(), response.Body.String())
	}
	var users, wallets int
	if err := f.store.DB().QueryRow(`SELECT count(*) FROM users WHERE id=?`, f.userID).Scan(&users); err != nil || users != 0 {
		t.Fatalf("account survived: %d %v", users, err)
	}
	if err := f.store.DB().QueryRow(`SELECT count(*) FROM credit_accounts WHERE user_id=?`, f.userID).Scan(&wallets); err != nil || wallets != 0 {
		t.Fatalf("wallets survived: %d %v", wallets, err)
	}
	var recordID int64
	var snapshotJSON string
	if err := f.store.DB().QueryRow(`SELECT alert_id,snapshot_json FROM admin_account_deletions WHERE former_user_id=?`, f.userID).Scan(&recordID, &snapshotJSON); err != nil {
		t.Fatal(err)
	}
	var snapshot adminalerts.AccountDeletion
	if err := json.Unmarshal([]byte(snapshotJSON), &snapshot); err != nil || snapshot.Source != "admin" ||
		snapshot.ActorUserID == nil || *snapshot.ActorUserID != fmt.Sprint(f.adminID) || snapshot.GeneralBalance != "1000000" {
		t.Fatalf("deletion history: %s %v", snapshotJSON, err)
	}
	history := testApplicationRequest(t, f.app.handler, http.MethodGet, auditAdminHost,
		fmt.Sprintf("/admin/api/users/deleted/%d", recordID), "", f.adminCookies, nil)
	if history.Code != http.StatusOK || !strings.Contains(history.Body.String(), `"source":"admin"`) {
		t.Fatalf("historical HTTP detail: %d %s", history.Code, history.Body.String())
	}
	call(path, body, f.adminCookies, adminDeletionHeaders(key, token), http.StatusForbidden)
	call(path, body, f.adminCookies, adminDeletionHeaders(key, adminDeletionElevation(t, f)), http.StatusNoContent)
	replayToken := adminDeletionElevation(t, f)
	call(path, `{"expected_revision":"0","confirmation":"DELETE"}`, f.adminCookies, adminDeletionHeaders(key, replayToken), http.StatusConflict)
	call(fmt.Sprintf("/admin/api/users/%d", f.adminID), body, f.adminCookies, adminDeletionHeaders(key, replayToken), http.StatusConflict)
	call(path, body, f.adminCookies, adminDeletionHeaders(key, replayToken), http.StatusNoContent)
	if err := f.store.DB().QueryRow(`SELECT count(*) FROM admin_account_deletions WHERE former_user_id=?`, f.userID).Scan(&histories); err != nil || histories != 1 {
		t.Fatalf("replay duplicated history: %d %v", histories, err)
	}
}

func TestAdminAccountDeletionDrainsRequestsAndConcurrentRetry(t *testing.T) {
	f := newGameWireFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	validate := func(context.Context, int64, string) (bool, error) { return true, nil }
	active, release, err := f.app.forward.lifecycle.Admit(ctx, f.userID, "in-flight", validate)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	token := adminDeletionElevation(t, f)
	key := strings.Repeat("R", 22)
	path := fmt.Sprintf("/admin/api/users/%d", f.userID)
	body := `{"expected_revision":"0","confirmation":"DELETE"}`
	result := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		result <- testApplicationRequest(t, f.app.handler, http.MethodDelete, auditAdminHost, path, body,
			f.adminCookies, adminDeletionHeaders(key, token))
	}()
	select {
	case <-active.Done():
	case response := <-result:
		t.Fatalf("deletion returned before drain: %d %s", response.Code, response.Body.String())
	case <-ctx.Done():
		t.Fatal("deletion did not cancel active request")
	}
	var users int
	if err := f.store.DB().QueryRow(`SELECT count(*) FROM users WHERE id=?`, f.userID).Scan(&users); err != nil || users != 1 {
		t.Fatalf("deletion changed account before drain: %d %v", users, err)
	}
	if _, lateRelease, err := f.app.forward.lifecycle.Admit(ctx, f.userID, "late", validate); !errors.Is(err, lifecyclegate.ErrRetiring) {
		if lateRelease != nil {
			lateRelease()
		}
		t.Fatalf("late request crossed deletion barrier: %v", err)
	}
	retryToken := adminDeletionElevation(t, f)
	retry := testApplicationRequest(t, f.app.handler, http.MethodDelete, auditAdminHost, path, body,
		f.adminCookies, adminDeletionHeaders(key, retryToken))
	if retry.Code != http.StatusConflict {
		t.Fatalf("concurrent retry: %d %s", retry.Code, retry.Body.String())
	}
	release()
	select {
	case response := <-result:
		if response.Code != http.StatusNoContent {
			t.Fatalf("deletion after drain: %d %s", response.Code, response.Body.String())
		}
	case <-ctx.Done():
		t.Fatal("deletion did not finish after drain")
	}
	replay := testApplicationRequest(t, f.app.handler, http.MethodDelete, auditAdminHost, path, body,
		f.adminCookies, adminDeletionHeaders(key, retryToken))
	if replay.Code != http.StatusNoContent {
		t.Fatalf("retry did not preserve elevation or receipt: %d %s", replay.Code, replay.Body.String())
	}
}
