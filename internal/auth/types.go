// Package auth implements the server-side identity boundary for the user and
// administrator stations. It deliberately keeps provider credentials and
// bearer/session plaintext out of persistence and logging surfaces.
package auth

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/credits"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
	"github.com/waiting-here/NonbiriAPI/internal/ratelimit"
)

const (
	StationUser      = "user"
	StationAdmin     = "admin"
	OAuthIntentLogin = "login"
	// OAuthIntentElevate is the distinct OAuth intent for a fresh identity
	// re-authorization. Elevation states are session-bound and can never be
	// exchanged for login states (and vice versa).
	OAuthIntentElevate = "elevate"
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
	// ErrElevationRequired is the stable boundary failure reported when an
	// account self-service request lacks a valid, unexpired, unconsumed
	// elevated capability. It never carries the capability token.
	ErrElevationRequired = errors.New("elevated capability required")
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

// GuildMember is the server-scoped profile fetched from the registration
// guild: the member's server nickname (empty when they have none), their
// server-specific avatar hash (empty when they use the global avatar), and
// the role ids needed for the registration-gate role check. It is captured
// alongside the role check so the same authorized call also refreshes the
// displayed nickname and avatar snapshot.
type GuildMember struct {
	Nick   string
	Avatar string
	Roles  []string
}

// DiscordLogin carries an identity and a transient membership capability. The
// HTTP implementation closes over an in-memory access token only for the
// duration of the callback; it never returns that token to callers or stores
// it. Tests and alternate providers may populate GuildID/RoleIDs instead.
type DiscordLogin struct {
	Identity    DiscordIdentity
	GuildMember func(context.Context, string) (GuildMember, error)
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
// banned_until / charity_suspended_until are nullable unix seconds; an
// authenticated caller is never actively banned (a ban deletes its sessions),
// but a charity-eligibility suspension can be in force while the account
// itself remains usable.
//
// Additive level/economy projection (implementation contract §6.2): credits
// and donation_credit are canonical decimal milli-credit strings;
// effective_level is the server-authoritative resolved level for THIS request
// (read paths may lazily persist an auto-level promotion); manual_level is
// the nullable manual override (null = automatic). Level is display/state
// data only — capability decisions are made server-side per use.
type UserResponse struct {
	ID                        int64     `json:"id"`
	Username                  string    `json:"username"`
	Avatar                    string    `json:"avatar"`
	AvatarURL                 string    `json:"avatar_url"`
	GuildNick                 string    `json:"guild_nick"`
	GuildAvatarURL            string    `json:"guild_avatar_url"`
	Lang                      string    `json:"lang"`
	IsBanned                  bool      `json:"is_banned"`
	BlockedReason             string    `json:"blocked_reason,omitempty"`
	BannedUntil               *int64    `json:"banned_until"`
	CharitySuspendedUntil     *int64    `json:"charity_suspended_until"`
	EndpointLimit             *int      `json:"endpoint_limit"`
	EffectiveEndpointLimit    int       `json:"effective_endpoint_limit"`
	RPMLimit                  *int      `json:"rpm_limit"`
	EffectiveRPMLimit         int       `json:"effective_rpm_limit"`
	ConcurrencyLimit          *int      `json:"concurrency_limit"`
	EffectiveConcurrencyLimit int       `json:"effective_concurrency_limit"`
	GameProfilePublic         bool      `json:"game_profile_public"`
	Credits                   string    `json:"credits"`
	DonationCredit            string    `json:"donation_credit"`
	EffectiveLevel            int       `json:"effective_level"`
	ManualLevel               *int      `json:"manual_level"`
	CreatedAt                 time.Time `json:"created_at"`
}

// unixSecondsPtr projects a nullable deadline as a JSON number pointer.
func unixSecondsPtr(t *time.Time) *int64 {
	if t == nil {
		return nil
	}
	v := t.Unix()
	return &v
}

// publicUser projects the user row with the effective level resolved for this
// request by the authoritative resolver. effectiveLevel must come from
// db.Store.ResolveEffectiveLevel (it may have lazily persisted a promotion);
// callers never recompute a level from thresholds themselves.
func publicUser(user *db.User, effectiveLevel int, defaults db.UserLimitDefaults) UserResponse {
	if user == nil {
		return UserResponse{}
	}
	limits := db.ProjectUserLimits(user, defaults)
	return UserResponse{
		ID: user.ID, Username: user.Username, Avatar: user.Avatar, Lang: user.Lang,
		AvatarURL: discordAvatarURL(user.DiscordID, user.Avatar),
		GuildNick: user.GuildNick, GuildAvatarURL: user.GuildAvatarURL,
		IsBanned: user.IsBanned, BlockedReason: user.BannedReason,
		BannedUntil:               unixSecondsPtr(user.BannedUntil),
		CharitySuspendedUntil:     unixSecondsPtr(user.CharitySuspendedUntil),
		EndpointLimit:             limits.EndpointLimit,
		EffectiveEndpointLimit:    limits.EffectiveEndpointLimit,
		RPMLimit:                  limits.RPMLimit,
		EffectiveRPMLimit:         limits.EffectiveRPMLimit,
		ConcurrencyLimit:          limits.ConcurrencyLimit,
		EffectiveConcurrencyLimit: limits.EffectiveConcurrencyLimit,
		GameProfilePublic:         user.GameProfilePublic,
		CreatedAt:                 user.CreatedAt,
		Credits:                   credits.FormatAmount(user.Credits),
		DonationCredit:            credits.FormatAmount(user.DonationCredit),
		EffectiveLevel:            effectiveLevel,
		ManualLevel:               user.Level,
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

func requireMethod(w http.ResponseWriter, r *http.Request, method string) bool {
	if r != nil && r.Method == method {
		return true
	}
	writeStableError(w, httperr.CodeMethodNotAllowed, "method not allowed")
	return false
}
