// Package auth owns the Generation 2 browser authentication and session
// boundary for the user and administrator stations.
package auth

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/elevation"
	"github.com/waiting-here/NonbiriAPI/internal/maintenance"
	"github.com/waiting-here/NonbiriAPI/internal/ratelimit"
)

const (
	StationUser        = "user"
	StationAdmin       = "admin"
	OAuthIntentLogin   = "login"
	OAuthIntentElevate = "elevate"

	DefaultOAuthStateTTL      = 10 * time.Minute
	DefaultSessionIdleTTL     = 7 * 24 * time.Hour
	DefaultSessionAbsoluteTTL = 30 * 24 * time.Hour
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
	ErrClosed               = errors.New("authentication runtime is closed")
	ErrFrozen               = errors.New("authentication routes are frozen")
	ErrDuplicateRoute       = errors.New("authentication route already registered")
	ErrInvalidRoute         = errors.New("authentication route is invalid")
)

type DiscordAuthorizeRequest struct {
	ClientID    string
	RedirectURI string
	State       string
	Intent      string
}

type DiscordIdentity struct {
	ID         string
	Username   string
	GlobalName string
	Avatar     string
}

type GuildMember struct {
	Nick   string
	Avatar string
	Roles  []string
}

// DiscordLogin keeps the provider access token inside the callback-owned
// closure. Neither this value nor the context actor contains that token.
type DiscordLogin struct {
	Identity    DiscordIdentity
	GuildMember func(context.Context, string) (GuildMember, error)
}

type DiscordProvider interface {
	AuthorizationURL(context.Context, DiscordAuthorizeRequest) (string, error)
	Exchange(context.Context, string, string) (DiscordLogin, error)
}

type GenerationTwoSubkeyDeriver interface {
	DeriveGenerationTwoSubkey([]byte) ([]byte, error)
}

type LoginThrottle interface {
	Check(identity, username string) (ratelimit.LoginDecision, error)
	Failure(identity, username string) (ratelimit.LoginDecision, error)
	Success(identity, username string) error
}

type MaintenanceGate interface {
	State() (maintenance.State, bool)
}

type RuntimeConfig struct {
	Store                *db.Store
	Provider             DiscordProvider
	DiscordClientID      string
	DiscordRedirectURI   string
	UserSiteBaseURL      string
	AdminUsername        string
	AdminPassword        string
	CredentialKeyDeriver GenerationTwoSubkeyDeriver
	Authorizer           *authz.Authorizer
	Maintenance          MaintenanceGate
	OAuthStates          *StateManager
	Elevation            *elevation.Manager
	AdminLoginThrottle   LoginThrottle
	Now                  func() time.Time
	SessionIdleTTL       time.Duration
	SessionAbsoluteTTL   time.Duration
}

// ActorFromContext returns the request-bound, non-secret authorization actor.
// It contains only irreversible session lookup material and the live session
// generation observed at the entry boundary.
func ActorFromContext(ctx context.Context) (authz.Actor, bool) {
	if ctx == nil {
		return authz.Actor{}, false
	}
	actor, ok := ctx.Value(actorContextKey{}).(authz.Actor)
	return actor, ok && actor.UserID > 0 && actor.SessionTokenHash != "" && actor.SessionGeneration != ""
}

type actorContextKey struct{}

func withActor(ctx context.Context, actor authz.Actor) context.Context {
	return context.WithValue(ctx, actorContextKey{}, actor)
}

// UserEnvelope is the exact Generation 2 shape shared by GET /api/session and
// GET/PATCH /api/me.
type UserEnvelope struct {
	User User `json:"user"`
}

type User struct {
	ID                        string       `json:"id"`
	Username                  string       `json:"username"`
	Avatar                    *string      `json:"avatar"`
	AvatarURL                 *string      `json:"avatar_url"`
	GuildNick                 *string      `json:"guild_nick"`
	GuildAvatarURL            *string      `json:"guild_avatar_url"`
	Lang                      string       `json:"lang"`
	IsBanned                  bool         `json:"is_banned"`
	BannedUntil               *int64       `json:"banned_until"`
	CharitySuspendedUntil     *int64       `json:"charity_suspended_until"`
	EndpointLimit             *string      `json:"endpoint_limit"`
	EffectiveEndpointLimit    string       `json:"effective_endpoint_limit"`
	RPMLimit                  *string      `json:"rpm_limit"`
	EffectiveRPMLimit         string       `json:"effective_rpm_limit"`
	ConcurrencyLimit          *string      `json:"concurrency_limit"`
	EffectiveConcurrencyLimit string       `json:"effective_concurrency_limit"`
	Balance                   string       `json:"balance"`
	DonationCredit            string       `json:"donation_credit"`
	EffectiveLevel            int          `json:"effective_level"`
	LevelDisplayName          string       `json:"level_display_name"`
	GameProfilePublic         bool         `json:"game_profile_public"`
	CreatedAt                 int64        `json:"created_at"`
	UpdatedAt                 int64        `json:"updated_at"`
	Usage                     UsageSummary `json:"usage"`
}

type UsageSummary struct {
	TotalRequests              string `json:"total_requests"`
	TotalUncachedInputTokens   string `json:"total_uncached_input_tokens"`
	TotalCacheWriteInputTokens string `json:"total_cache_write_input_tokens"`
	TotalCacheReadInputTokens  string `json:"total_cache_read_input_tokens"`
	TotalOutputTokens          string `json:"total_output_tokens"`
	TotalPromptTokens          string `json:"total_prompt_tokens"`
	TotalCompletionTokens      string `json:"total_completion_tokens"`
	TotalUnknownUsageRequests  string `json:"total_unknown_usage_requests"`
}

type AdminResponse struct {
	Username string `json:"username"`
}

type AdminEnvelope struct {
	Admin AdminResponse `json:"admin"`
}

type ElevationResponse struct {
	Token     string `json:"token"`
	ExpiresAt int64  `json:"expires_at"`
}

type AuthorizationURLResponse struct {
	AuthorizationURL string `json:"authorization_url"`
}

// AnonymousUserHandler is the registration type for routes that deliberately
// run without a user session, such as OAuth start and callback.
type AnonymousUserHandler func(http.ResponseWriter, *http.Request)

// OptionalUserPrincipal is nil for a genuinely anonymous request and present
// only after the normal user session boundary has authenticated the request.
type OptionalUserPrincipal struct {
	UserID int64
}

// OptionalUserHandler supports the small route class that accepts either an
// anonymous caller or a fully authenticated user caller.
type OptionalUserHandler func(http.ResponseWriter, *http.Request, *OptionalUserPrincipal)
