// Package rps implements the authoritative three-player rock-paper-scissors
// runtime. Public values in this file mirror the closed beta.1 wire contract;
// persistence and reducer-only values live in the other package files.
package rps

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
)

const (
	RouteState       = "/api/games/rps/state"
	RouteQueue       = "/api/games/rps/queue"
	RouteQueueItem   = "/api/games/rps/queue/{id}"
	RouteActions     = "/api/games/rps/sessions/{id}/actions"
	RouteLease       = "/api/games/rps/sessions/{id}/lease"
	RoutePendingACK  = "/api/games/rps/pending-result/ack"
	RouteLeaderboard = "/api/games/rps/leaderboard"
	RouteTutorial    = "/api/games/rps/tutorial/seen"

	ContinuationKind = "rps_session"

	ActionRead       = "read"
	ActionPlay       = "action"
	ActionLease      = "lease"
	ActionPendingACK = "pending_ack"
	ActionDeadline   = "deadline"
	ActionTerminal   = "terminal"

	PhaseGesture         = "gesture"
	PhaseDealerRaise     = "dealer_raise"
	PhaseFollowers       = "followers"
	PhasePaidPoolGesture = "paid_pool_gesture"
	PhaseFreePoolGesture = "free_pool_gesture"
	PhaseUltimateGesture = "ultimate_gesture"
	PhaseTerminal        = "terminal_processing"

	StateStarted            = "started"
	StateTerminalProcessing = "terminal_processing"

	GestureRock     = "rock"
	GestureScissors = "scissors"
	GesturePaper    = "paper"

	FollowerCall      = "call"
	FollowerSurrender = "surrender"

	TerminalQuickResolved              = "quick_resolved"
	TerminalStandardRoundLimit         = "standard_round_limit"
	TerminalStandardInsufficient       = "standard_insufficient_balance"
	TerminalDeathmatchExhausted        = "deathmatch_balance_exhausted"
	TerminalUltimateResolved           = "ultimate_resolved"
	TerminalFreeTieLimit               = "free_tie_limit"
	RevealThreeEqual                   = "three_equal"
	RevealAllDistinct                  = "all_distinct"
	RevealOneBeatsTwo                  = "one_beats_two"
	RevealTwoBeatOne                   = "two_beat_one"
	RevealOneSurrenderTie              = "one_surrender_tie"
	RevealOneSurrenderDecided          = "one_surrender_decided"
	RevealTwoSurrenders                = "two_surrenders"
	EventPhaseChanged                  = "phase_changed"
	EventActionLocked                  = "action_locked"
	EventReveal                        = "reveal"
	EventSettlement                    = "settlement"
	EventReminder                      = "reminder"
	EventIdentityReset                 = "identity_reset"
	EventTerminal                      = "terminal"
	maxProjectedStateBytes             = 64 * 1024
	maxRecentEvents                    = 64
	summaryWindowSeconds         int64 = 30 * 24 * 60 * 60
	leaseTTLSeconds              int64 = 15
)

var (
	ErrInvalidRequest      = errors.New("rps: invalid request")
	ErrUnauthorized        = errors.New("rps: unauthorized")
	ErrForbidden           = errors.New("rps: forbidden")
	ErrNotFound            = errors.New("rps: not found")
	ErrConflict            = errors.New("rps: conflict")
	ErrRateLimited         = errors.New("rps: rate limited")
	ErrFeatureDisabled     = errors.New("rps: feature disabled")
	ErrInsufficientCredits = errors.New("rps: insufficient credits")
	ErrMaintenance         = errors.New("rps: maintenance")
	ErrServiceUnavailable  = errors.New("rps: service unavailable")
	ErrResourceLimit       = errors.New("rps: resource limit")
	ErrInvariant           = errors.New("rps: invariant violation")
	ErrClosed              = errors.New("rps: closed")
)

type Queue struct {
	ID        string `json:"id"`
	Mode      string `json:"mode"`
	State     string `json:"state"`
	Revision  string `json:"revision"`
	Deadline  int64  `json:"deadline"`
	ServerNow int64  `json:"server_now"`
}

