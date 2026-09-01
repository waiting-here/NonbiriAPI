package forward

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/charityrouting"
	"github.com/waiting-here/NonbiriAPI/internal/debug"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
	"github.com/waiting-here/NonbiriAPI/internal/lifecyclegate"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
	"github.com/waiting-here/NonbiriAPI/internal/routing"
)

type fakeCallerKeyResolver struct {
	identity resources.CallerIdentity
	err      error
	calls    int
}

type sequenceCallerKeyResolver struct {
	identities []resources.CallerIdentity
	errors     []error
	calls      int
}

func (resolver *sequenceCallerKeyResolver) ResolveCallerKey(context.Context, string) (resources.CallerIdentity, error) {
	index := resolver.calls
	resolver.calls++
	var identity resources.CallerIdentity
	var err error
	if index < len(resolver.identities) {
		identity = resolver.identities[index]
	}
	if index < len(resolver.errors) {
		err = resolver.errors[index]
	}
	return identity, err
}

func (resolver *fakeCallerKeyResolver) ResolveCallerKey(context.Context, string) (resources.CallerIdentity, error) {
	resolver.calls++
	return resolver.identity, resolver.err
}

func TestCallerKeyMiddlewareExactBearerAndLifecycleRecheck(t *testing.T) {
	lifecycle, err := lifecyclegate.New(lifecyclegate.Config{MaxUsers: 4})
	if err != nil {
		t.Fatalf("lifecyclegate.New: %v", err)
	}
	resolver := &fakeCallerKeyResolver{identity: resources.CallerIdentity{UserID: 9, Generation: 3}}
	middleware, err := NewCallerKeyMiddleware(resolver, lifecycle)
	if err != nil {
		t.Fatalf("NewCallerKeyMiddleware: %v", err)
	}
	next := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		userID, identityErr := CallerIdentity(request)
		if identityErr != nil || userID != 9 {
			t.Fatalf("CallerIdentity=(%d,%v)", userID, identityErr)
		}
		writer.WriteHeader(http.StatusNoContent)
	})
	request := httptest.NewRequest(http.MethodGet, "https://gateway.example/v1/models", nil)
	request.Header.Set("Authorization", "Bearer nbk_test")
	recorder := httptest.NewRecorder()

	middleware.Wrap(next).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusNoContent || resolver.calls != 2 {
		t.Fatalf("response=%d resolver calls=%d body=%q", recorder.Code, resolver.calls, recorder.Body.String())
	}
	if recorder.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control=%q", recorder.Header().Get("Cache-Control"))
	}
}

func TestCallerKeyMiddlewareRejectsBeforeDownstream(t *testing.T) {
	lifecycle, _ := lifecyclegate.New(lifecyclegate.Config{MaxUsers: 4})
	tests := []struct {
		name       string
		method     string
		path       string
		headers    []string
		wantStatus int
		wantCalls  int
	}{
		{name: "missing", method: http.MethodGet, path: "/v1/models", wantStatus: http.StatusUnauthorized},
		{name: "duplicate", method: http.MethodGet, path: "/v1/models", headers: []string{"Bearer a", "Bearer b"}, wantStatus: http.StatusUnauthorized},
		{name: "wrong scheme", method: http.MethodGet, path: "/v1/models", headers: []string{"Basic a"}, wantStatus: http.StatusUnauthorized},
		{name: "wrong method", method: http.MethodPost, path: "/v1/models", headers: []string{"Bearer a"}, wantStatus: http.StatusMethodNotAllowed},
		{name: "trailing slash", method: http.MethodGet, path: "/v1/models/", headers: []string{"Bearer a"}, wantStatus: http.StatusNotFound},
		{name: "encoded path", method: http.MethodGet, path: "/v1/%6dodels", headers: []string{"Bearer a"}, wantStatus: http.StatusNotFound},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			resolver := &fakeCallerKeyResolver{identity: resources.CallerIdentity{UserID: 1}}
			middleware, _ := NewCallerKeyMiddleware(resolver, lifecycle)
			called := false
			next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true })
			request := httptest.NewRequest(test.method, "https://gateway.example"+test.path, nil)
			for _, header := range test.headers {
				request.Header.Add("Authorization", header)
			}
			recorder := httptest.NewRecorder()
			middleware.Wrap(next).ServeHTTP(recorder, request)
			if recorder.Code != test.wantStatus || called || resolver.calls != test.wantCalls {
				t.Fatalf("status=%d called=%v resolver=%d body=%q", recorder.Code, called, resolver.calls, recorder.Body.String())
			}
			if recorder.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("Cache-Control=%q", recorder.Header().Get("Cache-Control"))
			}
		})
	}
}

