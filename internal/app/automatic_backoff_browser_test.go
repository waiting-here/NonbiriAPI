package app

import (
	"fmt"
	"testing"
)

func seedAutomaticBackoffBrowser(t *testing.T, f *imageBrowserFixture, now int64, channel string) map[string]any {
	t.Helper()
	owner := f.users[0].Cookie
	endpoint := f.call("POST", "/api/endpoints", map[string]any{"source": "mainstream", "channel_id": channel, "note": "Automatic backoff browser states", "enabled": true}, owner, false)["id"].(string)
	physical := []string{}
	keys := []map[string]any{}
	for i := range 3 {
		key := f.call("POST", "/api/endpoints/"+endpoint+"/keys", map[string]any{"secret": fmt.Sprint("synthetic-backoff-", i), "note": fmt.Sprint("Backoff state ", i+1), "enabled": true, "force_store_false": false, "ownership_confirmed": true}, owner, false)["id"].(string)
		physical = append(physical, key)
		keys = append(keys, map[string]any{"endpoint_key_id": key, "expires_at": nil})
		if _, err := f.store.DB().Exec(`INSERT INTO model_pair_catalog(endpoint_key_id,normalized_model_id,automatic_supports,manual_supports,automatic_revision,pair_revision,updated_at) VALUES(?,'backoff-upstream',1,0,1,1,?)`, key, now); err != nil {
			t.Fatal(err)
		}
	}
	d := f.call("POST", "/api/donations", map[string]any{"description": "Automatic backoff states", "discord_public_thanks": false, "ownership_authorized": true, "keys": keys}, owner, false)
	keyIDs := []string{}
	selections := []map[string]any{}
	for i, key := range physical {
		var id string
		if err := f.store.DB().QueryRow("SELECT CAST(id AS TEXT) FROM donation_keys WHERE donation_id=? AND endpoint_key_id=?", d["id"], key).Scan(&id); err != nil {
			t.Fatal(err)
		}
		keyIDs = append(keyIDs, id)
		selections = append(selections, map[string]any{"donation_key_id": id, "upstream_model_id": "backoff-upstream"})
		if _, err := f.store.DB().Exec("UPDATE donation_keys SET safe_note=? WHERE id=?", []string{"Historical quota wait", "Response-header wait", "Ready for quota trial"}[i], id); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.store.DB().Exec(`INSERT INTO donation_key_backoffs(donation_key_id,generation,episode,period,source,stage,retry_at,updated_at) VALUES(?,7,3,'day','custom_rule',1,?,?), (?,2,1,'week','builtin',0,?,?)`, keyIDs[0], now+7200, now, keyIDs[2], now-60, now); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.DB().Exec("INSERT INTO donation_key_cooldowns VALUES(?,?,?)", keyIDs[0], now+3600, now); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.DB().Exec("UPDATE donation_keys SET enabled=0 WHERE id=?", keyIDs[1]); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.DB().Exec("INSERT INTO donation_key_cooldowns VALUES(?,?,?)", keyIDs[1], now+7200, now); err != nil {
		t.Fatal(err)
	}
	model := f.call("POST", "/admin/api/charity-models", map[string]any{"provider": "backoff-browser", "model": "states", "enabled": true, "is_mainstream": true, "flatten_tool_calls": false, "pricing": map[string]any{"mode": "per_request", "user_price": "0", "donor_reward": "0"}, "discount": map[string]any{"enabled": false, "percent": 100, "start_at": nil, "end_at": nil}}, f.adminCookie, true)
	f.call("POST", "/admin/api/charity-models/"+model["id"].(string)+"/bindings/batch-receipt", map[string]any{"expected_binding_revision": "0", "selections": selections}, f.adminCookie, true)
	return map[string]any{"donation_id": d["id"], "key_ids": keyIDs, "model_id": model["id"]}
}
