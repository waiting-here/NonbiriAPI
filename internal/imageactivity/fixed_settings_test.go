package imageactivity

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
)

func TestFixedConnectionDefaultsSecretAndReadOnlyInputs(t *testing.T) {
	f := newFixture(t)
	secret := "fixture-secret"
	input := ConnectionInput{ExpectedRevision: "1", BaseURL: f.upstream.server.URL + "/v1", Secret: SecretInput{Mode: "replace", Value: &secret}}
	if _, err := f.service.PutConnection(f.ctx(f.steward), f.steward, f.key(), input); !errors.Is(err, authz.ErrForbidden) {
		t.Fatal("steward can change image service credentials")
	}
	if _, err := f.service.PutConnection(f.ctx(f.admin), f.admin, f.key(), input); err != nil {
		t.Fatal(err)
	}
	connection, err := f.service.GetUpstream(f.ctx(f.admin), f.admin)
	if err != nil || connection.RPM == nil || *connection.RPM != 100 || connection.Concurrency == nil || *connection.Concurrency != 2 || connection.PerUserLimit != 1 || connection.GlobalLimit != 100 || connection.QueueTimeoutSeconds != 1800 || connection.ExecutionTimeoutSeconds != 1800 || connection.MemoryBudgetMiB != 512 || len(connection.ImageOrigins) != 0 {
		t.Fatalf("wrong internal defaults: %+v %v", connection, err)
	}
	var before, after string
	if err := f.database.QueryRow("SELECT secret_ciphertext FROM image_upstream_revisions WHERE revision=2").Scan(&before); err != nil {
		t.Fatal(err)
	}
	input.ExpectedRevision, input.Secret = connection.Revision, SecretInput{Mode: "keep"}
	if _, err := f.service.PutConnection(f.ctx(f.admin), f.admin, f.key(), input); err != nil {
		t.Fatal(err)
	}
	if err := f.database.QueryRow("SELECT secret_ciphertext FROM image_upstream_revisions WHERE revision=3").Scan(&after); err != nil || before != after {
		t.Fatal("keeping the secret rewrote its ciphertext")
	}
	current, err := f.service.GetUpstream(f.ctx(f.admin), f.admin)
	if err != nil || current.Control.ID != connection.Control.ID || current.BaseURL != connection.BaseURL {
		t.Fatal("connection identity changed on secret keep")
	}
	view, _ := json.Marshal(current)
	if strings.Contains(string(view), secret) {
		t.Fatal("secret exposed in the connection response")
	}
}

