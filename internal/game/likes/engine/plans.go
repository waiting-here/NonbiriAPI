package engine

import (
	"slices"
)

type shopQuote struct {
	Price, Amount int64
	Category      string
}

func (e *Engine) shopQuote(s *State, seat int, purchase Purchase, used []string) (shopQuote, error) {
	p := s.Players[seat]
	v := shopQuote{Category: purchase.Item}
	if s.Result != nil || s.AwaitingNextRound || len(used) >= int(e.param("PREP_MAX")) || len(purchase.Target) > 160 {
		return v, ErrPlan
	}
	if overloaded, _ := restriction(s, seat); overloaded {
		return v, ErrPlan
	}
	if purchase.Item != "cleanse" && purchase.Target != "" {
		return v, ErrPlan
	}
	switch purchase.Item {
	case "sub":
		v.Price, v.Amount = e.param("SUB_PRICE"), e.param("SUB_TOTAL_UPGRADE")
		if discount := e.passive(p, "VALUE_SUBSCRIPTION"); discount != nil {
			v.Price = max(0, v.Price-discount.P)
		}
		if p.Role == "DeepSeek" || p.BurstCap == 0 {
			return v, ErrPlan
		}
	case "api":
		v.Price, v.Amount = e.param("API_PRICE"), p.APIPack
	case "charge":
		v.Price, v.Amount = e.param("CHARGE_PRICE"), max(0, min(e.param("CHARGE_PACK"), e.param("ENERGY_CAP")-s.Energy))
		if v.Amount == 0 {
			return v, ErrPlan
		}
	case "cleanse":
		v.Price, v.Amount, v.Category = e.param("CLEANSE_PRICE"), 1, "item"
		if purchase.Target == "" || !slices.ContainsFunc(removable(s, seat, "self"), func(st Status) bool { return st.Key == purchase.Target }) {
			return v, ErrPlan
		}
	case "regulator":
		v.Price, v.Amount, v.Category = e.param("REGULATOR_PRICE"), e.param("REGULATOR_SAVE"), "item"
	default:
		return v, ErrPlan
	}
	if slices.Contains(used, v.Category) || p.Gold < v.Price {
		return v, ErrPlan
	}
	return v, nil
}
func (e *Engine) shop(s *State, seat int, purchase Purchase, used *[]string) (shopQuote, int64, error) {
	v, err := e.shopQuote(s, seat, purchase, *used)
	if err != nil {
		return v, 0, err
	}
	p := &s.Players[seat]
	p.Gold -= v.Price
	*used = append(*used, v.Category)
	charge := int64(0)
	switch purchase.Item {
	case "sub":
		e.upgradeSubscription(p)
	case "api":
		p.API += p.APIPack
	case "charge":
		charge = e.param("CHARGE_PACK")
		s.Energy = min(e.param("ENERGY_CAP"), s.Energy+charge)
	case "cleanse":
		p.Effects = slices.DeleteFunc(p.Effects, func(st Status) bool { return st.Key == purchase.Target })
		_, p.Stunned = restriction(s, seat)
	case "regulator":
		s.Grants = append(s.Grants, Grant{BuffID: e.buffKind("REGULATOR").ID, Owner: seat})
	}
	return v, charge, nil
}
func (e *Engine) prepare(s State, seat int, plan Plan) (State, error) {
	if plan.Purchases == nil || len(plan.Purchases) > int(e.param("PREP_MAX")) {
		return State{}, ErrPlan
	}
	draft := clone(s)
	used := []string{}
	for _, purchase := range plan.Purchases {
		if _, _, err := e.shop(&draft, seat, purchase, &used); err != nil {
			return State{}, err
		}
	}
	return draft, nil
}
func (e *Engine) quote(prepared *State, seat int, plan Plan) (PlanPreview, error) {
	result := PlanPreview{Actions: []Action{}, Shortages: []string{}}
	if plan.Main == nil {
		if len(plan.Extra) != 0 {
			return result, ErrPlan
		}
		result.Success = true
		return result, nil
	}
	first, _ := e.effect(prepared, plan.Main.SkillID, seat)
	limit := int64(0)
	if first.Kind == "INSERT" {
		limit = min(first.P, e.param("INSERT_CAP"))
	}
	if int64(len(plan.Extra)) > limit {
		return result, ErrPlan
	}
	choices := append([]Choice{*plan.Main}, plan.Extra...)
	s := clone(*prepared)
	allowance := clone(s.Players[seat])
	stopped := false
	for index, choice := range choices {
		valid := clone(s)
		valid.Players[seat].Used = clone(allowance.Used)
		valid.Players[seat].Resources = clone(allowance.Resources)
		valid.Players[seat].Gold = allowance.Gold
		effect, _ := e.effect(&valid, choice.SkillID, seat)
		if !validTargets(prepared, seat, effect, choice) {
			return result, ErrPlan
		}
		withoutTargets := choice
		withoutTargets.Targets = nil
		v := e.usage(&valid, seat, withoutTargets, false)
		if overloaded, _ := restriction(&valid, seat); overloaded || !v.Legal {
			return result, ErrPlan
		}
		allowance.Gold -= v.Gold
		allowance.Used[choice.SkillID]++
		for key, n := range e.skills[choice.SkillID].ResourceCosts {
			allowance.Resources[key] -= n
			if allowance.Resources[key] < 0 {
				return result, ErrPlan
			}
		}
		if choice.SkillID == "PUB41" {
			v.Learning = ptr(e.learning(prepared, seat))
		}
		result.Energy += v.Energy
		result.Token += v.Token
		a := Action{Choice: clone(choice), Main: index == 0, Preview: v, Cancelled: stopped}
		result.Actions = append(result.Actions, a)
		if stopped {
			continue
		}
		if slices.Contains(v.Shortages, "token") {
			stopped = true
			result.Shortages = append(result.Shortages, "token")
			continue
		}
		e.pay(&s, seat, a)
		consumeDecay(&s.Players[seat], choice.SkillID, v.TemplateID, v.Effect)
		consumeDegradation(&s, seat)
	}
	scores := clone(*prepared)
	removed := map[string]bool{}
	for _, a := range result.Actions {
		if !a.Cancelled && !slices.Contains(a.Preview.Shortages, "token") && effectMode(a.Preview.Effect, a.Choice) == "self" {
			for _, key := range a.Choice.Targets {
				removed[key] = true
			}
		}
	}
	scores.Players[seat].Effects = slices.DeleteFunc(scores.Players[seat].Effects, func(st Status) bool { return removed[st.Key] })
	for i := range result.Actions {
		a := &result.Actions[i]
		v := &a.Preview
		character := e.characterBonus(&scores, seat, e.skills[a.Choice.SkillID], v.Effect, a.Main, stepLikes(&scores, "main", 0))
		basis, bonus := e.basis(&scores, seat, e.skills[a.Choice.SkillID], v.Effect, v.TemplateID, character)
		v.BaseLikeBonus = bonus
		v.BaseLikes, v.IntrinsicLikes = basis.Intrinsic+v.ConditionalLikes, basis.Intrinsic
		v.Likes = e.score(&scores, seat, e.skills[a.Choice.SkillID], v.Effect, v.TemplateID, v.ConditionalLikes, false, 0, character).Final
		if !a.Cancelled && !slices.Contains(v.Shortages, "token") {
			consumeDecay(&scores.Players[seat], a.Choice.SkillID, v.TemplateID, v.Effect)
			consumeDegradation(&scores, seat)
		}
	}
	if result.Energy > prepared.Energy {
		result.Shortages = append(result.Shortages, "energy")
	}
	result.Success = len(result.Shortages) == 0
	if plan.Main.SkillID == "PUB41" {
		result.Learning = ptr(e.learning(prepared, seat))
	}
	return result, nil
}

