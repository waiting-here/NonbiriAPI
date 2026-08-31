package activities

import "github.com/waiting-here/NonbiriAPI/internal/accountstream"

const (
	PoolTypeWelfare  = "welfare"
	PoolTypeThursday = "thursday"

	PoolStateOpen   = "open"
	PoolStateClosed = "closed"

	PeriodStateConfigured       = "configured"
	PeriodStateOpen             = "open"
	PeriodStateSettling         = "settling"
	PeriodStateSettled          = "settled"
	PeriodStateConfigurationErr = "configuration_error"

	UnpaidAccountBanned  = "account_banned"
	UnpaidAccountDeleted = "account_deleted"

	MasterReasonAvailable          = "available"
	MasterReasonDisabled           = "disabled"
	MasterReasonConfigurationError = "configuration_error"

	WelfareStateUnavailable        = "unavailable"
	WelfareStateAvailable          = "available"
	WelfareStateClaimed            = "claimed"
	WelfareStateIneligible         = "ineligible"
	WelfareStateEmpty              = "empty"
	WelfareStateConfigurationError = "configuration_error"

	ThursdayStateUnavailable        = "unavailable"
	ThursdayStateNotOpen            = "not_open"
	ThursdayStateOpen               = "open"
	ThursdayStateSettling           = "settling"
	ThursdayStateEnded              = "ended"
	ThursdayStateConfigurationError = "configuration_error"

	DirectionIncrease = "increase"
	DirectionDecrease = "decrease"

	// PoolDecreaseConfirmation is the exact dangerous-confirmation token.
	PoolDecreaseConfirmation = "DECREASE"

	SettlementBatchSize = 100
)

type Page[T any] struct {
	Data       []T     `json:"data"`
	NextCursor *string `json:"next_cursor"`
}

type Pool struct {
	ID        string  `json:"id"`
	PoolType  string  `json:"pool_type"`
	PeriodID  *string `json:"period_id"`
	State     string  `json:"state"`
	Revision  string  `json:"revision"`
	Balance   string  `json:"balance"`
	CreatedAt int64   `json:"created_at"`
	ClosedAt  *int64  `json:"closed_at"`
}

type PumpsBP struct {
	Platform int `json:"platform"`
	Welfare  int `json:"welfare"`
	NextPool int `json:"next_pool"`
}

type SettlementView struct {
	FrozenPool                string `json:"frozen_pool"`
	ContributionCount         string `json:"contribution_count"`
	EligibleContributionCount string `json:"eligible_contribution_count"`
	ProcessedCount            string `json:"processed_count"`
	PayoutTotal               string `json:"payout_total"`
	Rollover                  string `json:"rollover"`
}

type Period struct {
	ID            string          `json:"id"`
	PeriodKey     string          `json:"period_key"`
	State         string          `json:"state"`
	Revision      string          `json:"revision"`
	OpensAt       int64           `json:"opens_at"`
	ClosesAt      int64           `json:"closes_at"`
	Literature    string          `json:"literature"`
	Entry         string          `json:"entry"`
	PerUserLimit  int             `json:"per_user_limit"`
	PumpsBP       PumpsBP         `json:"pumps_bp"`
	CurrentPoolID string          `json:"current_pool_id"`
	NextPoolID    string          `json:"next_pool_id"`
	Settlement    *SettlementView `json:"settlement"`
	CreatedAt     int64           `json:"created_at"`
	TerminalAt    *int64          `json:"terminal_at"`
}

type AdminThursdayState struct {
	Period *Period `json:"period"`
}

type ThursdayCurrent struct {
	PeriodID      string `json:"period_id"`
	Revision      string `json:"revision"`
	OpensAt       int64  `json:"opens_at"`
	ClosesAt      int64  `json:"closes_at"`
	Literature    string `json:"literature"`
	Entry         string `json:"entry"`
	PerUserLimit  int    `json:"per_user_limit"`
	PoolBalance   string `json:"pool_balance"`
	MyCount       string `json:"my_count"`
	MyContributed string `json:"my_contributed"`
}

type ThursdayNext struct {
	PeriodID    string `json:"period_id"`
	OpensAt     int64  `json:"opens_at"`
	PoolBalance string `json:"pool_balance"`
}

type ThursdayLastResult struct {
	PeriodID      string  `json:"period_id"`
	MyCount       string  `json:"my_count"`
	MyContributed string  `json:"my_contributed"`
	Payout        string  `json:"payout"`
	UnpaidReason  *string `json:"unpaid_reason"`
}

