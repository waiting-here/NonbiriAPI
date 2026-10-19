package charityrouting

import (
	"net/http"

	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

func (api *httpAPI) listAdminPriceTemplates(w http.ResponseWriter, r *http.Request) {
	api.listPriceTemplates(w, r, roleAdmin, 0)
}
func (api *httpAPI) listStewardPriceTemplates(w http.ResponseWriter, r *http.Request, p UserPrincipal) {
	api.listPriceTemplates(w, r, roleSteward, p.UserID)
}
func (api *httpAPI) listPriceTemplates(w http.ResponseWriter, r *http.Request, role roleKind, actorID int64) {
	if !requireNoBody(w, r) {
		return
	}
	values, ok := requestQuery(w, r)
	if !ok {
		return
	}
	for name, v := range values {
		if len(v) != 1 || (name != "q" && name != "state" && name != "page" && name != "page_size") {
			writeRoutingError(w, ErrInvalidRequest)
			return
		}
	}
	page, _, err := pagination.Parse(values)
	if err != nil {
		writeRoutingError(w, ErrInvalidRequest)
		return
	}
	state := "enabled"
	if v, exists := values["state"]; exists {
		state = v[0]
	}
	out, err := api.service.priceTemplates(r.Context(), role, actorID, values.Get("q"), state, page)
	if err != nil {
		writeRoutingError(w, err)
		return
	}
	writeJSON(w, out)
}
func (api *httpAPI) getAdminPriceTemplate(w http.ResponseWriter, r *http.Request) {
	api.getPriceTemplate(w, r, roleAdmin, 0)
}
func (api *httpAPI) getStewardPriceTemplate(w http.ResponseWriter, r *http.Request, p UserPrincipal) {
	api.getPriceTemplate(w, r, roleSteward, p.UserID)
}
func (api *httpAPI) getPriceTemplate(w http.ResponseWriter, r *http.Request, role roleKind, actorID int64) {
	id, ok := parsePathID(w, r, "id")
	if !ok || !requireNoBody(w, r) || !requireEmptyQuery(w, r) {
		return
	}
	out, err := api.service.priceTemplate(r.Context(), role, actorID, id)
	if err != nil {
		writeRoutingError(w, err)
		return
	}
	writeJSON(w, out)
}

type priceTemplateWire struct {
	Name                requiredField[string]       `json:"name"`
	Description         requiredField[string]       `json:"description"`
	Enabled             requiredField[bool]         `json:"enabled"`
	Pricing             requiredField[pricingWire]  `json:"pricing"`
	TokenReserveCredits nullableField[string]       `json:"token_reserve_credits"`
	Discount            requiredField[discountWire] `json:"discount"`
	ExpectedRevision    requiredField[string]       `json:"expected_revision"`
}

func (api *httpAPI) mutateAdminPriceTemplate(w http.ResponseWriter, r *http.Request) {
	if !requireEmptyQuery(w, r) {
		return
	}
	id := int64(0)
	route := routeAdminPriceTemplates
	var ids []int64
	if r.Method != http.MethodPost {
		var ok bool
		id, ok = parsePathID(w, r, "id")
		if !ok {
			return
		}
		ids = []int64{id}
		route = routeAdminPriceTemplate
	}
	var wire priceTemplateWire
	var input *PriceTemplateInput
	var expected string
	var canonical map[string]any
	if r.Method == http.MethodDelete {
		var body struct {
			ExpectedRevision requiredField[string] `json:"expected_revision"`
		}
		if !decodeStrictObject(w, r, &body) {
			return
		}
		if !body.ExpectedRevision.Set {
			writeRoutingError(w, ErrInvalidRequest)
			return
		}
		expected = body.ExpectedRevision.Value
		canonical = map[string]any{"expected_revision": expected}
	} else {
		if !decodeStrictObject(w, r, &wire) {
			return
		}
		if !wire.Name.Set || !wire.Description.Set || !wire.Enabled.Set || !wire.Pricing.Set || !wire.TokenReserveCredits.Set || !wire.Discount.Set ||
			(id == 0 && wire.ExpectedRevision.Set) || (id != 0 && !wire.ExpectedRevision.Set) {
			writeRoutingError(w, ErrInvalidRequest)
			return
		}
		pricing, pj, err := parsePricing(wire.Pricing.Value)
		if err != nil {
			writeRoutingError(w, err)
			return
		}
		discount, dj, err := parseDiscount(wire.Discount.Value)
		if err != nil {
			writeRoutingError(w, err)
			return
		}
		input = &PriceTemplateInput{Name: wire.Name.Value, Description: wire.Description.Value, Enabled: wire.Enabled.Value, Pricing: pricing, TokenReserveCredits: wire.TokenReserveCredits.Value, Discount: discount}
		canonical = map[string]any{"name": input.Name, "description": input.Description, "enabled": input.Enabled, "pricing": pj, "token_reserve_credits": input.TokenReserveCredits, "discount": dj}
		if id != 0 {
			expected = wire.ExpectedRevision.Value
			canonical["expected_revision"] = expected
		}
	}
	mutation, ok := mutationFor(w, r, route, ids, canonical)
	if !ok {
		return
	}
	result, err := api.service.mutatePriceTemplate(r.Context(), id, expected, mutation, input)
	if err != nil {
		writeRoutingError(w, err)
		return
	}
	writeMutation(w, result)
}
