package forward

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/httpmw"
)

func TestBrowserCORSPreflightBoundary(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*http.Request)
		status int
		next   bool
		cors   bool
	}{
		{"embeddings", nil, 204, false, true},
		{"models", func(r *http.Request) { r.URL.Path = "/v1/models"; r.Header.Set("Access-Control-Request-Method", "GET") }, 204, false, true},
		{"chat", func(r *http.Request) { r.URL.Path = "/v1/chat/completions" }, 204, false, true},
		{"opaque origin", func(r *http.Request) { r.Header.Set("Origin", "null") }, 204, false, true},
		{"no requested headers", func(r *http.Request) { r.Header.Del("Access-Control-Request-Headers") }, 204, false, true},
		{"bare options", func(r *http.Request) { r.Header.Del("Access-Control-Request-Method") }, 405, true, true},
		{"missing origin", func(r *http.Request) { r.Header.Del("Origin") }, 400, false, true},
		{"duplicate origin", func(r *http.Request) { r.Header.Add("Origin", "https://other.example") }, 400, false, true},
		{"empty origin", func(r *http.Request) { r.Header.Set("Origin", "") }, 400, false, true},
		{"long origin", func(r *http.Request) { r.Header.Set("Origin", strings.Repeat("x", 2049)) }, 400, false, true},
		{"wrong method", func(r *http.Request) { r.Header.Set("Access-Control-Request-Method", "DELETE") }, 405, false, true},
		{"method case", func(r *http.Request) { r.Header.Set("Access-Control-Request-Method", "post") }, 405, false, true},
		{"duplicate method", func(r *http.Request) { r.Header.Add("Access-Control-Request-Method", "POST") }, 400, false, true},
		{"long method", func(r *http.Request) { r.Header.Set("Access-Control-Request-Method", strings.Repeat("x", 129)) }, 400, false, true},
		{"query", func(r *http.Request) { r.URL.RawQuery = "x=1" }, 400, false, true},
		{"empty query", func(r *http.Request) { r.URL.ForceQuery = true }, 400, false, true},
		{"unknown path", func(r *http.Request) { r.URL.Path = "/v1/responses" }, 404, true, false},
		{"trailing slash", func(r *http.Request) { r.URL.Path += "/" }, 404, true, false},
		{"escaped path", func(r *http.Request) { r.URL.RawPath = "/v1/%65mbeddings" }, 404, true, false},
		{"user session", func(r *http.Request) { r.URL.Path = "/api/caller-key/regenerate" }, 404, true, false},
		{"admin session", func(r *http.Request) { r.URL.Path = "/admin/api/users" }, 404, true, false},
		{"automation", func(r *http.Request) { r.URL.Path = "/api/steward/automation/donation-key-failure-policy" }, 404, true, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			called := false
			handler := BrowserCORS(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called = true
				failure := exactIngressFailure(r.Method, r.URL.Path, r.URL.EscapedPath())
				if failure == nil {
					t.Fatal("unexpected actual request")
				}
				writeFailure(w, *failure)
			}))
			r := httptest.NewRequest("OPTIONS", "https://api.example/v1/embeddings", nil)
			r.Header.Set("Origin", "https://browser.example")
			r.Header.Set("Access-Control-Request-Method", "POST")
			r.Header.Set("Access-Control-Request-Headers", "Authorization, Content-Type, X-Stainless-Lang")
			if test.change != nil {
				test.change(r)
			}
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, r)
			if rec.Code != test.status || called != test.next || (rec.Header().Get("Access-Control-Allow-Origin") == "*") != test.cors {
				t.Fatalf("status=%d downstream=%t headers=%v body=%s", rec.Code, called, rec.Header(), rec.Body.String())
			}
			if rec.Header().Get("Access-Control-Allow-Credentials") != "" || rec.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("unexpected credentials or cache policy: %v", rec.Header())
			}
			if rec.Code == 204 {
				if rec.Body.Len() != 0 || rec.Header().Get("Access-Control-Allow-Methods") != r.Header.Get("Access-Control-Request-Method") || rec.Header().Get("Access-Control-Max-Age") != "600" {
					t.Fatalf("preflight headers=%v body=%s", rec.Header(), rec.Body.String())
				}
				wantHeaders := strings.ToLower(r.Header.Get("Access-Control-Request-Headers"))
				if rec.Header().Get("Access-Control-Allow-Headers") != wantHeaders {
					t.Fatalf("allowed headers=%q want=%q", rec.Header().Get("Access-Control-Allow-Headers"), wantHeaders)
				}
			} else if rec.Header().Get("Access-Control-Allow-Methods") != "" || rec.Header().Get("Access-Control-Allow-Headers") != "" {
				t.Fatalf("invalid preflight was approved: %v", rec.Header())
			}
		})
	}
}

