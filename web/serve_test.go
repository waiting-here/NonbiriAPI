package web

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestStubFSContainsIndex(t *testing.T) {
	for _, site := range []Site{SiteAdmin, SiteUser} {
		fsys := siteFS(site)
		if _, err := fs.Stat(fsys, "index.html"); err != nil {
			t.Errorf("%s: index.html missing in embedded FS: %v", site, err)
			continue
		}
		b, err := fs.ReadFile(fsys, "index.html")
		if err != nil {
			t.Errorf("%s: read index.html: %v", site, err)
			continue
		}
		if !strings.Contains(string(b), "NonbiriAPI") {
			t.Errorf("%s: index.html marker missing", site)
		}
	}
}

func TestPickSiteConfiguredBoundary(t *testing.T) {
	cases := map[string]Site{
		"admin.example.com":        SiteAdmin,
		"admin.example.com:8080":   SiteAdmin,
		"ADMIN.Example.com.":       SiteAdmin,
		"example.com":              SiteUser,
		"example.com:8080":         SiteUser,
		"EXAMPLE.COM.":             SiteUser,
		"localhost":                SiteUnknown,
		"unknown.example.com":      SiteUnknown,
		"admin.example.com:broken": SiteUnknown,
		"":                         SiteUnknown,
	}
	for raw, want := range cases {
		if got := PickSite(raw, "example.com", "admin.example.com"); got != want {
			t.Errorf("PickSite(%q) = %q, want %q", raw, got, want)
		}
	}
	if got := PickSite("example.com"); got != SiteUnknown {
		t.Fatalf("PickSite without configured hosts = %q, want unknown", got)
	}
}

func TestSPAHandlerIndexAndFallback(t *testing.T) {
	h := SPAHandler(SiteUser)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET / status = %d, want 200", rec.Code)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Fatalf("GET / cache-control = %q, want no-store", cc)
	}
	if !strings.Contains(rec.Body.String(), "NonbiriAPI") {
		t.Fatalf("GET / body missing marker: %q", rec.Body.String())
	}

	// Unknown client route falls back to index.html (SPA routing).
	fb := httptest.NewRecorder()
	h.ServeHTTP(fb, httptest.NewRequest(http.MethodGet, "/some/deep/route", nil))
	if fb.Code != http.StatusOK {
		t.Fatalf("fallback status = %d, want 200", fb.Code)
	}
	if cc := fb.Header().Get("Cache-Control"); cc != "no-store" {
		t.Fatalf("fallback cache-control = %q, want no-store", cc)
	}
	if !strings.Contains(fb.Body.String(), "NonbiriAPI") {
		t.Fatalf("fallback body missing marker: %q", fb.Body.String())
	}
}
