// Package runtime owns the persistent Fishing state machine and the
// HTTP/adaptor seams composed by the application root.
package runtime

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/game"
)

const (
	RouteGames              = "/api/games"
	RouteFishingBatches     = "/api/games/fishing/batches"
	RouteFishingState       = "/api/games/fishing/state"
	RouteFishingACK         = "/api/games/fishing/batches/{id}/ack"
	RouteFishingRecover     = "/api/games/fishing/batches/{id}/recover"
	RouteFishingLeaderboard = "/api/games/fishing/leaderboard"
	RouteAdminActiveCounts  = "/admin/api/games/active-counts"
	RouteAdminGamesConfig   = "/admin/api/games/config"

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

type FishingOutcome struct {
	Ordinal    int    `json:"ordinal"`
	SpeciesKey string `json:"species_key"`
	Tier       string `json:"tier"`
	SizeCM     int    `json:"size_cm"`
	Reward     string `json:"reward"`
}

type FishingBatchResult struct {
	BatchID          string           `json:"batch_id"`
	Bait             string           `json:"bait"`
	Count            int              `json:"count"`
	UnitPrice        string           `json:"unit_price"`
	EntryTotal       string           `json:"entry_total"`
	Outcomes         []FishingOutcome `json:"outcomes"`
	PayoutTotal      string           `json:"payout_total"`
	Balance          string           `json:"balance"`
	SettledAt        int64            `json:"settled_at"`
	IdempotentReplay bool             `json:"idempotent_replay"`
}

type FishingSettlementPending struct {
	BatchID        string `json:"batch_id"`
	Bait           string `json:"bait"`
	Count          int    `json:"count"`
	EntryTotal     string `json:"entry_total"`
	State          string `json:"state"`
	NextAttemptAt  *int64 `json:"next_attempt_at"`
	RetryExhausted bool   `json:"retry_exhausted"`
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
	Rank         string   `json:"rank"`
	SpeciesKey   string   `json:"species_key,omitempty"`
	SizeCM       int      `json:"size_cm,omitempty"`
	TotalCredits string   `json:"total_credits,omitempty"`
	Identity     Identity `json:"identity"`
	IsMe         bool     `json:"is_me"`
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
	if single {
		return json.Marshal(struct {
			Rank       string   `json:"rank"`
			SpeciesKey string   `json:"species_key"`
			SizeCM     int      `json:"size_cm"`
			Identity   Identity `json:"identity"`
			IsMe       bool     `json:"is_me"`
		}{Rank: row.Rank, SpeciesKey: row.SpeciesKey, SizeCM: row.SizeCM, Identity: row.Identity, IsMe: row.IsMe})
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

type AdminActiveCounts struct {
	Games  []AdminGameCount  `json:"games"`
	Queues []AdminQueueCount `json:"queues"`
}
type AdminGameCount struct {
	Game  string  `json:"game"`
	Mode  *string `json:"mode"`
	Spec  *string `json:"spec"`
	Phase *string `json:"phase"`
	Count string  `json:"count"`
}
type AdminQueueCount struct {
	Mode  string `json:"mode"`
	Count string `json:"count"`
}

type UserExport struct {
	Pending  []FishingSettlementPending `json:"fishing_pending"`
	Terminal []FishingBatchResult       `json:"fishing_terminal"`
	Single   *FishingLeaderboardRow     `json:"fishing_single_best"`
	Total    *FishingLeaderboardRow     `json:"fishing_rolling_total"`
}

type AdminRouteRegistrar interface {
	RegisterAdminRoute(method, pattern string, handler http.Handler) error
}

type AdminFinalAuthorizer interface {
	AuthorizeAdminMutation(context.Context, *sql.Tx) error
}

type AdminFinalAuthorizerFunc func(context.Context, *sql.Tx) error

func (fn AdminFinalAuthorizerFunc) AuthorizeAdminMutation(ctx context.Context, tx *sql.Tx) error {
	return fn(ctx, tx)
}

type RPSHealthProbe interface {
	Ready(context.Context, *sql.Tx) bool
}
type RPSHealthProbeFunc func(context.Context, *sql.Tx) bool

func (fn RPSHealthProbeFunc) Ready(ctx context.Context, tx *sql.Tx) bool {
	return fn == nil || fn(ctx, tx)
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

type SnapshotProvider interface {
	GamesSnapshot(context.Context, int64, time.Time) (game.GamesSnapshot, error)
}
