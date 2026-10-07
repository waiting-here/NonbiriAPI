package duel

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/waiting-here/NonbiriAPI/internal/activities"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/game/randomness"
)

func (s *Service) step(ctx context.Context, tx *sql.Tx, v *sessionRecord, expected db.U128, seat int, action json.RawMessage, timeout bool, now int64) (activities.PublishFacts, error) {
	rules, ok := v.rules.(SequentialRules)
	if !ok {
		return activities.PublishFacts{}, ErrInvariant
	}
	secret, err := randomness.Load(ctx, tx, s.rules.ID(), v.ID)
	if err != nil {
		return activities.PublishFacts{}, err
	}
	if secret == nil {
		return activities.PublishFacts{}, ErrInvariant
	}
	stream, err := secret.Stream("actions")
	if err != nil {
		return activities.PublishFacts{}, err
	}
	next, err := rules.Step(v.Mode, v.Payload.Rules, seat, action, timeout, stream)
	if err != nil {
		return activities.PublishFacts{}, err
	}
	if err := randomness.Save(ctx, tx, secret); err != nil {
		return activities.PublishFacts{}, err
	}
	for _, round := range next.Rounds {
		body, err := Encode(roundRecord{Round: round.Round, Before: round.Before, After: round.After, Facts: round.Facts, Timeouts: v.Payload.RoundTimeouts})
		if err != nil {
			return activities.PublishFacts{}, err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO game_duel_rounds(session_id,round_no,record_json) VALUES(?,?,?)`, v.ID, round.Round, string(body)); err != nil {
			return activities.PublishFacts{}, err
		}
		v.Payload.RoundTimeouts = [2]bool{}
	}
	deadline := v.Deadline
	v.Payload.Rules = next.State
	if err := s.enterPhase(v, now, true); err != nil {
		return activities.PublishFacts{}, err
	}
	if next.KeepDeadline && v.Deadline != nil {
		v.Deadline = deadline
	}
	info, err := v.rules.Inspect(v.Mode, v.Payload.Rules)
	if err != nil {
		return activities.PublishFacts{}, err
	}
	if info.Result != nil {
		return s.terminal(ctx, tx, v, expected, now, info.Result.Winner, info.Result.Reason, false)
	}
	return activities.PublishFacts{}, s.saveSession(ctx, tx, v, expected)
}
