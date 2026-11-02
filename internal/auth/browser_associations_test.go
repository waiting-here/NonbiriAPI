package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/dbtest"
	"github.com/waiting-here/NonbiriAPI/internal/host"
)

func browserLogin(t *testing.T, f *runtimeFixture, id string, browser *http.Cookie) (*http.Cookie, *httptest.ResponseRecorder) {
	t.Helper()
	addLogin(f.provider, id, id)
	req := httptest.NewRequest(http.MethodGet, "https://user.example/api/auth/discord/callback", nil)
	if browser != nil {
		req.AddCookie(browser)
	}
	rec := httptest.NewRecorder()
	f.runtime.completeLogin(rec, req, f.provider.logins[id], "/")
	if rec.Code != http.StatusFound {
		t.Fatalf("login: %d %s", rec.Code, rec.Body)
	}
	return responseCookie(t, rec, UserSessionCookieName), rec
}

func browserCount(t *testing.T, f *runtimeFixture) int {
	t.Helper()
	var count int
	if err := f.store.ReadDB().QueryRow("SELECT COUNT(*) FROM browser_associations").Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func TestBrowserAssociationLoginLogoutAndExistingSession(t *testing.T) {
	f := newRuntimeFixture(t, nil)
	session, rec := browserLogin(t, f, "browser-user-a", nil)
	browser := responseCookie(t, rec, BrowserAuditCookieName)
	if browser.Domain != "" || browser.Path != "/api" || !browser.HttpOnly || !browser.Secure || browser.SameSite != http.SameSiteLaxMode || browser.MaxAge != 2592000 {
		t.Fatalf("cookie attributes: %+v", browser)
	}
	if strings.Contains(rec.Body.String(), browser.Value) {
		t.Fatal("token in response body")
	}
	if browserCount(t, f) != 1 {
		t.Fatal("successful login not collected")
	}
	logout := request(t, f.runtime.UserHandler(), host.StationUser, http.MethodPost, "https://user.example/api/auth/logout", "", []*http.Cookie{session, browser}, map[string]string{"Origin": "https://user.example"})
	if logout.Code != http.StatusNoContent {
		t.Fatalf("logout: %d %s", logout.Code, logout.Body)
	}
	for _, cookie := range logout.Result().Cookies() {
		if cookie.Name == BrowserAuditCookieName {
			t.Fatal("logout cleared browser evidence")
		}
	}
	_, second := browserLogin(t, f, "browser-user-b", browser)
	for _, cookie := range second.Result().Cookies() {
		if cookie.Name == BrowserAuditCookieName {
			t.Fatal("valid browser token was renewed")
		}
	}
	var distinct int
	if err := f.store.ReadDB().QueryRow("SELECT COUNT(DISTINCT browser_digest) FROM browser_associations").Scan(&distinct); err != nil || distinct != 1 || browserCount(t, f) != 2 {
		t.Fatal("shared browser not grouped", distinct, err)
	}
	for _, cookie := range second.Result().Cookies() {
		if cookie.Name == UserSessionCookieName {
			session = cookie
		}
	}
	existing := request(t, f.runtime.UserHandler(), host.StationUser, http.MethodGet, "https://user.example/api/session", "", []*http.Cookie{session}, nil)
	if existing.Code != http.StatusOK {
		t.Fatal(existing.Code, existing.Body)
	}
	other := responseCookie(t, existing, BrowserAuditCookieName)
	if other.Value == browser.Value || browserCount(t, f) != 3 {
		t.Fatal("existing session did not acquire independent browser cookie")
	}
}

func TestBrowserAssociationRejectsForgedDuplicateExpiredAndWrongScope(t *testing.T) {
	f := newRuntimeFixture(t, nil)
	session, rec := browserLogin(t, f, "browser-validate", nil)
	browser := responseCookie(t, rec, BrowserAuditCookieName)
	if _, ok := browserTokenIssued(f.runtime.browserAuditKey, "https://other.example", browser.Value, authTestNow); ok {
		t.Fatal("accepted other host scope")
	}
	if _, ok := browserTokenIssued(make([]byte, 32), f.runtime.siteOrigin, browser.Value, authTestNow); ok {
		t.Fatal("accepted other key")
	}
	if _, ok := browserTokenIssued(f.runtime.browserAuditKey, f.runtime.siteOrigin, browser.Value, authTestNow-1); ok {
		t.Fatal("accepted future token")
	}
	if _, ok := browserTokenIssued(f.runtime.browserAuditKey, f.runtime.siteOrigin, browser.Value, authTestNow+2592000); ok {
		t.Fatal("accepted expired token")
	}
	for _, cookies := range [][]*http.Cookie{
		{session, {Name: BrowserAuditCookieName, Value: strings.Repeat("a", 64)}},
		{session, browser, browser},
		{session, {Name: BrowserAuditCookieName, Value: browser.Value + "x"}},
	} {
		response := request(t, f.runtime.UserHandler(), host.StationUser, http.MethodGet, "https://user.example/api/me", "", cookies, nil)
		replacement := responseCookie(t, response, BrowserAuditCookieName)
		if response.Code != 200 || replacement.Value == browser.Value {
			t.Fatal("invalid cookie was linked instead of replaced")
		}
	}
	// A cookie with an authentic signature but an expired timestamp is replaced.
	expired, _ := signedBrowserToken(f.runtime.browserAuditKey, f.runtime.siteOrigin, authTestNow-2592000)
	response := request(t, f.runtime.UserHandler(), host.StationUser, http.MethodGet, "https://user.example/api/session", "", []*http.Cookie{session, {Name: BrowserAuditCookieName, Value: expired}}, nil)
	if response.Code != 200 || responseCookie(t, response, BrowserAuditCookieName).Value == expired {
		t.Fatal("expired cookie reused")
	}
}

func TestBrowserAssociationBoundedWritesCancellationAndDeletion(t *testing.T) {
	f := newRuntimeFixture(t, nil)
	session, rec := browserLogin(t, f, "browser-bounds", nil)
	browser := responseCookie(t, rec, BrowserAuditCookieName)
	var userID int64
	hash := sessionLookupHash(session.Value)
	if err := f.store.ReadDB().QueryRow("SELECT user_id FROM sessions WHERE token_hash=?", hash).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	digest := sessionLookupHash(browser.Value)
	dbtest.WithoutWriter(t, f.store.DB(), f.store.ReadDB(), func(ctx context.Context) {
		if err := f.runtime.recordBrowserAssociation(ctx, userID, hash, digest, authTestNow, authTestNow+3599, authTestNow+2592000); err != nil {
			t.Fatal("recent observation requested writer", err)
		}
	})
	for i := range 40 {
		if err := f.runtime.recordBrowserAssociation(t.Context(), userID, hash, fmt.Sprintf("%064x", i+1), authTestNow, authTestNow+int64(i), authTestNow+2592000); err != nil {
			t.Fatal(err)
		}
	}
	if browserCount(t, f) != MaxUserBrowsers {
		t.Fatal("unbounded per-user browser storage")
	}
	// A new cookie observed in the same second must survive the size cap.
	newDigest := strings.Repeat("f", 64)
	if err := f.runtime.recordBrowserAssociation(t.Context(), userID, hash, newDigest, authTestNow, authTestNow+39, authTestNow+2592000); err != nil {
		t.Fatal(err)
	}
	dbtest.WithoutWriter(t, f.store.DB(), f.store.ReadDB(), func(ctx context.Context) {
		if err := f.runtime.recordBrowserAssociation(ctx, userID, hash, newDigest, authTestNow, authTestNow+39, authTestNow+2592000); err != nil {
			t.Fatal("newest cookie was evicted at the cap", err)
		}
	})
	tx, err := f.store.DB().BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	err = f.runtime.recordBrowserAssociation(ctx, userID, hash, strings.Repeat("e", 64), authTestNow, authTestNow+50, authTestNow+2592000)
	cancel()
	if err == nil {
		t.Fatal("blocked writer ignored cancellation")
	}
	if _, err = tx.Exec("DELETE FROM users WHERE id=?", userID); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err = f.runtime.recordBrowserAssociation(t.Context(), userID, hash, strings.Repeat("f", 64), authTestNow, authTestNow+60, authTestNow+2592000); !errors.Is(err, errSessionUnauthorized) {
		t.Fatal(err)
	}
	if browserCount(t, f) != 0 {
		t.Fatal("late observation revived deleted account")
	}
	if err = f.store.DB().PingContext(t.Context()); err != nil {
		t.Fatal("writer not reusable", err)
	}
}

func TestBrowserAssociationFailureDoesNotFailSessionOrCollectRejectedLogin(t *testing.T) {
	f := newRuntimeFixture(t, nil)
	session, _ := browserLogin(t, f, "browser-errors", nil)
	if _, err := f.store.DB().Exec("DROP TABLE browser_associations"); err != nil {
		t.Fatal(err)
	}
	_, login := browserLogin(t, f, "browser-login-with-audit-failure", nil)
	for _, cookie := range login.Result().Cookies() {
		if cookie.Name == BrowserAuditCookieName {
			t.Fatal("failed login audit emitted browser token")
		}
	}
	response := request(t, f.runtime.UserHandler(), host.StationUser, http.MethodGet, "https://user.example/api/session", "", []*http.Cookie{session}, nil)
	if response.Code != 200 {
		t.Fatal("audit failure blocked session", response.Code, response.Body)
	}
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == BrowserAuditCookieName {
			t.Fatal("failed audit emitted new token")
		}
	}
	if _, err := f.store.DB().Exec("UPDATE users SET is_banned=1 WHERE is_admin=0"); err != nil {
		t.Fatal(err)
	}
	denied := request(t, f.runtime.UserHandler(), host.StationUser, http.MethodGet, "https://user.example/api/session", "", []*http.Cookie{session}, nil)
	if denied.Code == 200 {
		t.Fatal("banned session accepted")
	}
	for _, cookie := range denied.Result().Cookies() {
		if cookie.Name == BrowserAuditCookieName {
			t.Fatal("rejected session collected")
		}
	}
}

func TestBrowserAssociationQueuedObservationLosesToAccountDeletion(t *testing.T) {
	f := newRuntimeFixture(t, nil)
	session, _ := browserLogin(t, f, "browser-deletion-race", nil)
	hash := sessionLookupHash(session.Value)
	var userID int64
	if err := f.store.ReadDB().QueryRow("SELECT user_id FROM sessions WHERE token_hash=?", hash).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	tx, err := f.store.DB().BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	waits := f.store.DB().Stats().WaitCount
	done := make(chan error, 1)
	go func() {
		done <- f.runtime.recordBrowserAssociation(ctx, userID, hash, strings.Repeat("e", 64), authTestNow, authTestNow, authTestNow+2592000)
	}()
	for f.store.DB().Stats().WaitCount == waits {
		select {
		case err := <-done:
			t.Fatal("observation did not wait for writer", err)
		case <-ctx.Done():
			t.Fatal("observation did not reach writer")
		case <-time.After(time.Millisecond):
		}
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM users WHERE id=?", userID); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err = <-done; !errors.Is(err, errSessionUnauthorized) {
		t.Fatal("queued observation did not recheck current account/session", err)
	}
	if browserCount(t, f) != 0 {
		t.Fatal("queued observation revived deleted data")
	}
}
