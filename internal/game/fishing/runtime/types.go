// Package runtime owns the persistent Fishing state machine and the
// HTTP/adaptor seams composed by the application root.
package runtime

import (
	"encoding/json"
	"errors"

	"github.com/waiting-here/NonbiriAPI/internal/game"
	"github.com/waiting-here/NonbiriAPI/internal/game/fishing"
)

const (
	RouteFishingBatches     = "/api/games/fishing/batches"
	RouteFishingState       = "/api/games/fishing/state"
	RouteFishingACK         = "/api/games/fishing/batches/{id}/ack"
	RouteFishingRecover     = "/api/games/fishing/batches/{id}/recover"
	RouteFishingLeaderboard = "/api/games/fishing/leaderboard"

	PendingStateSettlement = "settlement_pending"
	PendingStateRecovery   = "recovery_required"
)

var (
	ErrInvalidRequest      = errors.New("game runtime: invalid request")
	ErrUnauthorized        = errors.New("game runtime: unauthorized")
	ErrForbidden           = errors.New("game runtime: forbidden")
	ErrNotFound            = errors.New("game runtime: not found")
	ErrConflict            = errors.New("game runtime: conflict")
	ErrRateLimited         = errors.New("game runtime: rate limited")
	ErrFeatureDisabled     = errors.New("game runtime: feature disabled")
	ErrInsufficientCredits = errors.New("game runtime: insufficient credits")
	ErrMaintenance         = errors.New("game runtime: maintenance")
	ErrServiceUnavailable  = errors.New("game runtime: service unavailable")
	ErrCapacity            = errors.New("game runtime: capacity exhausted")
	ErrInvariant           = errors.New("game runtime: invariant violation")
	ErrClosed              = errors.New("game runtime: closed")
)

type FishingRake struct {
	Platform string `json:"platform"`
	Welfare  string `json:"welfare"`
	Thursday string `json:"thursday"`
}

func rakeFromMilli(platform, welfare, thursday int64) FishingRake {
	return FishingRake{game.FormatAmount(platform), game.FormatAmount(welfare), game.FormatAmount(thursday)}
}

type FishingOutcome struct {
	NetReward           string      `json:"net_reward"`
	Rake                FishingRake `json:"rake"`
	Ordinal             int         `json:"ordinal"`
	SpeciesKey          string      `json:"species_key"`
	Tier                string      `json:"tier"`
	SizeCM              int         `json:"size_cm"`
	Reward              string      `json:"reward"`
	BlueFatFishLengthCM *string     `json:"blue_fat_fish_length_cm"`
}

type FishingBatchResult struct {
	NetPayoutTotal   string           `json:"net_payout_total"`
	Rake             FishingRake      `json:"rake"`
	RulesVersion     int              `json:"rules_version"`
	Payment          game.Payment     `json:"payment"`
	BatchID          string           `json:"batch_id"`
	Bait             string           `json:"bait"`
	Count            int              `json:"count"`
	UnitPrice        string           `json:"unit_price"`
	EntryTotal       string           `json:"entry_total"`
	Outcomes         []FishingOutcome `json:"outcomes"`
	PayoutTotal      string           `json:"payout_total"`
	Balance          string           `json:"balance"`
	GameBalance      string           `json:"game_balance"`
	SettledAt        int64            `json:"settled_at"`
	IdempotentReplay bool             `json:"idempotent_replay"`
}

type FishingSettlementPending struct {
	RulesVersion   int          `json:"rules_version"`
	Payment        game.Payment `json:"payment"`
	BatchID        string       `json:"batch_id"`
	Bait           string       `json:"bait"`
	Count          int          `json:"count"`
	EntryTotal     string       `json:"entry_total"`
	State          string       `json:"state"`
	NextAttemptAt  *int64       `json:"next_attempt_at"`
	RetryExhausted bool         `json:"retry_exhausted"`
}

type FishingState struct {
	SettlementPending *FishingSettlementPending `json:"settlement_pending"`
	Unrevealed        *FishingBatchResult       `json:"unrevealed"`
	HasMoreUnrevealed bool                      `json:"has_more_unrevealed"`
}

type Identity struct {
	Kind        string  `json:"kind"`
	DisplayName string  `json:"display_name,omitempty"`
	AvatarURL   *string `json:"avatar_url,omitempty"`
}

