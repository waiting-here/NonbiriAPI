package strategy

import (
	"context"
	"math"
	"math/rand"
	"os"
	"strconv"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/game/ai"
	"github.com/waiting-here/NonbiriAPI/internal/game/bidding/engine"
)

// This opt-in evaluation uses held-out reward seeds and swaps the seats for
// every seed. Its baselines describe behavior, not human skill levels.
func TestPresetEvaluation(t *testing.T) {
	if os.Getenv("BIDDING_EVALUATE") != "1" {
		t.Skip("set BIDDING_EVALUATE=1 for paired strategy evaluation")
	}
	const seeds = 64
	base := int64(900000)
	if configured := os.Getenv("BIDDING_EVAL_SEED"); configured != "" {
		var err error
		base, err = strconv.ParseInt(configured, 10, 64)
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, preset := range Presets() {
		policy, err := Compile(preset.Policy)
		if err != nil {
			t.Fatal(err)
		}
		for _, baseline := range []string{"random", "minimum", "pool-quantile"} {
			memory := evaluationMemory(baseline)
			for _, remember := range []bool{false, true} {
				wins, draws, losses, total := 0, 0, 0, 0
				for seed := range seeds {
					for seat := range 2 {
						margin := evaluationMatch(t, policy, baseline, base+int64(seed), seat, func() *Summary {
							if remember {
								return memory
							}
							return nil
						}())
						total += margin
						if margin > 0 {
							wins++
						} else if margin == 0 {
							draws++
						} else {
							losses++
						}
					}
				}
				n := float64(seeds * 2)
				p := float64(wins) / n
				// Wilson bounds avoid zero-width intervals at the extremes.
				z := 1.96
				denom := 1 + z*z/n
				center := (p + z*z/(2*n)) / denom
				half := z * math.Sqrt(p*(1-p)/n+z*z/(4*n*n)) / denom
				t.Logf("preset=%s baseline=%s memory=%t n=%d W/D/L=%d/%d/%d mean_margin=%.2f win95=[%.3f,%.3f] params=%+v", preset.ID, baseline, remember, seeds*2, wins, draws, losses, float64(total)/n, center-half, center+half, preset.Policy.Parameters)
			}
		}
	}
}
func baselineAction(o Observation, kind string, random *rand.Rand) Action {
	if o.Phase == engine.Joker {
		use := false
		switch kind {
		case "random":
			use = random.Intn(4) == 0
		case "pool-quantile":
			use = o.View.PoolPoints >= 16 || o.Round >= 11
		}
		return Action{Kind: "joker", Use: &use}
	}
	cards := o.View.HandRemaining[o.Seat]
	index := 0
	switch kind {
	case "random":
		index = random.Intn(len(cards))
	case "pool-quantile":
		index = int(math.Round(math.Min(1, float64(o.View.PoolPoints)/26) * float64(len(cards)-1)))
	}
	card := cards[index]
	return Action{Kind: "bid", Card: &card}
}
func evaluationMemory(kind string) *Summary {
	samples := []Sample{}
	// Training seeds are disjoint from the paired evaluation seeds.
	for seed := range 30 {
		state, _ := engine.New(rand.New(rand.NewSource(int64(2000 + seed))))
		random := rand.New(rand.NewSource(int64(3000 + seed)))
		var features Features
		for state.Phase != engine.Terminal {
			if state.Phase == engine.Joker {
				seat := *engine.Dealer(state.Round)
				r, _ := Request(state, seat)
				a := baselineAction(r.Observation.(Observation), kind, random)
				state, _ = engine.DecideJoker(state, seat, *a.Use)
				continue
			}
			bids := [2]int{}
			for seat := range 2 {
				r, _ := Request(state, seat)
				o := r.Observation.(Observation)
				a := baselineAction(o, kind, random)
				bids[seat] = *a.Card
				if seat == 0 {
					features.AddHumanBid(o, *a.Card)
				}
			}
			state, _, _ = engine.ResolveBids(state, bids)
		}
		samples = append(samples, Sample{At: 10000, Features: features})
	}
	result := Summarize(samples, 10000)
	return &result
}

type evaluationRandom struct{ *rand.Rand }

func (r evaluationRandom) IntN(n int) int { return r.Intn(n) }
func evaluationMatch(t *testing.T, policy *Compiled, baseline string, seed int64, bot int, memory *Summary) int {
	t.Helper()
	state, err := engine.New(rand.New(rand.NewSource(seed)))
	if err != nil {
		t.Fatal(err)
	}
	botRandom := rand.New(rand.NewSource(seed*31 + int64(bot)))
	opponent := rand.New(rand.NewSource(seed*37 + int64(1-bot)))
	choose := func(seat int) Action {
		request, err := Request(state, seat)
		if err != nil {
			t.Fatal(err)
		}
		if seat != bot {
			return baselineAction(request.Observation.(Observation), baseline, opponent)
		}
		request.Policy = policy
		request.Personalization = memory
		request.Random = evaluationRandom{botRandom}
		result, err := (Source{}).Decide(context.Background(), request)
		if err != nil {
			t.Fatal(err)
		}
		value, err := ai.ResolveChoice(request, result)
		if err != nil {
			t.Fatal(err)
		}
		return value.(Action)
	}
	for state.Phase != engine.Terminal {
		if state.Phase == engine.Joker {
			seat := *engine.Dealer(state.Round)
			a := choose(seat)
			state, err = engine.DecideJoker(state, seat, *a.Use)
		} else {
			a, b := choose(0), choose(1)
			state, _, err = engine.ResolveBids(state, [2]int{*a.Card, *b.Card})
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	return state.Scores[bot] - state.Scores[1-bot]
}
