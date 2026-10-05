package lifecycle

import (
	"context"
	"database/sql"
	"encoding/json"
)

// These fields pin the personal projection independently of live response DTOs.
// Rule JSON has already passed the game's participant-specific visibility filter.
type DuelExport struct {
	AI            *DuelAIExport       `json:"ai,omitempty"`
	Queue         *DuelQueueExport    `json:"queue"`
	Current       *DuelStateExport    `json:"current"`
	CurrentRounds []DuelRoundExport   `json:"current_rounds"`
	History       []DuelMatchExport   `json:"history"`
	Loadouts      []DuelLoadoutExport `json:"loadouts,omitempty"`
}
type DuelLoadoutExport struct {
	Slot      int             `json:"slot"`
	Revision  string          `json:"revision"`
	Mode      string          `json:"mode"`
	Loadout   json.RawMessage `json:"loadout"`
	UpdatedAt int64           `json:"updated_at"`
}
type DuelQueueExport struct {
	Economy      string            `json:"economy,omitempty"`
	AI           *DuelAITerms      `json:"ai,omitempty"`
	Position     int               `json:"position,omitempty"`
	ID           string            `json:"id"`
	Revision     string            `json:"revision"`
	Mode         string            `json:"mode"`
	Deadline     int64             `json:"deadline"`
	Ticket       string            `json:"ticket"`
	Payment      GamePaymentExport `json:"payment"`
	TermsHash    string            `json:"terms_hash"`
	RulesVersion int               `json:"rules_version"`
	Loadout      json.RawMessage   `json:"loadout,omitempty"`
}
type DuelStateExport struct {
	Economy      string                `json:"economy,omitempty"`
	AI           *DuelAIView           `json:"ai,omitempty"`
	Sources      []DuelActionSource    `json:"action_sources,omitempty"`
	ID           string                `json:"id"`
	Game         string                `json:"game"`
	Mode         string                `json:"mode"`
	RulesVersion int                   `json:"rules_version"`
	ContentHash  string                `json:"content_hash"`
	Revision     string                `json:"revision"`
	PhaseSeq     string                `json:"phase_seq"`
	Phase        string                `json:"phase"`
	Round        int                   `json:"round"`
	Deadline     *int64                `json:"deadline"`
	ServerNow    int64                 `json:"server_now"`
	You          int                   `json:"you"`
	Locked       [2]bool               `json:"locked"`
	Ticket       string                `json:"ticket"`
	Rake         DuelRatesExport       `json:"rake_bp"`
	OwnPayment   GamePaymentExport     `json:"own_payment"`
	View         json.RawMessage       `json:"view"`
	Resolution   *DuelResolutionExport `json:"resolution"`
	RoundStart   *DuelRoundStartExport `json:"round_start"`
}
type DuelResultExport struct {
	Economy      string                `json:"economy,omitempty"`
	AI           *DuelAIView           `json:"ai,omitempty"`
	ID           string                `json:"id"`
	Game         string                `json:"game"`
	Mode         string                `json:"mode"`
	TerminalAt   int64                 `json:"terminal_at"`
	Outcome      string                `json:"outcome"`
	Reason       string                `json:"reason"`
	Scores       [2]int64              `json:"scores"`
	OwnPayment   GamePaymentExport     `json:"own_payment"`
	OwnRefund    GamePaymentExport     `json:"own_refund"`
	PrizeGeneral string                `json:"prize_general"`
	Rake         DuelAmountsExport     `json:"rake"`
	Resolution   *DuelResolutionExport `json:"resolution"`
	You          int                   `json:"you"`
	View         json.RawMessage       `json:"view"`
}
type DuelDetailExport struct {
	Sources          []DuelActionSource `json:"action_sources,omitempty"`
	Result           *DuelResultExport  `json:"result"`
	RulesVersion     int                `json:"rules_version"`
	ContentHash      string             `json:"content_hash"`
	Ticket           string             `json:"ticket"`
	Rake             DuelRatesExport    `json:"rake_bp"`
	Initial          json.RawMessage    `json:"initial"`
	TerminalActions  [2]json.RawMessage `json:"terminal_actions"`
	RoundStartEvents json.RawMessage    `json:"round_start_events"`
}
type DuelMatchExport struct {
	Detail DuelDetailExport  `json:"detail"`
	Rounds []DuelRoundExport `json:"rounds"`
}
type DuelRoundExport struct {
	Sources     [2]string       `json:"sources,omitempty"`
	Round       int             `json:"round"`
	Before      json.RawMessage `json:"before"`
	After       json.RawMessage `json:"after"`
	Facts       json.RawMessage `json:"facts"`
	StartEvents json.RawMessage `json:"start_events"`
	Timeouts    [2]bool         `json:"timeouts"`
}
type DuelResolutionExport struct {
	Round     int             `json:"round"`
	StartedAt int64           `json:"started_at"`
	EndsAt    int64           `json:"ends_at"`
	Summary   json.RawMessage `json:"summary"`
}
type DuelRoundStartExport struct {
	Round     int             `json:"round"`
	StartedAt int64           `json:"started_at"`
	Events    json.RawMessage `json:"events"`
}
type DuelRatesExport struct {
	Platform int `json:"platform"`
	Welfare  int `json:"welfare"`
	Thursday int `json:"thursday"`
}
type DuelAmountsExport struct {
	Platform string `json:"platform"`
	Welfare  string `json:"welfare"`
	Thursday string `json:"thursday"`
}
type DuelExporter interface {
	ExportDuel(context.Context, *sql.Tx, ExportRequest) (DuelExport, ExportFinalizer, error)
}

