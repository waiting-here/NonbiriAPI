package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/connectionstore/testseed"
	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

func TestManagedDonationDiscoveryRegisteredSessionRoutes(t *testing.T) {
	for _, share := range []bool{true, false} {
		t.Run(fmt.Sprintf("shared=%t", share), func(t *testing.T) {
			testManagedDonationDiscoveryRegisteredSessionRoutes(t, share)
		})
	}
}

func testManagedDonationDiscoveryRegisteredSessionRoutes(t *testing.T, share bool) {
	t.Helper()
	f := newGameWireFixture(t)
	exec := func(query string, args ...any) int64 {
		t.Helper()
		result, err := f.store.DB().Exec(query, args...)
		if err != nil {
			t.Fatal(err)
		}
		id, err := result.LastInsertId()
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	exec(`UPDATE site_config SET value='1' WHERE key='donation_accept_enabled'`)
	endpoint := appFixtureEndpoint(t, f.store, testseed.EndpointInput{OwnerUserID: f.userID, Type: connectorcontract.TypeOpenAICompatible, BaseURL: "https://discovery.example.test/v1", Note: "", At: f.now})
	contextID, envelope := sealRootTestCredential(t)
	secret := exec(`INSERT INTO endpoint_key_secrets(context_id,canonical_base_url,connector_type,encrypted_secret,created_at) VALUES(?,'https://discovery.example.test/v1','openai-compatible',?,?)`, contextID, envelope, f.now)
	physical := appFixtureKey(t, f.store, testseed.KeyInput{EndpointID: endpoint, SecretRefID: secret, Note: "", At: f.now}).KeyID
	source := appFixtureSource(t, f.store, testseed.DiscoverySourceInput{EndpointKeyID: physical, ListFormat: "openai", At: f.now})
	sharing := ""
	if share {
		sharing = fmt.Sprintf(`,"discovery_source_id":"%d","discovery_source_revision":"1"`, source.SourceID)
	}
	headers := func(host, seed string) map[string]string {
		return map[string]string{"Content-Type": "application/json", "Origin": "http://" + host, "Idempotency-Key": strings.Repeat(seed, 22)}
	}
	created := testApplicationRequest(t, f.app.handler, "POST", auditUserHost, "/api/donations",
		fmt.Sprintf(`{"description":"Model discovery fixture","keys":[{"endpoint_key_id":"%d","expires_at":null,"native_services":["chat_completions"]%s}],"ownership_authorized":true,"discord_public_thanks":false}`, physical, sharing), f.cookies, headers(auditUserHost, "C"))
	var d struct {
		ID, Revision string
		Keys         []struct{ ID string }
	}
	if created.Code != 201 || json.Unmarshal(created.Body.Bytes(), &d) != nil || len(d.Keys) != 1 {
		t.Fatalf("create: %d %s", created.Code, created.Body)
	}
	key := d.Keys[0].ID
	approved := testApplicationRequest(t, f.app.handler, "POST", auditAdminHost, "/admin/api/donations/"+d.ID+"/review",
		fmt.Sprintf(`{"decision":"approve","expected_revision":%q,"reason":"Approved","key_settings":[{"donation_key_id":%q,"enabled":true,"price_limit":null,"calls_limit":null,"tokens_limit":null,"token_reserve":0,"safe_note":"","expires_at":null}]}`, d.Revision, key), f.adminCookies, headers(auditAdminHost, "A"))
	if approved.Code != 200 {
		t.Fatalf("approve: %d %s", approved.Code, approved.Body)
	}
	path := "/api/steward/donations/" + d.ID + "/keys/" + key + "/models"
	for _, suffix := range []string{"/refresh", "/discovery"} {
		method := "POST"
		if suffix == "/discovery" {
			method = "GET"
		}
		denied := testApplicationRequest(t, f.app.handler, method, auditUserHost, path+suffix, "", f.cookies, headers(auditUserHost, "D"))
		if denied.Code != 403 {
			t.Fatalf("ordinary %s: %d %s", suffix, denied.Code, denied.Body)
		}
	}
	exec(`UPDATE users SET level=6 WHERE id=?`, f.userID)
	selected := testApplicationRequest(t, f.app.handler, "POST", auditUserHost, "/api/steward/donation-keys/models/refresh/selection", `{"donation_id":null,"cursor":null}`, f.cookies, headers(auditUserHost, "S"))
	if selected.Code != 200 || strings.Contains(selected.Body.String(), `"key_id":"`+key+`"`) != share {
		t.Fatalf("select: %d %s", selected.Code, selected.Body)
	}
	if !share {
		for _, admin := range []bool{false, true} {
			host, cookies, prefix := auditUserHost, f.cookies, "/api/steward"
			if admin {
				host, cookies, prefix = auditAdminHost, f.adminCookies, "/admin/api"
			}
			for _, suffix := range []string{"/refresh", "/discovery"} {
				method := "POST"
				if suffix == "/discovery" {
					method = "GET"
				}
				response := testApplicationRequest(t, f.app.handler, method, host,
					prefix+"/donations/"+d.ID+"/keys/"+key+"/models"+suffix, "", cookies, headers(host, "N"))
				if response.Code != http.StatusLocked || !strings.Contains(response.Body.String(), `"code":"discovery_not_shared"`) || !strings.Contains(response.Body.String(), `"source":"platform"`) {
					t.Fatalf("unshared %s %s: %d %s", host, suffix, response.Code, response.Body)
				}
			}
		}
		var count int
		if err := f.store.DB().QueryRow(`SELECT count(*) FROM accepted_operations WHERE kind='model_discovery'`).Scan(&count); err != nil || count != 0 {
			t.Fatalf("unshared catalog accepted an operation: %d %v", count, err)
		}
		return
	}
	for _, testcase := range []struct{ body, origin, query string }{{"{}", "http://" + auditUserHost, ""}, {"", "https://foreign.example.test", ""}, {"", "http://" + auditUserHost, "?q=x"}} {
		h := headers(auditUserHost, "R")
		h["Origin"] = testcase.origin
		response := testApplicationRequest(t, f.app.handler, "POST", auditUserHost, path+"/refresh"+testcase.query, testcase.body, f.cookies, h)
		if response.Code != 400 && response.Code != 403 {
			t.Fatalf("invalid request: %d %s", response.Code, response.Body)
		}
	}
	accepted := testApplicationRequest(t, f.app.handler, "POST", auditUserHost, path+"/refresh", "", f.cookies, headers(auditUserHost, "R"))
	var out resources.DiscoveryAccepted
	if accepted.Code != http.StatusAccepted || json.Unmarshal(accepted.Body.Bytes(), &out) != nil || out.Evidence.State != "checking" {
		t.Fatalf("accept: %d %s", accepted.Code, accepted.Body)
	}
	replay := testApplicationRequest(t, f.app.handler, "POST", auditUserHost, path+"/refresh", "", f.cookies, headers(auditUserHost, "R"))
	if replay.Code != 202 || replay.Body.String() != accepted.Body.String() {
		t.Fatalf("replay: %d %s", replay.Code, replay.Body)
	}
	adminPath := strings.Replace(path, "/api/steward", "/admin/api", 1)
	read := testApplicationRequest(t, f.app.handler, "GET", auditAdminHost, adminPath+"/discovery", "", f.adminCookies, nil)
	if read.Code != 200 || read.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("admin read: %d %s", read.Code, read.Body)
	}
	exec(`UPDATE users SET level=4 WHERE id=?`, f.userID)
	revoked := testApplicationRequest(t, f.app.handler, "POST", auditUserHost, path+"/refresh", "", f.cookies, headers(auditUserHost, "R"))
	if revoked.Code != 403 {
		t.Fatalf("revoked replay: %d %s", revoked.Code, revoked.Body)
	}
	caller := testApplicationRequest(t, f.app.handler, "POST", auditUserHost, path+"/refresh", "", nil, map[string]string{"Authorization": "Bearer synthetic-caller"})
	if caller.Code != 401 {
		t.Fatalf("caller authorized: %d", caller.Code)
	}
}
