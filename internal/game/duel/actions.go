package duel

import (
	"context"
	"encoding/json"

	"github.com/waiting-here/NonbiriAPI/internal/activities"
	"github.com/waiting-here/NonbiriAPI/internal/db"
)

type actionBody struct {
	PhaseSeq string          `json:"phase_seq"`
	Action   json.RawMessage `json:"action"`
}
type surrenderBody struct {
	PhaseSeq string `json:"phase_seq"`
}

func (s *Service) Action(ctx context.Context, in ActionInput) (MutationResult, error) {
	return s.act(ctx, in, false)
}
func (s *Service) Surrender(ctx context.Context, in ActionInput) (MutationResult, error) {
	if len(in.Action) > 0 {
		return MutationResult{}, ErrInvalidRequest
	}
	return s.act(ctx, in, true)
}
func (s *Service) act(ctx context.Context, in ActionInput, surrender bool) (MutationResult, error) {
	seq, err := db.ParseU128Decimal(in.PhaseSeq)
	if err != nil || seq.Big().Sign() <= 0 || !db.ValidateOpaqueID(in.SessionID, s.sessionPrefix) {
		return MutationResult{}, ErrInvalidRequest
	}
	if !surrender && (len(in.Action) == 0 || len(in.Action) > 16384 || validateJSON(in.Action) != nil) {
		return MutationResult{}, ErrInvalidRequest
	}
	tx, now, err := s.begin(ctx)
	if err != nil {
		return MutationResult{}, err
	}
	defer tx.Rollback()
	if err := s.authorize(ctx, tx, in.Identity); err != nil {
		return MutationResult{}, err
	}
	route := "/api/games/" + s.rules.ID() + "/sessions/{id}/actions"
	var body any = actionBody{PhaseSeq: in.PhaseSeq, Action: in.Action}
	if surrender {
		route = "/api/games/" + s.rules.ID() + "/sessions/{id}/surrender"
		body = surrenderBody{PhaseSeq: in.PhaseSeq}
	}
	if replay, err := s.probeReplay(ctx, tx, in.Identity, in.IdempotencyKey, "POST", route, in.SessionID, body, now); err != nil {
		return MutationResult{}, err
	} else if replay != nil {
		return *replay, nil
	}
	v, err := s.session(ctx, tx, in.SessionID)
	if err != nil {
		return MutationResult{}, notFound(err)
	}
	seat, err := s.seat(&v, in.UserID)
	if err != nil {
		return MutationResult{}, err
	}
	operation := "action"
	if surrender {
		operation = "surrender"
	}
	if err := s.authorizeExisting(ctx, tx, v, in.Identity, operation, now); err != nil {
		return MutationResult{}, err
	}
	if v.State != "active" {
		return MutationResult{}, ErrConflict
	}
	if facts, changed, err := s.advance(ctx, tx, &v, now); err != nil {
		return MutationResult{}, classify(err)
	} else if changed {
		if err := tx.Commit(); err != nil {
			return MutationResult{}, classify(err)
		}
		s.publish(ctx, facts)
		return MutationResult{}, ErrConflict
	}
	if v.PhaseSeq != seq || !surrender && (v.Phase == "settlement" || v.Seats[seat].Locked) {
		return MutationResult{}, ErrConflict
	}
	var action json.RawMessage
	if !surrender {
		action, err = s.rules.Accept(v.Mode, v.Payload.Rules, seat, in.Action)
		if err != nil {
			return MutationResult{}, err
		}
	}
	done, err := s.reserveAction(in.UserID)
	if err != nil {
		return MutationResult{}, err
	}
	committed := false
	defer func() { done(committed) }()
	expected := v.Revision
	v.Revision, err = increment(v.Revision)
	if err != nil {
		return MutationResult{}, err
	}
	var facts activities.PublishFacts
	if surrender {
		winner := 1 - seat
		v.PhaseSeq, err = increment(v.PhaseSeq)
		if err != nil {
			return MutationResult{}, err
		}
		facts, err = s.terminal(ctx, tx, &v, expected, now, &winner, "surrender", false)
	} else {
		v.Seats[seat].Action = action
		v.Seats[seat].Locked = true
		if v.Seats[0].Locked && v.Seats[1].Locked {
			facts, err = s.resolve(ctx, tx, &v, expected, now)
		} else {
			err = s.saveSession(ctx, tx, &v, expected)
		}
	}
	if err != nil {
		return MutationResult{}, classify(err)
	}
	d, err := s.replay(ctx, tx, in.Identity, in.IdempotencyKey, "POST", route, in.SessionID, body, now)
	if err != nil {
		return MutationResult{}, err
	}
	result, err := finish(ctx, tx, d, 200, ActionReceipt{SessionID: v.ID, Revision: v.Revision.Decimal(), PhaseSeq: v.PhaseSeq.Decimal(), Locked: v.Seats[seat].Locked})
	if err != nil {
		return MutationResult{}, err
	}
	committed = true
	s.publish(ctx, facts)
	return result, nil
}
