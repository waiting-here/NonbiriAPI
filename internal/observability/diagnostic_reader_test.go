package observability

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/upstreamerror"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

type diagnosticActorKey struct{}
type diagnosticTestAuthority struct{ auth *authz.Authorizer }

func (a diagnosticTestAuthority) check(ctx context.Context, tx *sql.Tx, id int64, role authz.Role) error {
	actor, ok := ctx.Value(diagnosticActorKey{}).(authz.Actor)
	if !ok || actor.UserID != id {
		return authz.ErrUnauthorized
	}
	_, err := a.auth.Authorize(ctx, tx, actor, authz.Requirement{Role: role})
	return err
}
func (a diagnosticTestAuthority) AuthorizeAdmin(ctx context.Context, tx *sql.Tx, id int64) error {
	return a.check(ctx, tx, id, authz.RoleAdministrator)
}
func (a diagnosticTestAuthority) AuthorizeStewardRead(ctx context.Context, tx *sql.Tx, id int64) error {
	return a.check(ctx, tx, id, authz.RoleSteward)
}

type diagnosticFixture struct {
	repository                        *Repository
	reader                            *DiagnosticReader
	owner                             int64
	admin, steward, trainee, ordinary authz.Actor
}

func newDiagnosticFixture(t *testing.T) *diagnosticFixture {
	r, owner := newObservedDatabase(t)
	f := &diagnosticFixture{repository: r, owner: owner}
	authority := diagnosticTestAuthority{authz.New(authz.Options{Now: r.now})}
	reader, err := NewDiagnosticReader(DiagnosticReaderConfig{Database: r.db, FinalAuth: authority, Now: func() time.Time { return r.now() }})
	if err != nil {
		t.Fatal(err)
	}
	f.reader = reader
	for _, entry := range []struct {
		name         string
		admin, level int
		target       *authz.Actor
	}{{"admin", 1, 1, &f.admin}, {"steward", 0, 6, &f.steward}, {"trainee", 0, 5, &f.trainee}, {"ordinary", 0, 1, &f.ordinary}} {
		zero := make([]byte, 16)
		result, err := r.db.Exec("INSERT INTO users(discord_id,username,is_admin,level,donation_credit_mag,total_requests,total_uncached_input_tokens,total_cache_write_input_tokens,total_cache_read_input_tokens,total_output_tokens,total_unknown_usage_requests,revision,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)", "diagnostic-"+entry.name, entry.name, entry.admin, entry.level, zero, zero, zero, zero, zero, zero, zero, zero, r.now().Unix(), r.now().Unix())
		if err != nil {
			t.Fatal(err)
		}
		id, err := result.LastInsertId()
		if err != nil {
			t.Fatal(err)
		}
		token := "diagnostic-session-" + entry.name
		_, err = r.db.Exec("INSERT INTO sessions(token_hash,user_id,last_seen_at,expires_at,absolute_expires_at,created_at,cred_gen) VALUES(?,?,?,?,?,?,?)", token, id, r.now().Unix(), r.now().Unix()+86400, r.now().Unix()+86400, r.now().Unix(), "g1")
		if err != nil {
			t.Fatal(err)
		}
		kind := authz.ActorUserSession
		if entry.admin == 1 {
			kind = authz.ActorAdminSession
		}
		*entry.target = authz.Actor{Kind: kind, UserID: id, SessionTokenHash: token, SessionGeneration: "g1"}
	}
	return f
}
func diagContext(actor authz.Actor) context.Context {
	return context.WithValue(context.Background(), diagnosticActorKey{}, actor)
}
func diagActor(actor authz.Actor) DiagnosticActor {
	return DiagnosticActor{UserID: actor.UserID, Admin: actor.Kind == authz.ActorAdminSession}
}
func diagnosticID(t *testing.T, prefix string) string {
	t.Helper()
	id, err := db.GenerateOpaqueID(prefix)
	if err != nil {
		t.Fatal(err)
	}
	return id
}
func (f *diagnosticFixture) discovery(t *testing.T, at int64) string {
	id := diagnosticID(t, "req_")
	_, err := f.repository.db.Exec("INSERT INTO logical_requests(id,user_id,route_kind,state,attempt_limit,caller_result_class,caller_status,accounting_state,settlement_destination,ledger_rows_remaining,created_at,terminal_at) VALUES(?,?,'model_discovery','terminal',1,'failed',503,'none','user',zeroblob(16),?,?)", id, f.owner, at, at)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.repository.db.Exec("INSERT INTO request_logs(logical_request_id,user_id,route_kind,started_at,completed_at,caller_result_class,caller_status,status_code) VALUES(?,?,'model_discovery',?,?,'failed',503,503)", id, f.owner, at, at)
	if err != nil {
		t.Fatal(err)
	}
	return id
}
func (f *diagnosticFixture) capture(t *testing.T, ref DiagnosticRef, mime string, body []byte) string {
	t.Helper()
	upstreamerror.CaptureEvent(f.repository.ErrorScope(context.Background(), ref), 503, mime, body)
	var id int64
	if err := f.repository.db.QueryRow("SELECT max(id) FROM request_error_bodies").Scan(&id); err != nil {
		t.Fatal(err)
	}
	return strconv.FormatInt(id, 10)
}
func (f *diagnosticFixture) imageRoots(t *testing.T) (string, string) {
	t.Helper()
	r := f.repository
	now := r.now().Unix()
	control := diagnosticID(t, "iup_")
	model := diagnosticID(t, "imdl_")
	task := diagnosticID(t, "img_")
	operation := diagnosticID(t, "op_")
	statements := []struct {
		q    string
		args []any
	}{
		{"INSERT INTO image_upstream_control(id,identity_hash,rpm_limit,concurrency_limit,protection_paused,protection_reason,protection_revision,updated_at) VALUES(?,randomblob(32),60,1,0,'',1,?)", []any{control, now}},
		{"INSERT INTO image_upstream_revisions(revision,control_id,base_url,secret_context,secret_ciphertext,adapter_json,image_origins_json,per_user_limit,global_limit,queue_timeout_seconds,execution_timeout_seconds,memory_budget_mib,created_at) VALUES(1,?,'https://fixture.invalid',zeroblob(16),'fixture','{}','[]',1,100,60,60,512,?)", []any{control, now}},
		{"INSERT INTO image_activity_models(id,control_id,upstream_model_id,metadata_json,discovered_at) VALUES(?,?,'fixture-model','{}',?)", []any{model, control, now}},
		{"INSERT INTO image_model_revisions(model_id,revision,display_name,description,enabled,parameters_json,combinations_json,mapping_json,paper_price_mag,brush_price_mag,created_at) VALUES(?,1,'Fixture','',1,'[]','[]','{}',X'000000000000000000000000000003E8',zeroblob(16),?)", []any{model, now}},
		{"INSERT INTO image_activity_tasks(id,user_id,model_id,model_revision,n,paper_charge_mag,brush_charge_mag,state,finance_state,slot_state,ledger_rows_remaining,created_at,updated_at,queue_deadline,execution_timeout_seconds,completed_at) VALUES(?,?,?,1,1,X'000000000000000000000000000003E8',zeroblob(16),'failed','refunded','none',zeroblob(16),?,?,?,?,?)", []any{task, f.owner, model, now, now, now + 60, 60, now}},
		{"INSERT INTO accepted_operations(id,kind,actor_user_id,actor_role,payload_hash,state,created_at,terminal_at) VALUES(?,'image_model_discovery',?,'admin',zeroblob(32),'completed',?,?)", []any{operation, f.admin.UserID, now, now}},
		{"INSERT INTO image_model_refreshes(operation_id,upstream_revision,control_id,state,created_at,deadline,completed_at) VALUES(?,1,?,'failed',?,?,?)", []any{operation, control, now, now + 60, now}},
	}
	for _, s := range statements {
		if _, err := r.db.Exec(s.q, s.args...); err != nil {
			t.Fatal(err)
		}
	}
	return task, operation
}
func TestIndependentDiagnosticsFinalAuthorityAndBoundedPagination(t *testing.T) {
	f := newDiagnosticFixture(t)
	now := f.repository.now().Unix()
	request := f.discovery(t, now)
	for i := 1; i <= 23; i++ {
		f.capture(t, DiagnosticRef{RequestID: request, AttemptSeq: 1, EventSeq: i - 1}, "text/plain", []byte("private diagnostic"))
	}
	ordinary, _ := observedLog(t, f.repository, f.owner, now)
	f.capture(t, DiagnosticRef{RequestID: ordinary, AttemptSeq: 1}, "text/plain", []byte("normal API diagnostic"))
	for _, actor := range []authz.Actor{f.admin, f.steward} {
		page, err := f.reader.List(diagContext(actor), diagActor(actor), DiagnosticFilter{})
		if err != nil || len(page.Data) != 20 || page.NextBefore == nil {
			t.Fatalf("list %d %v", len(page.Data), err)
		}
		next, err := f.reader.List(diagContext(actor), diagActor(actor), DiagnosticFilter{Before: *page.NextBefore})
		if err != nil || len(next.Data) != 3 || next.NextBefore != nil {
			t.Fatalf("page2 %+v %v", next, err)
		}
		if next.Data[0].SubjectID != request {
			t.Fatal("foreign diagnostic category")
		}
		detail, err := f.reader.Detail(diagContext(actor), diagActor(actor), page.Data[0].ID)
		if err != nil || detail.Body.Body != "private diagnostic" {
			t.Fatalf("detail %+v %v", detail, err)
		}
	}
	for _, actor := range []authz.Actor{f.trainee, f.ordinary} {
		if _, err := f.reader.List(diagContext(actor), diagActor(actor), DiagnosticFilter{}); !errors.Is(err, ErrDiagnosticForbidden) {
			t.Fatalf("role allowed %v", err)
		}
	}
	page, _ := f.reader.List(diagContext(f.steward), diagActor(f.steward), DiagnosticFilter{})
	if _, err := f.repository.db.Exec("UPDATE users SET level=5 WHERE id=?", f.steward.UserID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.reader.Detail(diagContext(f.steward), diagActor(f.steward), page.Data[0].ID); !errors.Is(err, ErrDiagnosticForbidden) {
		t.Fatalf("stale role accepted %v", err)
	}
	if _, err := f.repository.db.Exec("UPDATE sessions SET cred_gen='g2' WHERE token_hash=?", f.admin.SessionTokenHash); err != nil {
		t.Fatal(err)
	}
	if _, err := f.reader.Detail(diagContext(f.admin), diagActor(f.admin), page.Data[0].ID); !errors.Is(err, ErrDiagnosticForbidden) {
		t.Fatalf("stale session accepted %v", err)
	}
}
func TestIndependentDiagnosticsImageRootsExpiryAndAnonymization(t *testing.T) {
	f := newDiagnosticFixture(t)
	task, operation := f.imageRoots(t)
	now := f.repository.now().Unix()
	body := []byte("{\"category\":\"image_response_omitted\",\"reason\":\"invalid_response\",\"original_body_saved\":false}")
	taskEvent := f.capture(t, DiagnosticRef{TaskID: task, AttemptSeq: 7}, imageDiagnosticMIME, body)
	operationEvent := f.capture(t, DiagnosticRef{OperationID: operation, AttemptSeq: 1}, "application/json", []byte("{\"error\":\"discovery\"}"))
	if _, err := f.repository.db.Exec("INSERT INTO image_task_sources(task_id,user_id,effective_ip,ip_quality,source_json,occurred_at) VALUES(?,?,'192.0.2.17','direct_peer',?,?)", task, f.owner, `{"effective_ip":"192.0.2.17","ip_quality":"direct_peer","user_agent":"fixture-client"}`, now); err != nil {
		t.Fatal(err)
	}
	result, err := f.reader.Detail(diagContext(f.admin), diagActor(f.admin), taskEvent)
	if err != nil || !result.Item.Synthetic || result.Item.AttemptSeq != 7 || result.Source == nil || result.Source.UserAgent != "fixture-client" {
		t.Fatalf("task %+v %v", result, err)
	}
	if _, err := f.repository.db.Exec("UPDATE image_task_sources SET user_id=? WHERE task_id=?", f.steward.UserID, task); err != nil {
		t.Fatal(err)
	}
	isolated, err := f.reader.Detail(diagContext(f.admin), diagActor(f.admin), taskEvent)
	if err != nil || isolated.Source != nil {
		t.Fatalf("foreign source association exposed %+v %v", isolated.Source, err)
	}
	orphan := diagnosticID(t, "op_")
	if _, err := f.repository.db.Exec("INSERT INTO accepted_operations(id,kind,actor_user_id,actor_role,payload_hash,state,created_at,terminal_at) VALUES(?,'image_model_discovery',?,'admin',zeroblob(32),'completed',?,?)", orphan, f.admin.UserID, now, now); err != nil {
		t.Fatal(err)
	}
	orphanEvent := f.capture(t, DiagnosticRef{OperationID: orphan, AttemptSeq: 1}, "text/plain", []byte("unrelated operation"))
	if _, err := f.reader.Detail(diagContext(f.admin), diagActor(f.admin), orphanEvent); !errors.Is(err, ErrDiagnosticNotFound) {
		t.Fatalf("operation without refresh root exposed %v", err)
	}
	page, err := f.reader.List(diagContext(f.steward), diagActor(f.steward), DiagnosticFilter{Kind: "image_discovery", SubjectID: operation})
	if err != nil || len(page.Data) != 1 || page.Data[0].ID != operationEvent {
		t.Fatalf("operation %+v %v", page, err)
	}
	// Even if body cleanup lags, null owners and missing operation roots cannot reopen data.
	if _, err = f.repository.db.Exec("UPDATE image_activity_tasks SET user_id=NULL,finance_state='deleted',model_id=NULL,model_revision=NULL,n=NULL,paper_charge_mag=NULL,brush_charge_mag=NULL WHERE id=?", task); err != nil {
		t.Fatal(err)
	}
	if _, err = f.reader.Detail(diagContext(f.admin), diagActor(f.admin), taskEvent); !errors.Is(err, ErrDiagnosticNotFound) {
		t.Fatalf("anonymous task visible %v", err)
	}
	if _, err = f.repository.db.Exec("UPDATE accepted_operations SET actor_user_id=NULL WHERE id=?", operation); err != nil {
		t.Fatal(err)
	}
	if _, err = f.reader.Detail(diagContext(f.admin), diagActor(f.admin), operationEvent); !errors.Is(err, ErrDiagnosticNotFound) {
		t.Fatalf("anonymous operation visible %v", err)
	}
	oldRequest := f.discovery(t, now-RetentionSeconds-1)
	oldRoot := f.capture(t, DiagnosticRef{RequestID: oldRequest, AttemptSeq: 1}, "text/plain", []byte("old aggregate"))
	if _, err := f.reader.Detail(diagContext(f.admin), diagActor(f.admin), oldRoot); !errors.Is(err, ErrDiagnosticNotFound) {
		t.Fatalf("expired aggregate reopened %v", err)
	}
	request := f.discovery(t, now)
	expired := f.capture(t, DiagnosticRef{RequestID: request, AttemptSeq: 1}, "text/plain", []byte("expired"))
	if _, err = f.repository.db.Exec("UPDATE request_error_bodies SET expires_at=? WHERE id=?", now, expired); err != nil {
		t.Fatal(err)
	}
	if _, err = f.reader.Detail(diagContext(f.admin), diagActor(f.admin), expired); !errors.Is(err, ErrDiagnosticNotFound) {
		t.Fatalf("expiry visible %v", err)
	}
}
func TestIndependentDiagnosticHTTPValidationAndBodySafety(t *testing.T) {
	f := newDiagnosticFixture(t)
	request := f.discovery(t, f.repository.now().Unix())
	id := f.capture(t, DiagnosticRef{RequestID: request, AttemptSeq: 1}, "text/html", []byte("<script>untrusted</script>"))
	req := httptest.NewRequest("GET", "/admin/api/diagnostics/"+id, nil).WithContext(diagContext(f.admin))
	req.SetPathValue("id", id)
	res := httptest.NewRecorder()
	f.reader.serve(res, req, diagActor(f.admin))
	var result DiagnosticDetail
	if res.Code != 200 || json.Unmarshal(res.Body.Bytes(), &result) != nil || result.Body.Body != "<script>untrusted</script>" || res.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("HTTP body %d %s", res.Code, res.Body.String())
	}
	for _, query := range []string{"page_size=21", "before=-1", "before=01", "before=9223372036854775808", "kind=foreign", "kind=all&kind=image_task", "extra=1", "from=1&to=3000000", "subject_id=not-a-root"} {
		req = httptest.NewRequest("GET", "/admin/api/diagnostics?"+query, nil).WithContext(diagContext(f.admin))
		res = httptest.NewRecorder()
		f.reader.serve(res, req, diagActor(f.admin))
		if res.Code != 400 {
			t.Fatalf("query %s got%d", query, res.Code)
		}
	}
	req = httptest.NewRequest("GET", "/admin/api/diagnostics", strings.NewReader("body")).WithContext(diagContext(f.admin))
	res = httptest.NewRecorder()
	f.reader.serve(res, req, diagActor(f.admin))
	if res.Code != 400 {
		t.Fatal("GET body accepted")
	}
	ctx := authz.WithStewardCaller(diagContext(f.steward), authz.StewardCaller{UserID: f.steward.UserID})
	if _, err := f.reader.List(ctx, diagActor(f.steward), DiagnosticFilter{}); !errors.Is(err, ErrDiagnosticForbidden) {
		t.Fatalf("CallerKey allowed %v", err)
	}
	for _, state := range []string{"capacity_exhausted", "unavailable"} {
		_, err := f.repository.db.Exec("UPDATE request_error_bodies SET save_state=?,body=NULL,bytes_saved=0 WHERE id=?", state, id)
		if err != nil {
			t.Fatal(err)
		}
		detail, err := f.reader.Detail(diagContext(f.admin), diagActor(f.admin), id)
		if err != nil || detail.Body.SaveState != state || detail.Body.Body != "" {
			t.Fatal(fmt.Sprint(state, err))
		}
	}
}
