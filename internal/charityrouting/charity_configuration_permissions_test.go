package charityrouting

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/agentplan"
	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/managementlog"
	"github.com/waiting-here/NonbiriAPI/internal/pagination"
	"github.com/waiting-here/NonbiriAPI/internal/requestadaptation"
)

func saveCharityPolicy(t *testing.T, e *routingTestEnv, actor int64, p authz.StewardPermissions) {
	t.Helper()
	grants, _ := json.Marshal(p.Permissions)
	limits, _ := json.Marshal(p.Limits)
	presetExec(t, e, `INSERT INTO steward_permissions(user_id,revision,permissions,limits,donation_scope,created_at,updated_at) VALUES(?,1,?,?,?,?,?) ON CONFLICT(user_id) DO UPDATE SET permissions=excluded.permissions,donation_scope=excluded.donation_scope`, actor, string(grants), string(limits), p.DonationScope, routingTestNow, routingTestNow)
}
func TestAffinityPermissionIsIndependentAndLive(t *testing.T) {
	e := newRoutingTestEnv(t)
	e.seedUser(t, true, nil)
	five := int64(5)
	actor := e.seedUser(t, false, &five)
	model := e.createModel(t, 'a')
	mid, _ := parsePositiveID(model.ID)
	presetExec(t, e, "UPDATE charity_models SET is_mainstream=1 WHERE id=?", mid)
	p := authz.DefaultStewardPermissions(5)
	p.Permissions["charity.affinity"] = true
	p.Permissions["charity.model_settings"] = false
	p.Permissions["charity.read"] = false
	saveCharityPolicy(t, e, actor, p)
	ttl := 600
	patch := ModelPatch{ExpectedRevision: "1", AffinityTTLSeconds: &ttl}
	ctx := authz.WithStewardPermission(context.Background(), "model_fields")
	mutation := routingMutation(t, 'b', http.MethodPatch, routeStewardModel, []int64{mid}, patch)
	made, err := e.service.PatchSteward(ctx, actor, mid, mutation, patch)
	if err != nil || made.Value.AffinityTTLSeconds != ttl {
		t.Fatal(made, err)
	}
	repeated, err := e.service.PatchSteward(ctx, actor, mid, mutation, patch)
	if err != nil || !repeated.Replayed {
		t.Fatal("replay", err)
	}
	patch.ExpectedRevision = made.Value.Revision
	patch.Enabled = new(false)
	if _, err := e.service.PatchSteward(ctx, actor, mid, routingMutation(t, 'c', http.MethodPatch, routeStewardModel, []int64{mid}, patch), patch); !errors.Is(err, authz.ErrPermissionDenied) {
		t.Fatal("mixed settings", err)
	}
	p.Permissions["charity.affinity"] = false
	p.Permissions["charity.model_settings"] = true
	saveCharityPolicy(t, e, actor, p)
	if _, err := e.service.PatchSteward(ctx, actor, mid, mutation, ModelPatch{ExpectedRevision: "1", AffinityTTLSeconds: &ttl}); !errors.Is(err, authz.ErrPermissionDenied) {
		t.Fatal("revoked replay", err)
	}
	p.Permissions["charity.create"] = true
	saveCharityPolicy(t, e, actor, p)
	input := testModelCreate()
	input.IsMainstream = true
	input.AffinityTTLSeconds = &ttl
	if _, err := e.service.CreateSteward(authz.WithStewardPermission(context.Background(), "charity.create"), actor, routingMutation(t, 'd', http.MethodPost, routeStewardModels, nil, input), input); !errors.Is(err, authz.ErrPermissionDenied) {
		t.Fatal("create bypass", err)
	}
}

