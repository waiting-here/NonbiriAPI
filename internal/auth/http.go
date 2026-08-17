package auth

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"nonbiriapi/internal/db"
	"nonbiriapi/internal/host"
	"nonbiriapi/internal/httperr"
	"nonbiriapi/internal/httpmw"
)

const (
	maxOAuthStateBytes = 4096
	maxOAuthCodeBytes  = 2048
	maxFormBodyBytes   = 16 * 1024
	maxUsernameBytes   = 256
	maxPasswordBytes   = 4096
	maxIntentBytes     = 64
)

func writeStableError(w http.ResponseWriter, code, message string) {
	httperr.WriteError(w, httperr.New(code, message))
}

func writeAuthFailure(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrProviderUnavailable), errors.Is(err, ErrRegistrationPaused):
		writeStableError(w, httperr.CodeServiceUnavailable, "authentication service unavailable")
	case errors.Is(err, ErrProviderUnauthorized), errors.Is(err, ErrGuildRoleMismatch), errors.Is(err, ErrInvalidIdentity), errors.Is(err, db.ErrBanned), errors.Is(err, ErrStateInvalid), errors.Is(err, ErrStateExpired), errors.Is(err, ErrStateReplay):
		writeStableError(w, httperr.CodeUnauthorized, "authentication failed")
	case errors.Is(err, db.ErrConflict):
		writeStableError(w, httperr.CodeConflict, "authentication conflict")
	case errors.Is(err, ErrStationMismatch):
		writeStableError(w, httperr.CodeForbidden, "station authorization required")
	default:
		writeStableError(w, httperr.CodeInternal, "authentication failed")
	}
}

func stationIs(r *http.Request, expected host.Station) bool {
	return r != nil && httpmw.StationOf(r) == expected
}

func requireStation(w http.ResponseWriter, r *http.Request, expected host.Station) bool {
	if stationIs(r, expected) {
		return true
	}
	writeStableError(w, httperr.CodeForbidden, "station authorization required")
	return false
}

func secureCookieForRequest(r *http.Request, siteBaseURL string) bool {
	// RequestIsHTTPS is set by the trusted edge context. The fixed configured
	// origin is a safe fallback for direct handler tests and does not consult
	// Host or X-Forwarded-*.
	return httpmw.RequestIsHTTPS(r) || strings.HasPrefix(strings.ToLower(strings.TrimSpace(siteBaseURL)), "https://")
}

func fixedOrigin(raw string) (string, error) {
	raw = strings.TrimRight(strings.TrimSpace(raw), "/")
	if !validateBoundedText(raw, 2048, false) {
		return "", errors.New("site origin is invalid")
	}
	u, err := url.Parse(raw)
	if err != nil || (!strings.EqualFold(u.Scheme, "http") && !strings.EqualFold(u.Scheme, "https")) || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && strings.Trim(u.Path, "/") != "") {
		return "", errors.New("site origin is invalid")
	}
	parsed, err := host.ParseConfigured(u.Host)
	if err != nil {
		return "", errors.New("site origin is invalid")
	}
	return strings.ToLower(u.Scheme) + "://" + parsed.Authority, nil
}

func validateBoundedText(value string, maxBytes int, allowEmpty bool) bool {
	if !allowEmpty && value == "" {
		return false
	}
	if len(value) > maxBytes || !utf8.ValidString(value) {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) || r == 0x7f {
			return false
		}
	}
	return true
}

func validateOAuthStateText(value string) bool {
	return validateBoundedText(value, maxOAuthStateBytes, false)
}

func validateOAuthCode(value string) bool {
	return validateBoundedText(value, maxOAuthCodeBytes, false)
}

func validateIntent(value string) bool {
	if !validateBoundedText(value, maxIntentBytes, false) {
		return false
	}
	for _, r := range value {
		if !(unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-') {
			return false
		}
	}
	return true
}

func decodeJSONBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	if r == nil || r.Body == nil {
		writeStableError(w, httperr.CodeInvalidRequest, "invalid request")
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBodyBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			writeStableError(w, httperr.CodeInvalidRequest, "invalid request")
		} else if isMaxBytesError(err) {
			writeStableError(w, httperr.CodePayloadTooLarge, "request body too large")
		} else {
			writeStableError(w, httperr.CodeInvalidRequest, "invalid request")
		}
		return false
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if isMaxBytesError(err) {
			writeStableError(w, httperr.CodePayloadTooLarge, "request body too large")
		} else {
			writeStableError(w, httperr.CodeInvalidRequest, "invalid request")
		}
		return false
	}
	return true
}

func isMaxBytesError(err error) bool {
	var maxBytesErr *http.MaxBytesError
	return errors.As(err, &maxBytesErr)
}

func singleQueryValue(r *http.Request, key string) (string, bool) {
	if r == nil || r.URL == nil || key == "" {
		return "", false
	}
	values, ok := r.URL.Query()[key]
	returnValue := ""
	if len(values) == 1 {
		returnValue = values[0]
	}
	return returnValue, ok && len(values) == 1
}

func noStoreRedirect(w http.ResponseWriter, r *http.Request, location string) {
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, location, http.StatusFound)
}

func clearAuthCookies(w http.ResponseWriter, r *http.Request, siteBaseURL string) {
	ClearOAuthStateCookie(w, secureCookieForRequest(r, siteBaseURL))
}

type methodHandler func(http.ResponseWriter, *http.Request)

func (h methodHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) { h(w, r) }

func setRetryAfter(w http.ResponseWriter, seconds int) {
	if seconds > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(seconds))
	}
}
