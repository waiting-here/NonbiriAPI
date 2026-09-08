// Package timeapi exposes the read-only, same-station time-zone registry and
// wall-clock resolution endpoints defined by the beta.2 wire contract. The
// handlers perform no persistent writes and depend only on the embedded
// calendar registry.
package timeapi

import (
	"errors"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"strings"

	"github.com/waiting-here/NonbiriAPI/internal/calendar"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

const (
	routeTimeZones        = "/api/time-zones"
	routeTimeResolve      = "/api/time/resolve"
	routeAdminTimeZones   = "/admin/api/time-zones"
	routeAdminTimeResolve = "/admin/api/time/resolve"

	maxZoneBytes   = 64
	maxUnixSeconds = int64(253402300799)
)

// RegisterRoutes mounts the time-zone and resolution endpoints on both
// stations. The handlers are read-only and ignore the principal identity.
func RegisterRoutes(users resources.UserRouteRegistrar, admins resources.AdminRouteRegistrar) error {
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
