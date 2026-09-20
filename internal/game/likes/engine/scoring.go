package engine

import (
	"github.com/waiting-here/NonbiriAPI/internal/game/likes/catalog"
	"slices"
)

var timedKinds = []string{"AMPLIFY", "SUPPRESS", "TOKEN_TAX", "NONBASIC_TAX", "SAVE_ENERGY", "API_DISCOUNT", "REGULATOR"}
var negativeKinds = []string{"STUN", "STOP", "SUBSCRIPTION_BAN", "SUPPRESS", "TOKEN_TAX", "NONBASIC_TAX", "OVERLOAD", "SOTA_FANATICISM", "BASE_SUPPRESS", "MODEL_DEGRADATION"}

func decayKey(id, template string) string {
	if id == template {
		return template
	}
	return template + ":distilled"
}
func consumeDecay(p *Player, id, template string, effect catalog.Effect) {
	if effect.Kind != "SVG_DECAY" {
		return
	}
	if p.SkillDecay == nil {
		p.SkillDecay = map[string]int64{}
	}
	p.SkillDecay[decayKey(id, template)]++
}
func consumeDegradation(s *State, seat int) {
	p := &s.Players[seat]
	p.Effects = slices.DeleteFunc(p.Effects, func(st Status) bool { return st.Kind == "MODEL_DEGRADATION" && active(s, st) && st.Layers <= 1 })
	for i := range p.Effects {
		st := &p.Effects[i]
		if st.Kind == "MODEL_DEGRADATION" && active(s, *st) {
			st.Layers--
		}
	}
}
func (e *Engine) baseBonus(s *State, seat int, sk catalog.Skill, cost catalog.Cost, base int64) int64 {
	if base <= 0 {
		return 0
	}
	p := s.Players[seat]
	bonus := int64(0)
	if strength := e.passive(p, "LIKE_STRENGTH"); strength != nil && cost.Token > 0 && cost.Energy > 0 {
		bonus += strength.P
		if sk.ResourceCosts["R_IMAGE"] > 0 {
			bonus += strength.Q
		}
	}
	if student := e.passive(p, "STUDENT_DISCOUNT"); student != nil && s.LikesAtStart[seat] < s.LikesAtStart[other(seat)] {
		bonus += student.P
	}
	return bonus
}
func (e *Engine) castPassive(s *State, seat int, sk catalog.Skill) int64 {
	p, n := s.Players[seat], int64(0)
	if sk.Kind == "basic" {
		if passive := e.passive(p, "PRO_EXPERIENCE"); passive != nil {
			n += passive.P
		}
	}
	if sk.Payment == "api" {
		if passive := e.passive(p, "API_SPECIALIST"); passive != nil {
			n += passive.P
		}
	}
	return e.finalLikes(s, seat, n)
}
func (e *Engine) basis(s *State, seat int, sk catalog.Skill, effect catalog.Effect, template string, character ...int64) (ScoreBreakdown, int64) {
	p := s.Players[seat]
	b := ScoreBreakdown{Original: effect.Likes, Parts: []ScorePart{}}
	raw := effect.Likes
	if effect.Kind == "SVG_DECAY" {
		raw = max(effect.Q, raw-p.SkillDecay[decayKey(sk.ID, template)]*effect.P)
		if raw != effect.Likes {
			b.Parts = append(b.Parts, ScorePart{Key: "skill-decay", Amount: raw - effect.Likes})
		}
	}
	bonus := e.baseBonus(s, seat, sk, sk.Cost(), effect.Likes)
	if bonus != 0 {
		b.Parts = append(b.Parts, ScorePart{Key: "harness", Amount: bonus})
	}
	if len(character) > 0 && character[0] != 0 {
		bonus += character[0]
		b.Parts = append(b.Parts, ScorePart{Key: "character", Amount: character[0]})
	}
	penalty := int64(0)
	for _, st := range p.Effects {
		if st.Kind == "BASE_SUPPRESS" && active(s, st) {
			n := st.P * st.Layers
			penalty += n
			b.Parts = append(b.Parts, ScorePart{Key: "base-suppress", BuffID: st.BuffID, Amount: -n})
		}
	}
	b.Intrinsic = max(0, raw+bonus-penalty)
	if raw+bonus-penalty < 0 {
		b.Parts = append(b.Parts, ScorePart{Key: "base-floor", Amount: -(raw + bonus - penalty)})
	}
	if effect.Kind == "LOW_POWER" && s.Energy <= effect.P {
		b.Conditional += effect.Q
	}
	target := other(seat)
	if effect.AuditTarget == "self" {
		target = seat
	}
	if record := s.Records[target]; effect.Kind == "AUDIT" && record != nil && record.LastMain != nil && record.LastMain.Success && record.LastMain.Kind != "basic" {
		b.Conditional += effect.P
	}
	if effect.Kind == "DUAL_AUDIT" {
		if record := s.Records[seat]; record != nil && optional(record.NonbasicSuccess, false) {
			b.Conditional += effect.P
		}
		if record := s.Records[other(seat)]; record != nil && optional(record.NonbasicSuccess, false) {
			b.Conditional += effect.Q
		}
	}
	return b, bonus
}
func (e *Engine) score(s *State, seat int, sk catalog.Skill, effect catalog.Effect, template string, conditional int64, derived bool, reduction int64, character ...int64) ScoreBreakdown {
	b, _ := e.basis(s, seat, sk, effect, template, character...)
	b.Conditional = conditional
	if conditional != 0 {
		b.Parts = append(b.Parts, ScorePart{Key: "condition", Amount: conditional})
	}
	n := b.Intrinsic + conditional
	if !derived && n > 0 {
		for _, st := range s.Players[seat].Effects {
			if !active(s, st) {
				continue
			}
			if st.Kind == "AMPLIFY" {
				n += st.P
				b.Parts = append(b.Parts, ScorePart{Key: "amplify", Amount: st.P, BuffID: st.BuffID})
			}
			if st.Kind == "SUPPRESS" {
				n -= st.P
				b.Parts = append(b.Parts, ScorePart{Key: "suppress", Amount: -st.P, BuffID: st.BuffID})
			}
		}
	}
	for _, st := range s.Players[seat].Effects {
		if st.Kind == "MODEL_DEGRADATION" && active(s, st) {
			amount := st.P * st.Layers
			n -= amount
			b.Parts = append(b.Parts, ScorePart{Key: "degradation", Amount: -amount, BuffID: st.BuffID})
		}
	}
	if reduction != 0 {
		n -= reduction
		b.Parts = append(b.Parts, ScorePart{Key: "counter", Amount: -reduction})
	}
	b.BeforeMultiplier = max(0, n)
	if n < 0 {
		b.Parts = append(b.Parts, ScorePart{Key: "score-floor", Amount: -n})
	}
	b.Multiplier = e.multiplier(s, seat)
	b.Passive = e.castPassive(s, seat, sk)
	b.Final = b.BeforeMultiplier*b.Multiplier + b.Passive
	return b
}
