package logapi

// Query parameter parsing for the log/usage routes. Every parameter is
// single-valued and pre-validated before it reaches the repository; page
// sizes are clamped; unknown or ambiguous parameters, out-of-range values,
// and over-long inputs are rejected with the stable invalid_request envelope
// so a broken or hostile client cannot force unbounded parameter work or
// bypass a filter.

import (
	"net/http"
	"strconv"
	"unicode"
	"unicode/utf8"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
)

const (
	// defaultLogPageSize is the page size used when page_size is omitted.
	defaultLogPageSize = 20
	// maxModelFilterRunes mirrors the repository's stored-text bound for the
	// platform model filter.
	maxModelFilterRunes = 512
	// maxRawQueryBytes bounds the whole query string before parsing. The
	// parameter set is small and values are bounded individually; a larger
	// query cannot be legitimate.
	maxRawQueryBytes = 8192
)

// parseLogQuery builds a bounded db.LogQuery from strict single-value query
// parameters. Unknown parameters, repeated parameters, unparseable or
// out-of-range values, and over-long inputs are rejected with
// invalid_request. page defaults to 1 and must be >= 1; page_size defaults
// to 20 and clamps into [1, db.MaxLogPageLimit].
func parseLogQuery(r *http.Request) (db.LogQuery, httperr.Error) {
	invalid := httperr.New(httperr.CodeInvalidRequest, "invalid query parameter")
	if r == nil || r.URL == nil || len(r.URL.RawQuery) > maxRawQueryBytes ||
		!onlyParams(r, "user_id", "model", "status", "from", "to", "page", "page_size") {
		return db.LogQuery{}, invalid
	}
	q := db.LogQuery{Page: 1, PageSize: defaultLogPageSize}

	if v, present, ok := singleValue(r, "user_id"); present {
		if !ok {
			return db.LogQuery{}, invalid
		}
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil || id <= 0 {
			return db.LogQuery{}, invalid
		}
		q.UserID = id
	}
	if v, present, ok := singleValue(r, "model"); present {
		if !ok || !validStoredTextFilter(v) {
			return db.LogQuery{}, invalid
		}
		q.Model = v
	}
	if v, present, ok := singleValue(r, "status"); present {
		if !ok {
			return db.LogQuery{}, invalid
		}
		status, err := strconv.Atoi(v)
		if err != nil || status < 100 || status > 599 {
			return db.LogQuery{}, invalid
		}
		q.Status = status
	}
	if v, present, ok := singleValue(r, "from"); present {
		if !ok {
			return db.LogQuery{}, invalid
		}
		from, err := strconv.ParseInt(v, 10, 64)
		if err != nil || from < 0 {
			return db.LogQuery{}, invalid
		}
		q.FromUnix = from
	}
	if v, present, ok := singleValue(r, "to"); present {
		if !ok {
			return db.LogQuery{}, invalid
		}
		to, err := strconv.ParseInt(v, 10, 64)
		if err != nil || to < 0 {
			return db.LogQuery{}, invalid
		}
		q.ToUnix = to
	}
	if q.FromUnix > 0 && q.ToUnix > 0 && q.FromUnix > q.ToUnix {
		return db.LogQuery{}, invalid
	}
	if v, present, ok := singleValue(r, "page"); present {
		if !ok {
			return db.LogQuery{}, invalid
		}
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return db.LogQuery{}, invalid
		}
		q.Page = n
	}
	if v, present, ok := singleValue(r, "page_size"); present {
		if !ok {
			return db.LogQuery{}, invalid
		}
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return db.LogQuery{}, invalid
		}
		if n > db.MaxLogPageLimit {
			n = db.MaxLogPageLimit
		}
		q.PageSize = n
	}

	// Every value above is pre-validated against the repository's own rules
	// (user id > 0, model stored-text bounds, status 100..599, non-negative
	// non-inverted time range, 1-based page and clamped page size), so
	// QueryRequestLogs can never see an invalid filter. The repository
	// re-validates defensively; a failure there maps to internal error without
	// echoing anything.
	return q, httperr.Error{}
}

