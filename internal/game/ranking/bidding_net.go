package ranking

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"math/big"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/db"
)

const biddingBoard = "bidding_net_profit"
const biddingBatchRows = 1000
const biddingBatchTime = 2 * time.Second

type biddingRebuild struct {
	state           string
	watermark, last []byte
	coverage        sql.NullInt64
	missing         int64
}

func readBiddingRebuild(ctx context.Context, tx *sql.Tx) (biddingRebuild, error) {
	var b biddingRebuild
	err := tx.QueryRowContext(ctx, `SELECT state,watermark,last_seq,history_coverage_start,missing_events FROM game_bidding_net_rebuild WHERE id=1`).Scan(&b.state, &b.watermark, &b.last, &b.coverage, &b.missing)
	return b, err
}

func startBiddingRebuild(ctx context.Context, tx *sql.Tx, now int64, watermark []byte) error {
	var epoch int64
	if len(watermark) != 16 {
		return ErrInvalid
	}
	if err := tx.QueryRowContext(ctx, `SELECT started_at FROM game_statistics_epoch WHERE id=1`).Scan(&epoch); err != nil {
		return err
	}
	coverage := max(epoch, max(int64(0), now-week))
	_, err := tx.ExecContext(ctx, `UPDATE game_bidding_net_rebuild SET state='scanning',watermark=?,history_coverage_start=?,updated_at=? WHERE id=1 AND state='pending'`, watermark, coverage, now)
	return err
}

