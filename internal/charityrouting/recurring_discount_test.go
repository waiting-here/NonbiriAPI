package charityrouting

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/charitydiscount"
	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/connector/openai"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
	"github.com/waiting-here/NonbiriAPI/internal/pagination"
	"github.com/waiting-here/NonbiriAPI/internal/rolepolicy"
	"github.com/waiting-here/NonbiriAPI/internal/sessionheader"
)

type revokedReceiptAdmin struct{ *routingTestAuth }

func (*revokedReceiptAdmin) AuthorizeAdminMutation(context.Context, *sql.Tx, int64) error {
	return ErrForbidden
}

func recurringConfig(percent int) *charitydiscount.Config {
	v := charitydiscount.Default()
	v.Enabled, v.DefaultPercent = true, percent
	return &v
}

func TestRecurringDiscountModelReplacementAndAuthority(t *testing.T) {
	e := newRoutingTestEnv(t)
	e.seedUser(t, true, nil)
	model := e.createModel(t, 'a')
	if !reflect.DeepEqual(model.RecurringDiscount, charitydiscount.Default()) || model.ServerNow != routingTestNow {
		t.Fatal(model)
	}
	id, _ := parsePositiveID(model.ID)
	config := recurringConfig(0)
	config.Periods = []charitydiscount.Period{{Weekdays: []int{7, 1}, StartTime: "22:00", EndTime: "02:00", Percent: 50}}
	patch := ModelPatch{ExpectedRevision: model.Revision, RecurringDiscount: config}
	updated, err := e.service.PatchAdmin(t.Context(), id, routingMutation(t, 'b', http.MethodPatch, routeAdminModel, []int64{id}, patch), patch)
	if err != nil || updated.Value.RecurringDiscount.Periods[0].Weekdays[0] != 1 || config.Periods[0].Weekdays[0] != 7 {
		t.Fatal(updated, err, config)
	}
	kept := updated.Value.RecurringDiscount.Clone()
	patch = ModelPatch{ExpectedRevision: updated.Value.Revision, PublicDescription: new("Updated description")}
	updated, err = e.service.PatchAdmin(t.Context(), id, routingMutation(t, 'c', http.MethodPatch, routeAdminModel, []int64{id}, patch), patch)
	if err != nil || !reflect.DeepEqual(updated.Value.RecurringDiscount, kept) {
		t.Fatal(updated, err)
	}
	empty := charitydiscount.Default()
	patch = ModelPatch{ExpectedRevision: updated.Value.Revision, RecurringDiscount: &empty}
	updated, err = e.service.PatchAdmin(t.Context(), id, routingMutation(t, 'd', http.MethodPatch, routeAdminModel, []int64{id}, patch), patch)
	if err != nil || !reflect.DeepEqual(updated.Value.RecurringDiscount, empty) {
		t.Fatal(updated, err)
	}
	five, six := int64(5), int64(6)
	trainee, steward := e.seedUser(t, false, &five), e.seedUser(t, false, &six)
	if _, err := e.store.DB().Exec("UPDATE charity_models SET is_mainstream=1 WHERE id=?", id); err != nil {
		t.Fatal(err)
	}
	patch = ModelPatch{ExpectedRevision: updated.Value.Revision, RecurringDiscount: recurringConfig(50)}
	role := rolepolicy.Default()
	patch.RolePolicy = &role
	if _, err := e.service.PatchSteward(t.Context(), trainee, id, routingMutation(t, 'e', http.MethodPatch, routeStewardModel, []int64{id}, patch), patch); !errors.Is(err, ErrForbidden) {
		t.Fatal("trainee changed pricing", err)
	}
	if _, err := e.service.PatchSteward(t.Context(), steward, id, routingMutation(t, 'f', http.MethodPatch, routeStewardModel, []int64{id}, patch), patch); err != nil {
		t.Fatal(err)
	}
}

