package web

import (
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// Site identifies one of the two embedded SPAs.
type Site string

const (
	SiteAdmin Site = "admin"
	SiteUser  Site = "user"
)

// The embedded filesystems for each site are resolved once at init. Which
// implementation backs them (stub placeholders vs. real dist output) is chosen
// by the build tag in embed_stub.go / embed_dist.go.
var (
	adminFS = embeddedAdmin()
	userFS  = embeddedUser()
)

func siteFS(site Site) fs.FS {
	if site == SiteAdmin {
		return adminFS
	}
	return userFS
}

// PickSite is a PLACEHOLDER host-to-site router. It selects which embedded SPA
// to serve based on the request Host header. It is NOT a security control:
// trusted-proxy forwarding, distinct-host enforcement, and client-IP/CIDR
// validation are implemented by Phase 1 track E. Until then this naive prefix
// mapping exists only so the binary serves the right placeholder bundle, and
// callers must not treat it as protective routing.
func PickSite(host string) Site {
	h := host
	if i := strings.IndexByte(h, ':'); i >= 0 {
		h = h[:i] // strip a trailing :port
	}
	h = strings.ToLower(strings.TrimSuffix(h, "."))
	if strings.HasPrefix(h, "admin.") {
		return SiteAdmin
	}
	return SiteUser
}

// SPAHandler serves a single site's embedded SPA. Unknown paths fall back to
// the site index.html so client-side routing works. The fallback response and
// any direct index.html request are sent with Cache-Control: no-store so a
// stale SPA bundle from a previous binary never shadows a redeploy; hashed
// asset files keep the file server's default caching.
func SPAHandler(site Site) http.Handler {
	fsys := siteFS(site)
	fileServer := http.FileServer(http.FS(fsys))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		clean := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		if clean == "" || clean == "." {
			clean = "index.html"
		}
		if _, err := fs.Stat(fsys, clean); err != nil {
			serveIndexNoStore(w, fsys)
			return
		}
		if clean == "index.html" {
			w.Header().Set("Cache-Control", "no-store")
		}
		fileServer.ServeHTTP(w, r)
	})
}

// serveIndexNoStore writes the index.html with no-store headers. Used for both
// the SPA fallback and (conceptually) the root entry.
func serveIndexNoStore(w http.ResponseWriter, fsys fs.FS) {
	b, err := fs.ReadFile(fsys, "index.html")
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(b)
}

// NewMultiHandler routes each request to the SPA for its Host's site. It
// precomputes the per-site handlers so per-request work is a dispatch only.
func NewMultiHandler() http.Handler {
	admin := SPAHandler(SiteAdmin)
	user := SPAHandler(SiteUser)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if PickSite(r.Host) == SiteAdmin {
			admin.ServeHTTP(w, r)
			return
		}
		user.ServeHTTP(w, r)
	})
}