package web

import (
	"io/fs"
	"net/http"
	"path"
	"strings"

	"github.com/waiting-here/NonbiriAPI/internal/host"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
)

// Site identifies one of the two embedded SPAs.
type Site string

const (
	SiteUnknown Site = ""
	SiteAdmin   Site = "admin"
	SiteUser    Site = "user"
)

// The embedded filesystems for each site are resolved once at init. Which
// implementation backs them (stub placeholders vs. real dist output) is chosen
// by the build tag in embed_stub.go / embed_dist.go.
var (
	adminFS = embeddedAdmin()
	userFS  = embeddedUser()
)

func siteFS(site Site) fs.FS {
	switch site {
	case SiteAdmin:
		return adminFS
	case SiteUser:
		return userFS
	default:
		return nil
	}
}

// PickSite maps a host only when the configured user and admin hosts are
// supplied. An unknown, malformed, or unconfigured host always returns
// SiteUnknown; there is no implicit user-site fallback.
func PickSite(raw string, configured ...string) Site {
	if len(configured) != 2 {
		return SiteUnknown
	}
	matcher, err := host.NewMatcher(configured[0], configured[1])
	if err != nil {
		return SiteUnknown
	}
	switch matcher.Match(raw) {
	case host.StationAdmin:
		return SiteAdmin
	case host.StationUser:
		return SiteUser
	default:
		return SiteUnknown
	}
}

// SPAHandler serves a single site's embedded SPA. Unknown paths fall back to
// the site index.html so client-side routing works. The fallback response and
// any direct index.html request are sent with Cache-Control: no-store so a
// stale SPA bundle from a previous binary never shadows a redeploy; hashed
// asset files keep the file server's default caching.
func SPAHandler(site Site) http.Handler {
	fsys := siteFS(site)
	if fsys == nil {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			httperr.WriteError(w, httperr.New(httperr.CodeInternal, "site is not configured"))
		})
	}
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

// NewMultiHandler routes only the configured station hosts to their own
// embedded SPA. When used behind httpmw, the validated station in the request
// context is authoritative; the host matcher is retained for direct tests and
// other safe compositions.
func NewMultiHandler(configured ...string) http.Handler {
	admin := SPAHandler(SiteAdmin)
	user := SPAHandler(SiteUser)

	var matcher host.Matcher
	matcherReady := len(configured) == 2
	if matcherReady {
		var err error
		matcher, err = host.NewMatcher(configured[0], configured[1])
		matcherReady = err == nil
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		station, inContext := host.StationFromContext(r.Context())
		if !inContext && matcherReady {
			switch matcher.Match(r.Host) {
			case host.StationAdmin:
				station = host.StationAdmin
			case host.StationUser:
				station = host.StationUser
			default:
				station = host.StationUnknown
			}
		}
		switch station {
		case host.StationAdmin:
			admin.ServeHTTP(w, r)
		case host.StationUser:
			user.ServeHTTP(w, r)
		default:
			httperr.WriteError(w, httperr.New(httperr.CodeInvalidRequest, "misdirected request"))
		}
	})
}
