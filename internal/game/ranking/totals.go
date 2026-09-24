// Package ranking maintains exact game contributions inside the caller's
// settlement transaction. It never infers game results from wallet changes.
package ranking

import (
	"context"
	"database/sql"
	"errors"
	"math/big"
	"strings"

	"github.com/waiting-here/NonbiriAPI/internal/db"
)

var (
	ErrCatchingUp = errors.New("rankings are catching up")
	ErrInvalid    = errors.New("invalid ranking contribution")
)

const week int64 = 604800
const month int64 = 2592000

// Contribution is supplied only by a terminal finance adapter, after its
// source CAS. Loss includes both payment assets and excludes other activity.
type Contribution struct {
	UserID               int64
	Game, Source         string
	SettledAt            int64
	Loss, PositiveProfit *big.Int
}

func RecordTx(ctx context.Context, tx *sql.Tx, c Contribution) error {
	if ctx == nil || tx == nil || c.UserID <= 0 || c.SettledAt < 0 || c.SettledAt > 253399708799 || len(c.Source) == 0 || len(c.Source) > 128 || c.Loss == nil || c.PositiveProfit == nil || c.PositiveProfit.Sign() < 0 {
		return ErrInvalid
	}
	for _, r := range c.Source {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("_:-", r)) {
			return ErrInvalid
		}
	}
	profitBoard := c.Game == "bidding" || c.Game == "blackjack"
	switch c.Game {
	case "fishing", "linklink", "rps", "bidding", "likes", "blackjack":
	default:
		return ErrInvalid
	}
	if !profitBoard && c.PositiveProfit.Sign() != 0 {
		return ErrInvalid
	}
	loss, err := db.SM128FromBig(c.Loss)
	if err != nil {
		return err
	}
	profit, err := db.U128FromBig(c.PositiveProfit)
	if err != nil {
		return err
	}
	var epoch int64
	if err := tx.QueryRowContext(ctx, `SELECT started_at FROM game_statistics_epoch WHERE id=1`).Scan(&epoch); err != nil {
		return err
	}
	if c.SettledAt < epoch {
		return nil
	}
	ready, err := AdvanceTx(ctx, tx, c.SettledAt)
	if err != nil {
		return err
	}
	if !ready {
		return ErrCatchingUp
	}
	var raw []byte
	if err := tx.QueryRowContext(ctx, `SELECT next_event_seq FROM game_rank_counters WHERE id=1`).Scan(&raw); err != nil {
		return err
	}
	seq, err := db.DecodeU128(raw)
	if err != nil {
		return err
	}
	next, err := db.U128FromBig(new(big.Int).Add(seq.Big(), big.NewInt(1)))
	if err != nil {
		return err
	}
	var e7, e30 any
	if profitBoard && c.PositiveProfit.Sign() > 0 {
		e7, e30 = c.SettledAt+week, c.SettledAt+month
	}
	sign, mag := int(loss.Sign), db.EncodeU128(loss.Mag)
	if _, err := tx.ExecContext(ctx, `INSERT INTO game_rank_events(seq,user_id,game_key,source_id,settled_at,loss_sign,loss_mag,positive_profit,charity_expires_at,profit_7d_expires_at,profit_30d_expires_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, raw, c.UserID, c.Game, c.Source, c.SettledAt, sign, mag, db.EncodeU128(profit), c.SettledAt+week, e7, e30); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE game_rank_counters SET next_event_seq=? WHERE id=1`, db.EncodeU128(next)); err != nil {
		return err
	}
	if err := changeTotal(ctx, tx, c.UserID, "game_charity", "7d", c.Loss, c.SettledAt, 1, raw); err != nil {
		return err
	}
	if err := recordNetTotals(ctx, tx, c, raw); err != nil {
		return err
	}
	if profitBoard {
		for _, window := range []string{"7d", "30d", "history"} {
			if err := changeTotal(ctx, tx, c.UserID, c.Game, window, c.PositiveProfit, c.SettledAt, 1, raw); err != nil {
				return err
			}
		}
	}
	return nil
}

func changeTotal(ctx context.Context, tx *sql.Tx, user int64, board, window string, delta *big.Int, at int64, phase int, seq []byte) error {
	if delta.Sign() == 0 {
		return nil
	}
	var sign int
	var mag []byte
	err := tx.QueryRowContext(ctx, `SELECT amount_sign,amount_mag FROM game_rank_totals WHERE user_id=? AND board=? AND window=?`, user, board, window).Scan(&sign, &mag)
	current := new(big.Int)
	if err == nil {
		value, err := db.NewSM128(sign, mag)
		if err != nil {
			return err
		}
		current = value.Big()
	} else if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	current.Add(current, delta)
	if board != "game_charity" && !isNetBoard(board) && current.Sign() < 0 {
		return ErrInvalid
	}
	value, err := db.SM128FromBig(current)
	if err != nil {
		return err
	}
	sign, mag = int(value.Sign), db.EncodeU128(value.Mag)
	_, err = tx.ExecContext(ctx, `INSERT INTO game_rank_totals(user_id,board,window,amount_sign,amount_mag,achieved_at,achieved_phase,achieved_seq) VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(user_id,board,window) DO UPDATE SET amount_sign=excluded.amount_sign,amount_mag=excluded.amount_mag,achieved_at=excluded.achieved_at,achieved_phase=excluded.achieved_phase,achieved_seq=excluded.achieved_seq`, user, board, window, sign, mag, at, phase, seq)
	return err
}
