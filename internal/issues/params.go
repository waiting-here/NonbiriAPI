package issues

// Query parameter parsing for the issue routes. Every parameter is
// single-valued and pre-validated before it reaches the repository; page
// sizes are clamped; unknown or ambiguous parameters, out-of-range values,
// and over-long inputs are rejected with the stable invalid_request envelope
// so a broken or hostile client cannot force unbounded parameter work or
// bypass a filter.

import (
	"net/http"
	"strconv"

	"nonbiriapi/internal/db"
	"nonbiriapi/internal/httperr"
)

const (
	// defaultIssueLimit is the page size used when limit is omitted.
	defaultIssueLimit = 100
	// maxRawQueryBytes bounds the whole query string before parsing. The
	// parameter set is small and values are bounded individually; a larger
	// query cannot be legitimate.
	maxRawQueryBytes = 8192
)

// parseIssueQuery builds a bounded db.IssueQuery from strict single-value
// query parameters. Only resolved / before_id / limit are accepted; anything
// else (including repeats) is invalid_request. limit clamps into
// [1, db.MaxIssuePageLimit] and defaults to defaultIssueLimit when omitted.
// The owner id is not a parameter: it always comes from the session principal
// inside the handler, so no query can ever select another user's issues.
func parseIssueQuery(r *http.Request) (db.IssueQuery, httperr.Error) {
	invalid := httperr.New(httperr.CodeInvalidRequest, "invalid query parameter")
	if r == nil || r.URL == nil || len(r.URL.RawQuery) > maxRawQueryBytes ||
		!onlyParams(r, "resolved", "before_id", "limit") {
		return db.IssueQuery{}, invalid
	}
	q := db.IssueQuery{Limit: defaultIssueLimit}

	if v, present, ok := singleValue(r, "resolved"); present {
		if !ok {
			return db.IssueQuery{}, invalid
		}
		var resolved bool
		switch v {
		case "true":
			resolved = true
		case "false":
			resolved = false
		default:
			return db.IssueQuery{}, invalid
		}
		q.Resolved = &resolved
	}
	if v, present, ok := singleValue(r, "before_id"); present {
		if !ok {
			return db.IssueQuery{}, invalid
		}
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil || id <= 0 {
			return db.IssueQuery{}, invalid
		}
		q.BeforeID = id
	}
	if v, present, ok := singleValue(r, "limit"); present {
		if !ok {
			return db.IssueQuery{}, invalid
		}
		n, err := strconv.Atoi(v)
		if err != nil {
			return db.IssueQuery{}, invalid
		}
		if n < 1 {
			n = 1
		}
		if n > db.MaxIssuePageLimit {
			n = db.MaxIssuePageLimit
		}
		q.Limit = n
	}

	// The owner predicate is mandatory and never comes from this parser; the
	// handler assigns it from the session principal. QueryUserIssues
	// re-validates defensively; a failure there maps to internal error
	// without echoing anything.
	return q, httperr.Error{}
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
