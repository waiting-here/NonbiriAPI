package donation

import "net/http"

func (api *httpAPI) failurePolicyOwner(w http.ResponseWriter, r *http.Request, p UserPrincipal) {
	api.failurePolicyEdit(w, r, p, "")
}
func (api *httpAPI) failurePolicyAdmin(w http.ResponseWriter, r *http.Request) {
	api.failurePolicyEdit(w, r, UserPrincipal{}, reviewerAdmin)
}
func (api *httpAPI) failurePolicySteward(w http.ResponseWriter, r *http.Request, p UserPrincipal) {
	api.failurePolicyEdit(w, r, p, reviewerSteward)
}
func (api *httpAPI) failurePolicyEdit(w http.ResponseWriter, r *http.Request, p UserPrincipal, role reviewerRole) {
	donationID, ok := parsePathID(w, r, "id")
	if !ok {
		return
	}
	keyID, ok := parsePathID(w, r, "keyId")
	if !ok || !requireEmptyQuery(w, r) {
		return
	}
	var wire struct {
		ExpectedRevision requiredField[string] `json:"expected_revision"`
		Threshold        requiredField[string] `json:"failure_disable_threshold"`
	}
	if !decodeStrictObject(w, r, &wire) {
		return
	}
	expected, err := requiredRevision(wire.ExpectedRevision)
	if err != nil || !wire.Threshold.Set {
		writeDonationError(w, ErrInvalidRequest)
		return
	}
	route := routeOwnerFailurePolicy
	if role == reviewerAdmin {
		route = routeAdminFailurePolicy
	}
	if role == reviewerSteward {
		route = routeStewardFailurePolicy
	}
	mutation, ok := mutationFor(w, r, route, []int64{donationID, keyID}, map[string]any{"expected_revision": wire.ExpectedRevision.Value, "failure_disable_threshold": wire.Threshold.Value})
	if !ok {
		return
	}
	out, err := api.service.failurePolicy(r.Context(), p.UserID, role, donationID, keyID, expected, wire.Threshold.Value, mutation)
	if err != nil {
		writeDonationError(w, err)
		return
	}
	writeMutation(w, out)
}
