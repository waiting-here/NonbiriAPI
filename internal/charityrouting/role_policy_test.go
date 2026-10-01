package charityrouting

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/rolepolicy"
)

func TestRolePolicyManagedScopeRevisionAuditAndReplay(t *testing.T) {
	e := newRoutingTestEnv(t)
	e.seedUser(t, true, nil)
	five, six := int64(5), int64(6)
	trainee := e.seedUser(t, false, &five)
	steward := e.seedUser(t, false, &six)
	m := e.createModel(t, 'a')
	id, _ := parsePositiveID(m.ID)
	policy := rolepolicy.Default()
	policy.Rules["developer"] = "user"
	patch := ModelPatch{ExpectedRevision: "1", RolePolicy: &policy}
	mutation := routingMutation(t, 'b', http.MethodPatch, routeStewardModel, []int64{id}, patch)
	if _, err := e.service.PatchSteward(context.Background(), trainee, id, mutation, patch); !errors.Is(err, ErrNotFound) {
		t.Fatal("custom model accepted", err)
	}
	if _, err := e.store.DB().Exec("UPDATE charity_models SET is_mainstream=1 WHERE id=?", id); err != nil {
		t.Fatal(err)
	}
	changed, err := e.service.PatchSteward(context.Background(), trainee, id, mutation, patch)
	if err != nil || changed.Value.Revision != "2" || changed.Value.RolePolicy.Rules["developer"] != "user" {
		t.Fatalf("%+v %v", changed, err)
	}
	replay, err := e.service.PatchSteward(context.Background(), trainee, id, mutation, patch)
	if err != nil || !replay.Replayed {
		t.Fatalf("replay %+v %v", replay, err)
	}
	var role string
	var from, to int64
	var old, next sql.NullInt64
	err = e.store.DB().QueryRow("SELECT actor_role,old_value,new_value,from_revision,to_revision FROM policy_audits WHERE resource_type='charity_model' AND resource_id=? AND policy='role_policy'", id).Scan(&role, &old, &next, &from, &to)
	if err != nil || role != "trainee5" || old.Valid || next.Valid || from != 1 || to != 2 {
		t.Fatalf("audit %s %v %v %d %d %v", role, old, next, from, to, err)
	}
	enabled := false
	forbidden := ModelPatch{ExpectedRevision: "2", RolePolicy: &policy, Enabled: &enabled}
	if _, err := e.service.PatchSteward(context.Background(), trainee, id, routingMutation(t, 'c', http.MethodPatch, routeStewardModel, []int64{id}, forbidden), forbidden); !errors.Is(err, ErrForbidden) {
		t.Fatal("trainee other policy accepted", err)
	}
	omitted := ModelPatch{ExpectedRevision: "2", Enabled: &enabled}
	preserved, err := e.service.PatchSteward(context.Background(), steward, id, routingMutation(t, 'd', http.MethodPatch, routeStewardModel, []int64{id}, omitted), omitted)
	if err != nil || preserved.Value.RolePolicy.Rules["developer"] != "user" {
		t.Fatalf("omitted %+v %v", preserved, err)
	}
	if _, err := e.store.DB().Exec("UPDATE charity_models SET is_mainstream=0 WHERE id=?", id); err != nil {
		t.Fatal(err)
	}
	if _, err := e.service.PatchSteward(context.Background(), trainee, id, mutation, patch); !errors.Is(err, ErrNotFound) {
		t.Fatal("revoked replay accepted", err)
	}
	admin, err := e.service.GetAdmin(context.Background(), id)
	if err != nil || admin.RolePolicy.Rules["developer"] != "user" {
		t.Fatalf("read %+v %v", admin, err)
	}
}
func TestRolePolicyCreateAndStrictHTTPParsing(t *testing.T) {
	e := newRoutingTestEnv(t)
	e.seedUser(t, true, nil)
	p := rolepolicy.Default()
	p.DefaultAction = "reject"
	p.Rules["developer"] = "system"
	input := testModelCreate()
	input.RolePolicy = &p
	result, err := e.service.CreateAdmin(context.Background(), routingMutation(t, 'e', http.MethodPost, routeAdminModels, nil, input), input)
	if err != nil || result.Value.RolePolicy.DefaultAction != "reject" {
		t.Fatalf("%+v %v", result, err)
	}
	var from, to int
	if err := e.store.DB().QueryRow("SELECT from_revision,to_revision FROM policy_audits WHERE resource_type='charity_model' AND resource_id=? AND policy='role_policy'", result.Value.ID).Scan(&from, &to); err != nil || from != 0 || to != 1 {
		t.Fatal(from, to, err)
	}
	for _, raw := range []string{
		"{\"expected_revision\":\"1\",\"role_policy\":null}",
		"{\"expected_revision\":\"1\",\"role_policy\":{\"default_action\":\"native\",\"rules\":{\"developer\":\"user\",\"developer\":\"system\"}}}",
	} {
		var wire modelPatchWire
		if json.Unmarshal([]byte(raw), &wire) == nil {
			t.Fatal("invalid policy accepted", raw)
		}
	}
}
