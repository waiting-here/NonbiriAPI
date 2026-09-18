package engine

import (
	"crypto/rand"
	"encoding/json"
	"math/big"
	"slices"
)

func cryptoIndex(count int) (int, error) {
	if count < 1 {
		return 0, ErrRandom
	}
	n, err := rand.Int(rand.Reader, big.NewInt(int64(count)))
	if err != nil {
		return 0, ErrRandom
	}
	return int(n.Int64()), nil
}
func (r *roundRun) randomIndex(count int) (int, error) {
	index, err := r.pick(count)
	if err != nil || index < 0 || index >= count {
		return 0, ErrRandom
	}
	r.s.DrawSeq++
	r.record.Draws = append(r.record.Draws, Draw{Ordinal: r.s.DrawSeq, CandidateCount: count, Index: index})
	return index, nil
}

// Resolve computes a complete round atomically in memory. Production callers
// pass nil for cryptographic random selection; tests can inject a fixed tape.
func (e *Engine) Resolve(previous State, plans [2]Plan, pick func(int) (int, error)) (State, RoundRecord, error) {
	if err := e.Validate(previous); err != nil {
		return State{}, RoundRecord{}, err
	}
	if previous.Result != nil || previous.AwaitingNextRound {
		return State{}, RoundRecord{}, ErrState
	}
	for seat := range 2 {
		if previous.Blocked[seat] {
			plans[seat] = EmptyPlan()
			continue
		}
		var err error
		plans[seat], err = e.ValidatePlan(previous, seat, plans[seat])
		if err != nil {
			return State{}, RoundRecord{}, err
		}
	}
	if pick == nil {
		pick = cryptoIndex
	}
	s := clone(previous)
	record := RoundRecord{Round: s.Round, Plans: clone(plans), Before: frame(&s, "before", 0), Frames: []Frame{}, Events: []Event{}, Draws: []Draw{}}
	run := roundRun{e: e, s: &s, record: &record, pick: pick}
	if err := run.settle(plans); err != nil {
		return State{}, RoundRecord{}, err
	}
	record.After = frame(&s, "after", len(record.Events))
	record.Result = clone(s.Result)
	if err := e.Validate(s); err != nil {
		return State{}, RoundRecord{}, err
	}
	body, err := json.Marshal(record)
	if err != nil || len(body) > MaxRoundBytes {
		return State{}, RoundRecord{}, ErrInvariant
	}
	return s, record, nil
}

func (r *roundRun) end(reason string, forced *int) {
	s := r.s
	result := Result{Reason: reason, Scores: [2]int64{s.Players[0].Likes, s.Players[1].Likes}, Winner: clone(forced)}
	if forced == nil {
		if result.Scores[0] > result.Scores[1] {
			result.Winner = ptr(0)
		} else if result.Scores[1] > result.Scores[0] {
			result.Winner = ptr(1)
		}
	}
	s.Result = &result
	r.log("end", nil, map[string]any{"result": clone(result)})
}
func (r *roundRun) failShared(seat int, plan Plan, record *TurnRecord) {
	record.Stunned = true
	if plan.Main != nil {
		r.s.CastSeq++
		record.LastMain = &Sample{ID: r.s.CastSeq, SkillID: plan.Main.SkillID, Kind: r.e.skills[plan.Main.SkillID].Kind, Success: false}
	}
	for _, a := range appendMain(plan) {
		r.log("skill-cancelled", ptr(seat), map[string]any{"skillId": a.SkillID, "success": false, "reason": "shared-energy"})
	}
}
func appendMain(plan Plan) []Choice {
	if plan.Main == nil {
		return []Choice{}
	}
	return append([]Choice{*plan.Main}, plan.Extra...)
}
func clearOverloadCache(p *Player) {
	p.Effects = slices.DeleteFunc(p.Effects, func(st Status) bool {
		return slices.Contains([]string{"CACHE", "COMBO", "REGULATOR"}, st.Kind) && !(st.Kind == "CACHE" && optional(st.PersistentLayers, int64(0)) > 0)
	})
	for i := range p.Effects {
		st := &p.Effects[i]
		if st.Kind == "CACHE" {
			st.Layers = optional(st.PersistentLayers, int64(0))
		}
	}
}

