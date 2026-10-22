package app

import (
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/headerfacts"
)

// Large browser datasets are isolated from the ordinary management fixture.
// Both active resources and retained history use product routes.
func seedWorkflowBrowser(t *testing.T, f *imageBrowserFixture, management map[string]any, userURL string) map[string]any {
	t.Helper()
	owner := f.users[1]
	if _, err := f.store.DB().Exec(`UPDATE site_config SET value='100' WHERE key='default_endpoint_key_limit'`); err != nil {
		t.Fatal(err)
	}
	requestPrice := map[string]any{"pricing": map[string]any{"mode": "per_request", "user_price": "3.5", "donor_reward": "1.75"},
		"token_reserve_credits": nil, "discount": map[string]any{"enabled": false, "percent": 0, "start_at": nil, "end_at": nil}}
	prices := func(a, b, c, d string) map[string]any {
		return map[string]any{"uncached_input": a, "cache_write_input": b, "cache_read_input": c, "output": d}
	}
	tokenPrice := map[string]any{"pricing": map[string]any{"mode": "per_token", "user_prices": prices("20", "24", "2", "60"), "donor_rewards": prices("10", "12", "1", "30"),
		"long_context":    map[string]any{"input_threshold": "100000", "user_prices": prices("40", "48", "4", "120")},
		"context_penalty": map[string]any{"input_threshold": "200000", "fine": "500"}},
		"token_reserve_credits": "25.125", "discount": map[string]any{"enabled": true, "percent": 80, "start_at": nil, "end_at": nil}}
	copyPrice := func(source map[string]any) map[string]any {
		out := map[string]any{}
		for k, v := range source {
			out[k] = v
		}
		return out
	}
	models := make([]string, 240)
	for i := range models {
		body := copyPrice(requestPrice)
		if i == 239 {
			body = copyPrice(tokenPrice)
		}
		body["provider"], body["model"] = "workflow", fmt.Sprintf("model-%03d", i+1)
		body["enabled"], body["is_mainstream"], body["flatten_tool_calls"] = i != 1, i == 0, false
		body["allowed_levels"], body["public_description"] = []int{1, 2, 3, 4, 5, 6}, "Synthetic workflow model"
		models[i] = f.call("POST", "/admin/api/charity-models", body, f.adminCookie, true)["id"].(string)
	}
	templates := make([]string, 0, 2)
	for _, preset := range []struct {
		name   string
		config map[string]any
	}{{"Workflow token complete", tokenPrice}, {"Workflow request zero discount", requestPrice}} {
		body := copyPrice(preset.config)
		body["name"], body["description"], body["enabled"] = preset.name, "Synthetic complete price snapshot", true
		templates = append(templates, f.call("POST", "/admin/api/charity-price-templates", body, f.adminCookie, true)["id"].(string))
	}
	endpoint := f.call("POST", "/api/endpoints", map[string]any{"source": "mainstream", "channel_id": management["channel_id"], "note": "Workflow service", "enabled": true}, owner.Cookie, false)["id"].(string)
	donate := func(endpoint, description, secret string, count int) map[string]any {
		keys := make([]map[string]any, count)
		for i := range keys {
			body := secret
			if count > 1 {
				body += fmt.Sprintf("-%d", i+1)
			}
			key := f.call("POST", "/api/endpoints/"+endpoint+"/keys", map[string]any{"secret": body, "note": "Workflow credential", "enabled": true, "force_store_false": false, "ownership_confirmed": true}, owner.Cookie, false)["id"].(string)
			keys[i] = map[string]any{"endpoint_key_id": key, "expires_at": nil}
		}
		return f.call("POST", "/api/donations", map[string]any{"description": description, "discord_public_thanks": false, "ownership_authorized": true, "keys": keys}, owner.Cookie, false)
	}
	prior := donate(endpoint, "Workflow prior review", "synthetic-workflow-reviewed", 1)
	f.call("POST", "/admin/api/donations/"+prior["id"].(string)+"/review", map[string]any{"decision": "force_reject", "expected_revision": prior["revision"], "reason": "Synthetic review required"}, f.adminCookie, true)
	pending := donate(endpoint, "Workflow awaiting review", "synthetic-workflow-reviewed", 1)
	pendingID := pending["id"].(string)
	detail := f.call("GET", "/admin/api/donations/"+pendingID, nil, f.adminCookie, true)
	keyID := detail["keys"].([]any)[0].(map[string]any)["id"].(string)
	catalog := "/admin/api/donations/" + pendingID + "/keys/" + keyID + "/models"
	for start := 0; start < 144; start += 100 {
		current := f.call("GET", catalog+"?page=1&page_size=20", nil, f.adminCookie, true)
		entries := make([]string, 0, 100)
		for i := start; i < min(start+100, 144); i++ {
			entries = append(entries, fmt.Sprintf("provider/model-%03d-2026-10", i+1))
		}
		f.call("POST", catalog+"/manual", map[string]any{"expected_manual_catalog_revision": current["manual_catalog_revision"], "entries": entries}, f.adminCookie, true)
	}
	custom := f.call("POST", "/api/endpoints", map[string]any{"source": "custom", "connector_type": "openai-compatible", "base_url": f.upstream.URL + "/workflow/v1", "note": "Workflow custom service", "enabled": true}, owner.Cookie, false)["id"].(string)
	processing := donate(custom, "Workflow whole donation four keys", "synthetic-workflow-whole", 4)
	processingID := processing["id"].(string)
	processingDetail := f.call("GET", "/admin/api/donations/"+processingID, nil, f.adminCookie, true)
	if processingDetail["status"] == "pending" {
		settings := make([]map[string]any, 0, 4)
		for _, value := range processingDetail["keys"].([]any) {
			key := value.(map[string]any)
			setting := map[string]any{"donation_key_id": key["id"], "price_limit": nil, "calls_limit": nil, "tokens_limit": nil, "token_reserve": 0, "enabled": false, "safe_note": "", "expires_at": nil}
			if review := key["review"].(map[string]any); review["required"] == true {
				setting["expected_review_revision"] = review["revision"]
			}
			settings = append(settings, setting)
		}
		f.call("POST", "/admin/api/donations/"+processingID+"/review", map[string]any{"decision": "approve", "expected_revision": processingDetail["revision"], "reason": "Synthetic approval", "key_settings": settings}, f.adminCookie, true)
	}
	ended := donate(endpoint, "Workflow ended donation", "synthetic-workflow-ended", 24)
	endedPath := "/api/donations/" + ended["id"].(string)
	endedCurrent := f.call("GET", endedPath, nil, owner.Cookie, false)
	if endedCurrent["status"] == "approved" {
		f.call("POST", endedPath+"/terminate", map[string]any{"expected_revision": endedCurrent["revision"], "confirmation": "terminate"}, owner.Cookie, false)
	} else {
		f.call("POST", endedPath+"/withdraw", map[string]any{"expected_revision": endedCurrent["revision"]}, owner.Cookie, false)
	}
	physicalKey := endedCurrent["keys"].([]any)[0].(map[string]any)["endpoint_key_id"]
	for i := 1; i <= 165; i++ {
		historical := f.call("POST", "/api/donations", map[string]any{"description": fmt.Sprintf("Workflow historical %03d", i), "discord_public_thanks": false, "ownership_authorized": true, "keys": []map[string]any{{"endpoint_key_id": physicalKey, "expires_at": nil}}}, owner.Cookie, false)
		path := "/api/donations/" + historical["id"].(string)
		current := f.call("GET", path, nil, owner.Cookie, false)
		if current["status"] == "approved" {
			f.call("POST", path+"/terminate", map[string]any{"expected_revision": current["revision"], "confirmation": "terminate"}, owner.Cookie, false)
		} else {
			f.call("POST", path+"/withdraw", map[string]any{"expected_revision": current["revision"]}, owner.Cookie, false)
		}
	}
	shortcutDonation := donate(endpoint, "Workflow log shortcut keys", "synthetic-workflow-shortcut", 2)
	shortcutRequest, shortcutTargets := seedWorkflowLogShortcut(t, f, management["charity_model_id"].(string), shortcutDonation["id"].(string))
	requestID := seedWorkflowHeaderRequest(t, f, management, userURL)
	return map[string]any{"owner": owner, "models": models, "templates": templates, "pending_donation_id": pendingID, "pending_key_id": keyID,
		"processing_donation_id": processingID, "ended_donation_id": ended["id"], "header_request_id": requestID,
		"shortcut_request_id": shortcutRequest, "shortcut_model_id": management["charity_model_id"], "shortcut_attempt_targets": shortcutTargets}
}

