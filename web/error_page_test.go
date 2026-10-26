package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/waiting-here/NonbiriAPI/internal/httperr"
)

func TestBrowserSitePlainErrors(t *testing.T) {
	for _, tc := range []struct {
		name, path string
		fs         fstest.MapFS
		status     int
	}{
		{"missing index", "/some-page", fstest.MapFS{}, 404},
		{"restricted artwork", "/assets/gacha/unknown.svg", fstest.MapFS{"index.html": {Data: []byte("index")}}, 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, document := range []bool{true, false} {
				req := httptest.NewRequest(http.MethodGet, tc.path, nil)
				if document {
					req.Header.Set("Accept", "text/html")
				}
				rec := httptest.NewRecorder()
				spaHandler(tc.fs).ServeHTTP(httperr.WithBrowserErrors(rec, req, httperr.BrowserPageOptions{Styles: ErrorPageStyles()}), req)
				if rec.Code != tc.status {
					t.Fatalf("status=%d", rec.Code)
				}
				if document {
					for _, expected := range []string{"<!doctype html>", "--nb-color-bg:", "[data-theme='dark']", ".nb-btn--primary", `href="/"`} {
						if !strings.Contains(rec.Body.String(), expected) {
							t.Fatalf("page lacks %q", expected)
						}
					}
				} else if rec.Header().Get("Content-Type") != "text/plain; charset=utf-8" {
					t.Fatalf("non-document content type changed: %q", rec.Header().Get("Content-Type"))
				}
			}
		})
	}
}
