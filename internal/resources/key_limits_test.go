package resources

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"testing"
)

func TestEndpointKeyLimitsOwnerCASReplayExportAndDeletion(t *testing.T) {
	e := newResourceTestEnvironment(t)
	owner := e.seedUser(t, "key-limits-owner")
	other := e.seedUser(t, "key-limits-other")
	endpoint := e.createEndpoint(t, owner, resourceTestKey('A'))
	eid := resourceTestID(t, endpoint.ID)
	key := e.createEndpointKey(t, owner, eid, resourceTestKey('B'))
	kid := resourceTestID(t, key.ID)
	if key.MaxConcurrency != 0 || key.MaxRPM != 0 {
		t.Fatal("limits must default to unlimited")
	}
	input := PatchEndpointKeyInput{MaxConcurrency: pointer(int64(2)), MaxRPM: pointer(int64(30)), ExpectedRevision: 1}
	mutation := resourceTestMutation(t, resourceTestKey('C'), "PATCH", routeEndpointKey, []int64{eid, kid}, patchEndpointKeyCanonical{MaxConcurrency: input.MaxConcurrency, MaxRPM: input.MaxRPM, ExpectedRevision: "1"})
	if _, err := e.repository.PatchEndpointKey(context.Background(), other, eid, kid, mutation, input); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-owner patch=%v", err)
	}
	got, err := e.repository.PatchEndpointKey(context.Background(), owner, eid, kid, mutation, input)
	if err != nil || got.Value.MaxConcurrency != 2 || got.Value.MaxRPM != 30 || got.Value.Revision != "2" {
		t.Fatalf("patch=%+v %v", got.Value, err)
	}
	replay, err := e.repository.PatchEndpointKey(context.Background(), owner, eid, kid, mutation, input)
	if err != nil || !replay.Replayed || string(replay.Body) != string(got.Body) {
		t.Fatalf("replay=%+v %v", replay, err)
	}
	stale := mutation
	stale.IdempotencyKey = resourceTestKey('D')
	if _, err := e.repository.PatchEndpointKey(context.Background(), owner, eid, kid, stale, input); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale revision=%v", err)
	}
	tx, err := e.store.DB().Begin()
	if err != nil {
		t.Fatal(err)
	}
	exported, err := e.repository.ExportLifecycleResources(context.Background(), tx, owner, resourceTestNow, 100)
	tx.Rollback()
	if err != nil || len(exported.Endpoints) != 1 || exported.Endpoints[0].Keys[0].MaxConcurrency != 2 || exported.Endpoints[0].Keys[0].MaxRPM != 30 {
		t.Fatalf("export limits lost: %+v %v", exported, err)
	}
	if _, err := e.store.DB().Exec(`DELETE FROM endpoint_keys WHERE id=?`, kid); err != nil {
		t.Fatal(err)
	}
	if e.rowCount(t, `SELECT COUNT(*) FROM endpoint_key_limits WHERE endpoint_key_id=?`, kid) != 0 {
		t.Fatal("limits survived key deletion")
	}
}

func TestEndpointKeyLimitsHTTPValidationAndPartialPatch(t *testing.T) {
	e := newResourceTestEnvironment(t)
	owner := e.seedUser(t, "limit-http")
	endpoint := e.createEndpoint(t, owner, resourceTestKey('A'))
	reg := &resourceTestRegistrar{}
	if err := RegisterRoutes(reg, e.repository); err != nil {
		t.Fatal(err)
	}
	create := reg.handlers[http.MethodPost+" "+routeEndpointKeys]
	principal := UserPrincipal{UserID: owner}
	path := "/api/endpoints/" + endpoint.ID + "/keys"
	values := map[string]string{"id": endpoint.ID}
	base := `"secret":"fixture-limit-secret","note":"","enabled":true,"force_store_false":false,"ownership_confirmed":true`
	for i, value := range []string{"-1", "1.5", "null", "2147483648", `"2"`, "true"} {
		for _, field := range []string{"max_concurrency", "max_rpm"} {
			body := fmt.Sprintf(`{%s,"%s":%s}`, base, field, value)
			response := resourceHTTPCall(t, create, principal, "POST", path, body, resourceTestKey(byte('C'+i)), values)
			if response.Code != 400 {
				t.Fatalf("%s=%s status=%d", field, value, response.Code)
			}
		}
	}
	response := resourceHTTPCall(t, create, principal, "POST", path, `{`+base+`,"max_concurrency":3,"max_rpm":2147483647}`, resourceTestKey('M'), values)
	var key EndpointKey
	if response.Code != 201 || json.Unmarshal(response.Body.Bytes(), &key) != nil || key.MaxConcurrency != 3 || key.MaxRPM != 2147483647 {
		t.Fatalf("create=%d %s", response.Code, response.Body.String())
	}
	values["keyId"] = key.ID
	patch := reg.handlers[http.MethodPatch+" "+routeEndpointKey]
	response = resourceHTTPCall(t, patch, principal, "PATCH", path+"/"+key.ID, `{"max_concurrency":0,"expected_revision":"1"}`, resourceTestKey('N'), values)
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &key) != nil || key.MaxConcurrency != 0 || key.MaxRPM != 2147483647 {
		t.Fatalf("partial patch=%d %s", response.Code, response.Body.String())
	}
	for i, value := range []string{"null", "-1", "0.5"} {
		response = resourceHTTPCall(t, patch, principal, "PATCH", path+"/"+key.ID, `{"max_rpm":`+value+`,"expected_revision":"2"}`, resourceTestKey(byte('P'+i)), values)
		if response.Code != 400 {
			t.Fatalf("patch invalid=%d %s", response.Code, response.Body.String())
		}
	}
}
