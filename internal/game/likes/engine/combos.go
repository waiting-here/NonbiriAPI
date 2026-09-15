package engine

import "slices"

func (r *roundRun) conversions(actions [2][]Action) {
	e, s := r.e, r.s
	for seat := range 2 {
		for _, a := range actions[seat] {
			if a.Preview.Effect.Kind == "CACHE_CONVERT" {
				p := &s.Players[seat]
				pro, flash := e.buffKind("CACHE"), e.buffKind("CACHE")
				for _, b := range e.c.Buffs {
					if b.CacheScope == "pro" {
						pro = b
					}
					if b.CacheScope == "flash" {
						flash = b
					}
				}
				old, persistent, pending := int64(0), int64(0), int64(0)
				for _, st := range p.Effects {
					if st.BuffID == pro.ID {
						old, persistent = st.Layers, optional(st.PersistentLayers, int64(0))
					}
				}
				for _, g := range s.Grants {
					if g.Owner == seat && g.BuffID == pro.ID {
						pending += optional(g.Amount, int64(1))
					}
				}
				n := min(pro.Cap, old+pending)
				p.Effects = slices.DeleteFunc(p.Effects, func(st Status) bool { return st.BuffID == pro.ID })
				s.Grants = slices.DeleteFunc(s.Grants, func(g Grant) bool { return g.Owner == seat && g.BuffID == pro.ID })
				data := map[string]any{"layers": n}
				if n > 0 {
					api, gold := e.skills[a.Choice.SkillID].Token*a.Preview.Effect.P/100*n, a.Preview.Effect.Q*n
					p.API += api
					p.Gold += gold
					s.Grants = append(s.Grants, Grant{BuffID: flash.ID, Owner: seat, Amount: ptr(n), SourceSkill: a.Choice.SkillID, PersistentLayers: ptr(persistent)}, Grant{BuffID: e.buffKind("COMBO").ID, Owner: seat, Amount: ptr(n), SourceSkill: a.Choice.SkillID})
					data["api"], data["gold"] = api, gold
				}
				r.log("conversion", ptr(seat), data)
			}
		}
	}
}

func (r *roundRun) castEvent(seat int, a Action, score ScoreBreakdown) {
	data := map[string]any{"skillId": a.Choice.SkillID, "derived": a.Derived, "main": a.Main, "energy": a.Preview.Energy, "token": a.Preview.Token, "likes": score.Final, "passiveLikes": score.Passive, "trialPayment": a.Preview.TrialPayment, "subPayment": a.Preview.SubPayment, "apiPayment": a.Preview.APIPayment, "gold": a.Preview.Gold, "resourceCosts": a.Preview.ResourceCosts, "templateId": a.Preview.TemplateID, "success": true}
	if a.Choice.SkillID == "PUB41" {
		data["level"] = clone(r.s.Players[seat].Distill.Level)
	}
	r.log("cast", ptr(seat), data).Score = ptr(score)
}

// Each batch freezes both sides' quotes before either payment. A failed attempt
// spends progress, never money or energy, and cannot grant overload.
func (r *roundRun) combos(plans [2]Plan, overloaded [2]bool) error {
	e, s := r.e, r.s
	progress := []Grant{}
	for _, g := range s.Grants {
		if e.buffs[g.BuffID].Kind == "COMBO" {
			progress = append(progress, g)
		}
	}
	s.Grants = slices.DeleteFunc(s.Grants, func(g Grant) bool { return e.buffs[g.BuffID].Kind == "COMBO" })
	for _, g := range progress {
		if plans[g.Owner].Main != nil && !overloaded[g.Owner] {
			r.gainProgress(g.Owner, optional(g.Amount, int64(1)))
		}
	}
	combo := e.buffKind("COMBO")
	queued, attempts := [2]int64{}, [2]int{}
	for {
		type candidate struct {
			seat   int
			action Action
		}
		candidates := []candidate{}
		for seat := range 2 {
			if plans[seat].Main == nil || overloaded[seat] {
				continue
			}
			if queued[seat] == 0 {
				for i := range s.Players[seat].Effects {
					st := &s.Players[seat].Effects[i]
					if st.Kind == "COMBO" && st.Layers >= combo.P {
						st.Layers -= combo.P
						queued[seat] += combo.Q
						break
					}
				}
			}
			if queued[seat] == 0 {
				continue
			}
			queued[seat]--
			attempts[seat]++
			if attempts[seat] > MaxFlashPerSeat {
				return ErrInvariant
			}
			choice := Choice{SkillID: "GEM01", Pay: "auto"}
			preview := e.usage(s, seat, choice, true)
			action := Action{Choice: choice, Preview: preview, Derived: true}
			if !preview.Legal || slices.Contains(preview.Shortages, "token") {
				r.log("combo-skip", ptr(seat), map[string]any{"reason": "personal-resources", "success": false})
			} else {
				candidates = append(candidates, candidate{seat: seat, action: action})
			}
		}
		energy := int64(0)
		for _, c := range candidates {
			energy += c.action.Preview.Energy
		}
		if energy > s.Energy {
			for _, c := range candidates {
				r.log("combo-skip", ptr(c.seat), map[string]any{"required": energy, "available": s.Energy, "reason": "shared-energy", "success": false})
			}
		} else {
			for _, c := range candidates {
				seat, a := c.seat, c.action
				e.pay(s, seat, a)
				s.Energy -= a.Preview.Energy
				score := e.score(s, seat, e.skills["GEM01"], a.Preview.Effect, a.Preview.TemplateID, a.Preview.ConditionalLikes, true, 0)
				s.Players[seat].Likes += score.Final
				consumeDegradation(s, seat)
				r.castEvent(seat, a, score)
				effect := a.Preview.Effect
				if effect.N > 0 && effect.BuffID != "" {
					r.materialize(Grant{BuffID: effect.BuffID, Owner: seat, SourceSkill: "GEM01"}, s.Round)
					for i := range s.Players[seat].Effects {
						st := &s.Players[seat].Effects[i]
						if st.BuffID == effect.BuffID {
							st.RefreshedTurn = ptr(s.Round)
						}
					}
					r.gainProgress(seat, 1)
				}
			}
		}
		more := false
		for seat := range 2 {
			if plans[seat].Main != nil && !overloaded[seat] {
				more = more || queued[seat] > 0
				for _, st := range s.Players[seat].Effects {
					if st.Kind == "COMBO" && st.Layers >= combo.P {
						more = true
					}
				}
			}
		}
		if !more {
			break
		}
	}
	return nil
}
