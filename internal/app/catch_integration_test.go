package app

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/game/steadycatch"
)

func TestCatchRoutesControlsAndMaintenanceContinuation(t *testing.T) {
	f := newGameWireFixture(t)
	adminHeaders := map[string]string{"Content-Type": "application/json", "Origin": "http://" + auditAdminHost, "Idempotency-Key": strings.Repeat("C", 22)}
	configured := testApplicationRequest(t, f.app.handler, "PATCH", auditAdminHost, "/admin/api/games/config", `{"expected_revision":"2","steadycatch":{"enabled":true,"price":"0","first_clear_reward":"0"}}`, f.adminCookies, adminHeaders)
	if configured.Code != 200 {
		t.Fatalf("configure: %d %s", configured.Code, configured.Body.String())
	}
	headers := map[string]string{"Content-Type": "application/json", "Origin": "http://" + auditUserHost, "Idempotency-Key": strings.Repeat("S", 22)}
	started := testApplicationRequest(t, f.app.handler, "POST", auditUserHost, "/api/games/steady-catch/sessions", "{}", f.cookies, headers)
	if started.Code != 200 {
		t.Fatalf("start: %d %s", started.Code, started.Body.String())
	}
	var session steadycatch.View
	if err := json.Unmarshal(started.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	if session.Status != "paused" || session.State.Tick != 0 || session.Payment.General != "0" || session.Payment.Game != "0" {
		t.Fatalf("initial: %+v", session)
	}
	path := "/api/games/steady-catch/sessions/" + session.ID + "/controls"
	forged := testApplicationRequest(t, f.app.handler, "POST", auditUserHost, path, `{"revision":1,"action":"resume","until_tick":0,"inputs":[],"score":999999}`, f.cookies, headers)
	if forged.Code != 400 {
		t.Fatalf("forged score: %d", forged.Code)
	}
	resume := testApplicationRequest(t, f.app.handler, "POST", auditUserHost, path, `{"revision":1,"action":"resume","until_tick":0,"inputs":[]}`, f.cookies, headers)
	if resume.Code != 200 {
		t.Fatalf("resume: %d %s", resume.Code, resume.Body.String())
	}
	counts := testApplicationRequest(t, f.app.handler, "GET", auditAdminHost, "/admin/api/games/active-counts", "", f.adminCookies, nil)
	if counts.Code != 200 || !strings.Contains(counts.Body.String(), `"game":"steadycatch"`) {
		t.Fatalf("counts: %d %s", counts.Code, counts.Body.String())
	}
	adminHeaders["Idempotency-Key"] = strings.Repeat("M", 22)
	closed := testApplicationRequest(t, f.app.handler, "POST", auditAdminHost, "/admin/api/maintenance/enable", `{"expected_revision":"2","reason":"fixture","confirmation":true}`, f.adminCookies, adminHeaders)
	if closed.Code != 200 {
		t.Fatalf("maintenance: %d %s", closed.Code, closed.Body.String())
	}
	read := testApplicationRequest(t, f.app.handler, "GET", auditUserHost, "/api/games/steady-catch/session", "", f.cookies, nil)
	if read.Code != 200 {
		t.Fatalf("continue read: %d %s", read.Code, read.Body.String())
	}
	paused := testApplicationRequest(t, f.app.handler, "POST", auditUserHost, path, `{"revision":2,"action":"pause","until_tick":0,"inputs":[]}`, f.cookies, headers)
	if paused.Code != 200 {
		t.Fatalf("continue pause: %d %s", paused.Code, paused.Body.String())
	}
	blocked := testApplicationRequest(t, f.app.handler, "POST", auditUserHost, "/api/games/steady-catch/sessions", "{}", f.cookies, headers)
	if blocked.Code != http.StatusServiceUnavailable {
		t.Fatalf("new start during maintenance: %d", blocked.Code)
	}
}
