package auth

import (
	"net/http"
	"strings"
	"time"
)

const (
	UserSessionCookieName  = "nb_user_session"
	AdminSessionCookieName = "nb_admin_session"
	OAuthStateCookieName   = "nb_oauth_state"
	ElevatedCookieName     = "nb_elevated"
	userSessionCookiePath  = "/api"
	adminSessionCookiePath = "/admin"
	oauthStateCookiePath   = "/api/auth/discord"
	elevatedCookiePath     = "/"
)

func sessionCookie(name, value, path string, secure bool, maxAge int, expires time.Time) *http.Cookie {
	c := &http.Cookie{Name: name, Value: value, Path: path, HttpOnly: true, Secure: secure, SameSite: http.SameSiteLaxMode, MaxAge: maxAge}
	if !expires.IsZero() {
		c.Expires = expires.UTC()
	}
	return c
}

func setUserSessionCookie(w http.ResponseWriter, token string, expiry, now time.Time, secure bool) {
	w.Header().Set("Cache-Control", "no-store")
	http.SetCookie(w, sessionCookie(UserSessionCookieName, token, userSessionCookiePath, secure, maxAgeUntil(expiry, now), expiry))
}
func setAdminSessionCookie(w http.ResponseWriter, token string, expiry, now time.Time, secure bool) {
	w.Header().Set("Cache-Control", "no-store")
	http.SetCookie(w, sessionCookie(AdminSessionCookieName, token, adminSessionCookiePath, secure, maxAgeUntil(expiry, now), expiry))
}
func clearUserSessionCookie(w http.ResponseWriter, secure bool) {
	http.SetCookie(w, sessionCookie(UserSessionCookieName, "", userSessionCookiePath, secure, -1, time.Unix(1, 0)))
}
func clearAdminSessionCookie(w http.ResponseWriter, secure bool) {
	http.SetCookie(w, sessionCookie(AdminSessionCookieName, "", adminSessionCookiePath, secure, -1, time.Unix(1, 0)))
}
func setOAuthStateCookie(w http.ResponseWriter, state string, secure bool, ttl time.Duration, now time.Time) {
	http.SetCookie(w, sessionCookie(OAuthStateCookieName, state, oauthStateCookiePath, secure, maxAgeUntil(now.Add(ttl), now), now.Add(ttl)))
}
func clearOAuthStateCookie(w http.ResponseWriter, secure bool) {
	http.SetCookie(w, sessionCookie(OAuthStateCookieName, "", oauthStateCookiePath, secure, -1, time.Unix(1, 0)))
}

func setElevatedCookie(w http.ResponseWriter, token string, secure bool, expiry, now time.Time) {
	c := sessionCookie(ElevatedCookieName, token, elevatedCookiePath, secure, maxAgeUntil(expiry, now), expiry)
	c.HttpOnly = false
	http.SetCookie(w, c)
}

func clearElevatedCookie(w http.ResponseWriter, secure bool) {
	w.Header().Set("Cache-Control", "no-store")
	http.SetCookie(w, sessionCookie(ElevatedCookieName, "", elevatedCookiePath, secure, -1, time.Unix(1, 0)))
}

func cookieValue(r *http.Request, name string) (string, bool) {
	value, present, valid := uniqueCookieValue(r, name)
	return value, present && valid
}

func uniqueCookieValue(r *http.Request, name string) (string, bool, bool) {
	if r == nil {
		return "", false, false
	}
	found := false
	value := ""
	for _, c := range r.Cookies() {
		if c.Name != name {
			continue
		}
		if found || c.Value == "" || len(c.Value) > 4096 {
			return "", true, false
		}
		found = true
		value = c.Value
	}
	rawCount := rawCookieNameCount(r, name)
	if rawCount > 1 || (rawCount == 1 && !found) {
		return "", true, false
	}
	return value, found, true
}

func rawCookieNameCount(r *http.Request, name string) int {
	if r == nil || name == "" {
		return 0
	}
	count := 0
	for _, line := range r.Header.Values("Cookie") {
		for _, part := range strings.Split(line, ";") {
			part = strings.TrimSpace(part)
			candidate := part
			if index := strings.IndexByte(candidate, '='); index >= 0 {
				candidate = candidate[:index]
			}
			if strings.TrimSpace(candidate) == name {
				count++
			}
		}
	}
	return count
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
