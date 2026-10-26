package app

import (
	"database/sql"
	"net/http"

	"github.com/waiting-here/NonbiriAPI/internal/auth"
	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

const (
	adminCloudflareRoutes   = "/admin/api/egress/cloudflare-routes"
	stewardCloudflareRoutes = "/api/steward/egress/cloudflare-routes"
)

func (a *application) registerCloudflareRoutes(store *db.Store) error {
	if err := a.authRuntime.RegisterAdminRoute(http.MethodGet, adminCloudflareRoutes, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a.serveCloudflareRoutes(store, w, r, authz.RoleAdministrator)
	})); err != nil {
		return err
	}
	return a.authRuntime.RegisterUserRoute(http.MethodGet, stewardCloudflareRoutes, func(w http.ResponseWriter, r *http.Request, _ resources.UserPrincipal) {
		a.serveCloudflareRoutes(store, w, r, authz.RoleSteward)
	})
}

func (a *application) serveCloudflareRoutes(store *db.Store, w http.ResponseWriter, r *http.Request, role authz.Role) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodGet {
		httperr.WriteError(w, httperr.New(httperr.CodeMethodNotAllowed, "method not allowed"))
		return
	}
	if requestHasQuery(r) || requestCarriesBody(r) {
		httperr.WriteError(w, httperr.New(httperr.CodeInvalidRequest, "invalid request"))
		return
	}
	actor, ok := auth.ActorFromContext(r.Context())
	if !ok {
		writeCloudflareStatusError(w, authz.ErrUnauthorized)
		return
	}
	tx, err := store.ReadDB().BeginTx(r.Context(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		writeCloudflareStatusError(w, err)
		return
	}
	defer tx.Rollback()
	if _, err := a.authorizer.Authorize(r.Context(), tx, actor, authz.Requirement{Role: role}); err != nil {
		writeCloudflareStatusError(w, err)
		return
	}
	status := a.egress.CloudflareStatus()
	if err := tx.Commit(); err != nil {
		writeCloudflareStatusError(w, err)
		return
	}
	httperr.WriteJSON(w, http.StatusOK, status)
}

func writeCloudflareStatusError(w http.ResponseWriter, err error) {
	if authz.WritePermissionDenied(w, err) {
		return
	}
	httperr.WriteMapped(w, err,
		httperr.Mapping{Err: authz.ErrUnauthorized, Code: httperr.CodeUnauthorized, Message: "authentication required"},
		httperr.Mapping{Err: authz.ErrForbidden, Code: httperr.CodeForbidden, Message: "forbidden"},
		httperr.Mapping{Err: authz.ErrMFARequired, Code: httperr.CodeMFARequired, Message: "MFA verification is required."},
	)
}