type RuleSnapshot struct {
	RulesVersion       int     `json:"rules_version"`
	Base               string  `json:"base"`
	PumpsBP            PumpsBP `json:"pumps_bp"`
	GestureSeconds     int     `json:"gesture_seconds"`
	DealerSeconds      int     `json:"dealer_seconds"`
	FollowerSeconds    int     `json:"follower_seconds"`
	StandardMultiplier int     `json:"standard_multiplier"`
	FreeTieReminder    int     `json:"free_tie_reminder"`
	FreeTieLimit       int     `json:"free_tie_limit"`
}

type PumpsBP struct {
	Platform int `json:"platform"`
	Welfare  int `json:"welfare"`
	Thursday int `json:"thursday"`
}

type Cuts struct {
	Platform string `json:"platform"`
	Welfare  string `json:"welfare"`
	Thursday string `json:"thursday"`
}

type Economy struct {
	PlayerPool            string  `json:"player_pool"`
	PermanentMultiplier   string  `json:"permanent_multiplier"`
	PoolBaseMultiplier    *string `json:"pool_base_multiplier"`
	CurrentPlanMultiplier *string `json:"current_plan_multiplier"`
	DealerRaise           *string `json:"dealer_raise"`
	Cuts                  Cuts    `json:"cuts"`
	WelfareCarry          string  `json:"welfare_carry"`
}

type FunSnapshot struct {
	State           string `json:"state"`
	CompletedCount  string `json:"completed_count,omitempty"`
	ProfitableCount string `json:"profitable_count,omitempty"`
	RockCount       string `json:"rock_count,omitempty"`
	ScissorsCount   string `json:"scissors_count,omitempty"`
	PaperCount      string `json:"paper_count,omitempty"`
}

// Seat is marshalled manually because active and deidentified seats are a
// strict union. In particular, identity, fun and hidden-action-related keys
// never appear on a deleted seat, even as null placeholders.
type Seat struct {
	SeatNo            int
	Viewer            string
	DeletionState     string
	DisplayName       string
	AvatarURL         *string
	StartingBalance   string
	CurrentBalance    string
	CurrentRoundInput string
	CurrentAllIn      bool
	TotalInput        string
	TotalReturned     string
	TimeoutCount      string
	FunSnapshot       FunSnapshot
	VisibleGesture    *string
	FollowerAction    *string
	TerminalReturn    *string
	WalletNet         *string
}

