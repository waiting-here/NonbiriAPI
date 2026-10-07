package steadycatch

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/game"
	"github.com/waiting-here/NonbiriAPI/internal/game/finance"
	"github.com/waiting-here/NonbiriAPI/internal/game/steadycatch/engine"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
	"github.com/waiting-here/NonbiriAPI/internal/useractivity"
)

type Controls struct {
	Revision int64          `json:"revision"`
	Action   string         `json:"action"`
	Until    int            `json:"until_tick"`
	Inputs   []engine.Input `json:"inputs"`
}

func (s *Service) Start(ctx context.Context, user int64, key string) (View, error) {
	if user <= 0 {
		return View{}, ErrInvalid
	}
	if _, err := idempotency.KeyHash(key); err != nil {
		return View{}, ErrInvalid
	}
	tx, nowMS, err := s.begin(ctx)
	if err != nil {
		return View{}, err
	}
	defer tx.Rollback()
	now := nowMS / 1000
	if err = s.authorizer.AuthorizeUserMutation(ctx, tx, user); err != nil {
		return View{}, err
	}
	actor, _ := idempotency.ActorScopeHash("user", strconv.FormatInt(user, 10))
	digest, _ := idempotency.RequestDigest(idempotency.DigestInput{ActorScopeHash: actor, Method: "POST", Route: "/api/games/steady-catch/sessions", Body: []byte("{}")})
	decision, err := idempotency.Begin(ctx, tx, idempotency.BeginInput{Scope: idempotency.ScopeGameCatch, ActorHash: actor, Key: key, RequestHash: digest, DecisionNow: now})
	if err != nil {
		return View{}, err
	}
	if decision.Kind == idempotency.Replay {
		var prior struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(decision.ResponseBody, &prior) != nil {
			return View{}, ErrUnavailable
		}
		r, err := s.load(ctx, tx, prior.ID, user)
		if err != nil {
			return View{}, err
		}
		r.ServerMS = nowMS
		return r.View, nil
	}
	var maintenance bool
	if err = tx.QueryRowContext(ctx, `SELECT enabled FROM maintenance_state WHERE id=1`).Scan(&maintenance); err != nil {
		return View{}, err
	}
	if maintenance {
		return View{}, resources.ErrMaintenance
	}
	var existing string
	err = tx.QueryRowContext(ctx, `SELECT id FROM game_catch_sessions WHERE user_id=? AND status IN ('playing','paused')`, user).Scan(&existing)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return View{}, err
	}
	if err == nil {
		r, err := s.load(ctx, tx, existing, user)
		if err != nil {
			return View{}, err
		}
		if now < r.ExpiresAt {
			return View{}, ErrConflict
		}
		previous := r.Revision
		r.Revision++
		if err = s.finish(ctx, tx, &r, "abandoned", previous, now); err != nil {
			return View{}, err
		}
	}
	cfg, err := s.settings(ctx, tx)
	if err != nil {
		return View{}, err
	}
	if !cfg.Enabled {
		return View{}, ErrDisabled
	}
	reservation, _, err := s.limiter.ReserveTx(ctx, tx, user)
	if err != nil {
		return View{}, err
	}
	defer reservation.Release()
	id, err := db.GenerateOpaqueID("sc_")
	if err != nil {
		return View{}, err
	}
	op, err := db.GenerateOpaqueID("op_")
	if err != nil {
		return View{}, err
	}
	seed, err := s.seed()
	if err != nil {
		return View{}, err
	}
	state := engine.New(seed)
	body, err := json.Marshal(state)
	if err != nil {
		return View{}, err
	}
	err = s.finance.Start(ctx, tx, finance.SoloStart{Meta: ledger.Meta{OperationID: op, ActorUserID: user, CreatedAt: now}, SessionID: id, UserID: user, Ticket: ledger.AmountFromMilli(cfg.Price), MayReward: cfg.Reward > 0}, func(ctx context.Context, tx *sql.Tx, payment ledger.Payment, hold db.U128) error {
		var receipt any
		if cfg.Price > 0 {
			receipt = op
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO game_catch_sessions(id,user_id,status,revision,seed,engine_json,tick,anchor_tick,anchor_ms,price_milli,general_paid_milli,game_paid_milli,first_reward_milli,entry_operation_id,ledger_rows_remaining,created_at,expires_at,updated_at) VALUES(?,?,'paused',1,?,?,0,0,?,?,?,?,?,?,?,?,?,?)`, id, user, seed, string(body), nowMS, cfg.Price, payment.General.Big().Int64(), payment.Game.Big().Int64(), cfg.Reward, receipt, db.EncodeU128(hold), now, now+sessionSeconds, now)
		return err
	})
	if err != nil {
		return View{}, err
	}
	if err = useractivity.RecordActiveTx(ctx, tx, useractivity.ActiveEvent{UserID: user, At: now, Kind: "game", Fresh: true}); err != nil {
		return View{}, err
	}
	receipt, _ := json.Marshal(struct {
		ID string `json:"id"`
	}{id})
	if err = idempotency.Complete(ctx, tx, decision, 201, receipt); err != nil {
		return View{}, err
	}
	r, err := s.load(ctx, tx, id, user)
	if err != nil {
		return View{}, err
	}
	if err = tx.Commit(); err != nil {
		return View{}, err
	}
	reservation.Commit()
	r.ServerMS = nowMS
	return r.View, nil
}

func (s *Service) Read(ctx context.Context, identity Identity) (*View, error) {
	tx, nowMS, err := s.begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err = s.authorizer.AuthorizeUserMutation(ctx, tx, identity.UserID); err != nil {
		return nil, err
	}
	var id string
	err = tx.QueryRowContext(ctx, `SELECT id FROM game_catch_sessions WHERE user_id=? AND (terminal_at IS NULL OR terminal_at>?) ORDER BY terminal_at IS NULL DESC,created_at DESC,id DESC LIMIT 1`, identity.UserID, nowMS/1000-retentionSeconds).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	r, err := s.load(ctx, tx, id, identity.UserID)
	if err != nil {
		return nil, err
	}
	if err = s.authorizeExisting(ctx, tx, r, identity, "read"); err != nil {
		return nil, err
	}
	if r.TerminalAt == nil && nowMS/1000 >= r.ExpiresAt {
		previous := r.Revision
		r.Revision++
		if err = s.finish(ctx, tx, &r, "abandoned", previous, nowMS/1000); err != nil {
			return nil, err
		}
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	r.ServerMS = nowMS
	return &r.View, nil
}

func (s *Service) Control(ctx context.Context, identity Identity, id string, c Controls) (View, error) {
	if !db.ValidateOpaqueID(id, "sc_") || c.Revision < 1 || c.Revision >= 9007199254740990 || len(c.Inputs) > engine.MaxBatchTicks {
		return View{}, ErrInvalid
	}
	if c.Action != "advance" && c.Action != "pause" && c.Action != "resume" && c.Action != "abandon" {
		return View{}, ErrInvalid
	}
	body, err := json.Marshal(c)
	if err != nil {
		return View{}, err
	}
	digest := sha256.Sum256(body)
	tx, nowMS, err := s.begin(ctx)
	if err != nil {
		return View{}, err
	}
	defer tx.Rollback()
	now := nowMS / 1000
	if err = s.authorizer.AuthorizeUserMutation(ctx, tx, identity.UserID); err != nil {
		return View{}, err
	}
	r, err := s.load(ctx, tx, id, identity.UserID)
	if err != nil {
		return View{}, err
	}
	if err = s.authorizeExisting(ctx, tx, r, identity, "controls"); err != nil {
		return View{}, err
	}
	if bytes.Equal(r.LastHash, digest[:]) {
		r.ServerMS = nowMS
		return r.View, nil
	}
	if r.Revision != c.Revision || r.TerminalAt != nil {
		return View{}, ErrConflict
	}
	previous := r.Revision
	r.Revision++
	r.LastHash = digest[:]
	if now >= r.ExpiresAt {
		err = s.finish(ctx, tx, &r, "abandoned", previous, now)
	} else if c.Action == "abandon" {
		if len(c.Inputs) != 0 || c.Until != r.State.Tick {
			return View{}, ErrInvalid
		}
		err = s.finish(ctx, tx, &r, "abandoned", previous, now)
	} else if c.Action == "resume" {
		if r.Status != "paused" || len(c.Inputs) != 0 || c.Until != r.State.Tick {
			return View{}, ErrConflict
		}
		r.Status = "playing"
		r.AnchorTick = r.State.Tick
		r.AnchorMS = nowMS
		err = s.save(ctx, tx, &r, previous, now, "")
	} else {
		if r.Status != "playing" || c.Until < r.State.Tick {
			return View{}, ErrConflict
		}
		// The resumed clock is anchored by the server; clients cannot advance time.
		allowed := r.AnchorTick + int(max(int64(0), nowMS-r.AnchorMS)*engine.Hz/1000)
		if c.Until > allowed {
			return View{}, ErrInvalid
		}
		if c.Until > r.State.Tick {
			r.State, err = engine.Advance(r.State, c.Inputs, c.Until)
			if err != nil {
				return View{}, ErrInvalid
			}
			inputs, _ := json.Marshal(c.Inputs)
			_, err = tx.ExecContext(ctx, `INSERT INTO game_catch_inputs(session_id,until_tick,controls_json) VALUES(?,?,?)`, id, c.Until, string(inputs))
			if err != nil {
				return View{}, err
			}
		} else if c.Action != "pause" || len(c.Inputs) > 0 {
			return View{}, ErrInvalid
		}
		if r.State.Finished() {
			status := "failed"
			if r.State.Cleared() {
				status = "completed"
			}
			err = s.finish(ctx, tx, &r, status, previous, now)
		} else {
			if c.Action == "pause" {
				r.Status = "paused"
				r.AnchorTick = r.State.Tick
				r.AnchorMS = nowMS
			}
			err = s.save(ctx, tx, &r, previous, now, "")
		}
	}
	if err != nil {
		return View{}, err
	}
	if err = tx.Commit(); err != nil {
		return View{}, err
	}
	r.ServerMS = nowMS
	r.Reward = game.FormatAmount(r.RewardPaid)
	return r.View, nil
}
