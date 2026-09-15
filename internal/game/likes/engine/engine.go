package engine

import (
	"encoding/json"
	"errors"
	"maps"
	"slices"

	"github.com/waiting-here/NonbiriAPI/internal/game/likes/catalog"
)

var ErrState = errors.New("likes: invalid state")
var ErrPlan = errors.New("likes: invalid plan")
var ErrInvariant = errors.New("likes: rule invariant failed")
var ErrRandom = errors.New("likes: random source unavailable")

type Engine struct {
	c         catalog.Config
	hash      string
	skills    map[string]catalog.Skill
	buffs     map[string]catalog.Buff
	roles     map[string]catalog.Role
	harnesses map[string]catalog.Harness
	passives  map[string]catalog.Passive
}

func New(mode string) (*Engine, error) {
	c, hash, err := catalog.Load(mode)
	if err != nil {
		return nil, err
	}
	e := &Engine{c: c, hash: hash, skills: map[string]catalog.Skill{}, buffs: map[string]catalog.Buff{}, roles: map[string]catalog.Role{}, harnesses: map[string]catalog.Harness{}, passives: map[string]catalog.Passive{}}
	for _, sk := range c.Skills {
		e.skills[sk.ID] = sk
	}
	for _, buff := range c.Buffs {
		e.buffs[buff.ID] = buff
	}
	for _, role := range c.Roles {
		e.roles[role.ID] = role
	}
	for _, h := range c.Harnesses {
		e.harnesses[h.ID] = h
	}
	for _, p := range c.Passives {
		e.passives[p.ID] = p
	}
	return e, nil
}

func (e *Engine) ContentHash() string     { return e.hash }
func (e *Engine) Catalog() catalog.Config { return clone(e.c) }
func (e *Engine) param(key string) int64  { return e.c.Parameters[key] }
func ptr[T any](value T) *T               { return &value }
func optional[T any](value *T, fallback T) T {
	if value != nil {
		return *value
	}
	return fallback
}
func other(seat int) int      { return 1 - seat }
func validSeat(seat int) bool { return seat == 0 || seat == 1 }
func clone[T any](value T) T {
	body, err := json.Marshal(value)
	if err != nil {
		panic("likes: cannot encode rule value")
	}
	var result T
	if json.Unmarshal(body, &result) != nil {
		panic("likes: cannot copy rule value")
	}
	return result
}
func hasStatus(p Player, kind string) bool {
	return slices.ContainsFunc(p.Effects, func(st Status) bool { return st.Kind == kind })
}
func active(s *State, st Status) bool {
	return st.ActiveFrom <= s.Round && optional(st.Expires, MaxSafeInteger) >= s.Round
}
func (e *Engine) passive(p Player, kind string) *catalog.Passive {
	for _, key := range e.harnesses[optional(p.Harness, "")].Passives {
		item := e.passives[key]
		if item.Kind == kind {
			return &item
		}
	}
	return nil
}
func (e *Engine) buffKind(kind string) catalog.Buff {
	for _, item := range e.c.Buffs {
		if item.Kind == kind {
			return item
		}
	}
	panic("likes: missing fixed buff")
}
func (e *Engine) speed(s *State, seat int) *Status {
	for _, st := range s.Players[seat].Effects {
		if st.Kind == "SPEED_MODE" && active(s, st) {
			return &st
		}
	}
	return nil
}
func (e *Engine) multiplier(s *State, seat int) int64 {
	if mode := e.speed(s, seat); mode != nil {
		return mode.Q
	}
	return 1
}
func (e *Engine) finalLikes(s *State, seat int, n int64) int64 {
	return max(0, n) * e.multiplier(s, seat)
}
func (e *Engine) overloadDuration(s *State, seat int) int64 {
	if mode := e.speed(s, seat); mode != nil {
		return e.buffs[mode.BuffID].N
	}
	return e.buffKind("OVERLOAD").N
}
func restriction(s *State, seat int) (overloaded, stunned bool) {
	for _, st := range s.Players[seat].Effects {
		if st.Kind == "OVERLOAD" && active(s, st) {
			overloaded = true
		}
		if st.Kind == "STUN" && st.ActiveFrom <= s.Round {
			stunned = true
		}
	}
	return
}
func (e *Engine) slots(harness *string) int {
	return 4 + e.harnesses[optional(harness, "")].ActiveSlots
}

