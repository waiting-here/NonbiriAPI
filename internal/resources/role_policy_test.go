package resources

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/rolepolicy"
)

func TestModelRolePolicyPersistenceAuditReplayAndExport(t *testing.T) {
	e := newResourceTestEnvironment(t)
	owner := e.seedUser(t, "policy-owner")
	other := e.seedUser(t, "policy-other")
	model := e.createModel(t, owner, resourceTestKey('a'), "policy", "model")
	id := resourceTestID(t, model.ID)
	canonical, _ := model.RolePolicy.Canonical()
	if canonical != "{\"default_action\":\"native\",\"rules\":{}}" {
		t.Fatal(canonical)
	}
	policy := rolepolicy.Default()
	policy.Rules["developer"] = "user"
	input := PatchModelInput{RolePolicy: &policy, ExpectedRevision: 1}
	mutation := resourceTestMutation(t, resourceTestKey('b'), "PATCH", routeModel, []int64{id}, patchModelCanonical{RolePolicy: &policy, ExpectedRevision: "1"})
	changed, err := e.repository.PatchModel(context.Background(), owner, id, mutation, input)
	if err != nil || changed.Value.Revision != "2" || changed.Value.RolePolicy.Rules["developer"] != "user" {
		t.Fatalf("%+v %v", changed, err)
	}
	replay, err := e.repository.PatchModel(context.Background(), owner, id, mutation, input)
	if err != nil || !replay.Replayed {
		t.Fatalf("replay %+v %v", replay, err)
	}
	var from, to int64
	var old, next sql.NullInt64
	if err := e.store.DB().QueryRow("SELECT old_value,new_value,from_revision,to_revision FROM policy_audits WHERE resource_type='model' AND resource_id=? AND policy='role_policy'", id).Scan(&old, &next, &from, &to); err != nil || old.Valid || next.Valid || from != 1 || to != 2 {
		t.Fatalf("audit %v %v %d %d %v", old, next, from, to, err)
	}
	rename := "renamed"
	omitted := resourceTestMutation(t, resourceTestKey('c'), "PATCH", routeModel, []int64{id}, patchModelCanonical{Provider: &rename, ExpectedRevision: "2"})
	preserved, err := e.repository.PatchModel(context.Background(), owner, id, omitted, PatchModelInput{Provider: &rename, ExpectedRevision: 2})
	if err != nil || preserved.Value.RolePolicy.Rules["developer"] != "user" {
		t.Fatalf("omitted policy %+v %v", preserved, err)
	}
	stale := resourceTestMutation(t, resourceTestKey('d'), "PATCH", routeModel, []int64{id}, patchModelCanonical{RolePolicy: &policy, ExpectedRevision: "1"})
	if _, err := e.repository.PatchModel(context.Background(), owner, id, stale, input); !errors.Is(err, ErrConflict) {
		t.Fatal("stale policy accepted", err)
	}
	if _, err := e.repository.GetModel(context.Background(), other, id); !errors.Is(err, ErrNotFound) {
		t.Fatal("foreign policy exposed", err)
	}
	if count := e.rowCount(t, "SELECT count(*) FROM policy_audits WHERE resource_type='model' AND resource_id=? AND policy='role_policy'", id); count != 1 {
		t.Fatal("audit count", count)
	}
	tx, err := e.store.DB().BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	exported, err := e.repository.ExportLifecycleResources(context.Background(), tx, owner, resourceTestNow, 100)
	if err != nil || len(exported.Models) != 1 || exported.Models[0].RolePolicy.Rules["developer"] != "user" {
		t.Fatalf("export %+v %v", exported, err)
	}
	body, _ := json.Marshal(preserved.Value)
	if !strings.Contains(string(body), "role_policy") {
		t.Fatal("public policy missing")
	}
}
func TestModelRolePolicyHTTPRejectsNullAndDuplicateRules(t *testing.T) {
	for _, raw := range []string{
		"{\"provider\":\"p\",\"model\":\"m\",\"role_policy\":null}",
		"{\"provider\":\"p\",\"model\":\"m\",\"role_policy\":{\"default_action\":\"native\",\"rules\":{\"developer\":\"user\",\"developer\":\"system\"}}}",
		"{\"expected_revision\":\"1\",\"role_policy\":{\"default_action\":\"native\",\"rules\":{\"user\":\"system\"}}}",
	} {
		out := httptest.NewRecorder()
		request := httptest.NewRequest("POST", "/", strings.NewReader(raw))
		request.Header.Set("Content-Type", "application/json")
		var input createModelRequest
		if _, ok := decodeStrictObject(out, request, &input); ok {
			t.Fatal("invalid policy accepted", raw)
		}
	}
}
