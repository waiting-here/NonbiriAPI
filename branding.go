package main

import (
	"net/http"

	"github.com/waiting-here/NonbiriAPI/internal/adminapi"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
)

// The anonymous admin shell needs only the same name and logo already public
// on the user station. Protected bootstrap and administration remain separate.
func servePublicBranding(store *db.Store, w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		httperr.WriteError(w, httperr.New(httperr.CodeMethodNotAllowed, "method not allowed"))
		return
	}
	if requestHasQuery(r) || requestCarriesBody(r) {
		httperr.WriteError(w, httperr.New(httperr.CodeInvalidRequest, "invalid request"))
		return
	}
	config, err := adminapi.ReadPublicConfig(store)
	if err != nil {
		httperr.WriteError(w, httperr.New(httperr.CodeInternal, "service unavailable"))
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httperr.WriteJSON(w, http.StatusOK, struct {
		SiteName    string `json:"site_name"`
		SiteLogoURL string `json:"site_logo_url"`
	}{config.SiteName, config.SiteLogoURL})
}
