package lifecycle

import (
	"context"
	"database/sql"
)

// ExportDocument is the only value encoded by the export handler. Every
// field is explicit so a future database or domain DTO field cannot silently
// enter a personal export.
type ExportDocument struct {
	Checkins       []CheckinExport         `json:"checkins"`
	GameOnboarding []OnboardingExport      `json:"game_onboarding"`
	SchemaVersion  int                     `json:"schema_version"`
	GeneratedAt    int64                   `json:"generated_at"`
	User           UserExport              `json:"user"`
	Endpoints      []EndpointExport        `json:"endpoints"`
	CatalogPairs   []CatalogPairExport     `json:"catalog_pairs"`
	Models         []ModelExport           `json:"models"`
	CallerKey      *CallerKeyExport        `json:"caller_key"`
	Usage          UsageExport             `json:"usage"`
	LogSummary     LogSummaryExport        `json:"log_summary"`
	Issues         []IssueExport           `json:"issues"`
	CreditLedger   []LedgerEntryExport     `json:"credit_ledger"`
	WelfareClaims  []WelfareExport         `json:"welfare_claims"`
	Thursday       []ThursdayExport        `json:"thursday"`
	Donations      []DonationExport        `json:"donations"`
	Charity        CharityExport           `json:"charity"`
	Fishing        FishingExport           `json:"fishing"`
	LinkLink       LinkLinkExport          `json:"linklink"`
	RPS            RPSExport               `json:"rps"`
	Bidding        DuelExport              `json:"bidding"`
	Likes          DuelExport              `json:"likes"`
	Blackjack      BlackjackExport         `json:"blackjack"`
	Randomness     []RandomnessProofExport `json:"randomness"`
}

type UserExport struct {
	ID                        string  `json:"id"`
	Username                  string  `json:"username"`
	Avatar                    *string `json:"avatar"`
	AvatarURL                 *string `json:"avatar_url"`
	GuildNick                 *string `json:"guild_nick"`
	GuildAvatarURL            *string `json:"guild_avatar_url"`
	Lang                      string  `json:"lang"`
	IsBanned                  bool    `json:"is_banned"`
	BannedUntil               *int64  `json:"banned_until"`
	CharitySuspendedUntil     *int64  `json:"charity_suspended_until"`
	EndpointLimit             *string `json:"endpoint_limit"`
	EffectiveEndpointLimit    string  `json:"effective_endpoint_limit"`
	RPMLimit                  *string `json:"rpm_limit"`
	EffectiveRPMLimit         string  `json:"effective_rpm_limit"`
	ConcurrencyLimit          *string `json:"concurrency_limit"`
	EffectiveConcurrencyLimit string  `json:"effective_concurrency_limit"`
	GameBalance               string  `json:"game_balance"`
	Balance                   string  `json:"balance"`
	DonationCredit            string  `json:"donation_credit"`
	EffectiveLevel            int     `json:"effective_level"`
	LevelDisplayName          string  `json:"level_display_name"`
	GameProfilePublic         bool    `json:"game_profile_public"`
	CreatedAt                 int64   `json:"created_at"`
	UpdatedAt                 int64   `json:"updated_at"`
}

type UsageExport struct {
	TotalRequests              string `json:"total_requests"`
	TotalUncachedInputTokens   string `json:"total_uncached_input_tokens"`
	TotalCacheWriteInputTokens string `json:"total_cache_write_input_tokens"`
	TotalCacheReadInputTokens  string `json:"total_cache_read_input_tokens"`
	TotalOutputTokens          string `json:"total_output_tokens"`
	TotalPromptTokens          string `json:"total_prompt_tokens"`
	TotalCompletionTokens      string `json:"total_completion_tokens"`
	TotalUnknownUsageRequests  string `json:"total_unknown_usage_requests"`
}

type LogSummaryExport struct {
	TotalLogs        string `json:"total_logs"`
	LogsLast30Days   string `json:"logs_last_30_days"`
	ErrorLogs        string `json:"error_logs"`
	UsageUnknownLogs string `json:"usage_unknown_logs"`
	AverageDuration  string `json:"average_duration_ms"`
}

