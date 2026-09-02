package checkin

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"github.com/waiting-here/NonbiriAPI/internal/httperr"
)

type httpAPI struct{ service *Service }

func RegisterRoutes(registrar UserRouteRegistrar, service *Service) error {
	if nilInterface(registrar) || service == nil || service.database == nil {
		return errors.New("checkin: user registrar and service are required")
	}
	api := &httpAPI{service: service}
	routes := []struct {
		method, pattern string
		handler         AuthorizedUserHandler
	}{
		{http.MethodGet, Route, api.status},
		{http.MethodPost, Route, api.checkin},
	}
	for _, route := range routes {
		if err := registrar.RegisterUserRoute(route.method, route.pattern, route.handler); err != nil {
			return fmt.Errorf("checkin: register %s %s: %w", route.method, route.pattern, err)
		}
	}
	return nil
}

func (api *httpAPI) status(writer http.ResponseWriter, request *http.Request, principal UserPrincipal) {
	if !requireEmptyQuery(writer, request) || !requireNoBody(writer, request) {
		return
	}
	status, err := api.service.Status(request.Context(), principal.UserID)
	if err != nil {
		writeError(writer, err)
		return
	}
	if !status.Enabled {
		writeJSON(writer, http.StatusOK, struct {
			Enabled bool `json:"enabled"`
		}{Enabled: false})
		return
	}
	writeJSON(writer, http.StatusOK, struct {
		Enabled        bool   `json:"enabled"`
		CheckedInToday bool   `json:"checked_in_today"`
		Balance        string `json:"balance"`
		AwardMinimum   string `json:"award_min"`
		AwardMaximum   string `json:"award_max"`
		BalanceCap     string `json:"balance_cap"`
	}{
		Enabled: true, CheckedInToday: status.CheckedInToday, Balance: status.Balance,
		AwardMinimum: status.AwardMinimum, AwardMaximum: status.AwardMaximum, BalanceCap: status.BalanceCap,
	})
}

func (api *httpAPI) checkin(writer http.ResponseWriter, request *http.Request, principal UserPrincipal) {
	if !requireEmptyQuery(writer, request) || !requireNoBody(writer, request) {
		return
	}
	result, err := api.service.Checkin(request.Context(), principal.UserID)
	if err != nil {
		writeError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, result)
}

func requireEmptyQuery(writer http.ResponseWriter, request *http.Request) bool {
	if request == nil || request.URL == nil || request.URL.ForceQuery {
		writeError(writer, ErrInvalidRequest)
		return false
	}
	values, err := url.ParseQuery(request.URL.RawQuery)
	if err != nil || len(values) != 0 {
		writeError(writer, ErrInvalidRequest)
		return false
	}
	return true
}

func requireNoBody(writer http.ResponseWriter, request *http.Request) bool {
	if request == nil || request.Body == nil || request.Body == http.NoBody {
		return true
	}
	buffer := make([]byte, 1)
	read, err := request.Body.Read(buffer)
	if err != nil && !errors.Is(err, io.EOF) || read != 0 {
		writeError(writer, ErrInvalidRequest)
		return false
	}
	return true
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	httperr.WriteJSON(writer, status, value)
}

func writeError(writer http.ResponseWriter, err error) {
	code, message := httperr.CodeInternal, "internal error"
	switch {
	case errors.Is(err, ErrInvalidRequest):
		code, message = httperr.CodeInvalidRequest, "invalid request"
	case errors.Is(err, ErrUnauthorized):
		code, message = httperr.CodeUnauthorized, "authentication required"
	case errors.Is(err, ErrForbidden):
		code, message = httperr.CodeForbidden, "access forbidden"
	case errors.Is(err, ErrNotFound):
		code, message = httperr.CodeNotFound, "resource not found"
	case errors.Is(err, ErrFeatureDisabled):
		code, message = httperr.CodeFeatureDisabled, "check-in is unavailable"
	case errors.Is(err, ErrAlreadyCheckedIn):
		code, message = httperr.CodeAlreadyCheckedIn, "already checked in today"
	case errors.Is(err, ErrBalanceCap):
		code, message = httperr.CodeCheckinCapReached, "check-in balance cap reached"
	case errors.Is(err, ErrMaintenance):
		code, message = httperr.CodeMaintenance, "maintenance mode"
	case errors.Is(err, ErrResourceLimit):
		code, message = httperr.CodeResourceLimitExceeded, "resource limit exceeded"
	case errors.Is(err, ErrUnavailable):
		code, message = httperr.CodeServiceUnavailable, "service unavailable"
	}
	httperr.WriteError(writer, httperr.New(code, message))
}