func (e *Engine) PlanPreview(state State, seat int, plan Plan) (PlanPreview, error) {
	if !validSeat(seat) || state.Result != nil || state.AwaitingNextRound {
		return PlanPreview{}, ErrPlan
	}
	if err := e.Validate(state); err != nil {
		return PlanPreview{}, err
	}
	prepared, err := e.prepare(state, seat, plan)
	if err != nil {
		return PlanPreview{}, err
	}
	if overloaded, stunned := restriction(&prepared, seat); overloaded || stunned && plan.Main != nil {
		return PlanPreview{}, ErrPlan
	}
	return e.quote(&prepared, seat, plan)
}
func (e *Engine) ValidatePlan(state State, seat int, plan Plan) (Plan, error) {
	if _, err := e.PlanPreview(state, seat, plan); err != nil {
		return Plan{}, err
	}
	next := clone(plan)
	if next.Extra == nil {
		next.Extra = []Choice{}
	}
	return next, nil
}

// ValidateManualPlan requires a main skill unless shopping leaves the player
// stunned. Empty automatic timeout plans are validated separately.
func (e *Engine) ValidateManualPlan(state State, seat int, plan Plan) (Plan, error) {
	next, err := e.ValidatePlan(state, seat, plan)
	if err != nil {
		return Plan{}, err
	}
	prepared, err := e.prepare(state, seat, next)
	if err != nil {
		return Plan{}, err
	}
	_, stunned := restriction(&prepared, seat)
	if next.Main == nil && !stunned {
		return Plan{}, ErrPlan
	}
	return next, nil
}
