package charityrouting

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/credits"
	"github.com/waiting-here/NonbiriAPI/internal/httpapi"
	"github.com/waiting-here/NonbiriAPI/internal/modeltype"
)

func TestModalityPricingWireAndSafeProjections(t *testing.T) {
	raw := `{"mode":"per_token","user_prices":{"uncached_input":"1","cache_write_input":"2","cache_read_input":"3","output":"4"},"donor_rewards":{"uncached_input":"0.5","cache_write_input":"1","cache_read_input":"1.5","output":"2"},"user_modalities":{"input_enabled":true,"output_enabled":false,"audio":{"uncached_input":"9.001","cache_write_input":"2","cache_read_input":"1","output":"0"}},"donor_modalities":{"input_enabled":false,"output_enabled":true,"text":{"uncached_input":"0","cache_write_input":"0","cache_read_input":"0","output":"0"}},"long_context":{"input_threshold":"100","user_prices":{"uncached_input":"2","cache_write_input":"4","cache_read_input":"6","output":"8"},"donor_rewards":{"uncached_input":"1","cache_write_input":"2","cache_read_input":"3","output":"4"},"donor_modalities":{"input_enabled":false,"output_enabled":false}}}`
	var wire pricingWire
	if err := httpapi.DecodeJSON([]byte(raw), &wire); err != nil {
		t.Fatal(err)
	}
	in, canonical, err := parsePricing(wire)
	if err != nil {
		t.Fatal(err)
	}
	if canonical["user_modalities"] == nil {
		t.Fatal("modality omitted from canonical request")
	}
	prices, err := validatePricing(in)
	if err != nil {
		t.Fatal(err)
	}
	core, err := credits.DecodeContextPricing(prices.contextPricing)
	if err != nil || core.UserModalities.Audio.UncachedInput != 9001 || core.UserModalities.Text != nil || core.DonorModalities.Text.Output != 0 {
		t.Fatal(core, err)
	}
	admin := AdminPricing{Mode: "per_token"}
	if err := adminContextPricing(&admin, prices.contextPricing); err != nil {
		t.Fatal(err)
	}
	if admin.UserModalities.Audio.UncachedInput != "9.001" || admin.LongContext.DonorRewards.Output != "4" || admin.LongContext.DonorModalities == nil {
		t.Fatal(admin)
	}
	steward := StewardPricing{}
	stewardContextPricing(admin, &steward)
	if steward.LongContext.DonorRewards.Output != "4" {
		t.Fatal(steward)
	}
	public := CapabilityPricing{Mode: "per_token"}
	if err := capabilityContextPricing(&public, prices.contextPricing, 50, 80); err != nil {
		t.Fatal(err)
	}
	if public.UserModalitiesMilli.Audio.UncachedInput != "9001" || public.DiscountedUserModalitiesMilli.Audio.UncachedInput != "4501" || public.CurrentUserModalitiesMilli.Audio.UncachedInput != "7201" {
		t.Fatal(public)
	}
	encoded, _ := json.Marshal(public)
	if strings.Contains(string(encoded), "donor") {
		t.Fatal("public projection exposed rewards")
	}
}

func TestSearchUnitConfigurationConstraints(t *testing.T) {
	one := "1"
	in := PricingInput{Mode: "per_search_unit", UserPrice: &one, DonorReward: &one}
	if _, err := validatePricing(in); err != nil {
		t.Fatal(err)
	}
	if !validPricingOperations(in.Mode, modeltype.Set{"rerank"}) || validPricingOperations(in.Mode, modeltype.Default()) || validPricingOperations(in.Mode, modeltype.Set{"rerank", "embeddings"}) {
		t.Fatal("operation compatibility")
	}
	in.UserModalities = &ModalityPricingInput{}
	if _, err := validatePricing(in); err == nil {
		t.Fatal("search unit accepted modality pricing")
	}
	in.UserModalities = nil
	in.LongContext = &LongContextInput{InputThreshold: "1", UserPrice: &one}
	if _, err := validatePricing(in); err == nil {
		t.Fatal("search unit accepted long context")
	}
}

func TestLegacyLongContextRewardsRemainOmitted(t *testing.T) {
	in := testModelCreate().Pricing
	two := "2"
	in.LongContext = &LongContextInput{InputThreshold: "100", UserPrice: &two}
	prices, err := validatePricing(in)
	if err != nil {
		t.Fatal(err)
	}
	out := AdminPricing{Mode: in.Mode}
	if err := adminContextPricing(&out, prices.contextPricing); err != nil {
		t.Fatal(err)
	}
	if out.LongContext.DonorReward != nil || out.LongContext.DonorRewards != nil || out.LongContext.DonorModalities != nil {
		t.Fatal("legacy reward override added")
	}
}

