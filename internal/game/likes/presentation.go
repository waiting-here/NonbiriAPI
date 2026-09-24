package likes

import (
	"encoding/json"
	"reflect"

	"github.com/waiting-here/NonbiriAPI/internal/game/duel"
	"github.com/waiting-here/NonbiriAPI/internal/game/likes/engine"
)

type presentationStep struct {
	Stage      string  `json:"stage"`
	DurationMS int64   `json:"duration_ms"`
	EventIDs   []int64 `json:"event_ids"`
}

type effectCue struct {
	Key              string `json:"key"`
	Kind             string `json:"kind"`
	BuffID           string `json:"buff_id"`
	Layers           int64  `json:"layers"`
	Remaining        int64  `json:"remaining"`
	ActiveFrom       int64  `json:"active_from"`
	PersistentLayers *int64 `json:"persistent_layers,omitempty"`
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
	Plans    [2]engine.Plan     `json:"plans"`
	Before   frameCue           `json:"before"`
	After    frameCue           `json:"after"`
	Frames   []frameCue         `json:"frames"`
	Events   []engine.Event     `json:"events"`
	Timeline []presentationStep `json:"timeline,omitempty"`
}

func compactFrame(f engine.Frame) frameCue {
	c := frameCue{Stage: f.Stage, Energy: f.Energy}
	for seat, p := range f.Players {
		v := resourceCue{Gold: p.Gold, Likes: p.Likes, Burst: p.Burst, BurstCap: p.BurstCap, Sub: p.Sub, SubCap: p.SubCap, API: p.API, Trial: p.Trial, Resources: p.Resources, ResourceCaps: p.ResourceCaps, Effects: []effectCue{}}
		for _, effect := range p.Effects {
			v.Effects = append(v.Effects, effectCue{Key: effect.Key, Kind: effect.Kind, BuffID: effect.BuffID, Layers: effect.Layers, Remaining: effect.Remaining, ActiveFrom: effect.ActiveFrom, PersistentLayers: effect.PersistentLayers})
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
	p.Timeline = presentationTimeline(p)
	return p
}

// Each side keeps its event order. Opposing events share a beat; shared facts
// form their own beats. More actions extend the presentation instead of
// shortening earlier actions to fit a fixed window.
func presentationTimeline(p presentation) []presentationStep {
	stages := []string{"reveal", "shopping", "payment", "cleansing", "score", "aftereffects", "round-end"}
	steps := []presentationStep{}
	previous := p.Before
	for _, stage := range stages {
		frame := previous
		for _, candidate := range p.Frames {
			if candidate.Stage == stage {
				frame = candidate
				break
			}
		}
		base := int64(1500)
		if stage == "reveal" {
			base = 2000
		}
		if stage == "round-end" {
			base = 1200
		}
		appendStep := func(events []engine.Event) {
			step := presentationStep{Stage: stage, DurationMS: base, EventIDs: []int64{}}
			for _, event := range events {
				step.EventIDs = append(step.EventIDs, event.ID)
				duration := base
				if event.Kind == "cast" {
					duration = 2400
					if event.Score != nil {
						duration += int64(len(event.Score.Parts)) * 300
					}
				}
				if event.Kind == "overload" {
					duration = 2000
				}
				if duration > step.DurationMS {
					step.DurationMS = duration
				}
			}
			steps = append(steps, step)
		}
		var paired [2][]engine.Event
		flush := func() {
			for i := 0; i < max(len(paired[0]), len(paired[1])); i++ {
				var events []engine.Event
				for seat := range 2 {
					if i < len(paired[seat]) {
						events = append(events, paired[seat][i])
					}
				}
				appendStep(events)
			}
			paired = [2][]engine.Event{}
		}
		initial := len(steps)
		for _, event := range p.Events {
			if event.Stage != stage {
				continue
			}
			if event.Seat == nil {
				flush()
				appendStep([]engine.Event{event})
			} else if *event.Seat >= 0 && *event.Seat < 2 {
				paired[*event.Seat] = append(paired[*event.Seat], event)
			}
		}
		flush()
		if len(steps) == initial && (stage == "reveal" || stage == "round-end" || frame.Energy != previous.Energy || !reflect.DeepEqual(frame.Players, previous.Players)) {
			appendStep(nil)
		}
		previous = frame
	}
	var duration int64
	for _, step := range steps {
		duration += step.DurationMS
	}
	// Public timestamps use whole seconds. Keep any rounding in the final hold.
	steps[len(steps)-1].DurationMS += (1000 - duration%1000) % 1000
	return steps
}

// Existing stored summaries retain their original five-second presentation.
// New summaries carry the exact server-generated schedule; there is no total
// duration cap. The existing event and response-size budgets remain in force.
func (*Rules) PresentationDuration(raw json.RawMessage) (int64, error) {
	var p presentation
	if duel.Decode(raw, &p) != nil || len(p.Events) > 512 || len(p.Frames) > 7 {
		return 0, duel.ErrInvariant
	}
	if p.Timeline == nil {
		return 5, nil
	}
	if !reflect.DeepEqual(p.Timeline, presentationTimeline(p)) {
		return 0, duel.ErrInvariant
	}
	var duration int64
	for _, step := range p.Timeline {
		duration += step.DurationMS
	}
	return duration / 1000, nil
}