func (seat *Seat) UnmarshalJSON(body []byte) error {
	if seat == nil || rpsValidateStrictResponseJSON(body) != nil {
		return ErrInvariant
	}
	var discriminator struct {
		Viewer        string `json:"viewer"`
		DeletionState string `json:"deletion_state"`
	}
	if json.Unmarshal(body, &discriminator) != nil {
		return ErrInvariant
	}
	type activeSeat struct {
		SeatNo            int         `json:"seat_no"`
		Viewer            string      `json:"viewer"`
		DeletionState     string      `json:"deletion_state"`
		DisplayName       string      `json:"display_name"`
		AvatarURL         *string     `json:"avatar_url"`
		StartingBalance   string      `json:"starting_balance"`
		CurrentBalance    string      `json:"current_balance"`
		CurrentRoundInput string      `json:"current_round_input"`
		CurrentAllIn      bool        `json:"current_all_in"`
		TotalInput        string      `json:"total_input"`
		TotalReturned     string      `json:"total_returned"`
		TimeoutCount      string      `json:"timeout_count"`
		FunSnapshot       FunSnapshot `json:"fun_snapshot"`
		VisibleGesture    *string     `json:"visible_gesture,omitempty"`
		FollowerAction    *string     `json:"follower_action,omitempty"`
		TerminalReturn    *string     `json:"terminal_return,omitempty"`
		WalletNet         *string     `json:"wallet_net,omitempty"`
	}
	type deletedSeat struct {
		SeatNo            int     `json:"seat_no"`
		Viewer            string  `json:"viewer"`
		DeletionState     string  `json:"deletion_state"`
		StartingBalance   string  `json:"starting_balance"`
		CurrentBalance    string  `json:"current_balance"`
		CurrentRoundInput string  `json:"current_round_input"`
		CurrentAllIn      bool    `json:"current_all_in"`
		TotalInput        string  `json:"total_input"`
		TotalReturned     string  `json:"total_returned"`
		TimeoutCount      string  `json:"timeout_count"`
		TerminalReturn    *string `json:"terminal_return,omitempty"`
		WalletNet         *string `json:"wallet_net,omitempty"`
	}
	var decoded Seat
	if discriminator.DeletionState == "active" {
		var value activeSeat
		if !rpsDecodeStrictResponseBytes(body, &value) {
			return ErrInvariant
		}
		decoded = Seat{
			SeatNo: value.SeatNo, Viewer: value.Viewer, DeletionState: value.DeletionState,
			DisplayName: value.DisplayName, AvatarURL: value.AvatarURL,
			StartingBalance: value.StartingBalance, CurrentBalance: value.CurrentBalance,
			CurrentRoundInput: value.CurrentRoundInput, CurrentAllIn: value.CurrentAllIn,
			TotalInput: value.TotalInput, TotalReturned: value.TotalReturned, TimeoutCount: value.TimeoutCount,
			FunSnapshot: value.FunSnapshot, VisibleGesture: value.VisibleGesture, FollowerAction: value.FollowerAction,
			TerminalReturn: value.TerminalReturn, WalletNet: value.WalletNet,
		}
	} else {
		var value deletedSeat
		if !rpsDecodeStrictResponseBytes(body, &value) {
			return ErrInvariant
		}
		decoded = Seat{
			SeatNo: value.SeatNo, Viewer: value.Viewer, DeletionState: value.DeletionState,
			StartingBalance: value.StartingBalance, CurrentBalance: value.CurrentBalance,
			CurrentRoundInput: value.CurrentRoundInput, CurrentAllIn: value.CurrentAllIn,
			TotalInput: value.TotalInput, TotalReturned: value.TotalReturned, TimeoutCount: value.TimeoutCount,
			TerminalReturn: value.TerminalReturn, WalletNet: value.WalletNet,
		}
	}
	canonical, err := json.Marshal(decoded)
	if err != nil || !bytes.Equal(canonical, body) {
		return ErrInvariant
	}
	*seat = decoded
	return nil
}

func (seat Seat) MarshalJSON() ([]byte, error) {
	base := struct {
		SeatNo            int     `json:"seat_no"`
		Viewer            string  `json:"viewer"`
		DeletionState     string  `json:"deletion_state"`
		StartingBalance   string  `json:"starting_balance"`
		CurrentBalance    string  `json:"current_balance"`
		CurrentRoundInput string  `json:"current_round_input"`
		CurrentAllIn      bool    `json:"current_all_in"`
		TotalInput        string  `json:"total_input"`
		TotalReturned     string  `json:"total_returned"`
		TimeoutCount      string  `json:"timeout_count"`
		TerminalReturn    *string `json:"terminal_return,omitempty"`
		WalletNet         *string `json:"wallet_net,omitempty"`
	}{seat.SeatNo, seat.Viewer, seat.DeletionState, seat.StartingBalance, seat.CurrentBalance,
		seat.CurrentRoundInput, seat.CurrentAllIn, seat.TotalInput, seat.TotalReturned,
		seat.TimeoutCount, seat.TerminalReturn, seat.WalletNet}
	if seat.DeletionState != "active" {
		if seat.DeletionState != "deletion_pending" && seat.DeletionState != "deidentified" || seat.Viewer != "opponent" {
			return nil, ErrInvariant
		}
		return json.Marshal(base)
	}
	if seat.Viewer != "self" && seat.Viewer != "opponent" || seat.DisplayName == "" {
		return nil, ErrInvariant
	}
	type activeSeat struct {
		SeatNo            int         `json:"seat_no"`
		Viewer            string      `json:"viewer"`
		DeletionState     string      `json:"deletion_state"`
		DisplayName       string      `json:"display_name"`
		AvatarURL         *string     `json:"avatar_url"`
		StartingBalance   string      `json:"starting_balance"`
		CurrentBalance    string      `json:"current_balance"`
		CurrentRoundInput string      `json:"current_round_input"`
		CurrentAllIn      bool        `json:"current_all_in"`
		TotalInput        string      `json:"total_input"`
		TotalReturned     string      `json:"total_returned"`
		TimeoutCount      string      `json:"timeout_count"`
		FunSnapshot       FunSnapshot `json:"fun_snapshot"`
		VisibleGesture    *string     `json:"visible_gesture,omitempty"`
		FollowerAction    *string     `json:"follower_action,omitempty"`
		TerminalReturn    *string     `json:"terminal_return,omitempty"`
		WalletNet         *string     `json:"wallet_net,omitempty"`
	}
	return json.Marshal(activeSeat{
		seat.SeatNo, seat.Viewer, seat.DeletionState, seat.DisplayName, seat.AvatarURL,
		seat.StartingBalance, seat.CurrentBalance, seat.CurrentRoundInput, seat.CurrentAllIn,
		seat.TotalInput, seat.TotalReturned, seat.TimeoutCount, seat.FunSnapshot,
		seat.VisibleGesture, seat.FollowerAction, seat.TerminalReturn, seat.WalletNet,
	})
}

