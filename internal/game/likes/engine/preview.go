package engine

import (
	"maps"
	"slices"

	"github.com/waiting-here/NonbiriAPI/internal/game/likes/catalog"
)

func effectMode(effect catalog.Effect, choice Choice) string {
	if effect.Kind == "CLEANSE" {
		return "self"
	}
	if effect.Kind == "DISPEL" {
		return "opponent"
	}
	if effect.Kind == "CLEANSE_OR_DISPEL" {
		if choice.CleanseMode != "" {
			return choice.CleanseMode
		}
		return "self"
	}
	return ""
}
func removable(s *State, seat int, mode string) []Status {
	owner := seat
	if mode == "opponent" {
		owner = other(seat)
	}
	result := []Status{}
	for _, st := range s.Players[owner].Effects {
		if st.Category != "state" && (mode == "self" && !st.Positive || mode == "opponent" && st.Positive) {
			result = append(result, st)
		}
	}
	return result
}
func validTargets(s *State, seat int, effect catalog.Effect, choice Choice) bool {
	mode := effectMode(effect, choice)
	limit := int64(0)
	if mode != "" && !effect.RandomTargets {
		limit = effect.P
	}
	if int64(len(choice.Targets)) > limit {
		return false
	}
	seen := map[string]bool{}
	for _, key := range choice.Targets {
		if len(key) > 160 || seen[key] || !slices.ContainsFunc(removable(s, seat, mode), func(st Status) bool { return st.Key == key }) {
			return false
		}
		seen[key] = true
	}
	return true
}
func (e *Engine) cacheScope(s *State, seat int, id string) string {
	_, template := e.effect(s, id, seat)
	if e.skills[template].Kind != "basic" || template == "PUB02" {
		return ""
	}
	scope := "common"
	if template == "GEM01" {
		scope = "flash"
	} else if template == "GEM02" {
		scope = "pro"
	}
	if id == "PUB41" {
		scope = "distilled-" + scope
	}
	return scope
}

