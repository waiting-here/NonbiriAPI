package engine

import "github.com/waiting-here/NonbiriAPI/internal/game/likes/catalog"

// StepLikesSnapshot is transient settlement state. Round-start pricing and
// degradation keep using State.LikesAtStart. Neither seat observes a partial
// score commit from its opponent within this step.
type StepLikesSnapshot struct {
	Kind  string   `json:"kind"`
	Index int      `json:"index"`
	Likes [2]int64 `json:"likes"`
}

func stepLikes(s *State, kind string, index int) StepLikesSnapshot {
	return StepLikesSnapshot{kind, index, [2]int64{s.Players[0].Likes, s.Players[1].Likes}}
}

func (e *Engine) characterBonus(s *State, seat int, skill catalog.Skill, effect catalog.Effect, main bool, step StepLikesSnapshot) int64 {
	if !e.characterPassives {
		return 0
	}
	switch s.Players[seat].Role {
	case "Claude":
		if main && effect.Likes > 0 && step.Likes[seat] > step.Likes[other(seat)] {
			return 1
		}
	case "Gemini":
		if skill.Kind == "basic" {
			return 1
		}
	}
	return 0
}

func (r *roundRun) scoreSteps(actions [2][]Action, reductions, rewards [2]int64) ([2]int64, error) {
	e, s := r.e, r.s
	drains := [2]int64{}
	for slot := 0; slot < max(1, len(actions[0]), len(actions[1])); slot++ {
		kind, index := "main", 0
		if slot > 0 {
			kind, index = "extra", slot-1
		}
		r.step = stepLikes(s, kind, index)
		gains := [2]int64{}
		if slot == 0 {
			gains = rewards
		}
		for seat := range 2 {
			if slot >= len(actions[seat]) {
				continue
			}
			a := actions[seat][slot]
			// Payment only cancels a suffix; it never compacts failed slots.
			if a.Main != (slot == 0) {
				return drains, ErrInvariant
			}
			effect, skill := a.Preview.Effect, e.skills[a.Choice.SkillID]
			reduction := int64(0)
			if a.Main {
				reduction = reductions[seat]
			}
			bonus := e.characterBonus(s, seat, skill, effect, a.Main, r.step)
			score := e.score(s, seat, skill, effect, a.Preview.TemplateID, a.Preview.ConditionalLikes, a.Derived, reduction, bonus)
			consumeDecay(&s.Players[seat], skill.ID, a.Preview.TemplateID, effect)
			consumeDegradation(s, seat)
			gains[seat] += score.Final
			if effect.Kind == "BURST_DRAIN" {
				drains[other(seat)] += effect.P
			}
			cast := r.castEvent(seat, a, score)
			applications, err := r.actionEffect(seat, a)
			if err != nil {
				return drains, err
			}
			if len(applications) > 0 {
				r.record.Events[cast].Data["applications"] = applications
			}
		}
		for seat := range 2 {
			s.Players[seat].Likes += gains[seat]
		}
	}
	return drains, nil
}

type Application struct {
	BuffID   string `json:"buff_id"`
	Target   int    `json:"target"`
	Success  int64  `json:"success"`
	Resisted int64  `json:"resisted"`
	Derived  bool   `json:"derived"`
}

func (r *roundRun) attributes(source, target int) (hit, resist int64) {
	if r.s.Players[source].Role == "DeepSeek" {
		hit = 25
		if r.step.Likes[source] > r.step.Likes[target] {
			hit += 25
		}
	}
	if r.s.Players[target].Role == "GLM" {
		resist = 25
		if r.step.Likes[target] < r.step.Likes[source] {
			resist += 25
		}
	}
	return
}

func (r *roundRun) attempt(source int, g Grant, derived bool) (Grant, Application, error) {
	b := r.e.buffs[g.BuffID]
	outcome := Application{BuffID: g.BuffID, Target: g.Owner, Derived: derived}
	count := int64(1)
	switch b.Kind {
	case "BASE_SUPPRESS", "MODEL_DEGRADATION", "SOTA_FANATICISM":
		count = optional(g.Amount, int64(1))
	}
	if count < 1 || count > MaxAttemptLayers {
		return Grant{}, outcome, ErrInvariant
	}
	hit, resist := r.attributes(source, g.Owner)
	numerator, denominator := 100+hit, 100+resist
	for layer := int64(1); layer <= count; layer++ {
		var draw *int64
		ok := numerator >= denominator
		if !ok {
			index, err := r.randomIndex(int(denominator))
			if err != nil {
				return Grant{}, outcome, err
			}
			draw = ptr(r.s.DrawSeq)
			ok = int64(index) < numerator
		}
		if ok {
			outcome.Success++
		} else {
			outcome.Resisted++
		}
		r.log("effect-attempt", ptr(source), map[string]any{"rules_version": catalog.BehaviorVersion, "step": r.step, "source": source, "target": g.Owner, "skill_id": g.SourceSkill, "buff_id": g.BuffID, "layer": layer, "hit": hit, "resist": resist, "numerator": numerator, "denominator": denominator, "success": ok, "draw": draw, "derived": derived})
	}
	if g.Amount != nil {
		g.Amount = ptr(outcome.Success)
	}
	return g, outcome, nil
}

// Catalog order, then layer order, then that kind's derived SOTA attempt is
// stable across map layouts, seats and replays. Only successful grants survive.
func (r *roundRun) applyGrants(source int, skill string, grants []Grant) ([]Application, error) {
	results := []Application{}
	for _, b := range r.e.c.Buffs {
		enemySuccess := false
		for _, g := range grants {
			if g.BuffID != b.ID {
				continue
			}
			if g.Owner != source && b.Category == "debuff" {
				accepted, outcome, err := r.attempt(source, g, false)
				if err != nil {
					return nil, err
				}
				results = append(results, outcome)
				if outcome.Success == 0 {
					continue
				}
				g = accepted
			}
			r.s.Grants = append(r.s.Grants, g)
			enemySuccess = enemySuccess || g.Owner != source
		}
		if sota := r.e.passive(r.s.Players[source], "SOTA_ONLY"); enemySuccess && sota != nil && sota.P > 0 {
			g := Grant{BuffID: sota.BuffID, Owner: other(source), SourceSkill: skill, Amount: ptr(sota.P)}
			accepted, outcome, err := r.attempt(source, g, true)
			if err != nil {
				return nil, err
			}
			results = append(results, outcome)
			if outcome.Success > 0 {
				r.s.Grants = append(r.s.Grants, accepted)
			}
		}
	}
	return results, nil
}

// At most four paid casts, two hostile kinds per cast, four layers per kind
// and four derived SOTA layers give 64 attempts. Random cleansing adds at most
// 16 draws. Ten Flash casts grant only self effects. 96 draws times 75 rounds
// stays below the existing 10,000-state and 65,664-proof sample bounds.
const MaxAttemptLayers int64 = 4
const MaxRoundDraws = 96
const MaxRoundEvents = 512
