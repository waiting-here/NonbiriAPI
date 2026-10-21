package logapi

import (
	"context"
	"net/http"

	"github.com/waiting-here/NonbiriAPI/internal/observability"
)

type PressureReader interface {
	Snapshot() observability.PressureSnapshot
}

func RegisterAdminPressureRoute(registrar AdminRouteRegistrar, repository *Repository, authorizer DiagnosticAdminAuthorizer, reader PressureReader) error {
	if registrar == nil || repository == nil || authorizer == nil || reader == nil {
		return ErrInvalid
	}
	api := &diagnosticAPI{repository: repository, admin: authorizer}
	return registrar.RegisterAdminRoute(http.MethodGet, "/admin/api/logs/pressure", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		api.pressure(w, r, 0, reader)
	}))
}

func RegisterStewardPressureRoute(registrar UserRouteRegistrar, repository *Repository, authorizer StewardAuthorizer, reader PressureReader) error {
	if registrar == nil || repository == nil || authorizer == nil || reader == nil {
		return ErrInvalid
	}
	api := &diagnosticAPI{repository: repository, steward: authorizer}
	return registrar.RegisterUserRoute(http.MethodGet, "/api/steward/logs/pressure", func(w http.ResponseWriter, r *http.Request, principal UserPrincipal) {
		api.pressure(w, r, principal.UserID, reader)
	})
}

func (api *diagnosticAPI) pressure(w http.ResponseWriter, r *http.Request, actorID int64, reader PressureReader) {
	if !requireNoBody(w, r) {
		return
	}
	if r.URL.RawQuery != "" {
		writeLogError(w, ErrInvalid)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), logPageTimeout)
	defer cancel()
	tx, _, err := api.beginAuthorizedReadOn(ctx, api.repository.readDB, actorID)
	if err != nil {
		writeLogError(w, err)
		return
	}
	defer tx.Rollback()
	value := reader.Snapshot()
	if err := tx.Commit(); err != nil {
		writeLogError(w, translateReadError(ctx, err))
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeManagementLogJSON(w, http.StatusOK, value)
}
