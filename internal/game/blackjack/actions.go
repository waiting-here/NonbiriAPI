package blackjack

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"strconv"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/game"
	"github.com/waiting-here/NonbiriAPI/internal/game/blackjack/config"
	"github.com/waiting-here/NonbiriAPI/internal/game/blackjack/engine"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
)

type enqueueBody struct {
	Stake      string `json:"stake"`
	ConfigHash string `json:"config_hash"`
}
type actionBody struct {
	Hand     int    `json:"hand"`
	Revision string `json:"revision"`
	Action   string `json:"action"`
}
type emoteBody struct {
	Emote string `json:"emote"`
}

func (s *Service) Enqueue(ctx context.Context, in EnqueueInput) (MutationResult, error) {
	stake, err := game.ParseAmount(in.Stake)
	if err != nil || stake <= 0 || len(in.ConfigHash) != 64 {
		return MutationResult{}, ErrInvalid
	}
	tx, now, err := s.begin(ctx)
	if err != nil {
		return MutationResult{}, err
	}
	defer tx.Rollback()
	if err := s.authorize(ctx, tx, in.Identity); err != nil {
		return MutationResult{}, err
	}
	d, err := s.replay(ctx, tx, in.Identity, in.Key, "POST", "/api/games/blackjack/queue", "", enqueueBody{in.Stake, in.ConfigHash}, now)
	if err != nil {
		return MutationResult{}, err
	}
	if d.Kind == idempotency.Replay {
		return replayResult(d), nil
	}
	facts, err := s.progress(ctx, tx, now, true)
	if err != nil {
		return MutationResult{}, err
	}
	e, err := currentEntry(ctx, tx, in.UserID)
	if err == nil {
		if e.Stake != stake {
			return MutationResult{}, ErrConflict
		}
		position, err := queuePosition(ctx, tx, e)
		if err != nil {
			return MutationResult{}, err
		}
		result, err := finish(ctx, tx, d, 200, QueueReceipt{e.ID, strconv.FormatInt(position, 10), e.State})
		if err == nil {
			s.publish(ctx, facts)
		}
		return result, err
	}
	if !noRows(err) {
		return MutationResult{}, err
	}
	cfg, err := s.config(ctx, tx)
	if err != nil {
		return MutationResult{}, err
	}
	maintenance, err := maintenanceOn(ctx, tx)
	if err != nil {
		return MutationResult{}, err
	}
	if maintenance {
		return MutationResult{}, ErrMaintenance
	}
	if !cfg.Enabled || !cfg.Accepts(stake) || configurationHash(cfg) != in.ConfigHash {
		return MutationResult{}, ErrConflict
	}
	ok, err := eligible(ctx, tx, in.UserID, now)
	if err != nil {
		return MutationResult{}, err
	}
	if !ok {
		return MutationResult{}, ErrForbidden
	}
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM game_blackjack_entries WHERE state='waiting'`).Scan(&count); err != nil {
		return MutationResult{}, err
	}
	if count >= config.QueueCapacity {
		return MutationResult{}, ErrLimit
	}
	start, _, err := s.limiter.Reserve(in.UserID)
	if err != nil {
		if errors.Is(err, game.ErrStartRateLimited) {
			return MutationResult{}, ErrRateLimited
		}
		return MutationResult{}, ErrUnavailable
	}
	defer start.Release()
	id, err := s.generate("bjq_")
	if err != nil {
		return MutationResult{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO game_blackjack_entries(id,user_id,state,stake_milli,platform_bp,welfare_bp,thursday_bp,created_at) VALUES(?,?,'waiting',?,?,?,?,?)`, id, in.UserID, stake, cfg.Rake.Platform, cfg.Rake.Welfare, cfg.Rake.Thursday, now); err != nil {
		return MutationResult{}, err
	}
	e, err = readEntry(ctx, tx, id)
	if err != nil {
		return MutationResult{}, err
	}
	if err := s.reservePayment(ctx, tx, e, "base", 0, now); err != nil {
		return MutationResult{}, err
	}
	more, err := s.progress(ctx, tx, now, true)
	if err != nil {
		return MutationResult{}, err
	}
	mergeFacts(&facts, more)
	facts.AccountIDs = append(facts.AccountIDs, in.UserID)
	e, err = readEntry(ctx, tx, id)
	if err != nil {
		return MutationResult{}, err
	}
	position, err := queuePosition(ctx, tx, e)
	if err != nil {
		return MutationResult{}, err
	}
	result, err := finish(ctx, tx, d, 201, QueueReceipt{e.ID, strconv.FormatInt(position, 10), e.State})
	if err == nil {
		s.publish(ctx, facts)
	}
	return result, err
}

