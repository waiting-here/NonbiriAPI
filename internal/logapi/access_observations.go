package logapi

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"

	"github.com/waiting-here/NonbiriAPI/internal/observability"
)

func (api *diagnosticAPI) accessObservations(w http.ResponseWriter, r *http.Request, actorID int64, summary bool) {
	if !requireNoBody(w, r) {
		return
	}
	now, err := api.repository.decisionNow()
	if err != nil {
		writeLogError(w, err)
		return
	}
	filter, err := parseAccessFilter(r.URL.RawQuery, now)
	if err != nil {
		writeLogError(w, err)
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
	var value any
	if summary {
		value, err = observability.SummarizeAccessTx(ctx, tx, filter)
	} else {
		value, err = observability.ListAccessTx(ctx, tx, filter)
	}
	if err != nil {
		if errors.Is(err, observability.ErrInvalid) {
			err = ErrInvalid
		}
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

func parseAccessFilter(raw string, now int64) (observability.AccessFilter, error) {
	filter := observability.AccessFilter{To: now, From: max(0, now-86400), Limit: 50}
	if len(raw) > 2048 {
		return filter, ErrInvalid
	}
	values, err := url.ParseQuery(raw)
	if err != nil {
		return filter, ErrInvalid
	}
	for key, value := range values {
		if len(value) != 1 {
			return filter, ErrInvalid
		}
		if key == "cursor" {
			if len(value[0]) > 256 {
				return filter, ErrInvalid
			}
			filter.Cursor = value[0]
			continue
		}
		if key == "path_kind" {
			filter.PathKind = value[0]
			continue
		}
		n, err := strconv.ParseInt(value[0], 10, 64)
		if err != nil || n < 0 {
			return filter, ErrInvalid
		}
		switch key {
		case "key_generation":
			filter.KeyGeneration = &n
		case "status_class":
			if n < 1 || n > 5 {
				return filter, ErrInvalid
			}
			filter.StatusClass = int(n)
		case "from":
			filter.From = n
		case "to":
			filter.To = n
		case "user_id":
			if n == 0 {
				return filter, ErrInvalid
			}
			filter.UserID = n
		case "page_size":
			if n < 1 || n > 100 {
				return filter, ErrInvalid
			}
			filter.Limit = int(n)
		default:
			return filter, ErrInvalid
		}
	}
	if filter.To > now || filter.From < max(0, now-observability.RetentionSeconds) || filter.To <= filter.From || filter.To-filter.From > observability.RetentionSeconds {
		return filter, ErrInvalid
	}
	if filter.KeyGeneration != nil && filter.UserID <= 0 {
		return filter, ErrInvalid
	}
	return filter, nil
}
