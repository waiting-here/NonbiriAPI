package likes

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/game/likes/engine"
)

func TestPresentationPairsOpponentsButNeverCompressesConsecutiveCasts(t *testing.T) {
	zero, one := 0, 1
	p := presentation{Events: []engine.Event{
		{ID: 1, Stage: "score", Kind: "cast", Seat: &zero},
		{ID: 2, Stage: "score", Kind: "cast", Seat: &one},
		{ID: 3, Stage: "score", Kind: "cast", Seat: &zero},
	}}
	steps := presentationTimeline(p)
	if len(steps) != 4 || !reflect.DeepEqual(steps[1].EventIDs, []int64{1, 2}) || !reflect.DeepEqual(steps[2].EventIDs, []int64{3}) {
		t.Fatal(steps)
	}
	for _, step := range steps[1:3] {
		if step.DurationMS != 2400 {
			t.Fatal("cast compressed", step)
		}
	}
	for id := int64(4); id <= 40; id++ {
		p.Events = append(p.Events, engine.Event{ID: id, Stage: "aftereffects", Kind: "cast", Seat: &zero})
	}
	p.Timeline = presentationTimeline(p)
	body, _ := json.Marshal(p)
	duration, err := (&Rules{}).PresentationDuration(body)
	if err != nil || duration < 90 {
		t.Fatal("presentation capped", duration, err)
	}
	for _, step := range p.Timeline {
		if len(step.EventIDs) > 0 && step.DurationMS < 2400 {
			t.Fatal("follow-up compressed", step)
		}
	}
	p.Timeline[1].DurationMS = 500
	body, _ = json.Marshal(p)
	if _, err := (&Rules{}).PresentationDuration(body); err == nil {
		t.Fatal("inconsistent timeline accepted")
	}
}

func TestPresentationTimelinesCoverAuthorityEventsAndLegacySummaries(t *testing.T) {
	data, err := os.ReadFile("../../../web/src/user/games/likes/testdata/authority.json")
	if err != nil {
		t.Fatal(err)
	}
	type fixture struct {
		Summary presentation `json:"summary"`
	}
	var source struct {
		Rounds    []fixture          `json:"rounds"`
		Scenarios map[string]fixture `json:"scenarios"`
	}
	if err := json.Unmarshal(data, &source); err != nil {
		t.Fatal(err)
	}
	type output struct {
		Timeline []presentationStep `json:"timeline"`
		Seconds  int64              `json:"seconds"`
	}
	build := func(p presentation) output {
		legacy, _ := json.Marshal(p)
		if seconds, err := (&Rules{}).PresentationDuration(legacy); err != nil || seconds != 5 {
			t.Fatal("legacy presentation rejected", err)
		}
		p.Timeline = presentationTimeline(p)
		seen := map[int64]bool{}
		for _, step := range p.Timeline {
			for _, id := range step.EventIDs {
				if seen[id] {
					t.Fatal("duplicate event", id)
				}
				seen[id] = true
			}
		}
		if len(seen) != len(p.Events) {
			t.Fatal("omitted events")
		}
		encoded, _ := json.Marshal(p)
		seconds, err := (&Rules{}).PresentationDuration(encoded)
		if err != nil || seconds < 3 {
			t.Fatal(seconds, err)
		}
		return output{p.Timeline, seconds}
	}
	type timelines struct {
		Rounds    []output          `json:"rounds"`
		Scenarios map[string]output `json:"scenarios"`
	}
	var result timelines
	result.Scenarios = map[string]output{}
	for _, round := range source.Rounds {
		result.Rounds = append(result.Rounds, build(round.Summary))
	}
	for name, scenario := range source.Scenarios {
		result.Scenarios[name] = build(scenario.Summary)
	}
	// This fixture lets browser and API tests consume the exact Go schedule.
	if path := os.Getenv("GAME_PRESENTATION_FIXTURES_OUT"); path != "" {
		encoded, _ := json.MarshalIndent(result, "", "  ")
		if err := os.WriteFile(path, append(encoded, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
	} else {
		data, err := os.ReadFile("../../../web/src/user/games/likes/testdata/timelines.json")
		var checked timelines
		if err != nil || json.Unmarshal(data, &checked) != nil || !reflect.DeepEqual(result, checked) {
			t.Fatal("browser presentation fixtures differ from the authoritative schedule", err)
		}
	}
}