func (s *Service) Leave(ctx context.Context, identity Identity, key, id string) (MutationResult, error) {
	if !db.ValidateOpaqueID(id, "bjq_") {
		return MutationResult{}, ErrInvalid
	}
	tx, now, err := s.begin(ctx)
	if err != nil {
		return MutationResult{}, err
	}
	defer tx.Rollback()
	if err := s.authorize(ctx, tx, identity); err != nil {
		return MutationResult{}, err
	}
	e, err := readEntry(ctx, tx, id)
	if err != nil {
		return MutationResult{}, classify(err)
	}
	if !e.User.Valid || e.User.Int64 != identity.UserID {
		return MutationResult{}, ErrNotFound
	}
	d, err := s.replay(ctx, tx, identity, key, "DELETE", "/api/games/blackjack/queue/{id}", id, struct{}{}, now)
	if err != nil {
		return MutationResult{}, err
	}
	if d.Kind == idempotency.Replay {
		return replayResult(d), nil
	}
	facts, err := s.progress(ctx, tx, now, true)
	if err != nil {
		return MutationResult{}, err
	}
	e, err = readEntry(ctx, tx, id)
	if err != nil {
		return MutationResult{}, err
	}
	if e.State != "released" {
		if e.State != "waiting" && e.State != "seated" {
			return s.finishConflict(ctx, tx, d, facts)
		}
		if err := s.releaseEntry(ctx, tx, e, now); err != nil {
			return MutationResult{}, err
		}
		facts.AccountIDs = append(facts.AccountIDs, identity.UserID)
	}
	// Empty seating reservations have no game history.
	if _, err := tx.ExecContext(ctx, `DELETE FROM game_blackjack_sessions WHERE phase='seating' AND NOT EXISTS(SELECT 1 FROM game_blackjack_entries e WHERE e.session_id=game_blackjack_sessions.id)`); err != nil {
		return MutationResult{}, err
	}
	more, err := s.progress(ctx, tx, now, true)
	if err != nil {
		return MutationResult{}, err
	}
	mergeFacts(&facts, more)
	result, err := finish(ctx, tx, d, 204, nil)
	if err == nil {
		s.publish(ctx, facts)
	}
	return result, err
}

func participant(ctx context.Context, tx *sql.Tx, session string, user int64) (entryRecord, error) {
	return scanEntry(tx.QueryRowContext(ctx, `SELECT `+entryColumns+` FROM game_blackjack_entries WHERE session_id=? AND user_id=? AND state IN ('seated','playing','settled','released') ORDER BY ordinal DESC LIMIT 1`, session, user))
}