func TestRecurringDiscountCurrentPricesAndDecisionClock(t *testing.T) {
	for _, token := range []bool{false, true} {
		t.Run(fmt.Sprint("token=", token), func(t *testing.T) {
			e := newRoutingTestEnv(t)
			e.seedUser(t, true, nil)
			e.seedUserBalance(t, e.caller, "100000")
			input := testModelCreate()
			input.Discount.Enabled = false
			input.RecurringDiscount = recurringConfig(50)
			if token {
				input.Pricing = PricingInput{Mode: "per_token", UserPrices: &TokenPricesInput{"1", "2", "3", "4"}, DonorRewards: &TokenPricesInput{"0", "0", "0", "0"},
					LongContext: &LongContextInput{InputThreshold: "10", UserPrices: &TokenPricesInput{"2", "4", "6", "8"}}}
				input.TokenReserveCredits = new("5")
			} else {
				input.Pricing.LongContext = &LongContextInput{InputThreshold: "10", UserPrice: new("7")}
			}
			made, err := e.service.CreateAdmin(t.Context(), routingMutation(t, 'g', http.MethodPost, routeAdminModels, nil, input), input)
			if err != nil {
				t.Fatal(err)
			}
			id, _ := parsePositiveID(made.Value.ID)
			owner := e.seedUser(t, false, nil)
			_, key, _ := e.seedCandidate(t, owner, 'q', "upstream-model")
			batch := BindingBatch{ExpectedBindingRevision: "0", Selections: []BindingSelection{{DonationKeyID: fmt.Sprint(key), UpstreamModelID: "upstream-model"}}}
			if _, err := e.service.AddBindingsAdmin(t.Context(), id, routingMutation(t, 'Q', http.MethodPost, routeAdminBindingBatch, []int64{id}, batch), batch); err != nil {
				t.Fatal(err)
			}
			check := func(wantPercent int) {
				t.Helper()
				catalog, err := e.service.Catalog(t.Context(), e.caller, CatalogFilter{}, pagination.Default())
				if err != nil || len(catalog.Models) != 1 {
					t.Fatal(catalog, err)
				}
				capability, err := e.service.Capability(t.Context(), e.caller, catalog.ServerNow)
				if err != nil || len(capability.Models) != 1 || !reflect.DeepEqual(catalog.Models[0].Pricing, capability.Models[0].Pricing) {
					t.Fatal(capability, err)
				}
				m := catalog.Models[0]
				p := m.Pricing
				if m.EffectiveDiscount.Percent != wantPercent {
					t.Fatal(m.EffectiveDiscount)
				}
				if token {
					if p.CurrentUserPriceMilli != nil || p.LongContext.CurrentUserPriceMilli != nil ||
						*p.UserPricesMilli != (CapabilityTokenPrices{"1000", "2000", "3000", "4000"}) ||
						*p.DiscountedUserPricesMilli != (CapabilityTokenPrices{"800", "1600", "2400", "3200"}) ||
						p.CurrentUserPricesMilli.CacheReadInput != fmt.Sprint(3000*wantPercent/100) ||
						p.LongContext.CurrentUserPricesMilli.Output != fmt.Sprint(8000*wantPercent/100) {
						t.Fatal(p)
					}
				} else if p.CurrentUserPricesMilli != nil || p.LongContext.CurrentUserPricesMilli != nil ||
					*p.DiscountedUserPriceMilli != "2400" || *p.CurrentUserPriceMilli != fmt.Sprint(3000*wantPercent/100) ||
					*p.LongContext.CurrentUserPriceMilli != fmt.Sprint(7000*wantPercent/100) {
					t.Fatal(p)
				}
				r, err := openai.DecodeChatRequest(strings.NewReader(`{"model":"[公益]provider/model","messages":[{"role":"user","content":"A sufficiently long input for admission."}]}`), openai.MaxRequestBodyBytes)
				if err != nil {
					t.Fatal(err)
				}
				defer r.Clear()
				preflight, err := e.service.Preflight(t.Context(), e.caller, r.Model, r, catalog.ServerNow)
				wantReserve := int64(3000 * wantPercent / 100)
				if token {
					wantReserve = 5000
				}
				if err != nil || preflight.ReservedMilli != wantReserve {
					t.Fatal(preflight, err)
				}
				snapshot, err := e.service.SnapshotForCaller(t.Context(), e.caller, id, catalog.ServerNow, []connectorcontract.Type{connectorcontract.TypeOpenAICompatible})
				if err != nil || snapshot.ReservedMilli != wantReserve {
					t.Fatal(snapshot, err)
				}
			}
			check(50)
			for _, bounds := range [][2]int64{{routingTestNow + 10, routingTestNow + 20}, {routingTestNow - 20, routingTestNow - 10}, {routingTestNow - 1, routingTestNow + 1}} {
				if _, err := e.store.DB().Exec("UPDATE charity_models SET discount_enabled=1,discount_start_at=?,discount_end_at=? WHERE id=?", bounds[0], bounds[1], id); err != nil {
					t.Fatal(err)
				}
				want := 100
				if bounds[0] < routingTestNow && bounds[1] > routingTestNow {
					want = 80
				}
				check(want)
			}
			input.Model = "second"
			if _, err := e.service.CreateAdmin(t.Context(), routingMutation(t, 'G', http.MethodPost, routeAdminModels, nil, input), input); err != nil {
				t.Fatal(err)
			}
			e.service.now = func() time.Time { return time.Unix(e.clock.Add(1), 0) }
			page, err := e.service.modelsPage(t.Context(), roleAdmin, 0, "", nil, pagination.Default())
			if err != nil || len(page.Data) != 2 || page.Data[0].ServerNow != routingTestNow+1 || page.Data[1].ServerNow != page.Data[0].ServerNow || page.Data[0].EffectiveDiscount.Percent != 100 {
				t.Fatal(page, err)
			}
		})
	}
}