// A new contribution is buffered against the fixed source watermark until
// publication. The unique source sequence makes retried settlement safe.
func recordBiddingNet(ctx context.Context, tx *sql.Tx, c Contribution, seq []byte) error {
	if c.Game != "bidding" {
		return nil
	}
	b, err := readBiddingRebuild(ctx, tx)
	if err != nil {
		return err
	}
	if b.state == "pending" {
		if err := startBiddingRebuild(ctx, tx, c.SettledAt, seq); err != nil {
			return err
		}
		b.state = "scanning"
	}
	if b.state == "completed" {
		return changeTotal(ctx, tx, c.UserID, biddingBoard, "7d", new(big.Int).Neg(c.Loss), c.SettledAt, 1, seq)
	}
	value, err := db.SM128FromBig(c.Loss)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO game_bidding_net_rebuild_events(source_seq,user_id,loss_sign,loss_mag,settled_at) VALUES(?,?,?,?,?) ON CONFLICT(source_seq) DO NOTHING`, seq, c.UserID, int(value.Sign), db.EncodeU128(value.Mag), c.SettledAt)
	return err
}

func advanceBiddingTx(ctx context.Context, tx *sql.Tx, now int64) (bool, error) {
	if ctx == nil || tx == nil || now < 0 {
		return false, ErrInvalid
	}
	ready, err := ReadyTx(ctx, tx, now)
	if err != nil || !ready {
		return false, err
	}
	b, err := readBiddingRebuild(ctx, tx)
	if err != nil {
		return false, err
	}
	if b.state == "completed" {
		return true, nil
	}
	if b.state == "pending" {
		var next []byte
		if err := tx.QueryRowContext(ctx, `SELECT next_event_seq FROM game_rank_counters WHERE id=1`).Scan(&next); err != nil {
			return false, err
		}
		if err := startBiddingRebuild(ctx, tx, now, next); err != nil {
			return false, err
		}
		b.state, b.watermark = "scanning", next
	}
	deadline := time.Now().Add(biddingBatchTime)
	used := 0
	for used < biddingBatchRows && time.Now().Before(deadline) {
		switch b.state {
		case "scanning":
			chunk := min(128, biddingBatchRows-used)
			rows, err := tx.QueryContext(ctx, `SELECT seq,user_id,settled_at,loss_sign,loss_mag FROM game_rank_events WHERE game_key='bidding' AND seq>? AND seq<? ORDER BY seq LIMIT ?`, b.last, b.watermark, chunk)
			if err != nil {
				return false, err
			}
			type event struct {
				seq, mag []byte
				user, at int64
				sign     sql.NullInt64
			}
			items := make([]event, 0, chunk)
			for rows.Next() {
				var e event
				if err := rows.Scan(&e.seq, &e.user, &e.at, &e.sign, &e.mag); err != nil {
					rows.Close()
					return false, err
				}
				items = append(items, e)
			}
			if err := rows.Err(); err != nil {
				rows.Close()
				return false, err
			}
			if err := rows.Close(); err != nil {
				return false, err
			}
			if len(items) == 0 {
				b.state = "publishing"
				if _, err := tx.ExecContext(ctx, `UPDATE game_bidding_net_rebuild SET state='publishing',updated_at=? WHERE id=1`, now); err != nil {
					return false, err
				}
				continue
			}
			for _, e := range items {
				if e.at+week > now {
					if !e.sign.Valid || e.mag == nil {
						b.missing++
					} else {
						loss, err := db.NewSM128(int(e.sign.Int64), e.mag)
						if err != nil {
							return false, err
						}
						if err := changeBiddingScratch(ctx, tx, e.user, new(big.Int).Neg(loss.Big()), e.at, 1, e.seq); err != nil {
							return false, err
						}
					}
				}
				b.last = e.seq
				used++
			}
			if _, err := tx.ExecContext(ctx, `UPDATE game_bidding_net_rebuild SET last_seq=?,missing_events=?,updated_at=? WHERE id=1`, b.last, b.missing, now); err != nil {
				return false, err
			}
		case "publishing":
			count, err := publishBiddingShadow(ctx, tx, biddingBatchRows-used)
			if err != nil {
				return false, err
			}
			used += count
			if count > 0 {
				continue
			}
			count, err = publishBiddingBuffer(ctx, tx, biddingBatchRows-used)
			if err != nil {
				return false, err
			}
			used += count
			if count > 0 {
				continue
			}
			if _, err := tx.ExecContext(ctx, `UPDATE game_bidding_net_rebuild SET state='completed',updated_at=? WHERE id=1 AND state='publishing'`, now); err != nil {
				return false, err
			}
			return true, nil
		default:
			return false, ErrInvalid
		}
	}
	return false, nil
}

func changeBiddingScratch(ctx context.Context, tx *sql.Tx, user int64, delta *big.Int, at int64, phase int, seq []byte) error {
	if delta.Sign() == 0 {
		return nil
	}
	var sign int
	var raw []byte
	var previousAt int64
	var previousPhase int
	var previousSeq []byte
	current := new(big.Int)
	err := tx.QueryRowContext(ctx, `SELECT amount_sign,amount_mag,achieved_at,achieved_phase,achieved_seq FROM game_bidding_net_rebuild_totals WHERE user_id=?`, user).Scan(&sign, &raw, &previousAt, &previousPhase, &previousSeq)
	if err == nil {
		if laterBiddingAchievement(previousAt, previousPhase, previousSeq, at, phase, seq) {
			at, phase, seq = previousAt, previousPhase, previousSeq
		}
		current.SetBytes(raw)
		if sign < 0 {
			current.Neg(current)
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	current.Add(current, delta)
	if current.BitLen() > 256 {
		return ErrInvalid
	}
	mag := new(big.Int).Abs(current).FillBytes(make([]byte, 32))
	_, err = tx.ExecContext(ctx, `INSERT INTO game_bidding_net_rebuild_totals(user_id,amount_sign,amount_mag,achieved_at,achieved_phase,achieved_seq) VALUES(?,?,?,?,?,?) ON CONFLICT(user_id) DO UPDATE SET amount_sign=excluded.amount_sign,amount_mag=excluded.amount_mag,achieved_at=excluded.achieved_at,achieved_phase=excluded.achieved_phase,achieved_seq=excluded.achieved_seq`, user, current.Sign(), mag, at, phase, seq)
	return err
}

// Rebuild chunks can observe source settlements after an interleaved expiry.
// Achievement follows event order, independent of the scanner's work order.
func laterBiddingAchievement(oldAt int64, oldPhase int, oldSeq []byte, at int64, phase int, seq []byte) bool {
	return oldAt > at || oldAt == at && (oldPhase > phase || oldPhase == phase && bytes.Compare(oldSeq, seq) > 0)
}

func publishBiddingShadow(ctx context.Context, tx *sql.Tx, limit int) (int, error) {
	rows, err := tx.QueryContext(ctx, `SELECT user_id,amount_sign,amount_mag,achieved_at,achieved_phase,achieved_seq FROM game_bidding_net_rebuild_totals ORDER BY user_id LIMIT ?`, min(limit, 128))
	if err != nil {
		return 0, err
	}
	type total struct {
		user, at    int64
		sign, phase int
		mag, seq    []byte
	}
	items := []total{}
	for rows.Next() {
		var v total
		if err := rows.Scan(&v.user, &v.sign, &v.mag, &v.at, &v.phase, &v.seq); err != nil {
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
		if err := changeTotal(ctx, tx, v.user, biddingBoard, "7d", amount, v.at, v.phase, v.seq); err != nil {
			return 0, err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM game_bidding_net_rebuild_totals WHERE user_id=?`, v.user); err != nil {
			return 0, err
		}
	}
	return len(items), nil
}

