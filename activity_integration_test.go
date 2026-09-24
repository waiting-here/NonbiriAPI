package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/dbfixture"
	"github.com/waiting-here/NonbiriAPI/internal/elevation"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
	"github.com/waiting-here/NonbiriAPI/internal/lifecycle"
	"github.com/waiting-here/NonbiriAPI/internal/observability"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
	"github.com/waiting-here/NonbiriAPI/internal/upstreamerror"
)

func newActivityWireFixture(t *testing.T) *imageBrowserFixture {
	t.Helper()
	vault, err := secret.New(bytes.Repeat([]byte{0x37}, secret.MasterKeyBytes))
	if err != nil {
		t.Fatal(err)
	}
	f := &imageBrowserFixture{t: t, vault: vault, cfg: auditConfig(), path: filepath.Join(t.TempDir(), "activity.db")}
	dbfixture.Materialize(t, f.path)
	f.upstreamState = newImageFixtureUpstream(t)
	f.upstream = httptest.NewServer(f.upstreamState)
	if err := f.open(); err != nil {
		f.upstream.Close()
		vault.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := f.closeApplication(); err != nil {
			t.Error(err)
		}
		f.upstream.Close()
		vault.Close()
	})
	f.initialize()
	return f
}

func TestImageActivityRootMaintenanceBanDeletionAndExport(t *testing.T) {
	f := newActivityWireFixture(t)
	const base = "/api/limited-activities/picture-book"
	models := f.call("GET", base+"/models", nil, f.users[0].Cookie, false)["data"].([]any)
	model := models[0].(map[string]any)
	submit := func(seat int, prompt string) string {
		t.Helper()
		receipt := f.call("POST", base+"/tasks", map[string]any{
			"model_id": model["id"], "expected_model_revision": model["revision"], "prompt": prompt, "n": 1,
		}, f.users[seat].Cookie, false)
		return receipt["task"].(map[string]any)["id"].(string)
	}
	waitTask := func(seat int, id, status string) map[string]any {
		t.Helper()
		deadline := time.Now().Add(20 * time.Second)
		for {
			value := f.call("GET", base+"/tasks/"+id, nil, f.users[seat].Cookie, false)
			if value["status"] == status {
				return value
			}
			if time.Now().After(deadline) {
				t.Fatalf("task %s stayed %v, want %s", id, value["status"], status)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	first := submit(0, "hold prompt-sentinel")
	waitTask(0, first, "running")
	queued := submit(1, "hold queued-sentinel")
	waitTask(1, queued, "queued")
	f.call("POST", "/admin/api/maintenance/enable", map[string]any{
		"expected_revision": "2", "reason": "planned maintenance", "confirmation": true,
	}, f.adminCookie, true)
	cancelled := waitTask(1, queued, "cancelled")
	if cancelled["billing_state"] != "refunded" || cancelled["refund"].(map[string]any)["paper"] != "2" || cancelled["refund"].(map[string]any)["brush"] != "1" {
		t.Fatalf("maintenance did not refund both currencies: %v", cancelled)
	}
	for _, mutation := range []struct {
		path string
		body any
	}{
		{base + "/exchange", map[string]string{"asset": "sketch_paper", "quantity": "1"}},
		{base + "/tasks", map[string]any{"model_id": model["id"], "expected_model_revision": model["revision"], "prompt": "new", "n": 1}},
	} {
		response := f.request("POST", mutation.path, mutation.body, f.users[1].Cookie, false)
		if response.Code != http.StatusServiceUnavailable {
			t.Fatalf("maintenance admission: %s %d %s", mutation.path, response.Code, response.Body.String())
		}
	}
	f.upstreamState.release()
	waitTask(0, first, "succeeded")
	image := f.request("GET", base+"/tasks/"+first+"/images/0", nil, f.users[0].Cookie, false)
	if image.Code != http.StatusOK || image.Header().Get("Content-Type") != "image/png" {
		t.Fatalf("maintenance image continuation: %d %s", image.Code, image.Body.String())
	}
	f.call("POST", "/admin/api/maintenance/disable", map[string]any{"expected_revision": "3", "reason": "continue"}, f.adminCookie, true)
	lifecycleCall := func(seat int, path string, body any) *httptest.ResponseRecorder {
		t.Helper()
		userID, err := strconv.ParseInt(f.users[seat].ID, 10, 64)
		if err != nil {
			t.Fatal(err)
		}
		binding := sha256.Sum256([]byte(f.users[seat].Cookie.Value))
		token, _, err := f.app.Load().authRuntime.ElevationManager().IssueBound(userID, elevation.KindUser, hex.EncodeToString(binding[:]))
		if err != nil {
			t.Fatal(err)
		}
		return testApplicationRequest(t, f.app.Load().handler, http.MethodPost, f.cfg.UserHost, path,
			wireBody(t, body), []*http.Cookie{f.users[seat].Cookie}, map[string]string{
				"Content-Type": "application/json", "Origin": "http://" + f.cfg.UserHost, "X-Elevated-Token": token})
	}
	exported := lifecycleCall(0, "/api/account/export", nil)
	var document lifecycle.ExportDocument
	if exported.Code != http.StatusOK || json.Unmarshal(exported.Body.Bytes(), &document) != nil || document.SchemaVersion != 10 {
		t.Fatalf("activity export: %d %s", exported.Code, exported.Body.String())
	}
	if len(document.ImageTasks) != 1 || document.ImageTasks[0].BillingState != "charged" || len(document.LimitedActivities.Exchanges) != 2 || document.LimitedActivities.Wallet.Paper != "98" || document.LimitedActivities.Wallet.Brush != "19" || document.Inactivity.Activity == nil {
		t.Fatalf("activity export missing ledger/state: %+v", document.GovernanceExport)
	}
	for _, forbidden := range []string{"prompt-sentinel", imageFixtureModel, imageFixtureSecret, f.upstream.URL} {
		if strings.Contains(exported.Body.String(), forbidden) {
			t.Fatal("private execution data exported")
		}
	}
	active := submit(1, "hold deleted-sentinel")
	waitTask(1, active, "running")
	toBan := submit(0, "hold banned-sentinel")
	waitTask(0, toBan, "queued")
	var revision []byte
	if err := f.store.DB().QueryRow("SELECT revision FROM users WHERE id=?", f.users[0].ID).Scan(&revision); err != nil {
		t.Fatal(err)
	}
	rev, err := db.DecodeU128(revision)
	if err != nil {
		t.Fatal(err)
	}
	banned := f.request("POST", "/admin/api/users/"+f.users[0].ID+"/ban", map[string]any{"expected_revision": rev.Decimal(), "reason": "policy", "duration_seconds": nil}, f.adminCookie, true)
	if banned.Code != http.StatusNoContent {
		t.Fatalf("ban: %d %s", banned.Code, banned.Body.String())
	}
	var state, finance string
	if err := f.store.DB().QueryRow("SELECT state,finance_state FROM image_activity_tasks WHERE id=?", toBan).Scan(&state, &finance); err != nil || state != "cancelled" || finance != "refunded" {
		t.Fatalf("ban missed queue: %s %s %v", state, finance, err)
	}
	ctx := context.Background()
	tx, err := f.store.DB().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := strconv.ParseInt(f.users[0].ID, 10, 64)
	for asset, want := range map[ledger.Asset]string{ledger.SketchPaper: "98000", ledger.SketchBrush: "19000"} {
		account, readErr := ledger.UserAssetAccount(ctx, tx, id, asset)
		if readErr != nil || account.Balance.Decimal() != want {
			tx.Rollback()
			t.Fatalf("ban refund %s: %s %v", asset, account.Balance.Decimal(), readErr)
		}
	}
	tx.Rollback()
	if blocked := f.request("GET", base+"/tasks/"+first+"/images/0", nil, f.users[0].Cookie, false); blocked.Code != http.StatusUnauthorized && blocked.Code != http.StatusForbidden {
		t.Fatalf("banned image access: %d", blocked.Code)
	}
	upstreamerror.CaptureEvent(f.app.Load().audits.observations.ErrorScope(ctx, observability.DiagnosticRef{TaskID: active, AttemptSeq: 1}), 503, "text/plain", []byte("temporary upstream failure"))
	deleted := lifecycleCall(1, "/api/account/delete", map[string]string{"confirm": "DELETE"})
	if deleted.Code != http.StatusNoContent {
		t.Fatalf("delete active image owner: %d %s", deleted.Code, deleted.Body.String())
	}
	var anonymous int
	if err := f.store.DB().QueryRow("SELECT count(*) FROM image_activity_tasks WHERE id=? AND user_id IS NULL AND model_id IS NULL AND paper_charge_mag IS NULL AND finance_state='deleted'", active).Scan(&anonymous); err != nil || anonymous != 1 {
		t.Fatalf("active task was not anonymized: %d %v", anonymous, err)
	}
	for _, query := range []string{
		"SELECT count(*) FROM image_task_sources WHERE task_id=?",
		"SELECT count(*) FROM request_error_bodies WHERE task_id=?",
	} {
		var count int
		if err := f.store.DB().QueryRow(query, active).Scan(&count); err != nil || count != 0 {
			t.Fatalf("deleted diagnostics remain: %d %v", count, err)
		}
	}
	f.upstreamState.release()
	deadline := time.Now().Add(20 * time.Second)
	for {
		var occupied int
		if err := f.store.DB().QueryRow("SELECT count(*) FROM image_activity_tasks WHERE id=? AND slot_state IN ('held','uncertain')", active).Scan(&occupied); err != nil {
			t.Fatal(err)
		}
		if occupied == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("anonymous execution did not release the upstream slot")
		}
		time.Sleep(20 * time.Millisecond)
	}
	var recreated int
	if err := f.store.DB().QueryRow("SELECT count(*) FROM users WHERE id=?", f.users[1].ID).Scan(&recreated); err != nil || recreated != 0 {
		t.Fatalf("late result recreated account: %d %v", recreated, err)
	}
}