type RoundSummary struct {
	BaseRoundCount   string  `json:"base_round_count"`
	PaidTieCount     string  `json:"paid_tie_count"`
	FreeTieCount     string  `json:"free_tie_count"`
	PaidPoolStreak   string  `json:"paid_pool_streak"`
	FreePoolStreak   string  `json:"free_pool_streak"`
	ReminderActive   bool    `json:"reminder_active"`
	LastRevealResult *string `json:"last_reveal_result"`
}

type RecentEvent struct {
	Seq           string          `json:"seq"`
	IdentityEpoch string          `json:"identity_epoch"`
	Kind          string          `json:"kind"`
	PhaseSeq      string          `json:"phase_seq"`
	SafePayload   json.RawMessage `json:"safe_payload"`
}

type State struct {
	SessionID           string        `json:"session_id"`
	Mode                string        `json:"mode"`
	State               string        `json:"state"`
	Phase               string        `json:"phase"`
	PhaseSeq            string        `json:"phase_seq"`
	Revision            string        `json:"revision"`
	IdentityEpoch       string        `json:"identity_epoch"`
	ServerNow           int64         `json:"server_now"`
	Deadline            *int64        `json:"deadline"`
	RuleSnapshot        RuleSnapshot  `json:"rule_snapshot"`
	Economy             Economy       `json:"economy"`
	Seats               []Seat        `json:"seats"`
	CurrentActorOptions []string      `json:"current_actor_options"`
	RoundSummary        RoundSummary  `json:"round_summary"`
	RecentEvents        []RecentEvent `json:"recent_events"`
	EventsTruncated     bool          `json:"events_truncated"`
	FirstAvailableSeq   string        `json:"first_available_seq"`
}

type PendingSeat struct {
	SeatNo int    `json:"seat_no"`
	Result string `json:"result"`
}

type PendingResult struct {
	SessionID      string        `json:"session_id"`
	Mode           string        `json:"mode"`
	TerminalReason string        `json:"terminal_reason"`
	OwnSeatNo      int           `json:"own_seat_no"`
	OwnInput       string        `json:"own_input"`
	OwnReturned    string        `json:"own_returned"`
	OwnWalletNet   string        `json:"own_wallet_net"`
	Seats          []PendingSeat `json:"seats"`
	CreatedAt      int64         `json:"created_at"`
}

type ModeConfig struct {
	Enabled         bool    `json:"enabled"`
	Base            string  `json:"base"`
	PumpsBP         PumpsBP `json:"pumps_bp"`
	QueueSeconds    int     `json:"queue_seconds"`
	GestureSeconds  int     `json:"gesture_seconds"`
	DealerSeconds   int     `json:"dealer_seconds"`
	FollowerSeconds int     `json:"follower_seconds"`
	QueueCapacity   int     `json:"queue_capacity"`
}