type DuelAIExport struct {
	Preferences []DuelAIPreferenceExport     `json:"preferences"`
	Memories    []DuelAIMemoryExport         `json:"memories"`
	Snapshots   []DuelAIMemorySnapshotExport `json:"snapshots"`
	Clears      []DuelAIClearExport          `json:"clears"`
}
type DuelAIPreferenceExport struct {
	BotID         string `json:"bot_id"`
	MemoryEnabled bool   `json:"memory_enabled"`
	UpdatedAt     int64  `json:"updated_at"`
}
type DuelAIMemoryExport struct {
	SessionID   string          `json:"session_id"`
	BotID       string          `json:"bot_id"`
	Version     int             `json:"feature_version"`
	CompletedAt int64           `json:"completed_at"`
	ExpiresAt   int64           `json:"expires_at"`
	Features    json.RawMessage `json:"features"`
}
type DuelAIMemorySnapshotExport struct {
	SessionID     string          `json:"session_id"`
	MemoryEnabled bool            `json:"memory_enabled"`
	Samples       int             `json:"samples"`
	Summary       json.RawMessage `json:"summary,omitempty"`
}
type DuelAIClearExport struct {
	BotID       string `json:"bot_id"`
	ChallengeID string `json:"challenge_id"`
	CompletedAt int64  `json:"completed_at"`
	Reward      string `json:"reward"`
}

type DuelAITerms struct {
	BotID         string `json:"bot_id"`
	BotName       string `json:"bot_name"`
	Description   string `json:"description"`
	Revision      int64  `json:"revision,string"`
	ChallengeID   string `json:"challenge_id"`
	RulesKey      string `json:"rules_key"`
	PolicyID      string `json:"policy_id"`
	PolicyVersion int    `json:"policy_version"`
	SourceID      string `json:"source_id"`
	PolicySchema  string `json:"policy_schema"`
	FirstReward   string `json:"first_reward"`
	MemoryDays    int    `json:"memory_days"`
	MemoryGames   int    `json:"memory_games"`
}

type DuelAIView struct {
	Terms         DuelAITerms `json:"terms"`
	MemoryEnabled bool        `json:"memory_enabled"`
	MemorySamples int         `json:"memory_samples"`
	FirstClear    bool        `json:"first_clear"`
	Reward        string      `json:"reward"`
}

type DuelActionSource struct {
	PhaseSeq   string          `json:"phase_seq,omitempty"`
	Round      int             `json:"round"`
	Phase      string          `json:"phase"`
	Seat       int             `json:"seat"`
	Action     json.RawMessage `json:"action"`
	Origin     string          `json:"origin"`
	Failure    string          `json:"failure,omitempty"`
	AcceptedAt int64           `json:"accepted_at,omitempty"`
}