func seedWorkflowLogShortcut(t *testing.T, f *imageBrowserFixture, modelID, donationID string) (string, []map[string]string) {
	t.Helper()
	donation := f.call("GET", "/admin/api/donations/"+donationID, nil, f.adminCookie, true)
	keys := donation["keys"].([]any)
	if donation["status"] != "approved" || len(keys) != 2 {
		t.Fatal("shortcut donation must contain two approved mainstream keys")
	}
	at := f.now().Unix() - 3
	requestID, err := db.GenerateOpaqueID("req_")
	if err != nil {
		t.Fatal(err)
	}
	tx, err := f.store.DB().Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := tx.Exec(query, args...); err != nil {
			t.Fatal(err)
		}
	}
	var model string
	if err := tx.QueryRow(`SELECT full_name FROM charity_models WHERE id=? AND is_mainstream=1`, modelID).Scan(&model); err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO logical_requests(id,user_id,route_kind,model_snapshot,state,attempt_limit,caller_result_class,caller_status,caller_error_code,accounting_state,settlement_destination,ledger_rows_remaining,created_at,terminal_at)
VALUES(?,?,'charity_chat_completions',?,'terminal',2,'failed',503,'upstream','released','user',zeroblob(16),?,?)`, requestID, f.users[1].ID, model, at, at+1)
	result, err := tx.Exec(`INSERT INTO request_logs(logical_request_id,user_id,route_kind,model,started_at,completed_at,caller_result_class,caller_status,caller_error_code,status_code,attempt_count,error_source,error_code,error_diag,duration_ms)
