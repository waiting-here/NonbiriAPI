package app

import "testing"

func seedResourceHealthBrowser(t *testing.T, f *imageBrowserFixture, now int64, channel string) map[string]any {
	t.Helper()
	owner := f.users[0].Cookie
	endpoint := f.call("POST", "/api/endpoints", map[string]any{"source": "mainstream", "channel_id": channel, "note": "Service health example", "enabled": true}, owner, false)["id"].(string)
	key := f.call("POST", "/api/endpoints/"+endpoint+"/keys", map[string]any{"secret": "synthetic-service-health", "note": "Service health example", "enabled": true, "force_store_false": false, "ownership_confirmed": true}, owner, false)["id"].(string)
	shared := appFixtureSharedCatalog(t, f.store, key, []string{"health-upstream"}, now)
	shared["native_services"] = []string{"chat_completions", "embeddings", "images_generations"}
	donation := f.call("POST", "/api/donations", map[string]any{"description": "Service health example", "discord_public_thanks": false, "ownership_authorized": true, "keys": []map[string]any{shared}}, owner, false)
	var donated string
	if err := f.store.DB().QueryRow(`SELECT CAST(id AS TEXT) FROM donation_keys WHERE donation_id=? AND endpoint_key_id=?`, donation["id"], key).Scan(&donated); err != nil {
		t.Fatal(err)
	}
	model := f.call("POST", "/admin/api/charity-models", map[string]any{"provider": "health-browser", "model": "chat", "enabled": true, "is_mainstream": true, "model_types": []string{"chat_completions"}, "flatten_tool_calls": false, "pricing": map[string]any{"mode": "per_request", "user_price": "0", "donor_reward": "0"}, "discount": map[string]any{"enabled": false, "percent": 100, "start_at": nil, "end_at": nil}}, f.adminCookie, true)
	f.call("POST", "/admin/api/charity-models/"+model["id"].(string)+"/bindings/batch-receipt", map[string]any{"expected_binding_revision": "0", "selections": []map[string]any{{"targets": []map[string]any{{"donation_key_id": donated, "upstream_model_id": "health-upstream", "native_services": []string{"chat_completions"}}}}}}, f.adminCookie, true)
	if _, err := f.store.DB().Exec(`INSERT INTO donation_service_health(use_id,endpoint_key_id,native_service_bit,credential_version_id,streak_generation,failure_streak,failure_disabled,next_claim_seq,next_fold_seq,backoff_generation,backoff_episode,backoff_stage,updated_at)
SELECT u.id,dk.endpoint_key_id,u.native_service_bit,g.credential_version_id,nbi_u128(1),nbi_u128(10),1,nbi_u128(1),nbi_u128(1),1,0,0,?
FROM donation_uses u JOIN donation_keys dk ON dk.id=u.donation_key_id JOIN donation_use_revisions r ON r.id=u.current_approved_revision_id JOIN endpoint_key_grant_revisions g ON g.id=r.grant_revision_id WHERE dk.id=? AND u.native_service_bit=1`, now, donated); err != nil {
		t.Fatal(err)
	}
	return map[string]any{"donation_id": donation["id"], "key_id": donated, "model_id": model["id"]}
}
