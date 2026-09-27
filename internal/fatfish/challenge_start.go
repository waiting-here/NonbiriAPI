package fatfish

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
)

type StartInput struct {
	TabCapability string `json:"tab_capability"`
}
type AbandonInput struct {
	TabCapability string `json:"tab_capability"`
}

func (s *Service) Start(ctx context.Context, userID int64, id string, input StartInput, key string) (ChallengeView, error) {
	nowMS, err := s.nowMS()
	if err != nil {
		return ChallengeView{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ChallengeView{}, err
	}
	defer tx.Rollback()
	if err = s.authorizeUserTx(ctx, tx, userID, nowMS, false); err != nil {
		return ChallengeView{}, err
	}
	c, err := readChallengeTx(ctx, tx, id)
	if err != nil {
		return ChallengeView{}, err
	}
	if c.userID != userID || c.playtest {
		return ChallengeView{}, ErrNotFound
	}
	if !sameCapability(c.capabilityHash, input.TabCapability) {
		return ChallengeView{}, ErrForbidden
	}
	decision, err := s.beginMutationTx(ctx, tx, "user", userID, key, http.MethodPost, "/challenges/{id}/start", []string{id}, input, nowMS)
	if err != nil {
		return ChallengeView{}, err
	}
	if _, ok, replayErr := replayMutation[ChallengeView](decision); replayErr != nil {
		return ChallengeView{}, replayErr
	} else if ok {
		view, err := c.view(nowMS, input.TabCapability)
		if err != nil {
			return ChallengeView{}, err
		}
		return view, tx.Commit()
	}
	if c.state == "prepared" {
		if nowMS >= c.prepareUntil {
			return ChallengeView{}, ErrClosed
		}
		if err = s.admissionTx(ctx, tx, userID, nowMS); err != nil {
			return ChallengeView{}, err
		}
		n, err := readNodeSnapshotTx(ctx, tx, c.periodID, c.nodeID)
		if err != nil {
			return ChallengeView{}, err
		}
		if n.revision != c.nodeRevision || n.versionID != c.versionID {
			return ChallengeView{}, ErrConflict
		}
		if !availablePeriod(n.periodState, n.periodStart, n.periodEnd, nowMS) || n.periodPaused {
			return ChallengeView{}, ErrClosed
		}
		var unlocked int
		err = tx.QueryRowContext(ctx, `SELECT 1 FROM fatfish_progress WHERE user_id=? AND period_id=? AND node_id=?`, userID, c.periodID, c.nodeID).Scan(&unlocked)
		if errors.Is(err, sql.ErrNoRows) {
			return ChallengeView{}, ErrForbidden
		}
		if err != nil {
			return ChallengeView{}, err
		}
		op, err := s.chargeTx(ctx, tx, userID, nowMS, "ticket", ticketReceiptKey(c.id), c.price)
		if err != nil {
			return ChallengeView{}, err
		}
		startAt := nowMS + 3000
		endAt := startAt + int64(c.durationSeconds)*1000
		submitUntil := endAt + 1800000
		res, err := tx.ExecContext(ctx, `UPDATE fatfish_challenges SET state='active',start_at_ms=?,end_at_ms=?,submit_until_ms=?,ticket_operation_id=?,revision=revision+1
 WHERE id=? AND state='prepared' AND revision=?`, startAt, endAt, submitUntil, op, c.id, c.revision)
		if err != nil {
			return ChallengeView{}, err
		}
		count, err := res.RowsAffected()
		if err != nil {
			return ChallengeView{}, err
		}
		if count != 1 {
			return ChallengeView{}, ErrConflict
		}
		c, err = readChallengeTx(ctx, tx, id)
		if err != nil {
			return ChallengeView{}, err
		}
	} else if c.state != "active" && c.state != "verifying" && c.state != "settled_pass" && c.state != "settled_fail" {
		return ChallengeView{}, ErrClosed
	}
	view, err := c.view(nowMS, input.TabCapability)
	if err != nil {
		return ChallengeView{}, err
	}
	receipt := view
	receipt.Level = nil
	receipt.Seed = ""
	if err = completeMutationTx(ctx, tx, decision, receipt, http.StatusOK); err != nil {
		return ChallengeView{}, err
	}
	return view, tx.Commit()
}

func (s *Service) Abandon(ctx context.Context, userID int64, id string, input AbandonInput, key string) (ChallengeView, error) {
	nowMS, err := s.nowMS()
	if err != nil {
		return ChallengeView{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ChallengeView{}, err
	}
	defer tx.Rollback()
	if err = s.authorizeUserTx(ctx, tx, userID, nowMS, false); err != nil {
		return ChallengeView{}, err
	}
	c, err := readChallengeTx(ctx, tx, id)
	if err != nil {
		return ChallengeView{}, err
	}
	if c.userID != userID || c.playtest {
		return ChallengeView{}, ErrNotFound
	}
	decision, err := s.beginMutationTx(ctx, tx, "user", userID, key, http.MethodPost, "/challenges/{id}/abandon", []string{id}, input, nowMS)
	if err != nil {
		return ChallengeView{}, err
	}
	if replay, ok, replayErr := replayMutation[ChallengeView](decision); ok || replayErr != nil {
		return replay, replayErr
	}
	if c.state == "verifying" && c.resultJSON.Valid {
		return ChallengeView{}, ErrConflict
	}
	if c.state == "prepared" || c.state == "active" || c.state == "verifying" {
		res, err := tx.ExecContext(ctx, `UPDATE fatfish_challenges SET state='abandoned',terminal_at_ms=?,terminal_reason='user_abandoned',revision=revision+1
 WHERE id=? AND state=? AND revision=?`, nowMS, id, c.state, c.revision)
		if err != nil {
			return ChallengeView{}, err
		}
		count, err := res.RowsAffected()
		if err != nil {
			return ChallengeView{}, err
		}
		if count != 1 {
			return ChallengeView{}, ErrConflict
		}
		c, err = readChallengeTx(ctx, tx, id)
		if err != nil {
			return ChallengeView{}, err
		}
	}
	view, err := c.view(nowMS, "")
	if err != nil {
		return ChallengeView{}, err
	}
	if err = completeMutationTx(ctx, tx, decision, view, http.StatusOK); err != nil {
		return ChallengeView{}, err
	}
	if err = tx.Commit(); err != nil {
		return ChallengeView{}, err
	}
	s.cancelJob(id)
	return view, nil
}