func TestBrowserCORSHeaderLimits(t *testing.T) {
	for _, test := range []struct {
		name   string
		values []string
		status int
	}{
		{"sdk headers", []string{"authorization, Content-Type, x-stainless-lang, x-stainless-runtime, x-stainless-package-version, x-custom"}, 204},
		{"count at bound", []string{strings.Repeat("x,", 63) + "y"}, 204},
		{"name at bound", []string{strings.Repeat("x", 128)}, 204},
		{"bytes at bound", []string{strings.Repeat(strings.Repeat("x", 127)+",", 31) + strings.Repeat("y", 128)}, 204},
		{"count over bound", []string{strings.Repeat("x,", 64) + "y"}, 400},
		{"name over bound", []string{strings.Repeat("x", 129)}, 400},
		{"bytes over bound", []string{strings.Repeat(strings.Repeat("x", 127)+",", 32) + "y"}, 400},
		{"empty", []string{""}, 400},
		{"duplicate", []string{"authorization", "content-type"}, 400},
		{"empty item", []string{"authorization,,content-type"}, 400},
		{"colon and value", []string{"authorization: secret"}, 400},
		{"control", []string{"x\x00header"}, 400},
		{"injection", []string{"x-header\r\nAccess-Control-Allow-Credentials: true"}, 400},
		{"non ascii", []string{"x-你好"}, 400},
	} {
		t.Run(test.name, func(t *testing.T) {
			r := httptest.NewRequest("OPTIONS", "https://api.example/v1/embeddings", nil)
			r.Header.Set("Origin", "https://browser.example")
			r.Header.Set("Access-Control-Request-Method", "POST")
			r.Header["Access-Control-Request-Headers"] = test.values
			r.Header.Set("Authorization", "Bearer must-not-be-reflected")
			rec := httptest.NewRecorder()
			BrowserCORS(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("preflight reached authentication") })).ServeHTTP(rec, r)
			if rec.Code != test.status {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
			for _, values := range rec.Header() {
				if strings.Contains(strings.Join(values, ","), "must-not-be-reflected") {
					t.Fatal("reflected credential")
				}
			}
		})
	}
}

func TestBrowserCORSActualResponsesAndStationBoundary(t *testing.T) {
	for _, status := range []int{200, 400, 401, 403, 404, 405, 429, 502, 503} {
		r := httptest.NewRequest("POST", "https://api.example/v1/embeddings", nil)
		r.Header.Set("Origin", "https://browser.example")
		rec := httptest.NewRecorder()
		BrowserCORS(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(status)
		})).ServeHTTP(rec, r)
		if rec.Code != status || rec.Header().Get("Access-Control-Allow-Origin") != "*" || rec.Header().Get("Access-Control-Expose-Headers") != "Retry-After" {
			t.Fatalf("status=%d headers=%v", rec.Code, rec.Header())
		}
	}
	handler, err := httpmw.New(httpmw.Config{UserHost: "api.example", AdminHost: "admin.example", SiteBaseURL: "https://api.example"}, BrowserCORS(http.NotFoundHandler()))
	if err != nil {
		t.Fatal(err)
	}
	for _, authority := range []string{"admin.example", "unknown.example"} {
		r := httptest.NewRequest("OPTIONS", "https://"+authority+"/v1/embeddings", nil)
		r.Header.Set("Origin", "https://browser.example")
		r.Header.Set("Access-Control-Request-Method", "POST")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, r)
		if rec.Code < 400 || rec.Header().Get("Access-Control-Allow-Origin") != "" {
			t.Fatalf("host boundary status=%d headers=%v", rec.Code, rec.Header())
		}
	}
}

func TestBrowserCORSStreamingCancellation(t *testing.T) {
	cancelled := make(chan struct{})
	server := httptest.NewServer(BrowserCORS(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: first\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		close(cancelled)
	})))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	r, err := http.NewRequestWithContext(ctx, "POST", server.URL+"/v1/chat/completions", nil)
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("Origin", "https://browser.example")
	response, err := server.Client().Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	first := make([]byte, len("data: first\n\n"))
	if _, err := io.ReadFull(response.Body, first); err != nil || string(first) != "data: first\n\n" || response.Header.Get("Access-Control-Allow-Origin") != "*" {
		t.Fatalf("first frame=%q headers=%v err=%v", first, response.Header, err)
	}
	cancel()
	select {
	case <-cancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("upstream context not cancelled")
	}
}