// HomeState is the strict top-level union. The branch fields are exported for
// callers and tests but MarshalJSON emits only the selected branch.
type HomeState struct {
	Kind         string
	TutorialSeen bool
	Modes        map[string]ModeConfig
	Queue        *Queue
	Session      *State
	Result       *PendingResult
}

func (state HomeState) MarshalJSON() ([]byte, error) {
	switch state.Kind {
	case "idle":
		if len(state.Modes) != 3 || state.Queue != nil || state.Session != nil || state.Result != nil {
			return nil, ErrInvariant
		}
		return json.Marshal(struct {
			Kind         string                `json:"kind"`
			TutorialSeen bool                  `json:"tutorial_seen"`
			Modes        map[string]ModeConfig `json:"modes"`
		}{state.Kind, state.TutorialSeen, state.Modes})
	case "queue":
		if state.Queue == nil || state.Session != nil || state.Result != nil {
			return nil, ErrInvariant
		}
		return json.Marshal(struct {
			Kind  string `json:"kind"`
			Queue Queue  `json:"queue"`
		}{state.Kind, *state.Queue})
	case "session":
		if state.Session == nil || state.Queue != nil || state.Result != nil {
			return nil, ErrInvariant
		}
		return json.Marshal(struct {
			Kind    string `json:"kind"`
			Session State  `json:"session"`
		}{state.Kind, *state.Session})
	case "pending_result":
		if state.Result == nil || state.Queue != nil || state.Session != nil {
			return nil, ErrInvariant
		}
		return json.Marshal(struct {
			Kind   string        `json:"kind"`
			Result PendingResult `json:"result"`
		}{state.Kind, *state.Result})
	default:
		return nil, ErrInvariant
	}
}

type Identity struct {
	Kind        string
	DisplayName string
	AvatarURL   *string
}

func (identity Identity) MarshalJSON() ([]byte, error) {
	if identity.Kind == "anonymous" {
		return json.Marshal(struct {
			Kind string `json:"kind"`
		}{identity.Kind})
	}
	if identity.Kind != "public" || identity.DisplayName == "" {
		return nil, ErrInvariant
	}
	return json.Marshal(struct {
		Kind        string  `json:"kind"`
		DisplayName string  `json:"display_name"`
		AvatarURL   *string `json:"avatar_url"`
	}{identity.Kind, identity.DisplayName, identity.AvatarURL})
}

type LeaderboardRow struct {
	Board           string
	Rank            string
	Identity        Identity
	SessionCount    string
	ProfitableCount string
	ProfitRate      string
	NetProfit       string
	IsMe            bool
}

func (row LeaderboardRow) MarshalJSON() ([]byte, error) {
	switch row.Board {
	case "profit_rate":
		return json.Marshal(struct {
			Rank            string   `json:"rank"`
			Identity        Identity `json:"identity"`
			SessionCount    string   `json:"session_count"`
			ProfitableCount string   `json:"profitable_count"`
			ProfitRate      string   `json:"profit_rate"`
			IsMe            bool     `json:"is_me"`
		}{row.Rank, row.Identity, row.SessionCount, row.ProfitableCount, row.ProfitRate, row.IsMe})
	case "net_profit":
		return json.Marshal(struct {
			Rank         string   `json:"rank"`
			Identity     Identity `json:"identity"`
			SessionCount string   `json:"session_count"`
			NetProfit    string   `json:"net_profit"`
			IsMe         bool     `json:"is_me"`
		}{row.Rank, row.Identity, row.SessionCount, row.NetProfit, row.IsMe})
	default:
		return nil, ErrInvariant
	}
}

type Leaderboard struct {
	Mode        string           `json:"mode"`
	Board       string           `json:"board"`
	WindowDays  int              `json:"window_days"`
	WindowStart int64            `json:"window_start"`
	MinSessions int              `json:"min_sessions"`
	Rows        []LeaderboardRow `json:"rows"`
	Me          *LeaderboardRow  `json:"me"`
}

