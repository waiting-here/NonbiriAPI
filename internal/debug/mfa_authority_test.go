package debug

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

type heldDebugBody struct {
	reader  io.Reader
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (body *heldDebugBody) Read(p []byte) (int, error) {
	body.once.Do(func() { close(body.entered); <-body.release })
	return body.reader.Read(p)
}

func (body *heldDebugBody) Close() error { return nil }

func setDebugBrowserMFA(verifier *debugTestVerifier, err error) {
	verifier.mu.Lock()
	verifier.browserErr = err
	verifier.mu.Unlock()
}

func requireDebugMFADenial(t *testing.T, response *httptest.ResponseRecorder) {
	t.Helper()
	if response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), `"mfa_required"`) {
		t.Fatalf("MFA rejection = %d %s", response.Code, response.Body.String())
	}
}

func TestDebugFinalAuthorityErrorsKeepTheirStatus(t *testing.T) {
	for _, test := range []struct {
		err    error
		status int
	}{
		{resources.ErrUnauthorized, http.StatusUnauthorized},
		{resources.ErrForbidden, http.StatusForbidden},
		{resources.ErrNotFound, http.StatusNotFound},
	} {
		response := httptest.NewRecorder()
		(&HTTPAPI{}).writeDomainError(response, test.err)
		if response.Code != test.status {
			t.Fatalf("%v returned %d: %s", test.err, response.Code, response.Body)
		}
	}
}

func TestDebugMutationRechecksMFAAfterBlockedBody(t *testing.T) {
	for _, name := range []string{"create", "mode", "stop", "replace"} {
		t.Run(name, func(t *testing.T) {
			clock := newDebugTestClock(180_000)
			verifier := &debugTestVerifier{state: IdentityActive}
			hub, _ := newDebugTestHub(t, clock, verifier)
			database := newDebugMutationDB(t)
			mutations, _ := newMutationRepository(database, clock.Now, debugFinalAuthority{verifier})
			api, _ := NewHTTPAPI(hub, mutations)
			principal := UserPrincipal{UserID: 180, SessionBinding: "binding-180"}
			var original DebugSessionMetadata
			if name != "create" {
				original = mustStartDebug(t, hub, principal.UserID, principal.SessionBinding)
			}
			method, path, content, handler := http.MethodPost, "/api/debug/session", "", api.createSession
			switch name {
			case "mode":
				method, path, content, handler = http.MethodPut, "/api/debug/session/mode", `{"mode":"live","expected_revision":"1","live_confirmation":true}`, api.changeMode
			case "stop":
				path, content, handler = "/api/debug/session/stop", `{"expected_revision":"1","confirm_inflight":false}`, api.stopSession
			case "replace":
				path, content, handler = "/api/debug/session/replace", `{"expected_revision":"1","confirm_inflight":false}`, api.replaceSession
			}
			body := &heldDebugBody{reader: strings.NewReader(content), entered: make(chan struct{}), release: make(chan struct{})}
			request := httptest.NewRequest(method, path, nil)
			request.Body = body
			request.Header.Set("Idempotency-Key", debugMutationKey('M'))
			response := httptest.NewRecorder()
			done := make(chan struct{})
			go func() { defer close(done); handler(response, request, principal) }()
			select {
			case <-body.entered:
			case <-time.After(time.Second):
				t.Fatal("handler did not start reading body")
			}
			setDebugBrowserMFA(verifier, authz.ErrMFARequired)
			close(body.release)
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("handler remained blocked")
			}
			requireDebugMFADenial(t, response)
			metadata, err := hub.Metadata(principal.UserID)
			if err != nil || metadata.Active != original.Active || metadata.ID != original.ID || metadata.Revision != original.Revision || metadata.Mode != original.Mode {
				t.Fatalf("delayed unauthorized mutation changed hub: before=%+v after=%+v err=%v", original, metadata, err)
			}
			var receipts int
			if err := database.QueryRow(`SELECT COUNT(*) FROM idempotency_records`).Scan(&receipts); err != nil || receipts != 0 {
				t.Fatalf("unauthorized receipt count=%d err=%v", receipts, err)
			}
		})
	}
}

func TestDebugMutationRechecksMFAAfterMutexBeforeReplay(t *testing.T) {
	clock := newDebugTestClock(181_000)
	verifier := &debugTestVerifier{state: IdentityActive}
	hub, _ := newDebugTestHub(t, clock, verifier)
	mutations, _ := newMutationRepository(newDebugMutationDB(t), clock.Now, debugFinalAuthority{verifier})
	api, _ := NewHTTPAPI(hub, mutations)
	principal := UserPrincipal{UserID: 181, SessionBinding: "binding-181"}
	newRequest := func() *http.Request {
		request := httptest.NewRequest(http.MethodPost, "/api/debug/session", nil)
		request.Header.Set("Idempotency-Key", debugMutationKey('N'))
		return request
	}
	first := httptest.NewRecorder()
	api.createSession(first, newRequest(), principal)
	if first.Code != http.StatusCreated {
		t.Fatal(first.Code, first.Body.String())
	}
	mutations.mu.Lock()
	body := &heldDebugBody{reader: strings.NewReader(""), entered: make(chan struct{}), release: make(chan struct{})}
	request := newRequest()
	request.Body = body
	response := httptest.NewRecorder()
	done := make(chan struct{})
	go func() { defer close(done); api.createSession(response, request, principal) }()
	select {
	case <-body.entered:
	case <-time.After(time.Second):
		mutations.mu.Unlock()
		t.Fatal("replay did not start reading body")
	}
	close(body.release)
	setDebugBrowserMFA(verifier, authz.ErrMFARequired)
	mutations.mu.Unlock()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("replay remained blocked")
	}
	requireDebugMFADenial(t, response)
	if response.Body.String() == first.Body.String() {
		t.Fatal("returned a private cached response after MFA expired")
	}
}

