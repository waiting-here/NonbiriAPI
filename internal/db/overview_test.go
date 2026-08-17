package db

// Contract tests for the admin overview projections: metadata-only listings
// across all users with live key/binding counts, excluding every secret and
// ciphertext column.

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func ovSeedEndpoint(t *testing.T, store *Store, userID int64, baseURL, note string, enabled bool) int64 {
	t.Helper()
	now := time.Now().Unix()
	enabledInt := 0
	if enabled {
		enabledInt = 1
	}
	res, err := store.DB().Exec(`INSERT INTO endpoints (user_id, connector_type, base_url, note, enabled, created_at, updated_at) VALUES (?, 'openai-compatible', ?, ?, ?, ?, ?)`,
		userID, baseURL, note, enabledInt, now, now)
	if err != nil {
		t.Fatalf("seed endpoint: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("seed endpoint last id: %v", err)
	}
	return id
}

func ovSeedKey(t *testing.T, store *Store, endpointID int64) int64 {
	t.Helper()
	now := time.Now().Unix()
	res, err := store.DB().Exec(`INSERT INTO endpoint_keys (endpoint_id, encrypted_secret, display_head, display_tail, created_at, updated_at) VALUES (?, 'nbsec:v1:aes-256-gcm:test-only', 'abcd', 'wxyz', ?, ?)`,
		endpointID, now, now)
	if err != nil {
		t.Fatalf("seed endpoint key: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("seed endpoint key last id: %v", err)
	}
	return id
}

func ovSeedModel(t *testing.T, store *Store, userID int64, provider, model string, silentRetry bool) int64 {
	t.Helper()
	now := time.Now().Unix()
	retryInt := 0
	if silentRetry {
		retryInt = 1
	}
	res, err := store.DB().Exec(`INSERT INTO models (user_id, provider, model, full_name, route_strategy, silent_retry, created_at, updated_at) VALUES (?, ?, ?, ?, 'ordered', ?, ?, ?)`,
		userID, provider, model, provider+"/"+model, retryInt, now, now)
	if err != nil {
		t.Fatalf("seed model: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("seed model last id: %v", err)
	}
	return id
}

func ovSeedBinding(t *testing.T, store *Store, modelID, endpointKeyID int64, upstreamModelID string, ord int) {
	t.Helper()
	now := time.Now().Unix()
	if _, err := store.DB().Exec(`INSERT INTO model_bindings (model_id, endpoint_key_id, upstream_model_id, ord, created_at) VALUES (?, ?, ?, ?, ?)`,
		modelID, endpointKeyID, upstreamModelID, ord, now); err != nil {
		t.Fatalf("seed binding: %v", err)
	}
}

func TestEndpointsOverviewProjection(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "overview.db"))
	defer store.Close()
	u1 := seedUsageUser(t, store, "u1")
	u2 := seedUsageUser(t, store, "u2")

	e1 := ovSeedEndpoint(t, store, u1, "https://one.example", "note one", true)
	ovSeedKey(t, store, e1)
	ovSeedKey(t, store, e1)
	e2 := ovSeedEndpoint(t, store, u2, "https://two.example", "note two", false)
	ovSeedKey(t, store, e2)
	e3 := ovSeedEndpoint(t, store, u1, "https://three.example", "", true)

	eps, err := store.ListEndpointsOverview(context.Background())
	if err != nil {
		t.Fatalf("ListEndpointsOverview: %v", err)
	}
	if len(eps) != 3 {
		t.Fatalf("overview rows = %d, want 3: %+v", len(eps), eps)
	}
	want := []EndpointOverview{
		{ID: e1, UserID: u1, ConnectorType: "openai-compatible", BaseURL: "https://one.example", Note: "note one", Enabled: true, KeyCount: 2},
		{ID: e2, UserID: u2, ConnectorType: "openai-compatible", BaseURL: "https://two.example", Note: "note two", Enabled: false, KeyCount: 1},
		{ID: e3, UserID: u1, ConnectorType: "openai-compatible", BaseURL: "https://three.example", Note: "", Enabled: true, KeyCount: 0},
	}
	for i, got := range eps {
		if got.ID != want[i].ID || got.UserID != want[i].UserID || got.ConnectorType != want[i].ConnectorType ||
			got.BaseURL != want[i].BaseURL || got.Note != want[i].Note || got.Enabled != want[i].Enabled ||
			got.KeyCount != want[i].KeyCount || got.CreatedAt == 0 || got.UpdatedAt == 0 {
			t.Errorf("row %d = %+v, want %+v", i, got, want[i])
		}
	}
}

func TestModelsOverviewProjection(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "overview-models.db"))
	defer store.Close()
	u1 := seedUsageUser(t, store, "u1")
	u2 := seedUsageUser(t, store, "u2")

	key1 := seedUsageKey(t, store, u1)
	key2 := seedUsageKey(t, store, u2)

	m1 := ovSeedModel(t, store, u1, "prov1", "mod1", true)
	ovSeedBinding(t, store, m1, key1, "upstream/a", 0)
	ovSeedBinding(t, store, m1, key1, "upstream/b", 1)
	m2 := ovSeedModel(t, store, u2, "prov2", "mod2", false)

	models, err := store.ListModelsOverview(context.Background())
	if err != nil {
		t.Fatalf("ListModelsOverview: %v", err)
	}
	if len(models) != 2 {
		t.Fatalf("overview rows = %d, want 2: %+v", len(models), models)
	}
	first, second := models[0], models[1]
	if first.ID != m1 || first.UserID != u1 || first.Provider != "prov1" || first.Model != "mod1" ||
		first.FullName != "prov1/mod1" || first.RouteStrategy != "ordered" || !first.SilentRetry ||
		first.BindingCount != 2 || first.CreatedAt == 0 {
		t.Errorf("first row = %+v", first)
	}
	if second.ID != m2 || second.UserID != u2 || second.Provider != "prov2" || second.Model != "mod2" ||
		second.FullName != "prov2/mod2" || second.RouteStrategy != "ordered" || second.SilentRetry ||
		second.BindingCount != 0 || second.CreatedAt == 0 {
		t.Errorf("second row = %+v", second)
	}
	_ = key2 // the u2 key exists so the u2 model could bind; count stays 0
}
