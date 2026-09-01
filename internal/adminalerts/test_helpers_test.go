package adminalerts

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/dbtest"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

const alertTestNow int64 = 1_700_000_000

type inspectingFinalAuthorizer struct {
	calls        int
	usedTx       bool
	forced       error
	beforeReturn func(context.Context, *sql.Tx, int64) error
}

func (authorizer *inspectingFinalAuthorizer) AuthorizeAdmin(ctx context.Context, tx *sql.Tx, adminID int64) error {
	authorizer.calls++
	if tx == nil {
		return errors.New("missing final transaction")
	}
	authorizer.usedTx = true
	if authorizer.beforeReturn != nil {
		if err := authorizer.beforeReturn(ctx, tx, adminID); err != nil {
			return err
		}
	}
	var isAdmin int
	if err := tx.QueryRowContext(ctx, `SELECT is_admin FROM users WHERE id=?`, adminID).Scan(&isAdmin); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return authz.ErrUnauthorized
		}
		return err
	}
	if authorizer.forced != nil {
		return authorizer.forced
	}
	if isAdmin != 1 {
		return authz.ErrForbidden
	}
	return nil
}

type routeCapture struct {
	handlers map[string]AuthorizedAdminHandler
	failAt   int
	calls    int
}

func (capture *routeCapture) RegisterAdminRoute(method, pattern string, handler AuthorizedAdminHandler) error {
	capture.calls++
	if capture.failAt > 0 && capture.calls == capture.failAt {
		return errors.New("registration rejected")
	}
	if capture.handlers == nil {
		capture.handlers = make(map[string]AuthorizedAdminHandler)
	}
	key := method + " " + pattern
	if handler == nil {
		return errors.New("nil handler")
	}
	if _, exists := capture.handlers[key]; exists {
		return errors.New("duplicate route")
	}
	capture.handlers[key] = handler
	return nil
}

type alertTestEnvironment struct {
	store      *db.Store
	repository *Repository
	authorizer *inspectingFinalAuthorizer
	routes     *routeCapture
	clock      atomic.Int64
	adminID    int64
}

func newAlertTestEnvironment(t *testing.T) *alertTestEnvironment {
	t.Helper()
	master := bytes.Repeat([]byte{0x51}, secret.MasterKeyBytes)
	vault, err := secret.New(master)
	clear(master)
	if err != nil {
		t.Fatalf("secret.New: %v", err)
	}
	t.Cleanup(func() { _ = vault.Close() })
	path := filepath.Join(t.TempDir(), "administrator-alerts.sqlite")
	dbtest.EnsureOwnerOnlyParent(t, path)
	store, err := db.Open(path, vault)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	environment := &alertTestEnvironment{store: store, authorizer: &inspectingFinalAuthorizer{}}
	environment.clock.Store(alertTestNow)
	repository, err := NewRepository(Config{
		Store: store, CursorKeys: vault, FinalAuth: environment.authorizer,
		Now: func() time.Time { return time.Unix(environment.clock.Load(), 0) },
	})
	if err != nil {
		t.Fatalf("NewRepository: %v", err)
	}
	environment.repository = repository
	environment.adminID = environment.seedAdmin(t)
	environment.routes = &routeCapture{}
	if err := RegisterRoutes(environment.routes, repository); err != nil {
		t.Fatalf("RegisterRoutes: %v", err)
	}
	return environment
}

func (environment *alertTestEnvironment) seedAdmin(t *testing.T) int64 {
	t.Helper()
	zero := make([]byte, 16)
	result, err := environment.store.DB().Exec(`
INSERT INTO users(
 discord_id,username,is_admin,donation_credit_mag,total_requests,total_uncached_input_tokens,
 total_cache_write_input_tokens,total_cache_read_input_tokens,total_output_tokens,total_unknown_usage_requests,
 revision,lang,created_at,updated_at
) VALUES(NULL,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		"alert administrator", 1, zero, zero, zero, zero, zero, zero, zero, zero, "zh", alertTestNow, alertTestNow)
	if err != nil {
		t.Fatalf("seed administrator: %v", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("seed administrator id: %v", err)
	}
	return id
}

func (environment *alertTestEnvironment) seedAlert(
	t *testing.T,
	kind string,
	message string,
	ref string,
	subjectUserID *int64,
	createdAt int64,
	resolved bool,
) int64 {
	t.Helper()
	var subject any
	if subjectUserID != nil {
		subject = *subjectUserID
	}
	resolvedValue := 0
	var resolvedAt any
	if resolved {
		resolvedValue = 1
		resolvedAt = createdAt + 1
	}
	result, err := environment.store.DB().Exec(`
INSERT INTO admin_alerts(kind,message,ref,subject_user_id,created_at,resolved,resolved_at)
VALUES(?,?,?,?,?,?,?)`, kind, message, ref, subject, createdAt, resolvedValue, resolvedAt)
	if err != nil {
		t.Fatalf("seed administrator alert: %v", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("seed administrator alert id: %v", err)
	}
	return id
}

func (environment *alertTestEnvironment) handler(t *testing.T, method, pattern string) AuthorizedAdminHandler {
	t.Helper()
	handler := environment.routes.handlers[method+" "+pattern]
	if handler == nil {
		t.Fatalf("missing route %s %s", method, pattern)
	}
	return handler
}

func invokeAlertHandler(
	t *testing.T,
	handler AuthorizedAdminHandler,
	method string,
	target string,
	body *string,
	pathID string,
	adminID int64,
	headers http.Header,
) *httptest.ResponseRecorder {
	t.Helper()
	var request *http.Request
	if body == nil {
		request = httptest.NewRequest(method, target, nil)
	} else {
		request = httptest.NewRequest(method, target, strings.NewReader(*body))
	}
	if pathID != "" {
		request.SetPathValue("id", pathID)
	}
	for name, values := range headers {
		for _, value := range values {
			request.Header.Add(name, value)
		}
	}
	recorder := httptest.NewRecorder()
	handler(recorder, request, AdminPrincipal{UserID: adminID})
	return recorder
}

func stringBody(value string) *string { return &value }

func requireErrorCode(t *testing.T, response *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if response.Code != status {
		t.Fatalf("status=%d want=%d body=%s", response.Code, status, response.Body.String())
	}
	var envelope httperr.Envelope
	if err := jsonDecode(response, &envelope); err != nil {
		t.Fatalf("decode error envelope: %v body=%s", err, response.Body.String())
	}
	if envelope.Error.Code != code || envelope.Error.Source != httperr.SourcePlatform {
		t.Fatalf("error=%+v want code=%s", envelope.Error, code)
	}
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("missing no-store: %q", response.Header().Get("Cache-Control"))
	}
}

func jsonDecode(response *httptest.ResponseRecorder, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(response.Body.Bytes()))
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("trailing JSON response")
	}
	return nil
}

func alertIDString(id int64) string { return strconv.FormatInt(id, 10) }
