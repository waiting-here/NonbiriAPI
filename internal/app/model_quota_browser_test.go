package app

import "testing"

func seedModelQuotaBrowser(t *testing.T, f *imageBrowserFixture, now int64, channel string) map[string]any {
	t.Helper()
	owner := f.users[0].Cookie
	endpoint := f.call("POST", "/api/endpoints", map[string]any{"source": "mainstream", "channel_id": channel, "note": "Model quota example", "enabled": true}, owner, false)["id"].(string)
	physical := f.call("POST", "/api/endpoints/"+endpoint+"/keys", map[string]any{"secret": "synthetic-model-quota", "note": "Model quota example", "enabled": true, "force_store_false": false, "ownership_confirmed": true}, owner, false)["id"].(string)
	names := []string{"quota-upstream-a", "quota-upstream-b"}
	donation := f.call("POST", "/api/donations", map[string]any{"description": "Independent model quotas", "discord_public_thanks": false, "ownership_authorized": true, "keys": []map[string]any{appFixtureSharedCatalog(t, f.store, physical, names, now)}}, owner, false)
	var key string
	if err := f.store.DB().QueryRow("SELECT CAST(id AS TEXT) FROM donation_keys WHERE donation_id=? AND endpoint_key_id=?", donation["id"], physical).Scan(&key); err != nil {
		t.Fatal(err)
	}
	models := []string{}
	for _, name := range names {
		model := f.call("POST", "/admin/api/charity-models", map[string]any{"provider": "quota-browser", "model": name, "enabled": true, "is_mainstream": true, "flatten_tool_calls": false, "pricing": map[string]any{"mode": "per_request", "user_price": "0", "donor_reward": "0"}, "discount": map[string]any{"enabled": false, "percent": 100, "start_at": nil, "end_at": nil}}, f.adminCookie, true)
		models = append(models, model["id"].(string))
		f.call("POST", "/admin/api/charity-models/"+model["id"].(string)+"/bindings/batch-receipt", map[string]any{"expected_binding_revision": "0", "selections": []map[string]any{{"donation_key_id": key, "upstream_model_id": name}}}, f.adminCookie, true)
	}
	// Seed a settled usage projection; browser actions configure real limits on
	// this history. Reservation and settlement transitions have SQLite tests.
	if _, err := f.store.DB().Exec(`UPDATE donation_keys SET token_reserve=5,input_token_reserve=2,output_token_reserve=3,tokens_used=nbi_u128(5),input_tokens_used=nbi_u128(2),output_tokens_used=nbi_u128(3) WHERE id=?`, key); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.DB().Exec(`INSERT INTO donation_model_quotas(donation_key_id,upstream_model_id,tokens_used,input_tokens_used,output_tokens_used,created_at) VALUES(?, ?,nbi_u128(5),nbi_u128(2),nbi_u128(3),?)`, key, names[0], now); err != nil {
		t.Fatal(err)
	}
	return map[string]any{"donation_id": donation["id"], "key_id": key, "upstream_models": names, "model_ids": models}
}