func TestCallerKeyLifecycleFinalRecheckRejectsRevocationAndGenerationChange(t *testing.T) {
	tests := []struct {
		name       string
		identities []resources.CallerIdentity
		errors     []error
		wantHTTP   int
	}{
		{
			name: "generation changed",
			identities: []resources.CallerIdentity{
				{UserID: 7, Generation: 3}, {UserID: 7, Generation: 4},
			},
			wantHTTP: http.StatusUnauthorized,
		},
		{
			name:       "key revoked",
			identities: []resources.CallerIdentity{{UserID: 7, Generation: 3}, {}},
			errors:     []error{nil, resources.ErrNotFound},
			wantHTTP:   http.StatusUnauthorized,
		},
		{
			name:       "recheck unavailable",
			identities: []resources.CallerIdentity{{UserID: 7, Generation: 3}, {}},
			errors:     []error{nil, errors.New("database unavailable")},
			wantHTTP:   http.StatusServiceUnavailable,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			lifecycle, err := lifecyclegate.New(lifecyclegate.Config{MaxUsers: 4})
			if err != nil {
				t.Fatal(err)
			}
			resolver := &sequenceCallerKeyResolver{identities: test.identities, errors: test.errors}
			middleware, err := NewCallerKeyMiddleware(resolver, lifecycle)
			if err != nil {
				t.Fatal(err)
			}
			called := false
			request := httptest.NewRequest(http.MethodGet, "https://gateway.example/v1/models", nil)
			request.Header.Set("Authorization", "Bearer nbk_test")
			recorder := httptest.NewRecorder()

			middleware.Wrap(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true })).ServeHTTP(recorder, request)

			if recorder.Code != test.wantHTTP || called || resolver.calls != 2 {
				t.Fatalf("response=%d called=%v resolver=%d body=%q", recorder.Code, called, resolver.calls, recorder.Body.String())
			}
		})
	}
}

