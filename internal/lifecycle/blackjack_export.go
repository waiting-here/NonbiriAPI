package lifecycle

import (
	"context"
	"database/sql"
	"encoding/json"
)

type BlackjackExport struct {
	Current *BlackjackCurrentExport  `json:"current"`
	History []BlackjackHistoryExport `json:"history"`
}
type BlackjackCurrentExport struct {
	ServerNow   int64                 `json:"server_now"`
	Phase       string                `json:"phase"`
	Deadline    int64                 `json:"deadline"`
	NextRoundAt int64                 `json:"next_round_at"`
	You         *BlackjackEntryExport `json:"you"`
	YourSeat    *int                  `json:"your_seat"`
	Table       *BlackjackTableExport `json:"table"`
}
type BlackjackEntryExport struct {
	ID        string            `json:"id"`
	Position  string            `json:"position"`
	State     string            `json:"state"`
	Stake     string            `json:"stake"`
	Rake      DuelRatesExport   `json:"rake_bp"`
	Payment   GamePaymentExport `json:"payment"`
	Seat      *int              `json:"seat"`
	SessionID *string           `json:"session_id"`
	Pending   bool              `json:"pending"`
}
type BlackjackTableExport struct {
	ID          string `json:"id"`
	Revision    string `json:"revision"`
	StartedAt   int64  `json:"started_at"`
	Phase       string `json:"phase"`
	Deadline    int64  `json:"deadline"`
	NextRoundAt int64  `json:"next_round_at"`
	TerminalAt  *int64 `json:"terminal_at"`
	Reason      string `json:"reason,omitempty"`
	// The module produces this public-only card and settlement projection.
	Fact json.RawMessage `json:"fact"`
}
type BlackjackHistoryExport struct {
	Summary BlackjackSummaryExport `json:"summary"`
	Table   BlackjackTableExport   `json:"table"`
}
type BlackjackSummaryExport struct {
	ID         string            `json:"id"`
	StartedAt  int64             `json:"started_at"`
	TerminalAt int64             `json:"terminal_at"`
	Phase      string            `json:"phase"`
	Reason     string            `json:"reason"`
	Seat       int               `json:"seat"`
	Stake      string            `json:"stake"`
	TotalStake string            `json:"total_stake"`
	Net        string            `json:"net"`
	Payment    GamePaymentExport `json:"payment"`
}
type BlackjackExporter interface {
	ExportBlackjack(context.Context, *sql.Tx, ExportRequest) (BlackjackExport, ExportFinalizer, error)
}
