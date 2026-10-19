package charityrouting

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

func templateInput() PriceTemplateInput {
	return PriceTemplateInput{Name: "Full price", Description: "Saved pricing", Enabled: true, Pricing: testModelCreate().Pricing, TokenReserveCredits: new("2.5"), Discount: DiscountInput{Enabled: false, Percent: 0}}
}
func TestPriceTemplatesCompleteReplacementPermissionsAndCAS(t *testing.T) {
	e := newRoutingTestEnv(t)
	ctx := context.Background()
	e.seedUser(t, true, nil)
	six, five := int64(6), int64(5)
	steward, trainee := e.seedUser(t, false, &six), e.seedUser(t, false, &five)
	input := templateInput()
	input.Pricing.LongContext = &LongContextInput{InputThreshold: "100", UserPrice: new("9")}
	input.Pricing.ContextPenalty = &ContextPenaltyInput{InputThreshold: "200", Fine: "4"}
	mutation := routingMutation(t, 'A', http.MethodPost, routeAdminPriceTemplates, nil, input)
	made, err := e.service.mutatePriceTemplate(ctx, 0, "", mutation, &input)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := parsePositiveID(made.Value.ID)
	if made.Value.Revision != "1" || *made.Value.TokenReserveCredits != "2.5" || made.Value.Discount.Enabled || made.Value.Discount.Percent != 0 {
		t.Fatal(made.Value)
	}
	repeated, err := e.service.mutatePriceTemplate(ctx, 0, "", mutation, &input)
	if err != nil || !repeated.Replayed || string(repeated.Body) != string(made.Body) {
		t.Fatal(repeated, err)
	}
	if _, err = e.service.priceTemplates(ctx, roleSteward, trainee, "", "enabled", pagination.Default()); !errors.Is(err, ErrForbidden) {
		t.Fatal("level five", err)
	}
	if _, err = e.service.priceTemplates(ctx, roleSteward, steward, "", "all", pagination.Default()); !errors.Is(err, ErrForbidden) {
		t.Fatal("all", err)
	}
	list, err := e.service.priceTemplates(ctx, roleSteward, steward, "Full", "enabled", pagination.Default())
	if err != nil || list.Pagination.TotalItems != "1" {
		t.Fatal(list, err)
	}
	input.Enabled = false
	input.Pricing = PricingInput{Mode: "per_token", UserPrices: &TokenPricesInput{"0", "0", "0", "0"}, DonorRewards: &TokenPricesInput{"1", "2", "3", "4"}}
	input.TokenReserveCredits = nil
	replaced, err := e.service.mutatePriceTemplate(ctx, id, "1", routingMutation(t, 'B', http.MethodPut, routeAdminPriceTemplate, []int64{id}, input), &input)
	if err != nil || replaced.Value.Revision != "2" || replaced.Value.Pricing.LongContext != nil || replaced.Value.Pricing.ContextPenalty != nil || replaced.Value.TokenReserveCredits != nil || replaced.Value.Enabled {
		t.Fatal(replaced, err)
	}
	if _, err = e.service.priceTemplate(ctx, roleSteward, steward, id); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if _, err = e.service.mutatePriceTemplate(ctx, id, "1", routingMutation(t, 'C', http.MethodPut, routeAdminPriceTemplate, []int64{id}, input), &input); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	gone, err := e.service.mutatePriceTemplate(ctx, id, "2", routingMutation(t, 'D', http.MethodDelete, routeAdminPriceTemplate, []int64{id}, map[string]any{"expected_revision": "2"}), nil)
	if err != nil || gone.Status != 204 || len(gone.Body) != 0 {
		t.Fatal(gone, err)
	}
	var audits int
	if err = e.store.DB().QueryRow("SELECT COUNT(*) FROM charity_price_template_audits").Scan(&audits); err != nil || audits != 3 {
		t.Fatal(audits, err)
	}
	retained, err := e.service.Retain(ctx, routingTestNow+90*86400, 100, time.Time{})
	if err != nil || retained.Processed != 3 {
		t.Fatal(retained, err)
	}
}

func TestPriceTemplateHTTPRequiresExplicitNullAndPreservesDisabledValues(t *testing.T) {
	e := newRoutingTestEnv(t)
	e.seedUser(t, true, nil)
	api := &httpAPI{service: e.service}
	body := map[string]any{"name": "Template", "description": "", "enabled": false, "pricing": map[string]any{"mode": "per_request", "user_price": "0", "donor_reward": "0", "long_context": nil, "context_penalty": nil}, "discount": map[string]any{"enabled": false, "percent": 0, "start_at": nil, "end_at": nil}}
	call := func(v map[string]any, key string) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(v)
		r := httptest.NewRequest("POST", routeAdminPriceTemplates, strings.NewReader(string(raw)))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Idempotency-Key", key)
		w := httptest.NewRecorder()
		api.mutateAdminPriceTemplate(w, r)
		return w
	}
	if w := call(body, strings.Repeat("a", 22)); w.Code != 400 {
		t.Fatal(w.Code, w.Body)
	}
	body["token_reserve_credits"] = nil
	if w := call(body, strings.Repeat("b", 22)); w.Code != 201 {
		t.Fatal(w.Code, w.Body)
	}
	body["token_reserve_credits"] = "1"
	if w := call(body, strings.Repeat("c", 22)); w.Code != 201 {
		t.Fatal(w.Code, w.Body)
	}
	body["name"] = strings.Repeat("n", 81)
	if w := call(body, strings.Repeat("d", 22)); w.Code != 400 {
		t.Fatal(w.Code, w.Body)
	}
}

func TestPriceTemplateCapacityAndModelIndependence(t *testing.T) {
	e := newRoutingTestEnv(t)
	e.seedUser(t, true, nil)
	ctx := context.Background()
	input := templateInput()
	model := e.createModel(t, 'z')
	tx, err := e.store.DB().Begin()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 256; i++ {
		_, err = tx.Exec(`INSERT INTO charity_price_templates(name,description,enabled,revision,pricing,discount,created_at,updated_at) VALUES(?,'',1,1,'{}','{}',?,?)`, fmt.Sprint(i), routingTestNow, routingTestNow)
		if err != nil {
			t.Fatal(err)
		}
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err = e.service.mutatePriceTemplate(ctx, 0, "", routingMutation(t, 'a', http.MethodPost, routeAdminPriceTemplates, nil, input), &input); !errors.Is(err, ErrResourceLimit) {
		t.Fatal(err)
	}
	id, _ := parsePositiveID(model.ID)
	after, err := e.service.GetAdmin(ctx, id)
	if err != nil || after.Revision != model.Revision || *after.Pricing.UserPrice != *model.Pricing.UserPrice {
		t.Fatal(after, err)
	}
}
