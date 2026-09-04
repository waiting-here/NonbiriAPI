package adminusers

import "github.com/waiting-here/NonbiriAPI/internal/db"

type Page[T any] struct {
	Data       []T     `json:"data"`
	NextCursor *string `json:"next_cursor"`
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

type AdminUserLevel struct {
	Manual      *int   `json:"manual"`
	Automatic   int    `json:"automatic"`
	Effective   int    `json:"effective"`
	DisplayName string `json:"display_name"`
}

type AdminUser struct {
	ID                        string         `json:"id"`
	DiscordID                 *string        `json:"discord_id"`
	Username                  string         `json:"username"`
	AvatarURL                 *string        `json:"avatar_url"`
	GuildNick                 *string        `json:"guild_nick"`
	GuildAvatarURL            *string        `json:"guild_avatar_url"`
	IsAdmin                   bool           `json:"is_admin"`
	IsBanned                  bool           `json:"is_banned"`
	BannedReason              string         `json:"banned_reason"`
	BannedUntil               *int64         `json:"banned_until"`
	CharitySuspendedUntil     *int64         `json:"charity_suspended_until"`
	EndpointLimit             *string        `json:"endpoint_limit"`
	EffectiveEndpointLimit    string         `json:"effective_endpoint_limit"`
	RPMLimit                  *string        `json:"rpm_limit"`
	EffectiveRPMLimit         string         `json:"effective_rpm_limit"`
	ConcurrencyLimit          *string        `json:"concurrency_limit"`
	EffectiveConcurrencyLimit string         `json:"effective_concurrency_limit"`
	Lang                      string         `json:"lang"`
	Balance                   string         `json:"balance"`
	DonationCredit            string         `json:"donation_credit"`
	Level                     AdminUserLevel `json:"level"`
	GameProfilePublic         bool           `json:"game_profile_public"`
	Revision                  string         `json:"revision"`
	Usage                     UsageSummary   `json:"usage"`
	CreatedAt                 int64          `json:"created_at"`
	UpdatedAt                 int64          `json:"updated_at"`
}

type AdminUserUsage struct {
	UserID string       `json:"user_id"`
	Usage  UsageSummary `json:"usage"`
}

type ActivityDay struct {
	Day                   int64   `json:"day"`
	ProductActive         bool    `json:"product_active"`
	APIRequests           string  `json:"api_requests"`
	UncachedInputTokens   string  `json:"uncached_input_tokens"`
	CacheWriteInputTokens string  `json:"cache_write_input_tokens"`
	CacheReadInputTokens  string  `json:"cache_read_input_tokens"`
	OutputTokens          string  `json:"output_tokens"`
	Checkins              string  `json:"checkins"`
	ConsoleWrites         string  `json:"console_writes"`
	GameActive            bool    `json:"game_active"`
	GameRounds            string  `json:"game_rounds"`
	DistinctProductUsers  *string `json:"distinct_product_users"`
}

type ActivityPage struct {
	Enabled    bool          `json:"enabled"`
	Data       []ActivityDay `json:"data"`
	NextCursor *string       `json:"next_cursor"`
}

type EndpointOverviewUser struct {
	UserID        string `json:"user_id"`
	EndpointCount string `json:"endpoint_count"`
	KeyCount      string `json:"key_count"`
	EnabledCount  string `json:"enabled_count"`
}

type EndpointOverview struct {
	BaseURL       string                 `json:"base_url"`
	UserCount     string                 `json:"user_count"`
	EndpointCount string                 `json:"endpoint_count"`
	KeyCount      string                 `json:"key_count"`
	Users         []EndpointOverviewUser `json:"users"`
}

type UserListQuery struct {
	IsBanned *bool
	Q        string
	Cursor   string
	Limit    int
}

type PageQuery struct {
	Cursor string
	Limit  int
}

type EndpointOverviewQuery struct {
	Q      string
	Cursor string
	Limit  int
}

type ProfileMutation struct {
	ExpectedRevision db.U128
	EndpointLimitSet bool
	EndpointLimit    *int64
	RPMLimitSet      bool
	RPMLimit         *int64
	ConcurrencySet   bool
	Concurrency      *int64
	LangSet          bool
	Lang             string
	LevelSet         bool
	Level            *int
}

type EconomyMutation struct {
	ExpectedRevision db.U128
	Target           string
	Direction        string
	AmountMilli      int64
	Reason           string
}

type BanMutation struct {
	ExpectedRevision db.U128
	Reason           string
	DurationSeconds  *int64
}

type ControlMutation struct {
	IdempotencyKey string
	Method         string
	Route          string
	PathIDs        []string
	CanonicalBody  []byte
}

type MutationResult[T any] struct {
	Value    T
	Status   int
	Body     []byte
	Replayed bool
}
