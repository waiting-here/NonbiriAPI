package engine

import (
	"encoding/json"
	"testing"
)

func TestLocalContextTargetsSurviveEmptyDeckAndSerialization(t *testing.T) {
	s := fixture()
	ids := hand(&s, 0, "openai_infra_planner", "openai_infra_solver", "openai_infra_verifier")
	s.Choices[0] = &Choice{Kind: "context_window", Options: append([]int{}, ids...), Initial: append([]int{}, ids...), Remaining: 3, CanQuit: true}
	picked := map[int]bool{}
	for range 3 {
		body, _ := json.Marshal(s)
		_ = json.Unmarshal(body, &s)
		a, err := LocalChoice(DecisionState(s, 0), 0, zeroRandom{})
		if err != nil {
			t.Fatal(err)
		}
		if a.Kind != "choose" || picked[a.Card] {
			t.Fatal("redrew a returned card instead of the next original target", a)
		}
		picked[a.Card] = true
		s = apply(t, s, 0, a)
	}
	if len(picked) != 3 {
		t.Fatal(picked)
	}
}
