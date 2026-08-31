package logapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/waiting-here/NonbiriAPI/internal/httperr"
)

type HTTPAPI struct {
	repository *Repository
	steward    StewardAuthorizer
}

func NewHTTPAPI(repository *Repository, steward StewardAuthorizer) (*HTTPAPI, error) {
	if repository == nil {
		return nil, ErrInvalid
	}
	return &HTTPAPI{repository: repository, steward: steward}, nil
}

func RegisterUserRoutes(registrar UserRouteRegistrar, repository *Repository) error {
	if registrar == nil {
		return ErrInvalid
	}
	api, err := NewHTTPAPI(repository, nil)
	if err != nil {
		return err
	}
	if err := registrar.RegisterUserRoute(http.MethodGet, "/api/logs", api.userList); err != nil {
		return err
	}
	return registrar.RegisterUserRoute(http.MethodGet, "/api/logs/{id}", api.userDetail)
}

func RegisterStewardRoutes(registrar UserRouteRegistrar, repository *Repository, authorizer StewardAuthorizer) error {
	if registrar == nil || authorizer == nil {
		return ErrInvalid
	}
	api, err := NewHTTPAPI(repository, authorizer)
	if err != nil {
		return err
	}
	if err := registrar.RegisterUserRoute(http.MethodGet, "/api/steward/logs", api.stewardList); err != nil {
		return err
	}
	return registrar.RegisterUserRoute(http.MethodGet, "/api/steward/logs/{id}", api.stewardDetail)
}

func RegisterAdminRoutes(registrar AdminRouteRegistrar, repository *Repository) error {
	if registrar == nil {
		return ErrInvalid
	}
	api, err := NewHTTPAPI(repository, nil)
	if err != nil {
		return err
	}
	routes := []struct {
		path    string
		handler http.Handler
	}{
		{"/admin/api/logs", http.HandlerFunc(api.adminList)},
		{"/admin/api/logs/export.csv", http.HandlerFunc(api.adminExportCSV)},
		{"/admin/api/logs/export.json", http.HandlerFunc(api.adminExportJSON)},
		{"/admin/api/logs/{id}", http.HandlerFunc(api.adminDetail)},
	}
	for _, route := range routes {
		if err := registrar.RegisterAdminRoute(http.MethodGet, route.path, route.handler); err != nil {
			return err
		}
	}
	return nil
}

func (api *HTTPAPI) userList(writer http.ResponseWriter, request *http.Request, principal UserPrincipal) {
	if !requireNoBody(writer, request) {
		return
	}
	filter, err := parseListFilter(request.URL.RawQuery, "user", false)
	if err != nil {
		writeLogError(writer, err)
		return
	}
	page, err := api.repository.ListUser(request.Context(), principal.UserID, filter)
	if err != nil {
		writeLogError(writer, err)
		return
	}
	writeLogJSON(writer, http.StatusOK, page)
}

func (api *HTTPAPI) userDetail(writer http.ResponseWriter, request *http.Request, principal UserPrincipal) {
	if !requireNoBody(writer, request) {
		return
	}
	filter, err := parseAttemptFilter(request.URL.RawQuery)
	if err != nil {
		writeLogError(writer, err)
		return
	}
	detail, err := api.repository.GetUser(request.Context(), principal.UserID, request.PathValue("id"), filter)
	if err != nil {
		writeLogError(writer, err)
		return
	}
	writeLogJSON(writer, http.StatusOK, detail)
}

func (api *HTTPAPI) adminList(writer http.ResponseWriter, request *http.Request) {
	if !requireNoBody(writer, request) {
		return
	}
	filter, err := parseListFilter(request.URL.RawQuery, "admin", false)
	if err != nil {
		writeLogError(writer, err)
		return
	}
	page, err := api.repository.ListAdmin(request.Context(), filter)
	if err != nil {
		writeLogError(writer, err)
		return
	}
	writeLogJSON(writer, http.StatusOK, page)
}

func (api *HTTPAPI) adminDetail(writer http.ResponseWriter, request *http.Request) {
	if !requireNoBody(writer, request) {
		return
	}
	filter, err := parseAttemptFilter(request.URL.RawQuery)
	if err != nil {
		writeLogError(writer, err)
		return
	}
	detail, err := api.repository.GetAdmin(request.Context(), request.PathValue("id"), filter)
	if err != nil {
		writeLogError(writer, err)
		return
	}
	writeLogJSON(writer, http.StatusOK, detail)
}

