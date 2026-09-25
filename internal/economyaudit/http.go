package economyaudit

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
)

func RegisterRoutes(registrar AdminRouteRegistrar, service *Service) error {
	if registrar == nil || service == nil {
		return ErrInvalid
	}
	for _, name := range []string{"summary", "series", "channels", "operations"} {
		if err := registrar.RegisterAdminRoute(http.MethodGet, "/admin/api/economy-audit/"+name, func(w http.ResponseWriter, r *http.Request, p AdminPrincipal) {
			filter, err := parseFilter(r, service.now().Unix())
			if err != nil {
				writeError(w, err)
				return
			}
			var result any
			switch name {
			case "summary":
				result, err = service.Summary(r.Context(), p.UserID, filter)
			case "series":
				result, err = service.Series(r.Context(), p.UserID, filter)
			case "channels":
				result, err = service.Channels(r.Context(), p.UserID, filter)
			case "operations":
				result, err = service.Operations(r.Context(), p.UserID, filter)
			}
			if err != nil {
				writeError(w, err)
				return
			}
			w.Header().Set("Cache-Control", "no-store")
			httperr.WriteJSON(w, http.StatusOK, result)
		}); err != nil {
			return err
		}
	}
	return nil
}

func parseFilter(r *http.Request, now int64) (Filter, error) {
	f := Filter{Asset: ledger.General, From: max(0, now-86400), To: min(maxUnix, now+1), Bucket: "hour"}
	if r == nil || r.URL == nil || len(r.URL.RawQuery) > 4096 {
		return f, ErrInvalid
	}
	if r.Body != nil {
		data, err := io.ReadAll(io.LimitReader(r.Body, 1))
		if err != nil || len(data) != 0 {
			return f, ErrInvalid
		}
	}
	values, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return f, ErrInvalid
	}
	for key, entries := range values {
		if len(entries) != 1 || entries[0] == "" {
			return f, ErrInvalid
		}
		value := entries[0]
		switch key {
		case "asset":
			f.Asset = ledger.Asset(value)
		case "kind":
			f.Kind = value
		case "channel":
			f.Channel = value
		case "bucket":
			f.Bucket = value
		case "cursor":
			f.Cursor = value
		case "from", "to":
			n, err := strconv.ParseInt(value, 10, 64)
			if err != nil || strconv.FormatInt(n, 10) != value {
				return f, ErrInvalid
			}
			if key == "from" {
				f.From = n
			} else {
				f.To = n
			}
		default:
			return f, ErrInvalid
		}
	}
	return f, validateFilter(f)
}

func writeError(w http.ResponseWriter, err error) {
	code, message := httperr.CodeServiceUnavailable, "The accounting audit could not be loaded. Please try again."
	switch {
	case errors.Is(err, ErrInvalid):
		code, message = httperr.CodeInvalidRequest, "Check the asset, time range and audit filters."
	case errors.Is(err, authz.ErrUnauthorized):
		code, message = httperr.CodeUnauthorized, "Sign in to view accounting audits."
	case errors.Is(err, authz.ErrForbidden):
		code, message = httperr.CodeForbidden, "Accounting audits require administrator access."
	case errors.Is(err, db.ErrTimezoneUnavailable):
		code, message = httperr.CodeFeatureDisabled, "Configure the site time zone before viewing accounting audits."
	default:
		category := "internal"
		switch {
		case errors.Is(err, ErrInvariant):
			category = "invariant"
		case errors.Is(err, ErrUnavailable):
			category = "unavailable"
		case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
			category = "timeout_or_cancel"
		}
		// Do not log the raw error: SQLite text can include private row content.
		slog.Error("economy audit read failed", "category", category)
	}
	httperr.WriteError(w, httperr.New(code, message))
}