func TestRecurringDiscountHTTPBoundsAndStrictReplacement(t *testing.T) {
	e := newRoutingTestEnv(t)
	e.seedUser(t, true, nil)
	api := &httpAPI{service: e.service}
	input := testModelCreate()
	input.RecurringDiscount = recurringConfig(50)
	role := rolepolicy.Default()
	for i := range 32 {
		role.Rules[fmt.Sprintf("extra%d", i)] = "native"
	}
	input.RolePolicy = &role
	for i := range 16 {
		input.RecurringDiscount.Periods = append(input.RecurringDiscount.Periods, charitydiscount.Period{
			Weekdays: []int{1, 2, 3, 4, 5, 6, 7}, StartTime: fmt.Sprintf("%02d:00", i), EndTime: fmt.Sprintf("%02d:30", i), Percent: 100})
		input.SessionHeaders = append(input.SessionHeaders, sessionheader.Config{Name: fmt.Sprintf("x-recurring-%d", i), IdleSeconds: 300, Format: "uuid_v4"})
	}
	input.Pricing = PricingInput{Mode: "per_token", UserPrices: &TokenPricesInput{"1", "2", "3", "4"}, DonorRewards: &TokenPricesInput{"1", "2", "3", "4"},
		LongContext: &LongContextInput{InputThreshold: "10", UserPrices: &TokenPricesInput{"2", "4", "6", "8"}}, ContextPenalty: &ContextPenaltyInput{InputThreshold: "100", Fine: "1"}}
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	call := func(body string, seed byte) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, routeAdminModels, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Idempotency-Key", strings.Repeat(string(seed), 22))
		w := httptest.NewRecorder()
		api.createAdminModel(w, r)
		return w
	}
	if w := call(string(raw), 'h'); w.Code != 201 {
		t.Fatal("legal sixteen-period body", w.Code, w.Body)
	}
	for i, config := range []string{"null", `{}`, `{"enabled":false,"time_zone":"UTC","default_percent":50,"periods":null}`,
		`{"enabled":false,"enabled":true,"time_zone":"UTC","default_percent":50,"periods":[]}`} {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil {
			t.Fatal(err)
		}
		fields["recurring_discount"] = json.RawMessage(config)
		body, _ := json.Marshal(fields)
		if w := call(string(body), byte('i'+i)); w.Code != 400 {
			t.Fatal("invalid config accepted", w.Code, w.Body)
		}
	}
	if w := call(string(raw)+strings.Repeat(" ", 16<<10), 'm'); w.Code != 413 {
		t.Fatal("model body budget", w.Code, w.Body)
	}
}

func TestRecurringDiscountTemplateCopiesAndOmissionClears(t *testing.T) {
	e := newRoutingTestEnv(t)
	e.seedUser(t, true, nil)
	input := templateInput()
	input.RecurringDiscount = recurringConfig(0)
	input.RecurringDiscount.Enabled = false
	input.RecurringDiscount.Periods = []charitydiscount.Period{{Weekdays: []int{1}, StartTime: "00:00", EndTime: "24:00", Percent: 0}}
	made, err := e.service.mutatePriceTemplate(t.Context(), 0, "", routingMutation(t, 'n', http.MethodPost, routeAdminPriceTemplates, nil, input), &input)
	if err != nil || !reflect.DeepEqual(*input.RecurringDiscount, made.Value.RecurringDiscount) {
		t.Fatal(made, err)
	}
	modelInput := testModelCreate()
	copied := made.Value.RecurringDiscount.Clone()
	modelInput.RecurringDiscount, modelInput.Discount = &copied, input.Discount
	model, err := e.service.CreateAdmin(t.Context(), routingMutation(t, 'o', http.MethodPost, routeAdminModels, nil, modelInput), modelInput)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := parsePositiveID(made.Value.ID)
	input.RecurringDiscount = nil
	updated, err := e.service.mutatePriceTemplate(t.Context(), id, "1", routingMutation(t, 'p', http.MethodPut, routeAdminPriceTemplate, []int64{id}, input), &input)
	if err != nil || !reflect.DeepEqual(updated.Value.RecurringDiscount, charitydiscount.Default()) {
		t.Fatal(updated, err)
	}
	modelID, _ := parsePositiveID(model.Value.ID)
	after, err := e.service.GetAdmin(t.Context(), modelID)
	if err != nil || !reflect.DeepEqual(after.RecurringDiscount, copied) || after.Discount.Enabled || after.Discount.Percent != 0 {
		t.Fatal(after, err)
	}
}