// parseEndpointOverviewQuery builds a bounded db.EndpointOverviewQuery from
// strict single-value query parameters for /admin/api/overview/endpoints.
// Unknown parameters, repeated parameters, unparseable or out-of-range
// values, and over-long inputs are rejected with invalid_request. page
// defaults to 1 and must be >= 1; page_size defaults to 20 and clamps into
// [1, db.MaxOverviewPageLimit]; filter is an optional literal substring
// matched against the stored canonical base_url.
func parseEndpointOverviewQuery(r *http.Request) (db.EndpointOverviewQuery, httperr.Error) {
	invalid := httperr.New(httperr.CodeInvalidRequest, "invalid query parameter")
	if r == nil || r.URL == nil || len(r.URL.RawQuery) > maxRawQueryBytes ||
		!onlyParams(r, "page", "page_size", "filter") {
		return db.EndpointOverviewQuery{}, invalid
	}
	q := db.EndpointOverviewQuery{Page: 1, PageSize: defaultLogPageSize}

	if v, present, ok := singleValue(r, "page"); present {
		if !ok {
			return db.EndpointOverviewQuery{}, invalid
		}
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return db.EndpointOverviewQuery{}, invalid
		}
		q.Page = n
	}
	if v, present, ok := singleValue(r, "page_size"); present {
		if !ok {
			return db.EndpointOverviewQuery{}, invalid
		}
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return db.EndpointOverviewQuery{}, invalid
		}
		if n > db.MaxOverviewPageLimit {
			n = db.MaxOverviewPageLimit
		}
		q.PageSize = n
	}
	if v, present, ok := singleValue(r, "filter"); present {
		if !ok || !validStoredTextFilter(v) {
			return db.EndpointOverviewQuery{}, invalid
		}
		q.Filter = v
	}
	return q, httperr.Error{}
}

// parseUsageQuery reads the aggregation selector for /admin/api/usage.
// group_by=site (default) selects site-wide totals; group_by=user selects
// per-user totals ordered by total requests descending. limit clamps into
// [1, db.MaxUsageByUserLimit] and applies to the by-user aggregation only.
func parseUsageQuery(r *http.Request) (groupBy string, limit int, derr httperr.Error) {
	invalid := httperr.New(httperr.CodeInvalidRequest, "invalid query parameter")
	groupBy = "site"
	limit = db.MaxUsageByUserLimit
	if r == nil || r.URL == nil || len(r.URL.RawQuery) > maxRawQueryBytes ||
		!onlyParams(r, "group_by", "limit") {
		return "", 0, invalid
	}
	if v, present, ok := singleValue(r, "group_by"); present {
		if !ok || (v != "site" && v != "user") {
			return "", 0, invalid
		}
		groupBy = v
	}
	if v, present, ok := singleValue(r, "limit"); present {
		if !ok {
			return "", 0, invalid
		}
		n, err := strconv.Atoi(v)
		if err != nil {
			return "", 0, invalid
		}
		if n < 1 {
			n = 1
		}
		if n > db.MaxUsageByUserLimit {
			n = db.MaxUsageByUserLimit
		}
		limit = n
	}
	return groupBy, limit, httperr.Error{}
}

// onlyParams reports whether every query key is one of the allowed names.
func onlyParams(r *http.Request, allowed ...string) bool {
	values := r.URL.Query()
	if len(values) == 0 {
		return true
	}
	known := make(map[string]bool, len(allowed))
	for _, name := range allowed {
		known[name] = true
	}
	for name := range values {
		if !known[name] {
			return false
		}
	}
	return true
}

// singleValue returns the single occurrence of a query parameter. A repeated
// parameter is ambiguous and rejected (ok=false): the server never chooses an
// attacker-controlled ordering.
func singleValue(r *http.Request, name string) (value string, present, ok bool) {
	values, exists := r.URL.Query()[name]
	if !exists || len(values) == 0 {
		return "", false, true
	}
	if len(values) != 1 {
		return "", true, false
	}
	return values[0], true, true
}

// validStoredTextFilter mirrors the repository's stored-text rules for a
// free-text filter (platform model name, endpoint base_url substring): valid
// UTF-8, at most maxModelFilterRunes runes, no C0 controls or DEL, and no
// leading/trailing whitespace. An empty value is rejected here: a client
// that wants no filter omits the parameter.
func validStoredTextFilter(s string) bool {
	if s == "" || !utf8.ValidString(s) || utf8.RuneCountInString(s) > maxModelFilterRunes {
		return false
	}
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	first, _ := utf8.DecodeRuneInString(s)
	last, _ := utf8.DecodeLastRuneInString(s)
	return !unicode.IsSpace(first) && !unicode.IsSpace(last)
}
