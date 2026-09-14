package engine

import (
	"maps"
	"slices"
	"strings"
)

type PlayerView struct {
	Role         string           `json:"role"`
	Harness      *string          `json:"harness"`
	ActiveSlots  int              `json:"activeSlots"`
	Gold         int64            `json:"gold"`
	Likes        int64            `json:"likes"`
	BurstCap     int64            `json:"burstCap"`
	Burst        int64            `json:"burst"`
	Sub          int64            `json:"sub"`
	API          int64            `json:"api"`
	Images       int64            `json:"images"`
	APIPack      int64            `json:"apiPack"`
	Trial        *int64           `json:"trial,omitempty"`
	Resources    map[string]int64 `json:"resources"`
	ResourceCaps map[string]int64 `json:"resourceCaps"`
	Subscription Subscription     `json:"subscription"`
	NormalTurns  int64            `json:"normalTurns"`
	Stunned      bool             `json:"stunned"`
	Overloaded   bool             `json:"overloaded"`
	Effects      []Status         `json:"effects"`
	Revealed     []string         `json:"revealed"`
	Slots        []*string        `json:"slots"`
	Fog          bool             `json:"fog"`
	Loadout      []string         `json:"loadout,omitempty"`
	Used         map[string]int64 `json:"used"`
	SkillDecay   map[string]int64 `json:"skillDecay"`
	Distill      *Distill         `json:"distill"`
}
type View struct {
	Energy     int64          `json:"energy"`
	Players    [2]PlayerView  `json:"players"`
	Records    [2]*TurnRecord `json:"records"`
	LockedPlan *Plan          `json:"locked_plan"`
}

// Project requires an authenticated seat. The outer service derives terminal
// from its stored session and supplies only that viewer's locked plan.
func (e *Engine) Project(s State, viewer int, terminal bool, locked *Plan) (View, error) {
	if !validSeat(viewer) {
		return View{}, ErrPlan
	}
	if err := e.Validate(s); err != nil {
		return View{}, err
	}
	view := View{Energy: s.Energy, Records: clone(s.Records), LockedPlan: clone(locked)}
	for seat, p := range s.Players {
		own := seat == viewer || terminal
		visible := p.Revealed
		if own {
			visible = p.Loadout
		}
		v := PlayerView{Role: p.Role, Harness: clone(p.Harness), ActiveSlots: p.ActiveSlots, Gold: p.Gold, Likes: p.Likes, BurstCap: p.BurstCap, Burst: p.Burst, Sub: p.Sub, API: p.API, Images: p.Images, APIPack: p.APIPack, Trial: clone(p.Trial), Resources: maps.Clone(p.Resources), ResourceCaps: maps.Clone(p.ResourceCaps), Subscription: clone(p.Subscription), NormalTurns: p.NormalTurns, Stunned: p.Stunned, Effects: clone(p.Effects), Revealed: slices.Clone(p.Revealed), Slots: make([]*string, p.ActiveSlots), Fog: !own, Used: map[string]int64{}, SkillDecay: map[string]int64{}}
		v.Overloaded, _ = restriction(&s, seat)
		if own {
			v.Loadout = slices.Clone(p.Loadout)
		}
		for i, key := range visible {
			v.Slots[i] = ptr(key)
			v.Used[key] = p.Used[key]
		}
		distillVisible := own || slices.Contains(p.Revealed, "PUB41")
		for key, value := range p.SkillDecay {
			base := strings.TrimSuffix(key, ":distilled")
			if own || key == base && slices.Contains(p.Revealed, base) || key != base && distillVisible {
				v.SkillDecay[key] = value
			}
		}
		if distillVisible {
			v.Distill = ptr(clone(p.Distill))
		}
		view.Players[seat] = v
	}
	return view, nil
}