func TestAdaptationStewardWritesCheckScopeAndPermissionBeforeReplay(t *testing.T) {
	e := newRoutingTestEnv(t)
	e.seedUser(t, true, nil)
	five, six := int64(5), int64(6)
	actor := e.seedUser(t, false, &five)
	senior := e.seedUser(t, false, &six)
	owner := e.seedUser(t, false, nil)
	_, keyID, _ := e.seedCandidate(t, owner, 'k', "upstream")
	model := e.createModel(t, 'e')
	mid := catalogBind(t, e, model, keyID, 'f')
	presetExec(t, e, "UPDATE charity_models SET is_mainstream=1 WHERE id=?", mid)
	var bid int64
	if err := e.store.DB().QueryRow("SELECT id FROM charity_model_bindings WHERE charity_model_id=?", mid).Scan(&bid); err != nil {
		t.Fatal(err)
	}
	var err error
	e.service.adaptations, err = requestadaptation.New(requestadaptation.Config{DB: e.store.DB(), Codec: e.vault, KeyDeriver: e.vault})
	if err != nil {
		t.Fatal(err)
	}
	p := authz.DefaultStewardPermissions(5)
	p.DonationScope = authz.DonationScopeFull
	p.Permissions["charity.model_settings"] = true
	saveCharityPolicy(t, e, actor, p)
	ctx := context.Background()
	if _, err := e.service.GetAdaptation(ctx, roleSteward, actor, mid, 0); err != nil {
		t.Fatal("old model read", err)
	}
	if _, err := e.service.GetAdaptation(ctx, roleSteward, actor, mid, bid); err != nil {
		t.Fatal("old visible binding read", err)
	}
	p.DonationScope = authz.DonationScopeNone
	saveCharityPolicy(t, e, actor, p)
	if _, err := e.service.GetAdaptation(ctx, roleSteward, actor, mid, bid); err == nil {
		t.Fatal("old invisible binding read")
	}
	if _, err := e.service.GetAdaptation(ctx, roleSteward, actor, mid, 0); err != nil {
		t.Fatal("model read acquired source gate", err)
	}
	p.DonationScope = authz.DonationScopeFull
	saveCharityPolicy(t, e, actor, p)
	patch, err := requestadaptation.ParsePatch([]byte(`{"expected_revision":"0","fixed_headers":{"mode":"replace","values":{"X-Protected":{"action":"replace","value":"private-adaptation-token","sensitive_paths":[""]}}}}`))
	if err != nil {
		t.Fatal(err)
	}
	mutation := routingMutation(t, 'g', http.MethodPut, routeStewardBindingAdaptation, []int64{mid, bid}, map[string]any{"change": true})
	if _, err := e.service.putAdaptation(ctx, roleSteward, actor, mid, bid, mutation, patch); !errors.Is(err, authz.ErrPermissionDenied) {
		t.Fatal("old read became write", err)
	}
	p.Permissions["charity.model_settings"] = false
	p.Permissions["charity.request_adaptation"] = true
	saveCharityPolicy(t, e, actor, p)
	for attempt := range 2 {
		got, err := e.service.putAdaptation(ctx, roleSteward, actor, mid, bid, mutation, patch)
		if err != nil || got.Replayed != (attempt == 1) || strings.Contains(string(got.Body), "private-adaptation-token") {
			t.Fatal("save/replay", got, err)
		}
	}
	got, err := e.service.GetAdaptation(ctx, roleSteward, actor, mid, bid)
	body, _ := json.Marshal(got)
	if err != nil || strings.Contains(string(body), "private-adaptation-token") {
		t.Fatal("configuration exposed protected value", string(body), err)
	}
	var role string
	if err := e.store.DB().QueryRow("SELECT actor_role FROM request_adaptation_audits WHERE scope='binding' AND resource_id=?", bid).Scan(&role); err != nil || role != "trainee5" {
		t.Fatal("actor role", role, err)
	}
	p.Permissions["charity.request_adaptation"] = false
	saveCharityPolicy(t, e, actor, p)
	if _, err := e.service.putAdaptation(ctx, roleSteward, actor, mid, bid, mutation, patch); !errors.Is(err, authz.ErrPermissionDenied) {
		t.Fatal("revoked replay", err)
	}
	p.Permissions["charity.request_adaptation"] = true
	p.DonationScope = authz.DonationScopeNone
	saveCharityPolicy(t, e, actor, p)
	if _, err := e.service.GetAdaptation(ctx, roleSteward, actor, mid, bid); err == nil {
		t.Fatal("invisible binding adaptation read")
	}
	if _, err := e.service.putAdaptation(ctx, roleSteward, actor, mid, bid, mutation, patch); err == nil {
		t.Fatal("scope replay allowed")
	}
	seniorMutation := routingMutation(t, 'h', http.MethodPut, routeStewardModelAdaptation, []int64{mid}, map[string]any{"change": true})
	if _, err := e.service.putAdaptation(ctx, roleSteward, senior, mid, 0, seniorMutation, patch); err != nil {
		t.Fatal("level6", err)
	}
	if err := e.store.DB().QueryRow("SELECT actor_role FROM request_adaptation_audits WHERE scope='charity_model' AND resource_id=?", mid).Scan(&role); err != nil || role != "level6" {
		t.Fatal(role, err)
	}
}

