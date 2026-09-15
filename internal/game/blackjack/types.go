package blackjack

import (
	"encoding/json"
	"errors"

	"github.com/waiting-here/NonbiriAPI/internal/game"
	"github.com/waiting-here/NonbiriAPI/internal/game/blackjack/config"
	"github.com/waiting-here/NonbiriAPI/internal/game/blackjack/engine"
)

const retentionSeconds int64 = 30 * 24 * 60 * 60
const maxTime int64 = 253399708739

var (
	ErrInvalid      = errors.New("blackjack: invalid request")
	ErrConflict     = errors.New("blackjack: state changed")
	ErrUnavailable  = errors.New("blackjack: unavailable")
	ErrInvariant    = errors.New("blackjack: invariant violation")
	ErrUnauthorized = errors.New("blackjack: unauthorized")
	ErrForbidden    = errors.New("blackjack: forbidden")
	ErrMaintenance  = errors.New("blackjack: maintenance")
	ErrNotFound     = errors.New("blackjack: not found")
	ErrInsufficient = errors.New("blackjack: insufficient credits")
	ErrLimit        = errors.New("blackjack: resource limit")
	ErrRateLimited  = errors.New("blackjack: rate limited")
)

type Identity struct {
	UserID         int64
	SessionBinding string
}
type MutationResult struct {
	Status   int
	Body     json.RawMessage
	Replayed bool
}
type EnqueueInput struct {
	Identity
	Key        string
	Stake      string
	ConfigHash string
}
type ActionInput struct {
	Identity
	Key, SessionID string
	Hand           int
	Revision       string
	Action         string
}
type QueueReceipt struct {
	ID       string `json:"id"`
	Position string `json:"position"`
	State    string `json:"state"`
}
type ActionReceipt struct {
	SessionID string `json:"session_id"`
	BatchAt   int64  `json:"batch_at"`
	Hand      int    `json:"hand"`
	Revision  string `json:"revision"`
}

type OwnEntry struct {
	ID        string              `json:"id"`
	Position  string              `json:"position"`
	State     string              `json:"state"`
	Stake     string              `json:"stake"`
	Rake      config.Rates        `json:"rake_bp"`
	Payment   game.Payment        `json:"payment"`
	Seat      *int                `json:"seat"`
	SessionID *string             `json:"session_id"`
	Pending   bool                `json:"pending"`
	Legal     map[string][]string `json:"legal_actions"`
}

type SeatTerms struct {
	Seat    int          `json:"seat"`
	Stake   string       `json:"stake"`
	Rake    config.Rates `json:"rake_bp"`
	Stopped bool         `json:"stopped"`
	Emote   string       `json:"emote,omitempty"`
	EmoteAt *int64       `json:"emote_at,omitempty"`
}

type SeatSettlement struct {
	Seat  int              `json:"seat"`
	Hands []HandSettlement `json:"hands"`
}

type SeatRefund struct {
	Seat   int    `json:"seat"`
	Amount string `json:"amount"`
}

// TableFact is safe for every spectator. It never carries account identities,
// payment composition, pending intents, the shoe or a concealed dealer card.
type TableFact struct {
	Cards       *engine.View     `json:"cards"`
	Seats       []SeatTerms      `json:"seats"`
	Settlements []SeatSettlement `json:"settlements"`
	Refunds     []SeatRefund     `json:"refunds,omitempty"`
}
type TableView struct {
	ID          string    `json:"id"`
	Revision    string    `json:"revision"`
	StartedAt   int64     `json:"started_at"`
	Phase       string    `json:"phase"`
	Deadline    int64     `json:"deadline"`
	NextRoundAt int64     `json:"next_round_at"`
	TerminalAt  *int64    `json:"terminal_at"`
	Reason      string    `json:"reason,omitempty"`
	Fact        TableFact `json:"fact"`
}
type Home struct {
	ServerNow   int64       `json:"server_now"`
	Phase       string      `json:"phase"`
	Deadline    int64       `json:"deadline"`
	NextRoundAt int64       `json:"next_round_at"`
	Config      config.Wire `json:"config"`
	ConfigHash  string      `json:"config_hash"`
	QueueCount  string      `json:"queue_count"`
	You         *OwnEntry   `json:"you"`
	YourSeat    *int        `json:"your_seat"`
	Table       *TableView  `json:"table"`
}
