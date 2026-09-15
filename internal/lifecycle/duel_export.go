package lifecycle

import (
	"context"
	"database/sql"
	"encoding/json"
)

// These fields pin the personal projection independently of live response DTOs.
// Rule JSON has already passed the game's participant-specific visibility filter.
type DuelExport struct {
	Queue         *DuelQueueExport  `json:"queue"`
	Current       *DuelStateExport  `json:"current"`
	CurrentRounds []DuelRoundExport `json:"current_rounds"`
	History       []DuelMatchExport `json:"history"`
}
type DuelQueueExport struct {
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