func TestPriceTemplateManageIncludesReadButReadDoesNotWrite(t *testing.T) {
	e := newRoutingTestEnv(t)
	e.seedUser(t, true, nil)
	five := int64(5)
	actor := e.seedUser(t, false, &five)
	p := authz.DefaultStewardPermissions(5)
	p.Permissions["charity.price_templates.read"] = true
	saveCharityPolicy(t, e, actor, p)
	ctx := context.Background()
	input := templateInput()
	input.Enabled = false
	made, err := e.service.mutatePriceTemplate(ctx, 0, "", routingMutation(t, 'i', http.MethodPost, routeAdminPriceTemplates, nil, input), &input)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := parsePositiveID(made.Value.ID)
	if list, err := e.service.priceTemplates(ctx, roleSteward, actor, "", "enabled", pagination.Default()); err != nil || len(list.Data) != 0 {
		t.Fatal(list, err)
	}
	if _, err := e.service.priceTemplates(ctx, roleSteward, actor, "", "all", pagination.Default()); !errors.Is(err, ErrForbidden) {
		t.Fatal("read all", err)
	}
	if _, err := e.service.priceTemplate(ctx, roleSteward, actor, id); !errors.Is(err, ErrNotFound) {
		t.Fatal("read disabled", err)
	}
	mutation := routingMutation(t, 'j', http.MethodPut, routeStewardPriceTemplate, []int64{id}, input)
	if _, err := e.service.mutatePriceTemplateAs(ctx, roleSteward, actor, id, "1", mutation, &input); !errors.Is(err, authz.ErrPermissionDenied) {
		t.Fatal("read wrote", err)
	}
	p.Permissions["charity.price_templates.read"] = false
	p.Permissions["charity.price_templates.manage"] = true
	saveCharityPolicy(t, e, actor, p)
	if _, err := e.service.priceTemplate(ctx, roleSteward, actor, id); err != nil {
		t.Fatal("manager read", err)
	}
	if list, err := e.service.priceTemplates(ctx, roleSteward, actor, "", "disabled", pagination.Default()); err != nil || len(list.Data) != 1 {
		t.Fatal(list, err)
	}
	for attempt := range 2 {
		result, err := e.service.mutatePriceTemplateAs(ctx, roleSteward, actor, id, "1", mutation, &input)
		if err != nil || result.Replayed != (attempt == 1) {
			t.Fatal(result, err)
		}
	}
	p.Permissions["charity.price_templates.manage"] = false
	saveCharityPolicy(t, e, actor, p)
	if _, err := e.service.mutatePriceTemplateAs(ctx, roleSteward, actor, id, "1", mutation, &input); !errors.Is(err, authz.ErrPermissionDenied) {
		t.Fatal("revoked template replay", err)
	}
}

