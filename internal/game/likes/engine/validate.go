package engine

import (
	"slices"
	"strings"
)

func safe(value int64) bool { return value >= 0 && value <= MaxResourceValue }

func (e *Engine) Validate(s State) error {
	if s.Version != 1 || s.Mode != e.c.Mode || s.Round < 1 || s.Round > e.param("MAX_ROUNDS") || s.Energy < 0 || s.Energy > e.param("ENERGY_CAP") || s.CastSeq < 0 || s.CastSeq > 4*e.param("MAX_ROUNDS") || s.EventSeq < 0 || s.EventSeq > 100_000 || s.DrawSeq < 0 || s.DrawSeq > 10_000 || len(s.Grants) > 64 {
		return ErrState
	}
	if s.AwaitingNextRound && (s.Result != nil || s.Round >= e.param("MAX_ROUNDS")) {
		return ErrState
	}
	for seat, p := range s.Players {
		if s.Result == nil && !s.AwaitingNextRound {
			overloaded, stunned := restriction(&s, seat)
			if s.Blocked[seat] != overloaded || p.Stunned != stunned {
				return ErrState
			}
		}
		if err := e.ValidateSelection(Selection{Role: p.Role, Harness: p.Harness, Skills: p.Loadout}); err != nil {
			return ErrState
		}
		if p.ActiveSlots != e.slots(p.Harness) || p.NormalTurns != s.Round || len(p.Effects) > len(e.c.Buffs) || p.Used == nil || p.Resources == nil || p.ResourceCaps == nil {
			return ErrState
		}
		for _, value := range []int64{p.Gold, p.Likes, p.Burst, p.BurstCap, p.Sub, p.API, p.Images, p.APIPack, s.LikesAtStart[seat], p.Subscription.BurstInitial, p.Subscription.TotalInitial, p.Subscription.TotalCap} {
			if !safe(value) {
				return ErrState
			}
		}
		if p.Burst > p.BurstCap || p.Sub > p.Subscription.TotalCap || p.Subscription.TotalCap != p.BurstCap*2 || p.Subscription.TotalInitial != p.Subscription.BurstInitial*2 || p.Subscription.BurstInitial != e.initialBurst(e.roles[p.Role]) {
			return ErrState
		}
		if p.Role == "DeepSeek" && (p.BurstCap != 0 || p.Sub != 0) {
			return ErrState
		}
		for _, clock := range []*int64{p.Subscription.BurstResetAt, p.Subscription.TotalResetAt} {
			if clock != nil && (!safe(*clock) || *clock <= s.Round) {
				return ErrState
			}
		}
		if e.passive(p, "FREE_TRIAL") != nil {
			if p.Trial == nil || !safe(*p.Trial) {
				return ErrState
			}
		} else if p.Trial != nil {
			return ErrState
		}
		allocations := e.roles[p.Role].Resources
		if len(p.Resources) != len(allocations) || len(p.ResourceCaps) != len(allocations) {
			return ErrState
		}
		for key := range allocations {
			value, ok := p.Resources[key]
			cap, capOK := p.ResourceCaps[key]
			if !ok || !capOK || !safe(value) || !safe(cap) || value > cap {
				return ErrState
			}
		}
		if len(p.Used) > len(p.Loadout) || len(p.Revealed) > len(p.Loadout) {
			return ErrState
		}
		for key, value := range p.Used {
			if !slices.Contains(p.Loadout, key) || value < 0 || value > 2*s.Round {
				return ErrState
			}
			if limit := e.skills[key].MaxUses; limit != nil && value > *limit {
				return ErrState
			}
		}
		seen := map[string]bool{}
		for _, key := range p.Revealed {
			if seen[key] || !slices.Contains(p.Loadout, key) {
				return ErrState
			}
			seen[key] = true
		}
		if p.Distill.Learning < 0 || p.Distill.Learning > e.skills["PUB41"].Learn || len(p.Distill.UsedSamples) > int(e.skills["PUB41"].Learn) {
			return ErrState
		}
		if (p.Distill.Template == nil) != (p.Distill.Level == nil) {
			return ErrState
		}
		if p.Distill.Template != nil && (!e.skills[*p.Distill.Template].Copyable || !slices.Contains([]string{"I", "II"}, *p.Distill.Level)) {
			return ErrState
		}
		samples := map[int64]bool{}
		for _, id := range p.Distill.UsedSamples {
			if id < 1 || id > s.CastSeq || samples[id] {
				return ErrState
			}
			samples[id] = true
		}
		for key, n := range p.SkillDecay {
			template := strings.TrimSuffix(key, ":distilled")
			if e.skills[template].Effects.Base.Kind != "SVG_DECAY" || n < 0 || n > 2*s.Round {
				return ErrState
			}
		}
		seen = map[string]bool{}
		for _, st := range p.Effects {
			b, ok := e.buffs[st.BuffID]
			if !ok || seen[st.Key] || st.Key != b.ID || st.Kind != b.Kind || st.Category != b.Category || st.Name != b.Name || st.Positive == slices.Contains(negativeKinds, b.Kind) {
				return ErrState
			}
			seen[st.Key] = true
			expectP, expectQ := b.P, b.Q
			if b.Kind == "REGULATOR" {
				expectP = e.param("REGULATOR_SAVE")
			}
			if b.Kind == "CACHE" {
				expectQ = b.Cap
			}
			if st.P != expectP || st.Q != expectQ || st.Remaining < 0 || st.Remaining > e.param("MAX_ROUNDS")+2 || st.Layers < 0 || st.Layers > 1_000 || st.ActiveFrom < 1 || st.ActiveFrom > s.Round+1 || len(st.AppliedBy) > 80 {
				return ErrState
			}
			if st.Expires != nil && (*st.Expires < st.ActiveFrom || *st.Expires > e.param("MAX_ROUNDS")+6) {
				return ErrState
			}
			if st.RefreshedTurn != nil && (*st.RefreshedTurn < 0 || *st.RefreshedTurn > s.Round) {
				return ErrState
			}
			if st.Kind == "CACHE" && (st.Layers > b.Cap || optional(st.PersistentLayers, int64(0)) < 0 || optional(st.PersistentLayers, int64(0)) > st.Layers) {
				return ErrState
			}
			if slices.Contains([]string{"ENERGY_STACK", "SOTA_FANATICISM", "BASE_SUPPRESS", "MODEL_DEGRADATION"}, st.Kind) && (st.Layers < 1 || st.Layers > b.Cap) {
				return ErrState
			}
			if st.Kind == "COMBO" && (st.Layers < 1 || st.Layers >= b.P) {
				return ErrState
			}
			if b.Reapply == "add" && slices.Contains(timedKinds, b.Kind) && (st.Remaining < 1 || st.Remaining > b.Cap) {
				return ErrState
			}
		}
		if rec := s.Records[seat]; rec != nil {
			for _, sample := range []*Sample{rec.Last, rec.LastMain, rec.LastSuccess, rec.LastCopyable} {
				if sample == nil {
					continue
				}
				if sample.ID < 1 || sample.ID > s.CastSeq || sample.Kind != e.skills[sample.SkillID].Kind || !slices.Contains(p.Loadout, sample.SkillID) || sample.Derived {
					return ErrState
				}
			}
			if rec.LastSuccess != nil && !rec.LastSuccess.Success || rec.LastCopyable != nil && (!rec.LastCopyable.Success || !e.skills[rec.LastCopyable.SkillID].Copyable) {
				return ErrState
			}
		}
	}
	for _, g := range s.Grants {
		if !validSeat(g.Owner) || e.buffs[g.BuffID].ID == "" || optional(g.Amount, int64(1)) < 1 || optional(g.Amount, int64(1)) > 4 || optional(g.Duration, int64(0)) < 0 || optional(g.Duration, int64(0)) > 6 {
			return ErrState
		}
	}
	if s.Result != nil {
		if !slices.Contains([]string{"target", "double-overload", "limit", "surrender"}, s.Result.Reason) || s.Result.Scores != ([2]int64{s.Players[0].Likes, s.Players[1].Likes}) || s.Result.Winner != nil && !validSeat(*s.Result.Winner) {
			return ErrState
		}
		if s.Result.Reason != "surrender" {
			winner := -1
			if s.Players[0].Likes > s.Players[1].Likes {
				winner = 0
			} else if s.Players[1].Likes > s.Players[0].Likes {
				winner = 1
			}
			if optional(s.Result.Winner, -1) != winner {
				return ErrState
			}
		}
		if s.Result.Reason == "surrender" && s.Result.Winner == nil || s.Result.Reason == "limit" && s.Round != e.param("MAX_ROUNDS") || s.Result.Reason == "target" && s.Players[0].Likes < e.param("TARGET_LIKES") && s.Players[1].Likes < e.param("TARGET_LIKES") {
			return ErrState
		}
		if s.Result.Reason == "double-overload" {
			for _, p := range s.Players {
				if !slices.ContainsFunc(p.Effects, func(st Status) bool { return st.Kind == "OVERLOAD" && optional(st.Expires, int64(0)) > s.Round }) {
					return ErrState
				}
			}
		}
	}
	return nil
}
