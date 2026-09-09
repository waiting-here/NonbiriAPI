package donation

import (
	"net/http"
	"net/url"

	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

const (
	routeOwnerKeys         = "/api/donations/{id}/keys"
	routeAdminKeys         = "/admin/api/donations/{id}/keys"
	routeStewardKeys       = "/api/steward/donations/{id}/keys"
	routeAdminSources      = "/admin/api/donation-sources"
	routeStewardSources    = "/api/steward/donation-sources"
	routeAdminSourceKeys   = "/admin/api/donation-sources/{source_key}/keys"
	routeStewardSourceKeys = "/api/steward/donation-sources/{source_key}/keys"
)

func parseNumberedQuery(values url.Values, allowed ...string) (pagination.Request, error) {
	for key, entries := range values {
		known := key == "page" || key == "page_size"
		for _, name := range allowed {
			if key == name {
				known = true
				break
			}
		}
		if !known || len(entries) != 1 {
			return pagination.Request{}, ErrInvalidRequest
		}
	}
	page, _, err := pagination.Parse(values)
	if err != nil {
		return pagination.Request{}, ErrInvalidRequest
	}
	return page, nil
}

// Returning true means a numbered response or an error has already been sent.
func (api *httpAPI) listNumbered(writer http.ResponseWriter, request *http.Request, principal UserPrincipal, role reviewerRole, values url.Values) bool {
	_, numbered, err := pagination.Parse(values)
	if err != nil {
		writeDonationError(writer, ErrInvalidRequest)
		return true
	}
	if !numbered {
		return false
	}
	allowed := []string{"status", "q"}
	if role != recurringOwner {
		allowed = append(allowed, "handling")
	}
	page, err := parseNumberedQuery(values, allowed...)
	if err != nil {
		writeDonationError(writer, err)
		return true
	}
	filter := ManagementFilter{Status: values.Get("status"), Query: values.Get("q"), Handling: values.Get("handling")}
	switch role {
	case recurringOwner:
		out, err := api.service.DonationsOwnerPage(request.Context(), principal.UserID, filter, page)
		if err != nil {
			writeDonationError(writer, err)
		} else {
			writeJSON(writer, out)
		}
	case reviewerAdmin:
		out, err := api.service.DonationsAdminPage(request.Context(), filter, page)
		if err != nil {
			writeDonationError(writer, err)
		} else {
			writeJSON(writer, out)
		}
	case reviewerSteward:
		out, err := api.service.DonationsStewardPage(request.Context(), principal.UserID, filter, page)
		if err != nil {
			writeDonationError(writer, err)
		} else {
			writeJSON(writer, out)
		}
	default:
		writeDonationError(writer, ErrInvalidRequest)
	}
	return true
}

func (api *httpAPI) keysOwner(writer http.ResponseWriter, request *http.Request, principal UserPrincipal) {
	api.keysNumbered(writer, request, principal, recurringOwner)
}
func (api *httpAPI) keysAdmin(writer http.ResponseWriter, request *http.Request) {
	api.keysNumbered(writer, request, UserPrincipal{}, reviewerAdmin)
}
func (api *httpAPI) keysSteward(writer http.ResponseWriter, request *http.Request, principal UserPrincipal) {
	api.keysNumbered(writer, request, principal, reviewerSteward)
}

func (api *httpAPI) keysNumbered(writer http.ResponseWriter, request *http.Request, principal UserPrincipal, role reviewerRole) {
	id, ok := parsePathID(writer, request, "id")
	if !ok || !requireNoBody(writer, request) {
		return
	}
	values, ok := requestQuery(writer, request)
	if !ok {
		return
	}
	page, err := parseNumberedQuery(values)
	if err != nil {
		writeDonationError(writer, err)
		return
	}
	if role == recurringOwner {
		out, err := api.service.KeysOwnerPage(request.Context(), principal.UserID, id, page)
		if err != nil {
			writeDonationError(writer, err)
		} else {
			writeJSON(writer, out)
		}
		return
	}
	out, err := api.service.keysPage(request.Context(), role, principal.UserID, id, page)
	if err != nil {
		writeDonationError(writer, err)
	} else {
		writeJSON(writer, out)
	}
}

func (api *httpAPI) sourcesAdmin(writer http.ResponseWriter, request *http.Request) {
	api.sourcesNumbered(writer, request, UserPrincipal{}, reviewerAdmin, false)
}
func (api *httpAPI) sourcesSteward(writer http.ResponseWriter, request *http.Request, principal UserPrincipal) {
	api.sourcesNumbered(writer, request, principal, reviewerSteward, false)
}
func (api *httpAPI) sourceKeysAdmin(writer http.ResponseWriter, request *http.Request) {
	api.sourcesNumbered(writer, request, UserPrincipal{}, reviewerAdmin, true)
}
func (api *httpAPI) sourceKeysSteward(writer http.ResponseWriter, request *http.Request, principal UserPrincipal) {
	api.sourcesNumbered(writer, request, principal, reviewerSteward, true)
}

func (api *httpAPI) sourcesNumbered(writer http.ResponseWriter, request *http.Request, principal UserPrincipal, role reviewerRole, keys bool) {
	if !requireNoBody(writer, request) {
		return
	}
	values, ok := requestQuery(writer, request)
	if !ok {
		return
	}
	allowed := []string{"q", "scope", "handling"}
	if keys {
		allowed = append(allowed, "idle")
	}
	page, err := parseNumberedQuery(values, allowed...)
	if err != nil {
		writeDonationError(writer, err)
		return
	}
	filter := SourceFilter{Query: values.Get("q"), Scope: values.Get("scope"), Handling: values.Get("handling"), Idle: values.Get("idle")}
	if value, present := values["scope"]; present && value[0] == "" {
		writeDonationError(writer, ErrInvalidRequest)
		return
	}
	if value, present := values["handling"]; present && value[0] == "" {
		writeDonationError(writer, ErrInvalidRequest)
		return
	}
	if value, present := values["idle"]; present && value[0] == "" {
		writeDonationError(writer, ErrInvalidRequest)
		return
	}
	if keys {
		out, err := api.service.sourceKeysPage(request.Context(), role, principal.UserID, request.PathValue("source_key"), filter, page)
		if err != nil {
			writeDonationError(writer, err)
		} else {
			writeJSON(writer, out)
		}
		return
	}
	out, err := api.service.sourcesPage(request.Context(), role, principal.UserID, filter, page)
	if err != nil {
		writeDonationError(writer, err)
	} else {
		writeJSON(writer, out)
	}
}
