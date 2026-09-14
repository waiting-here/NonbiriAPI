package engine

import "slices"

func (r *roundRun) expire(plans [2]Plan, successful [2][]Action, newOverload [2]bool) {
	e, s := r.e, r.s
	for seat := range 2 {
		if newOverload[seat] {
			r.overload(seat)
		}
		refresh := map[string]bool{}
		for _, g := range s.Grants {
			if g.Owner == seat {
				refresh[g.BuffID] = true
			}
		}
		p := &s.Players[seat]
		next := []Status{}
		for _, st := range p.Effects {
			keep := false
			switch {
			case st.Kind == "CACHE":
				keep = plans[seat].Main != nil && !newOverload[seat] && (refresh[st.Key] || optional(st.RefreshedTurn, int64(-1)) == s.Round)
				if !keep {
					st.Layers = optional(st.PersistentLayers, int64(0))
					keep = st.Layers > 0
				}
			case slices.Contains(timedKinds, st.Kind), st.Kind == "OVERLOAD":
				st.Remaining = max(0, optional(st.Expires, s.Round)-s.Round)
				keep = st.Remaining > 0
			case st.Kind == "BASE_SUPPRESS", st.Kind == "MODEL_DEGRADATION":
				keep = st.Layers > 0
			case st.Kind == "COMBO":
				keep = plans[seat].Main != nil && !newOverload[seat] && st.Layers > 0
			case st.Kind == "SPEED_MODE":
				keep = true
			case st.Kind == "SOTA_FANATICISM":
				if !refresh[st.Key] && optional(st.RefreshedTurn, int64(-1)) != s.Round {
					st.Layers--
				}
				keep = st.Layers > 0
			case st.Kind == "ENERGY_STACK", st.Kind == "SUBSCRIPTION_BAN" && st.Expires == nil:
				keep = true
			default:
				if st.Kind == "STUN" && plans[seat].Main == nil {
					st.Remaining--
				}
				keep = optional(st.Expires, MaxSafeInteger) > s.Round && st.Remaining > 0
			}
			if keep {
				next = append(next, st)
			}
		}
		p.Effects = next
	}
	grants := s.Grants
	s.Grants = []Grant{}
	for _, g := range grants {
		if newOverload[g.Owner] && slices.Contains([]string{"CACHE", "COMBO", "REGULATOR"}, e.buffs[g.BuffID].Kind) {
			if optional(g.PersistentLayers, int64(0)) > 0 {
				g.Amount = clone(g.PersistentLayers)
			} else {
				continue
			}
		}
		r.materialize(g, s.Round+1)
	}
	for seat := range 2 {
		for _, a := range successful[seat] {
			if a.Preview.Effect.Kind == "TOGGLE_SPEED" {
				id := a.Preview.Effect.BuffID
				p := &s.Players[seat]
				if slices.ContainsFunc(p.Effects, func(st Status) bool { return st.BuffID == id }) {
					p.Effects = slices.DeleteFunc(p.Effects, func(st Status) bool { return st.BuffID == id })
					r.log("mode", ptr(seat), map[string]any{"enabled": false, "buffId": id})
				} else {
					r.materialize(Grant{BuffID: id, Owner: seat, SourceSkill: a.Choice.SkillID}, s.Round+1)
				}
			}
		}
	}
}
