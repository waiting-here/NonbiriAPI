package app

import (
	"errors"
	"net/http"

	"github.com/waiting-here/NonbiriAPI/internal/adminapi"
	"github.com/waiting-here/NonbiriAPI/internal/auth"
	"github.com/waiting-here/NonbiriAPI/internal/config"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
	"github.com/waiting-here/NonbiriAPI/internal/httpmw"
	"github.com/waiting-here/NonbiriAPI/internal/stewardautomation"
	"github.com/waiting-here/NonbiriAPI/web"
)

func healthz(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		httperr.WriteError(w, httperr.New(httperr.CodeMethodNotAllowed, "method not allowed"))
		return
	}
	if requestHasQuery(r) || requestCarriesBody(r) {
		httperr.WriteError(w, httperr.New(httperr.CodeInvalidRequest, "invalid request"))
		return
	}
	httperr.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func requestCarriesBody(r *http.Request) bool {
	return r != nil && (r.ContentLength != 0 || len(r.TransferEncoding) != 0)
}

func requestHasQuery(r *http.Request) bool {
	return r == nil || r.URL == nil || r.URL.ForceQuery || r.URL.RawQuery != ""
}

func apiNotFound(w http.ResponseWriter, _ *http.Request) {
	httperr.WriteError(w, httperr.New(httperr.CodeNotFound, "not found"))
}

// servePublicConfig is the anonymous Generation 2 bootstrap projection. All
// other production APIs are mounted by their authenticated domain owners.
func servePublicConfig(store *db.Store, w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		httperr.WriteError(w, httperr.New(httperr.CodeMethodNotAllowed, "method not allowed"))
		return
	}
	if requestHasQuery(r) || requestCarriesBody(r) {
		httperr.WriteError(w, httperr.New(httperr.CodeInvalidRequest, "invalid request"))
		return
	}
	out, err := adminapi.ReadPublicConfig(store)
	if err != nil {
		httperr.WriteError(w, httperr.New(httperr.CodeInternal, "service unavailable"))
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httperr.WriteJSON(w, http.StatusOK, out)
}

func freshSafeMux(cfg *config.Config, store *db.Store) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", healthz)
	mux.HandleFunc("/readyz", (*readinessState)(nil).serveHTTP)
	if store != nil {
		mux.Handle("/admin/api/branding", httpmw.API(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { servePublicBranding(store, w, r) })))
	}

	userAPI := httpmw.API(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if store != nil && r.URL.Path == "/api/config" {
			servePublicConfig(store, w, r)
			return
		}
		apiNotFound(w, r)
	}))
	adminAPI := httpmw.API(http.HandlerFunc(apiNotFound))
	mux.Handle("/api", userAPI)
	mux.Handle("/api/", userAPI)
	mux.Handle("/v1", userAPI)
	mux.Handle("/v1/", userAPI)
	mux.Handle("/admin/api", adminAPI)
	mux.Handle("/admin/api/", adminAPI)
	mux.Handle("/", web.NewMultiHandler(cfg.UserHost, cfg.AdminHost))
	return mux
}

func generationTwoMux(cfg *config.Config, store *db.Store, authRuntime *auth.Runtime, callerHandler http.Handler, automationHandlers ...http.Handler) (*http.ServeMux, error) {
	if cfg == nil || store == nil || authRuntime == nil || callerHandler == nil {
		return nil, errors.New("Generation 2 HTTP dependencies are required")
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", healthz)

	publicConfig := httpmw.API(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		servePublicConfig(store, w, r)
	}))
	mux.Handle("/api/config", publicConfig)
	mux.Handle("/admin/api/branding", httpmw.API(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { servePublicBranding(store, w, r) })))

	userAuth := authRuntime.UserHandler()
	mux.Handle("/api", userAuth)
	mux.Handle("/api/", userAuth)
	if len(automationHandlers) > 1 || len(automationHandlers) == 1 && automationHandlers[0] == nil {
		return nil, errors.New("invalid automation handler")
	}
	if len(automationHandlers) == 1 {
		automation := httpmw.API(automationHandlers[0])
		mux.Handle(stewardautomation.PersonalPrefix, automation)
		mux.Handle(stewardautomation.StewardPrefix, automation)
		mux.Handle(stewardautomation.DonationsPath, automation)
		mux.Handle(stewardautomation.BindingsPath, automation)
		mux.Handle(stewardautomation.FailurePolicyPath, automation)
	}

	callerAPI := httpmw.API(callerHandler)
	mux.Handle("/v1", callerAPI)
	mux.Handle("/v1/", callerAPI)

	// Administrator routes deliberately bypass the maintenance admission gate.
	// The authentication runtime still enforces the admin host, password,
	// credential generation, live session and final-transaction authorization.
	adminAuth := authRuntime.AdminHandler()
	mux.Handle("/admin/api", adminAuth)
	mux.Handle("/admin/api/", adminAuth)

	mux.Handle("/", web.NewMultiHandler(cfg.UserHost, cfg.AdminHost))
	return mux, nil
}

func stationBoundary(cfg *config.Config, next http.Handler) (http.Handler, error) {
	if cfg == nil || next == nil {
		return nil, errors.New("HTTP boundary dependencies are required")
	}
	return httpmw.New(httpmw.Config{
		UserHost:          cfg.UserHost,
		AdminHost:         cfg.AdminHost,
		SiteBaseURL:       cfg.SiteBaseURL,
		TrustedProxyCIDRs: cfg.TrustedProxyCIDRs,
	}, next)
}

// newHTTPHandler remains the boundary-only constructor used by tests and
// embedded-shell callers. Without a validated store it deliberately exposes
// only liveness and unavailable readiness probes.
func newHTTPHandler(cfg *config.Config) (http.Handler, error) {
	if cfg == nil {
		return nil, errors.New("configuration is required")
	}
	return stationBoundary(cfg, freshSafeMux(cfg, nil))
}
