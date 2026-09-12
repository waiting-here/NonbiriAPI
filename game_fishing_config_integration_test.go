package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestFishingRakeConfigurationHTTPRevision(t *testing.T) {
	f := newGameWireFixture(t)
	route := "/admin/api/games/config"
	headers := map[string]string{"Content-Type": "application/json", "Origin": "http://" + auditAdminHost, "Idempotency-Key": strings.Repeat("R", 22)}
	invalid := testApplicationRequest(t, f.app.handler, http.MethodPatch, auditAdminHost, route, `{"expected_revision":"2","fishing":{"rake_bp":{"platform":9800}}}`, f.adminCookies, headers)
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("invalid total: %d %s", invalid.Code, invalid.Body.String())
	}
	body := `{"expected_revision":"2","fishing":{"rake_bp":{"platform":9799}}}`
	headers["Idempotency-Key"] = strings.Repeat("S", 22)
	updated := testApplicationRequest(t, f.app.handler, http.MethodPatch, auditAdminHost, route, body, f.adminCookies, headers)
	if updated.Code != http.StatusOK {
		t.Fatalf("update: %d %s", updated.Code, updated.Body.String())
	}
	var state struct {
		Revision string
		Fishing  struct {
			Rake struct{ Platform, Welfare, Thursday int } `json:"rake_bp"`
			RTP  struct{ Standard, Premium int }           `json:"rtp_percent"`
		}
	}
	if err := json.Unmarshal(updated.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	if state.Revision != "3" || state.Fishing.Rake.Platform != 9799 || state.Fishing.Rake.Welfare != 100 || state.Fishing.Rake.Thursday != 100 ||
		state.Fishing.RTP.Standard != 100 || state.Fishing.RTP.Premium != 100 {
		t.Fatalf("configuration=%+v", state)
	}
	replay := testApplicationRequest(t, f.app.handler, http.MethodPatch, auditAdminHost, route, body, f.adminCookies, headers)
	if replay.Code != http.StatusOK || replay.Body.String() != updated.Body.String() {
		t.Fatalf("replay: %d %s", replay.Code, replay.Body.String())
	}
	headers["Idempotency-Key"] = strings.Repeat("T", 22)
	stale := testApplicationRequest(t, f.app.handler, http.MethodPatch, auditAdminHost, route, body, f.adminCookies, headers)
	if stale.Code != http.StatusConflict {
		t.Fatalf("stale revision: %d %s", stale.Code, stale.Body.String())
	}
}