type LeaseResult struct {
	ExpiresAt int64 `json:"expires_at"`
}

type EnqueueInput struct {
	UserID              int64
	Mode                string
	DeviceToken         string
	CanonicalSourceIP   [16]byte
	DeathmatchConfirmed bool
	IdempotencyKey      string
}

type CancelInput struct {
	UserID           int64
	QueueID          string
	ExpectedRevision string
	IdempotencyKey   string
}

type ActionInput struct {
	UserID           int64
	SessionBinding   string
	SessionID        string
	PhaseSeq         string
	ExpectedRevision string
	Action           string
	Gesture          string
	DealerDecision   string
	RaiseAmount      string
	FollowerDecision string
	IdempotencyKey   string
}

type ReadInput struct {
	UserID         int64
	SessionBinding string
}

type LeaseInput struct {
	UserID         int64
	SessionBinding string
	SessionID      string
	LeaseID        string
}

type ACKInput struct {
	UserID         int64
	SessionBinding string
	SessionID      string
}

type MutationResult struct {
	State            HomeState
	HTTPStatus       int
	IdempotentReplay bool
}

func (result MutationResult) valid() bool {
	return result.HTTPStatus >= http.StatusOK && result.HTTPStatus <= 299 && result.State.Kind != ""
}

type QueueMutationResult struct {
	Queue            Queue
	HTTPStatus       int
	IdempotentReplay bool
}

func (result QueueMutationResult) valid() bool {
	return result.HTTPStatus == http.StatusAccepted && result.Queue.ID != ""
}

type EmptyMutationResult struct {
	HTTPStatus       int
	IdempotentReplay bool
}

type ActiveCount struct {
	Mode  string `json:"mode"`
	Phase string `json:"phase"`
	Count string `json:"count"`
}

type QueueCount struct {
	Mode  string `json:"mode"`
	Count string `json:"count"`
}

type ActiveCounts struct {
	Sessions []ActiveCount `json:"sessions"`
	Queues   []QueueCount  `json:"queues"`
}

type HomeContinueItem struct {
	Game       string `json:"game"`
	ResourceID string `json:"resource_id"`
	State      string `json:"state"`
	RouteID    string `json:"route_id"`
}

type HomePendingItem struct {
	Game       string `json:"game"`
	ResourceID string `json:"resource_id"`
	CreatedAt  int64  `json:"created_at"`
	RouteID    string `json:"route_id"`
}

type HomeSummary struct {
	Continue       []HomeContinueItem `json:"continue"`
	PendingResults []HomePendingItem  `json:"pending_results"`
}

type SummarySeatExport struct {
	SeatNo        int    `json:"seat_no"`
	Input         string `json:"input"`
	Returned      string `json:"returned"`
	WalletNet     string `json:"wallet_net"`
	TimeoutCount  string `json:"timeout_count"`
	RockCount     string `json:"rock_count"`
	ScissorsCount string `json:"scissors_count"`
	PaperCount    string `json:"paper_count"`
}

type SummaryExport struct {
	SessionID      string            `json:"session_id"`
	Mode           string            `json:"mode"`
	TerminalReason string            `json:"terminal_reason"`
	StartedAt      int64             `json:"started_at"`
	TerminalAt     int64             `json:"terminal_at"`
	OwnSeat        SummarySeatExport `json:"own_seat"`
}

type FunStatsExport struct {
	CompletedCount  string `json:"completed_count"`
	ProfitableCount string `json:"profitable_count"`
	RockCount       string `json:"rock_count"`
	ScissorsCount   string `json:"scissors_count"`
	PaperCount      string `json:"paper_count"`
}

type UserExport struct {
	Current      *HomeState      `json:"current"`
	Pending      *PendingResult  `json:"pending_result"`
	Summaries    []SummaryExport `json:"summaries"`
	FunStats     *FunStatsExport `json:"fun_stats"`
	TutorialSeen bool            `json:"tutorial_seen"`
}
