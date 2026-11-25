package rps

import (
	"context"
	"database/sql"
	"errors"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/game/randomness"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
)

const TerminalServerRestart = "server_restart"

// Committed pending results live outside the queue and active session tables.
func (s *Service) cancelStartupBatch(ctx context.Context, now int64, limit int) (int, error) {
	for i := 0; i < limit; i++ {
		worked, err := s.cancelStartupOne(ctx, now)
		if err != nil || !worked {
			return i, err
		}
	}
	return limit, nil
}
func (s *Service) cancelStartupOne(ctx context.Context, now int64) (bool, error) {
	tx, err := s.database.BeginTx(ctx, nil)
	if err != nil {
		return false, classifyDB(err)
	}
	defer tx.Rollback()
	q, err := scanQueue(tx.QueryRowContext(ctx, "SELECT "+queueColumns+" FROM game_rps_queue ORDER BY id LIMIT 1"))
	if err == nil {
		if err = s.releaseQueueTx(ctx, tx, q, now, 0); err != nil {
			return false, err
		}
		return true, classifyDB(tx.Commit())
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return false, classifyDB(err)
	}
	var id string
	err = tx.QueryRowContext(ctx, "SELECT id FROM game_rps_sessions ORDER BY id LIMIT 1").Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, classifyDB(err)
	}
	record, found, err := loadSessionByID(ctx, tx, id)
	if err != nil {
		return false, err
	}
	if !found {
		return false, ErrInvariant
	}
	op, err := s.generate("op_")
	if err != nil {
		return false, err
	}
	reason := TerminalServerRestart
	record.TerminalReason = &reason
	record.WelfareCarryTotal = db.U128{}
	for i := range record.Seats {
		seat := &record.Seats[i]
		sign := 0
		zero := db.U128{}
		returned := seat.StartingBalance
		seat.WalletNetSign, seat.WalletNetMag, seat.TerminalReturn = &sign, &zero, &returned
	}
	err = s.finance.CancelSession(ctx, tx, id, ledger.Meta{OperationID: op, CreatedAt: now}, func(ctx context.Context, tx *sql.Tx) error {
		if err := s.insertTerminalFactsTx(ctx, tx, &record, now); err != nil {
			return err
		}
		if err := randomness.MoveToSummary(ctx, tx, "rps", id); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM game_rps_sessions WHERE id=?", id); err != nil {
			return classifyDB(err)
		}
		_, err := tx.ExecContext(ctx, "UPDATE admin_alerts SET resolved=1,resolved_at=? WHERE kind='rps_terminal_retrying' AND ref=? AND resolved=0", now, id)
		return classifyDB(err)
	})
	if err != nil {
		return false, mapLedger(err)
	}
	return true, classifyDB(tx.Commit())
}
