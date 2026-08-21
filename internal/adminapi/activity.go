package adminapi

// GET /admin/api/activity — the administrator daily activity screen.
//
// The response is a bounded page of site-local day rollups, newest first.
// Distinct-user counts are k-anonymity suppressed: a day whose
// distinct_product_users is below the threshold reports JSON null instead of
// the true count (never conflated with 0), while request/token/check-in/
// console aggregates always stay numeric. There is no user-level drill-down
// on this surface by design.
//
// While the site timezone offset is unset, activity collection is disabled:
// the endpoint reports an explicit disabled status instead of inventing day
// keys under an implicit default. The reason is never exposed beyond the
// administrator station (this tree is admin-session-only anyway).

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
)

// parseActivityQuery builds a bounded page request from strict single-value
// query parameters: page defaults to 1 (>= 1) and page_size defaults to 20,
// clamped into [1, db.MaxLogPageLimit]. Unknown, repeated, or malformed
// parameters are invalid_request.
func parseActivityQuery(r *http.Request) (db.SiteActivityQuery, httperr.Error) {
	invalid := httperr.New(httperr.CodeInvalidRequest, "invalid query parameter")
	if r == nil || r.URL == nil || !onlyParams(r, "page", "page_size") {
		return db.SiteActivityQuery{}, invalid
	}
	q := db.SiteActivityQuery{Page: 1, PageSize: defaultUserPageSize}
	if v, present, ok := singleValue(r, "page"); present {
		if !ok {
			return db.SiteActivityQuery{}, invalid
		}
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return db.SiteActivityQuery{}, invalid
		}
		q.Page = n
	}
	if v, present, ok := singleValue(r, "page_size"); present {
		if !ok {
			return db.SiteActivityQuery{}, invalid
		}
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return db.SiteActivityQuery{}, invalid
		}
		if n > db.MaxLogPageLimit {
			n = db.MaxLogPageLimit
		}
		q.PageSize = n
	}
	return q, httperr.Error{}
}

// getActivity handles GET /admin/api/activity.
func (h *Handler) getActivity(w http.ResponseWriter, r *http.Request) {
	if h.store == nil {
		writeErr(w, httperr.New(httperr.CodeServiceUnavailable, "service unavailable"))
		return
	}
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}
	query, derr := parseActivityQuery(r)
	if derr.Code != "" {
		writeErr(w, derr)
		return
	}
	resp := activityResponse{Enabled: true, Data: []activityDayJSON{}}
	if _, err := h.store.SiteTimezoneOffsetMinutes(); err != nil {
		if !errors.Is(err, db.ErrTimezoneUnavailable) {
			slog.Error("activity status check failed", "err", err)
			writeErr(w, httperr.New(httperr.CodeInternal, "internal error"))
			return
		}
		// Activity is disabled while no explicit site timezone offset is
		// configured; report that state without revealing more.
		resp.Enabled = false
		httperr.WriteJSON(w, http.StatusOK, resp)
		return
	}
	days, hasMore, err := h.store.QuerySiteActivity(r.Context(), query)
	if err != nil {
		writeRepoErr(w, err)
		return
	}
	for _, day := range days {
		resp.Data = append(resp.Data, newActivityDayJSON(day))
	}
	resp.HasMore = hasMore
	httperr.WriteJSON(w, http.StatusOK, resp)
}

type activityResponse struct {
	Enabled bool              `json:"enabled"`
	Data    []activityDayJSON `json:"data"`
	HasMore bool              `json:"has_more"`
}

// activityDayJSON is one site-local day's wire shape. distinct_product_users
// is a pointer so values below the k-anonymity threshold marshal as null;
// every other aggregate is always numeric.
type activityDayJSON struct {
	Day                   int64  `json:"day"`
	ProductActive         bool   `json:"product_active"`
	APIRequests           int64  `json:"api_requests"`
	UncachedInputTokens   int64  `json:"uncached_input_tokens"`
	CacheWriteInputTokens int64  `json:"cache_write_input_tokens"`
	CacheReadInputTokens  int64  `json:"cache_read_input_tokens"`
	OutputTokens          int64  `json:"output_tokens"`
	Checkins              int64  `json:"checkins"`
	ConsoleWrites         int64  `json:"console_writes"`
	GameActive            bool   `json:"game_active"`
	GameRounds            int64  `json:"game_rounds"`
	DistinctProductUsers  *int64 `json:"distinct_product_users"`
}

func newActivityDayJSON(day db.SiteActivityDay) activityDayJSON {
	out := activityDayJSON{
		Day:                   day.Day,
		ProductActive:         day.ProductActive,
		APIRequests:           day.APIRequests,
		UncachedInputTokens:   day.UncachedInputTokens,
		CacheWriteInputTokens: day.CacheWriteInputTokens,
		CacheReadInputTokens:  day.CacheReadInputTokens,
		OutputTokens:          day.OutputTokens,
		Checkins:              day.Checkins,
		ConsoleWrites:         day.ConsoleWrites,
		GameActive:            day.GameActive,
		GameRounds:            day.GameRounds,
	}
	if day.DistinctProductUsers >= db.ActivityKAnonymity {
		count := day.DistinctProductUsers
		out.DistinctProductUsers = &count
	}
	return out
}
