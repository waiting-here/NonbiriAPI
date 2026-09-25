// Package timeapi exposes the read-only, same-station time-zone registry and
// wall-clock resolution endpoints defined by the beta.2 wire contract. The
// handlers perform no persistent writes and depend only on the embedded
// calendar registry.
package timeapi

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"strings"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/calendar"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

const (
	routeTimeZones          = "/api/time-zones"
	routeTimeResolve        = "/api/time/resolve"
	routeAdminTimeZones     = "/admin/api/time-zones"
	routeAdminTimeResolve   = "/admin/api/time/resolve"
	routeAdminTimeContext   = "/admin/api/time-context"
	routeStewardTimeContext = "/api/steward/time-context"

	maxZoneBytes   = 64
	maxUnixSeconds = int64(253402300799)
)

// ContextResolver supplies the fixed site offset after rechecking the live
// station capability in the same transaction as the read. The time API keeps
// this seam narrow so it cannot expose the rest of the site configuration.
type ContextResolver interface {
	AdminTimeContext(context.Context, int64) (TimeContext, error)
	StewardTimeContext(context.Context, int64) (TimeContext, error)
}

type TimeContext struct {
	Configured    bool
	OffsetMinutes int
}

// RegisterRoutes mounts the time-zone and resolution endpoints on both
// stations and, when supplied, the authorized site-context endpoints.
func RegisterRoutes(users resources.UserRouteRegistrar, admins resources.AdminRouteRegistrar, resolvers ...ContextResolver) error {
	if isNilInterface(users) || isNilInterface(admins) {
		return errors.New("timeapi: route registrars are required")
	}
	if err := users.RegisterUserRoute(http.MethodGet, routeTimeZones, getTimeZones); err != nil {
		return err
	}
	if err := users.RegisterUserRoute(http.MethodGet, routeTimeResolve, resolveTime); err != nil {
		return err
	}
	if err := admins.RegisterAdminRoute(http.MethodGet, routeAdminTimeZones, adminGetTimeZones); err != nil {
		return err
	}
	if err := admins.RegisterAdminRoute(http.MethodGet, routeAdminTimeResolve, adminResolveTime); err != nil {
		return err
	}
	if len(resolvers) > 1 {
		return errors.New("timeapi: only one context resolver is supported")
	}
	if len(resolvers) == 1 && !isNilInterface(resolvers[0]) {
		resolver := resolvers[0]
		if err := admins.RegisterAdminRoute(http.MethodGet, routeAdminTimeContext, func(w http.ResponseWriter, r *http.Request, p resources.AdminPrincipal) {
			getTimeContext(w, r, resolver, p.UserID, true)
		}); err != nil {
			return err
		}
		if err := users.RegisterUserRoute(http.MethodGet, routeStewardTimeContext, func(w http.ResponseWriter, r *http.Request, p resources.UserPrincipal) {
			getTimeContext(w, r, resolver, p.UserID, false)
		}); err != nil {
			return err
		}
	}
	return nil
}

type timeZonesResponse struct {
	Version string   `json:"version"`
	Zones   []string `json:"zones"`
}

type resolveResponse struct {
	Instant       int64  `json:"instant"`
	Local         string `json:"local"`
	TimeZone      string `json:"time_zone"`
	OffsetSeconds int    `json:"offset_seconds"`
	Adjustment    string `json:"adjustment"`
}

type timeContextResponse struct {
	Mode          string `json:"mode"`
	OffsetMinutes *int   `json:"offset_minutes"`
}

func getTimeContext(writer http.ResponseWriter, request *http.Request, resolver ContextResolver, userID int64, admin bool) {
	if !requireNoBody(writer, request) || !requireEmptyQuery(writer, request) {
		return
	}
	if resolver == nil || userID <= 0 {
		writeContextError(writer, authz.ErrUnauthorized)
		return
	}
	var (
		value TimeContext
		err   error
	)
	if admin {
		value, err = resolver.AdminTimeContext(request.Context(), userID)
	} else {
		value, err = resolver.StewardTimeContext(request.Context(), userID)
	}
	if err != nil {
		writeContextError(writer, err)
		return
	}
	if !value.Configured {
		httperr.WriteJSON(writer, http.StatusOK, timeContextResponse{Mode: "site", OffsetMinutes: nil})
		return
	}
	if !db.ValidSiteTimezoneOffset(value.OffsetMinutes) {
		writeContextError(writer, db.ErrTimezoneUnavailable)
		return
	}
	offset := value.OffsetMinutes
	httperr.WriteJSON(writer, http.StatusOK, timeContextResponse{Mode: "site", OffsetMinutes: &offset})
}

