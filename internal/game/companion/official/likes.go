package official

import (
	"encoding/json"
	"math"
	"slices"
	"sync"

	"github.com/waiting-here/NonbiriAPI/internal/game/likes/catalog"
	"github.com/waiting-here/NonbiriAPI/internal/game/likes/engine"
)

type likesRules struct {
	engine *engine.Engine
	config catalog.Config
	skills map[string]catalog.Skill
}

func loadLikes(mode string) (*likesRules, error) {
	e, err := engine.New(mode)
	if err != nil {
		return nil, err
	}
	c := e.Catalog()
	rules := &likesRules{e, c, make(map[string]catalog.Skill, len(c.Skills))}
	for _, skill := range c.Skills {
		rules.skills[skill.ID] = skill
	}
	return rules, nil
}

var likesModes = map[string]func() (*likesRules, error){
	"quick":    sync.OnceValues(func() (*likesRules, error) { return loadLikes("quick") }),
	"standard": sync.OnceValues(func() (*likesRules, error) { return loadLikes("standard") }),
}

type likesAction struct {
	Kind string      `json:"kind"`
	Plan engine.Plan `json:"plan"`
}

func (r *likesRules) setup(examples []json.RawMessage, preferred string) (engine.Selection, error) {
	// Every example includes the player's locks. Grow a legal example using
	// that role's published starting deck and keep its specified harness.
	var selection engine.Selection
	if preferred == "" {
		preferred = "ChatGPT"
	}
	for _, raw := range examples {
		var candidate engine.Selection
		if json.Unmarshal(raw, &candidate) != nil || r.engine.ValidateSelection(candidate) != nil {
			continue
		}
		if selection.Role == "" || candidate.Role == preferred {
			selection = candidate
		}
	}
	if selection.Role == "" {
		return selection, errDecision
	}
	for _, preset := range r.config.Loadouts {
		if preset.Role != selection.Role {
			continue
		}
		// A legal setup example may already occupy a slot with a basic attack.
		// Reserve space for a finisher and a repeatable middle-cost move first.
		roleSkill := map[string]string{"ChatGPT": "GPT22", "Claude": "CLA22", "Gemini": "GEM02", "GLM": "GLM23", "DeepSeek": "DS22"}[selection.Role]
		for _, id := range append([]string{"PUB61", roleSkill, "PUB21"}, preset.Skills...) {
			if slices.Contains(selection.Skills, id) {
				continue
			}
			next := selection
			next.Skills = append(slices.Clone(selection.Skills), id)
			if r.engine.ValidateSelection(next) == nil {
				selection = next
			}
		}
		break
	}
	return selection, nil
}

func (r *likesRules) choosePlan(seat int, view engine.View, choices []json.RawMessage) (likesAction, error) {
	player := view.Players[seat]
	bestScore := math.Inf(-1)
	var best likesAction
	for _, raw := range choices {
		var action likesAction
		if json.Unmarshal(raw, &action) != nil || action.Kind != "plan" {
			continue
		}
		score := -0.5
		if action.Plan.Main != nil {
			skill := r.skills[action.Plan.Main.SkillID]
			effect := skill.Effects.Base
			score = float64(effect.Likes) - float64(skill.Token)/330 - float64(skill.Energy)/130
			switch effect.Kind {
			case "SVG_DECAY":
				score -= float64(player.SkillDecay[skill.ID] * effect.P)
			case "AMPLIFY", "DEGRADE", "SUPPRESS":
				score += float64(effect.P)
			case "AUDIT":
				last := view.Records[1-seat]
				if last != nil && last.LastMain != nil && last.LastMain.Success && last.LastMain.Kind != "basic" {
					score += float64(effect.P)
				}
			case "LOW_POWER":
				if view.Energy <= effect.P {
					score += float64(effect.Q)
				}
			case "CACHE":
				score += 1.5
			}
			// Energy is shared. Spend it sooner when ahead; while behind,
			// prefer efficient moves that leave enough turns to catch up.
			if player.Likes < view.Players[1-seat].Likes && view.Energy < 220 {
				score -= float64(skill.Energy) / 15
			}
			tokens := player.API + min(player.Burst, player.Sub)
			switch skill.Payment {
			case "api":
				tokens = player.API
			case "sub":
				tokens = min(player.Burst, player.Sub)
			}
			price := r.config.Parameters["API_PRICE"]
			if tokens < skill.Token && skill.Payment != "sub" && player.Gold >= price+skill.Gold && !player.Overloaded {
				action.Plan.Purchases = append(action.Plan.Purchases, engine.Purchase{Item: "api"})
				tokens += player.APIPack
			}
			if tokens < skill.Token || player.Gold < skill.Gold || view.Energy < skill.Energy {
				score -= 100
			}
			for key, cost := range skill.ResourceCosts {
				if player.Resources[key] < cost {
					score -= 100
				}
			}
		}
		if score > bestScore {
			best, bestScore = action, score
		}
	}
	if best.Kind == "" {
		return best, errDecision
	}
	return best, nil
}

func likesDecision(mode, phase string, seat int, view json.RawMessage, examples []json.RawMessage, preferred string) (any, error) {
	get, ok := likesModes[mode]
	if !ok || seat < 0 || seat > 1 {
		return nil, errDecision
	}
	rules, err := get()
	if err != nil {
		return nil, err
	}
	if phase == "setup" {
		return rules.setup(examples, preferred)
	}
	if phase != "plan" {
		return nil, errDecision
	}
	var state engine.View
	if err := json.Unmarshal(view, &state); err != nil {
		return nil, err
	}
	return rules.choosePlan(seat, state, examples)
}