type debugSQLFinalAuthority struct{ calls atomic.Int64 }

func (authority *debugSQLFinalAuthority) AuthorizeUserMutation(ctx context.Context, tx *sql.Tx, _ int64) error {
	authority.calls.Add(1)
	var allowed bool
	if err := tx.QueryRowContext(ctx, `SELECT allowed FROM debug_authority`).Scan(&allowed); err != nil {
		return err
	}
	if !allowed {
		return authz.ErrMFARequired
	}
	return nil
}

func TestDebugMutationRechecksMFAAfterSQLiteWriterWait(t *testing.T) {
	schema := newDebugMutationDB(t)
	var ddl string
	if err := schema.QueryRow(`SELECT sql FROM sqlite_master WHERE name='idempotency_records'`).Scan(&ddl); err != nil {
		t.Fatal(err)
	}
	database, err := sql.Open("sqlite", filepath.ToSlash(filepath.Join(t.TempDir(), "debug.db"))+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	database.SetMaxOpenConns(2)
	for _, statement := range []string{ddl, `CREATE TABLE debug_authority(allowed INTEGER NOT NULL)`, `INSERT INTO debug_authority VALUES(1)`} {
		if _, err := database.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	authority := &debugSQLFinalAuthority{}
	repository, _ := newMutationRepository(database, time.Now, authority)
	writer, err := database.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Rollback()
	if _, err := writer.Exec(`UPDATE debug_authority SET allowed=0`); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	var operations atomic.Int64
	go func() {
		_, _, _, err := repository.Execute(t.Context(), 182, resources.ControlMutation{IdempotencyKey: debugMutationKey('P'), Method: http.MethodPost, Route: "/api/debug/session"}, func() (int, []byte, error) {
			operations.Add(1)
			return http.StatusCreated, []byte("{}\n"), nil
		})
		done <- err
	}()
	deadline := time.Now().Add(time.Second)
	for database.Stats().InUse != 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if database.Stats().InUse != 2 {
		t.Fatal("mutation did not reach SQLite while writer was held")
	}
	if authority.calls.Load() != 0 {
		t.Fatal("authority was read before the SQLite writer wait")
	}
	if err := writer.Commit(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, authz.ErrMFARequired) || operations.Load() != 0 || authority.calls.Load() != 1 {
			t.Fatalf("writer wait result=%v operations=%d authority calls=%d", err, operations.Load(), authority.calls.Load())
		}
	case <-time.After(time.Second):
		t.Fatal("mutation did not resume after writer release")
	}
}

func TestDebugBrowserMFARequiredPreservesCallerCapture(t *testing.T) {
	clock := newDebugTestClock(183_000)
	verifier := &debugTestVerifier{state: IdentityActive}
	hub, _ := newDebugTestHub(t, clock, verifier)
	mutations, _ := newMutationRepository(newDebugMutationDB(t), clock.Now, debugFinalAuthority{verifier})
	api, _ := NewHTTPAPI(hub, mutations)
	principal := UserPrincipal{UserID: 183, SessionBinding: "binding-183"}
	mustStartDebug(t, hub, principal.UserID, principal.SessionBinding)
	if _, err := hub.ChangeMode(principal.UserID, "1", ModeLive, true); err != nil {
		t.Fatal(err)
	}
	setDebugBrowserMFA(verifier, authz.ErrMFARequired)
	response := httptest.NewRecorder()
	api.getSession(response, httptest.NewRequest(http.MethodGet, "/api/debug/session", nil), principal)
	requireDebugMFADenial(t, response)
	decision, err := hub.DecideAfterAdmission(t.Context(), CaptureInput{UserID: principal.UserID, RouteKind: RouteOpenAIChat, Model: "m", MediaType: "application/json", Body: []byte(`{}`), IdentityCertain: true})
	if err != nil || decision.Mode != ModeLive || decision.Trace == nil {
		t.Fatalf("browser MFA altered CallerKey capture: decision=%+v err=%v", decision, err)
	}
	setDebugBrowserMFA(verifier, nil)
	subscription, err := hub.Subscribe(t.Context(), principal.UserID, principal.SessionBinding, "")
	if err != nil {
		t.Fatal(err)
	}
	_ = mustNextDebug(t, subscription)
	hub.config.heartbeat = time.Millisecond
	setDebugBrowserMFA(verifier, authz.ErrMFARequired)
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if err := subscription.Stream(ctx, newLockedDebugWriter()); !errors.Is(err, authz.ErrMFARequired) {
		t.Fatalf("MFA-expired observer: %v", err)
	}
	metadata, err := hub.Metadata(principal.UserID)
	if err != nil || !metadata.Active || metadata.Mode != ModeLive || metadata.ConnectedSubscribers != 0 {
		t.Fatalf("browser expiry changed capture session: %+v %v", metadata, err)
	}
	select {
	case <-decision.Trace.Context().Done():
		t.Fatal("browser expiry canceled a CallerKey live trace")
	default:
	}
}
