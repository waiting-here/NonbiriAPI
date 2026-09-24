package riskaudit

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/dbfixture"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

type auditFixture struct {
	t          *testing.T
	store      *db.Store
	repository *Repository
	now        int64
	admin      int64
	authority  *testFinalAuth
}

// The fixture supplies the entry snapshot explicitly; the real authorizer
// still reads sessions, generations, bans and roles from the final SQL tx.
type testFinalAuth struct {
	authorizer *authz.Authorizer
	actors     map[int64]authz.Actor
}

func (a *testFinalAuth) AuthorizeAdmin(ctx context.Context, tx *sql.Tx, id int64) error {
	_, err := a.authorizer.Authorize(ctx, tx, a.actors[id], authz.Requirement{Role: authz.RoleAdministrator})
	return err
}
func (a *testFinalAuth) AuthorizeStewardMutation(ctx context.Context, tx *sql.Tx, id int64) error {
	_, err := a.authorizer.Authorize(ctx, tx, a.actors[id], authz.Requirement{Role: authz.RoleSteward})
	return err
}

type auditAdminRoutes struct{ handlers map[string]http.Handler }

func (r *auditAdminRoutes) RegisterAdminRoute(method, path string, h http.Handler) error {
	r.handlers[method+" "+path] = h
	return nil
}

