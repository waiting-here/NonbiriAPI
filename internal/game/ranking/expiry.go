package ranking

import (
	"context"
	"database/sql"
	"errors"
	"math/big"
	"time"
)

const advanceRows = 512
const chunkRows = 64
const advanceTime = 25 * time.Millisecond

type expiryGroup struct {
	user, at      int64
	board, window string
	delta         *big.Int
	seq           []byte
}

func (g expiryGroup) column() string {
	if g.board == "game_charity" {
		return "charity_expires_at"
	}
	if g.window == "7d" {
		return "profit_7d_expires_at"
	}
	return "profit_30d_expires_at"
}

// AdvanceTx performs bounded work in the caller's transaction. A partial group
// is durable only when that transaction commits. Readers must check ReadyTx;
// totals are changed once per complete logical expiry group, including a group
// that takes several transactions. The wide scratch accumulator cannot
// overflow even if every possible SQLite row holds a maximal contribution.
func AdvanceTx(ctx context.Context, tx *sql.Tx, now int64) (bool, error) {
	if ctx == nil || tx == nil || now < 0 {
		return false, ErrInvalid
	}
	deadline := time.Now().Add(advanceTime)
	remaining := advanceRows
	for remaining > 0 && time.Now().Before(deadline) {
		g, err := nextGroup(ctx, tx, now)
		if errors.Is(err, sql.ErrNoRows) {
			return true, nil
		}
		if err != nil {
			return false, err
		}
		count, err := advanceGroup(ctx, tx, g, min(chunkRows, remaining))
		if err != nil {
			return false, err
		}
		remaining -= max(1, count)
	}
	return ReadyTx(ctx, tx, now)
}

func ReadyTx(ctx context.Context, tx *sql.Tx, now int64) (bool, error) {
	_, err := nextGroup(ctx, tx, now)
	if errors.Is(err, sql.ErrNoRows) {
		return true, nil
	}
	return false, err
}

func nextGroup(ctx context.Context, tx *sql.Tx, now int64) (expiryGroup, error) {
	var g expiryGroup
	var sign int
	var raw []byte
	err := tx.QueryRowContext(ctx, `SELECT user_id,board,window,expires_at,delta_sign,delta_mag,last_seq FROM game_rank_expiry_work WHERE id=1`).Scan(&g.user, &g.board, &g.window, &g.at, &sign, &raw, &g.seq)
	if err == nil {
		g.delta = new(big.Int).SetBytes(raw)
		if sign < 0 {
			g.delta.Neg(g.delta)
		}
		if g.at > now {
			return expiryGroup{}, ErrCatchingUp
		}
		return g, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return g, err
	}
	// Three index heads bound the search independently of the event population.
	err = tx.QueryRowContext(ctx, `SELECT user_id,board,window,expires_at FROM (
SELECT * FROM (SELECT user_id,'game_charity' AS board,'7d' AS window,charity_expires_at AS expires_at FROM game_rank_events WHERE charity_expires_at<=? ORDER BY charity_expires_at,user_id,seq LIMIT 1)
UNION ALL SELECT * FROM (SELECT user_id,game_key,'7d',profit_7d_expires_at FROM game_rank_events WHERE profit_7d_expires_at<=? ORDER BY profit_7d_expires_at,user_id,game_key,seq LIMIT 1)
UNION ALL SELECT * FROM (SELECT user_id,game_key,'30d',profit_30d_expires_at FROM game_rank_events WHERE profit_30d_expires_at<=? ORDER BY profit_30d_expires_at,user_id,game_key,seq LIMIT 1)
) ORDER BY expires_at,board,window,user_id LIMIT 1`, now, now, now).Scan(&g.user, &g.board, &g.window, &g.at)
	g.delta = new(big.Int)
	g.seq = make([]byte, 16)
	return g, err
}

func advanceGroup(ctx context.Context, tx *sql.Tx, g expiryGroup, limit int) (int, error) {
	column := g.column()
	predicate := column + `=? AND user_id=?`
	args := []any{g.at, g.user}
	if g.board != "game_charity" {
		predicate += ` AND game_key=?`
		args = append(args, g.board)
	}
	value := `1,positive_profit`
	if g.board == "game_charity" {
		value = `loss_sign,loss_mag`
	}
	args = append(args, limit)
	rows, err := tx.QueryContext(ctx, `SELECT seq,`+value+` FROM game_rank_events WHERE `+predicate+` ORDER BY seq LIMIT ?`, args...)
	if err != nil {
		return 0, err
	}
	type item struct {
		seq, mag []byte
		sign     int
	}
	items := make([]item, 0, limit)
	for rows.Next() {
		var v item
		if err := rows.Scan(&v.seq, &v.sign, &v.mag); err != nil {
			rows.Close()
			return 0, err
		}
		items = append(items, v)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	if err := rows.Close(); err != nil {
		return 0, err
	}
	for _, v := range items {
		amount := new(big.Int).SetBytes(v.mag)
		if v.sign < 0 {
			amount.Neg(amount)
		}
		g.delta.Sub(g.delta, amount)
		g.seq = v.seq
		set := column + `=NULL`
		if g.board == "game_charity" {
			set += `,loss_sign=NULL,loss_mag=NULL`
		}
		if _, err := tx.ExecContext(ctx, `UPDATE game_rank_events SET `+set+` WHERE seq=?`, v.seq); err != nil {
			return 0, err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM game_rank_events WHERE seq=? AND charity_expires_at IS NULL AND profit_7d_expires_at IS NULL AND profit_30d_expires_at IS NULL`, v.seq); err != nil {
			return 0, err
		}
	}
	var more int
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM game_rank_events WHERE `+predicate+`)`, args[:len(args)-1]...).Scan(&more)
	if err != nil {
		return 0, err
	}
	if more == 0 {
		if err := changeTotal(ctx, tx, g.user, g.board, g.window, g.delta, g.at, 0, g.seq); err != nil {
			return 0, err
		}
		_, err = tx.ExecContext(ctx, `DELETE FROM game_rank_expiry_work WHERE id=1`)
	} else {
		if g.delta.BitLen() > 256 {
			return 0, ErrInvalid
		}
		mag := new(big.Int).Abs(g.delta).FillBytes(make([]byte, 32))
		_, err = tx.ExecContext(ctx, `INSERT INTO game_rank_expiry_work(id,user_id,board,window,expires_at,delta_sign,delta_mag,last_seq) VALUES(1,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET delta_sign=excluded.delta_sign,delta_mag=excluded.delta_mag,last_seq=excluded.last_seq`, g.user, g.board, g.window, g.at, g.delta.Sign(), mag, g.seq)
	}
	return len(items), err
}
