package forward

import (
	"net/http"
	"strings"

	"github.com/waiting-here/NonbiriAPI/internal/httperr"
)

// BrowserCORS enables browser clients only on the exact public CallerKey
// routes. Mount it after the station Host boundary and before maintenance,
// authentication and admission, so a preflight never consumes caller resources.
// Actual requests still traverse every downstream authorization check.
func BrowserCORS(next http.Handler) http.Handler {
	if next == nil {
		next = http.NotFoundHandler()
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method := exactIngressMethod(r.URL.Path, r.URL.EscapedPath())
		if method == "" {
			next.ServeHTTP(w, r)
			return
		}
		// A constant wildcard permits explicitly supplied Bearer keys, never
		// browser credentials mode "include". Do not reflect Origin or enable
		// Allow-Credentials: the session APIs have a separate same-origin policy.
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Expose-Headers", "Retry-After, X-Request-ID")
		if r.Method != http.MethodOptions || len(r.Header.Values("Access-Control-Request-Method")) == 0 {
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Add("Vary", "Origin, Access-Control-Request-Method, Access-Control-Request-Headers")
		origin, originOK := boundedCORSHeader(r, "Origin", 2048)
		requestedMethod, methodOK := boundedCORSHeader(r, "Access-Control-Request-Method", 128)
		headers, headersOK := corsRequestHeaders(r)
		if !originOK || origin == "" || !methodOK || requestedMethod == "" || !headersOK || r.URL.RawQuery != "" || r.URL.ForceQuery {
			writeFailure(w, platformFailure(httperr.CodeInvalidRequest, "invalid CORS preflight"))
			return
		}
		if requestedMethod != method {
			writeFailure(w, platformFailure(httperr.CodeMethodNotAllowed, "method not allowed"))
			return
		}
		w.Header().Set("Access-Control-Allow-Methods", method)
		if headers != "" {
			w.Header().Set("Access-Control-Allow-Headers", headers)
		}
		w.Header().Set("Access-Control-Max-Age", "600")
		w.WriteHeader(http.StatusNoContent)
	})
}

func boundedCORSHeader(r *http.Request, name string, maxBytes int) (string, bool) {
	values := r.Header.Values(name)
	if len(values) != 1 || len(values[0]) > maxBytes || strings.ContainsAny(values[0], "\x00\r\n") {
		return "", false
	}
	value := strings.TrimSpace(values[0])
	return value, value == values[0]
}

func corsRequestHeaders(r *http.Request) (string, bool) {
	if len(r.Header.Values("Access-Control-Request-Headers")) == 0 {
		return "", true
	}
	raw, ok := boundedCORSHeader(r, "Access-Control-Request-Headers", 4096)
	if !ok {
		return "", false
	}
	parts := strings.Split(raw, ",")
	if len(parts) > 64 {
		return "", false
	}
	for i, part := range parts {
		name := strings.Trim(part, " \t")
		if len(name) == 0 || len(name) > 128 {
			return "", false
		}
		for _, ch := range name {
			if !(ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || strings.ContainsRune("!#$%&'*+-.^_`|~", ch)) {
				return "", false
			}
		}
		// Only validated field names are returned, never values (including the
		// CallerKey). Explicit names also support SDK metadata and Authorization,
		// which browsers do not cover with an Allow-Headers wildcard.
		parts[i] = strings.ToLower(name)
	}
	return strings.Join(parts, ", "), true
}
