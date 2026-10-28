package app

import (
	"net/http"
	"reflect"
	"testing"

	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/db"
)

type donationContextBrowserSample struct {
	ID          string   `json:"donation_id"`
	Description string   `json:"description"`
	Category    string   `json:"category"`
	Name        string   `json:"channel_name"`
	BaseURL     string   `json:"base_url"`
	Tags        []string `json:"tags"`
}

// This opt-in scene owns its users and resources so existing management
// scenarios retain their donations, sessions and permission settings.
func seedDonationContextBrowser(t *testing.T, f *imageBrowserFixture, now int64) map[string]any {
	t.Helper()
	var adminID int64
	if err := f.store.DB().QueryRow(`SELECT id FROM users WHERE is_admin=1`).Scan(&adminID); err != nil {
		t.Fatal(err)
	}
	owner := f.seedUser(120, 1, adminID)
	five, six := f.seedUser(121, 5, adminID), f.seedUser(122, 6, adminID)
	if _, err := f.store.DB().Exec(`UPDATE site_config SET value='1' WHERE key='donation_accept_enabled'`); err != nil {
		t.Fatal(err)
	}
	seed := func(category, name, base string, tags []string) donationContextBrowserSample {
		t.Helper()
		channel, err := db.GenerateOpaqueID("mch_")
		if err != nil {
			t.Fatal(err)
		}
		appFixtureMainstreamChannel(t, f.store, channel, name, base, connectorcontract.TypeOpenAICompatible, now)
		if _, err := f.store.DB().Exec(`UPDATE mainstream_channels SET category=? WHERE id=?`, category, channel); err != nil {
			t.Fatal(err)
		}
		endpoint := f.call(http.MethodPost, "/api/endpoints", map[string]any{"source": "mainstream", "channel_id": channel, "note": name, "enabled": true}, owner.Cookie, false)["id"].(string)
		donate := func(description string) map[string]any {
			t.Helper()
			key := f.call(http.MethodPost, "/api/endpoints/"+endpoint+"/keys", map[string]any{
				"secret": "synthetic-source-context-" + category, "note": name, "enabled": true, "force_store_false": false, "ownership_confirmed": true,
			}, owner.Cookie, false)["id"].(string)
			return f.call(http.MethodPost, "/api/donations", map[string]any{
				"description": description, "discord_public_thanks": false, "ownership_authorized": true,
				"keys": []map[string]any{appFixtureSharedCatalog(t, f.store, key, []string{"source-context-model"}, now)},
			}, owner.Cookie, false)
		}
		prior := donate("Source context " + category + " prior submission")
		f.call(http.MethodPost, "/admin/api/donations/"+prior["id"].(string)+"/review", map[string]any{
			"decision": "force_reject", "expected_revision": prior["revision"], "reason": "Synthetic source-context review requirement",
		}, f.adminCookie, true)
		description := "Source context " + category + " pending submission"
		pending := donate(description)
		if pending["status"] != "pending" {
			t.Fatalf("source-context donation is not pending: %v", pending["status"])
		}
		return donationContextBrowserSample{ID: pending["id"].(string), Description: description, Category: category, Name: name, BaseURL: base, Tags: tags}
	}
	subscription := seed("subscription", "Source context subscription", "https://subscription.review-context.example.test/v1", []string{"user_gateway"})
	api := seed("api_platform", "Source context API", "https://edge.api.review-context.example.test/v1", []string{"mainstream", "steward_gateway"})
	mutate := func(method, path string, body any) {
		t.Helper()
		response := f.request(method, path, body, f.adminCookie, true)
		if response.Code != http.StatusNoContent {
			t.Fatalf("source-context labels: %d %s", response.Code, response.Body.String())
		}
	}
	for _, entry := range []struct{ base, tag string }{{subscription.BaseURL, "user_gateway"}, {api.BaseURL, "mysterious"}} {
		mutate(http.MethodPatch, "/admin/api/overview/endpoints/tags", map[string]any{"base_urls": []string{entry.base}, "tag": entry.tag, "add": true})
	}
	mutate(http.MethodPut, "/admin/api/overview/endpoints/tag-rules", map[string]any{"pattern": "*.api.review-context.example.test", "action": "remove", "tags": []string{}})
	mutate(http.MethodPut, "/admin/api/overview/endpoints/tag-rules", map[string]any{"pattern": "*.edge.api.review-context.example.test", "action": "add", "tags": api.Tags})
	samples := []donationContextBrowserSample{subscription, api}
	for _, sample := range samples {
		value := f.call(http.MethodGet, "/admin/api/donations/"+sample.ID, nil, f.adminCookie, true)
		source := value["keys"].([]any)[0].(map[string]any)["safe_source"].(map[string]any)
		tags := []string{}
		for _, tag := range source["endpoint_tags"].([]any) {
			tags = append(tags, tag.(string))
		}
		if source["category"] != sample.Category || !reflect.DeepEqual(tags, sample.Tags) {
			t.Fatalf("source-context projection: %v", source)
		}
	}
	return map[string]any{"owner": owner, "stewards": map[string]imageBrowserUser{"five": five, "six": six}, "donations": samples}
}