type EndpointExport struct {
	ID            string               `json:"id"`
	ConnectorType string               `json:"connector_type"`
	BaseURL       string               `json:"base_url"`
	Origin        EndpointOriginExport `json:"origin"`
	Note          string               `json:"note"`
	Enabled       bool                 `json:"enabled"`
	CreatedAt     int64                `json:"created_at"`
	UpdatedAt     int64                `json:"updated_at"`
	Keys          []EndpointKeyExport  `json:"keys"`
}

type EndpointOriginExport struct {
	Kind      string `json:"kind"`
	ChannelID string `json:"channel_id,omitempty"`
	Name      string `json:"name,omitempty"`
}

type EndpointKeyExport struct {
	ID              string `json:"id"`
	DisplayHead     string `json:"display_head"`
	DisplayTail     string `json:"display_tail"`
	Note            string `json:"note"`
	Enabled         bool   `json:"enabled"`
	ForceStoreFalse bool   `json:"force_store_false"`
	MaxConcurrency  int64  `json:"max_concurrency"`
	MaxRPM          int64  `json:"max_rpm"`
	SuspensionState string `json:"suspension_state"`
	CreatedAt       int64  `json:"created_at"`
	UpdatedAt       int64  `json:"updated_at"`
}

type DiscoveryEvidenceExport struct {
	State      string  `json:"state"`
	Result     *string `json:"result"`
	SafeClass  string  `json:"safe_class"`
	ObservedAt *int64  `json:"observed_at"`
	Count      *string `json:"count"`
}

type CatalogEntryExport struct {
	ID              string `json:"id"`
	SourceType      string `json:"source_type"`
	UpstreamModelID string `json:"upstream_model_id"`
	Provider        string `json:"provider"`
	CreatedAt       int64  `json:"created_at"`
	UpdatedAt       int64  `json:"updated_at"`
}

type CatalogPairExport struct {
	EndpointID       string                  `json:"endpoint_id"`
	EndpointKeyID    string                  `json:"endpoint_key_id"`
	Evidence         DiscoveryEvidenceExport `json:"evidence"`
	AutomaticEntries []CatalogEntryExport    `json:"automatic_entries"`
	ManualEntries    []CatalogEntryExport    `json:"manual_entries"`
}

type ModelExport struct {
	ID               string          `json:"id"`
	Provider         string          `json:"provider"`
	Model            string          `json:"model"`
	FullName         string          `json:"full_name"`
	RouteStrategy    string          `json:"route_strategy"`
	SilentRetry      bool            `json:"silent_retry"`
	FlattenToolCalls bool            `json:"flatten_tool_calls"`
	CreatedAt        int64           `json:"created_at"`
	UpdatedAt        int64           `json:"updated_at"`
	Bindings         []BindingExport `json:"bindings"`
}

type BindingExport struct {
	ID                     string `json:"id"`
	EndpointKeyID          string `json:"endpoint_key_id"`
	EndpointBaseURL        string `json:"endpoint_base_url"`
	ConnectorType          string `json:"connector_type"`
	EndpointNote           string `json:"endpoint_note"`
	EndpointKeyDisplayHead string `json:"endpoint_key_display_head"`
	EndpointKeyDisplayTail string `json:"endpoint_key_display_tail"`
	EndpointKeyNote        string `json:"endpoint_key_note"`
	UpstreamModelID        string `json:"upstream_model_id"`
	Ord                    int    `json:"ord"`
}

type CallerKeyExport struct {
	Display    string `json:"display"`
	Generation string `json:"generation"`
	CreatedAt  int64  `json:"created_at"`
	UpdatedAt  int64  `json:"updated_at"`
}

type IssueDeepLinkExport struct {
	RouteID    string  `json:"route_id"`
	ResourceID *string `json:"resource_id"`
}