func (e *Engine) ValidateSelection(selection Selection) error {
	role, ok := e.roles[selection.Role]
	if !ok {
		return ErrPlan
	}
	if selection.Harness != nil {
		if _, ok := e.harnesses[*selection.Harness]; !ok {
			return ErrPlan
		}
	}
	if len(selection.Skills) < 1 || len(selection.Skills) > e.slots(selection.Harness) {
		return ErrPlan
	}
	seen, sustainable := map[string]bool{}, false
	for _, key := range selection.Skills {
		sk, ok := e.skills[key]
		if !ok || seen[key] || sk.Owner != "全局公共" && sk.Owner != selection.Role {
			return ErrPlan
		}
		seen[key] = true
		if sk.Stable && sk.ID != "PUB41" && sk.MaxUses == nil && sk.Effects.Base.Likes > 0 && (sk.Payment != "sub" || e.initialBurst(role) > 0) {
			renewable := true
			for key, cost := range sk.ResourceCosts {
				if cost > 0 && (key != "R_IMAGE" || !e.c.Resources[0].Subscription) {
					renewable = false
				}
			}
			sustainable = sustainable || renewable
		}
	}
	if !sustainable {
		return ErrPlan
	}
	return nil
}
func (e *Engine) initialBurst(role catalog.Role) int64 {
	if n, ok := role.Overrides["burstCap"]; ok {
		return n
	}
	if role.ID == "DeepSeek" {
		return 0
	}
	return e.param("BURST_CAP")
}

func (e *Engine) Create(selections [2]Selection) (State, error) {
	s := State{Version: 1, Mode: e.c.Mode, Round: 1, Energy: e.param("ENERGY_START"), Grants: []Grant{}}
	for seat, selection := range selections {
		if err := e.ValidateSelection(selection); err != nil {
			return State{}, err
		}
		r := e.roles[selection.Role]
		base := func(key string, fallback int64) int64 {
			if n, ok := r.Overrides[key]; ok {
				return n
			}
			return fallback
		}
		api, pack := e.param("API_START"), e.param("API_PACK")
		if r.ID == "DeepSeek" {
			api, pack = e.param("DS_API_START"), e.param("DS_API_PACK")
		}
		p := Player{Role: r.ID, Harness: clone(selection.Harness), ActiveSlots: e.slots(selection.Harness), Loadout: slices.Clone(selection.Skills), Gold: base("gold", e.param("INITIAL_GOLD")), BurstCap: e.initialBurst(r), API: base("api", api), APIPack: base("apiPack", pack), Effects: []Status{}, Used: map[string]int64{}, Revealed: []string{}, Resources: map[string]int64{}, ResourceCaps: map[string]int64{}, Distill: Distill{Learning: e.skills["PUB41"].Learn, UsedSamples: []int64{}}}
		p.Burst, p.Sub = p.BurstCap, p.BurstCap*2
		p.Subscription = Subscription{BurstInitial: p.BurstCap, TotalInitial: p.Sub, TotalCap: p.Sub}
		for key, a := range r.Resources {
			p.Resources[key] = a.Initial
			p.ResourceCaps[key] = a.Cap
		}
		if e.passive(p, "FREE_TRIAL") != nil {
			p.Trial = ptr(int64(0))
		}
		s.Players[seat] = p
	}
	e.begin(&s)
	return s, e.Validate(s)
}

