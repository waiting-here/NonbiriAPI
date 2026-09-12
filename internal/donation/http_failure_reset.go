package donation

import (
	"net/http"
)

func (api *httpAPI) failureResetOwner(w http.ResponseWriter, r *http.Request, principal UserPrincipal) {
	donationID, ok := parsePathID(w, r, "id")
	if !ok {
		return
	}
	keyID, ok := parsePathID(w, r, "keyId")
	if !ok || !requireEmptyQuery(w, r) {
		return
	}
	var wire revisionWire
	if !decodeStrictObject(w, r, &wire) {
		return
	}
	revision, err := requiredRevision(wire.ExpectedRevision)
	if err != nil {
		writeDonationError(w, err)
		return
	}
	mutation, ok := mutationFor(w, r, routeOwnerFailureReset, []int64{donationID, keyID},
		map[string]any{"expected_revision": wire.ExpectedRevision.Value})
	if !ok {
		return
	}
	out, err := api.service.resetFailureOwner(r.Context(), principal.UserID, donationID, keyID, revision, mutation)
	if err != nil {
		writeDonationError(w, err)
		return
	}
	writeMutation(w, out)
}

type failureResetItemWire struct {
	DonationID       requiredField[string] `json:"donation_id"`
	KeyID            requiredField[string] `json:"key_id"`
	ExpectedRevision requiredField[string] `json:"expected_revision"`
}

func parseFailureResetItems(wire []failureResetItemWire) ([]failureResetItem, []FailureResetRef, error) {
	if len(wire) < 1 || len(wire) > maxFailureResetItems {
		return nil, nil, ErrInvalidRequest
	}
	items := make([]failureResetItem, len(wire))
	refs := make([]FailureResetRef, len(wire))
	keys := make(map[int64]bool, len(wire))
	revisions := make(map[int64]int64)
	for i, entry := range wire {
		donationID, err := requiredID(entry.DonationID)
		if err != nil {
			return nil, nil, err
		}
		keyID, err := requiredID(entry.KeyID)
		if err != nil || keys[keyID] {
			return nil, nil, ErrInvalidRequest
		}
		revision, err := requiredRevision(entry.ExpectedRevision)
		if err != nil {
			return nil, nil, err
		}
		if previous, found := revisions[donationID]; found && previous != revision {
			return nil, nil, ErrInvalidRequest
		}
		keys[keyID] = true
		revisions[donationID] = revision
		items[i] = failureResetItem{donationID, keyID, revision}
		refs[i] = FailureResetRef{entry.DonationID.Value, entry.KeyID.Value, entry.ExpectedRevision.Value}
	}
	return items, refs, nil
}

func (api *httpAPI) failureResetAdmin(w http.ResponseWriter, r *http.Request) {
	api.failureResetManagement(w, r, UserPrincipal{}, reviewerAdmin)
}
func (api *httpAPI) failureResetSteward(w http.ResponseWriter, r *http.Request, p UserPrincipal) {
	api.failureResetManagement(w, r, p, reviewerSteward)
}
func (api *httpAPI) failureResetManagement(w http.ResponseWriter, r *http.Request, p UserPrincipal, role reviewerRole) {
	if !requireEmptyQuery(w, r) {
		return
	}
	var wire struct {
		Items requiredField[[]failureResetItemWire] `json:"items"`
	}
	if !decodeStrictObject(w, r, &wire) {
		return
	}
	items, refs, err := parseFailureResetItems(wire.Items.Value)
	if err != nil {
		writeDonationError(w, err)
		return
	}
	route := routeAdminFailureReset
	if role == reviewerSteward {
		route = routeStewardFailureReset
	}
	mutation, ok := mutationFor(w, r, route, nil, map[string]any{"items": refs})
	if !ok {
		return
	}
	out, err := api.service.resetFailureBatch(r.Context(), role, p.UserID, items, mutation)
	if err != nil {
		writeDonationError(w, err)
		return
	}
	writeMutation(w, out)
}

type failureSelectionWire struct {
	View       requiredField[string] `json:"view"`
	DonationID requiredField[string] `json:"donation_id"`
	SourceKey  requiredField[string] `json:"source_key"`
	Status     requiredField[string] `json:"status"`
	Query      requiredField[string] `json:"q"`
	Scope      requiredField[string] `json:"scope"`
	Handling   requiredField[string] `json:"handling"`
	Idle       requiredField[string] `json:"idle"`
}

func parseFailureSelection(w failureSelectionWire) (failureSelection, error) {
	out := failureSelection{View: w.View.Value, SourceKey: w.SourceKey.Value,
		Management: ManagementFilter{Status: w.Status.Value, Query: w.Query.Value, Handling: w.Handling.Value},
		Source:     SourceFilter{Query: w.Query.Value, Scope: w.Scope.Value, Handling: w.Handling.Value, Idle: w.Idle.Value}}
	switch out.View {
	case "donations":
		if w.DonationID.Set || w.SourceKey.Set || w.Scope.Set || w.Idle.Set || !validManagementFilter(out.Management) {
			return out, ErrInvalidRequest
		}
	case "donation_keys":
		if w.SourceKey.Set || w.Status.Set || w.Query.Set || w.Scope.Set || w.Handling.Set || w.Idle.Set {
			return out, ErrInvalidRequest
		}
		id, err := requiredID(w.DonationID)
		if err != nil {
			return out, err
		}
		out.DonationID = id
	case "sources", "source_keys":
		if w.DonationID.Set || w.Status.Set || w.Scope.Set && w.Scope.Value == "" ||
			w.Handling.Set && w.Handling.Value == "" || w.Idle.Set && w.Idle.Value == "" ||
			!validSourceFilter(out.Source, out.View == "source_keys") {
			return out, ErrInvalidRequest
		}
		if out.View == "source_keys" {
			if !validSourceKey(out.SourceKey) {
				return out, ErrInvalidRequest
			}
		} else if w.SourceKey.Set || w.Idle.Set {
			return out, ErrInvalidRequest
		}
	default:
		return out, ErrInvalidRequest
	}
	return out, nil
}

func (api *httpAPI) failureSelectionAdmin(w http.ResponseWriter, r *http.Request) {
	api.failureSelectionManagement(w, r, UserPrincipal{}, reviewerAdmin)
}
func (api *httpAPI) failureSelectionSteward(w http.ResponseWriter, r *http.Request, p UserPrincipal) {
	api.failureSelectionManagement(w, r, p, reviewerSteward)
}
func (api *httpAPI) failureSelectionManagement(w http.ResponseWriter, r *http.Request, p UserPrincipal, role reviewerRole) {
	if !requireEmptyQuery(w, r) {
		return
	}
	var wire struct {
		Selection requiredField[failureSelectionWire] `json:"selection"`
		Cursor    nullableField[string]               `json:"cursor"`
	}
	if !decodeStrictObject(w, r, &wire) {
		return
	}
	if !wire.Cursor.Set || wire.Cursor.Value != nil && (*wire.Cursor.Value == "" || len(*wire.Cursor.Value) > 4096) {
		writeDonationError(w, ErrInvalidRequest)
		return
	}
	selection, err := parseFailureSelection(wire.Selection.Value)
	if err != nil {
		writeDonationError(w, err)
		return
	}
	token := ""
	if wire.Cursor.Value != nil {
		token = *wire.Cursor.Value
	}
	out, err := api.service.selectFailureReset(r.Context(), role, p.UserID, selection, token)
	if err != nil {
		writeDonationError(w, err)
		return
	}
	writeJSON(w, out)
}
