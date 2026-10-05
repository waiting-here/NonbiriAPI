package strategy

import (
	"context"
	"math"
	"strconv"

	"github.com/waiting-here/NonbiriAPI/internal/game/ai"
	"github.com/waiting-here/NonbiriAPI/internal/game/bidding/engine"
)

type Candidate struct {
	ID            string  `json:"id"`
	Score         float64 `json:"score"`
	Probability   float64 `json:"probability"`
	GuaranteedWin bool    `json:"guaranteed_win"`
}
type Analysis struct {
	Candidates   []Candidate `json:"candidates"`
	MatchedRule  int         `json:"matched_rule"`
	FilterEmpty  bool        `json:"filter_empty"`
	MemoryWeight float64     `json:"memory_weight"`
}

type Source struct{}

func (Source) ID() string                    { return SourceID }
func (Source) Supports(c ai.Capability) bool { return c == Capability() }
func (s Source) Decide(ctx context.Context, request ai.Request) (ai.Result, error) {
	if !s.Supports(request.Capability) {
		return ai.Result{}, ai.ErrUnsupported
	}
	o, ok := request.Observation.(Observation)
	if !ok {
		return ai.Result{}, ErrObservation
	}
	p, ok := request.Policy.(*Compiled)
	if !ok || p == nil {
		return ai.Result{}, ErrPolicy
	}
	var memory *Summary
	if request.Personalization != nil {
		memory, ok = request.Personalization.(*Summary)
		if !ok {
			return ai.Result{}, ErrObservation
		}
	}
	analysis, err := Analyze(ctx, o, p, memory)
	if err != nil {
		return ai.Result{}, err
	}
	selected := analysis.Candidates[0].ID
	random := 0.0
	if request.Random != nil {
		random = request.Random.Float64()
	}
	for _, candidate := range analysis.Candidates {
		if candidate.Probability <= 0 {
			continue
		}
		selected = candidate.ID
		random -= candidate.Probability
		if random < 0 {
			break
		}
	}
	result := ai.Selected(request, selected)
	if _, err = ai.ResolveChoice(request, result); err != nil {
		return ai.Result{}, err
	}
	return result, nil
}

func Analyze(ctx context.Context, o Observation, p *Compiled, memory *Summary) (Analysis, error) {
	if err := ctx.Err(); err != nil {
		return Analysis{}, err
	}
	if p == nil {
		return Analysis{}, ErrPolicy
	}
	if !o.valid() {
		return Analysis{}, ErrObservation
	}
	if o.Phase == engine.Joker {
		return analyzeJoker(ctx, o, p, memory)
	}
	return analyzeBids(ctx, o, p, memory)
}

func sign(v int) float64 {
	if v < 0 {
		return -1
	}
	if v > 0 {
		return 1
	}
	return 0
}

