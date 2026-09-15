package likes

import "github.com/waiting-here/NonbiriAPI/internal/game/likes/engine"

type effectCue struct {
	Key        string `json:"key"`
	Kind       string `json:"kind"`
	BuffID     string `json:"buff_id"`
	Layers     int64  `json:"layers"`
	Remaining  int64  `json:"remaining"`
	ActiveFrom int64  `json:"active_from"`
}
type resourceCue struct {
	Gold         int64            `json:"gold"`
	Likes        int64            `json:"likes"`
	Burst        int64            `json:"burst"`
	BurstCap     int64            `json:"burst_cap"`
	Sub          int64            `json:"sub"`
	SubCap       int64            `json:"sub_cap"`
	API          int64            `json:"api"`
	Trial        int64            `json:"trial"`
	Resources    map[string]int64 `json:"resources"`
	ResourceCaps map[string]int64 `json:"resource_caps"`
	Effects      []effectCue      `json:"effects"`
}
type frameCue struct {
	Stage   string         `json:"stage"`
	Players [2]resourceCue `json:"players"`
	Energy  int64          `json:"energy"`
}
type presentation struct {
	Plans  [2]engine.Plan `json:"plans"`
	Before frameCue       `json:"before"`
	After  frameCue       `json:"after"`
	Frames []frameCue     `json:"frames"`
	Events []engine.Event `json:"events"`
}

func compactFrame(f engine.Frame) frameCue {
	c := frameCue{Stage: f.Stage, Energy: f.Energy}
	for seat, p := range f.Players {
		v := resourceCue{Gold: p.Gold, Likes: p.Likes, Burst: p.Burst, BurstCap: p.BurstCap, Sub: p.Sub, SubCap: p.SubCap, API: p.API, Trial: p.Trial, Resources: p.Resources, ResourceCaps: p.ResourceCaps, Effects: []effectCue{}}
		for _, effect := range p.Effects {
			v.Effects = append(v.Effects, effectCue{Key: effect.Key, Kind: effect.Kind, BuffID: effect.BuffID, Layers: effect.Layers, Remaining: effect.Remaining, ActiveFrom: effect.ActiveFrom})
		}
		c.Players[seat] = v
	}
	return c
}
func present(r engine.RoundRecord) presentation {
	p := presentation{Plans: r.Plans, Before: compactFrame(r.Before), After: compactFrame(r.After), Frames: []frameCue{}, Events: []engine.Event{}}
	for _, f := range r.Frames {
		p.Frames = append(p.Frames, compactFrame(f))
	}
	for _, event := range r.Events {
		// Per-status repeats remain available in the full round log. The paired
		// frames still show every status; these cues drive casts, fees and scores.
		switch event.Kind {
		case "cast", "skill-cancelled", "overload", "shop", "charge", "end", "cleanse", "counter", "resource-gain", "trial", "resource", "usage-reset", "learn", "power", "conversion", "combo-skip":
			p.Events = append(p.Events, event)
		}
	}
	return p
}
