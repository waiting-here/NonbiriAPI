package ranking

import (
	"context"
	"database/sql"
	"errors"
	"math/big"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/db"
)

var netBoards = [...]string{"game_net_profit", "fishing_net_profit", "blackjack_net_profit"}

func isNetBoard(board string) bool {
	return board == netBoards[0] || board == netBoards[1] || board == netBoards[2]
}

func netGameIndex(game string) int {
	switch game {
	case "fishing":
		return 1
	case "blackjack":
		return 2
	default:
		return -1
	}
}

func recordNetTotals(ctx context.Context, tx *sql.Tx, c Contribution, seq []byte) error {
	delta := new(big.Int).Neg(c.Loss)
	if err := changeTotal(ctx, tx, c.UserID, netBoards[0], "7d", delta, c.SettledAt, 1, seq); err != nil {
		return err
	}
	if index := netGameIndex(c.Game); index >= 0 {
		return changeTotal(ctx, tx, c.UserID, netBoards[index], "7d", delta, c.SettledAt, 1, seq)
	}
	return nil
}

func netRebuildReady(ctx context.Context, tx *sql.Tx) (bool, error) {
	var phase int
	err := tx.QueryRowContext(ctx, `SELECT phase FROM game_rank_net_rebuild WHERE id=1`).Scan(&phase)
	return phase == 2, err
}

type rebuildEvent struct {
	seq, magnitude []byte
	user, settled  int64
	game           string
	sign           sql.NullInt64
}

// advanceNetRebuild shares the caller's event and time budget. The existing
// settlement gate holds new contributions until this sequence snapshot is
// published; source removal and checkpoint advancement are transactional.
func advanceNetRebuild(ctx context.Context, tx *sql.Tx, limit int, deadline time.Time) (bool, int, error) {
	var phase int
	var through, last []byte
	if err := tx.QueryRowContext(ctx, `SELECT phase,through_seq,last_seq FROM game_rank_net_rebuild WHERE id=1`).Scan(&phase, &through, &last); err != nil {
		return false, 0, err
	}
	if phase == 2 {
		return true, 0, nil
	}
	if through == nil {
		if err := tx.QueryRowContext(ctx, `SELECT next_event_seq FROM game_rank_counters WHERE id=1`).Scan(&through); err != nil {
			return false, 0, err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE game_rank_net_rebuild SET through_seq=? WHERE id=1`, through); err != nil {
			return false, 0, err
		}
	}
	used := 0
	for phase == 0 && used < limit {
		chunk := min(chunkRows, limit-used)
		rows, err := tx.QueryContext(ctx, `SELECT seq,user_id,game_key,settled_at,loss_sign,loss_mag FROM game_rank_events WHERE seq>? AND seq<? ORDER BY seq LIMIT ?`, last, through, chunk)
		if err != nil {
			return false, used, err
		}
		items := make([]rebuildEvent, 0, chunk)
		for rows.Next() {
			var event rebuildEvent
			if err := rows.Scan(&event.seq, &event.user, &event.game, &event.settled, &event.sign, &event.magnitude); err != nil {
				rows.Close()
				return false, used, err
			}
			items = append(items, event)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return false, used, err
		}
		if err := rows.Close(); err != nil {
			return false, used, err
		}
		for _, event := range items {
			if event.sign.Valid && event.sign.Int64 != 0 {
				loss, err := db.NewSM128(int(event.sign.Int64), event.magnitude)
				if err != nil {
					return false, used, err
				}
				profit := new(big.Int).Neg(loss.Big())
				if err := accumulateNetScratch(ctx, tx, event, netBoards[0], profit); err != nil {
					return false, used, err
				}
				if index := netGameIndex(event.game); index >= 0 {
					if err := accumulateNetScratch(ctx, tx, event, netBoards[index], profit); err != nil {
						return false, used, err
					}
				}
			}
			last = event.seq
			used++
		}
		if len(items) < chunk {
			phase = 1
		}
		if _, err := tx.ExecContext(ctx, `UPDATE game_rank_net_rebuild SET phase=?,last_seq=? WHERE id=1`, phase, last); err != nil {
			return false, used, err
		}
		if len(items) > 0 && !time.Now().Before(deadline) {
			return false, used, nil
		}
	}
	for phase == 1 && used < limit {
		chunk := min(chunkRows, limit-used)
		rows, err := tx.QueryContext(ctx, `SELECT user_id,board,amount_sign,amount_mag,achieved_at,achieved_seq FROM game_rank_net_rebuild_totals ORDER BY user_id,board LIMIT ?`, chunk)
		if err != nil {
			return false, used, err
		}
		type total struct {
			user, at int64
			board    string
			sign     int
			mag, seq []byte
		}
		items := make([]total, 0, chunk)
		for rows.Next() {
			var value total
			if err := rows.Scan(&value.user, &value.board, &value.sign, &value.mag, &value.at, &value.seq); err != nil {
				rows.Close()
				return false, used, err
			}
			items = append(items, value)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return false, used, err
		}
		if err := rows.Close(); err != nil {
			return false, used, err
		}
		for _, value := range items {
			amount := new(big.Int).SetBytes(value.mag)
			if value.sign < 0 {
				amount.Neg(amount)
			}
			if err := changeTotal(ctx, tx, value.user, value.board, "7d", amount, value.at, 1, value.seq); err != nil {
				return false, used, err
			}
			if _, err := tx.ExecContext(ctx, `DELETE FROM game_rank_net_rebuild_totals WHERE user_id=? AND board=?`, value.user, value.board); err != nil {
				return false, used, err
			}
			used++
		}
		if len(items) < chunk {
			if _, err := tx.ExecContext(ctx, `UPDATE game_rank_net_rebuild SET phase=2 WHERE id=1`); err != nil {
				return false, used, err
			}
			return true, used, nil
		}
		if !time.Now().Before(deadline) {
			return false, used, nil
		}
	}
	return false, used, nil
}

func accumulateNetScratch(ctx context.Context, tx *sql.Tx, event rebuildEvent, board string, delta *big.Int) error {
	var sign int
	var magnitude []byte
	amount := new(big.Int)
	err := tx.QueryRowContext(ctx, `SELECT amount_sign,amount_mag FROM game_rank_net_rebuild_totals WHERE user_id=? AND board=?`, event.user, board).Scan(&sign, &magnitude)
	if err == nil {
		amount.SetBytes(magnitude)
		if sign < 0 {
			amount.Neg(amount)
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	amount.Add(amount, delta)
	if amount.BitLen() > 256 {
		return ErrInvalid
	}
	magnitude = new(big.Int).Abs(amount).FillBytes(make([]byte, 32))
	_, err = tx.ExecContext(ctx, `INSERT INTO game_rank_net_rebuild_totals(user_id,board,amount_sign,amount_mag,achieved_at,achieved_seq) VALUES(?,?,?,?,?,?) ON CONFLICT(user_id,board) DO UPDATE SET amount_sign=excluded.amount_sign,amount_mag=excluded.amount_mag,achieved_at=excluded.achieved_at,achieved_seq=excluded.achieved_seq`, event.user, board, amount.Sign(), magnitude, event.settled, event.seq)
	return err
}