func analyzeBids(ctx context.Context, o Observation, policy *Compiled, memory *Summary) (Analysis, error) {
	p, filter, rule := policy.settings(o)
	result := Analysis{MatchedRule: rule}
	prob, alpha := opponentDistribution(o, memory, p)
	result.MemoryWeight = alpha
	ours, theirs := o.View.HandRemaining[o.Seat], o.View.HandRemaining[1-o.Seat]
	n := len(ours)
	unseen, _ := future(o)
	pool := float64(o.View.PoolPoints)
	difference := float64(o.View.Scores[o.Seat] - o.View.Scores[1-o.Seat])
	rows, cols := make([]float64, n), make([]float64, n)
	total := 0.0
	for i, a := range ours {
		for j, b := range theirs {
			value := sign(a - b)
			rows[i] += value
			cols[j] += value
			total += value
		}
	}
	outcomes := make([][]float64, n)
	eligible := make([]bool, n)
	anyWin := false
	for i, a := range ours {
		if err := ctx.Err(); err != nil {
			return Analysis{}, err
		}
		candidate := Candidate{ID: "bid-" + strconv.Itoa(a), GuaranteedWin: n <= 2}
		outcomes[i] = make([]float64, n)
		for j, b := range theirs {
			won := sign(a - b)
			delta := won * pool
			carry := 0.0
			if a == b && n > 1 {
				carry = pool
			}
			var value float64
			if n <= 2 {
				final := difference + delta
				if n == 2 {
					final += sign(ours[1-i]-theirs[1-j]) * (unseen + carry)
				}
				candidate.GuaranteedWin = candidate.GuaranteedWin && final > 0
				// The small margin term breaks equal outcomes without preferring
				// a larger expected score over a certain win.
				value = sign(int(final)) + .001*final/engine.MaxPoints
			} else {
				edge := (total - rows[i] - cols[j] + won) / float64((n-1)*(n-1))
				behind := clamp(-difference/max(26, pool+unseen), 0, 1)
				remaining := p.HandValue * (1 - p.Urgency*behind) * (unseen + carry) * edge
				value = math.Tanh((difference + delta + remaining) / max(8, .35*(unseen+carry)))
			}
			outcomes[i][j] = value
			candidate.Score += prob[j] * value
		}
		anyWin = anyWin || candidate.GuaranteedWin
		eligible[i] = true
		result.Candidates = append(result.Candidates, candidate)
	}
	if p.EndgameGuard && anyWin {
		for i, c := range result.Candidates {
			eligible[i] = c.GuaranteedWin
		}
	}
	filtered := make([]bool, n)
	kept := 0
	for i, a := range ours {
		match := true
		switch filter {
		case "lower_half":
			match = i < (n+1)/2
		case "upper_half":
			match = i >= n/2
		case "non_losing":
			match = a >= theirs[len(theirs)-1]
		}
		filtered[i] = eligible[i] && match
		if filtered[i] {
			kept++
		}
	}
	if kept > 0 {
		eligible = filtered
	} else {
		result.FilterEmpty = filter != ""
	}
	// Pruning compares every opponent response, not only the predicted one.
	// A custom filter cannot reintroduce a dominated action for exploration.
	for i := range ours {
		if !eligible[i] {
			continue
		}
		for k := range ours {
			if k == i || !eligible[k] {
				continue
			}
			dominates, strict := true, false
			for j := range theirs {
				dominates = dominates && outcomes[k][j] >= outcomes[i][j]-1e-12
				strict = strict || outcomes[k][j] > outcomes[i][j]+1e-12
			}
			if dominates && strict {
				eligible[i] = false
				break
			}
		}
	}
	best := -1
	for i, c := range result.Candidates {
		if eligible[i] && (best < 0 || c.Score > result.Candidates[best].Score+1e-12) {
			best = i
		}
	}
	near := 0
	for i, c := range result.Candidates {
		if eligible[i] && c.Score >= result.Candidates[best].Score-.06 {
			near++
		}
	}
	for i, c := range result.Candidates {
		if eligible[i] && c.Score >= result.Candidates[best].Score-.06 {
			result.Candidates[i].Probability = p.Exploration / float64(near)
		}
	}
	result.Candidates[best].Probability += 1 - p.Exploration
	return result, nil
}

func analyzeJoker(ctx context.Context, o Observation, p *Compiled, memory *Summary) (Analysis, error) {
	params, _, rule := p.settings(o)
	keep := o
	keep.Phase = engine.Bid
	used := keep
	current := 0
	for _, reward := range o.View.Rewards {
		if reward.Round == o.Round && reward.Side == o.Seat {
			current = reward.Rank
		}
	}
	used.View.PoolPoints += current
	before, err := analyzeBids(ctx, keep, p, memory)
	if err != nil {
		return Analysis{}, err
	}
	after, err := analyzeBids(ctx, used, p, memory)
	if err != nil {
		return Analysis{}, err
	}
	value := func(a Analysis) float64 {
		v := 0.0
		for _, c := range a.Candidates {
			v += c.Score * c.Probability
		}
		return v
	}
	opportunity := 0.0
	if o.Round+2 <= 12 {
		unseen, ownMean := future(o)
		edge := 0.0
		for _, a := range o.View.HandRemaining[o.Seat] {
			for _, b := range o.View.HandRemaining[1-o.Seat] {
				edge += sign(a - b)
			}
		}
		edge /= float64(len(o.View.HandRemaining[0]) * len(o.View.HandRemaining[1]))
		opportunity = min(.2, params.JokerCost*ownMean*max(0, .5+.5*edge)/max(8, .35*unseen))
	}
	keepValue, useValue := value(before), value(after)-opportunity
	result := Analysis{MatchedRule: rule, FilterEmpty: before.FilterEmpty || after.FilterEmpty, MemoryWeight: before.MemoryWeight, Candidates: []Candidate{{ID: "keep", Score: keepValue}, {ID: "use", Score: useValue}}}
	if useValue > keepValue+1e-9 {
		result.Candidates[1].Probability = 1
	} else {
		result.Candidates[0].Probability = 1
	}
	return result, nil
}
