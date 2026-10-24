package charityrouting

import (
	"encoding/json"
	"errors"
	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"net/http"
	"strconv"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/httpapi"
	"github.com/waiting-here/NonbiriAPI/internal/sessionheader"
)

func TestSessionHeaderConfigurationPermissionsAndOmission(t *testing.T) {
	e := newRoutingTestEnv(t)
	e.seedUser(t, true, nil)
	in := testModelCreate()
	in.SessionHeaders = []sessionheader.Config{{Name: "X-Cache-Session", IdleSeconds: 300}}
	r, err := e.service.CreateAdmin(t.Context(), routingMutation(t, 'a', http.MethodPost, routeAdminModels, nil, in), in)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := strconv.ParseInt(r.Value.ID, 10, 64)
	if len(r.Value.SessionHeaders) != 1 || r.Value.SessionHeaders[0].Name != "x-cache-session" || r.Value.SessionHeaders[0].IdleSeconds != 300 {
		t.Fatal(r.Value.SessionHeaders)
	}
	if len(stewardModel(r.Value).SessionHeaders) != 1 {
		t.Fatal("steward lost config")
	}
	enabled := false
	p := ModelPatch{ExpectedRevision: r.Value.Revision, Enabled: &enabled}
	changed, err := e.service.PatchAdmin(t.Context(), id, routingMutation(t, 'b', http.MethodPatch, routeAdminModel, []int64{id}, p), p)
	if err != nil || len(changed.Value.SessionHeaders) != 1 {
		t.Fatal(changed, err)
	}
	empty := []sessionheader.Config{}
	p = ModelPatch{ExpectedRevision: changed.Value.Revision, SessionHeaders: &empty}
	cleared, err := e.service.PatchAdmin(t.Context(), id, routingMutation(t, 'c', http.MethodPatch, routeAdminModel, []int64{id}, p), p)
	if err != nil || len(cleared.Value.SessionHeaders) != 0 {
		t.Fatal(cleared, err)
	}
	p.ExpectedRevision = cleared.Value.Revision
	// Trainees cannot hide a settings change inside an otherwise permitted role-policy patch.
	five := int64(5)
	trainee := e.seedUser(t, false, &five)
	if _, err = e.store.DB().Exec("UPDATE charity_models SET is_mainstream=1 WHERE id=?", id); err != nil {
		t.Fatal(err)
	}
	role := in.RolePolicy
	if role == nil {
		v := r.Value.RolePolicy
		role = &v
	}
	p.RolePolicy = role
	_, err = e.service.PatchSteward(t.Context(), trainee, id, routingMutation(t, 'd', http.MethodPatch, routeStewardModel, []int64{id}, p), p)
	if !errors.Is(err, authz.ErrPermissionDenied) {
		t.Fatal(err)
	}
}
func TestSessionHeaderWireDefaultsAndStrictFields(t *testing.T) {
	var wire modelPatchWire
	raw := []byte(`{"expected_revision":"1","session_headers":[{"name":"X-Session"}]}`)
	if err := httpapi.DecodeJSON(raw, &wire); err != nil {
		t.Fatal(err)
	}
	v, canonical, err := parseModelPatch(wire)
	if err != nil || (*v.SessionHeaders)[0].IdleSeconds != 300 {
		t.Fatal(v, err)
	}
	if _, err = json.Marshal(canonical); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{`{"expected_revision":"1","session_headers":null}`, `{"expected_revision":"1","session_headers":[{"name":"x","idle_seconds":-1}]}`, `{"expected_revision":"1","session_headers":[{"name":"x","extra":true}]}`} {
		var w modelPatchWire
		if err = httpapi.DecodeJSON([]byte(raw), &w); err == nil {
			t.Fatal("accepted", raw)
		}
	}
}
