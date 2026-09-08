package charityrouting

import (
	"net/http"
	"net/url"

	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

func parseNumberedQuery(values url.Values, filters ...string) (pagination.Request, bool, error) {
	allowed := map[string]bool{"page": true, "page_size": true, "cursor": true, "limit": true}
	for _, filter := range filters {
		allowed[filter] = true
	}
	for name, values := range values {
		if !allowed[name] || len(values) != 1 {
			return pagination.Request{}, false, ErrInvalidRequest
		}
	}
	page, numbered, err := pagination.Parse(values)
	if err != nil {
		return page, numbered, ErrInvalidRequest
	}
	return page, numbered, nil
}

func (api *httpAPI) numberedModels(w http.ResponseWriter, r *http.Request, role roleKind, actorID int64, values url.Values, page pagination.Request) {
	raw, present := values["enabled"]
	filter := ""
	if present {
		filter = raw[0]
	}
	enabled, err := parseEnabledFilter(filter, present)
	if err != nil {
		writeRoutingError(w, err)
		return
	}
	result, err := api.service.modelsPage(r.Context(), role, actorID, values.Get("q"), enabled, page)
	if err != nil {
		writeRoutingError(w, err)
		return
	}
	if role == roleAdmin {
		writeJSON(w, result)
		return
	}
	out := Page[StewardCharityModel]{Data: make([]StewardCharityModel, len(result.Data)), Pagination: result.Pagination}
	for i, item := range result.Data {
		out.Data[i] = stewardModel(item)
	}
	writeJSON(w, out)
}

func (api *httpAPI) numberedCandidates(w http.ResponseWriter, r *http.Request, role roleKind, actorID, modelID int64, values url.Values, page pagination.Request) {
	donationID, err := optionalQueryID(values, "donation_id")
	if err != nil {
		writeRoutingError(w, err)
		return
	}
	keyID, err := optionalQueryID(values, "donation_key_id")
	if err != nil {
		writeRoutingError(w, err)
		return
	}
	result, err := api.service.candidatesPage(r.Context(), role, actorID, modelID,
		CandidateQuery{DonationID: donationID, DonationKeyID: keyID, Source: values.Get("source"), Query: values.Get("q")}, page)
	if err != nil {
		writeRoutingError(w, err)
		return
	}
	if role == roleAdmin {
		writeJSON(w, result)
		return
	}
	out := Page[StewardBindingCandidate]{Data: make([]StewardBindingCandidate, len(result.Data)), Pagination: result.Pagination}
	for i, item := range result.Data {
		out.Data[i] = stewardCandidate(item)
	}
	writeJSON(w, out)
}