// MarshalJSON preserves the strict identity union: anonymous rows expose
// only their discriminator, while public rows always include avatar_url even
// when the authoritative value is null.
func (identity Identity) MarshalJSON() ([]byte, error) {
	switch identity.Kind {
	case "anonymous":
		return json.Marshal(struct {
			Kind string `json:"kind"`
		}{Kind: identity.Kind})
	case "public":
		if identity.DisplayName == "" {
			return nil, errors.New("game runtime: public identity requires a display name")
		}
		return json.Marshal(struct {
			Kind        string  `json:"kind"`
			DisplayName string  `json:"display_name"`
			AvatarURL   *string `json:"avatar_url"`
		}{Kind: identity.Kind, DisplayName: identity.DisplayName, AvatarURL: identity.AvatarURL})
	default:
		return nil, errors.New("game runtime: invalid identity kind")
	}
}

type FishingLeaderboardRow struct {
	Rank                string   `json:"rank"`
	SpeciesKey          string   `json:"species_key,omitempty"`
	SizeCM              int      `json:"size_cm,omitempty"`
	TotalCredits        string   `json:"total_credits,omitempty"`
	Identity            Identity `json:"identity"`
	IsMe                bool     `json:"is_me"`
	BlueFatFishLengthCM *string  `json:"blue_fat_fish_length_cm,omitempty"`
}

// MarshalJSON enforces the board-specific row union. In particular, zero is
// a legitimate single-board size for junk and treasure and must not disappear
// through omitempty.
func (row FishingLeaderboardRow) MarshalJSON() ([]byte, error) {
	single := row.SpeciesKey != ""
	total := row.TotalCredits != ""
	if single == total || row.Rank == "" {
		return nil, errors.New("game runtime: invalid leaderboard row union")
	}
	if row.BlueFatFishLengthCM != nil && (!single || !fishing.ValidBlueFatFishLength(*row.BlueFatFishLengthCM) ||
		row.SizeCM < 100 || row.SizeCM > 200 || row.SpeciesKey != "koi" && row.SpeciesKey != "taimen" && row.SpeciesKey != "yellowcheek") {
		return nil, errors.New("game runtime: invalid leaderboard presentation")
	}
	if single {
		return json.Marshal(struct {
			Rank                string   `json:"rank"`
			SpeciesKey          string   `json:"species_key"`
			SizeCM              int      `json:"size_cm"`
			BlueFatFishLengthCM *string  `json:"blue_fat_fish_length_cm"`
			Identity            Identity `json:"identity"`
			IsMe                bool     `json:"is_me"`
		}{Rank: row.Rank, SpeciesKey: row.SpeciesKey, SizeCM: row.SizeCM, BlueFatFishLengthCM: row.BlueFatFishLengthCM, Identity: row.Identity, IsMe: row.IsMe})
	}
	return json.Marshal(struct {
		Rank         string   `json:"rank"`
		TotalCredits string   `json:"total_credits"`
		Identity     Identity `json:"identity"`
		IsMe         bool     `json:"is_me"`
	}{Rank: row.Rank, TotalCredits: row.TotalCredits, Identity: row.Identity, IsMe: row.IsMe})
}

type FishingLeaderboard struct {
	Board       string                  `json:"board"`
	WindowStart *int64                  `json:"window_start"`
	Entries     []FishingLeaderboardRow `json:"entries"`
	Me          *FishingLeaderboardRow  `json:"me"`
}

type UserExport struct {
	Pending     []FishingSettlementPending `json:"fishing_pending"`
	Terminal    []FishingTerminalExport    `json:"fishing_terminal"`
	Single      *FishingLeaderboardRow     `json:"fishing_single_best"`
	Total       *FishingLeaderboardRow     `json:"fishing_rolling_total"`
	RollingBest *FishingLeaderboardRow     `json:"fishing_rolling_best"`
}

// FishingTerminalExport is the lifecycle-only terminal projection. RevealedAt
// is deliberately kept out of FishingBatchResult so the public game response
// remains the frozen closed DTO while account export can include ACK state.
type FishingTerminalExport struct {
	NetPayoutTotal string           `json:"net_payout_total"`
	Rake           FishingRake      `json:"rake"`
	RulesVersion   int              `json:"rules_version"`
	Payment        game.Payment     `json:"payment"`
	BatchID        string           `json:"batch_id"`
	Bait           string           `json:"bait"`
	Count          int              `json:"count"`
	UnitPrice      string           `json:"unit_price"`
	EntryTotal     string           `json:"entry_total"`
	Outcomes       []FishingOutcome `json:"outcomes"`
	PayoutTotal    string           `json:"payout_total"`
	SettledAt      int64            `json:"settled_at"`
	RevealedAt     *int64           `json:"revealed_at"`
}

type StartInput struct {
	UserID         int64
	Bait           string
	Count          int
	IdempotencyKey string
}
type RecoverInput struct {
	UserID         int64
	BatchID        string
	IdempotencyKey string
}
