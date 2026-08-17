package adminapi

// Strict query-parameter parsing for the admin user routes. Every parameter
// is single-valued and pre-validated before it reaches the repository; page
// sizes are clamped; unknown, repeated, over-long, or malformed parameters
// are rejected with the stable invalid_request envelope.

import (
	"net/http"
	"strconv"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
)

const (
	defaultUserPageSize = 20
	maxRawQueryBytes    = 8192
)

// parseUserListQuery builds a bounded db.UserListQuery from strict
// single-value query parameters. page defaults to 1 and must be >= 1;
// page_size defaults to 20 and clamps into [1, db.MaxUserListPageSize];
// is_banned accepts exactly "true"/"false".
func parseUserListQuery(r *http.Request) (db.UserListQuery, httperr.Error) {
	invalid := httperr.New(httperr.CodeInvalidRequest, "invalid query parameter")
	if r == nil || r.URL == nil || len(r.URL.RawQuery) > maxRawQueryBytes ||
		!onlyParams(r, "page", "page_size", "is_banned") {
		return db.UserListQuery{}, invalid
	}
	q := db.UserListQuery{Page: 1, PageSize: defaultUserPageSize}

	if v, present, ok := singleValue(r, "page"); present {
		if !ok {
			return db.UserListQuery{}, invalid
		}
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return db.UserListQuery{}, invalid
		}
		q.Page = n
	}
	if v, present, ok := singleValue(r, "page_size"); present {
		if !ok {
			return db.UserListQuery{}, invalid
		}
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return db.UserListQuery{}, invalid
		}
		if n > db.MaxUserListPageSize {
			n = db.MaxUserListPageSize
		}
		q.PageSize = n
	}
	if v, present, ok := singleValue(r, "is_banned"); present {
		if !ok || (v != "true" && v != "false") {
			return db.UserListQuery{}, invalid
		}
		banned := v == "true"
		q.BannedOnly = &banned
	}
	return q, httperr.Error{}
}

// rejectUnknownParams rejects any query parameter for routes that take none.
func rejectUnknownParams(r *http.Request) httperr.Error {
	if r == nil || r.URL == nil || len(r.URL.RawQuery) > maxRawQueryBytes ||
		!onlyParams(r) {
		return httperr.New(httperr.CodeInvalidRequest, "invalid query parameter")
	}
	return httperr.Error{}
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
// parameter is ambiguous and rejected: the server never chooses an
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
