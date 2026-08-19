package adminapi

// Strict query-parameter parsing for the admin user routes. Every parameter
// is single-valued and pre-validated before it reaches the repository; page
// sizes are clamped; unknown, repeated, over-long, or malformed parameters
// are rejected with the stable invalid_request envelope.

import (
	"net/http"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
)

const (
	defaultUserPageSize = 20
	maxRawQueryBytes    = 8192
	// maxSearchQueryRunes bounds the optional admin user-search filter.
	maxSearchQueryRunes = 128
)

// parseUserListQuery builds a bounded db.UserListQuery from strict
// single-value query parameters. page defaults to 1 and must be >= 1;
// page_size defaults to 20 and clamps into [1, db.MaxUserListPageSize];
// is_banned accepts exactly "true"/"false"; q is an optional trimmed
// substring filter on username/discord_id (empty means no filter).
func parseUserListQuery(r *http.Request) (db.UserListQuery, httperr.Error) {
	invalid := httperr.New(httperr.CodeInvalidRequest, "invalid query parameter")
	if r == nil || r.URL == nil || len(r.URL.RawQuery) > maxRawQueryBytes ||
		!onlyParams(r, "page", "page_size", "is_banned", "q") {
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
	if v, present, ok := singleValue(r, "q"); present {
		if !ok {
			return db.UserListQuery{}, invalid
		}
		q.Q = strings.TrimSpace(v)
		if !validSearchQuery(q.Q) {
			return db.UserListQuery{}, invalid
		}
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

// validSearchQuery mirrors the bounded-text rules for the admin user-search
// filter: valid UTF-8, at most maxSearchQueryRunes runes, no C0 controls or
// DEL, and no leading/trailing whitespace. An empty value is valid and means
// no filter (the repository omits the LIKE predicate).
func validSearchQuery(s string) bool {
	if s == "" {
		return true
	}
	if !utf8.ValidString(s) || utf8.RuneCountInString(s) > maxSearchQueryRunes {
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
