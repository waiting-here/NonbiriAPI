package logapi

import (
	"context"
	"database/sql"
	"net/http"
	"net/url"
	"strconv"

	"github.com/waiting-here/NonbiriAPI/internal/auth"
	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/observability"
)

type DiagnosticAdminAuthorizer interface {
	AuthorizeAdmin(context.Context, *sql.Tx, int64) error
}

type diagnosticAPI struct {
	repository *Repository
	steward    StewardAuthorizer
	admin      DiagnosticAdminAuthorizer
}

// RegisterAdminDiagnosticRoutes adds separate lazy, management-only reads.
// User log exports and ordinary attempt DTOs cannot include these fields.
func RegisterAdminDiagnosticRoutes(registrar AdminRouteRegistrar, repository *Repository, authorizer DiagnosticAdminAuthorizer) error {
	if registrar == nil || repository == nil || authorizer == nil {
		return ErrInvalid
	}
	api := &diagnosticAPI{repository: repository, admin: authorizer}
	if err := registrar.RegisterAdminRoute(http.MethodGet, "/admin/api/logs/diagnostic-capacity", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { api.diagnosticCapacity(w, r, 0) })); err != nil {
		return err
	}
	for _, path := range []string{"/admin/api/logs/{id}/source", "/admin/api/logs/{id}/attempts/{seq}/errors", "/admin/api/logs/{id}/attempts/{seq}/errors/{event}"} {
		if err := registrar.RegisterAdminRoute(http.MethodGet, path, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { api.diagnostic(w, r, 0) })); err != nil {
			return err
		}
	}
	if err := registrar.RegisterAdminRoute(http.MethodGet, "/admin/api/abuse-audit/access-events", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { api.accessObservations(w, r, 0, false) })); err != nil {
		return err
	}
	if err := registrar.RegisterAdminRoute(http.MethodGet, "/admin/api/abuse-audit/access-summary", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { api.accessObservations(w, r, 0, true) })); err != nil {
		return err
	}
	return nil
}

func RegisterStewardDiagnosticRoutes(registrar UserRouteRegistrar, repository *Repository, authorizer StewardAuthorizer) error {
	if registrar == nil || repository == nil || authorizer == nil {
		return ErrInvalid
	}
	api := &diagnosticAPI{repository: repository, steward: authorizer}
	if err := registrar.RegisterUserRoute(http.MethodGet, "/api/steward/logs/diagnostic-capacity", func(w http.ResponseWriter, r *http.Request, p UserPrincipal) { api.diagnosticCapacity(w, r, p.UserID) }); err != nil {
		return err
	}
	for _, path := range []string{"/api/steward/logs/{id}/source", "/api/steward/logs/{id}/attempts/{seq}/errors", "/api/steward/logs/{id}/attempts/{seq}/errors/{event}"} {
		if err := registrar.RegisterUserRoute(http.MethodGet, path, func(w http.ResponseWriter, r *http.Request, p UserPrincipal) { api.diagnostic(w, r, p.UserID) }); err != nil {
			return err
		}
	}
	if err := registrar.RegisterUserRoute(http.MethodGet, "/api/steward/abuse-audit/access-events", func(w http.ResponseWriter, r *http.Request, p UserPrincipal) {
		api.accessObservations(w, r, p.UserID, false)
	}); err != nil {
		return err
	}
	if err := registrar.RegisterUserRoute(http.MethodGet, "/api/steward/abuse-audit/access-summary", func(w http.ResponseWriter, r *http.Request, p UserPrincipal) {
		api.accessObservations(w, r, p.UserID, true)
	}); err != nil {
		return err
	}
	return nil
}

