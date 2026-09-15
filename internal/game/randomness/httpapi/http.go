// Package httpapi exposes only an authorized, game-owned random proof projection.
package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/game/randomness"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

func Register(registrar resources.UserRouteRegistrar, database *sql.DB, authorizer resources.FinalTxAuthorizer, game string, now func() time.Time) error {
	if registrar == nil || database == nil || authorizer == nil || now == nil {
		return randomness.ErrInvalid
	}
	h := handler(database, authorizer, game, now, nil)
	return registrar.RegisterUserRoute("GET", "/api/games/"+game+"/randomness/{id}", func(w http.ResponseWriter, r *http.Request, p resources.UserPrincipal) {
		h(w, r, resources.ContinuationUserPrincipal{UserID: p.UserID})
	})
}

type ContinuationCheck func(context.Context, *sql.Tx, resources.ContinuationUserPrincipal, string, int64) error

func RegisterContinuation(registrar resources.ContinuationUserRouteRegistrar, database *sql.DB, authorizer resources.FinalTxAuthorizer, game string, now func() time.Time, check ContinuationCheck) error {
	if registrar == nil || database == nil || authorizer == nil || now == nil || check == nil {
		return randomness.ErrInvalid
	}
	return registrar.RegisterContinuationUserRoute("GET", "/api/games/"+game+"/randomness/{id}", handler(database, authorizer, game, now, check))
}

func handler(database *sql.DB, authorizer resources.FinalTxAuthorizer, game string, now func() time.Time, check ContinuationCheck) resources.AuthenticatedContinuationHandler {
	return func(w http.ResponseWriter, r *http.Request, p resources.ContinuationUserPrincipal) {
		w.Header().Set("Cache-Control", "no-store")
		if r.URL.RawQuery != "" {
			writeError(w, randomness.ErrInvalid)
			return
		}
		if r.Body != nil {
			body, err := io.ReadAll(io.LimitReader(r.Body, 1))
			if err != nil || len(body) != 0 {
				writeError(w, randomness.ErrInvalid)
				return
			}
		}
		tx, err := database.BeginTx(r.Context(), &sql.TxOptions{ReadOnly: true})
		if err != nil {
			writeError(w, err)
			return
		}
		defer tx.Rollback()
		if err := authorizer.AuthorizeUserMutation(r.Context(), tx, p.UserID); err != nil {
			writeError(w, err)
			return
		}
		decisionNow := now().Unix()
		if check != nil {
			if err := check(r.Context(), tx, p, r.PathValue("id"), decisionNow); err != nil {
				writeError(w, err)
				return
			}
		}
		proof, err := randomness.ReadForUser(r.Context(), tx, game, r.PathValue("id"), p.UserID, decisionNow)
		if err != nil {
			writeError(w, err)
			return
		}
		body, err := json.Marshal(struct {
			Proof *randomness.Proof `json:"proof"`
		}{proof})
		if err != nil || len(body) > randomness.MaxBytes+32 {
			writeError(w, randomness.ErrLimit)
			return
		}
		if err := tx.Commit(); err != nil {
			writeError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	}
}

func writeError(w http.ResponseWriter, err error) {
	code, text := httperr.CodeInternal, "request failed"
	switch {
	case errors.Is(err, randomness.ErrInvalid):
		code, text = httperr.CodeInvalidRequest, "invalid request"
	case errors.Is(err, sql.ErrNoRows):
		code, text = httperr.CodeNotFound, "resource not found"
	case errors.Is(err, resources.ErrUnauthorized):
		code, text = httperr.CodeUnauthorized, "authentication required"
	case errors.Is(err, resources.ErrForbidden):
		code, text = httperr.CodeForbidden, "forbidden"
	case errors.Is(err, resources.ErrMaintenance):
		code, text = httperr.CodeMaintenance, "maintenance mode"
	}
	httperr.WriteError(w, httperr.New(code, text))
}
