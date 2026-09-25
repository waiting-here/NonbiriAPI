package timeapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/httperr"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

type stubContextResolver struct {
	admin   TimeContext
	steward TimeContext
	err     error
}

func (resolver stubContextResolver) AdminTimeContext(context.Context, int64) (TimeContext, error) {
	return resolver.admin, resolver.err
}

func (resolver stubContextResolver) StewardTimeContext(context.Context, int64) (TimeContext, error) {
	return resolver.steward, resolver.err
}

func TestGetTimeContextReturnsOnlyFixedSiteOffset(t *testing.T) {
	offset := 330
	resolver := stubContextResolver{
		admin:   TimeContext{Configured: true, OffsetMinutes: offset},
		steward: TimeContext{Configured: false},
	}
	tests := []struct {
		name  string
		admin bool
		want  string
	}{
		{name: "admin configured", admin: true, want: `{"mode":"site","offset_minutes":330}`},
		{name: "steward unconfigured", admin: false, want: `{"mode":"site","offset_minutes":null}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, "/time-context", nil)
			getTimeContext(recorder, request, resolver, 7, test.admin)
			if recorder.Code != http.StatusOK {
				t.Fatalf("status=%d, body=%s", recorder.Code, recorder.Body.String())
			}
			if got := strings.TrimSpace(recorder.Body.String()); got != test.want {
				t.Fatalf("body=%s, want %s", got, test.want)
			}
		})
	}
}

func TestGetTimeContextRejectsUnauthorizedAndInvalidOffset(t *testing.T) {
	tests := []struct {
		name     string
		resolver ContextResolver
		userID   int64
		wantCode int
	}{
		{name: "missing resolver", userID: 7, wantCode: http.StatusUnauthorized},
		{name: "missing user", resolver: stubContextResolver{}, wantCode: http.StatusUnauthorized},
		{name: "invalid offset", resolver: stubContextResolver{admin: TimeContext{Configured: true, OffsetMinutes: 15}}, userID: 7, wantCode: http.StatusOK},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, "/time-context", nil)
			getTimeContext(recorder, request, test.resolver, test.userID, true)
			if recorder.Code != test.wantCode {
				t.Fatalf("status=%d, want %d; body=%s", recorder.Code, test.wantCode, recorder.Body.String())
			}
		})
	}
}

type timeEndpoint struct {
	name   string
	path   string
	invoke func(http.ResponseWriter, *http.Request)
}

func userTimeEndpoints() []timeEndpoint {
	return []timeEndpoint{
		{
			name: "user registry",
			path: routeTimeZones,
			invoke: func(writer http.ResponseWriter, request *http.Request) {
				getTimeZones(writer, request, resources.UserPrincipal{UserID: 7})
			},
		},
		{
			name: "user resolve",
			path: routeTimeResolve,
			invoke: func(writer http.ResponseWriter, request *http.Request) {
				resolveTime(writer, request, resources.UserPrincipal{UserID: 7})
			},
		},
	}
}

func adminTimeEndpoints() []timeEndpoint {
	return []timeEndpoint{
		{
			name: "admin registry",
			path: routeAdminTimeZones,
			invoke: func(writer http.ResponseWriter, request *http.Request) {
				adminGetTimeZones(writer, request, resources.AdminPrincipal{UserID: 9})
			},
		},
		{
			name: "admin resolve",
			path: routeAdminTimeResolve,
			invoke: func(writer http.ResponseWriter, request *http.Request) {
				adminResolveTime(writer, request, resources.AdminPrincipal{UserID: 9})
			},
		},
	}
}

func invokeTimeEndpoint(t *testing.T, endpoint timeEndpoint, request *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	endpoint.invoke(recorder, request)
	return recorder
}

func assertInvalidTimeResponse(t *testing.T, recorder *httptest.ResponseRecorder) {
	t.Helper()
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status=%d, want %d; body=%s", recorder.Code, http.StatusBadRequest, recorder.Body.String())
	}
	if got := recorder.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control=%q, want no-store", got)
	}
	var envelope httperr.Envelope
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode error envelope: %v; body=%q", err, recorder.Body.String())
	}
	if envelope.Error.Code != httperr.CodeInvalidRequest || envelope.Error.Source != httperr.SourcePlatform {
		t.Fatalf("error=%+v, want platform invalid_request", envelope.Error)
	}
}

func TestGetTimeZones(t *testing.T) {
	for _, endpoint := range append(userTimeEndpoints()[:1], adminTimeEndpoints()[:1]...) {
		t.Run(endpoint.name, func(t *testing.T) {
			recorder := invokeTimeEndpoint(t, endpoint, httptest.NewRequest(http.MethodGet, endpoint.path, nil))
			if recorder.Code != http.StatusOK {
				t.Fatalf("status=%d, want 200; body=%s", recorder.Code, recorder.Body.String())
			}
			var response timeZonesResponse
			if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
				t.Fatal(err)
			}
			if response.Version != "go1.26.6-zoneinfo" || len(response.Zones) < 100 {
				t.Fatalf("response=%+v", response)
			}
			for index := 1; index < len(response.Zones); index++ {
				if response.Zones[index-1] >= response.Zones[index] {
					t.Fatalf("zones are not strictly sorted at %d: %q, %q", index, response.Zones[index-1], response.Zones[index])
				}
			}
			for _, want := range []string{"UTC", "Asia/Kolkata", "US/Eastern"} {
				found := false
				for _, zone := range response.Zones {
					if zone == want {
						found = true
						break
					}
				}
				if !found {
					t.Fatalf("zones missing %q", want)
				}
			}
		})
	}
}

func TestGetTimeZonesRejectsStrictInputs(t *testing.T) {
	cases := []struct {
		name             string
		rawQuery         string
		forceQuery       bool
		body             string
		contentLength    int64
		transferEncoding []string
		transferHeader   string
	}{
		{name: "query", rawQuery: "x=1"},
		{name: "force query", forceQuery: true},
		{name: "body", body: "unexpected body"},
		{name: "declared content length", contentLength: 1},
		{name: "transfer encoding", transferEncoding: []string{"chunked"}},
		{name: "transfer encoding header", transferHeader: "chunked"},
	}
	for _, endpoint := range append(userTimeEndpoints()[:1], adminTimeEndpoints()[:1]...) {
		for _, tc := range cases {
			t.Run(endpoint.name+"/"+tc.name, func(t *testing.T) {
				var body io.Reader
				if tc.body != "" {
					body = strings.NewReader(tc.body)
				}
				request := httptest.NewRequest(http.MethodGet, endpoint.path, body)
				request.URL.RawQuery = tc.rawQuery
				request.URL.ForceQuery = tc.forceQuery
				if tc.contentLength != 0 {
					request.ContentLength = tc.contentLength
				}
				request.TransferEncoding = tc.transferEncoding
				if tc.transferHeader != "" {
					request.Header.Set("Transfer-Encoding", tc.transferHeader)
				}
				assertInvalidTimeResponse(t, invokeTimeEndpoint(t, endpoint, request))
			})
		}
	}
}

func TestResolveTimeSuccessAcrossStationsAndBoundaries(t *testing.T) {
	type resolveCase struct {
		name  string
		local string
		zone  string
		want  resolveResponse
	}
	cases := []resolveCase{
		{
			name:  "normal UTC",
			local: "2026-06-15T12:00:00",
			zone:  "UTC",
			want: resolveResponse{
				Instant:       time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC).Unix(),
				Local:         "2026-06-15T12:00:00",
				TimeZone:      "UTC",
				OffsetSeconds: 0,
				Adjustment:    "none",
			},
		},
		{
			name:  "half hour",
			local: "2026-06-15T12:00:00",
			zone:  "Asia/Kolkata",
			want: resolveResponse{
				Instant:       time.Date(2026, 6, 15, 6, 30, 0, 0, time.UTC).Unix(),
				Local:         "2026-06-15T12:00:00",
				TimeZone:      "Asia/Kolkata",
				OffsetSeconds: 5*3600 + 30*60,
				Adjustment:    "none",
			},
		},
		{
			name:  "compatibility alias",
			local: "2026-06-15T12:00:00",
			zone:  "US/Eastern",
			want: resolveResponse{
				Instant:       time.Date(2026, 6, 15, 16, 0, 0, 0, time.UTC).Unix(),
				Local:         "2026-06-15T12:00:00",
				TimeZone:      "US/Eastern",
				OffsetSeconds: -4 * 3600,
				Adjustment:    "none",
			},
		},
		{
			name:  "DST gap",
			local: "2026-03-08T02:30:00",
			zone:  "America/New_York",
			want: resolveResponse{
				Instant:       time.Date(2026, 3, 8, 7, 30, 0, 0, time.UTC).Unix(),
				Local:         "2026-03-08T03:30:00",
				TimeZone:      "America/New_York",
				OffsetSeconds: -4 * 3600,
				Adjustment:    "gap_shifted",
			},
		},
		{
			name:  "DST fold",
			local: "2026-11-01T01:30:00",
			zone:  "America/New_York",
			want: resolveResponse{
				Instant:       time.Date(2026, 11, 1, 6, 30, 0, 0, time.UTC).Unix(),
				Local:         "2026-11-01T01:30:00",
				TimeZone:      "America/New_York",
				OffsetSeconds: -5 * 3600,
				Adjustment:    "fold_later",
			},
		},
		{
			name:  "Unix lower boundary",
			local: "1970-01-01T00:00:00",
			zone:  "UTC",
			want: resolveResponse{
				Instant:       0,
				Local:         "1970-01-01T00:00:00",
				TimeZone:      "UTC",
				OffsetSeconds: 0,
				Adjustment:    "none",
			},
		},
		{
			name:  "Unix upper boundary",
			local: "9999-12-31T23:59:59",
			zone:  "UTC",
			want: resolveResponse{
				Instant:       maxUnixSeconds,
				Local:         "9999-12-31T23:59:59",
				TimeZone:      "UTC",
				OffsetSeconds: 0,
				Adjustment:    "none",
			},
		},
	}
	for _, endpoint := range append(userTimeEndpoints()[1:], adminTimeEndpoints()[1:]...) {
		for _, tc := range cases {
			t.Run(endpoint.name+"/"+tc.name, func(t *testing.T) {
				request := httptest.NewRequest(http.MethodGet, endpoint.path, nil)
				request.URL.RawQuery = "local=" + tc.local + "&time_zone=" + tc.zone
				recorder := invokeTimeEndpoint(t, endpoint, request)
				if recorder.Code != http.StatusOK {
					t.Fatalf("status=%d, want 200; body=%s", recorder.Code, recorder.Body.String())
				}
				var response resolveResponse
				if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
					t.Fatal(err)
				}
				if response != tc.want {
					t.Fatalf("response=%+v, want %+v", response, tc.want)
				}
			})
		}
	}
}

func TestResolveTimeRejectsStrictInputs(t *testing.T) {
	validQuery := "local=2026-06-15T12:00:00&time_zone=UTC"
	cases := []struct {
		name             string
		rawQuery         string
		forceQuery       bool
		body             string
		contentLength    int64
		transferEncoding []string
		transferHeader   string
	}{
		{name: "missing query"},
		{name: "unknown parameter", rawQuery: validQuery + "&extra=1"},
		{name: "repeated local", rawQuery: "local=2026-06-15T12:00:00&local=2026-06-15T12:00:00&time_zone=UTC"},
		{name: "repeated zone", rawQuery: validQuery + "&time_zone=UTC"},
		{name: "invalid percent escape", rawQuery: "local=%zz&time_zone=UTC"},
		{name: "semicolon separator", rawQuery: "local=2026-06-15T12:00:00;time_zone=UTC"},
		{name: "leading ampersand", rawQuery: "&" + validQuery},
		{name: "trailing ampersand", rawQuery: validQuery + "&"},
		{name: "empty query segment", rawQuery: "local=2026-06-15T12:00:00&&time_zone=UTC"},
		{name: "force query", forceQuery: true, rawQuery: validQuery},
		{name: "short local", rawQuery: "local=2026-6-15T12:00:00&time_zone=UTC"},
		{name: "lowercase separator", rawQuery: "local=2026-06-15t12:00:00&time_zone=UTC"},
		{name: "local suffix", rawQuery: "local=2026-06-15T12:00:00.000&time_zone=UTC"},
		{name: "impossible date", rawQuery: "local=2026-13-40T12:00:00&time_zone=UTC"},
		{name: "pre Unix", rawQuery: "local=1969-12-31T23:59:59&time_zone=UTC"},
		{name: "year zero", rawQuery: "local=0000-01-01T00:00:00&time_zone=UTC"},
		{name: "empty zone", rawQuery: "local=2026-06-15T12:00:00&time_zone="},
		{name: "unknown zone", rawQuery: "local=2026-06-15T12:00:00&time_zone=Not/A/Zone"},
		{name: "Local zone", rawQuery: "local=2026-06-15T12:00:00&time_zone=Local"},
		{name: "arbitrary path zone", rawQuery: "local=2026-06-15T12:00:00&time_zone=/etc/passwd"},
		{name: "parent path zone", rawQuery: "local=2026-06-15T12:00:00&time_zone=../UTC"},
		{name: "zone too long", rawQuery: "local=2026-06-15T12:00:00&time_zone=" + strings.Repeat("A", maxZoneBytes+1)},
		{name: "body", rawQuery: validQuery, body: "unexpected body"},
		{name: "declared content length", rawQuery: validQuery, contentLength: 1},
		{name: "transfer encoding", rawQuery: validQuery, transferEncoding: []string{"chunked"}},
		{name: "transfer encoding header", rawQuery: validQuery, transferHeader: "chunked"},
	}
	for _, endpoint := range append(userTimeEndpoints()[1:], adminTimeEndpoints()[1:]...) {
		for _, tc := range cases {
			t.Run(endpoint.name+"/"+tc.name, func(t *testing.T) {
				var body io.Reader
				if tc.body != "" {
					body = strings.NewReader(tc.body)
				}
				request := httptest.NewRequest(http.MethodGet, endpoint.path, body)
				request.URL.RawQuery = tc.rawQuery
				request.URL.ForceQuery = tc.forceQuery
				if tc.contentLength != 0 {
					request.ContentLength = tc.contentLength
				}
				request.TransferEncoding = tc.transferEncoding
				if tc.transferHeader != "" {
					request.Header.Set("Transfer-Encoding", tc.transferHeader)
				}
				assertInvalidTimeResponse(t, invokeTimeEndpoint(t, endpoint, request))
			})
		}
	}
}

type capturedUserTimeRoute struct {
	method  string
	pattern string
	handler resources.AuthorizedUserHandler
}

type capturedAdminTimeRoute struct {
	method  string
	pattern string
	handler resources.AuthorizedAdminHandler
}

type timeUserRegistrar struct {
	routes  []capturedUserTimeRoute
	calls   int
	failAt  int
	failErr error
}

func (registrar *timeUserRegistrar) RegisterUserRoute(method, pattern string, handler resources.AuthorizedUserHandler) error {
	registrar.calls++
	if registrar.failAt == registrar.calls {
		if registrar.failErr != nil {
			return registrar.failErr
		}
		return errors.New("user route registration failed")
	}
	registrar.routes = append(registrar.routes, capturedUserTimeRoute{method: method, pattern: pattern, handler: handler})
	return nil
}

type timeAdminRegistrar struct {
	routes  []capturedAdminTimeRoute
	calls   int
	failAt  int
	failErr error
}

func (registrar *timeAdminRegistrar) RegisterAdminRoute(method, pattern string, handler resources.AuthorizedAdminHandler) error {
	registrar.calls++
	if registrar.failAt == registrar.calls {
		if registrar.failErr != nil {
			return registrar.failErr
		}
		return errors.New("admin route registration failed")
	}
	registrar.routes = append(registrar.routes, capturedAdminTimeRoute{method: method, pattern: pattern, handler: handler})
	return nil
}

func TestRegisterRoutesRegistersExactGETRoutes(t *testing.T) {
	users := &timeUserRegistrar{}
	admins := &timeAdminRegistrar{}
	if err := RegisterRoutes(users, admins); err != nil {
		t.Fatal(err)
	}
	wantUsers := []string{
		http.MethodGet + " " + routeTimeZones,
		http.MethodGet + " " + routeTimeResolve,
	}
	if len(users.routes) != len(wantUsers) || users.calls != len(wantUsers) {
		t.Fatalf("user routes=%+v calls=%d, want %v", users.routes, users.calls, wantUsers)
	}
	for index, route := range users.routes {
		if route.method+" "+route.pattern != wantUsers[index] || route.handler == nil {
			t.Fatalf("user route[%d]=%+v, want %q with handler", index, route, wantUsers[index])
		}
	}
	wantAdmins := []string{
		http.MethodGet + " " + routeAdminTimeZones,
		http.MethodGet + " " + routeAdminTimeResolve,
	}
	if len(admins.routes) != len(wantAdmins) || admins.calls != len(wantAdmins) {
		t.Fatalf("admin routes=%+v calls=%d, want %v", admins.routes, admins.calls, wantAdmins)
	}
	for index, route := range admins.routes {
		if route.method+" "+route.pattern != wantAdmins[index] || route.handler == nil {
			t.Fatalf("admin route[%d]=%+v, want %q with handler", index, route, wantAdmins[index])
		}
	}
}

func TestRegisterRoutesReturnsRegistrarFailure(t *testing.T) {
	cases := []struct {
		name           string
		userFailureAt  int
		adminFailureAt int
		wantUserCalls  int
		wantAdminCalls int
	}{
		{name: "first user route", userFailureAt: 1, wantUserCalls: 1},
		{name: "second user route", userFailureAt: 2, wantUserCalls: 2},
		{name: "first admin route", adminFailureAt: 1, wantUserCalls: 2, wantAdminCalls: 1},
		{name: "second admin route", adminFailureAt: 2, wantUserCalls: 2, wantAdminCalls: 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			failure := errors.New(tc.name)
			users := &timeUserRegistrar{failAt: tc.userFailureAt, failErr: failure}
			admins := &timeAdminRegistrar{failAt: tc.adminFailureAt, failErr: failure}
			err := RegisterRoutes(users, admins)
			if !errors.Is(err, failure) {
				t.Fatalf("error=%v, want %v", err, failure)
			}
			if users.calls != tc.wantUserCalls || admins.calls != tc.wantAdminCalls {
				t.Fatalf("calls user=%d admin=%d, want user=%d admin=%d", users.calls, admins.calls, tc.wantUserCalls, tc.wantAdminCalls)
			}
		})
	}
}

func TestRegisterRoutesRejectsNilRegistrars(t *testing.T) {
	var users *timeUserRegistrar
	if err := RegisterRoutes(users, &timeAdminRegistrar{}); err == nil {
		t.Fatal("typed-nil user registrar was accepted")
	}
	var admins *timeAdminRegistrar
	if err := RegisterRoutes(&timeUserRegistrar{}, admins); err == nil {
		t.Fatal("typed-nil admin registrar was accepted")
	}
}