func (api *diagnosticAPI) diagnostic(w http.ResponseWriter, r *http.Request, actorID int64) {
	if !requireNoBody(w, r) {
		return
	}
	requestID := r.PathValue("id")
	if !db.ValidateOpaqueID(requestID, "req_") {
		writeLogError(w, ErrInvalid)
		return
	}
	query, err := parseDiagnosticQuery(r)
	if err != nil {
		writeLogError(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), logPageTimeout)
	defer cancel()
	tx, rootID, err := api.beginDiagnosticRead(ctx, requestID, actorID)
	if err != nil {
		writeLogError(w, err)
		return
	}
	defer tx.Rollback()
	var value any
	if query.attempt == 0 {
		value, err = observability.SourceTx(ctx, tx, rootID)
	} else if query.event > 0 {
		value, err = observability.ErrorBodyTx(ctx, tx, rootID, query.attempt, query.event)
	} else {
		value, err = observability.ListErrorsTx(ctx, tx, rootID, query.attempt, query.after)
	}
	if err != nil {
		writeLogError(w, translateSQLError(err))
		return
	}
	if err = tx.Commit(); err != nil {
		writeLogError(w, translateSQLError(err))
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	writeLogJSON(w, http.StatusOK, value)
}

func (api *diagnosticAPI) diagnosticCapacity(w http.ResponseWriter, r *http.Request, actorID int64) {
	if !requireNoBody(w, r) {
		return
	}
	if r.URL.RawQuery != "" {
		writeLogError(w, ErrInvalid)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), logPageTimeout)
	defer cancel()
	tx, err := api.beginAuthorizedRead(ctx, actorID)
	if err != nil {
		writeLogError(w, err)
		return
	}
	defer tx.Rollback()
	value, err := observability.RawCapacityTx(ctx, tx)
	if err != nil {
		writeLogError(w, translateSQLError(err))
		return
	}
	if err = tx.Commit(); err != nil {
		writeLogError(w, translateSQLError(err))
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeLogJSON(w, http.StatusOK, value)
}

type diagnosticQuery struct {
	attempt      int
	event, after int64
}

func parseDiagnosticQuery(r *http.Request) (diagnosticQuery, error) {
	var q diagnosticQuery
	var err error
	if len(r.URL.RawQuery) > 512 {
		return q, ErrInvalid
	}
	values, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return q, ErrInvalid
	}
	if r.PathValue("seq") != "" {
		q.attempt, err = strconv.Atoi(r.PathValue("seq"))
		if err != nil || q.attempt < 1 || q.attempt > 100 {
			return q, ErrInvalid
		}
	}
	if r.PathValue("event") != "" {
		q.event, err = strconv.ParseInt(r.PathValue("event"), 10, 64)
		if err != nil || q.event < 1 {
			return q, ErrInvalid
		}
	}
	for key, v := range values {
		if key != "after" || q.attempt == 0 || q.event != 0 || len(v) != 1 {
			return q, ErrInvalid
		}
		q.after, err = strconv.ParseInt(v[0], 10, 64)
		if err != nil || q.after < 0 {
			return q, ErrInvalid
		}
	}
	return q, nil
}

func (api *diagnosticAPI) beginDiagnosticRead(ctx context.Context, requestID string, actorID int64) (*sql.Tx, int64, error) {
	r := api.repository
	authorizer := api.steward
	now, err := r.decisionNow()
	if err != nil {
		return nil, 0, err
	}
	tx, err := api.beginAuthorizedRead(ctx, actorID)
	if err != nil {
		return nil, 0, err
	}
	success := false
	defer func() {
		if !success {
			_ = tx.Rollback()
		}
	}()
	var id int64
	var completed sql.NullInt64
	if err = tx.QueryRowContext(ctx, `SELECT id,completed_at FROM request_logs WHERE logical_request_id=?`, requestID).Scan(&id, &completed); err != nil {
		return nil, 0, translateSQLError(err)
	}
	visible := requestLogOrdinarilyVisible(completed, now)
	if authorizer == nil && r.heldRead != nil {
		var held bool
		held, err = r.heldRead.AuthorizeHeldRequestLogRead(ctx, tx, id, now)
		visible = visible || held
	} else if !visible && authorizer != nil {
		if held, ok := r.heldRead.(StewardHeldReadAuthorizer); ok {
			visible, err = held.AuthorizeStewardHeldRequestLogRead(ctx, tx, actorID, id, now)
		}
	}
	if err != nil {
		return nil, 0, err
	}
	if !visible {
		return nil, 0, ErrNotFound
	}
	success = true
	return tx, id, nil
}

// The final authority validates the exact session identity and generation in
// the same snapshot used for every newly sensitive projection.
func (api *diagnosticAPI) beginAuthorizedRead(ctx context.Context, actorID int64) (*sql.Tx, error) {
	if api.steward != nil {
		return api.repository.beginStewardRead(ctx, actorID, api.steward)
	}
	actor, present := auth.ActorFromContext(ctx)
	if api.admin == nil || !present || actor.Kind != authz.ActorAdminSession || (actorID != 0 && actorID != actor.UserID) {
		return nil, ErrForbidden
	}
	tx, err := api.repository.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, translateSQLError(err)
	}
	if err = api.admin.AuthorizeAdmin(ctx, tx, actor.UserID); err != nil {
		_ = tx.Rollback()
		return nil, ErrForbidden
	}
	return tx, nil
}
