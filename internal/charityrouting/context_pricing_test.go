package charityrouting

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"net/http"
	"strconv"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/httpapi"
	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

func TestContextPricingConfigurationAndProjections(t *testing.T) {
	e := newRoutingTestEnv(t)
	e.seedUser(t, true, nil)
	input := testModelCreate()
	price := "7.5"
	input.Pricing.LongContext = &LongContextInput{InputThreshold: "100000", UserPrice: &price}
	input.Pricing.ContextPenalty = &ContextPenaltyInput{InputThreshold: "200000", Fine: "500"}
	res, err := e.service.CreateAdmin(t.Context(), routingMutation(t, 'a', http.MethodPost, routeAdminModels, nil, input), input)
	if err != nil {
		t.Fatal(err)
	}
	if res.Value.Pricing.LongContext == nil || *res.Value.Pricing.LongContext.UserPrice != "7.5" || res.Value.Pricing.ContextPenalty.Fine != "500" {
		t.Fatal(res.Value.Pricing)
	}
	steward := stewardModel(res.Value)
	if steward.Pricing.LongContext.InputThreshold != "100000" || steward.Pricing.ContextPenalty.Fine != "500" {
		t.Fatal(steward)
	}
	catalog, err := e.service.Catalog(context.Background(), e.caller, CatalogFilter{}, pagination.Default())
	if err != nil || len(catalog.Models) != 1 {
		t.Fatal(catalog, err)
	}
	p := catalog.Models[0].Pricing
	if p.LongContext == nil || *p.LongContext.UserPriceMilli != "7500" || *p.LongContext.DiscountedUserPriceMilli != "6000" || p.ContextPenalty.FineMilli != "500000" {
		t.Fatal(p)
	}
	body, _ := json.Marshal(p)
	var public map[string]any
	if err = json.Unmarshal(body, &public); err != nil {
		t.Fatal(err)
	}
	for _, v := range []string{"donor_reward", "donor_rewards"} {
		if public[v] != nil {
			t.Fatal("reward exposed")
		}
	}
	id, _ := strconv.ParseInt(res.Value.ID, 10, 64)
	input.Pricing.LongContext = nil
	input.Pricing.ContextPenalty = nil
	patch := ModelPatch{ExpectedRevision: res.Value.Revision, Pricing: &input.Pricing}
	updated, err := e.service.PatchAdmin(t.Context(), id, routingMutation(t, 'b', http.MethodPatch, routeAdminModel, []int64{id}, patch), patch)
	if err != nil || updated.Value.Pricing.LongContext != nil || updated.Value.Pricing.ContextPenalty != nil {
		t.Fatal(updated, err)
	}
}

func TestContextPricingWireRejectsIncompleteAndMismatchedRates(t *testing.T) {
	for _, raw := range []string{
		`{"mode":"per_request","user_price":"1","donor_reward":"0","long_context":{"input_threshold":"0","user_price":"2"}}`,
		`{"mode":"per_request","user_price":"1","donor_reward":"0","long_context":{"input_threshold":"1","user_prices":{"uncached_input":"2","cache_write_input":"2","cache_read_input":"2","output":"2"}}}`,
		`{"mode":"per_request","user_price":"1","donor_reward":"0","context_penalty":{"input_threshold":"1"}}`,
		`{"mode":"per_request","user_price":"1","donor_reward":"0","context_penalty":{"input_threshold":"9223372036854775808","fine":"1"}}`,
		`{"mode":"per_request","user_price":"1","donor_reward":"0","context_penalty":{"input_threshold":"1","fine":"-1"}}`,
		`{"mode":"per_token","user_prices":{"uncached_input":"1","cache_write_input":"1","cache_read_input":"1","output":"1"},"donor_rewards":{"uncached_input":"0","cache_write_input":"0","cache_read_input":"0","output":"0"},"long_context":{"input_threshold":"1","user_prices":{"uncached_input":"1","output":"1"}}}`,
	} {
		var wire pricingWire
		err := httpapi.DecodeJSON([]byte(raw), &wire)
		if err == nil {
			var in PricingInput
			in, _, err = parsePricing(wire)
			if err == nil {
				_, err = validatePricing(in)
			}
		}
		if err == nil {
			t.Fatal("accepted", raw)
		}
	}
	var wire pricingWire
	if err := httpapi.DecodeJSON([]byte(`{"mode":"per_request","user_price":"1","donor_reward":"0","long_context":null,"context_penalty":null}`), &wire); err != nil {
		t.Fatal(err)
	}
	in, canonical, err := parsePricing(wire)
	if err != nil || in.LongContext != nil || in.ContextPenalty != nil {
		t.Fatal(in, err)
	}
	if _, ok := canonical["context_penalty"]; !ok {
		t.Fatal("clear missing from idempotency body")
	}
}

func TestContextPricingUsesExistingStewardModelScope(t *testing.T) {
	e := newRoutingTestEnv(t)
	e.seedUser(t, true, nil)
	five, six := int64(5), int64(6)
	trainee := e.seedUser(t, false, &five)
	steward := e.seedUser(t, false, &six)
	model := e.createModel(t, 'c')
	id, _ := parsePositiveID(model.ID)
	pricing := testModelCreate().Pricing
	price := "7"
	pricing.LongContext = &LongContextInput{InputThreshold: "10", UserPrice: &price}
	pricing.ContextPenalty = &ContextPenaltyInput{InputThreshold: "20", Fine: "3"}
	patch := ModelPatch{ExpectedRevision: model.Revision, Pricing: &pricing}
	mutate := func(seed byte, actor int64) error {
		_, err := e.service.PatchSteward(t.Context(), actor, id, routingMutation(t, seed, http.MethodPatch, routeStewardModel, []int64{id}, patch), patch)
		return err
	}
	if err := mutate('d', trainee); !errors.Is(err, ErrNotFound) {
		t.Fatal("unmanaged model", err)
	}
	if _, err := e.store.DB().Exec("UPDATE charity_models SET is_mainstream=1 WHERE id=?", id); err != nil {
		t.Fatal(err)
	}
	if err := mutate('e', trainee); !errors.Is(err, authz.ErrPermissionDenied) {
		t.Fatal("trainee cannot change prices", err)
	}
	patch.ExpectedRevision = "1"
	if _, err := e.store.DB().Exec("UPDATE charity_models SET is_mainstream=0 WHERE id=?", id); err != nil {
		t.Fatal(err)
	}
	if err := mutate('f', steward); err != nil {
		t.Fatal("steward", err)
	}
}