func publishBiddingBuffer(ctx context.Context, tx *sql.Tx, limit int) (int, error) {
	rows, err := tx.QueryContext(ctx, `SELECT source_seq,user_id,loss_sign,loss_mag,settled_at FROM game_bidding_net_rebuild_events ORDER BY source_seq LIMIT ?`, min(limit, 128))
	if err != nil {
		return 0, err
	}
	type event struct {
		seq, mag []byte
		user, at int64
		sign     int
	}
	items := []event{}
	for rows.Next() {
		var e event
		if err := rows.Scan(&e.seq, &e.user, &e.sign, &e.mag, &e.at); err != nil {
			rows.Close()
			return 0, err
		}
		items = append(items, e)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	if err := rows.Close(); err != nil {
		return 0, err
	}
	for _, e := range items {
		loss, err := db.NewSM128(e.sign, e.mag)
		if err != nil {
			return 0, err
		}
		if err := changeTotal(ctx, tx, e.user, biddingBoard, "7d", new(big.Int).Neg(loss.Big()), e.at, 1, e.seq); err != nil {
			return 0, err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM game_bidding_net_rebuild_events WHERE source_seq=?`, e.seq); err != nil {
			return 0, err
		}
	}
	return len(items), nil
}

// Capture an expiring source before its loss columns are cleared. A buffered
// event has never reached the visible board, so removal only drops its buffer.
func captureBiddingExpiry(ctx context.Context, tx *sql.Tx, seq []byte) (bool, error) {
	result, err := tx.ExecContext(ctx, `DELETE FROM game_bidding_net_rebuild_events WHERE source_seq=?`, seq)
	if err != nil {
		return false, err
	}
	removed, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if removed != 0 {
		return false, nil
	}
	b, err := readBiddingRebuild(ctx, tx)
	if err != nil {
		return false, err
	}
	if b.state == "completed" {
		return true, nil
	}
	if b.state == "pending" {
		return false, nil
	}
	if b.state == "scanning" {
		return string(seq) <= string(b.last) && string(seq) < string(b.watermark), nil
	}
	if b.state == "publishing" {
		return string(seq) <= string(b.last) || string(seq) >= string(b.watermark), nil
	}
	return false, ErrInvalid
}

func applyBiddingExpiry(ctx context.Context, tx *sql.Tx, user int64, delta *big.Int, at int64, seq []byte) error {
	if delta.Sign() == 0 {
		return nil
	}
	b, err := readBiddingRebuild(ctx, tx)
	if err != nil {
		return err
	}
	if b.state == "completed" {
		return changeTotal(ctx, tx, user, biddingBoard, "7d", delta, at, 0, seq)
	}
	var present int
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM game_bidding_net_rebuild_totals WHERE user_id=?)`, user).Scan(&present); err != nil {
		return err
	}
	if present == 1 {
		return changeBiddingScratch(ctx, tx, user, delta, at, 0, seq)
	}
	if b.state == "publishing" {
		return changeTotal(ctx, tx, user, biddingBoard, "7d", delta, at, 0, seq)
	}
	return ErrInvalid
}
