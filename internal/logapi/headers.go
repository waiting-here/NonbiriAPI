package logapi

import (
	"context"
	"net/http"
	"strconv"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/observability"
)

func (api *diagnosticAPI) headers(w http.ResponseWriter, r *http.Request, actorID int64) {
	if !requireNoBody(w, r) {
		return
	}
	requestID := r.PathValue("id")
	if !db.ValidateOpaqueID(requestID, "req_") || r.URL.RawQuery != "" {
		writeLogError(w, ErrInvalid)
		return
	}
	attempt := 0
	if raw := r.PathValue("seq"); raw != "" {
		var err error
		attempt, err = strconv.Atoi(raw)
		if err != nil || attempt < 1 || attempt > 100 {
			writeLogError(w, ErrInvalid)
			return
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), logPageTimeout)
	defer cancel()
	tx, rootID, redact, err := api.beginDiagnosticRead(ctx, requestID, actorID)
	if err != nil {
		writeLogError(w, err)
		return
	}
	defer tx.Rollback()
	stored, err := observability.HeadersTx(ctx, tx, rootID, attempt)
	if err != nil {
		writeLogError(w, translateReadError(ctx, err))
		return
	}
	if err = tx.Commit(); err != nil {
		writeLogError(w, translateReadError(ctx, err))
		return
	}
	value, err := stored.Decode(redact)
	if err != nil {
		writeLogError(w, ErrUnavailable)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	writeManagementLogJSON(w, http.StatusOK, value)
}
