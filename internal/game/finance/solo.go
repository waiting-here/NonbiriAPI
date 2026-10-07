package finance

import (
	"context"
	"database/sql"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
)

type SoloStart struct {
	Meta      ledger.Meta
	SessionID string
	UserID    int64
	Ticket    ledger.Amount
	MayReward bool
}
type SoloFinish struct {
	Meta      ledger.Meta
	SessionID string
	Reward    ledger.Amount
	Cancelled bool
}
type SoloStartMutation func(context.Context, *sql.Tx, ledger.Payment, db.U128) error
type SoloTerminalMutation func(context.Context, *sql.Tx, string) error

// Solo charges a ticket and reserves capacity for one first-clear reward or refund.
type Solo interface {
	Start(context.Context, *sql.Tx, SoloStart, SoloStartMutation) error
	Finish(context.Context, *sql.Tx, SoloFinish, SoloTerminalMutation) error
}
