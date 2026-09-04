package auth

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/waiting-here/NonbiriAPI/internal/host"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
	"github.com/waiting-here/NonbiriAPI/internal/httpmw"
)

const (
	maxOAuthStateBytes      = 4096
	maxOAuthCodeBytes       = 2048
	maxJSONBodyBytes        = 16 * 1024
	maxUsernameBytes        = 256
	maxPasswordBytes        = 4096
	maxIntentBytes          = 64
	maxStrictJSONDepth      = 32
	maxStrictJSONFields     = 256
	maxResourceIDBytes      = 512
	maxElevationHeaderBytes = 4096
	maxReturnQueryBytes     = 4 * 1024
	maxCallbackQueryBytes   = 16 * 1024
)

func writeStableError(w http.ResponseWriter, code, message string) {
	httperr.WriteError(w, httperr.New(code, message))
}

func writeAuthFailure(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrProviderUnavailable), errors.Is(err, ErrRegistrationPaused), errors.Is(err, ErrStateCapacity):
		writeStableError(w, httperr.CodeServiceUnavailable, "authentication service unavailable")
	case errors.Is(err, ErrProviderUnauthorized), errors.Is(err, ErrGuildRoleMismatch), errors.Is(err, ErrInvalidIdentity), errors.Is(err, ErrStateInvalid), errors.Is(err, ErrStateExpired), errors.Is(err, ErrStateReplay):
		writeStableError(w, httperr.CodeUnauthorized, "authentication failed")
	case errors.Is(err, ErrStationMismatch):
		writeStableError(w, httperr.CodeForbidden, "station authorization required")
	default:
		writeStableError(w, httperr.CodeInternal, "authentication failed")
	}
}

func requireStation(w http.ResponseWriter, r *http.Request, expected host.Station) bool {
	if r != nil && httpmw.StationOf(r) == expected {
		return true
	}
	writeStableError(w, httperr.CodeForbidden, "station authorization required")
	return false
}

func secureCookieForRequest(r *http.Request, siteBaseURL string) bool {
	return httpmw.RequestIsHTTPS(r) || strings.HasPrefix(strings.ToLower(siteBaseURL), "https://")
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
	if (!allowEmpty && value == "") || len(value) > maxBytes || !utf8.ValidString(value) {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) || r == 0x7f {
			return false
		}
	}
	return true
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

func validateOAuthStateText(v string) bool { return validateBoundedText(v, maxOAuthStateBytes, false) }
func validateOAuthCode(v string) bool      { return validateBoundedText(v, maxOAuthCodeBytes, false) }

func decodeJSONBody(w http.ResponseWriter, r *http.Request, dst any, strict bool) bool {
	if r == nil || r.Body == nil || !jsonContentType(r.Header.Get("Content-Type")) {
		writeStableError(w, httperr.CodeInvalidRequest, "invalid request")
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxJSONBodyBytes)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeStableError(w, httperr.CodePayloadTooLarge, "request body too large")
		} else {
			writeStableError(w, httperr.CodeInvalidRequest, "invalid request")
		}
		return false
	}
	if len(body) == 0 || !utf8.Valid(body) || (strict && scanStrictJSON(bytes.NewReader(body)) != nil) {
		writeStableError(w, httperr.CodeInvalidRequest, "invalid request")
		return false
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		writeStableError(w, httperr.CodeInvalidRequest, "invalid request")
		return false
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		writeStableError(w, httperr.CodeInvalidRequest, "invalid request")
		return false
	}
	return true
}

func jsonContentType(value string) bool {
	mediaType, _, err := mime.ParseMediaType(value)
	return err == nil && strings.EqualFold(mediaType, "application/json")
}

type strictJSONScanState struct{ fields int }

func scanStrictJSON(reader io.Reader) error {
	decoder := json.NewDecoder(reader)
	state := strictJSONScanState{}
	if err := scanStrictJSONValue(decoder, &state, 0); err != nil {
		return err
	}
	if _, err := decoder.Token(); err != io.EOF {
		if err == nil {
			return errors.New("trailing json")
		}
		return err
	}
	return nil
}

func scanStrictJSONValue(decoder *json.Decoder, state *strictJSONScanState, depth int) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	if depth >= maxStrictJSONDepth {
		return errors.New("json nesting limit")
	}
	switch delim {
	case '{':
		seen := map[string]struct{}{}
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return errors.New("object key required")
			}
			if _, exists := seen[key]; exists {
				return errors.New("duplicate json member")
			}
			seen[key] = struct{}{}
			state.fields++
			if state.fields > maxStrictJSONFields {
				return errors.New("json field limit")
			}
			if err := scanStrictJSONValue(decoder, state, depth+1); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim('}') {
			return errors.New("object terminator required")
		}
	case '[':
		for decoder.More() {
			if err := scanStrictJSONValue(decoder, state, depth+1); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim(']') {
			return errors.New("array terminator required")
		}
	default:
		return errors.New("invalid json delimiter")
	}
	return nil
}

func requireEmptyQuery(w http.ResponseWriter, r *http.Request) bool {
	if r != nil && r.URL != nil && r.URL.RawQuery == "" && !r.URL.ForceQuery {
		return true
	}
	writeStableError(w, httperr.CodeInvalidRequest, "invalid request")
	return false
}

func requireEmptyBody(w http.ResponseWriter, r *http.Request) bool {
	if r == nil || r.Body == nil {
		return true
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1)
	data, err := io.ReadAll(r.Body)
	if err != nil || len(data) != 0 {
		writeStableError(w, httperr.CodeInvalidRequest, "invalid request")
		return false
	}
	return true
}

func singleHeader(r *http.Request, name string, required bool) (string, bool) {
	values := r.Header.Values(name)
	if len(values) == 0 {
		return "", !required
	}
	if len(values) != 1 || !validateBoundedText(values[0], maxElevationHeaderBytes, false) {
		return "", false
	}
	return values[0], true
}

func noStoreRedirect(w http.ResponseWriter, r *http.Request, location string) {
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, location, http.StatusFound)
}

func setRetryAfter(w http.ResponseWriter, seconds int) {
	if seconds > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(seconds))
	}
}

func writeJSONBytes(w http.ResponseWriter, status int, body []byte) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}