func newAuditFixture(t *testing.T) *auditFixture {
	t.Helper()
	vault, err := secret.New(make([]byte, secret.MasterKeyBytes))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = vault.Close() })
	path := filepath.Join(t.TempDir(), "risk.db")
	dbfixture.Materialize(t, path)
	store, err := db.Open(path, vault)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	f := &auditFixture{t: t, store: store, now: 1790000640}
	f.authority = &testFinalAuth{authorizer: authz.New(authz.Options{Now: func() time.Time { return time.Unix(f.now, 0) }}), actors: make(map[int64]authz.Actor)}
	f.admin = f.user(0)
	actor := f.authority.actors[f.admin]
	actor.Kind = authz.ActorAdminSession
	f.authority.actors[f.admin] = actor
	f.repository, err = NewRepository(store.DB(), RepositoryOptions{Now: func() time.Time { return time.Unix(f.now, 0) }, FinalAuth: f.authority})
	if err != nil {
		t.Fatal(err)
	}
	return f
}
func (f *auditFixture) exec(query string, args ...any) {
	f.t.Helper()
	if _, err := f.store.DB().Exec(query, args...); err != nil {
		f.t.Fatal(err)
	}
}
func (f *auditFixture) user(level int) int64 {
	f.t.Helper()
	admin := 0
	if level == 0 {
		level = 1
		admin = 1
	}
	zero := db.EncodeU128(db.U128{})
	result, err := f.store.DB().Exec(`INSERT INTO users(username,is_admin,level,donation_credit_mag,total_requests,total_uncached_input_tokens,total_cache_write_input_tokens,total_cache_read_input_tokens,total_output_tokens,total_unknown_usage_requests,revision,created_at,updated_at) VALUES('Audit fixture',?,?,?,?,?,?,?,?,?,?,?,?)`, admin, level, zero, zero, zero, zero, zero, zero, zero, zero, f.now, f.now)
	if err != nil {
		f.t.Fatal(err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		f.t.Fatal(err)
	}
	token := fmt.Sprintf("audit-session-%d", id)
	generation := "g1"
	f.exec(`INSERT INTO sessions(token_hash,user_id,last_seen_at,expires_at,absolute_expires_at,created_at,cred_gen) VALUES(?,?,?,?,?,?,?)`, token, id, f.now, f.now+600, f.now+1200, f.now, generation)
	f.authority.actors[id] = authz.Actor{Kind: authz.ActorUserSession, UserID: id, SessionTokenHash: token, SessionGeneration: generation}
	return id
}
func (f *auditFixture) source(user int64, kind, ip, quality, ua, model, outcome string, attempts int) int64 {
	f.t.Helper()
	id, err := db.GenerateOpaqueID("req_")
	if err != nil {
		f.t.Fatal(err)
	}
	var status, code any
	statusValue := 0
	errorValue := ""
	if outcome == "failed" {
		status = 429
		statusValue = 429
		code = "rate_limited"
		errorValue = "rate_limited"
	} else if outcome == "success" {
		status = 200
		statusValue = 200
	}
	zero := db.EncodeU128(db.U128{})
	at := f.now - 300
	f.exec(`INSERT INTO logical_requests(id,user_id,route_kind,model_snapshot,state,attempt_limit,caller_result_class,caller_status,caller_error_code,accounting_state,settlement_destination,ledger_rows_remaining,created_at,terminal_at) VALUES(?,?,'openai_chat_completions',?,'terminal',1,?,?,?,'none','user',?,?,?)`, id, user, model, outcome, status, code, zero, at, at+125)
	f.exec(`INSERT INTO request_logs(logical_request_id,user_id,model,route_kind,caller_result_class,caller_status,caller_error_code,status_code,error_code,duration_ms,attempt_count,started_at,completed_at) VALUES(?,?,?,'openai_chat_completions',?,?,?,?,?,125000,?,?,?)`, id, user, model, outcome, status, code, statusValue, errorValue, attempts, at, at+125)
	var logID int64
	if f.store.DB().QueryRow(`SELECT id FROM request_logs WHERE logical_request_id=?`, id).Scan(&logID) != nil {
		f.t.Fatal("log lookup")
	}
	raw, _ := json.Marshal(Source{EffectiveIP: ip, IPQuality: quality, UserAgent: ua})
	f.exec(`INSERT INTO request_source_facts(request_log_id,user_id,kind,effective_ip,ip_quality,source_json,occurred_at) VALUES(?,?,?,?,?,?,?)`, logID, user, kind, ip, quality, string(raw), at)
	if attempts > 0 {
		claimID, err := db.GenerateOpaqueID("clm_")
		if err != nil {
			f.t.Fatal(err)
		}
		var dispatched any
		state := "released"
		if outcome != "failed" {
			dispatched = at
			state = "committed"
		}
		f.exec(`INSERT INTO dispatch_claims(id,logical_request_id,attempt_seq,purpose,claim_now,state,dispatched_at,terminal_at) VALUES(?,?,1,'self',?,?,?,?)`, claimID, id, at, state, dispatched, at+125)
	}
	return logID
}
func TestRepositoryAuthoritativeSchemaPermissionsRulesAndSources(t *testing.T) {
	f := newAuditFixture(t)
	ctx := context.Background()
	admin := Actor{Admin: true, UserID: f.admin}
	steward := Actor{UserID: f.user(6)}
	trainee := Actor{UserID: f.user(5)}
	ordinary := Actor{UserID: f.user(1)}
	if _, err := f.repository.GetConfig(ctx, Actor{Admin: true, UserID: ordinary.UserID}); !errors.Is(err, ErrForbidden) {
		t.Fatal("administrator flag bypassed final authority")
	}
	for _, actor := range []Actor{trainee, ordinary, {}} {
		if _, err := f.repository.GetConfig(ctx, actor); !errors.Is(err, ErrForbidden) {
			t.Fatalf("unprivileged actor: %v", err)
		}
	}
	config, err := f.repository.GetConfig(ctx, steward)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.repository.UpdateConfig(ctx, steward, config); !errors.Is(err, ErrForbidden) {
		t.Fatal("steward changed thresholds")
	}
	rule, err := f.repository.PutRule(ctx, steward, Rule{Name: "Example application", Status: "suspected", Enabled: true, Conditions: []Condition{{Field: "user_agent", Operator: "prefix", Value: "ExampleClient/"}}, EvidenceNote: "Self-reported identifier"}, true)
	if err != nil {
		t.Fatal(err)
	}
	rule.Enabled = false
	updated, err := f.repository.PutRule(ctx, admin, rule, false)
	if err != nil || updated.Revision != 2 {
		t.Fatalf("update: %+v %v", updated, err)
	}
	if _, err = f.repository.PutRule(ctx, admin, rule, false); !errors.Is(err, ErrConflict) {
		t.Fatal("stale revision accepted")
	}
	updated.Enabled = true
	updated, err = f.repository.PutRule(ctx, steward, updated, false)
	if err != nil {
		t.Fatal(err)
	}
	third := f.user(1)
	ip := "192.0.2.11"
	f.source(ordinary.UserID, "self", ip, "direct_peer", "ExampleClient/1", "example-model", "cancelled", 1)
	f.source(trainee.UserID, "charity", ip, "trusted_forwarded", "Go-http-client/1.1", "example-model", "failed", 1)
	f.source(third, "unclassified", ip, "direct_peer", "", "example-model", "success", 1)
	for _, user := range []int64{ordinary.UserID, trainee.UserID, third} {
		f.source(user, "self", "192.0.2.99", "peer_fallback", "GenericSDK", "example-model", "success", 1)
	}
	window := Window{From: f.now - 600, To: f.now, Limit: 100}
	ips, err := f.repository.SharedIPs(ctx, steward, window, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(ips.Items) != 1 || ips.Items[0].Users != 3 || ips.Items[0].Requests != 3 {
		t.Fatalf("IP associations: %+v", ips)
	}
	var dispatched, rejected int64
	for _, a := range ips.Items[0].Associations {
		dispatched += a.Dispatched
		rejected += a.Rejected
	}
	if dispatched != 2 || rejected != 1 {
		t.Fatalf("dispatch/refusal distinction: %d/%d", dispatched, rejected)
	}
	matches, err := f.repository.ClientMatches(ctx, steward, window)
	if err != nil || len(matches.Items) != 1 || matches.Items[0].UserID != ordinary.UserID {
		t.Fatalf("matches %+v %v", matches, err)
	}
	detail, err := f.repository.User(ctx, steward, ordinary.UserID, window)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Comparison.Others.Samples != 4 || detail.Comparison.User.Cancellations[3].Count != 1 {
		t.Fatalf("bounded comparison %+v", detail.Comparison)
	}
	if _, err = f.repository.Users(ctx, steward, Window{From: 1, To: f.now}); !errors.Is(err, ErrInvalid) {
		t.Fatal("unbounded range accepted")
	}
	window.Limit = 1
	first, err := f.repository.ClientMatches(ctx, steward, window)
	if err != nil || !first.HasMore || first.Scanned != 1 {
		t.Fatalf("paged recomputation %+v %v", first, err)
	}
	f.exec(`UPDATE users SET level=5 WHERE id=?`, steward.UserID)
	if _, err = f.repository.Rules(ctx, steward); !errors.Is(err, ErrForbidden) {
		t.Fatal("demoted steward retained access")
	}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest("GET", "/users?limit=101", nil)
	serve(f.repository, "users", admin, recorder, request)
	if recorder.Code != 400 {
		t.Fatalf("unbounded HTTP page: %d", recorder.Code)
	}
	recorder = httptest.NewRecorder()
	request = httptest.NewRequest("GET", "/users?from=1&from=2", nil)
	serve(f.repository, "users", admin, recorder, request)
	if recorder.Code != 400 {
		t.Fatal("duplicate parameter accepted")
	}
	recorder = httptest.NewRecorder()
	request = httptest.NewRequest("POST", "/client-rules", strings.NewReader(`{"name":"x","status":"suspected","enabled":true,"conditions":[],"created_by_role":"admin"}`))
	serve(f.repository, "create_rule", admin, recorder, request)
	if recorder.Code != 400 {
		t.Fatal("actor spoofing accepted")
	}
	f.exec(`UPDATE users SET level=6 WHERE id=?`, steward.UserID)
	f.exec(`UPDATE sessions SET cred_gen='replaced' WHERE user_id=?`, steward.UserID)
	if _, err = f.repository.Rules(ctx, steward); !errors.Is(err, ErrForbidden) {
		t.Fatal("replaced session retained read access")
	}
	if _, err = f.repository.PutRule(ctx, steward, updated, false); !errors.Is(err, ErrForbidden) {
		t.Fatal("replaced session retained write access")
	}
	f.exec(`DELETE FROM sessions WHERE user_id=?`, admin.UserID)
	if _, err = f.repository.UpdateConfig(ctx, admin, config); !errors.Is(err, ErrForbidden) {
		t.Fatal("revoked administrator changed policy")
	}
	current, err := f.repository.CurrentConfig(ctx)
	if err != nil || current.Revision != config.Revision {
		t.Fatal("unauthorized config commit")
	}
	if _, err = NewRepository(f.store.DB(), RepositoryOptions{}); !errors.Is(err, ErrInvalid) {
		t.Fatal("missing final authorizer accepted")
	}
	routes := &auditAdminRoutes{handlers: make(map[string]http.Handler)}
	if err = RegisterAdminRoutes(routes, f.repository); err != nil {
		t.Fatal(err)
	}
	recorder = httptest.NewRecorder()
	routes.handlers["GET /admin/api/abuse-audit/config"].ServeHTTP(recorder, httptest.NewRequest("GET", "/admin/api/abuse-audit/config", nil))
	if recorder.Code != 403 {
		t.Fatal("missing request actor accepted")
	}
}
func TestRepositoryMinutePersistenceDeletionRetentionAndWindowEdges(t *testing.T) {
	f := newAuditFixture(t)
	ctx := context.Background()
	user := f.user(1)
	start := f.now - 600
	values := make([]Minute, 5)
	for i := range values {
		values[i] = Minute{Epoch: "test", UserID: user, Minute: start + int64(i)*60, Kind: "total", RPMCommitted: 8, RPMLimit: 10, ConcurrencyLimit: 2, OccupancyMillis: 96000, Peak: 2, ConfigRevision: 1, UpdatedAt: start + int64(i+1)*60}
	}
	if err := f.repository.SaveMinutes(ctx, values, nil); err != nil {
		t.Fatal(err)
	}
	page, err := f.repository.Users(ctx, Actor{Admin: true, UserID: f.admin}, Window{From: start, To: start + 300})
	if err != nil || len(page.Items) != 1 || !page.Items[0].RPMRisk {
		t.Fatalf("complete run %+v %v", page, err)
	}
	page, err = f.repository.Users(ctx, Actor{Admin: true, UserID: f.admin}, Window{From: start + 1, To: start + 299})
	if err != nil || page.Items[0].RPMRisk {
		t.Fatal("partial query boundaries created a complete run")
	}
	f.exec(`DELETE FROM users WHERE id=?`, user)
	if err = f.repository.SaveMinutes(ctx, values, nil); err != nil {
		t.Fatal(err)
	}
	var count int
	f.store.DB().QueryRow(`SELECT COUNT(*) FROM risk_audit_minutes WHERE user_id=?`, user).Scan(&count)
	if count != 0 {
		t.Fatal("deleted user resurrected")
	}
	if err = f.repository.Cleanup(ctx, time.Unix(f.now, 0), 101); !errors.Is(err, ErrInvalid) {
		t.Fatal("cleanup exceeds frozen batch bound")
	}
	user = f.user(1)
	old := (f.now-int64(Retention/time.Second))/60*60 - 120
	values = make([]Minute, 60)
	gaps := make([]Gap, 60)
	for i := range values {
		minute := old - int64(i)*60
		values[i] = Minute{Epoch: "retention", UserID: user, Minute: minute, Kind: "total", ConfigRevision: 1, UpdatedAt: minute}
		gaps[i] = Gap{Epoch: "retention", Minute: minute, Reason: "restart"}
	}
	if err = f.repository.SaveMinutes(ctx, values, gaps); err != nil {
		t.Fatal(err)
	}
	batch, err := f.repository.CleanupBatch(ctx, time.Unix(f.now, 0), 100)
	if err != nil || batch.Processed != 100 || batch.Deleted != 100 || !batch.More {
		t.Fatalf("shared cleanup budget: %+v %v", batch, err)
	}
	batch, err = f.repository.CleanupBatch(ctx, time.Unix(f.now, 0), 100)
	if err != nil || batch.Processed != 20 || batch.Deleted != 20 || batch.More {
		t.Fatalf("remaining cleanup budget: %+v %v", batch, err)
	}
}