type IssueExport struct {
	ID           string               `json:"id"`
	State        string               `json:"state"`
	Source       string               `json:"source"`
	ResourceKind string               `json:"resource_kind"`
	SummaryCode  string               `json:"summary_code"`
	SafeDetail   string               `json:"safe_detail"`
	DeepLink     *IssueDeepLinkExport `json:"deep_link"`
	FirstSeenAt  int64                `json:"first_seen_at"`
	LastSeenAt   int64                `json:"last_seen_at"`
	Count        string               `json:"count"`
	ClosedAt     *int64               `json:"closed_at"`
}

type LedgerEntryExport struct {
	Asset       string `json:"asset_type"`
	OperationID string `json:"operation_id"`
	Kind        string `json:"kind"`
	SourceType  string `json:"source_type"`
	SourceID    string `json:"source_id"`
	Delta       string `json:"delta"`
	CreatedAt   int64  `json:"created_at"`
}

type WelfareExport struct {
	Asset     string `json:"asset_type"`
	SiteDay   string `json:"site_day"`
	Threshold string `json:"threshold"`
	Cap       string `json:"cap"`
	Awarded   string `json:"awarded"`
	CreatedAt int64  `json:"created_at"`
}

type ThursdayExport struct {
	PeriodID     string  `json:"period_id"`
	PeriodKey    string  `json:"period_key"`
	Count        string  `json:"count"`
	Contributed  string  `json:"contributed"`
	Eligible     bool    `json:"eligible"`
	Settled      bool    `json:"settled"`
	Payout       string  `json:"payout"`
	UnpaidReason *string `json:"unpaid_reason"`
	CreatedAt    int64   `json:"created_at"`
	UpdatedAt    int64   `json:"updated_at"`
}

type DonationExport struct {
	ID           string                `json:"id"`
	Status       string                `json:"status"`
	Description  string                `json:"description"`
	ReviewResult *DonationReviewExport `json:"review_result"`
	Keys         []DonationKeyExport   `json:"keys"`
	CreatedAt    int64                 `json:"created_at"`
	UpdatedAt    int64                 `json:"updated_at"`
}

type DonationReviewExport struct {
	Decision   string `json:"decision"`
	Reason     string `json:"reason"`
	ReviewedAt int64  `json:"reviewed_at"`
}

type DonationKeyExport struct {
	RecurringLimits     []RecurringLimitExport   `json:"recurring_limits"`
	ID                  string                   `json:"id"`
	EndpointKeyID       *string                  `json:"endpoint_key_id"`
	DisplayHead         string                   `json:"display_head"`
	DisplayTail         string                   `json:"display_tail"`
	SafeSource          DonationSafeSourceExport `json:"safe_source"`
	PhysicalEnabled     bool                     `json:"physical_enabled"`
	CharityState        string                   `json:"charity_state"`
	Limits              DonationLimitsExport     `json:"limits"`
	Usage               DonationUsageExport      `json:"usage"`
	TokenReserve        int64                    `json:"token_reserve"`
	AuthorizedExpiresAt *int64                   `json:"authorized_expires_at"`
	ExpiresAt           *int64                   `json:"expires_at"`
	Streak              DonationStreakExport     `json:"streak"`
	EndedReason         *string                  `json:"ended_reason"`
}

type RecurringLimitExport struct {
	ID               string  `json:"id"`
	Mode             string  `json:"mode"`
	Interval         string  `json:"interval"`
	Alignment        *string `json:"alignment"`
	TimeZone         string  `json:"time_zone"`
	WeekStartsOn     *int    `json:"week_starts_on"`
	Metric           string  `json:"metric"`
	Limit            string  `json:"limit"`
	Used             string  `json:"used"`
	Reserved         string  `json:"reserved"`
	Remaining        string  `json:"remaining"`
	State            string  `json:"state"`
	PeriodStart      *int64  `json:"period_start"`
	PeriodEnd        *int64  `json:"period_end"`
	NextTransitionAt *int64  `json:"next_transition_at"`
}

