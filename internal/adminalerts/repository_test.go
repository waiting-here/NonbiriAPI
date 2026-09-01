package adminalerts

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
)

func TestResolveAndReopenAreIdempotentSetStateWithoutReplay(t *testing.T) {
	environment := newAlertTestEnvironment(t)
	id := environment.seedAlert(t, string(KindFetchFailed), "idempotent target", "", &environment.adminID,
		alertTestNow-100, false)
	handler := environment.handler(t, http.MethodPost, routeResolveAlert)
	beforeReplay := countAlertRows(t, environment.store.DB(), `SELECT COUNT(*) FROM idempotency_records`)
	headers := http.Header{}
	headers.Add("Idempotency-Key", "not-a-valid-key")
	headers.Add("Idempotency-Key", "a-second-value")

	first := decodeAdminAlert(t, invokeAlertHandler(t, handler, http.MethodPost, routeResolveAlert,
		nil, alertIDString(id), environment.adminID, headers))
	if !first.Resolved || first.ResolvedAt == nil || *first.ResolvedAt != alertTestNow ||
		first.SubjectUserID == nil || *first.SubjectUserID != alertIDString(environment.adminID) {
		t.Fatalf("first resolve=%+v", first)
	}

	environment.clock.Store(alertTestNow + 10)
	repeated := decodeAdminAlert(t, invokeAlertHandler(t, handler, http.MethodPost, routeResolveAlert,
		stringBody(`{"resolved":true}`), alertIDString(id), environment.adminID, nil))
	if repeated.ResolvedAt == nil || *repeated.ResolvedAt != alertTestNow {
		t.Fatalf("repeat churned resolved_at: %+v", repeated)
	}

	reopened := decodeAdminAlert(t, invokeAlertHandler(t, handler, http.MethodPost, routeResolveAlert,
		stringBody(`{"resolved":false}`), alertIDString(id), environment.adminID, nil))
	if reopened.Resolved || reopened.ResolvedAt != nil {
		t.Fatalf("reopen=%+v", reopened)
	}
	environment.clock.Store(alertTestNow + 20)
	repeatedReopen := decodeAdminAlert(t, invokeAlertHandler(t, handler, http.MethodPost, routeResolveAlert,
		stringBody(`{"resolved":false}`), alertIDString(id), environment.adminID, nil))
	if repeatedReopen.Resolved || repeatedReopen.ResolvedAt != nil {
		t.Fatalf("repeat reopen=%+v", repeatedReopen)
	}

	environment.clock.Store(alertTestNow + 30)
	emptyObject := decodeAdminAlert(t, invokeAlertHandler(t, handler, http.MethodPost, routeResolveAlert,
		stringBody(`{}`), alertIDString(id), environment.adminID, nil))
	if !emptyObject.Resolved || emptyObject.ResolvedAt == nil || *emptyObject.ResolvedAt != alertTestNow+30 {
		t.Fatalf("empty object did not default to resolve: %+v", emptyObject)
	}
	environment.clock.Store(alertTestNow + 40)
	whitespace := decodeAdminAlert(t, invokeAlertHandler(t, handler, http.MethodPost, routeResolveAlert,
		stringBody(" \r\n\t "), alertIDString(id), environment.adminID, nil))
	if whitespace.ResolvedAt == nil || *whitespace.ResolvedAt != alertTestNow+30 {
		t.Fatalf("whitespace body changed idempotent resolve: %+v", whitespace)
	}

	afterReplay := countAlertRows(t, environment.store.DB(), `SELECT COUNT(*) FROM idempotency_records`)
	if afterReplay != beforeReplay {
		t.Fatalf("set-state route created replay rows: before=%d after=%d", beforeReplay, afterReplay)
	}
}