func TestHandlerIngressMatrix(t *testing.T) {
	fixture := newServiceFixture(t, &fakeDebugCapture{decision: debug.CaptureDecision{Active: true, Mode: debug.ModeDry, Language: "en"}})
	handler := NewHandler(fixture.service)
	tests := []struct {
		name       string
		method     string
		path       string
		body       io.Reader
		content    []string
		encoding   []string
		wantStatus int
		wantCode   string
	}{
		{name: "valid no content type", method: http.MethodPost, path: "/v1/chat/completions", body: strings.NewReader(`{"model":"provider/model","messages":[]}`), wantStatus: http.StatusUnprocessableEntity, wantCode: httperr.CodeDebugDryRunIntercepted},
		{name: "valid utf8", method: http.MethodPost, path: "/v1/chat/completions", body: strings.NewReader(`{"model":"provider/model","messages":[]}`), content: []string{"application/json; charset=utf-8"}, wantStatus: http.StatusUnprocessableEntity, wantCode: httperr.CodeDebugDryRunIntercepted},
		{name: "query", method: http.MethodPost, path: "/v1/chat/completions?x=1", body: strings.NewReader(`{"model":"provider/model"}`), wantStatus: http.StatusBadRequest, wantCode: httperr.CodeInvalidRequest},
		{name: "content type", method: http.MethodPost, path: "/v1/chat/completions", body: strings.NewReader(`{}`), content: []string{"text/plain"}, wantStatus: http.StatusBadRequest, wantCode: httperr.CodeInvalidRequest},
		{name: "duplicate content type", method: http.MethodPost, path: "/v1/chat/completions", body: strings.NewReader(`{}`), content: []string{"application/json", "application/json"}, wantStatus: http.StatusBadRequest, wantCode: httperr.CodeInvalidRequest},
		{name: "encoding", method: http.MethodPost, path: "/v1/chat/completions", body: strings.NewReader(`{}`), encoding: []string{"gzip"}, wantStatus: http.StatusBadRequest, wantCode: httperr.CodeInvalidRequest},
		{name: "duplicate json", method: http.MethodPost, path: "/v1/chat/completions", body: strings.NewReader(`{"model":"a/b","model":"a/b"}`), wantStatus: http.StatusBadRequest, wantCode: httperr.CodeInvalidRequest},
		{name: "trailing json", method: http.MethodPost, path: "/v1/chat/completions", body: strings.NewReader(`{"model":"a/b"}{}`), wantStatus: http.StatusBadRequest, wantCode: httperr.CodeInvalidRequest},
		{name: "wrong method", method: http.MethodGet, path: "/v1/chat/completions", wantStatus: http.StatusMethodNotAllowed, wantCode: httperr.CodeMethodNotAllowed},
		{name: "unknown path", method: http.MethodGet, path: "/v1/unknown", wantStatus: http.StatusNotFound, wantCode: httperr.CodeNotFound},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(test.method, "https://gateway.example"+test.path, test.body)
			for _, value := range test.content {
				request.Header.Add("Content-Type", value)
			}
			for _, value := range test.encoding {
				request.Header.Add("Content-Encoding", value)
			}
			request = withCallerIdentity(request, resources.CallerIdentity{UserID: 1})
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request)
			if recorder.Code != test.wantStatus || !strings.Contains(recorder.Body.String(), test.wantCode) {
				t.Fatalf("response=%d %q", recorder.Code, recorder.Body.String())
			}
			if recorder.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("Cache-Control=%q", recorder.Header().Get("Cache-Control"))
			}
		})
	}
}

