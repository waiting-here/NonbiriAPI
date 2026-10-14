package web

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
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

func TestSPAHandlerCachesOnlyExistingContentHashedBundles(t *testing.T) {
	h := spaHandler(fstest.MapFS{
		"index.html":                {Data: []byte("<html>current build</html>")},
		"assets/index-Ab12_9-x.js":  {Data: []byte("export default 1")},
		"assets/index-Z1234567.css": {Data: []byte("body {color:red}")},
		"assets/fixed.js":           {Data: []byte("export default 2")},
		"assets/card-Ab123456.webp": {Data: []byte("image")},
		"build-manifest.json":       {Data: []byte("{}")},
	})
	for _, tc := range []struct{ path, cache string }{
		{"/assets/index-Ab12_9-x.js", "public, max-age=31536000, immutable"},
		{"/assets/index-Z1234567.css?v=1", "public, max-age=31536000, immutable"},
		{"/assets/fixed.js", ""},
		{"/assets/card-Ab123456.webp", ""},
		{"/build-manifest.json", ""},
		{"/assets/oldbuild-Ab123456.js", "no-store"},
		{"/games/gacha", "no-store"},
		{"/", "no-store"},
		{"/index.html", "no-store"},
	} {
		t.Run(tc.path, func(t *testing.T) {
			for _, method := range []string{http.MethodGet, http.MethodHead} {
				w := httptest.NewRecorder()
				h.ServeHTTP(w, httptest.NewRequest(method, tc.path, nil))
				if got := w.Header().Get("Cache-Control"); got != tc.cache {
					t.Fatalf("%s cache=%q want=%q", method, got, tc.cache)
				}
			}
		})
	}
}
