package app

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/db"
)

func TestBindingManagementRoutesRequireLiveSessionRoleAndOrigin(t *testing.T) {
	f := newGameWireFixture(t)
	channel, err := db.GenerateOpaqueID("mch_")
	if err != nil {
		t.Fatal(err)
	}
	setLevel := func(level int) {
		t.Helper()
		if _, err := f.store.DB().Exec(`UPDATE users SET level=? WHERE id=?`, level, f.userID); err != nil {
			t.Fatal(err)
		}
	}
	call := func(method, path, body, origin string, cookies []*http.Cookie, want int) {
		t.Helper()
		response := testApplicationRequest(t, f.app.handler, method, auditUserHost, path, body, cookies, map[string]string{"Content-Type": "application/json", "Origin": origin, "Idempotency-Key": "binding-route-auth-123456789"})
		if response.Code != want {
			t.Fatalf("%s %s status %d want %d: %s", method, path, response.Code, want, response.Body.String())
		}
	}
	origin := "http://" + auditUserHost
	presets := "/api/steward/charity-binding-presets"
	options := "/api/steward/donations/999999/keys/999999/binding-options"
	batch := "/api/steward/donations/999999/keys/999999/bindings/batch"
	input := `{"expected_donation_revision":"1","targets":[{"charity_model_id":"999999","expected_binding_revision":"0","upstream_model_id":"exact"}]}`
	setLevel(6)
	call(http.MethodGet, presets, "", origin, f.cookies, http.StatusOK)
	call(http.MethodGet, options, "", origin, nil, http.StatusUnauthorized)
	call(http.MethodPost, batch, input, origin, nil, http.StatusUnauthorized)
	call(http.MethodPost, batch, input, "http://untrusted.example", f.cookies, http.StatusForbidden)
	call(http.MethodPut, presets+"/"+channel, `{"expected_channel_revision":"1","expected_preset_revision":"0","mappings":[]}`, "http://untrusted.example", f.cookies, http.StatusForbidden)
	call(http.MethodGet, presets+"?unknown=true", "", origin, f.cookies, http.StatusBadRequest)
	call(http.MethodPost, batch, `{"unknown":true}`, origin, f.cookies, http.StatusBadRequest)
	setLevel(5)
	call(http.MethodGet, presets, "", origin, f.cookies, http.StatusForbidden)
	call(http.MethodGet, presets+"/"+channel, "", origin, f.cookies, http.StatusForbidden)
	call(http.MethodPut, presets+"/"+channel, `{"expected_channel_revision":"1","expected_preset_revision":"0","mappings":[]}`, origin, f.cookies, http.StatusForbidden)
	call(http.MethodPost, "/api/steward/donations/999999/bindings/apply-preset", `{"channel_id":"`+channel+`","expected_channel_revision":"1","expected_preset_revision":"1","expected_donation_revision":"1","models":[]}`, origin, f.cookies, http.StatusForbidden)
	call(http.MethodGet, options, "", origin, f.cookies, http.StatusOK)
	call(http.MethodPost, batch, input, origin, f.cookies, http.StatusNotFound)
	policy := authz.DefaultStewardPermissions(5)
	policy.Permissions["charity.manage"] = false
	policy.Permissions["charity.presets"] = true
	grants, _ := json.Marshal(policy.Permissions)
	limits, _ := json.Marshal(policy.Limits)
	if _, err := f.store.DB().Exec(`INSERT INTO steward_permissions(user_id,revision,permissions,limits,created_at,updated_at) VALUES(?,1,?,?,?,?)`, f.userID, string(grants), string(limits), f.now, f.now); err != nil {
		t.Fatal(err)
	}
	call(http.MethodGet, presets, "", origin, f.cookies, http.StatusOK)
	call(http.MethodGet, options, "", origin, f.cookies, http.StatusForbidden)
	call(http.MethodPost, batch, input, origin, f.cookies, http.StatusForbidden)
	setLevel(4)
	call(http.MethodGet, options, "", origin, f.cookies, http.StatusForbidden)
	call(http.MethodPost, batch, input, origin, f.cookies, http.StatusForbidden)
}
