package alertapi

// Query/body parsing for the admin alert routes. Every parameter is
// single-valued and pre-validated before it reaches the repository; page
// sizes are clamped; unknown or ambiguous parameters, out-of-range values,
// and over-long inputs are rejected with the stable invalid_request envelope
// so a broken or hostile client cannot force unbounded parameter work or
// bypass a filter. The resolve body is strict single-object JSON; a missing
// or empty body means resolve (true), which keeps a plain POST an ack.

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
)

const (
	// DefaultAlertPageLimit is the page size used when limit is omitted.
	DefaultAlertPageLimit = 100
	// maxRawQueryBytes bounds the whole query string before parsing. The
	// parameter set is small and values are bounded individually; a larger
	// query cannot be legitimate.
	maxRawQueryBytes = 8192
	// maxResolveBodyBytes bounds the resolve request body. The only legal
	// body is a one-field object; anything larger is a hostile client.
	maxResolveBodyBytes = 4096
)

// parseAlertsQuery builds a bounded db.AlertQuery from strict single-value
// query parameters. Unknown parameters, repeated parameters, unparseable or
// out-of-range values, and over-long inputs are rejected with
// invalid_request. resolved accepts true/false; limit clamps into
// [1, db.MaxAdminAlertsPageLimit] and defaults to DefaultAlertPageLimit.
func parseAlertsQuery(r *http.Request) (db.AlertQuery, httperr.Error) {
	invalid := httperr.New(httperr.CodeInvalidRequest, "invalid query parameter")
	if r == nil || r.URL == nil || len(r.URL.RawQuery) > maxRawQueryBytes ||
		!onlyParams(r, "resolved", "before_id", "limit") {
		return db.AlertQuery{}, invalid
	}
	q := db.AlertQuery{Limit: DefaultAlertPageLimit}

	if v, present, ok := singleValue(r, "resolved"); present {
		if !ok {
			return db.AlertQuery{}, invalid
		}
		switch v {
		case "true":
			resolved := true
			q.Resolved = &resolved
		case "false":
			resolved := false
			q.Resolved = &resolved
		default:
			return db.AlertQuery{}, invalid
		}
	}
	if v, present, ok := singleValue(r, "before_id"); present {
		if !ok {
			return db.AlertQuery{}, invalid
		}
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil || id <= 0 {
			return db.AlertQuery{}, invalid
		}
		q.BeforeID = id
	}
	if v, present, ok := singleValue(r, "limit"); present {
		if !ok {
			return db.AlertQuery{}, invalid
		}
		n, err := strconv.Atoi(v)
		if err != nil {
			return db.AlertQuery{}, invalid
		}
		if n < 1 {
			n = 1
		}
		if n > db.MaxAdminAlertsPageLimit {
			n = db.MaxAdminAlertsPageLimit
		}
		q.Limit = n
	}

	// Every value above is pre-validated against the repository's own rules
	// (strict boolean, positive cursor, clamped page size), so
	// ListAdminAlerts can never see an invalid filter. The repository
	// re-validates defensively; a failure there maps to internal error
	// without echoing anything.
	return q, httperr.Error{}
}

// parseAlertID reads and validates the {id} path segment of the resolve
// route. It must be a positive integer; anything else is invalid_request
// (a malformed id never reaches the repository).
func parseAlertID(r *http.Request) (int64, httperr.Error) {
	invalid := httperr.New(httperr.CodeInvalidRequest, "invalid alert id")
	if r == nil {
		return 0, invalid
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		return 0, invalid
	}
	return id, httperr.Error{}
}

// parseResolveBody reads the optional resolve request body. An empty body
// means resolve (true), so a bare POST is a one-step ack. A non-empty body
// must be exactly one JSON object with at most the boolean field
// "resolved" (unknown fields, trailing values, non-boolean values, and
// over-long bodies are rejected with the stable envelope).
func parseResolveBody(r *http.Request) (resolved bool, derr httperr.Error) {
	resolved = true // default: a bare POST acks/resolves
	if r == nil || r.Body == nil {
		return resolved, httperr.Error{}
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, maxResolveBodyBytes+1))
	if err != nil {
		return resolved, httperr.New(httperr.CodeInvalidRequest, "invalid request body")
	}
	if len(raw) > maxResolveBodyBytes {
		return resolved, httperr.New(httperr.CodePayloadTooLarge, "request body too large")
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		// A bare POST is a one-step ack.
		return resolved, httperr.Error{}
	}
	var request struct {
		Resolved *bool `json:"resolved"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		return resolved, httperr.New(httperr.CodeInvalidRequest, "invalid request body")
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		// A second JSON value is ambiguous and rejected.
		return resolved, httperr.New(httperr.CodeInvalidRequest, "invalid request body")
	}
	if request.Resolved != nil {
		resolved = *request.Resolved
	}
	return resolved, httperr.Error{}
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

func nowUnix() int64 {
	return time.Now().Unix()
}