func (s *Service) Act(ctx context.Context, in ActionInput) (MutationResult, error) {
	revision, err := strconv.ParseInt(in.Revision, 10, 64)
	if err != nil || revision <= 0 || strconv.FormatInt(revision, 10) != in.Revision || in.Hand < 0 || in.Hand > 1 || !db.ValidateOpaqueID(in.SessionID, "bjt_") || !slices.Contains([]string{"hit", "stand", "double", "split"}, in.Action) {
		return MutationResult{}, ErrInvalid
	}
	tx, now, err := s.begin(ctx)
	if err != nil {
		return MutationResult{}, err
	}
	defer tx.Rollback()
	if err := s.authorize(ctx, tx, in.Identity); err != nil {
		return MutationResult{}, err
	}
	if _, err := participant(ctx, tx, in.SessionID, in.UserID); err != nil {
		return MutationResult{}, classify(err)
	}
	d, err := s.replay(ctx, tx, in.Identity, in.Key, "POST", "/api/games/blackjack/sessions/{id}/actions", in.SessionID, actionBody{in.Hand, in.Revision, in.Action}, now)
	if err != nil {
		return MutationResult{}, err
	}
	if d.Kind == idempotency.Replay {
		return replayResult(d), nil
	}
	facts, err := s.progress(ctx, tx, now, true)
	if err != nil {
		return MutationResult{}, err
	}
	v, err := readSession(ctx, tx, in.SessionID)
	if err != nil {
		return MutationResult{}, classify(err)
	}
	e, err := participant(ctx, tx, in.SessionID, in.UserID)
	if err != nil {
		return MutationResult{}, classify(err)
	}
	if v.Phase != "decision" || now >= v.StartedAt+engine.DecisionEnd || e.State != "playing" {
		return s.finishConflict(ctx, tx, d, facts)
	}
	if e.Pending.Valid || e.Stopped {
		return MutationResult{}, ErrConflict
	}
	if err := s.authorizeExisting(ctx, tx, v, in.Identity, "action", now); err != nil {
		return MutationResult{}, err
	}
	state, err := decodeState(v)
	if err != nil {
		return MutationResult{}, err
	}
	a := engine.Action{Seat: int(e.Seat.Int64), Hand: in.Hand, Revision: revision, Kind: in.Action}
	additional, err := state.AdditionalUnits(a)
	if err != nil {
		return MutationResult{}, ErrConflict
	}
	if additional > 0 {
		if err := s.reservePayment(ctx, tx, e, in.Action, in.Hand, now); err != nil {
			return MutationResult{}, err
		}
		facts.AccountIDs = append(facts.AccountIDs, in.UserID)
	}
	body, err := marshal(a)
	if err != nil {
		return MutationResult{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE game_blackjack_entries SET pending_json=?,pending_batch=? WHERE id=? AND pending_json IS NULL AND state='playing'`, string(body), now+1, e.ID); err != nil {
		return MutationResult{}, err
	}
	result, err := finish(ctx, tx, d, 202, ActionReceipt{v.ID, now + 1, in.Hand, in.Revision})
	if err == nil {
		s.publish(ctx, facts)
	}
	return result, err
}

func (s *Service) Emote(ctx context.Context, identity Identity, key, id, emote string) (MutationResult, error) {
	if !db.ValidateOpaqueID(id, "bjt_") || !slices.Contains([]string{"hello", "nice", "wow", "good_luck", "thanks", "gg"}, emote) {
		return MutationResult{}, ErrInvalid
	}
	tx, now, err := s.begin(ctx)
	if err != nil {
		return MutationResult{}, err
	}
	defer tx.Rollback()
	if err := s.authorize(ctx, tx, identity); err != nil {
		return MutationResult{}, err
	}
	e, err := participant(ctx, tx, id, identity.UserID)
	if err != nil {
		return MutationResult{}, classify(err)
	}
	d, err := s.replay(ctx, tx, identity, key, "POST", "/api/games/blackjack/sessions/{id}/emotes", id, emoteBody{emote}, now)
	if err != nil {
		return MutationResult{}, err
	}
	if d.Kind == idempotency.Replay {
		return replayResult(d), nil
	}
	v, err := readSession(ctx, tx, id)
	if err != nil {
		return MutationResult{}, classify(err)
	}
	if now >= v.StartedAt+engine.RoundSeconds || v.Phase == "cancelled" || e.Stopped || e.State == "released" {
		return MutationResult{}, ErrConflict
	}
	if e.EmoteAt.Valid && now < e.EmoteAt.Int64+2 {
		return MutationResult{}, ErrRateLimited
	}
	if err := s.authorizeExisting(ctx, tx, v, identity, "emote", now); err != nil {
		return MutationResult{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE game_blackjack_entries SET emote=?,emote_at=? WHERE id=?`, emote, now, e.ID); err != nil {
		return MutationResult{}, err
	}
	return finish(ctx, tx, d, 204, nil)
}
