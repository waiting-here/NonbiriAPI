package likes

import (
	"encoding/json"

	"github.com/waiting-here/NonbiriAPI/internal/game/duel"
	"github.com/waiting-here/NonbiriAPI/internal/game/likes/catalog"
	"github.com/waiting-here/NonbiriAPI/internal/game/likes/engine"
)

type Rules struct{ engines map[string]*engine.Engine }

var _ duel.Rules = (*Rules)(nil)

func NewRules() (*Rules, error) {
	r := &Rules{engines: map[string]*engine.Engine{}}
	for _, mode := range []string{"quick", "standard"} {
		e, err := engine.New(mode)
		if err != nil {
			return nil, err
		}
		r.engines[mode] = e
	}
	return r, nil
}
func (*Rules) ID() string { return "likes" }
func (r *Rules) Catalog(mode string) (duel.Catalog, error) {
	if r.engines[mode] == nil {
		return duel.Catalog{}, duel.ErrInvalidRequest
	}
	c, err := catalog.Public(mode)
	if err != nil {
		return duel.Catalog{}, duel.ErrInvariant
	}
	body, err := duel.Encode(c)
	return duel.Catalog{Hash: c.ContentHash, DesignVersion: c.DesignVersion, SchemaVersion: c.SchemaVersion, JSON: body}, err
}
func (r *Rules) Loadout(mode string, raw json.RawMessage) (json.RawMessage, error) {
	e := r.engines[mode]
	if e == nil {
		return nil, duel.ErrInvalidRequest
	}
	var selection engine.Selection
	if !duel.HasFields(raw, "role", "harness", "skills") || duel.Decode(raw, &selection) != nil || e.ValidateSelection(selection) != nil {
		return nil, duel.ErrInvalidRequest
	}
	return duel.Encode(selection)
}
func (r *Rules) Create(mode string, loadouts [2]json.RawMessage) (json.RawMessage, error) {
	var selections [2]engine.Selection
	for seat, raw := range loadouts {
		body, err := r.Loadout(mode, raw)
		if err != nil {
			return nil, err
		}
		_ = json.Unmarshal(body, &selections[seat])
	}
	s, err := r.engines[mode].Create(selections)
	if err != nil {
		return nil, duel.ErrInvariant
	}
	return duel.Encode(s)
}
func (r *Rules) state(mode string, raw json.RawMessage) (engine.State, error) {
	e := r.engines[mode]
	if e == nil {
		return engine.State{}, duel.ErrInvalidRequest
	}
	var s engine.State
	if duel.Decode(raw, &s) != nil || e.Validate(s) != nil || s.Mode != mode {
		return s, duel.ErrInvariant
	}
	return s, nil
}
func (r *Rules) Inspect(mode string, raw json.RawMessage) (duel.RuleInfo, error) {
	s, err := r.state(mode, raw)
	if err != nil {
		return duel.RuleInfo{}, err
	}
	i := duel.RuleInfo{Round: int(s.Round), Phase: "plan", Seconds: 20, Required: [2]bool{!s.Blocked[0], !s.Blocked[1]}, Scores: [2]int64{s.Players[0].Likes, s.Players[1].Likes}}
	if s.Result != nil {
		i.Phase = "terminal"
		i.Seconds = 0
		i.Required = [2]bool{}
		i.Result = &duel.RuleResult{Winner: s.Result.Winner, Reason: s.Result.Reason, Scores: s.Result.Scores}
	} else if s.AwaitingNextRound {
		i.Phase = "settlement"
		// The persisted presentation supplies the duration for this phase.
		i.Seconds = 0
		i.Required = [2]bool{}
	}
	return i, nil
}

type action struct {
	Kind string       `json:"kind"`
	Plan *engine.Plan `json:"plan"`
}

