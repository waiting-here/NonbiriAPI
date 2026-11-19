package resources

import (
	"context"
	"net/http"
	"net/url"

	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

type manualCatalogEntryRequest struct {
	UpstreamModelID requestField[string] `json:"upstream_model_id"`
	Provider        requestField[string] `json:"provider"`
}

type createManualRequest struct {
	Entries requestField[[]manualCatalogEntryRequest] `json:"entries"`
}

type createManualCanonical struct {
	Entries []ManualCatalogInput `json:"entries"`
}

type bindingReplacementRequest struct {
	BindingID                  requestField[string] `json:"binding_id"`
	ReplacementUpstreamModelID requestField[string] `json:"replacement_upstream_model_id"`
}

type bindingReplacementCanonical struct {
	BindingID                  string `json:"binding_id"`
	ReplacementUpstreamModelID string `json:"replacement_upstream_model_id"`
}

type updateManualRequest struct {
	UpstreamModelID      requestField[string]                      `json:"upstream_model_id"`
	Provider             requestField[string]                      `json:"provider"`
	ExpectedPairRevision requestField[string]                      `json:"expected_pair_revision"`
	Replacements         requestField[[]bindingReplacementRequest] `json:"replacements"`
}

type updateManualCanonical struct {
	UpstreamModelID      string                        `json:"upstream_model_id"`
	Provider             string                        `json:"provider"`
	ExpectedPairRevision string                        `json:"expected_pair_revision"`
	Replacements         []bindingReplacementCanonical `json:"replacements"`
}

type deleteManualRequest struct {
	ExpectedPairRevision requestField[string]                      `json:"expected_pair_revision"`
	Replacements         requestField[[]bindingReplacementRequest] `json:"replacements"`
}

type deleteManualCanonical struct {
	ExpectedPairRevision string                        `json:"expected_pair_revision"`
	Replacements         []bindingReplacementCanonical `json:"replacements"`
}

func (api *httpAPI) getCatalog(writer http.ResponseWriter, request *http.Request, principal UserPrincipal) {
	endpointID, ok := parsePathID(writer, request, "id")
	if !ok {
		return
	}
	keyID, ok := parsePathID(writer, request, "keyId")
	if !ok || !requireNoBody(writer, request) {
		return
	}
	if serveNumberedPage(writer, request, []string{"source", "q", "match"}, func(ctx context.Context, values url.Values, page pagination.Request) (CatalogView, error) {
		if entries, present := values["source"]; present && entries[0] != "automatic" && entries[0] != "manual" {
			return CatalogView{}, ErrInvalidRequest
		}
		if entries, ok := values["match"]; ok && (entries[0] != "exact" || values.Get("q") == "") {
			return CatalogView{}, ErrInvalidRequest
		}
		return api.repository.catalogPage(ctx, principal.UserID, endpointID, keyID, values.Get("source"), values.Get("q"), values.Get("match") == "exact", page)
	}) {
		return
	}
	limit, cursor, ok := parsePageQuery(writer, request)
	if !ok {
		return
	}
	view, err := api.repository.GetCatalog(request.Context(), principal.UserID, endpointID, keyID, limit, cursor)
	if err != nil {
		writeResourceError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, view)
}

func (api *httpAPI) getCatalogSelection(w http.ResponseWriter, req *http.Request, principal UserPrincipal) {
	endpointID, ok := parsePathID(w, req, "id")
	if !ok {
		return
	}
	keyID, ok := parsePathID(w, req, "keyId")
	if !ok || !requireNoBody(w, req) {
		return
	}
	values, ok := requestQuery(w, req)
	if !ok {
		return
	}
	count, e1 := parseDecimalID(values.Get("count"))
	revision, e2 := parseDecimalID(values.Get("revision"))
	if !exactQuery(values, "q", "source", "count", "revision", "scope") || e1 != nil || e2 != nil {
		writeResourceError(w, ErrInvalidRequest)
		return
	}
	items, err := api.repository.selectCatalogMatches(req.Context(), principal.UserID, endpointID, keyID, values.Get("source"), values.Get("q"), values.Get("scope"), count, revision)
	if err != nil {
		writeResourceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (api *httpAPI) refreshDiscovery(writer http.ResponseWriter, request *http.Request, principal UserPrincipal) {
	endpointID, ok := parsePathID(writer, request, "id")
	if !ok {
		return
	}
	keyID, ok := parsePathID(writer, request, "keyId")
	if !ok {
		return
	}
	mutation, ok := noBodyMutation(writer, request, routeDiscovery, endpointID, keyID)
	if !ok {
		return
	}
	result, err := api.repository.RefreshDiscovery(request.Context(), principal.UserID, endpointID, keyID, mutation)
	if err != nil {
		writeResourceError(writer, err)
		return
	}
	writeMutation(writer, result)
}

func (api *httpAPI) createManualEntries(writer http.ResponseWriter, request *http.Request, principal UserPrincipal) {
	endpointID, ok := parsePathID(writer, request, "id")
	if !ok {
		return
	}
	keyID, ok := parsePathID(writer, request, "keyId")
	if !ok || !requireEmptyQuery(writer, request) {
		return
	}
	var body createManualRequest
	if _, ok := decodeStrictObject(writer, request, &body); !ok || !body.Entries.Set {
		if ok {
			writeResourceError(writer, ErrInvalidRequest)
		}
		return
	}
	entries := make([]ManualCatalogInput, len(body.Entries.Value))
	for index, entry := range body.Entries.Value {
		if !entry.UpstreamModelID.Set || !entry.Provider.Set {
			writeResourceError(writer, ErrInvalidRequest)
			return
		}
		entries[index] = ManualCatalogInput{UpstreamModelID: entry.UpstreamModelID.Value, Provider: entry.Provider.Value}
	}
	canonical := createManualCanonical{Entries: entries}
	mutation, ok := controlMutation(writer, request, routeManualCatalog, []int64{endpointID, keyID}, canonical)
	if !ok {
		return
	}
	result, err := api.repository.CreateManualEntries(request.Context(), principal.UserID, endpointID, keyID, mutation, entries)
	if err != nil {
		writeResourceError(writer, err)
		return
	}
	writeMutation(writer, result)
}

func parseBindingReplacements(items []bindingReplacementRequest) ([]BindingReplacement, []bindingReplacementCanonical, bool) {
	domain := make([]BindingReplacement, len(items))
	canonical := make([]bindingReplacementCanonical, len(items))
	for index, item := range items {
		if !item.BindingID.Set || !item.ReplacementUpstreamModelID.Set {
			return nil, nil, false
		}
		bindingID, err := parseDecimalID(item.BindingID.Value)
		if err != nil {
			return nil, nil, false
		}
		domain[index] = BindingReplacement{BindingID: bindingID, ReplacementUpstreamModelID: item.ReplacementUpstreamModelID.Value}
		canonical[index] = bindingReplacementCanonical{BindingID: item.BindingID.Value, ReplacementUpstreamModelID: item.ReplacementUpstreamModelID.Value}
	}
	return domain, canonical, true
}

func (api *httpAPI) updateManualEntry(writer http.ResponseWriter, request *http.Request, principal UserPrincipal) {
	endpointID, ok := parsePathID(writer, request, "id")
	if !ok {
		return
	}
	keyID, ok := parsePathID(writer, request, "keyId")
	if !ok {
		return
	}
	entryID, ok := parsePathID(writer, request, "entryId")
	if !ok || !requireEmptyQuery(writer, request) {
		return
	}
	var body updateManualRequest
	if _, ok := decodeStrictObject(writer, request, &body); !ok {
		return
	}
	revision, revisionOK := canonicalExpectedRevision(body.ExpectedPairRevision)
	if !body.UpstreamModelID.Set || !body.Provider.Set || !body.Replacements.Set || !revisionOK {
		writeResourceError(writer, ErrInvalidRequest)
		return
	}
	replacements, canonicalReplacements, ok := parseBindingReplacements(body.Replacements.Value)
	if !ok {
		writeResourceError(writer, ErrInvalidRequest)
		return
	}
	canonical := updateManualCanonical{
		UpstreamModelID: body.UpstreamModelID.Value, Provider: body.Provider.Value,
		ExpectedPairRevision: body.ExpectedPairRevision.Value, Replacements: canonicalReplacements,
	}
	mutation, ok := controlMutation(writer, request, routeManualEntry, []int64{endpointID, keyID, entryID}, canonical)
	if !ok {
		return
	}
	result, err := api.repository.UpdateManualEntry(request.Context(), principal.UserID, endpointID, keyID, entryID, mutation, UpdateManualInput{
		UpstreamModelID: canonical.UpstreamModelID, Provider: canonical.Provider,
		ExpectedPairRevision: revision, Replacements: replacements,
	})
	if err != nil {
		writeResourceError(writer, err)
		return
	}
	writeMutation(writer, result)
}

func (api *httpAPI) deleteManualEntry(writer http.ResponseWriter, request *http.Request, principal UserPrincipal) {
	endpointID, ok := parsePathID(writer, request, "id")
	if !ok {
		return
	}
	keyID, ok := parsePathID(writer, request, "keyId")
	if !ok {
		return
	}
	entryID, ok := parsePathID(writer, request, "entryId")
	if !ok || !requireEmptyQuery(writer, request) {
		return
	}
	var body deleteManualRequest
	if _, ok := decodeStrictObject(writer, request, &body); !ok {
		return
	}
	revision, revisionOK := canonicalExpectedRevision(body.ExpectedPairRevision)
	if !body.Replacements.Set || !revisionOK {
		writeResourceError(writer, ErrInvalidRequest)
		return
	}
	replacements, canonicalReplacements, ok := parseBindingReplacements(body.Replacements.Value)
	if !ok {
		writeResourceError(writer, ErrInvalidRequest)
		return
	}
	canonical := deleteManualCanonical{ExpectedPairRevision: body.ExpectedPairRevision.Value, Replacements: canonicalReplacements}
	mutation, ok := controlMutation(writer, request, routeManualEntry, []int64{endpointID, keyID, entryID}, canonical)
	if !ok {
		return
	}
	result, err := api.repository.DeleteManualEntry(request.Context(), principal.UserID, endpointID, keyID, entryID, mutation, DeleteManualInput{
		ExpectedPairRevision: revision, Replacements: replacements,
	})
	if err != nil {
		writeResourceError(writer, err)
		return
	}
	writeMutation(writer, result)
}