func TestRecurringDiscountLegacyReceiptsPreserveOriginalOutcome(t *testing.T) {
	for _, template := range []bool{false, true} {
		t.Run(fmt.Sprint("template=", template), func(t *testing.T) {
			e := newRoutingTestEnv(t)
			e.seedUser(t, true, nil)
			api := &httpAPI{service: e.service}
			route, body := routeAdminModels, `{"provider":"historical","model":"receipt","enabled":true,"flatten_tool_calls":false,"pricing":{"mode":"per_request","user_price":"3","donor_reward":"1"},"discount":{"enabled":true,"percent":80,"start_at":null,"end_at":null}}`
			table, auditTable := "charity_models", "policy_audits"
			if template {
				route, body = routeAdminPriceTemplates, `{"name":"Historical template","description":"","enabled":true,"pricing":{"mode":"per_request","user_price":"3","donor_reward":"1"},"token_reserve_credits":null,"discount":{"enabled":true,"percent":80,"start_at":null,"end_at":null}}`
				table, auditTable = "charity_price_templates", "charity_price_template_audits"
			}
			call := func() *httptest.ResponseRecorder {
				r := httptest.NewRequest(http.MethodPost, route, strings.NewReader(body))
				r.Header.Set("Content-Type", "application/json")
				r.Header.Set("Idempotency-Key", strings.Repeat("R", 22))
				w := httptest.NewRecorder()
				if template {
					api.mutateAdminPriceTemplate(w, r)
				} else {
					api.createAdminModel(w, r)
				}
				return w
			}
			first := call()
			if first.Code != 201 {
				t.Fatal(first.Code, first.Body)
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(first.Body.Bytes(), &fields); err != nil {
				t.Fatal(err)
			}
			delete(fields, "recurring_discount")
			delete(fields, "effective_discount")
			delete(fields, "server_now")
			historical, _ := json.Marshal(fields)
			hash, _ := idempotency.KeyHash(strings.Repeat("R", 22))
			result, err := e.store.DB().Exec(`INSERT OR REPLACE INTO idempotency_records(scope,actor_scope_hash,key_hash,request_hash,state,http_status,response_body,created_at,expires_at)
SELECT scope,actor_scope_hash,key_hash,request_hash,state,http_status,?,created_at,expires_at FROM idempotency_records WHERE key_hash=?`, historical, hash[:])
			if err != nil {
				t.Fatal(err)
			}
			if n, err := result.RowsAffected(); n != 1 || err != nil {
				t.Fatal(n, err)
			}
			var expires, auditCount int64
			if err := e.store.DB().QueryRow("SELECT expires_at FROM idempotency_records WHERE key_hash=?", hash[:]).Scan(&expires); err != nil {
				t.Fatal(err)
			}
			if err := e.store.DB().QueryRow("SELECT count(*) FROM " + auditTable).Scan(&auditCount); err != nil {
				t.Fatal(err)
			}
			live, _ := charitydiscount.Encode(*recurringConfig(20))
			if _, err := e.store.DB().Exec("UPDATE "+table+" SET revision=revision+1,recurring_discount=?", live); err != nil {
				t.Fatal(err)
			}
			e.clock.Store(routingTestNow + 100)
			replayed := call()
			if replayed.Code != 201 {
				t.Fatal(replayed.Code, replayed.Body)
			}
			var response map[string]json.RawMessage
			if err := json.Unmarshal(replayed.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			for name, original := range fields {
				if !bytes.Equal(original, response[name]) {
					t.Fatal("historical outcome changed", name, string(original), string(response[name]))
				}
			}
			var config charitydiscount.Config
			if err := json.Unmarshal(response["recurring_discount"], &config); err != nil || !reflect.DeepEqual(config, charitydiscount.Default()) {
				t.Fatal(config, err)
			}
			if !template && (string(response["server_now"]) != fmt.Sprint(routingTestNow) || !bytes.Contains(response["effective_discount"], []byte(`"percent":80`))) {
				t.Fatal("receipt used live time", replayed.Body)
			}
			var stored []byte
			var afterExpires, afterAudits, revision, count int64
			if err := e.store.DB().QueryRow("SELECT response_body,expires_at FROM idempotency_records WHERE key_hash=?", hash[:]).Scan(&stored, &afterExpires); err != nil {
				t.Fatal(err)
			}
			if err := e.store.DB().QueryRow("SELECT revision,(SELECT count(*) FROM "+auditTable+"),(SELECT count(*) FROM "+table+") FROM "+table).Scan(&revision, &afterAudits, &count); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(stored, historical) || afterExpires != expires || afterAudits != auditCount || revision != 2 || count != 1 {
				t.Fatal("receipt replay mutated persisted state", afterExpires, afterAudits, revision, count)
			}
			e.service.roleAuth = &revokedReceiptAdmin{e.auth}
			if denied := call(); denied.Code == 201 {
				t.Fatal("authority was not rechecked before replay")
			}
		})
	}
}