func (r *Rules) Accept(mode string, raw json.RawMessage, seat int, body json.RawMessage) (json.RawMessage, error) {
	return r.accept(mode, raw, seat, body, true)
}
func (r *Rules) accept(mode string, raw json.RawMessage, seat int, body json.RawMessage, manual bool) (json.RawMessage, error) {
	s, err := r.state(mode, raw)
	if err != nil {
		return nil, err
	}
	if seat < 0 || seat > 1 {
		return nil, duel.ErrInvalidRequest
	}
	if s.Result != nil || s.AwaitingNextRound {
		return nil, duel.ErrConflict
	}
	if s.Blocked[seat] {
		return nil, duel.ErrConflict
	}
	var a action
	var fields struct {
		Kind string          `json:"kind"`
		Plan json.RawMessage `json:"plan"`
	}
	if duel.Decode(body, &a) != nil || a.Kind != "plan" || a.Plan == nil || duel.Decode(body, &fields) != nil || !duel.HasFields(fields.Plan, "purchases", "main", "extra") || a.Plan.Purchases == nil || a.Plan.Extra == nil {
		return nil, duel.ErrInvalidRequest
	}
	validate := r.engines[mode].ValidatePlan
	if manual {
		validate = r.engines[mode].ValidateManualPlan
	}
	plan, err := validate(s, seat, *a.Plan)
	if err != nil {
		return nil, duel.ErrInvalidRequest
	}
	return duel.Encode(action{Kind: "plan", Plan: &plan})
}
func (r *Rules) Automatic(mode string, raw json.RawMessage, seat int) (json.RawMessage, error) {
	s, err := r.state(mode, raw)
	if err != nil {
		return nil, err
	}
	if seat < 0 || seat > 1 {
		return nil, duel.ErrInvalidRequest
	}
	if s.Result != nil || s.AwaitingNextRound {
		return nil, duel.ErrConflict
	}
	plan := engine.EmptyPlan()
	return duel.Encode(action{Kind: "plan", Plan: &plan})
}
func (r *Rules) Resolve(mode string, raw json.RawMessage, actions [2]json.RawMessage) (duel.Transition, error) {
	return r.ResolveWithRandom(mode, raw, actions, nil)
}
func (r *Rules) ResolveWithRandom(mode string, raw json.RawMessage, actions [2]json.RawMessage, pick func(int) (int, error)) (duel.Transition, error) {
	s, err := r.state(mode, raw)
	if err != nil {
		return duel.Transition{}, err
	}
	var plans [2]engine.Plan
	for seat, body := range actions {
		if s.Blocked[seat] {
			plans[seat] = engine.EmptyPlan()
			continue
		}
		validated, err := r.accept(mode, raw, seat, body, false)
		if err != nil {
			return duel.Transition{}, err
		}
		var a action
		_ = json.Unmarshal(validated, &a)
		plans[seat] = *a.Plan
	}
	next, record, err := r.engines[mode].Resolve(s, plans, pick)
	if err != nil {
		return duel.Transition{}, duel.ErrInvariant
	}
	state, err := duel.Encode(next)
	if err != nil {
		return duel.Transition{}, err
	}
	encoded, err := duel.Encode(record)
	if err != nil {
		return duel.Transition{}, err
	}
	presentation, err := duel.Encode(present(record))
	if err != nil || len(presentation) > 128<<10 {
		return duel.Transition{}, duel.ErrInvariant
	}
	return duel.Transition{State: state, Record: encoded, Presentation: presentation, Round: int(record.Round)}, nil
}
func (r *Rules) Begin(mode string, raw json.RawMessage) (json.RawMessage, json.RawMessage, error) {
	s, err := r.state(mode, raw)
	if err != nil {
		return nil, nil, err
	}
	next, events, err := r.engines[mode].BeginNextRound(s)
	if err != nil {
		return nil, nil, duel.ErrConflict
	}
	state, err := duel.Encode(next)
	if err != nil {
		return nil, nil, err
	}
	facts, err := duel.Encode(events)
	return state, facts, err
}
func (r *Rules) View(mode string, raw json.RawMessage, viewer int, terminal bool, own json.RawMessage) (json.RawMessage, error) {
	s, err := r.state(mode, raw)
	if err != nil {
		return nil, err
	}
	var plan *engine.Plan
	if len(own) > 0 {
		var a action
		if duel.Decode(own, &a) != nil || a.Kind != "plan" || a.Plan == nil {
			return nil, duel.ErrInvariant
		}
		plan = a.Plan
	}
	v, err := r.engines[mode].Project(s, viewer, terminal, plan)
	if err != nil {
		return nil, duel.ErrInvariant
	}
	return duel.Encode(v)
}
func (r *Rules) RoundView(mode string, raw json.RawMessage, viewer int, _ bool) (json.RawMessage, error) {
	if r.engines[mode] == nil || viewer < 0 || viewer > 1 {
		return nil, duel.ErrInvalidRequest
	}
	var record engine.RoundRecord
	if duel.Decode(raw, &record) != nil || record.Round < 1 || record.Round > 75 {
		return nil, duel.ErrInvariant
	}
	// This record contains declared plans and public resources only. Complete
	// private loadouts and future choices are never embedded by the engine.
	return duel.Encode(record)
}
func (r *Rules) Archive(mode string, raw json.RawMessage) (json.RawMessage, error) {
	return r.View(mode, raw, 0, true, nil)
}