func TestHandlerBodyAndModelRuneBoundaries(t *testing.T) {
	fixture := newServiceFixture(t, &fakeDebugCapture{decision: debug.CaptureDecision{Active: true, Mode: debug.ModeDry, Language: "en"}})
	handler := NewHandler(fixture.service)
	oversized := bytes.Repeat([]byte{'x'}, int(1<<20)+1)
	request := httptest.NewRequest(http.MethodPost, "https://gateway.example/v1/chat/completions", bytes.NewReader(oversized))
	request = withCallerIdentity(request, resources.CallerIdentity{UserID: 1})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusRequestEntityTooLarge || !strings.Contains(recorder.Body.String(), httperr.CodePayloadTooLarge) {
		t.Fatalf("oversized response=%d %q", recorder.Code, recorder.Body.String())
	}

	validModel := strings.Repeat("界", 133)
	invalidModel := validModel + "界"
	for _, test := range []struct {
		name, model string
		want        int
	}{
		{name: "133 runes decoded", model: validModel, want: http.StatusUnprocessableEntity},
		{name: "134 runes rejected", model: invalidModel, want: http.StatusBadRequest},
	} {
		t.Run(test.name, func(t *testing.T) {
			body := `{"model":` + string(mustJSON(t, test.model)) + `,"messages":[]}`
			req := httptest.NewRequest(http.MethodPost, "https://gateway.example/v1/chat/completions", strings.NewReader(body))
			req = withCallerIdentity(req, resources.CallerIdentity{UserID: 1})
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != test.want {
				t.Fatalf("response=%d %q", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestModelsWireStableSortedAndNoBodyOrQuery(t *testing.T) {
	fixture := newServiceFixture(t, nil)
	fixture.personal.models = []ListedModel{
		{ModelID: 2, Provider: "zeta", FullName: "zeta/model", CreatedAt: 20},
		{ModelID: 1, Provider: "alpha", FullName: "alpha/model", CreatedAt: 10},
	}
	fixture.charity.models = []ListedModel{{ModelID: 3, Provider: "care", FullName: "[公益]care/model", CreatedAt: 30}}
	handler := NewHandler(fixture.service)
	request := httptest.NewRequest(http.MethodGet, "https://gateway.example/v1/models", nil)
	request = withCallerIdentity(request, resources.CallerIdentity{UserID: 1})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("models response=%d %q", recorder.Code, recorder.Body.String())
	}
	want := `{"object":"list","data":[{"id":"[公益]care/model","object":"model","created":30,"owned_by":"care"},{"id":"alpha/model","object":"model","created":10,"owned_by":"alpha"},{"id":"zeta/model","object":"model","created":20,"owned_by":"zeta"}]}`
	if recorder.Body.String() != want {
		t.Fatalf("models=%q, want %q", recorder.Body.String(), want)
	}
	for _, url := range []string{"https://gateway.example/v1/models?x=1", "https://gateway.example/v1/models"} {
		var body io.Reader
		if !strings.Contains(url, "?") {
			body = strings.NewReader("x")
		}
		req := httptest.NewRequest(http.MethodGet, url, body)
		req = withCallerIdentity(req, resources.CallerIdentity{UserID: 1})
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), httperr.CodeInvalidRequest) {
			t.Fatalf("invalid models request=%d %q", rec.Code, rec.Body.String())
		}
	}
}

func TestModelsPreservesResourceLimitAndMasksInternalErrors(t *testing.T) {
	tests := []struct {
		name      string
		configure func(*serviceFixture)
		wantCode  string
		wantHTTP  int
	}{
		{
			name: "personal resource limit",
			configure: func(fixture *serviceFixture) {
				fixture.personal.listErr = routing.ErrResourceLimit
			},
			wantCode: httperr.CodeResourceLimitExceeded,
			wantHTTP: http.StatusUnprocessableEntity,
		},
		{
			name: "charity resource limit",
			configure: func(fixture *serviceFixture) {
				fixture.charity.listErr = charityrouting.ErrResourceLimit
			},
			wantCode: httperr.CodeResourceLimitExceeded,
			wantHTTP: http.StatusUnprocessableEntity,
		},
		{
			name: "internal repository error",
			configure: func(fixture *serviceFixture) {
				fixture.personal.listErr = errors.New("database unavailable")
			},
			wantCode: httperr.CodeInternal,
			wantHTTP: http.StatusInternalServerError,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newServiceFixture(t, nil)
			test.configure(fixture)
			request := httptest.NewRequest(http.MethodGet, "https://gateway.example/v1/models", nil)
			request = withCallerIdentity(request, resources.CallerIdentity{UserID: 1})
			recorder := httptest.NewRecorder()

			NewHandler(fixture.service).ServeHTTP(recorder, request)

			if recorder.Code != test.wantHTTP || !strings.Contains(recorder.Body.String(), test.wantCode) {
				t.Fatalf("response=%d %q", recorder.Code, recorder.Body.String())
			}
		})
	}
}

func withCallerIdentity(request *http.Request, identity resources.CallerIdentity) *http.Request {
	return request.WithContext(context.WithValue(request.Context(), callerIdentityContextKey{}, identity))
}

func mustJSON(t *testing.T, value string) []byte {
	t.Helper()
	encoded := bytes.NewBuffer(nil)
	if err := json.NewEncoder(encoded).Encode(value); err != nil {
		t.Fatalf("encode JSON: %v", err)
	}
	return bytes.TrimSpace(encoded.Bytes())
}
