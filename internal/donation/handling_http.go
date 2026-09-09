package donation

import "net/http"

func (api *httpAPI) badgeAdmin(w http.ResponseWriter, r *http.Request) {
	api.badgeRole(w, r, UserPrincipal{}, reviewerAdmin)
}
func (api *httpAPI) badgeSteward(w http.ResponseWriter, r *http.Request, p UserPrincipal) {
	api.badgeRole(w, r, p, reviewerSteward)
}

func (api *httpAPI) badgeRole(w http.ResponseWriter, r *http.Request, p UserPrincipal, role reviewerRole) {
	if r.URL.RawQuery != "" || r.ContentLength > 0 || len(r.TransferEncoding) > 0 {
		writeDonationError(w, ErrInvalidRequest)
		return
	}
	if !requireNoBody(w, r) {
		return
	}
	value, err := api.service.badge(r.Context(), role, p.UserID)
	if err != nil {
		writeDonationError(w, err)
		return
	}
	writeJSON(w, value)
}

type processWire struct {
	ExpectedHandlingRevision requiredField[string] `json:"expected_handling_revision"`
}

func (api *httpAPI) processAdmin(w http.ResponseWriter, r *http.Request) {
	api.processRole(w, r, UserPrincipal{}, reviewerAdmin)
}
func (api *httpAPI) processSteward(w http.ResponseWriter, r *http.Request, p UserPrincipal) {
	api.processRole(w, r, p, reviewerSteward)
}

func (api *httpAPI) processRole(w http.ResponseWriter, r *http.Request, p UserPrincipal, role reviewerRole) {
	id, ok := parsePathID(w, r, "id")
	if !ok || !requireEmptyQuery(w, r) {
		return
	}
	var wire processWire
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	if !decodeStrictObject(w, r, &wire) {
		return
	}
	revision, err := requiredRevision(wire.ExpectedHandlingRevision)
	if err != nil {
		writeDonationError(w, ErrInvalidRequest)
		return
	}
	route := routeAdminProcessed
	if role == reviewerSteward {
		route = routeStewardProcessed
	}
	mutation, ok := mutationFor(w, r, route, []int64{id}, map[string]any{"expected_handling_revision": wire.ExpectedHandlingRevision.Value})
	if !ok {
		return
	}
	out, err := api.service.process(r.Context(), role, p.UserID, id, mutation, revision)
	if err != nil {
		writeDonationError(w, err)
		return
	}
	writeMutation(w, out)
}
