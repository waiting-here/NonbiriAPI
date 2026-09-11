// Package finance declares game-specific capabilities for the shared ledger.
// Ports borrow the current transaction; neither ports nor callbacks commit it.
package finance

import (
	"context"
	"database/sql"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
)

type Mutation func(context.Context, *sql.Tx) error
type AccountMutation func(context.Context, *sql.Tx, int64) error
type Entry struct {
	Meta       ledger.Meta
	ResourceID string
	UserID     int64
	Amount     ledger.Amount
}
type FishingSettlement struct {
	Entry
	Payout ledger.Amount
}
type Fishing interface {
	Reserve(context.Context, *sql.Tx, Entry, Mutation) error
	Settle(context.Context, *sql.Tx, FishingSettlement, Mutation) error
	Release(context.Context, *sql.Tx, Entry, Mutation) error
}
type LinkLink interface {
	Entry(context.Context, *sql.Tx, Entry) error
}

type QueueInput struct {
	QueueID string
	UserID  int64
	Amount  ledger.Amount
}
type SessionStart struct {
	Meta       ledger.Meta
	SessionID  string
	FutureRows db.U128
	Queues     [3]QueueInput
}
type RoundCut struct {
	Meta                                ledger.Meta
	SessionID                           string
	Sequence                            db.U128
	WelfareAccountID, ThursdayAccountID int64
	Amounts                             ledger.RPSCutAmounts
}
type Payout struct {
	UserID int64
	Amount ledger.Amount
}
type Terminal struct {
	Meta             ledger.Meta
	SessionID        string
	WelfareAccountID int64
	Payouts          []Payout
	Deleted, Carry   ledger.Amount
}
type RPS interface {
	QueueReserve(context.Context, *sql.Tx, Entry, AccountMutation) (int64, error)
	QueueRelease(context.Context, *sql.Tx, Entry, Mutation) error
	SessionStart(context.Context, *sql.Tx, SessionStart, AccountMutation) error
	RoundCut(context.Context, *sql.Tx, RoundCut, Mutation) error
	Terminal(context.Context, *sql.Tx, Terminal, Mutation) error
}
