// Package creditapi exposes read-only credit history for the current account.
package creditapi

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/httperr"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

func RegisterUserRoutes(registrar resources.UserRouteRegistrar, database *sql.DB) error {
	if registrar == nil || database == nil {
		return ledger.ErrInvalidHistory
	}
	return registrar.RegisterUserRoute(http.MethodGet, "/api/credits/history", handler(database))
}

func handler(database *sql.DB) resources.AuthorizedUserHandler {
	return func(w http.ResponseWriter, r *http.Request, principal resources.UserPrincipal) {
		if principal.UserID <= 0 {
			httperr.WriteError(w, httperr.New(httperr.CodeUnauthorized, "Sign in to view your credit history."))
			return
		}
		filter, err := parseFilter(r.URL.RawQuery)
		if err == nil && r.Body != nil {
			body, readErr := io.ReadAll(io.LimitReader(r.Body, 1))
			if len(body) != 0 || readErr != nil {
				err = ledger.ErrInvalidHistory
			}
		}
		if err != nil {
			writeError(w, ledger.ErrInvalidHistory)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		tx, err := database.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
		if err != nil {
			writeError(w, err)
			return
		}
		defer tx.Rollback()
		page, err := ledger.UserHistory(ctx, tx, principal.UserID, time.Now().Unix(), filter)
		if err == nil {
			err = tx.Commit()
		}
		if err != nil {
			writeError(w, err)
			return
		}
		httperr.WriteJSON(w, http.StatusOK, page)
	}
}

func parseFilter(raw string) (ledger.HistoryFilter, error) {
	f := ledger.HistoryFilter{Page: 1, PageSize: 20}
	if len(raw) > 2048 {
		return f, ledger.ErrInvalidHistory
	}
	values, err := url.ParseQuery(raw)
	if err != nil {
		return f, ledger.ErrInvalidHistory
	}
	for key, entries := range values {
		if len(entries) != 1 || entries[0] == "" {
			return f, ledger.ErrInvalidHistory
		}
		value := entries[0]
		switch key {
		case "category":
			f.Category = value
		case "direction":
			f.Direction = value
		case "anchor":
			f.Anchor = value
		case "page", "page_size", "from", "to":
			n, err := strconv.ParseInt(value, 10, 64)
			if err != nil || n < 0 || strconv.FormatInt(n, 10) != value {
				return f, ledger.ErrInvalidHistory
			}
			switch key {
			case "page":
				f.Page = n
			case "page_size":
				if n != 20 && n != 50 && n != 100 {
					return f, ledger.ErrInvalidHistory
				}
				f.PageSize = int(n)
			case "from":
				f.From = &n
			case "to":
				f.To = &n
			}
		default:
			return f, ledger.ErrInvalidHistory
		}
	}
	return f, ledger.ValidateHistoryFilter(f)
}

func writeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ledger.ErrInvalidHistory):
		httperr.WriteError(w, httperr.New(httperr.CodeInvalidRequest, "Check the credit history filters and page number."))
	case errors.Is(err, ledger.ErrNotFound):
		httperr.WriteError(w, httperr.New(httperr.CodeNotFound, "Credit history is unavailable for this account."))
	default:
		httperr.WriteError(w, httperr.New(httperr.CodeServiceUnavailable, "Credit history could not be loaded. Please try again."))
	}
}