func (e *Engine) usage(s *State, seat int, choice Choice, derived bool) Preview {
	p, sk := s.Players[seat], e.skills[choice.SkillID]
	effect, template := e.effect(s, choice.SkillID, seat)
	cost := sk.Cost()
	v := Preview{Errors: []string{}, Shortages: []string{}, Effect: effect, TemplateID: template, ResourceCosts: maps.Clone(sk.ResourceCosts), Gold: cost.Gold}
	if s.Result != nil {
		v.Errors = append(v.Errors, "ended")
	}
	if p.Stunned {
		v.Errors = append(v.Errors, "stunned")
	}
	if _, ok := e.skills[choice.SkillID]; !ok {
		v.Errors = append(v.Errors, "unknown-skill")
	}
	if derived {
		if choice.SkillID != "GEM01" {
			v.Errors = append(v.Errors, "invalid-trigger")
		}
	} else if !slices.Contains(p.Loadout, choice.SkillID) {
		v.Errors = append(v.Errors, "not-equipped")
	}
	if !derived && sk.MaxUses != nil && p.Used[choice.SkillID] >= *sk.MaxUses {
		v.Errors = append(v.Errors, "uses-exhausted")
	}
	if choice.Pay != "" && choice.Pay != "auto" && choice.Pay != "api" {
		v.Errors = append(v.Errors, "payment")
	}
	if choice.CleanseMode != "" && choice.CleanseMode != "self" && choice.CleanseMode != "opponent" {
		v.Errors = append(v.Errors, "cleanup-mode")
	}
	if !validTargets(s, seat, effect, choice) {
		v.Errors = append(v.Errors, "targets")
	}
	banned := hasStatus(p, "SUBSCRIPTION_BAN")
	for key, n := range sk.ResourceCosts {
		if n > 0 {
			if p.Resources[key] < n {
				v.Errors = append(v.Errors, "resource:"+key)
			}
			if banned {
				v.Errors = append(v.Errors, "subscription-banned")
			}
		}
	}
	if p.Gold < cost.Gold {
		v.Errors = append(v.Errors, "gold")
	}
	apiMode := cost.Payment == "api" || cost.Payment == "mix" && !derived && (p.Role == "DeepSeek" || choice.Pay == "api")
	token, energy := cost.Token, cost.Energy
	if completion := e.passive(p, "CODE_COMPLETION"); completion != nil {
		token = max(0, token-completion.P)
	}
	if cost.Energy > 0 {
		for _, st := range p.Effects {
			if st.Kind == "SOTA_FANATICISM" && active(s, st) {
				energy += st.P * st.Layers
			}
		}
	}
	baseToken, baseEnergy := token, energy
	if speed := e.speed(s, seat); speed != nil {
		token *= speed.P
		energy *= speed.P
	}
	for _, st := range p.Effects {
		if st.Kind == "CACHE" && e.buffs[st.BuffID].CacheScope == e.cacheScope(s, seat, choice.SkillID) {
			token -= st.P * st.Layers
		}
		if !derived {
			if st.Kind == "TOKEN_TAX" || st.Kind == "NONBASIC_TAX" && sk.Kind != "basic" {
				token += st.P
			}
			if st.Kind == "API_DISCOUNT" && apiMode {
				token -= st.P
			}
			if st.Kind == "SAVE_ENERGY" || st.Kind == "REGULATOR" {
				energy -= st.P
			}
			if st.Kind == "ENERGY_STACK" {
				energy -= st.P * st.Layers
			}
		}
	}
	v.Token = max(0, token)
	if baseToken > 0 {
		v.Token = max(e.param("TOKEN_FLOOR"), token)
	} else {
		v.Token = 0
	}
	v.Energy = max(0, energy)
	if baseEnergy > 0 {
		v.Energy = max(e.param("ENERGY_FLOOR"), energy)
	}
	v.TrialPayment = min(v.Token, optional(p.Trial, int64(0)))
	due := v.Token - v.TrialPayment
	if banned && cost.Payment == "sub" && due > 0 {
		v.Errors = append(v.Errors, "subscription-banned")
	}
	if cost.Payment == "sub" {
		v.SubPayment = due
	} else if apiMode || banned {
		v.APIPayment = due
	} else {
		v.SubPayment = min(due, p.Burst, p.Sub)
		v.APIPayment = due - v.SubPayment
	}
	if s.Energy < v.Energy {
		v.Shortages = append(v.Shortages, "energy")
	}
	if p.Burst < v.SubPayment || p.Sub < v.SubPayment || p.API < v.APIPayment {
		v.Shortages = append(v.Shortages, "token")
	}
	scores := clone(*s)
	if effectMode(effect, choice) == "self" {
		scores.Players[seat].Effects = slices.DeleteFunc(scores.Players[seat].Effects, func(st Status) bool { return slices.Contains(choice.Targets, st.Key) })
	}
	b, bonus := e.basis(&scores, seat, sk, effect, template)
	score := e.score(&scores, seat, sk, effect, template, b.Conditional, derived, 0)
	v.BaseLikes, v.IntrinsicLikes, v.ConditionalLikes = b.Intrinsic+b.Conditional, b.Intrinsic, b.Conditional
	v.BaseLikeBonus, v.PassiveLikes, v.Likes = bonus, score.Passive, score.Final
	v.Legal = len(v.Errors) == 0
	v.Success = v.Legal && len(v.Shortages) == 0
	if choice.SkillID == "PUB41" {
		v.Learning = ptr(e.learning(s, seat))
	}
	return v
}

func (e *Engine) Preview(previous State, seat int, choice Choice) (Preview, error) {
	if !validSeat(seat) {
		return Preview{}, ErrPlan
	}
	if err := e.Validate(previous); err != nil {
		return Preview{}, err
	}
	v := e.usage(&previous, seat, choice, false)
	if overloaded, _ := restriction(&previous, seat); overloaded {
		v.Errors = append(v.Errors, "overloaded")
		v.Legal = false
		v.Success = false
	}
	return v, nil
}
