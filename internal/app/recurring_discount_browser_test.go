package app

import "testing"

func seedRecurringDiscountBrowser(t *testing.T, f *imageBrowserFixture, now int64, channel string) map[string]any {
	t.Helper()
	owner := f.users[0].Cookie
	endpoint := f.call("POST", "/api/endpoints", map[string]any{"source": "mainstream", "channel_id": channel, "note": "Recurring prices", "enabled": true}, owner, false)["id"].(string)
	physical := f.call("POST", "/api/endpoints/"+endpoint+"/keys", map[string]any{"secret": "synthetic-recurring-discount", "note": "Recurring prices", "enabled": true, "force_store_false": false, "ownership_confirmed": true}, owner, false)["id"].(string)
	if _, err := f.store.DB().Exec(`INSERT INTO model_pair_catalog(endpoint_key_id,normalized_model_id,automatic_supports,manual_supports,automatic_revision,pair_revision,updated_at) VALUES(?,'recurring-upstream',1,0,1,1,?)`, physical, now); err != nil {
		t.Fatal(err)
	}
	donation := f.call("POST", "/api/donations", map[string]any{"description": "Recurring price examples", "discord_public_thanks": false, "ownership_authorized": true, "keys": []map[string]any{{"endpoint_key_id": physical, "expires_at": nil}}}, owner, false)
	var key string
	if err := f.store.DB().QueryRow("SELECT CAST(id AS TEXT) FROM donation_keys WHERE donation_id=? AND endpoint_key_id=?", donation["id"], physical).Scan(&key); err != nil {
		t.Fatal(err)
	}
	recurring := map[string]any{"enabled": true, "time_zone": "UTC", "default_percent": 50, "periods": []any{}}
	fixed := map[string]any{"enabled": false, "percent": 80, "start_at": nil, "end_at": nil}
	requestPrice := map[string]any{"mode": "per_request", "user_price": "3", "donor_reward": "1", "long_context": map[string]any{"input_threshold": "100", "user_price": "7"}, "context_penalty": map[string]any{"input_threshold": "200000", "fine": "5"}}
	slots := func(a, b, c, d string) map[string]any {
		return map[string]any{"uncached_input": a, "cache_write_input": b, "cache_read_input": c, "output": d}
	}
	prices := []map[string]any{requestPrice, {"mode": "per_token", "user_prices": slots("1", "2", "3", "4"), "donor_rewards": slots("0", "0", "0", "0"), "long_context": map[string]any{"input_threshold": "100", "user_prices": slots("2", "4", "6", "8")}, "context_penalty": map[string]any{"input_threshold": "200000", "fine": "5"}}}
	result := map[string]any{"donation_id": donation["id"], "key_id": key}
	for i, modelName := range []string{"request", "tokens"} {
		model := f.call("POST", "/admin/api/charity-models", map[string]any{"provider": "recurring-browser", "model": modelName, "enabled": true, "is_mainstream": true, "flatten_tool_calls": false, "pricing": prices[i], "token_reserve_credits": "5", "discount": fixed, "recurring_discount": recurring}, f.adminCookie, true)
		f.call("POST", "/admin/api/charity-models/"+model["id"].(string)+"/bindings/batch-receipt", map[string]any{"expected_binding_revision": "0", "selections": []map[string]any{{"donation_key_id": key, "upstream_model_id": "recurring-upstream"}}}, f.adminCookie, true)
		field := "request_model_id"
		if i == 1 {
			field = "token_model_id"
		}
		result[field] = model["id"]
	}
	template := f.call("POST", "/admin/api/charity-price-templates", map[string]any{"name": "Recurring half price", "description": "Every day in UTC", "enabled": true, "pricing": requestPrice, "token_reserve_credits": "5", "discount": fixed, "recurring_discount": recurring}, f.adminCookie, true)
	result["template_id"] = template["id"]
	return result
}