func (api *HTTPAPI) stewardList(writer http.ResponseWriter, request *http.Request, principal UserPrincipal) {
	if !api.authorizeSteward(writer, request, principal.UserID) || !requireNoBody(writer, request) {
		return
	}
	filter, err := parseListFilter(request.URL.RawQuery, "steward", false)
	if err != nil {
		writeLogError(writer, err)
		return
	}
	page, err := api.repository.ListSteward(request.Context(), principal.UserID, filter, api.steward)
	if err != nil {
		writeLogError(writer, err)
		return
	}
	writeLogJSON(writer, http.StatusOK, page)
}

func (api *HTTPAPI) stewardDetail(writer http.ResponseWriter, request *http.Request, principal UserPrincipal) {
	if !api.authorizeSteward(writer, request, principal.UserID) || !requireNoBody(writer, request) {
		return
	}
	filter, err := parseAttemptFilter(request.URL.RawQuery)
	if err != nil {
		writeLogError(writer, err)
		return
	}
	detail, err := api.repository.GetSteward(
		request.Context(), principal.UserID, request.PathValue("id"), filter, api.steward,
	)
	if err != nil {
		writeLogError(writer, err)
		return
	}
	writeLogJSON(writer, http.StatusOK, detail)
}

func (api *HTTPAPI) authorizeSteward(writer http.ResponseWriter, request *http.Request, userID int64) bool {
	if api.steward == nil || userID <= 0 {
		writeLogError(writer, ErrForbidden)
		return false
	}
	if err := api.repository.authorizeStewardRead(request.Context(), userID, api.steward); err != nil {
		if errors.Is(err, ErrUnavailable) {
			writeLogError(writer, ErrUnavailable)
		} else {
			writeLogError(writer, ErrForbidden)
		}
		return false
	}
	return true
}

func (api *HTTPAPI) adminExportJSON(writer http.ResponseWriter, request *http.Request) {
	api.adminExport(writer, request, false)
}

func (api *HTTPAPI) adminExportCSV(writer http.ResponseWriter, request *http.Request) {
	api.adminExport(writer, request, true)
}

func (api *HTTPAPI) adminExport(writer http.ResponseWriter, request *http.Request, csv bool) {
	if !requireNoBody(writer, request) {
		return
	}
	filter, err := parseListFilter(request.URL.RawQuery, "admin", true)
	if err != nil {
		writeLogError(writer, err)
		return
	}
	rows, err := api.repository.ExportAdmin(request.Context(), filter)
	if err != nil {
		writeLogError(writer, err)
		return
	}
	var body []byte
	if csv {
		body, err = MarshalAdminCSV(rows)
	} else {
		body, err = MarshalAdminJSON(rows)
	}
	if err != nil {
		writeLogError(writer, err)
		return
	}
	writer.Header().Set("Cache-Control", "no-store")
	if csv {
		writer.Header().Set("Content-Type", "text/csv; charset=utf-8")
		writer.Header().Set("Content-Disposition", `attachment; filename="nonbiri-logs.csv"`)
	} else {
		writer.Header().Set("Content-Type", "application/json; charset=utf-8")
		writer.Header().Set("Content-Disposition", `attachment; filename="nonbiri-logs.json"`)
	}
	writer.WriteHeader(http.StatusOK)
	_, _ = writer.Write(body)
}

