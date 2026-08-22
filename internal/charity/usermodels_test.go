package charity

// User-station price-table handler tests (frozen §6.3): the site switch gates
// the projection to an empty list, enabled models carry original plus
// currently discounted user prices, reward prices stay undiscounted, and no
// donated-key or base-URL material ever appears.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/db"
)

func setUserConfigValue(t *testing.T, st *db.Store, key, value string) {
	t.Helper()
	if _, err := st.DB().Exec(`INSERT INTO site_config (key, value, updated_at) VALUES (?, ?, 0)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, value); err != nil {
		t.Fatalf("set config %s: %v", key, err)
	}
}

func TestUserModelsSwitchOffIsEmptyList(t *testing.T) {
	f := newCharityFixture(t)
	handler := NewUserModelsHandler(UserModelsDeps{Store: f.st})

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/charity/models", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("switch-off status = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("cache-control = %q, want no-store", got)
	}
	var body struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Data) != 0 {
		t.Fatalf("switch-off data = %d entries, want 0", len(body.Data))
	}
}

func TestUserModelsProjectionPricesAndAvailability(t *testing.T) {
	f := newCharityFixture(t)
	setUserConfigValue(t, f.st, "charity_enabled", "1")

	rec, body := f.do(t, "POST", "/admin/api/charity-models", map[string]any{
		"provider": "donor", "model": "m1", "pricing_mode": "per_request", "enabled": true,
		"prices": map[string]any{"request_user_price": "500", "request_donor_reward": "100"},
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create = %d %v", rec.Code, body)
	}
	id := int64(body["id"].(float64))
	// 50% discount with a window spanning the read time so it is active.
	rec, _ = f.do(t, "PATCH", "/admin/api/charity-models/"+itoa64(id), map[string]any{
		"discount": map[string]any{"percent": 50, "enabled": true, "start_at": 1000, "end_at": 9999999999},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("discount patch = %d", rec.Code)
	}

	models, err := f.st.ListUserCharityModels(context.Background(), 5000, MaxUserModelsLimit)
	if err != nil {
		t.Fatalf("ListUserCharityModels: %v", err)
	}
	if len(models) != 1 {
		t.Fatalf("models = %d, want 1", len(models))
	}
	m := models[0]
	if m.RequestUserPrice != 500 || m.RequestDonorReward != 100 {
		t.Fatalf("original prices = %d/%d, want 500/100", m.RequestUserPrice, m.RequestDonorReward)
	}
	if m.CurrentRequestUserPrice != 250 {
		t.Fatalf("current price = %d, want 250 (50%% of 500)", m.CurrentRequestUserPrice)
	}
	// The admin-created model has no donated candidate chain in this fixture:
	// it is listed (price table) but flagged unavailable.
	if m.Available {
		t.Fatal("model without a candidate chain should not be available")
	}

	handler := NewUserModelsHandler(UserModelsDeps{Store: f.st})
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/charity/models", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("handler status = %d", rec.Code)
	}
	raw := rec.Body.String()
	for _, leaked := range []string{"endpoint_key", "base_url", "donation_key"} {
		if strings.Contains(raw, leaked) {
			t.Fatalf("response leaks %q: %s", leaked, raw)
		}
	}
	var out struct {
		Data []struct {
			Prices map[string]string `json:"prices"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out.Data) != 1 {
		t.Fatalf("entries = %d, want 1", len(out.Data))
	}
	prices := out.Data[0].Prices
	if prices["request_donor_reward_milli"] != "100" {
		t.Fatalf("reward over wire = %v, want \"100\" (undiscounted)", prices["request_donor_reward_milli"])
	}
	if prices["current_request_user_price_milli"] != "250" {
		t.Fatalf("current price over wire = %v, want \"250\"", prices["current_request_user_price_milli"])
	}
	if prices["request_user_price_milli"] != "500" {
		t.Fatalf("original price over wire = %v, want \"500\"", prices["request_user_price_milli"])
	}
}