func writeContextError(writer http.ResponseWriter, err error) {
	status := http.StatusServiceUnavailable
	code := httperr.CodeServiceUnavailable
	message := "time context unavailable"
	if errors.Is(err, authz.ErrUnauthorized) {
		status, code, message = http.StatusUnauthorized, httperr.CodeUnauthorized, "authentication required"
	} else if errors.Is(err, authz.ErrForbidden) {
		status, code, message = http.StatusForbidden, httperr.CodeForbidden, "forbidden"
	} else if errors.Is(err, db.ErrTimezoneUnavailable) {
		status, code, message = http.StatusOK, "", ""
	}
	if status == http.StatusOK {
		httperr.WriteJSON(writer, status, timeContextResponse{Mode: "site", OffsetMinutes: nil})
		return
	}
	httperr.WriteError(writer, httperr.New(code, message))
}

func getTimeZones(writer http.ResponseWriter, request *http.Request, _ resources.UserPrincipal) {
	writeTimeZones(writer, request)
}

func adminGetTimeZones(writer http.ResponseWriter, request *http.Request, _ resources.AdminPrincipal) {
	writeTimeZones(writer, request)
}

func writeTimeZones(writer http.ResponseWriter, request *http.Request) {
	if !requireNoBody(writer, request) || !requireEmptyQuery(writer, request) {
		return
	}
	httperr.WriteJSON(writer, http.StatusOK, timeZonesResponse{
		Version: calendar.ZoneVersion(),
		Zones:   calendar.Zones(),
	})
}

func resolveTime(writer http.ResponseWriter, request *http.Request, _ resources.UserPrincipal) {
	writeResolve(writer, request)
}

func adminResolveTime(writer http.ResponseWriter, request *http.Request, _ resources.AdminPrincipal) {
	writeResolve(writer, request)
}

func writeResolve(writer http.ResponseWriter, request *http.Request) {
	if !requireNoBody(writer, request) {
		return
	}
	if request == nil || request.URL == nil {
		writeInvalid(writer)
		return
	}
	if request.URL.ForceQuery || strings.Contains(request.URL.RawQuery, ";") {
		writeInvalid(writer)
		return
	}
	query, err := url.ParseQuery(request.URL.RawQuery)
	if err != nil || !exactResolveQuery(query, request.URL.RawQuery) {
		writeInvalid(writer)
		return
	}
	locals := query["local"]
	zones := query["time_zone"]
	local := locals[0]
	zoneName := zones[0]
	if len(zoneName) == 0 || len(zoneName) > maxZoneBytes || !calendar.ValidZone(zoneName) {
		writeInvalid(writer)
		return
	}
	result, err := calendar.Resolve(local, zoneName)
	if err != nil {
		writeInvalid(writer)
		return
	}
	if result.Instant < 0 || result.Instant > maxUnixSeconds {
		writeInvalid(writer)
		return
	}
	httperr.WriteJSON(writer, http.StatusOK, resolveResponse{
		Instant:       result.Instant,
		Local:         result.Local,
		TimeZone:      result.TimeZone,
		OffsetSeconds: result.OffsetSeconds,
		Adjustment:    result.Adjustment,
	})
}

func requireNoBody(writer http.ResponseWriter, request *http.Request) bool {
	if request == nil {
		writeInvalid(writer)
		return false
	}
	if request.ContentLength != 0 || len(request.TransferEncoding) != 0 ||
		strings.TrimSpace(request.Header.Get("Transfer-Encoding")) != "" {
		writeInvalid(writer)
		return false
	}
	if request.Body == nil {
		return true
	}
	var probe [1]byte
	n, err := request.Body.Read(probe[:])
	if n != 0 || !errors.Is(err, io.EOF) {
		writeInvalid(writer)
		return false
	}
	return true
}

func requireEmptyQuery(writer http.ResponseWriter, request *http.Request) bool {
	if request == nil || request.URL == nil || request.URL.ForceQuery || request.URL.RawQuery != "" {
		writeInvalid(writer)
		return false
	}
	return true
}

func exactResolveQuery(query url.Values, rawQuery string) bool {
	if rawQuery == "" || strings.HasPrefix(rawQuery, "&") || strings.HasSuffix(rawQuery, "&") || strings.Contains(rawQuery, "&&") {
		return false
	}
	if len(query) != 2 {
		return false
	}
	for key, values := range query {
		switch key {
		case "local", "time_zone":
			if len(values) != 1 {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func writeInvalid(writer http.ResponseWriter) {
	httperr.WriteError(writer, httperr.New(httperr.CodeInvalidRequest, "invalid request"))
}

func isNilInterface(value any) bool {
	if value == nil {
		return true
	}
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return v.IsNil()
	default:
		return false
	}
}