func parseListFilter(rawQuery, role string, export bool) (ListFilter, error) {
	values, err := url.ParseQuery(rawQuery)
	if err != nil {
		return ListFilter{}, ErrInvalid
	}
	filter := ListFilter{}
	allowed := map[string]bool{
		"error_code": true, "status": true, "from": true, "to": true,
	}
	switch role {
	case "user":
		allowed["model"] = true
	case "admin":
		allowed["user_id"] = true
		allowed["endpoint_base_url"] = true
		allowed["upstream_model"] = true
	case "steward":
		allowed["endpoint_base_url"] = true
		allowed["upstream_model"] = true
	default:
		return ListFilter{}, ErrInvalid
	}
	if !export {
		allowed["cursor"] = true
		allowed["limit"] = true
	}
	for name, entries := range values {
		if !allowed[name] || len(entries) != 1 {
			return ListFilter{}, ErrInvalid
		}
		value := entries[0]
		switch name {
		case "user_id":
			parsed, ok := parseCanonicalInt64(value, 1, int64(^uint64(0)>>1))
			if !ok {
				return ListFilter{}, ErrInvalid
			}
			filter.UserID = &parsed
		case "endpoint_base_url":
			if value == "" || !utf8.ValidString(value) {
				return ListFilter{}, ErrInvalid
			}
			filter.EndpointBaseURL = &value
		case "upstream_model":
			if !utf8.ValidString(value) {
				return ListFilter{}, ErrInvalid
			}
			filter.UpstreamModel = &value
		case "model":
			if !utf8.ValidString(value) {
				return ListFilter{}, ErrInvalid
			}
			filter.Model = &value
		case "error_code":
			filter.ErrorCode = &value
		case "status":
			parsed, ok := parseCanonicalInt64(value, 100, 599)
			if !ok {
				return ListFilter{}, ErrInvalid
			}
			status := int(parsed)
			filter.Status = &status
		case "from":
			parsed, ok := parseCanonicalInt64(value, 0, maxUnixSecond)
			if !ok {
				return ListFilter{}, ErrInvalid
			}
			filter.From = &parsed
		case "to":
			parsed, ok := parseCanonicalInt64(value, 0, maxUnixSecond)
			if !ok {
				return ListFilter{}, ErrInvalid
			}
			filter.To = &parsed
		case "cursor":
			if value == "" {
				return ListFilter{}, ErrInvalid
			}
			filter.Cursor = value
		case "limit":
			parsed, ok := parseCanonicalInt64(value, 1, maximumLimit)
			if !ok {
				return ListFilter{}, ErrInvalid
			}
			filter.Limit = int(parsed)
		}
	}
	if export {
		filter.Limit = 0
	}
	return filter, nil
}

func parseAttemptFilter(rawQuery string) (AttemptFilter, error) {
	values, err := url.ParseQuery(rawQuery)
	if err != nil {
		return AttemptFilter{}, ErrInvalid
	}
	filter := AttemptFilter{}
	for name, entries := range values {
		if len(entries) != 1 {
			return AttemptFilter{}, ErrInvalid
		}
		switch name {
		case "attempt_cursor":
			if entries[0] == "" {
				return AttemptFilter{}, ErrInvalid
			}
			filter.Cursor = entries[0]
		case "attempt_limit":
			parsed, ok := parseCanonicalInt64(entries[0], 1, maximumLimit)
			if !ok {
				return AttemptFilter{}, ErrInvalid
			}
			filter.Limit = int(parsed)
		default:
			return AttemptFilter{}, ErrInvalid
		}
	}
	return filter, nil
}

func parseCanonicalInt64(value string, minimum, maximum int64) (int64, bool) {
	if value == "" || (len(value) > 1 && value[0] == '0') || strings.HasPrefix(value, "+") || strings.HasPrefix(value, "-") {
		return 0, false
	}
	for i := range value {
		if value[i] < '0' || value[i] > '9' {
			return 0, false
		}
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	return parsed, err == nil && parsed >= minimum && parsed <= maximum
}

func requireNoBody(writer http.ResponseWriter, request *http.Request) bool {
	if request == nil || request.Body == nil || request.Body == http.NoBody {
		return true
	}
	data, err := io.ReadAll(io.LimitReader(request.Body, 1))
	if err != nil || len(data) != 0 {
		writeLogError(writer, ErrInvalid)
		return false
	}
	return true
}

func writeLogJSON(writer http.ResponseWriter, status int, body any) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-store")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(body)
}

func writeLogError(writer http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrInvalid):
		httperr.WriteError(writer, httperr.New(httperr.CodeInvalidRequest, "invalid log request"))
	case errors.Is(err, ErrNotFound):
		httperr.WriteError(writer, httperr.New(httperr.CodeNotFound, "log was not found"))
	case errors.Is(err, ErrForbidden):
		httperr.WriteError(writer, httperr.New(httperr.CodeForbidden, "steward access is required"))
	case errors.Is(err, ErrConflict):
		httperr.WriteError(writer, httperr.New(httperr.CodeConflict, "log is not terminal"))
	case errors.Is(err, ErrCapacity):
		httperr.WriteError(writer, httperr.New(httperr.CodePayloadTooLarge, "log export exceeds its bound"))
	case errors.Is(err, ErrUnavailable):
		httperr.WriteError(writer, httperr.New(httperr.CodeServiceUnavailable, "logs are unavailable"))
	default:
		httperr.WriteError(writer, httperr.New(httperr.CodeInternal, "log projection failed"))
	}
}