func TestUnknownPersistedKindFailsClosedAndRollsBackResolve(t *testing.T) {
	environment := newAlertTestEnvironment(t)
	if _, err := environment.store.DB().Exec(`PRAGMA ignore_check_constraints=ON`); err != nil {
		t.Fatal(err)
	}
	result, err := environment.store.DB().Exec(`
INSERT INTO admin_alerts(kind,message,ref,created_at,resolved)
VALUES('future_unknown_kind','must not escape','private-payload',?,0)`, alertTestNow-1)
	if err != nil {
		t.Fatalf("seed hostile kind: %v", err)
	}
	if _, err := environment.store.DB().Exec(`PRAGMA ignore_check_constraints=OFF`); err != nil {
		t.Fatal(err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}

	listResponse := invokeAlertHandler(t, environment.handler(t, http.MethodGet, routeAlerts), http.MethodGet,
		routeAlerts, nil, "", environment.adminID, nil)
	requireErrorCode(t, listResponse, http.StatusInternalServerError, httperr.CodeInternal)
	if bytesContains(listResponse.Body.Bytes(), []byte("future_unknown_kind")) ||
		bytesContains(listResponse.Body.Bytes(), []byte("private-payload")) {
		t.Fatalf("hostile row escaped in response: %s", listResponse.Body.String())
	}

	resolveResponse := invokeAlertHandler(t, environment.handler(t, http.MethodPost, routeResolveAlert), http.MethodPost,
		routeResolveAlert, nil, alertIDString(id), environment.adminID, nil)
	requireErrorCode(t, resolveResponse, http.StatusInternalServerError, httperr.CodeInternal)
	var (
		resolved   int
		resolvedAt sql.NullInt64
	)
	if err := environment.store.DB().QueryRow(`SELECT resolved,resolved_at FROM admin_alerts WHERE id=?`, id).
		Scan(&resolved, &resolvedAt); err != nil {
		t.Fatal(err)
	}
	if resolved != 0 || resolvedAt.Valid {
		t.Fatalf("failed projection committed state change: resolved=%d resolved_at=%+v", resolved, resolvedAt)
	}
}

func TestFinalTransactionDemotionAndAuthorizationFailureRollback(t *testing.T) {
	environment := newAlertTestEnvironment(t)
	id := environment.seedAlert(t, string(KindMaintenanceEnabled), "original", "maintenance", nil,
		alertTestNow-10, false)
	getHandler := environment.handler(t, http.MethodGet, routeAlerts)
	postHandler := environment.handler(t, http.MethodPost, routeResolveAlert)
	page := decodeAlertPage(t, invokeAlertHandler(t, getHandler, http.MethodGet, routeAlerts,
		nil, "", environment.adminID, nil))
	if len(page.Data) != 1 || !environment.authorizer.usedTx {
		t.Fatalf("initial read=%+v used_tx=%v", page, environment.authorizer.usedTx)
	}

	environment.authorizer.forced = authz.ErrForbidden
	// Final authorization runs before cursor verification, so a demoted actor
	// cannot use cursor-oracle differences from the protected repository.
	requireErrorCode(t, invokeAlertHandler(t, getHandler, http.MethodGet, routeAlerts+"?cursor=bogus",
		nil, "", environment.adminID, nil), http.StatusForbidden, httperr.CodeForbidden)
	requireErrorCode(t, invokeAlertHandler(t, postHandler, http.MethodPost, routeResolveAlert,
		nil, alertIDString(id), environment.adminID, nil), http.StatusForbidden, httperr.CodeForbidden)
	assertAlertStorage(t, environment, id, "original", 0)

	environment.authorizer.forced = nil
	environment.authorizer.beforeReturn = func(ctx context.Context, tx *sql.Tx, _ int64) error {
		_, err := tx.ExecContext(ctx, `UPDATE admin_alerts SET message='rollback-probe' WHERE id=?`, id)
		return err
	}
	environment.authorizer.forced = authz.ErrForbidden
	requireErrorCode(t, invokeAlertHandler(t, postHandler, http.MethodPost, routeResolveAlert,
		nil, alertIDString(id), environment.adminID, nil), http.StatusForbidden, httperr.CodeForbidden)
	assertAlertStorage(t, environment, id, "original", 0)

	environment.authorizer.beforeReturn = nil
	environment.authorizer.forced = nil
	calls := environment.authorizer.calls
	requireErrorCode(t, invokeAlertHandler(t, getHandler, http.MethodGet, routeAlerts,
		nil, "", 0, nil), http.StatusUnauthorized, httperr.CodeUnauthorized)
	if environment.authorizer.calls != calls {
		t.Fatalf("invalid principal reached final authorizer: calls=%d want=%d", environment.authorizer.calls, calls)
	}

	environment.authorizer.forced = errors.New("authority store failed")
	requireErrorCode(t, invokeAlertHandler(t, postHandler, http.MethodPost, routeResolveAlert,
		nil, alertIDString(id), environment.adminID, nil), http.StatusInternalServerError, httperr.CodeInternal)
	assertAlertStorage(t, environment, id, "original", 0)
}

func decodeAdminAlert(t *testing.T, response *httptest.ResponseRecorder) AdminAlert {
	t.Helper()
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("missing no-store: %q", response.Header().Get("Cache-Control"))
	}
	var value AdminAlert
	if err := jsonDecode(response, &value); err != nil {
		t.Fatalf("decode administrator alert: %v", err)
	}
	return value
}

func countAlertRows(t *testing.T, database *sql.DB, statement string, arguments ...any) int {
	t.Helper()
	var count int
	if err := database.QueryRow(statement, arguments...).Scan(&count); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	return count
}

func assertAlertStorage(t *testing.T, environment *alertTestEnvironment, id int64, message string, resolved int) {
	t.Helper()
	var (
		storedMessage  string
		storedResolved int
	)
	if err := environment.store.DB().QueryRow(`SELECT message,resolved FROM admin_alerts WHERE id=?`, id).
		Scan(&storedMessage, &storedResolved); err != nil {
		t.Fatal(err)
	}
	if storedMessage != message || storedResolved != resolved {
		t.Fatalf("stored alert message=%q resolved=%d want=%q/%d", storedMessage, storedResolved, message, resolved)
	}
}

func bytesContains(haystack, needle []byte) bool {
	for index := 0; index+len(needle) <= len(haystack); index++ {
		matched := true
		for offset := range needle {
			if haystack[index+offset] != needle[offset] {
				matched = false
				break
			}
		}
		if matched {
			return true
		}
	}
	return len(needle) == 0
}
