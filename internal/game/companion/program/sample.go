package program

import (
	"encoding/json"

	"github.com/waiting-here/NonbiriAPI/internal/game/likes/engine"
	"github.com/waiting-here/NonbiriAPI/internal/httpapi"
)

// The published Likes fixture is a training game with both loadouts revealed
// in its sample and its complete preceding round in recent. Rebuild that
// particular position so arbitrary legal purchases and plans can be submitted.
func validLikesSample(sample, action json.RawMessage) bool {
	var req struct {
		Seat   int
		Mode   string
		Round  int
		View   engine.View
		Recent []struct{ Plans [2]engine.Plan }
	}
	var reply struct {
		Kind string
		Plan *engine.Plan
	}
	var shape struct{ Plan map[string]json.RawMessage }
	if json.Unmarshal(sample, &req) != nil || req.Seat < 0 || req.Seat > 1 ||
		httpapi.DecodeJSON(action, &reply) != nil || reply.Kind != "plan" || reply.Plan == nil ||
		reply.Plan.Purchases == nil || reply.Plan.Extra == nil || json.Unmarshal(action, &shape) != nil || shape.Plan["main"] == nil {
		return false
	}
	rules, err := engine.New(req.Mode)
	if err != nil {
		return false
	}
	var selections [2]engine.Selection
	for i, p := range req.View.Players {
		selections[i] = engine.Selection{Role: p.Role, Harness: p.Harness, Skills: p.Revealed}
	}
	state, err := rules.Create(selections)
	if err != nil {
		return false
	}
	for _, previous := range req.Recent {
		state, _, err = rules.Resolve(state, previous.Plans, func(int) (int, error) { return 0, nil })
		if err != nil || state.Result != nil {
			return false
		}
		if state.AwaitingNextRound {
			state, _, err = rules.BeginNextRound(state)
			if err != nil {
				return false
			}
		}
	}
	if state.Round != int64(req.Round) {
		return false
	}
	_, err = rules.ValidateManualPlan(state, req.Seat, *reply.Plan)
	return err == nil
}