type DonationSafeSourceExport struct {
	Kind          string  `json:"kind"`
	ConnectorType string  `json:"connector_type"`
	BaseURL       string  `json:"base_url"`
	ChannelID     *string `json:"channel_id,omitempty"`
	Name          *string `json:"name,omitempty"`
}

type DonationStreakExport struct {
	Generation      string `json:"generation"`
	Count           string `json:"count"`
	FailureDisabled bool   `json:"failure_disabled"`
}

type DonationLimitsExport struct {
	Price  *string `json:"price"`
	Calls  *string `json:"calls"`
	Tokens *string `json:"tokens"`
}

type DonationUsageExport struct {
	PriceUsed      string `json:"price_used"`
	PriceInflight  string `json:"price_inflight"`
	CallsUsed      string `json:"calls_used"`
	CallsInflight  string `json:"calls_inflight"`
	TokensUsed     string `json:"tokens_used"`
	TokensInflight string `json:"tokens_inflight"`
}

type CharityExport struct {
	RequestCount      string `json:"request_count"`
	OriginalCharge    string `json:"original_charge"`
	Charged           string `json:"charged"`
	Saved             string `json:"saved"`
	DonorReward       string `json:"donor_reward"`
	UncachedInput     string `json:"uncached_input_tokens"`
	CacheWriteInput   string `json:"cache_write_input_tokens"`
	CacheReadInput    string `json:"cache_read_input_tokens"`
	Output            string `json:"output_tokens"`
	UsageUnknownCount string `json:"usage_unknown_count"`
}

type FishingExport struct {
	Pending      []FishingPendingExport `json:"pending"`
	Terminal     []FishingBatchExport   `json:"terminal"`
	SingleBest   *FishingRankExport     `json:"single_best"`
	RollingTotal *FishingRankExport     `json:"rolling_total"`
	RollingBest  *FishingRankExport     `json:"rolling_best"`
}

type FishingPendingExport struct {
	RulesVersion   int               `json:"rules_version"`
	Payment        GamePaymentExport `json:"payment"`
	BatchID        string            `json:"batch_id"`
	Bait           string            `json:"bait"`
	Count          int               `json:"count"`
	EntryTotal     string            `json:"entry_total"`
	State          string            `json:"state"`
	NextAttemptAt  *int64            `json:"next_attempt_at"`
	RetryExhausted bool              `json:"retry_exhausted"`
}

type FishingBatchExport struct {
	NetPayoutTotal string                 `json:"net_payout_total"`
	Rake           FishingRakeExport      `json:"rake"`
	RulesVersion   int                    `json:"rules_version"`
	Payment        GamePaymentExport      `json:"payment"`
	BatchID        string                 `json:"batch_id"`
	Bait           string                 `json:"bait"`
	Count          int                    `json:"count"`
	UnitPrice      string                 `json:"unit_price"`
	EntryTotal     string                 `json:"entry_total"`
	Outcomes       []FishingOutcomeExport `json:"outcomes"`
	PayoutTotal    string                 `json:"payout_total"`
	SettledAt      int64                  `json:"settled_at"`
	RevealedAt     *int64                 `json:"revealed_at"`
}

type FishingRakeExport struct {
	Platform string `json:"platform"`
	Welfare  string `json:"welfare"`
	Thursday string `json:"thursday"`
}
type FishingOutcomeExport struct {
	NetReward           string            `json:"net_reward"`
	Rake                FishingRakeExport `json:"rake"`
	Ordinal             int               `json:"ordinal"`
	SpeciesKey          string            `json:"species_key"`
	Tier                string            `json:"tier"`
	SizeCM              int               `json:"size_cm"`
	Reward              string            `json:"reward"`
	BlueFatFishLengthCM *string           `json:"blue_fat_fish_length_cm"`
}

type FishingRankExport struct {
	Rank                string  `json:"rank"`
	SpeciesKey          *string `json:"species_key"`
	SizeCM              *int    `json:"size_cm"`
	TotalCredits        *string `json:"total_credits"`
	BlueFatFishLengthCM *string `json:"blue_fat_fish_length_cm,omitempty"`
}

