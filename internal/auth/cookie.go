package auth

import (
	"net/http"
	"time"
)

const (
	UserSessionCookieName  = "nb_user_session"
	AdminSessionCookieName = "nb_admin_session"
	OAuthStateCookieName   = "nb_oauth_state"
	ElevatedCookieName     = "nb_elevated"
)

const (
	userSessionCookiePath  = "/api"
	adminSessionCookiePath = "/admin"
	oauthStateCookiePath   = "/api/auth/discord"
	elevatedCookiePath     = "/"
)

func sessionCookie(name, value, path string, secure bool, maxAge int, expires time.Time) *http.Cookie {
	cookie := &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     path,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   maxAge,
	}
	if !expires.IsZero() {
		cookie.Expires = expires.UTC()
	}
	return cookie
}

// SetUserSessionCookie writes the normal-user station cookie. The opaque token
// is only present in the response cookie and is never written by this package
// to logs or persistence.
func SetUserSessionCookie(w http.ResponseWriter, token string, expiry time.Time, secure bool) {
	w.Header().Set("Cache-Control", "no-store")
	maxAge := maxAgeUntil(expiry, time.Now())
	http.SetCookie(w, sessionCookie(UserSessionCookieName, token, userSessionCookiePath, secure, maxAge, expiry))
}

func SetAdminSessionCookie(w http.ResponseWriter, token string, expiry time.Time, secure bool) {
	w.Header().Set("Cache-Control", "no-store")
	maxAge := maxAgeUntil(expiry, time.Now())
	http.SetCookie(w, sessionCookie(AdminSessionCookieName, token, adminSessionCookiePath, secure, maxAge, expiry))
}

func ClearUserSessionCookie(w http.ResponseWriter, secure bool) {
	w.Header().Set("Cache-Control", "no-store")
	http.SetCookie(w, sessionCookie(UserSessionCookieName, "", userSessionCookiePath, secure, -1, time.Unix(1, 0)))
}

func ClearAdminSessionCookie(w http.ResponseWriter, secure bool) {
	w.Header().Set("Cache-Control", "no-store")
	http.SetCookie(w, sessionCookie(AdminSessionCookieName, "", adminSessionCookiePath, secure, -1, time.Unix(1, 0)))
}

// SetOAuthStateCookie binds the signed OAuth state to the initiating browser.
func SetOAuthStateCookie(w http.ResponseWriter, state string, secure bool, ttl time.Duration) {
	w.Header().Set("Cache-Control", "no-store")
	seconds := int(ttl / time.Second)
	if seconds < 1 {
		seconds = 1
	}
	expires := time.Now().Add(ttl)
	http.SetCookie(w, sessionCookie(OAuthStateCookieName, state, oauthStateCookiePath, secure, seconds, expires))
}

func ClearOAuthStateCookie(w http.ResponseWriter, secure bool) {
	w.Header().Set("Cache-Control", "no-store")
	http.SetCookie(w, sessionCookie(OAuthStateCookieName, "", oauthStateCookiePath, secure, -1, time.Unix(1, 0)))
}

// SetElevatedCookie hands the single-use elevated capability to the SPA. It is
// deliberately not HttpOnly: the SPA must move the value into the
// X-Elevated-Token header for the account self-service endpoints. The cookie
// is short-lived (the capability TTL), SameSite Lax, and never written to any
// server-side persistence or log.
func SetElevatedCookie(w http.ResponseWriter, token string, secure bool, ttl time.Duration) {
	w.Header().Set("Cache-Control", "no-store")
	seconds := int(ttl / time.Second)
	if seconds < 1 {
		seconds = 1
	}
	cookie := &http.Cookie{
		Name:     ElevatedCookieName,
		Value:    token,
		Path:     elevatedCookiePath,
		HttpOnly: false,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   seconds,
		Expires:  time.Now().Add(ttl).UTC(),
	}
	http.SetCookie(w, cookie)
}

// ClearElevatedCookie removes the browser-side elevated capability. It is
// called after any consume attempt so a single-use token never lingers.
func ClearElevatedCookie(w http.ResponseWriter, secure bool) {
	w.Header().Set("Cache-Control", "no-store")
	http.SetCookie(w, sessionCookie(ElevatedCookieName, "", elevatedCookiePath, secure, -1, time.Unix(1, 0)))
}

func UserSessionToken(r *http.Request) string {
	return cookieValue(r, UserSessionCookieName)
}

func AdminSessionToken(r *http.Request) string {
	return cookieValue(r, AdminSessionCookieName)
}

func OAuthStateFromRequest(r *http.Request) string {
	return cookieValue(r, OAuthStateCookieName)
}

func cookieValue(r *http.Request, name string) string {
	if r == nil || name == "" {
		return ""
	}
	var value string
	found := false
	for _, cookie := range r.Cookies() {
		if cookie.Name != name {
			continue
		}
		if found {
			// Duplicate same-name cookies can be produced by overlapping paths.
			// Refuse the ambiguous request instead of choosing attacker-controlled
			// ordering.
			return ""
		}
		found = true
		value = cookie.Value
	}
	if !found {
		return ""
	}
	return value
}

func maxAgeUntil(expiry, now time.Time) int {
	remaining := expiry.Sub(now)
	if remaining <= 0 {
		return 1
	}
	seconds := int(remaining / time.Second)
	if remaining%time.Second != 0 {
		seconds++
	}
	if seconds < 1 {
		return 1
	}
	return seconds
}