func TestFixedServiceSizePriceAndFrozenChargeSurvivePriceChange(t *testing.T) {
	f, mock, routes := configureFixedService(t)
	model, err := f.service.GetAdminModel(f.ctx(f.admin), f.admin, f.model)
	if err != nil {
		t.Fatal(err)
	}
	pricing := PricingPolicy{Default: model.Price, Fallback: "default", Tiers: []TierPrice{}, Sizes: []SizePrice{{Width: 512, Height: 512, Price: Price{"5", "0"}}}}
	output := fixedAdminRequest(t, f, routes, http.MethodPut, "/models/"+f.model, ModelSettingsInput{ExpectedRevision: model.Revision, Enabled: true, Price: model.Price, Pricing: &pricing})
	var saved ModelReceipt
	if output.Code != http.StatusOK || json.Unmarshal(output.Body.Bytes(), &saved) != nil || saved.Revision != "2" {
		t.Fatalf("HTTP size pricing rejected: %d %s", output.Code, output.Body.String())
	}
	read := httptest.NewRecorder()
	routes.mux.ServeHTTP(read, httptest.NewRequest(http.MethodGet, adminPrefix+"/models/"+f.model, nil).WithContext(f.ctx(f.admin)))
	var persisted AdminModel
	if read.Code != http.StatusOK || json.Unmarshal(read.Body.Bytes(), &persisted) != nil || persisted.Pricing == nil || len(persisted.Pricing.Sizes) != 1 || persisted.Pricing.Sizes[0] != pricing.Sizes[0] || persisted.CapabilityIssues == nil {
		t.Fatal("HTTP GET did not retain the size pricing or stable capability projection")
	}
	t.Logf("Saved model response: %s", read.Body.String())
	count := 2
	input := SubmitInput{ModelID: f.model, ExpectedModelRevision: saved.Revision, ExpectedPricingRevision: saved.PricingRevision, Prompt: "Synthetic priced request", N: &count}
	quote, err := f.service.Quote(f.ctx(f.user), f.user, input)
	if err != nil || quote.Total != (Price{"10", "0"}) || quote.EffectiveSelection.Selection.Width != 512 || quote.Unit != (Price{"5", "0"}) || mock.posts.Load() != 0 {
		t.Fatalf("wrong exact-size quote: %+v %v", quote, err)
	}
	accepted, err := f.service.Submit(f.ctx(f.user), f.user, f.key(), input)
	if err != nil {
		t.Fatal(err)
	}
	pricing.Sizes[0].Price = Price{"9", "0"}
	if _, err = f.service.PutModelSettings(f.ctx(f.admin), f.admin, f.model, f.key(), ModelSettingsInput{ExpectedRevision: saved.Revision, Enabled: true, Price: model.Price, Pricing: &pricing}); err != nil {
		t.Fatal(err)
	}
	if _, err = f.service.Submit(f.ctx(f.other), f.other, f.key(), input); !errors.Is(err, ErrRefreshRequired) {
		t.Fatal("stale price confirmation accepted")
	}
	f.wait(t, func() bool {
		result, err := f.service.GetTask(f.ctx(f.user), f.user, accepted.Value.Task.ID)
		return err == nil && result.Status == "succeeded"
	})
	result, err := f.service.GetTask(f.ctx(f.user), f.user, accepted.Value.Task.ID)
	if err != nil || result.Charge != (Price{"10", "0"}) || mock.posts.Load() != 1 {
		t.Fatal("accepted charge was recomputed after changing the price")
	}
	f.checkLedger(t)
}

func TestFixedServiceBatchValidationAtomicityAndReplay(t *testing.T) {
	f, mock, routes := configureFixedService(t)
	mock.mu.Lock()
	mock.catalog = append(mock.catalog, json.RawMessage(`{"id":"unknown-image"}`))
	mock.mu.Unlock()
	accepted, err := f.service.RefreshServiceModels(f.ctx(f.admin), f.admin, f.key())
	if err != nil {
		t.Fatal(err)
	}
	f.wait(t, func() bool {
		refresh, err := f.service.GetRefresh(f.ctx(f.admin), f.admin, accepted.Value.Operation.ID)
		return err == nil && refresh.State == "succeeded"
	})
	catalog, err := f.service.ListCatalog(f.ctx(f.admin), f.admin, CatalogQuery{Type: "unknown"})
	if err != nil || len(catalog.Data) != 1 {
		t.Fatal("missing unknown catalog model")
	}
	unknown := catalog.Data[0].ID
	input := ModelSettingsBatch{Models: []ModelSettingsItem{
		{ID: f.model, Input: ModelSettingsInput{ExpectedRevision: "1", Enabled: true, Price: Price{"3", "1"}}},
		{ID: unknown, Input: ModelSettingsInput{ExpectedRevision: "0", Enabled: true, Price: Price{"2", "1"}}},
	}}
	failed, err := f.service.PutModelsSettings(f.ctx(f.admin), f.admin, f.key(), input)
	if err != nil || failed.Value.Applied || len(failed.Value.Issues) != 1 || failed.Value.Issues[0].Code != "unsupported_metadata" {
		t.Fatalf("wrong batch validation: %+v %v", failed, err)
	}
	model, err := f.service.GetAdminModel(f.ctx(f.admin), f.admin, f.model)
	if err != nil || model.Revision != "1" || model.Price != (Price{"2", "1"}) {
		t.Fatal("batch wrote an earlier valid row before a later row failed")
	}
	input.Models[1].Input.Enabled = false
	key := f.key()
	saved, err := f.service.PutModelsSettings(f.ctx(f.admin), f.admin, key, input)
	if err != nil || !saved.Value.Applied || len(saved.Value.Receipts) != 2 {
		t.Fatalf("valid settings batch %+v: %v", saved, err)
	}
	replay, err := f.service.PutModelsSettings(f.ctx(f.admin), f.admin, key, input)
	if err != nil || !replay.Replayed || !replay.Value.Applied || replay.Value.Receipts[0].Revision != saved.Value.Receipts[0].Revision {
		t.Fatal("minimal settings batch replay failed")
	}
	output := fixedAdminRequest(t, f, routes, http.MethodPost, "/models/batch", map[string]any{"models": []any{map[string]any{"id": f.model, "input": map[string]any{"expected_revision": "2", "price": Price{"2", "1"}}}}})
	if output.Code != http.StatusBadRequest {
		t.Fatal("omitting enabled silently disabled a model")
	}
	if mock.posts.Load() != 0 {
		t.Fatal("batch settings triggered generation")
	}
}