type ThursdayView struct {
	Enabled    bool                `json:"enabled"`
	State      string              `json:"state"`
	ServerNow  int64               `json:"server_now"`
	Current    *ThursdayCurrent    `json:"current"`
	Next       *ThursdayNext       `json:"next"`
	LastResult *ThursdayLastResult `json:"last_result"`
}

type MasterView struct {
	Enabled   bool   `json:"enabled"`
	Available bool   `json:"available"`
	Reason    string `json:"reason"`
}

type WelfareView struct {
	Enabled      bool   `json:"enabled"`
	State        string `json:"state"`
	SiteDay      string `json:"site_day"`
	Threshold    string `json:"threshold"`
	Cap          string `json:"cap"`
	PoolBalance  string `json:"pool_balance"`
	ClaimedToday bool   `json:"claimed_today"`
}

type ActivitiesSnapshot struct {
	Master   MasterView   `json:"master"`
	Welfare  WelfareView  `json:"welfare"`
	Thursday ThursdayView `json:"thursday"`
}

type WelfareConfig struct {
	Enabled   bool   `json:"enabled"`
	Threshold string `json:"threshold"`
	Cap       string `json:"cap"`
}

type ThursdayConfig struct {
	Enabled bool `json:"enabled"`
}

type ActivitiesConfig struct {
	Revision      string         `json:"revision"`
	MasterEnabled bool           `json:"master_enabled"`
	Welfare       WelfareConfig  `json:"welfare"`
	Thursday      ThursdayConfig `json:"thursday"`
}

type WelfareConfigPatch struct {
	Enabled   *bool
	Threshold *string
	Cap       *string
}

type ThursdayConfigPatch struct {
	Enabled *bool
}

type ActivitiesConfigPatch struct {
	ExpectedRevision int64
	MasterEnabled    *bool
	Welfare          *WelfareConfigPatch
	Thursday         *ThursdayConfigPatch
}

type ThursdayNextMutation struct {
	ExpectedRevision int64
	PeriodKey        string
	OpensAt          int64
	Literature       string
	Entry            string
	PerUserLimit     int
	PumpsBP          PumpsBP
}

type PoolAdjustment struct {
	Direction        string
	Amount           string
	Reason           string
	ExpectedRevision int64
	Confirmation     string
}

type PoolListQuery struct {
	PoolType string
	State    string
	Cursor   string
	Limit    int
}

type WelfareClaimResult struct {
	Awarded     string `json:"awarded"`
	Balance     string `json:"balance"`
	PoolBalance string `json:"pool_balance"`
	SiteDay     string `json:"site_day"`
}

type ThursdayContributionInput struct {
	PeriodID         string
	ExpectedRevision int64
}

type ThursdayContributionResult struct {
	Count       string `json:"count"`
	Balance     string `json:"balance"`
	PoolBalance string `json:"pool_balance"`
}

type ControlMutation struct {
	IdempotencyKey string
	Method         string
	Route          string
	PathIDs        []string
	Query          string
	CanonicalBody  []byte
}

type MutationResult[T any] struct {
	Value    T
	Status   int
	Body     []byte
	Replayed bool
}

type PublishFacts struct {
	Global     bool
	AccountIDs []int64
}

func (facts PublishFacts) empty() bool {
	return !facts.Global && len(facts.AccountIDs) == 0
}

type SnapshotProjection struct {
	Snapshot ActivitiesSnapshot
	Revision string
	Data     []byte
}

func (projection SnapshotProjection) AccountstreamSnapshot() accountstream.Snapshot {
	revision := projection.Revision
	return accountstream.Snapshot{Revision: &revision, IdentityEpoch: nil, Data: append([]byte(nil), projection.Data...)}
}

type PoolDestination struct {
	PoolID    string
	PoolType  string
	AccountID int64
}

type UserExport struct {
	WelfareClaims []WelfareClaimExport        `json:"welfare_claims"`
	Thursday      []ThursdayParticipantExport `json:"thursday"`
}

type WelfareClaimExport struct {
	SiteDay   string `json:"site_day"`
	Threshold string `json:"threshold"`
	Cap       string `json:"cap"`
	Awarded   string `json:"awarded"`
	CreatedAt int64  `json:"created_at"`
}

type ThursdayParticipantExport struct {
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

type WorkerResult struct {
	Changed       bool
	More          bool
	PeriodID      string
	ProcessedRows int
}