type LinkLinkExport struct {
	Active    *LinkLinkActiveExport   `json:"active"`
	Summaries []LinkLinkSummaryExport `json:"summaries"`
}

type LinkLinkActiveExport struct {
	OpportunitiesInitial   int               `json:"opportunities_initial"`
	OpportunitiesRemaining int               `json:"opportunities_remaining"`
	RulesVersion           int               `json:"rules_version"`
	Payment                GamePaymentExport `json:"payment"`
	SessionID              string            `json:"session_id"`
	Spec                   string            `json:"spec"`
	Price                  string            `json:"price"`
	State                  string            `json:"state"`
	PairsRemoved           int               `json:"pairs_removed"`
	TotalPairs             int               `json:"total_pairs"`
	StartedAt              int64             `json:"started_at"`
	Deadline               int64             `json:"deadline"`
}

type LinkLinkSummaryExport struct {
	OpportunitiesInitial   int               `json:"opportunities_initial"`
	OpportunitiesRemaining int               `json:"opportunities_remaining"`
	RulesVersion           int               `json:"rules_version"`
	Payment                GamePaymentExport `json:"payment"`
	SessionID              string            `json:"session_id"`
	Spec                   string            `json:"spec"`
	Price                  string            `json:"price"`
	TerminalReason         string            `json:"terminal_reason"`
	StartedAt              int64             `json:"started_at"`
	Deadline               int64             `json:"deadline"`
	TerminalAt             int64             `json:"terminal_at"`
	PairsRemoved           int               `json:"pairs_removed"`
	TotalPairs             int               `json:"total_pairs"`
	Score                  *string           `json:"score"`
}

type RPSExport struct {
	Current      *RPSCurrentExport  `json:"current"`
	Pending      *RPSPendingExport  `json:"pending_result"`
	Summaries    []RPSSummaryExport `json:"summaries"`
	FunStats     *RPSFunStatsExport `json:"fun_stats"`
	TutorialSeen bool               `json:"tutorial_seen"`
}

type GamePaymentExport struct {
	General string `json:"general"`
	Game    string `json:"game"`
}

type RPSFundingExport struct {
	BuyInGeneral   string `json:"buy_in_general"`
	BuyInGame      string `json:"buy_in_game"`
	CurrentGeneral string `json:"current_general"`
	GameRemaining  string `json:"game_remaining"`
}

type RPSCurrentExport struct {
	RulesVersion int                `json:"rules_version"`
	Payment      *GamePaymentExport `json:"payment,omitempty"`
	Funding      *RPSFundingExport  `json:"funding,omitempty"`
	Kind         string             `json:"kind"`
	ResourceID   string             `json:"resource_id"`
	Mode         string             `json:"mode"`
	State        string             `json:"state"`
	Phase        *string            `json:"phase"`
	Deadline     *int64             `json:"deadline"`
}

type RPSPendingExport struct {
	OwnBuyInGeneral    *string                `json:"own_buy_in_general"`
	OwnBuyInGame       *string                `json:"own_buy_in_game"`
	OwnReturnedGeneral *string                `json:"own_returned_general"`
	RulesVersion       int                    `json:"rules_version"`
	OwnBuyIn           *string                `json:"own_buy_in"`
	OwnCashOut         *string                `json:"own_cash_out"`
	SessionID          string                 `json:"session_id"`
	Mode               string                 `json:"mode"`
	TerminalReason     string                 `json:"terminal_reason"`
	OwnSeatNo          int                    `json:"own_seat_no"`
	OwnInput           string                 `json:"own_input"`
	OwnReturned        string                 `json:"own_returned"`
	OwnWalletNet       string                 `json:"own_wallet_net"`
	Seats              []RPSPendingSeatExport `json:"seats"`
	CreatedAt          int64                  `json:"created_at"`
}

type RPSPendingSeatExport struct {
	SeatNo int    `json:"seat_no"`
	Result string `json:"result"`
}

