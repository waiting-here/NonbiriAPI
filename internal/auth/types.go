// Package auth implements the server-side identity boundary for the user and
// administrator stations. It deliberately keeps provider credentials and
// bearer/session plaintext out of persistence and logging surfaces.
package auth

import (
	"context"
	"errors"
	"net/http"
	"time"

	"nonbiriapi/internal/db"
	"nonbiriapi/internal/httperr"
	"nonbiriapi/internal/ratelimit"
)

const (
	StationUser      = "user"
	StationAdmin     = "admin"
	OAuthIntentLogin = "login"
)

var (
	ErrStateInvalid         = errors.New("oauth state is invalid")
	ErrStateExpired         = errors.New("oauth state expired")
	ErrStateReplay          = errors.New("oauth state already used")
	ErrStateCapacity        = errors.New("oauth state capacity exhausted")
	ErrProviderUnavailable  = errors.New("identity provider unavailable")
	ErrProviderUnauthorized = errors.New("identity provider rejected authorization")
	ErrRegistrationPaused   = errors.New("registration is paused")
	ErrGuildRoleMismatch    = errors.New("identity does not satisfy registration gate")
	ErrInvalidIdentity      = errors.New("identity provider returned invalid identity")
	ErrStationMismatch      = errors.New("request station is not authorized")
)

// DiscordAuthorizeRequest contains only fixed server configuration and the
// signed state. The provider must not log or persist State.
type DiscordAuthorizeRequest struct {
	ClientID    string
	RedirectURI string
	State       string
	Intent      string
}

// DiscordIdentity is provider-supplied metadata. Values are bounded before
// they enter the users table. GuildID/RoleIDs are optional because the narrow
// provider interface can perform membership lookup lazily.
type DiscordIdentity struct {
	ID         string
	Username   string
	GlobalName string
	Avatar     string
	GuildID    string
	RoleIDs    []string
}

// DiscordLogin carries an identity and a transient membership capability. The
// HTTP implementation closes over an in-memory access token only for the
// duration of the callback; it never returns that token to callers or stores
// it. Tests and alternate providers may populate GuildID/RoleIDs instead.
type DiscordLogin struct {
	Identity     DiscordIdentity
	HasGuildRole func(context.Context, string, string) (bool, error)
}

// DiscordProvider is deliberately narrow so provider-specific credentials and
// token handling cannot leak into the core user repository.
type DiscordProvider interface {
	AuthorizationURL(context.Context, DiscordAuthorizeRequest) (string, error)
	Exchange(ctx context.Context, code, redirectURI string) (DiscordLogin, error)
}

// RegistrationGate is read at callback time from runtime site_config. Both
// values must be non-empty for a new registration.
type RegistrationGate struct {
	GuildID string
	RoleID  string
}

type RegistrationGateFunc func(context.Context) (RegistrationGate, error)

// LoginThrottle is the login-failure hook. It receives the already-derived
// client IP from httpmw.ClientIP rather than parsing headers.
type LoginThrottle interface {
	Check(identity, username string) (ratelimit.LoginDecision, error)
	Failure(identity, username string) (ratelimit.LoginDecision, error)
	Success(identity, username string) error
}

// PrincipalKind separates user sessions, admin sessions, and caller keys.
type PrincipalKind uint8

const (
	PrincipalUserSession PrincipalKind = iota + 1
	PrincipalAdminSession
	PrincipalCallerKey
)

// Principal is safe context metadata. It contains no raw session/caller token.
type Principal struct {
	User *db.User
	Kind PrincipalKind
}

type principalContextKey struct{}

func withPrincipal(ctx context.Context, principal Principal) context.Context {
	return context.WithValue(ctx, principalContextKey{}, principal)
}

// PrincipalFromContext returns an identity established by one of the auth
// middlewares. A missing value is never treated as anonymous success.
func PrincipalFromContext(ctx context.Context) (Principal, bool) {
	if ctx == nil {
		return Principal{}, false
	}
	principal, ok := ctx.Value(principalContextKey{}).(Principal)
	return principal, ok && principal.User != nil
}

// UserFromContext returns only a browser user-session principal. Keeping caller
// keys out of this default helper prevents a user-management handler from
// accidentally accepting the platform bearer credential; forwarding code must
// opt into CallerUserFromContext explicitly.
func UserFromContext(ctx context.Context) (*db.User, bool) {
	principal, ok := PrincipalFromContext(ctx)
	if !ok || principal.Kind != PrincipalUserSession {
		return nil, false
	}
	return principal.User, true
}

// CallerUserFromContext returns the user authenticated by the platform caller
// key. It is intentionally separate from UserFromContext so session-only APIs
// cannot silently accept an Authorization bearer key.
func CallerUserFromContext(ctx context.Context) (*db.User, bool) {
	principal, ok := PrincipalFromContext(ctx)
	if !ok || principal.Kind != PrincipalCallerKey {
		return nil, false
	}
	return principal.User, true
}

func AdminFromContext(ctx context.Context) (*db.User, bool) {
	principal, ok := PrincipalFromContext(ctx)
	if !ok || principal.Kind != PrincipalAdminSession || !principal.User.IsAdmin {
		return nil, false
	}
	return principal.User, true
}

// UserResponse is the bounded public user shape used by /api/session and
// /api/me. No Discord access token, session token, or caller key is included.
type UserResponse struct {
	ID            int64     `json:"id"`
	Username      string    `json:"username"`
	Avatar        string    `json:"avatar"`
	Lang          string    `json:"lang"`
	IsBanned      bool      `json:"is_banned"`
	BlockedReason string    `json:"blocked_reason,omitempty"`
	EndpointLimit *int      `json:"endpoint_limit"`
	RPMLimit      *int      `json:"rpm_limit"`
	CreatedAt     time.Time `json:"created_at"`
}

func publicUser(user *db.User) UserResponse {
	if user == nil {
		return UserResponse{}
	}
	return UserResponse{
		ID: user.ID, Username: user.Username, Avatar: user.Avatar, Lang: user.Lang,
		IsBanned: user.IsBanned, BlockedReason: user.BannedReason,
		EndpointLimit: user.EndpointLimit, RPMLimit: user.RPMLimit, CreatedAt: user.CreatedAt,
	}
}

// AdminResponse is the only admin identity shape returned to the browser.
type AdminResponse struct {
	Username string `json:"username"`
}

type adminEnvelope struct {
	Admin AdminResponse `json:"admin"`
}

type userEnvelope struct {
	User UserResponse `json:"user"`
}

// AuthError maps internal identity failures to stable HTTP errors without
// including input values. Callers should use writeAuthError instead of
// returning provider/DB error strings.
type authHTTPError struct {
	Code    string
	Status  int
	Message string
}

func requireMethod(w http.ResponseWriter, r *http.Request, method string) bool {
	if r != nil && r.Method == method {
		return true
	}
	writeStableError(w, httperr.CodeMethodNotAllowed, "method not allowed")
	return false
}
