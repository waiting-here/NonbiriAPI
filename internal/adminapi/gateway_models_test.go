package adminapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/gatewaypolicy"
)

func TestGatewayCapabilitiesImportEditDeleteAndNoResurrection(t *testing.T) {
	store := openGenerationTwoPublicConfigStore(t)
	ctx := context.Background()
	policies := gatewaypolicy.NewStore(store.DB())
	legacy := `{"models":[{"base_url":"https://gateway.example/v3/ai/","model":"anthropic/model","adapter":"anthropic_always_adaptive","efforts":["high"],"max_output_tokens":128000,"storage":"reject"}]}`
	if err := policies.Initialize(ctx, legacy, gatewaypolicy.Config{}); err != nil {
		t.Fatal(err)
	}
	auth := &siteConfigTestFinalAuthorizer{}
	repo := newSiteConfigTestRepository(t, store, auth)
	list, err := repo.ReadGatewayModels(ctx, 1)
	if err != nil || len(list.Data) != 1 {
		t.Fatalf("import %+v %v", list, err)
	}
	record := list.Data[0]
	if record.BaseURL != "https://gateway.example/v3/ai" || record.Cache != gatewaypolicy.RejectCache {
		t.Fatalf("normalized %+v", record)
	}
	id, _ := strconv.ParseInt(record.ID, 10, 64)
	record.Cache = gatewaypolicy.AnthropicCache
	input := GatewayModelInput{ExpectedRevision: record.Revision, Entry: &record.Entry}
	result, err := repo.MutateGatewayModel(ctx, 1, id, http.MethodPut, "gateway-capability-update-00001", input)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := repo.MutateGatewayModel(ctx, 1, id, http.MethodPut, "gateway-capability-update-00001", input)
	if err != nil || !replay.Replayed || string(replay.Body) != string(result.Body) {
		t.Fatalf("replay %v %v", replay, err)
	}
	_, err = repo.MutateGatewayModel(ctx, 1, id, http.MethodPut, "gateway-capability-update-00002", input)
	if !errors.Is(err, ErrSiteConfigConflict) {
		t.Fatalf("stale revision: %v", err)
	}
	target := gatewaypolicy.Target{BaseURL: record.BaseURL + "/", Model: record.Model}
	other := gatewaypolicy.Target{BaseURL: "https://other.example/v3/ai", Model: record.Model}
	read, err := policies.LoadMany(ctx, []gatewaypolicy.Target{target, other})
	if err != nil || read[target].Cache != gatewaypolicy.AnthropicCache || read[other].HasReasoning() {
		t.Fatalf("target isolation: %+v %v", read, err)
	}
	var updated gatewaypolicy.Record
	if err := json.Unmarshal(result.Body, &updated); err != nil {
		t.Fatal(err)
	}
	_, err = repo.MutateGatewayModel(ctx, 1, id, http.MethodDelete, "gateway-capability-delete-00001", GatewayModelInput{ExpectedRevision: updated.Revision})
	if err != nil {
		t.Fatal(err)
	}
	// Restart must ignore even malformed retired environment values.
	if err := policies.Initialize(ctx, "invalid stale environment", gatewaypolicy.Config{}); err != nil {
		t.Fatal(err)
	}
	if err := policies.Initialize(ctx, legacy, gatewaypolicy.Config{}); err != nil {
		t.Fatal(err)
	}
	list, err = repo.ReadGatewayModels(ctx, 1)
	if err != nil || len(list.Data) != 0 {
		t.Fatalf("deleted profile resurrected: %+v %v", list, err)
	}
	auth.setError(authz.ErrForbidden)
	if _, err := repo.ReadGatewayModels(ctx, 1); !errors.Is(err, ErrSiteConfigForbidden) {
		t.Fatalf("read role: %v", err)
	}
	_, err = repo.MutateGatewayModel(ctx, 1, 0, http.MethodPost, "gateway-capability-create-00001", GatewayModelInput{ExpectedRevision: "0", Entry: &record.Entry})
	if !errors.Is(err, ErrSiteConfigForbidden) {
		t.Fatalf("write role: %v", err)
	}
}

func TestGatewayCapabilityHTTPRejectsUnknownFields(t *testing.T) {
	_, api := newSiteConfigHTTPFixture(t)
	headers := map[string][]string{"Content-Type": {"application/json"}, "Idempotency-Key": {"gateway-capability-create-00001"}}
	valid := siteConfigHTTPRequest(t, api.mux, http.MethodPost, RouteAdminGatewayModels, []byte(`{"expected_revision":"0","entry":{"base_url":"https://g.example/ai","model":"m","adapter":"openai_chat"}}`), headers)
	if valid.Code != http.StatusOK {
		t.Fatalf("valid request status=%d body=%s", valid.Code, valid.Body.String())
	}
	headers["Idempotency-Key"] = []string{"gateway-capability-invalid-00001"}
	for _, body := range []string{
		`{"expected_revision":"0","entry":{"base_url":"https://g.example/ai","model":"m","adapter":"openai_chat","unknown":true}}`,
		`{"expected_revision":"0","entry":{"base_url":"https://g.example/ai","model":"m","adapter":"openai_chat"},"unknown":1}`,
		`{"expected_revision":"0","entry":{"base_url":"https://g.example/ai","model":"m","adapter":"openai_chat","max_output_tokens":-1}}`,
	} {
		result := siteConfigHTTPRequest(t, api.mux, http.MethodPost, RouteAdminGatewayModels, []byte(body), headers)
		if result.Code != http.StatusBadRequest {
			t.Fatalf("status=%d body=%s", result.Code, result.Body.String())
		}
	}
}

func TestGatewayEmptyImportRemainsEmptyAfterEnvironmentChanges(t *testing.T) {
	store := openGenerationTwoPublicConfigStore(t)
	ctx := context.Background()
	policies := gatewaypolicy.NewStore(store.DB())
	if err := policies.Initialize(ctx, "", gatewaypolicy.Config{}); err != nil {
		t.Fatal(err)
	}
	if err := gatewaypolicy.NewStore(store.DB()).Initialize(ctx, `{"models":[{"base_url":"https://g.example","model":"m","adapter":"openai_chat"}]}`, gatewaypolicy.Config{}); err != nil {
		t.Fatal(err)
	}
	rows, err := gatewaypolicy.ReadRecords(ctx, store.DB())
	if err != nil || len(rows) != 0 {
		t.Fatalf("retired variable changed empty DB: %+v %v", rows, err)
	}
}