VALUES(?,?,'charity_chat_completions',?, ?,?,'failed',503,'upstream',503,2,'upstream','upstream','Synthetic safe failure summary',1000)`, requestID, f.users[1].ID, model, at, at+1)
	if err != nil {
		t.Fatal(err)
	}
	logID, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	targets := make([]map[string]string, 0, 2)
	for i, value := range keys {
		keyID := value.(map[string]any)["id"].(string)
		claimID, err := db.GenerateOpaqueID("clm_")
		if err != nil {
			t.Fatal(err)
		}
		exec(`INSERT INTO request_attempts(claim_id,request_log_id,attempt_seq,connector_type,canonical_base_url,upstream_model_id,result_kind,upstream_status,diag,started_at,completed_at)
VALUES(?,?,?,'openai-compatible','https://synthetic.invalid/v1','synthetic/audit-model','response',503,'Synthetic upstream failure',?,?)`, claimID, logID, i+1, at, at+1)
		exec(`INSERT INTO dispatch_claims(id,logical_request_id,attempt_seq,purpose,donation_key_id,claim_now,state,dispatched_at,terminal_at,donor_reward_actual_milli,donor_reward_state,streak_disposition,failure_origin)
VALUES(?,?,?,'charity',?,?,'committed',?,?,0,'zero','upstream_failure','upstream_response')`, claimID, requestID, i+1, keyID, at, at, at+1)
		targets = append(targets, map[string]string{"donation_id": donationID, "donation_key_id": keyID})
	}
	exec(`INSERT INTO charity_reservations(logical_request_id,user_id,charity_model_id,model_snapshot,state,pricing_mode,discount_percent,
request_user_price_milli,request_donor_reward_milli,uncached_user_price_milli,cache_write_user_price_milli,cache_read_user_price_milli,output_user_price_milli,
uncached_donor_reward_milli,cache_write_donor_reward_milli,cache_read_donor_reward_milli,output_donor_reward_milli,
token_reserve_milli,user_reserved_milli,original_charge_milli,user_charge_milli,donor_reward_total_mag,created_at,finalized_at,updated_at)
VALUES(?,?,?,?,'released','per_request',100,0,0,0,0,0,0,0,0,0,0,0,0,0,0,zeroblob(16),?,?,?)`, requestID, f.users[1].ID, modelID, model, at, at+1, at+1)
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return requestID, targets
}

func TestWorkflowLogShortcutSeedRespectsSchemaAndTraineeScope(t *testing.T) {
	f := newActivityWireFixture(t)
	now := f.now().Unix()
	if _, err := f.store.DB().Exec(`UPDATE site_config SET value='1' WHERE key IN ('donation_accept_enabled','charity_enabled')`); err != nil {
		t.Fatal(err)
	}
	channel, err := db.GenerateOpaqueID("mch_")
	if err != nil {
		t.Fatal(err)
	}
	appFixtureMainstreamChannel(t, f.store, channel, "Shortcut service", f.upstream.URL+"/v1", connectorcontract.TypeOpenAICompatible, now)
	owner := f.users[1].Cookie
	endpoint := f.call("POST", "/api/endpoints", map[string]any{"source": "mainstream", "channel_id": channel, "note": "Shortcut service", "enabled": true}, owner, false)["id"].(string)
	keys := make([]map[string]any, 0, 2)
	for i := range 2 {
		key := f.call("POST", "/api/endpoints/"+endpoint+"/keys", map[string]any{"secret": fmt.Sprintf("synthetic-shortcut-%d", i), "note": "Shortcut credential", "enabled": true, "force_store_false": false, "ownership_confirmed": true}, owner, false)["id"].(string)
		keys = append(keys, map[string]any{"endpoint_key_id": key, "expires_at": nil})
	}
	donation := f.call("POST", "/api/donations", map[string]any{"description": "Shortcut donation", "discord_public_thanks": false, "ownership_authorized": true, "keys": keys}, owner, false)["id"].(string)
	model := f.call("POST", "/admin/api/charity-models", map[string]any{
		"provider": "browser", "model": "shortcut", "enabled": true, "is_mainstream": true, "flatten_tool_calls": false,
		"pricing": map[string]any{"mode": "per_request", "user_price": "0", "donor_reward": "0"}, "discount": map[string]any{"enabled": false, "percent": 100, "start_at": nil, "end_at": nil},
	}, f.adminCookie, true)["id"].(string)
	requestID, targets := seedWorkflowLogShortcut(t, f, model, donation)
	detail := f.call("GET", "/admin/api/logs/"+requestID, nil, f.adminCookie, true)
	if detail["request"].(map[string]any)["charity_model_id"] != model {
		t.Fatalf("model target = %v", detail["request"])
	}
	attempts := detail["attempts"].(map[string]any)["data"].([]any)
	if len(attempts) != 2 || targets[0]["donation_key_id"] == targets[1]["donation_key_id"] {
		t.Fatalf("attempt targets = %v, %v", attempts, targets)
	}
	for i, target := range targets {
		attempt := attempts[i].(map[string]any)
		if attempt["donation_id"] != target["donation_id"] || attempt["donation_key_id"] != target["donation_key_id"] {
			t.Fatalf("attempt %d target = %v, want %v", i, attempt, target)
		}
		path := "/api/steward/donations/" + target["donation_id"] + "/keys/" + target["donation_key_id"] + "?charity_model_id=" + model
		managed := f.call("GET", path, nil, f.users[2].Cookie, false)
		if managed["id"] != target["donation_key_id"] {
			t.Fatalf("trainee key target = %v", managed)
		}
	}
}

func seedWorkflowHeaderRequest(t *testing.T, f *imageBrowserFixture, management map[string]any, userURL string) string {
	t.Helper()
	var before int64
	if err := f.store.DB().QueryRow(`SELECT COALESCE(MAX(id),0) FROM request_logs`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	f.upstreamState.mu.Lock()
	f.upstreamState.geminiDiscoveryMode = "success"
	f.upstreamState.mu.Unlock()
	path := fmt.Sprintf("/api/endpoints/%s/keys/%s/models/refresh", management["gemini_endpoint_id"], management["gemini_key_id"])
	request, err := http.NewRequest(http.MethodPost, userURL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.AddCookie(f.users[0].Cookie)
	request.Header.Set("Origin", userURL)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", fmt.Sprintf("%022d", f.sequence.Add(1)))
	request.Header["User-Agent"] = []string{"WorkflowBrowser/1.0", "WorkflowBrowser/2.0"}
	request.Header["X-Workflow-Client"] = []string{"198.51.100.71", "198.51.100.72"}
	client := &http.Client{Timeout: 10 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("workflow discovery: %d %s", response.StatusCode, body)
	}
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		var requestID string
		var encoded []byte
		err := f.store.DB().QueryRow(`SELECT l.logical_request_id,h.encoded FROM request_logs l JOIN request_header_facts h ON h.request_log_id=l.id AND h.attempt_seq=1 WHERE l.id>? AND l.route_kind='model_discovery' AND h.save_state='saved' ORDER BY l.id DESC LIMIT 1`, before).Scan(&requestID, &encoded)
		if err == nil {
			view, decodeErr := headerfacts.DecodeCollection("saved", encoded, false)
			if decodeErr == nil && len(view.Responses) == 2 {
				return requestID
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("workflow discovery header facts did not settle")
	return ""
}
