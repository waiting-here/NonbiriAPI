package donation

import (
	"net/http"
	"strconv"
)

const (
	routeOwnerUses   = "/api/donations/{id}/uses"
	routeOwnerUse    = routeOwnerUses + "/{useId}"
	routeAdminUses   = "/admin/api/donations/{id}/uses"
	routeStewardUses = "/api/steward/donations/{id}/uses"
)

func useRoute(role reviewerRole) string {
	switch role {
	case reviewerAdmin:
		return routeAdminUses
	case reviewerSteward:
		return routeStewardUses
	default:
		return routeOwnerUses
	}
}

func (api *httpAPI) adminUseHandler(next AuthorizedUserHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) { next(w, r, UserPrincipal{}) }
}

func (api *httpAPI) usesPageHandler(role reviewerRole) AuthorizedUserHandler {
	return func(w http.ResponseWriter, r *http.Request, p UserPrincipal) {
		donationID, ok := parsePathID(w, r, "id")
		if !ok || !requireNoBody(w, r) {
			return
		}
		values, ok := requestQuery(w, r)
		if !ok {
			return
		}
		page, err := parseNumberedQuery(values, "state", "credential_id", "donation_key_id")
		if err != nil {
			writeDonationError(w, err)
			return
		}
		filter := UseFilter{State: values.Get("state")}
		if values.Has("credential_id") {
			id, err := strconv.ParseInt(values.Get("credential_id"), 10, 64)
			if err != nil || id <= 0 || strconv.FormatInt(id, 10) != values.Get("credential_id") {
				writeDonationError(w, ErrInvalidRequest)
				return
			}
			filter.CredentialID = id
		}
		if values.Has("donation_key_id") {
			id, err := strconv.ParseInt(values.Get("donation_key_id"), 10, 64)
			if err != nil || id <= 0 || strconv.FormatInt(id, 10) != values.Get("donation_key_id") {
				writeDonationError(w, ErrInvalidRequest)
				return
			}
			filter.DonationKeyID = id
		}
		if values.Has("state") && filter.State == "" {
			writeDonationError(w, ErrInvalidRequest)
			return
		}
		result, err := api.service.UsesPage(r.Context(), role, p.UserID, donationID, filter, page)
		if err != nil {
			writeDonationError(w, err)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, result)
	}
}

type usePatchWire struct {
	ExpectedRevision requiredField[string] `json:"expected_revision"`
	Enabled          requiredField[bool]   `json:"enabled"`
	DiscoveryEnabled requiredField[bool]   `json:"discovery_enabled"`
}

func (api *httpAPI) useMutationHandler(role reviewerRole, action string) AuthorizedUserHandler {
	return func(w http.ResponseWriter, r *http.Request, p UserPrincipal) {
		donationID, ok := parsePathID(w, r, "id")
		if !ok {
			return
		}
		useID, ok := parsePathID(w, r, "useId")
		if !ok || !requireEmptyQuery(w, r) {
			return
		}
		var wire usePatchWire
		if !decodeStrictObject(w, r, &wire) {
			return
		}
		revision, err := requiredRevision(wire.ExpectedRevision)
		if err != nil || action != "patch" && (wire.Enabled.Set || wire.DiscoveryEnabled.Set) {
			writeDonationError(w, ErrInvalidRequest)
			return
		}
		input := UsePatch{ExpectedRevision: revision}
		canonical := map[string]any{"expected_revision": wire.ExpectedRevision.Value}
		if wire.Enabled.Set {
			input.Enabled = &wire.Enabled.Value
			canonical["enabled"] = wire.Enabled.Value
		}
		if wire.DiscoveryEnabled.Set {
			input.DiscoveryEnabled = &wire.DiscoveryEnabled.Value
			canonical["discovery_enabled"] = wire.DiscoveryEnabled.Value
		}
		route := useRoute(role) + "/{useId}"
		if action == "cancel" {
			route += "/pending"
		}
		if action == "reset" {
			route += "/failure-streak-reset"
		}
		mutation, ok := mutationFor(w, r, route, []int64{donationID, useID}, canonical)
		if !ok {
			return
		}
		result, err := api.service.mutateUse(r.Context(), role, p.UserID, donationID, useID, input, action, mutation)
		if err != nil {
			writeDonationError(w, err)
			return
		}
		writeMutation(w, result)
	}
}

type useReviewItemWire struct {
	UseID                  requiredField[string] `json:"use_id"`
	ExpectedRevision       requiredField[string] `json:"expected_revision"`
	ExpectedReviewRevision nullableField[string] `json:"expected_review_revision"`
	Decision               requiredField[string] `json:"decision"`
	ReviewNote             requiredField[string] `json:"review_note"`
}

func (api *httpAPI) useReviewHandler(role reviewerRole) AuthorizedUserHandler {
	return func(w http.ResponseWriter, r *http.Request, p UserPrincipal) {
		donationID, ok := parsePathID(w, r, "id")
		if !ok || !requireEmptyQuery(w, r) {
			return
		}
		var wire struct {
			Items requiredField[[]useReviewItemWire] `json:"items"`
		}
		if !decodeStrictObject(w, r, &wire) {
			return
		}
		if !wire.Items.Set || len(wire.Items.Value) == 0 || len(wire.Items.Value) > 100 {
			writeDonationError(w, ErrInvalidRequest)
			return
		}
		items := make([]UseReviewItem, 0, len(wire.Items.Value))
		canonical := make([]map[string]any, 0, len(wire.Items.Value))
		for _, entry := range wire.Items.Value {
			id, e1 := requiredID(entry.UseID)
			revision, e2 := requiredRevision(entry.ExpectedRevision)
			if e1 != nil || e2 != nil || !entry.ExpectedReviewRevision.Set || !entry.Decision.Set || !entry.ReviewNote.Set {
				writeDonationError(w, ErrInvalidRequest)
				return
			}
			item := UseReviewItem{UseID: id, ExpectedRevision: revision, Decision: entry.Decision.Value, ReviewNote: entry.ReviewNote.Value}
			if entry.ExpectedReviewRevision.Value != nil {
				value, err := strconv.ParseInt(*entry.ExpectedReviewRevision.Value, 10, 64)
				if err != nil || value <= 0 || strconv.FormatInt(value, 10) != *entry.ExpectedReviewRevision.Value {
					writeDonationError(w, ErrInvalidRequest)
					return
				}
				item.ExpectedReviewRevision = &value
			}
			items = append(items, item)
			canonical = append(canonical, map[string]any{"use_id": entry.UseID.Value, "expected_revision": entry.ExpectedRevision.Value, "expected_review_revision": entry.ExpectedReviewRevision.Value, "decision": entry.Decision.Value, "review_note": entry.ReviewNote.Value})
		}
		mutation, ok := mutationFor(w, r, useRoute(role)+"/review", []int64{donationID}, map[string]any{"items": canonical})
		if !ok {
			return
		}
		result, err := api.service.reviewUses(r.Context(), role, p.UserID, donationID, items, mutation)
		if err != nil {
			writeDonationError(w, err)
			return
		}
		writeMutation(w, result)
	}
}