func TestModalityPricingPersistenceAndTemplateProjection(t *testing.T) {
	e := newRoutingTestEnv(t)
	e.seedUser(t, true, nil)
	rates := &TokenPricesInput{UncachedInput: "123.456", CacheWriteInput: "234.567", CacheReadInput: "345.678", Output: "456.789"}
	modal := &ModalityPricingInput{InputEnabled: true, OutputEnabled: true, Text: rates, Image: rates, Audio: rates, Video: rates}
	input := testModelCreate()
	input.Pricing = PricingInput{Mode: "per_token", UserPrices: rates, DonorRewards: rates, UserModalities: modal, DonorModalities: modal,
		LongContext: &LongContextInput{InputThreshold: "100000", UserPrices: rates, DonorRewards: rates, UserModalities: modal, DonorModalities: modal}}
	validated, err := validatePricing(input.Pricing)
	if err != nil || len(validated.contextPricing) <= 1024 {
		t.Fatal("expected expanded configuration", len(validated.contextPricing), err)
	}
	result, err := e.service.CreateAdmin(t.Context(), routingMutation(t, 'm', http.MethodPost, routeAdminModels, nil, input), input)
	if err != nil {
		t.Fatal(err)
	}
	if result.Value.Pricing.UserModalities.Video.Output != "456.789" || result.Value.Pricing.LongContext.DonorModalities.Audio.CacheReadInput != "345.678" {
		t.Fatal(result.Value.Pricing)
	}
	template, err := normalizePriceTemplate(PriceTemplateInput{Name: "Audio prices", Pricing: input.Pricing})
	if err != nil || template.Pricing.LongContext.DonorModalities.Audio.CacheReadInput != "345.678" {
		t.Fatal(template, err)
	}
}

func TestReserveForOutputUsesMaximumModalityTier(t *testing.T) {
	e := newRoutingTestEnv(t)
	e.seedUser(t, true, nil)
	input := testModelCreate()
	rates := &TokenPricesInput{UncachedInput: "0", CacheWriteInput: "0", CacheReadInput: "0", Output: "1"}
	input.Pricing = PricingInput{Mode: "per_token", UserPrices: rates, DonorRewards: rates,
		UserModalities: &ModalityPricingInput{OutputEnabled: true, Audio: &TokenPricesInput{"0", "0", "0", "3"}},
		LongContext: &LongContextInput{InputThreshold: "100", UserPrices: rates,
			UserModalities: &ModalityPricingInput{OutputEnabled: true, Audio: &TokenPricesInput{"0", "0", "0", "9"}}}}
	created, err := e.service.CreateAdmin(t.Context(), routingMutation(t, 'x', http.MethodPost, routeAdminModels, nil, input), input)
	if err != nil {
		t.Fatal(err)
	}
	modelID, _ := parsePositiveID(created.Value.ID)
	got, err := e.service.ReserveForOutput(t.Context(), modelID, 100, 1_000_000)
	if err != nil || got != 9000 {
		t.Fatalf("reserve=%d err=%v", got, err)
	}
	got, err = e.service.ReserveForOutput(t.Context(), modelID, 12000, 1_000_000)
	if err != nil || got != 12000 {
		t.Fatalf("configured floor=%d err=%v", got, err)
	}
}

func TestSearchUnitModelPersistenceAndOperationChanges(t *testing.T) {
	e := newRoutingTestEnv(t)
	e.seedUser(t, true, nil)
	input := testModelCreate()
	input.ModelTypes = modeltype.Set{"rerank"}
	price, reward := "2.5", "1"
	input.Pricing = PricingInput{Mode: "per_search_unit", UserPrice: &price, DonorReward: &reward}
	created, err := e.service.CreateAdmin(t.Context(), routingMutation(t, 'u', http.MethodPost, routeAdminModels, nil, input), input)
	if err != nil {
		t.Fatal(err)
	}
	if created.Value.Pricing.Mode != "per_search_unit" || created.Value.Pricing.UserPrice == nil || *created.Value.Pricing.UserPrice != "2.5" {
		t.Fatal(created.Value.Pricing)
	}
	modelID, _ := parsePositiveID(created.Value.ID)
	got, err := e.service.ReserveForOutput(t.Context(), modelID, 123, 1_000_000)
	if err != nil || got != 123 {
		t.Fatalf("search reserve=%d err=%v", got, err)
	}
	nextTypes := modeltype.Set{"embeddings"}
	patch := ModelPatch{ExpectedRevision: created.Value.Revision, ModelTypes: &nextTypes}
	if _, err := e.service.PatchAdmin(t.Context(), modelID, routingMutation(t, 'v', http.MethodPatch, routeAdminModel, []int64{modelID}, patch), patch); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("incompatible patch: %v", err)
	}
	patch.Pricing = &PricingInput{Mode: "per_request", UserPrice: &price, DonorReward: &reward}
	if _, err := e.service.PatchAdmin(t.Context(), modelID, routingMutation(t, 'w', http.MethodPatch, routeAdminModel, []int64{modelID}, patch), patch); err != nil {
		t.Fatal(err)
	}
}
