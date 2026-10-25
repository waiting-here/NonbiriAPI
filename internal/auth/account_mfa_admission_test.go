package auth

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/lifecyclegate"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
	"modernc.org/sqlite"
)

// Count actual SQLite queries so admission cannot silently add another
// per-request session or MFA lookup outside the lifecycle lease.
type admissionQueryTrace struct {
	mu      sync.Mutex
	queries []string
}

type admissionConnector struct {
	dsn   string
	trace *admissionQueryTrace
}

func (c admissionConnector) Driver() driver.Driver { return &sqlite.Driver{} }
func (c admissionConnector) Connect(context.Context) (driver.Conn, error) {
	conn, err := c.Driver().Open(c.dsn)
	if err != nil {
		return nil, err
	}
	return admissionConnection{Conn: conn, trace: c.trace}, nil
}

type admissionConnection struct {
	driver.Conn
	trace *admissionQueryTrace
}

func (c admissionConnection) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	return c.Conn.(driver.ConnBeginTx).BeginTx(ctx, opts)
}
func (c admissionConnection) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	c.trace.mu.Lock()
	c.trace.queries = append(c.trace.queries, query)
	c.trace.mu.Unlock()
	return c.Conn.(driver.QueryerContext).QueryContext(ctx, query, args)
}
func (c admissionConnection) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	return c.Conn.(driver.ExecerContext).ExecContext(ctx, query, args)
}

func TestAccountMFAAdmissionUsesOneLiveAuthorityQuery(t *testing.T) {
	f := newRuntimeFixture(t, nil)
	gate, err := lifecyclegate.New(lifecyclegate.Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer gate.Close()
	if err := f.runtime.AttachUserLifecycleGate(gate); err != nil {
		t.Fatal(err)
	}
	if err := f.runtime.RegisterUserRoute(http.MethodGet, "/api/mfa-admission-probe", func(w http.ResponseWriter, _ *http.Request, _ resources.UserPrincipal) {
		w.WriteHeader(http.StatusNoContent)
	}); err != nil {
		t.Fatal(err)
	}
	cookie := loginUser(t, f, "mfa-query-count", "")
	_, actor := userSessionIdentity(t, f, cookie)
	seed := enrollMFA(t, f, actor)
	clear(seed)
	var sequence int
	var name, path string
	if err := f.store.DB().QueryRow(`PRAGMA database_list`).Scan(&sequence, &name, &path); err != nil {
		t.Fatal(err)
	}
	trace := &admissionQueryTrace{}
	traced := sql.OpenDB(admissionConnector{dsn: path, trace: trace})
	defer traced.Close()
	f.runtime.db = traced
	f.runtime.readDB = traced
	response := mfaRequest(t, f, cookie, http.MethodGet, "/api/mfa-admission-probe", "")
	if response.Code != http.StatusNoContent {
		t.Fatal(response.Code, response.Body)
	}
	trace.mu.Lock()
	queries := append([]string(nil), trace.queries...)
	trace.mu.Unlock()
	var sessionReads, mfaReads int
	for _, query := range queries {
		normalized := strings.ToLower(strings.Join(strings.Fields(query), " "))
		if strings.Contains(normalized, "from sessions") {
			sessionReads++
		}
		if strings.Contains(normalized, "account_mfa_") {
			mfaReads++
			if !strings.Contains(normalized, "left join site_config") || !strings.Contains(normalized, "account_mfa_proofs") {
				t.Fatal("MFA admission did not combine live policy and proof")
			}
		}
	}
	if sessionReads != 2 || mfaReads != 1 {
		t.Fatalf("session reads=%d MFA reads=%d; want authentication plus one combined admission read", sessionReads, mfaReads)
	}
	t.Logf("request executed %d session reads: initial authentication and one combined policy/proof admission read", sessionReads)
	if _, err := f.store.DB().Exec(`DELETE FROM account_mfa_proofs WHERE session_hash=?`, actor.SessionTokenHash); err != nil {
		t.Fatal(err)
	}
	response = mfaRequest(t, f, cookie, http.MethodGet, "/api/mfa-admission-probe", "")
	if response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), `"mfa_required"`) {
		t.Fatal("lifecycle MFA denial", response.Code, response.Body)
	}
	response = mfaRequest(t, f, cookie, http.MethodGet, "/api/auth/mfa", "")
	if response.Code != http.StatusOK {
		t.Fatal("lifecycle bootstrap denied", response.Code, response.Body)
	}
}
