package likes

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/game/likes/engine"
)

// Browser feedback consumes these exact engine records, including each
// probability draw. Legacy records remain on their original rules.
func TestPassiveFeedbackFixtures(t *testing.T) {
	type scenario struct {
		Mode    string             `json:"mode"`
		Hash    string             `json:"content_hash"`
		Before  engine.View        `json:"before"`
		After   engine.View        `json:"after"`
		Facts   engine.RoundRecord `json:"facts"`
		Summary presentation       `json:"summary"`
		Seconds int64              `json:"seconds"`
	}
	result := map[string]scenario{}
	harness := "H02"
	for _, name := range []string{"partial", "chain", "guaranteed", "legacy"} {
		mode := "quick"
		if name == "chain" {
			mode = "standard"
		}
		e, err := engine.New(mode)
		if name == "legacy" {
			e, err = engine.NewLegacy(mode)
		}
		if err != nil {
			t.Fatal(err)
		}
		selections := [2]engine.Selection{
			{Role: "Claude", Harness: &harness, Skills: []string{"CLA01", "CLA23"}},
			{Role: "GLM", Skills: []string{"GLM01"}},
		}
		plans := [2]engine.Plan{engine.EmptyPlan(), engine.EmptyPlan()}
		plans[0].Main = &engine.Choice{SkillID: "CLA23"}
		if name == "chain" {
			for seat := range 2 {
				selections[seat] = engine.Selection{Role: "Gemini", Skills: []string{"PUB01", "GEM61"}}
				plans[seat].Main = &engine.Choice{SkillID: "GEM61"}
				plans[seat].Extra = []engine.Choice{{SkillID: "GEM61"}}
			}
		}
		if name == "guaranteed" {
			selections[0] = engine.Selection{Role: "DeepSeek", Harness: &harness, Skills: []string{"PUB01", "PUB62"}}
			plans[0].Main = &engine.Choice{SkillID: "PUB62"}
		}
		s, err := e.Create(selections)
		if err != nil {
			t.Fatal(err)
		}
		if name == "chain" || name == "guaranteed" {
			if name == "chain" {
				s.Energy = 400
			}
			for seat := range 2 {
				s.Players[seat].API = 100_000
			}
		}
		before, err := e.Project(s, 0, false, nil)
		if err != nil {
			t.Fatal(err)
		}
		tape, index := []int{99, 100, 124}, 0
		next, record, err := e.Resolve(s, plans, func(n int) (int, error) {
			if name != "partial" || n != 125 || index >= len(tape) {
				t.Fatalf("unexpected draw in %s: %d", name, n)
			}
			value := tape[index]
			index++
			return value, nil
		})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if name == "partial" && index != 3 {
			t.Fatal("partial application did not exercise SOTA")
		}
		if name == "chain" {
			count := 0
			for _, event := range record.Events {
				if event.Kind == "cast" && event.Data["derived"] == true {
					count++
				}
			}
			if count < 4 {
				t.Fatal("missing consecutive Flash casts", count)
			}
		}
		after, err := e.Project(next, 0, next.Result != nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		summary := present(record)
		for _, event := range summary.Events {
			if event.Kind == "effect-attempt" {
				t.Fatal("individual attempts expanded presentation")
			}
		}
		body, _ := json.Marshal(summary)
		seconds, err := (&Rules{}).PresentationDuration(body)
		if err != nil {
			t.Fatal(err)
		}
		result[name] = scenario{mode, e.ContentHash(), before, after, record, summary, seconds}
	}
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, '\n')
	if len(data) > 150_000 {
		t.Fatal("feedback fixtures exceed response budget")
	}
	if path := os.Getenv("GAME_PASSIVE_FIXTURES_OUT"); path != "" {
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
	} else {
		checked, err := os.ReadFile("../../../web/src/user/games/likes/testdata/passives.json")
		if err != nil || !bytes.Equal(data, checked) {
			t.Fatal("feedback differs from authoritative replay", err)
		}
	}
}
