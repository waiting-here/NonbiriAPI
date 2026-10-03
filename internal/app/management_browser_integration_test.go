package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/auth"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/elevation"
	"github.com/waiting-here/NonbiriAPI/internal/observability"
)

// The opt-in management fixture changes only its private test database. Product
// routes create resources and donations; account deletion uses the coordinator.
func seedManagementBrowser(t *testing.T, f *imageBrowserFixture, now int64) map[string]any {
	t.Helper()
	ctx := context.Background()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := f.store.DB().Exec(query, args...); err != nil {
			t.Fatal(err)
		}
	}
	var admin int64
	if err := f.store.DB().QueryRow(`SELECT id FROM users WHERE is_admin=1`).Scan(&admin); err != nil {
		t.Fatal(err)
	}
	const historyDiscord = "100000000000000101"
	old := f.seedUser(10, 1, admin, historyDiscord)
	oldRequest := seedAuditBrowserRequest(t, f, old.ID, "openai_chat_completions", "self", now-5, "198.51.100.81")
	oldID, _ := strconv.ParseInt(old.ID, 10, 64)
	oldBinding := sha256.Sum256([]byte(old.Cookie.Value))
	token, _, err := f.app.Load().authRuntime.ElevationManager().IssueBound(oldID, elevation.KindUser, fmt.Sprintf("%x", oldBinding))
	if err != nil {
		t.Fatal(err)
	}
	deleted := testApplicationRequest(t, f.app.Load().handler, http.MethodPost, f.cfg.UserHost, "/api/account/delete", `{"confirm":"DELETE"}`, []*http.Cookie{old.Cookie}, map[string]string{
		"Origin": "http://" + f.cfg.UserHost, "Content-Type": "application/json", "X-Elevated-Token": token})
	if deleted.Code != http.StatusNoContent {
		t.Fatalf("historical deletion: %d %s", deleted.Code, deleted.Body.String())
	}
	var historyID string
	if err := f.store.DB().QueryRow(`SELECT CAST(alert_id AS TEXT) FROM admin_account_deletions WHERE former_user_id=?`, oldID).Scan(&historyID); err != nil {
		t.Fatal(err)
	}
	current := f.seedUser(11, 1, admin, historyDiscord)
	disposable := f.seedUser(15, 1, admin, "100000000000000301")
	requests := []string{oldRequest}
	for i, ip := range []string{"198.51.100.82", "198.51.100.83"} {
		requests = append(requests, seedAuditBrowserRequest(t, f, current.ID, "openai_chat_completions", "self", now+int64(i), ip))
	}
	denials := map[string]any{}
	for i, kind := range []string{"client_rules", "charity", "manual"} {
		discord := fmt.Sprintf("1000000000000002%02d", i)
		user := f.seedUser(12+i, 1, admin, discord)
		manual := "Synthetic manual reason <em>kept as text</em>"
		exec(`UPDATE users SET is_banned=1,banned_until=NULL,banned_reason=?,lang='en' WHERE id=?`, manual, user.ID)
		if kind == "client_rules" {
			tx, err := f.store.DB().BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			err = observability.PutAutomaticReasonTx(ctx, tx, "user_ban", user.ID, observability.AutomaticReason{
				Kind: "client_rules", SchemaVersion: 1, Params: json.RawMessage(`{}`), ManualText: manual,
				Rules: []observability.ReasonRuleLabel{{RuleID: "synthetic-rule", Revision: 1, Name: "Synthetic client rule"}},
			})
			if err == nil {
				err = tx.Commit()
			} else {
				_ = tx.Rollback()
			}
			if err != nil {
				t.Fatal(err)
			}
		} else if kind == "charity" {
			id, err := db.GenerateOpaqueID("abc_")
			if err != nil {
				t.Fatal(err)
			}
			exec(`INSERT INTO abuse_cases(id,user_id,kind,reason_code,started_at,state,result) VALUES(?,?,'ban','charity_rpm',?,'active','applied')`, id, user.ID, now)
		}
		// Seed the external verification result, retaining the production grant
		// format, expiry, safe projection and lack of session authority.
		token := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{byte(130 + i)}, 32))
		digest := sha256.Sum256([]byte(token))
		exec(`INSERT INTO auth_denial_grants(token_hash,discord_id,cursor_domain,created_at,expires_at) VALUES(?,?,?,?,?)`, digest[:], discord, bytes.Repeat([]byte{byte(140 + i)}, 32), now, now+300)
		denials[kind] = map[string]any{"user_id": user.ID, "cookie": &http.Cookie{Name: auth.DenialCookieName, Value: token, Path: "/api/auth"}}
	}
	exec(`UPDATE site_config SET value='1' WHERE key IN ('donation_accept_enabled','charity_enabled')`)
	channel, err := db.GenerateOpaqueID("mch_")
	if err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO mainstream_channels(id,name,category,connector_type,canonical_base_url,enabled,state,revision,created_at,updated_at) VALUES(?,'Browser service','subscription','openai-compatible',?,1,'active',1,?,?)`, channel, f.upstream.URL+"/v1", now, now)
	owner := f.users[0].Cookie
	endpoint := f.call("POST", "/api/endpoints", map[string]any{"source": "mainstream", "channel_id": channel, "note": "Browser service", "enabled": true}, owner, false)["id"].(string)
	donate := func(secret, description string) map[string]any {
		key := f.call("POST", "/api/endpoints/"+endpoint+"/keys", map[string]any{"secret": secret, "note": "Browser credential", "enabled": true, "force_store_false": false, "ownership_confirmed": true}, owner, false)["id"].(string)
		return f.call("POST", "/api/donations", map[string]any{"description": description, "discord_public_thanks": false, "ownership_authorized": true, "keys": []map[string]any{{"endpoint_key_id": key, "expires_at": nil}}}, owner, false)
	}
	automatic := donate("synthetic-auto-donation", "Automatically approved browser donation")
	prior := donate("synthetic-reviewed-donation", "Initial review source")
	f.call("POST", "/admin/api/donations/"+prior["id"].(string)+"/review", map[string]any{"decision": "force_reject", "expected_revision": prior["revision"], "reason": "Synthetic credential review required"}, f.adminCookie, true)
	pending := donate("synthetic-reviewed-donation", "Browser donation awaiting credential review")
	model := f.call("POST", "/admin/api/charity-models", map[string]any{
		"provider": "browser", "model": "management", "enabled": true, "is_mainstream": true, "flatten_tool_calls": false,
		"pricing":  map[string]any{"mode": "per_request", "user_price": "0", "donor_reward": "0"},
		"discount": map[string]any{"enabled": false, "percent": 100, "start_at": nil, "end_at": nil},
	}, f.adminCookie, true)
	return map[string]any{"old_user_id": old.ID, "current_user": current, "history_discord_id": historyDiscord, "history_record_id": historyID, "request_ids": requests,
		"disposable_user_id": disposable.ID,
		"denials":            denials, "channel_id": channel, "endpoint_id": endpoint, "automatic_donation_id": automatic["id"], "pending_donation_id": pending["id"], "charity_model_id": model["id"]}
}
