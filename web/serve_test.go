package web

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/artwork"
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

func TestArtworkPolicyGuardsBothStationsBeforeStaticResponses(t *testing.T) {
	catalog, err := artwork.Parse([]byte(`{
 "version":1,"artworks":{
  "appearance:default":{"rating":"general"},"appearance:heart-moment":{"rating":"general"},
  "cover:permanent":{"rating":"general","name":"Safe","name_en":"Safe"},
  "card:restricted":{"rating":"r18"}
 },"assets":[
  {"owner":"card:restricted","files":["cards/restricted.webp","hires/cards/restricted.webp"],"prefixes":["motion/restricted/","hires/motion/restricted/"]},
  {"owner":"cover:permanent","files":["covers/permanent.webp"]}
 ]}`))
	if err != nil {
		t.Fatal(err)
	}
	modified := time.Unix(1700000000, 0).UTC()
	files := fstest.MapFS{"index.html": {Data: []byte("index")}, "assets/gacha/covers/permanent.webp": {Data: []byte("safe")}}
	paths := []string{"cards/restricted.webp", "hires/cards/restricted.webp", "motion/restricted/hero.png", "hires/motion/restricted/unused.webp"}
	for _, p := range paths {
		files["assets/gacha/"+p] = &fstest.MapFile{Data: []byte("restricted pixels"), ModTime: modified}
	}
	disabled := true
	calls := 0
	policy := func() bool { calls++; return disabled }
	handler := newMultiHandler(spaHandlerWithArtworkPolicy(files, catalog, policy), spaHandlerWithArtworkPolicy(files, catalog, policy), "user.example", "admin.example")
	request := func(method, host, target string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, "https://"+host+target, nil)
		req.Header.Set("If-Modified-Since", modified.Format(http.TimeFormat))
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		return w
	}
	for _, host := range []string{"user.example", "admin.example"} {
		for _, method := range []string{http.MethodGet, http.MethodHead} {
			for _, p := range append(slices.Clone(paths), "motion/restricted/missing.png") {
				for _, target := range []string{"/assets/gacha/" + p, "/assets/gacha/" + p + "?rev=2", "/assets/gacha/../gacha/" + p, "/assets/gacha//" + p, "/assets/gacha/" + strings.ReplaceAll(p, "/", "%2F")} {
					w := request(method, host, target)
					if w.Code != http.StatusForbidden || w.Header().Get("Cache-Control") != "no-store" || strings.Contains(w.Body.String(), "pixels") || w.Body.String() == "index" {
						t.Fatalf("%s %s: %d %s", method, target, w.Code, w.Body.String())
					}
				}
			}
		}
	}
	before := calls
	for _, extension := range []string{"webp", "png", "jpg", "jpeg", "gif", "avif", "svg", "bmp"} {
		asset := "assets/gacha/unknown." + extension
		files[asset] = &fstest.MapFile{Data: []byte("unclassified pixels")}
		w := request(http.MethodGet, "user.example", "/"+asset)
		if w.Code != http.StatusForbidden || w.Header().Get("Cache-Control") != "no-store" || strings.Contains(w.Body.String(), "pixels") {
			t.Fatal(asset, w.Code, w.Body.String())
		}
	}
	for _, asset := range []string{"composition.json", "NOTICE.md", "audio/theme.ogg"} {
		files["assets/gacha/"+asset] = &fstest.MapFile{Data: []byte("ordinary")}
		if w := request(http.MethodGet, "user.example", "/assets/gacha/"+asset); w.Code != http.StatusOK || w.Body.String() != "ordinary" {
			t.Fatal(asset, w.Code, w.Body.String())
		}
	}
	if w := request(http.MethodGet, "user.example", "/assets/gacha/covers/permanent.webp"); w.Code != http.StatusOK || w.Body.String() != "safe" {
		t.Fatal(w.Code, w.Body.String())
	}
	if calls != before {
		t.Fatal("ordinary asset consulted runtime policy")
	}
	disabled = false
	if w := request(http.MethodGet, "user.example", "/assets/gacha/unknown.svg"); w.Code != http.StatusForbidden || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("unclassified image allowed by global toggle", w.Code)
	}
	if w := request(http.MethodGet, "admin.example", "/assets/gacha/cards/restricted.webp"); w.Code != http.StatusNotModified || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal(w.Code, w.Header())
	}
	req := httptest.NewRequest(http.MethodGet, "https://user.example/assets/gacha/cards/restricted.webp", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK || w.Body.String() != "restricted pixels" || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal(w.Code, w.Body.String(), w.Header())
	}
	disabled = true
	if w := request(http.MethodGet, "user.example", "/assets/gacha/cards/restricted.webp"); w.Code != http.StatusForbidden {
		t.Fatal("toggle did not apply", w.Code)
	}
	nilHandler := spaHandlerWithArtworkPolicy(files, catalog, nil)
	w = httptest.NewRecorder()
	nilHandler.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatal("nil policy was unsafe", w.Code)
	}
}