func TestAffinityExistingAgentModelOperationsUseLiveFieldGrant(t *testing.T) {
	e := newRoutingTestEnv(t)
	e.seedUser(t, true, nil)
	five := int64(5)
	id := e.seedUser(t, false, &five)
	actor := agentplan.Actor{ID: id, Role: "level5"}
	p := authz.DefaultStewardPermissions(5)
	p.Permissions["charity.create"] = true
	p.Permissions["charity.affinity"] = true
	p.Permissions["charity.model_settings"] = false
	saveCharityPolicy(t, e, id, p)
	input := testModelCreate()
	input.IsMainstream = true
	input.AffinityTTLSeconds = new(600)
	tx, err := e.store.DB().BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	op := agentOp(t, "charity.model.create", "", input)
	if _, err := e.service.AgentPreview(t.Context(), tx, actor, op, allowAgent); err != nil {
		t.Fatal(err)
	}
	raw, err := e.service.AgentApply(t.Context(), tx, actor, op, allowAgent)
	if err != nil {
		t.Fatal(err)
	}
	var model StewardCharityModel
	if err := json.Unmarshal(raw, &model); err != nil || model.AffinityTTLSeconds != 600 {
		t.Fatal(model, err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	patch := agentOp(t, "charity.model.update", model.ID, map[string]any{"expected_revision": "1", "affinity_ttl_seconds": 900})
	tx, err = e.store.DB().BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := e.service.AgentPreview(t.Context(), tx, actor, patch, allowAgent); err != nil {
		t.Fatal(err)
	}
	tx.Rollback()
	p.Permissions["charity.affinity"] = false
	saveCharityPolicy(t, e, id, p)
	tx, err = e.store.DB().BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := e.service.AgentApply(t.Context(), tx, actor, patch, allowAgent); !errors.Is(err, authz.ErrPermissionDenied) {
		t.Fatal("revoked approved operation", err)
	}
	if _, err := e.service.AgentPreview(t.Context(), tx, actor, op, allowAgent); !errors.Is(err, authz.ErrPermissionDenied) {
		t.Fatal("create field bypass", err)
	}
}
func TestNewCharityPermissionChangesProduceManagementEvidence(t *testing.T) {
	e := newRoutingTestEnv(t)
	admin := e.seedUser(t, true, nil)
	five := int64(5)
	id := e.seedUser(t, false, &five)
	p := authz.DefaultStewardPermissions(5)
	saveCharityPolicy(t, e, id, p)
	e.store.SetManagementCapture(managementlog.CaptureEventID)
	for _, key := range []string{"charity.request_adaptation", "charity.affinity", "charity.price_templates.manage"} {
		ctx := managementlog.WithOperation(t.Context(), managementlog.Metadata{Source: managementlog.SourceWeb, ExecutorKind: "human", Actor: managementlog.Subject{ID: fmt.Sprint(admin), Name: "Administrator", Role: "admin"}, Domain: "users", Action: "permissions.update"})
		tx, err := e.store.DB().BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		p.Permissions[key] = true
		grants, _ := json.Marshal(p.Permissions)
		if _, err := tx.ExecContext(ctx, "UPDATE steward_permissions SET permissions=?,revision=revision+1 WHERE user_id=?", string(grants), id); err != nil {
			tx.Rollback()
			t.Fatal(err)
		}
		if err := managementlog.Commit(ctx, tx); err != nil {
			t.Fatal(err)
		}
		var changes, details, outcome string
		err = e.store.DB().QueryRow("SELECT changes,details,outcome FROM management_operation_items WHERE operation_id=(SELECT id FROM management_operations WHERE event_id=?)", managementlog.CaptureEventID(ctx)).Scan(&changes, &details, &outcome)
		if err != nil || outcome != "succeeded" || !strings.Contains(changes, "permissions."+key) || !strings.Contains(details, "updated") {
			t.Fatal(key, changes, details, outcome, err)
		}
	}
}

func TestStewardConfigurationHTTPMatchesManagementWire(t *testing.T) {
	e := newRoutingTestEnv(t)
	e.seedUser(t, true, nil)
	six := int64(6)
	actor := e.seedUser(t, false, &six)
	api := &httpAPI{service: e.service}
	model := e.createModel(t, 'm')
	var err error
	e.service.adaptations, err = requestadaptation.New(requestadaptation.Config{DB: e.store.DB(), Codec: e.vault, KeyDeriver: e.vault})
	if err != nil {
		t.Fatal(err)
	}
	principal := UserPrincipal{UserID: actor}
	call := func(method, id, body, key string, adaptation bool) *httptest.ResponseRecorder {
		route := routeStewardPriceTemplates
		if id != "" {
			route = routeStewardPriceTemplate
		}
		if adaptation {
			route = routeStewardModelAdaptation
		}
		req := httptest.NewRequest(method, route, strings.NewReader(body))
		req.SetPathValue("id", id)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", strings.Repeat(key, 22))
		out := httptest.NewRecorder()
		if adaptation {
			api.putStewardModelAdaptation(out, req, principal)
		} else {
			api.mutateStewardPriceTemplate(out, req, principal)
		}
		return out
	}
	adaptation := `{"expected_revision":"0","forward_headers":{"mode":"replace","values":["X-Project"]}}`
	for range 2 {
		if out := call("PUT", model.ID, adaptation, "p", true); out.Code != 200 {
			t.Fatal(out.Code, out.Body)
		}
	}
	if out := call("PUT", model.ID, adaptation, "q", true); out.Code != 409 {
		t.Fatal("stale adaptation", out.Code, out.Body)
	}
	body := `{"name":"Template","description":"","enabled":false,"pricing":{"mode":"per_request","user_price":"0","donor_reward":"0","long_context":null,"context_penalty":null},"token_reserve_credits":null,"discount":{"enabled":false,"percent":0,"start_at":null,"end_at":null}}`
	out := call("POST", "", body, "r", false)
	if out.Code != 201 {
		t.Fatal(out.Code, out.Body)
	}
	var made PriceTemplate
	if err := json.Unmarshal(out.Body.Bytes(), &made); err != nil {
		t.Fatal(err)
	}
	changed := strings.TrimSuffix(body, "}") + `,"expected_revision":"1"}`
	out = call("PUT", made.ID, changed, "s", false)
	if out.Code != 200 {
		t.Fatal(out.Code, out.Body)
	}
	out = call("DELETE", made.ID, `{"expected_revision":"2"}`, "t", false)
	if out.Code != 204 {
		t.Fatal(out.Code, out.Body)
	}
	for range 2 {
		out = call("DELETE", made.ID, `{"expected_revision":"2"}`, "t", false)
		if out.Code != 204 {
			t.Fatal("delete replay", out.Code, out.Body)
		}
	}
}
