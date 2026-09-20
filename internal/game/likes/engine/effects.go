package engine

import "slices"

type roundRun struct {
	e      *Engine
	s      *State
	record *RoundRecord
	stage  string
	pick   func(int) (int, error)
	step   StepLikesSnapshot
}

func (r *roundRun) log(kind string, seat *int, data map[string]any) *Event {
	r.s.EventSeq++
	r.record.Events = append(r.record.Events, Event{ID: r.s.EventSeq, Round: r.s.Round, Stage: r.stage, Kind: kind, Seat: seat, Data: data})
	return &r.record.Events[len(r.record.Events)-1]
}
func (r *roundRun) snapshot(stage string) {
	r.record.Frames = append(r.record.Frames, frame(r.s, stage, len(r.record.Events)))
}
func (r *roundRun) materialize(g Grant, starts int64) {
	e, s := r.e, r.s
	b := e.buffs[g.BuffID]
	p := &s.Players[g.Owner]
	var old *Status
	for _, st := range p.Effects {
		if st.Key == b.ID {
			old = ptr(st)
			break
		}
	}
	previous := Status{}
	if old != nil {
		previous = *old
	}
	n := optional(g.Duration, b.N)
	if b.Kind == "STUN" {
		n = max(1, n)
	} else if b.Kind == "REGULATOR" {
		n = 1
	}
	st := Status{Key: b.ID, Kind: b.Kind, Name: b.Name, Positive: !slices.Contains(negativeKinds, b.Kind), P: b.P, Q: b.Q, Remaining: n, BuffID: b.ID, AppliedBy: g.SourceSkill, ActiveFrom: starts}
	if b.Kind == "REGULATOR" {
		st.P = e.param("REGULATOR_SAVE")
	}
	if b.Kind == "CACHE" {
		st.Q = b.Cap
	}
	if b.Reapply == "add" {
		st.Remaining += previous.Remaining
		if slices.Contains(timedKinds, b.Kind) {
			st.Remaining = min(b.Cap, st.Remaining)
		}
	}
	st.Category = b.Category
	switch b.Kind {
	case "SPEED_MODE":
		st.Remaining = 0
	case "OVERLOAD":
		st.Expires = ptr(max(optional(previous.Expires, int64(0)), starts+n-1))
		st.Remaining = *st.Expires - starts + 1
	case "CACHE":
		st.Layers = min(b.Cap, previous.Layers+optional(g.Amount, int64(1)))
		st.RefreshedTurn = ptr(starts - 1)
		st.PersistentLayers = ptr(min(b.Cap, optional(previous.PersistentLayers, int64(0))+optional(g.PersistentLayers, int64(0))))
	case "COMBO":
		st.Layers = previous.Layers + optional(g.Amount, int64(1))
		st.Remaining = 0
	case "BASE_SUPPRESS", "MODEL_DEGRADATION", "ENERGY_STACK":
		st.Layers = min(b.Cap, previous.Layers+optional(g.Amount, int64(1)))
		st.Remaining = 0
	case "SOTA_FANATICISM":
		st.Layers = min(b.Cap, previous.Layers+optional(g.Amount, int64(1)))
		st.Remaining = 0
		st.RefreshedTurn = ptr(s.Round)
	case "SUBSCRIPTION_BAN":
		if old != nil && old.Expires == nil || n == 0 {
			st.Remaining = 0
		} else {
			st.Expires = ptr(max(optional(previous.Expires, int64(0)), starts+n-1))
		}
	default:
		st.Expires = ptr(starts + st.Remaining - 1)
	}
	p.Effects = slices.DeleteFunc(p.Effects, func(item Status) bool { return item.Key == st.Key })
	p.Effects = append(p.Effects, st)
	r.log("effect", ptr(g.Owner), map[string]any{"owner": g.Owner, "status": clone(st)})
}
func (r *roundRun) overload(seat int) {
	r.materialize(Grant{BuffID: r.e.buffKind("OVERLOAD").ID, Owner: seat, Duration: ptr(r.e.overloadDuration(r.s, seat)), SourceSkill: "resource-limit"}, r.s.Round+1)
}
func (r *roundRun) actionEffect(seat int, a Action) ([]Application, error) {
	e, s := r.e, r.s
	effect, source := a.Preview.Effect, a.Choice.SkillID
	recipient := seat
	if slices.Contains(negativeKinds, effect.Kind) {
		recipient = other(seat)
	}
	enemy := map[string]bool{}
	pending := []Grant{}
	grant := func(g Grant) {
		g.SourceSkill = source
		if e.characterPassives {
			pending = append(pending, g)
			return
		}
		s.Grants = append(s.Grants, g)
		if g.Owner != seat {
			enemy[g.BuffID] = true
		}
	}
	if (effect.Kind == "CACHE" || effect.Kind == "CACHE_COMBO") && effect.N > 0 && effect.BuffID != "" {
		target := source
		if effect.CacheTarget != "" {
			target = effect.CacheTarget
		}
		grant(Grant{BuffID: effect.BuffID, Owner: seat, TargetSkill: target})
	} else if slices.Contains([]string{"AMPLIFY", "SUPPRESS", "TOKEN_TAX", "NONBASIC_TAX", "SAVE_ENERGY", "API_DISCOUNT", "COUNTER", "STUN"}, effect.Kind) && effect.BuffID != "" {
		grant(Grant{BuffID: effect.BuffID, Owner: recipient})
	}
	if effect.Kind == "SUBSCRIPTION_BAN" && effect.BuffID != "" {
		grant(Grant{BuffID: effect.BuffID, Owner: other(seat), Duration: ptr(effect.N)})
	}
	if effect.Kind == "APOLOGY" && effect.BuffID != "" {
		if effect.P > 0 {
			grant(Grant{BuffID: effect.BuffID, Owner: seat, Amount: ptr(effect.P)})
		}
		if effect.Q > 0 {
			grant(Grant{BuffID: effect.BuffID, Owner: other(seat), Amount: ptr(effect.Q)})
		}
	}
	if effect.Kind == "DEGRADE" && effect.BuffID != "" {
		n := effect.P
		if s.LikesAtStart[other(seat)] > s.LikesAtStart[seat] {
			n += effect.Q
		}
		if n > 0 {
			grant(Grant{BuffID: effect.BuffID, Owner: other(seat), Amount: ptr(n)})
		}
	}
	if effect.Kind == "SELF_STUN" && effect.BuffID != "" {
		grant(Grant{BuffID: effect.BuffID, Owner: seat, Duration: ptr(effect.N)})
	}
	if effect.Kind == "ENERGY_STACK" && effect.BuffID != "" {
		grant(Grant{BuffID: effect.BuffID, Owner: seat, Amount: ptr(effect.N)})
	}
	if effect.Combo > 0 {
		grant(Grant{BuffID: e.buffKind("COMBO").ID, Owner: seat, Amount: ptr(effect.Combo)})
	}
	if effect.Kind == "COMBO" && effect.P > 0 {
		grant(Grant{BuffID: e.buffKind("COMBO").ID, Owner: seat, Amount: ptr(effect.P)})
	}
	if effect.ExtraBuffID != "" && effect.N > 0 {
		b := e.buffs[effect.ExtraBuffID]
		owner := seat
		if slices.Contains(negativeKinds, b.Kind) {
			owner = other(seat)
		}
		grant(Grant{BuffID: b.ID, Owner: owner})
	}
	if e.characterPassives {
		return r.applyGrants(seat, source, pending)
	}
	if sota := e.passive(s.Players[seat], "SOTA_ONLY"); sota != nil && sota.P > 0 {
		for range len(enemy) {
			s.Grants = append(s.Grants, Grant{BuffID: sota.BuffID, Owner: other(seat), SourceSkill: source, Amount: ptr(sota.P)})
		}
	}
	return nil, nil
}
func (r *roundRun) gainProgress(seat int, n int64) {
	r.materialize(Grant{BuffID: r.e.buffKind("COMBO").ID, Owner: seat, Amount: ptr(n)}, r.s.Round)
	for i := range r.s.Players[seat].Effects {
		st := &r.s.Players[seat].Effects[i]
		if st.Kind == "CACHE" && r.e.buffs[st.BuffID].RefreshOnCombo {
			st.RefreshedTurn = ptr(r.s.Round)
		}
	}
}