func TestFixedServiceIncompatibleRefreshKeepsEnabledPricesAndAcceptedSnapshot(t *testing.T) {
	f, mock, _ := configureFixedService(t)
	mock.mode.Store(1)
	task := fixedSubmit(t, f, 1)
	f.wait(t, func() bool {
		row, err := f.service.readTask(context.Background(), task.ID)
		return err == nil && row.state == "running" && row.upstreamID != ""
	})
	mock.mu.Lock()
	mock.catalog = []json.RawMessage{fixedMetadata(`{"customSizeMapping":{}}`, `{}`)}
	mock.mu.Unlock()
	accepted, err := f.service.RefreshServiceModels(f.ctx(f.admin), f.admin, f.key())
	if err != nil {
		t.Fatal(err)
	}
	f.wait(t, func() bool {
		refresh, err := f.service.GetRefresh(f.ctx(f.admin), f.admin, accepted.Value.Operation.ID)
		return err == nil && refresh.State == "succeeded"
	})
	model, err := f.service.GetAdminModel(f.ctx(f.admin), f.admin, f.model)
	if err != nil || !model.Enabled || model.Price != (Price{"2", "1"}) || model.CapabilityReadiness != "pending" || len(model.CapabilityIssues) == 0 {
		t.Fatalf("incompatible refresh changed operation settings: %+v %v", model, err)
	}
	if _, err := f.service.Quote(f.ctx(f.user), f.user, SubmitInput{ModelID: f.model, Prompt: "Synthetic new request"}); !errors.Is(err, ErrUnavailable) {
		t.Fatal("pending capability accepted a new quote")
	}
	if _, err := f.service.Submit(f.ctx(f.other), f.other, f.key(), SubmitInput{ModelID: f.model, ExpectedModelRevision: model.Revision, ExpectedPricingRevision: model.PricingRevision, Prompt: "Synthetic new request"}); !errors.Is(err, ErrUnavailable) {
		t.Fatal("pending capability accepted a new task")
	}
	saved, err := f.service.PutModelsSettings(f.ctx(f.admin), f.admin, f.key(), ModelSettingsBatch{Models: []ModelSettingsItem{{ID: f.model, Input: ModelSettingsInput{ExpectedRevision: model.Revision, Enabled: true, Price: Price{"3", "1"}}}}})
	if err != nil || !saved.Value.Applied {
		t.Fatalf("pending enabled model cannot retain enabled while updating price: %+v %v", saved, err)
	}
	row, err := f.service.readTask(context.Background(), task.ID)
	if err != nil || row.modelRevision != 1 {
		t.Fatal("incompatible metadata rewrote an accepted snapshot")
	}
	mock.mode.Store(0)
	f.now.Add(5)
	f.wait(t, func() bool {
		result, err := f.service.GetTask(f.ctx(f.user), f.user, task.ID)
		return err == nil && result.Status == "succeeded"
	})
	if mock.posts.Load() != 1 {
		t.Fatal("metadata refresh repeated a generation")
	}
	f.checkLedger(t)
}