func (r *roundRun) settle(plans [2]Plan) error {
	e, s := r.e, r.s
	r.stage = "reveal"
	for seat := range 2 {
		for _, choice := range appendMain(plans[seat]) {
			if !slices.Contains(s.Players[seat].Revealed, choice.SkillID) {
				s.Players[seat].Revealed = append(s.Players[seat].Revealed, choice.SkillID)
			}
		}
	}
	r.log("reveal", nil, map[string]any{"plans": clone(plans)})
	r.snapshot(r.stage)
	r.stage = "shopping"
	initial, requested := s.Energy, int64(0)
	for seat := range 2 {
		s.Energy = initial
		used := []string{}
		for _, purchase := range plans[seat].Purchases {
			v, charge, err := e.shop(s, seat, purchase, &used)
			if err != nil {
				return ErrInvariant
			}
			requested += charge
			amount := v.Amount
			if purchase.Item == "charge" {
				amount = charge
			}
			data := map[string]any{"item": purchase.Item, "price": v.Price, "amount": amount}
			if purchase.Target != "" {
				data["target"] = purchase.Target
			}
			r.log("shop", ptr(seat), data)
		}
	}
	s.Energy = min(e.param("ENERGY_CAP"), initial+requested)
	if requested > 0 {
		r.log("charge", nil, map[string]any{"requested": requested, "actual": s.Energy - initial, "overflow": requested - (s.Energy - initial)})
	}
	r.snapshot(r.stage)
	r.stage = "payment"
	quotes := [2]PlanPreview{}
	needed := int64(0)
	for seat := range 2 {
		var err error
		quotes[seat], err = e.quote(s, seat, plans[seat])
		if err != nil {
			return ErrInvariant
		}
		needed += quotes[seat].Energy
	}
	successful := [2][]Action{{}, {}}
	newOverload := [2]bool{}
	records := [2]*TurnRecord{{}, {}}
	for seat := range 2 {
		records[seat].Skipped = plans[seat].Main == nil
	}
	if needed > s.Energy {
		available := s.Energy
		s.Energy = 0
		for seat := range 2 {
			if quotes[seat].Energy > 0 {
				newOverload[seat] = true
				r.failShared(seat, plans[seat], records[seat])
			}
		}
		if newOverload[0] && newOverload[1] {
			for seat := range 2 {
				r.overload(seat)
				clearOverloadCache(&s.Players[seat])
				records[seat].Skipped = false
			}
			s.Records = records
			s.Grants = []Grant{}
			r.log("overload", nil, map[string]any{"required": needed, "available": available, "quotes": [2]int64{quotes[0].Energy, quotes[1].Energy}, "overloaded": newOverload, "reason": "shared-energy", "shortage": energyShortage(needed, available)})
			r.end("double-overload", nil)
			r.snapshot(r.stage)
			return nil
		}
		r.log("overload", nil, map[string]any{"required": needed, "available": available, "quotes": [2]int64{quotes[0].Energy, quotes[1].Energy}, "overloaded": newOverload, "reason": "shared-energy", "shortage": energyShortage(needed, available)})
	}
	for seat := range 2 {
		if newOverload[seat] {
			continue
		}
		for _, a := range quotes[seat].Actions {
			if a.Cancelled {
				r.log("skill-cancelled", ptr(seat), map[string]any{"skillId": a.Choice.SkillID, "success": false, "reason": "previous-failure"})
				continue
			}
			ok := !slices.Contains(a.Preview.Shortages, "token")
			s.CastSeq++
			sample := &Sample{ID: s.CastSeq, SkillID: a.Choice.SkillID, Success: ok, Kind: e.skills[a.Choice.SkillID].Kind, Derived: a.Derived}
			records[seat].Last = sample
			if a.Main {
				records[seat].LastMain = sample
			}
			if !ok {
				newOverload[seat] = true
				records[seat].Stunned = true
				r.log("overload", ptr(seat), map[string]any{"skillId": a.Choice.SkillID, "reason": "personal-resources", "success": false, "shortage": e.tokenShortage(s.Players[seat], a)})
				for _, cancelled := range quotes[seat].Actions {
					if cancelled.Cancelled {
						r.log("skill-cancelled", ptr(seat), map[string]any{"skillId": cancelled.Choice.SkillID, "success": false, "reason": "previous-failure"})
					}
				}
				break
			}
			e.pay(s, seat, a)
			s.Energy -= a.Preview.Energy
			successful[seat] = append(successful[seat], a)
			records[seat].LastSuccess = sample
			if sample.Kind != "basic" {
				records[seat].NonbasicSuccess = ptr(true)
			}
			if e.skills[a.Choice.SkillID].Copyable && a.Choice.SkillID != "PUB41" {
				records[seat].LastCopyable = sample
			}
		}
	}
	r.snapshot(r.stage)
	r.stage = "cleansing"
	type removal struct {
		owner int
		key   string
		by    int
	}
	removals := []removal{}
	for seat := range 2 {
		for _, a := range successful[seat] {
			mode := effectMode(a.Preview.Effect, a.Choice)
			if mode == "" {
				continue
			}
			owner := seat
			if mode == "opponent" {
				owner = other(seat)
			}
			targets := slices.Clone(a.Choice.Targets)
			if a.Preview.Effect.RandomTargets {
				candidates := []string{}
				for _, st := range removable(s, seat, mode) {
					candidates = append(candidates, st.Key)
				}
				slices.Sort(candidates)
				targets = []string{}
				for int64(len(targets)) < a.Preview.Effect.P && len(candidates) > 0 {
					index, err := r.randomIndex(len(candidates))
					if err != nil {
						return err
					}
					targets = append(targets, candidates[index])
					candidates = slices.Delete(candidates, index, index+1)
				}
			}
			for _, key := range targets {
				removals = append(removals, removal{owner: owner, key: key, by: seat})
			}
		}
	}
	for _, item := range removals {
		p := &s.Players[item.owner]
		if slices.ContainsFunc(p.Effects, func(st Status) bool { return st.Key == item.key && st.Category != "state" }) {
			p.Effects = slices.DeleteFunc(p.Effects, func(st Status) bool { return st.Key == item.key })
			r.log("cleanse", ptr(item.by), map[string]any{"owner": item.owner, "key": item.key})
		}
	}
	r.snapshot(r.stage)
	r.stage = "score"
	reductions, rewards := [2]int64{}, [2]int64{}
	for seat := range 2 {
		for _, a := range successful[seat] {
			if a.Preview.Effect.Kind == "PREDICT_COUNTER" {
				target := plans[other(seat)].Main
				hit := target != nil && e.skills[target.SkillID].Kind != "basic"
				if hit {
					reductions[other(seat)] += a.Preview.Effect.P
					rewards[seat] += e.finalLikes(s, seat, a.Preview.Effect.Q)
				}
				reward, reduction := int64(0), int64(0)
				if hit {
					reward = e.finalLikes(s, seat, a.Preview.Effect.Q)
					reduction = a.Preview.Effect.P
				}
				r.log("counter", ptr(seat), map[string]any{"hit": hit, "reduction": reduction, "reward": reward})
			}
		}
	}
	gains, drains := rewards, [2]int64{}
	for seat := range 2 {
		for _, a := range successful[seat] {
			effect, sk := a.Preview.Effect, e.skills[a.Choice.SkillID]
			reduction := int64(0)
			if a.Main {
				reduction = reductions[seat]
			}
			score := e.score(s, seat, sk, effect, a.Preview.TemplateID, a.Preview.ConditionalLikes, a.Derived, reduction)
			consumeDecay(&s.Players[seat], sk.ID, a.Preview.TemplateID, effect)
			consumeDegradation(s, seat)
			gains[seat] += score.Final
			if effect.Kind == "BURST_DRAIN" {
				drains[other(seat)] += effect.P
			}
			r.castEvent(seat, a, score)
			r.actionEffect(seat, a)
		}
	}
	scoreFrame := frame(s, r.stage, len(r.record.Events))
	for seat := range 2 {
		scoreFrame.Players[seat].Likes += gains[seat]
	}
	r.record.Frames = append(r.record.Frames, scoreFrame)
	r.stage = "aftereffects"
	for seat := range 2 {
		if slices.ContainsFunc(successful[seat], func(a Action) bool { return a.Preview.Effect.Kind == "PERSIST_CACHE" }) {
			for i := range s.Players[seat].Effects {
				st := &s.Players[seat].Effects[i]
				if st.Kind == "CACHE" {
					st.PersistentLayers = ptr(st.Layers)
					r.log("persist", ptr(seat), map[string]any{"buffId": st.BuffID, "layers": st.Layers})
				}
			}
		}
	}
	r.conversions(successful)
	if err := r.combos(plans, newOverload); err != nil {
		return err
	}
	for seat := range 2 {
		for _, a := range successful[seat] {
			if a.Preview.Effect.Kind == "RESOURCE_GAIN" {
				effect := a.Preview.Effect
				s.Players[seat].Gold += effect.P
				s.Players[seat].API += effect.Q
				r.log("resource-gain", ptr(seat), map[string]any{"gold": effect.P, "api": effect.Q})
			}
		}
	}
	for seat := range 2 {
		p := &s.Players[seat]
		if free := e.passive(*p, "FREE_TRIAL"); free != nil {
			n := int64(0)
			for _, a := range successful[other(seat)] {
				if e.skills[a.Choice.SkillID].Kind != "basic" {
					n += free.P
				}
			}
			if n > 0 {
				*p.Trial += n
				r.log("trial", ptr(seat), map[string]any{"amount": n})
			}
		}
	}
	for seat := range 2 {
		s.Players[seat].Likes += gains[seat]
		before := s.Players[seat].Burst
		s.Players[seat].Burst = max(0, before-drains[seat])
		if drains[seat] > 0 {
			r.log("resource", ptr(seat), map[string]any{"resource": "burst", "requested": drains[seat], "before": before, "after": s.Players[seat].Burst})
		}
	}
	reset := false
	for seat := range 2 {
		reset = reset || slices.ContainsFunc(successful[seat], func(a Action) bool { return a.Preview.Effect.Kind == "USAGE_RESET" })
	}
	if reset {
		for seat := range 2 {
			e.resetAllUsage(&s.Players[seat])
		}
		r.log("usage-reset", nil, nil)
	}
	for seat := range 2 {
		for _, a := range successful[seat] {
			if a.Choice.SkillID == "PUB41" {
				l := e.learning(s, seat)
				if l.Changes {
					d := &s.Players[seat].Distill
					d.Template, d.Level = clone(l.Template), clone(l.Level)
					d.Learning--
					d.UsedSamples = append(d.UsedSamples, l.Sample.ID)
					r.log("learn", ptr(seat), map[string]any{"template": clone(d.Template), "level": clone(d.Level), "remaining": d.Learning, "sample": l.Sample.ID})
					break
				}
			}
		}
	}
	r.snapshot(r.stage)
	r.stage = "round-end"
	r.expire(plans, successful, newOverload)
	s.Records = records
	double := true
	for seat := range 2 {
		double = double && slices.ContainsFunc(s.Players[seat].Effects, func(st Status) bool { return st.Kind == "OVERLOAD" && optional(st.Expires, int64(0)) > s.Round })
	}
	if double {
		r.end("double-overload", nil)
	} else if s.Players[0].Likes >= e.param("TARGET_LIKES") || s.Players[1].Likes >= e.param("TARGET_LIKES") {
		r.end("target", nil)
	} else if s.Round >= e.param("MAX_ROUNDS") {
		r.end("limit", nil)
	}
	if s.Result == nil {
		before := s.Energy
		s.Energy = min(e.param("ENERGY_CAP"), s.Energy+e.param("POWER_GENERATION"))
		r.log("power", nil, map[string]any{"requested": e.param("POWER_GENERATION"), "amount": s.Energy - before, "before": before, "after": s.Energy})
		s.AwaitingNextRound = true
	}
	r.snapshot(r.stage)
	return nil
}