// BeginNextRound is separate from Resolve so the outer service can preserve its
// display deadline and then give both seats a complete planning interval.
func (e *Engine) BeginNextRound(previous State) (State, []Event, error) {
	if err := e.Validate(previous); err != nil {
		return State{}, nil, err
	}
	if !previous.AwaitingNextRound || previous.Result != nil {
		return State{}, nil, ErrState
	}
	s := clone(previous)
	before := frame(&s, "round-start", 0)
	s.Round++
	s.AwaitingNextRound = false
	e.begin(&s)
	if err := e.Validate(s); err != nil {
		return State{}, nil, err
	}
	after := frame(&s, "round-start", 0)
	s.EventSeq++
	events := []Event{{ID: s.EventSeq, Round: s.Round, Stage: "round-start", Kind: "round-start", Data: map[string]any{"before": before, "after": after}}}
	return s, events, nil
}
func (e *Engine) begin(s *State) {
	for seat := range 2 {
		p := &s.Players[seat]
		p.NormalTurns = s.Round
		e.resetSubscription(p)
		s.Blocked[seat], p.Stunned = restriction(s, seat)
		s.LikesAtStart[seat] = p.Likes
	}
}

func (e *Engine) effect(s *State, id string, seat int) (catalog.Effect, string) {
	template, level := id, "base"
	if id == "PUB41" {
		d := s.Players[seat].Distill
		if d.Template == nil || d.Level == nil {
			return catalog.Effect{Kind: "SCORE"}, "PUB41"
		}
		template, level = *d.Template, *d.Level
	}
	sk := e.skills[template]
	value := sk.Effects.Base
	if level == "I" {
		value = sk.Effects.I
	} else if level == "II" {
		value = sk.Effects.II
	}
	if b, ok := e.buffs[value.BuffID]; ok {
		if slices.Contains([]string{"CACHE", "CACHE_COMBO", "ENERGY_STACK"}, value.Kind) {
			value.P, value.Q = b.P, b.Cap
		}
		if slices.Contains([]string{"AMPLIFY", "SUPPRESS", "TOKEN_TAX", "NONBASIC_TAX", "SAVE_ENERGY", "API_DISCOUNT", "COUNTER"}, value.Kind) {
			value.P, value.Q, value.N = b.P, b.Q, b.N
		}
	}
	return value, template
}
func (e *Engine) learning(s *State, seat int) Learning {
	d := s.Players[seat].Distill
	result := Learning{Template: clone(d.Template), Level: clone(d.Level), Reason: "no-sample"}
	if d.Learning <= 0 {
		result.Reason = "exhausted"
		return result
	}
	record := s.Records[other(seat)]
	if record == nil || record.Skipped || record.LastMain == nil || !record.LastMain.Success {
		return result
	}
	sample := record.LastMain
	if sample.SkillID == "PUB41" || !e.skills[sample.SkillID].Copyable {
		result.Reason = "uncopyable"
		return result
	}
	if e.c.Rules.UniqueSamples && slices.Contains(d.UsedSamples, sample.ID) {
		result.Reason = "used-sample"
		return result
	}
	result.Sample = clone(sample)
	if optional(d.Template, "") == sample.SkillID && optional(d.Level, "") == "II" {
		result.Reason = "highest-level"
		return result
	}
	result.Changes = true
	result.Template = ptr(sample.SkillID)
	result.Level = ptr("I")
	if optional(d.Template, "") == sample.SkillID {
		result.Level = ptr("II")
	}
	result.Reason = "learn-after-round"
	return result
}

func frame(s *State, stage string, end int) Frame {
	f := Frame{Stage: stage, Energy: s.Energy, EventEnd: end}
	for seat, p := range s.Players {
		f.Players[seat] = ResourceView{Gold: p.Gold, Likes: p.Likes, Burst: p.Burst, BurstCap: p.BurstCap, Sub: p.Sub, SubCap: p.Subscription.TotalCap, API: p.API, Trial: optional(p.Trial, int64(0)), Resources: maps.Clone(p.Resources), ResourceCaps: maps.Clone(p.ResourceCaps), Subscription: clone(p.Subscription), Effects: clone(p.Effects)}
	}
	return f
}
