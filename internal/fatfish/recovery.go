package fatfish

import (
	"context"
	"errors"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/lifecycle"
)

const summaryLifetime = 30 * 24 * time.Hour

func validSweep(now int64, limit int, deadline time.Time) bool {
	return now >= 0 && now <= maximumUnix && limit >= 1 && limit <= 100 && !deadline.IsZero()
}

func (s *Service) recover(ctx context.Context, now int64, limit int, deadline time.Time) (lifecycle.WorkResult, error) {
	if s == nil || !validSweep(now, limit, deadline) {
		return lifecycle.WorkResult{}, ErrInvalid
	}
	nowMS := now * 1000
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM fatfish_challenges WHERE
 (state='prepared' AND prepare_until_ms<=?) OR
 (state='active' AND submit_until_ms<?) OR
 (state='verifying' AND (verified_result_json IS NOT NULL OR received_at_ms+600000<?))
 ORDER BY prepared_at_ms,id LIMIT ?`, nowMS, nowMS, nowMS, limit+1)
	if err != nil {
		return lifecycle.WorkResult{}, err
	}
	ids := make([]string, 0, limit+1)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return lifecycle.WorkResult{}, err
		}
		ids = append(ids, id)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return lifecycle.WorkResult{}, err
	}
	rows.Close()
	more := len(ids) > limit
	if more {
		ids = ids[:limit]
	}
	processed := 0
	for _, id := range ids {
		if err = ctx.Err(); err != nil {
			return lifecycle.WorkResult{Processed: processed, More: true}, err
		}
		if !time.Now().Before(deadline) {
			return lifecycle.WorkResult{Processed: processed, More: true}, nil
		}
		if err = s.recoverOne(ctx, id, nowMS); err != nil {
			return lifecycle.WorkResult{Processed: processed, More: true}, err
		}
		processed++
	}
	return lifecycle.WorkResult{Processed: processed, More: more}, nil
}

func (s *Service) recoverOne(ctx context.Context, id string, nowMS int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	c, err := readChallengeTx(ctx, tx, id)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	switch c.state {
	case "prepared":
		if c.prepareUntil > nowMS {
			return tx.Commit()
		}
		if err = s.cancelChallengeTx(ctx, tx, c, nowMS, "prepare_expired", false); err != nil {
			return err
		}
		return tx.Commit()
	case "active":
		if !c.submitUntil.Valid || c.submitUntil.Int64 >= nowMS {
			return tx.Commit()
		}
		res, err := tx.ExecContext(ctx, `UPDATE fatfish_challenges SET state='expired',terminal_at_ms=?,terminal_reason='submit_expired',revision=revision+1 WHERE id=? AND state='active' AND revision=?`, nowMS, id, c.revision)
		if err != nil {
			return err
		}
		affected, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if affected != 1 {
			return ErrConflict
		}
		return tx.Commit()
	case "verifying":
		if err = tx.Commit(); err != nil {
			return err
		}
		if c.resultJSON.Valid {
			return s.settleVerified(ctx, id)
		}
		if c.received.Valid && c.received.Int64+600000 < nowMS {
			return s.refundSystemFault(ctx, id, "verification_unrecoverable")
		}
		return nil
	default:
		return tx.Commit()
	}
}

func (s *Service) retain(ctx context.Context, now int64, limit int, deadline time.Time) (lifecycle.WorkResult, error) {
	if s == nil || !validSweep(now, limit, deadline) {
		return lifecycle.WorkResult{}, ErrInvalid
	}
	cutoff := now*1000 - int64(summaryLifetime/time.Millisecond)
	if cutoff < 0 {
		return lifecycle.WorkResult{}, nil
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM fatfish_challenges WHERE terminal_at_ms IS NOT NULL AND terminal_at_ms<? ORDER BY terminal_at_ms,id LIMIT ?`, cutoff, limit+1)
	if err != nil {
		return lifecycle.WorkResult{}, err
	}
	ids := make([]string, 0, limit+1)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return lifecycle.WorkResult{}, err
		}
		ids = append(ids, id)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return lifecycle.WorkResult{}, err
	}
	rows.Close()
	more := len(ids) > limit
	if more {
		ids = ids[:limit]
	}
	processed := 0
	for _, id := range ids {
		if err = ctx.Err(); err != nil {
			return lifecycle.WorkResult{Processed: processed, More: true}, err
		}
		if !time.Now().Before(deadline) {
			return lifecycle.WorkResult{Processed: processed, More: true}, nil
		}
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return lifecycle.WorkResult{Processed: processed, More: true}, err
		}
		res, err := tx.ExecContext(ctx, `DELETE FROM fatfish_challenges WHERE id=? AND terminal_at_ms IS NOT NULL AND terminal_at_ms<?`, id, cutoff)
		if err != nil {
			tx.Rollback()
			return lifecycle.WorkResult{Processed: processed, More: true}, err
		}
		affected, err := res.RowsAffected()
		if err != nil {
			tx.Rollback()
			return lifecycle.WorkResult{Processed: processed, More: true}, err
		}
		if err = tx.Commit(); err != nil {
			return lifecycle.WorkResult{Processed: processed, More: true}, err
		}
		processed += int(affected)
	}
	return lifecycle.WorkResult{Processed: processed, More: more}, nil
}