type RPSSummaryExport struct {
	RulesVersion   int           `json:"rules_version"`
	SessionID      string        `json:"session_id"`
	Mode           string        `json:"mode"`
	TerminalReason string        `json:"terminal_reason"`
	StartedAt      int64         `json:"started_at"`
	TerminalAt     int64         `json:"terminal_at"`
	OwnSeat        RPSSeatExport `json:"own_seat"`
}

type RPSSeatExport struct {
	OwnBuyInGeneral    *string `json:"own_buy_in_general"`
	OwnBuyInGame       *string `json:"own_buy_in_game"`
	OwnReturnedGeneral *string `json:"own_returned_general"`
	OwnBuyIn           *string `json:"own_buy_in"`
	OwnCashOut         *string `json:"own_cash_out"`
	SeatNo             int     `json:"seat_no"`
	Input              string  `json:"input"`
	Returned           string  `json:"returned"`
	WalletNet          string  `json:"wallet_net"`
	TimeoutCount       string  `json:"timeout_count"`
	RockCount          string  `json:"rock_count"`
	ScissorsCount      string  `json:"scissors_count"`
	PaperCount         string  `json:"paper_count"`
}

type RPSFunStatsExport struct {
	CompletedCount  string `json:"completed_count"`
	ProfitableCount string `json:"profitable_count"`
	RockCount       string `json:"rock_count"`
	ScissorsCount   string `json:"scissors_count"`
	PaperCount      string `json:"paper_count"`
}

type ExportRequest struct {
	UserID      int64
	DecisionNow int64
	Limit       int
}

// ExportFinalizer owns process-local state derived by lazy convergence during
// export. The coordinator commits it only after the shared database snapshot
// commits and aborts it on every error or size-limit path.
type ExportFinalizer interface {
	Commit() bool
	Abort() bool
}

type IdentityExporter interface {
	ExportIdentity(context.Context, *sql.Tx, ExportRequest) (UserExport, UsageExport, LogSummaryExport, error)
}

type ResourceExporter interface {
	ExportResources(context.Context, *sql.Tx, ExportRequest) ([]EndpointExport, []CatalogPairExport, []ModelExport, *CallerKeyExport, error)
}

type IssueExporter interface {
	ExportIssues(context.Context, *sql.Tx, ExportRequest) ([]IssueExport, error)
}

type LedgerExporter interface {
	ExportLedger(context.Context, *sql.Tx, ExportRequest) ([]LedgerEntryExport, error)
}

type ActivityExporter interface {
	ExportActivities(context.Context, *sql.Tx, ExportRequest) (ActivityExport, error)
}

type DonationExporter interface {
	ExportDonations(context.Context, *sql.Tx, ExportRequest) ([]DonationExport, error)
}

type CharityExporter interface {
	ExportCharity(context.Context, *sql.Tx, ExportRequest) (CharityExport, error)
}

type FishingExporter interface {
	ExportFishing(context.Context, *sql.Tx, ExportRequest) (FishingExport, ExportFinalizer, error)
}

type LinkLinkExporter interface {
	ExportLinkLink(context.Context, *sql.Tx, ExportRequest) (LinkLinkExport, ExportFinalizer, error)
}

type RPSExporter interface {
	ExportRPS(context.Context, *sql.Tx, ExportRequest) (RPSExport, ExportFinalizer, error)
}

type CheckinExport struct {
	Asset     string `json:"asset_type"`
	SiteDay   string `json:"site_day"`
	Award     string `json:"award"`
	CreatedAt int64  `json:"created_at"`
}

type OnboardingExport struct {
	GameKey     string `json:"game_key"`
	TaskKey     string `json:"task_key"`
	Award       string `json:"award"`
	CompletedAt int64  `json:"completed_at"`
}

type ActivityExport struct {
	Checkins       []CheckinExport
	GameOnboarding []OnboardingExport
	WelfareClaims  []WelfareExport
	Thursday       []ThursdayExport
}
