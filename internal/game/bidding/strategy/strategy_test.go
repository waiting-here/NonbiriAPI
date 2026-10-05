package strategy

import (
	"context"
	"encoding/json"
	"math"
	"math/rand"
	"reflect"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/game/ai"
	"github.com/waiting-here/NonbiriAPI/internal/game/bidding/engine"
)

func TestPresetsAndPreviewUseSameSource(t *testing.T) {
	for _, preset := range Presets() {
		raw, _ := json.Marshal(preset.Policy)
		policy, err := Decode(raw)
		if err != nil {
			t.Fatal(err)
		}
		for _, scenario := range Scenarios() {
			analysis, err := Analyze(context.Background(), scenario.Observation, policy, nil)
			if err != nil {
				t.Fatal(err)
			}
			total := 0.0
			choices := ai.Choices{}
			for _, c := range analysis.Candidates {
				total += c.Probability
				choices = append(choices, ai.Choice{ID: c.ID, Action: c.ID})
			}
			if math.Abs(total-1) > 1e-10 {
				t.Fatal(total)
			}
			request := ai.Request{Capability: Capability(), Observation: scenario.Observation, Actions: choices, Policy: policy}
			result, err := (Source{}).Decide(context.Background(), request)
			if err != nil {
				t.Fatal(err)
			}
			selected, err := ai.ResolveChoice(request, result)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, c := range analysis.Candidates {
				if c.ID == selected && c.Probability > 0 {
					found = true
				}
			}
			if !found {
				t.Fatalf("unselectable %v", selected)
			}
		}
	}
}

func TestExactEndgameGuardPrecedesFiltersAndExploration(t *testing.T) {
	// 162 revealed base points, including the current 10; the final pair is 20.
	o := Observation{Round: 12, Phase: engine.Bid, Seat: 0, View: engine.View{HandRemaining: [2][]int{{4, 13}, {3, 12}}, PoolPoints: 10, Scores: [2]int{77, 75}}}
	for side := range 2 {
		for rank := 1; rank <= 13; rank++ {
			if side == 0 && rank == 7 || side == 1 && rank == 13 {
				continue
			}
			o.View.Rewards = append(o.View.Rewards, engine.Reward{Side: side, Rank: rank})
		}
	}
	p := Defaults()
	p.Parameters.HandValue = 0
	p.Parameters.Exploration = .15
	p.Rules = []Rule{{Match: "all", Conditions: []Condition{{"round", "gte", 12}}, Filter: "upper_half"}}
	compiled, _ := Compile(p)
	a, err := Analyze(context.Background(), o, compiled, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !a.FilterEmpty || !a.Candidates[0].GuaranteedWin || a.Candidates[0].Probability != 1 || a.Candidates[1].GuaranteedWin || a.Candidates[1].Probability != 0 {
		t.Fatalf("guard: %+v", a)
	}
	p.Parameters.EndgameGuard = false
	compiled, _ = Compile(p)
	a, err = Analyze(context.Background(), o, compiled, nil)
	if err != nil || a.Candidates[1].Probability != 1 {
		t.Fatalf("explicitly disabled: %+v %v", a, err)
	}
}

func TestHiddenRewardsDoNotChangeDecisionInput(t *testing.T) {
	s, err := engine.New(rand.New(rand.NewSource(81)))
	if err != nil {
		t.Fatal(err)
	}
	a, err := Request(s, 0)
	if err != nil {
		t.Fatal(err)
	}
	s.Decks[0][5], s.Decks[0][8] = s.Decks[0][8], s.Decks[0][5]
	s.Decks[1][6], s.Decks[1][9] = s.Decks[1][9], s.Decks[1][6]
	b, err := Request(s, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a, b) {
		t.Fatal("hidden reward order reached request")
	}
	compiled, _ := Compile(Defaults())
	a.Policy = compiled
	b.Policy = compiled
	ra, ea := (Source{}).Decide(context.Background(), a)
	rb, eb := (Source{}).Decide(context.Background(), b)
	if ea != nil || eb != nil || !reflect.DeepEqual(ra, rb) {
		t.Fatal(ra, rb, ea, eb)
	}
}

func TestMemoryUsesOpponentPerspectiveAndLocalEvidence(t *testing.T) {
	o := Scenarios()[1].Observation
	o.View.Scores = [2]int{2, 7}
	var f Features
	if !f.AddHumanBid(o, 13) {
		t.Fatal("missing sample")
	}
	if !f.Valid() {
		t.Fatal("invalid sample")
	}
	const now = 2000000
	m := Summarize([]Sample{{now, f}}, now)
	predict := o
	predict.Seat = 1
	c, n := weights(predict, &m)
	if n != 1 || c[4] != 1 {
		t.Fatal(c, n)
	}
	_, alpha := opponentDistribution(predict, &m, Defaults().Parameters)
	if math.Abs(alpha-1.0/13) > 1e-12 {
		t.Fatal(alpha)
	}
	// Hundreds of samples in an unrelated pool must not inflate confidence.
	m.Counts[contextIndex(1, 26, 0)][0] = 1000
	_, after := opponentDistribution(predict, &m, Defaults().Parameters)
	if alpha != after {
		t.Fatal(alpha, after)
	}
	decayed := Summarize([]Sample{{now - 14*86400, f}}, now)
	_, n = weights(predict, &decayed)
	if n != .5 {
		t.Fatal(n)
	}
}

func TestPolicyBoundsAndCompilationSnapshot(t *testing.T) {
	p := Defaults()
	value := 1.0
	p.Rules = []Rule{{Match: "all", Conditions: []Condition{{"round", "gte", 1}}, Override: Overrides{HandValue: &value}}}
	compiled, err := Compile(p)
	if err != nil {
		t.Fatal(err)
	}
	value = 999
	p.Rules[0].Conditions[0].Field = "private_deck"
	params, _, index := compiled.settings(Scenarios()[1].Observation)
	if params.HandValue != 1 || index != 0 {
		t.Fatal(params, index)
	}
	if _, err = Compile(p); err == nil {
		t.Fatal("accepted private field")
	}
	if _, err = Decode([]byte(`{"schema":"bidding-local/v1","unknown":1}`)); err == nil {
		t.Fatal("accepted unknown")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = Analyze(ctx, Scenarios()[0].Observation, compiled, nil); err != context.Canceled {
		t.Fatal(err)
	}
}

func TestPublicBidsInformDecisionsWithoutCrossMatchMemory(t *testing.T) {
	o := Scenarios()[1].Observation
	o.View.HandRemaining[1-o.Seat] = []int{5, 6, 7, 8, 9, 10, 11, 12, 13}
	without, alpha := opponentDistribution(o, nil, Defaults().Parameters)
	o.View.Played[1-o.Seat] = []int{1, 2, 3, 4}
	with, after := opponentDistribution(o, nil, Defaults().Parameters)
	if alpha != 0 || after != 0 || with[0] <= without[0] {
		t.Fatal("visible low bids were ignored, or fabricated cross-match memory", without, with)
	}
	total := 0.0
	for _, weight := range with {
		total += weight
	}
	if math.Abs(total-1) > 1e-10 {
		t.Fatal("unnormalized distribution", total)
	}
}
