package blackjack_test

import (
	"math"
	"math/rand"
	"os"
	"slices"
	"strconv"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/game/blackjack"
	"github.com/waiting-here/NonbiriAPI/internal/game/blackjack/config"
	"github.com/waiting-here/NonbiriAPI/internal/game/blackjack/engine"
)

// The policy oracle is test-only. It maximizes expected return with replacement,
// conditional on a negative American peek, with S17, DAS, one split, one card
// to split aces and no natural premium after splitting. Simulation below uses
// the real six-deck engine, without replacement, and the real integer fees.
// Thus the analytic value is a benchmark, not a claim of exact finite-shoe EV.
type returnPolicy struct {
	p      [11]float64
	up     int
	factor float64
	dealer [23]float64
	values [22][2]float64
	known  [22][2]bool
}

func handValue(low int, ace bool) int {
	if ace && low+10 <= 21 {
		return low + 10
	}
	return low
}
func boolIndex(v bool) int {
	if v {
		return 1
	}
	return 0
}

func makePolicy(up int, trueCount, factor float64) *returnPolicy {
	p := &returnPolicy{up: up, factor: factor}
	// A balanced high/low population tilt: one excess high card per deck
	// changes the low/high groups by half a card each. Neutral ranks stay fixed.
	for rank := 1; rank <= 10; rank++ {
		p.p[rank] = 1.0 / 13
		if rank >= 2 && rank <= 6 {
			p.p[rank] -= trueCount / 520
		}
		if rank == 1 || rank == 10 {
			p.p[rank] += trueCount / 520
		}
	}
	p.p[10] *= 4
	var memo [22][2][23]float64
	var known [22][2]bool
	var draw func(int, bool) [23]float64
	draw = func(low int, ace bool) [23]float64 {
		var out [23]float64
		value := handValue(low, ace)
		if value > 21 {
			out[22] = 1
			return out
		}
		if value >= 17 {
			out[value] = 1
			return out
		}
		a := boolIndex(ace)
		if known[low][a] {
			return memo[low][a]
		}
		for rank := 1; rank <= 10; rank++ {
			child := draw(low+rank, ace || rank == 1)
			for result, probability := range child {
				out[result] += p.p[rank] * probability
			}
		}
		known[low][a], memo[low][a] = true, out
		return out
	}
	denominator := 1.0
	if up == 1 {
		denominator -= p.p[10]
	}
	if up == 10 {
		denominator -= p.p[1]
	}
	for hole := 1; hole <= 10; hole++ {
		if up == 1 && hole == 10 || up == 10 && hole == 1 {
			continue
		}
		child := draw(up+hole, up == 1 || hole == 1)
		for result, probability := range child {
			p.dealer[result] += p.p[hole] / denominator * probability
		}
	}
	return p
}
func (p *returnPolicy) stand(low int, ace bool) float64 {
	value := handValue(low, ace)
	if value > 21 {
		return 0
	}
	result := 2 * p.dealer[22]
	for dealer := 17; dealer <= 21; dealer++ {
		if value > dealer {
			result += 2 * p.dealer[dealer]
		}
		if value == dealer {
			result += p.dealer[dealer]
		}
	}
	return p.factor * result
}
func (p *returnPolicy) hit(low int, ace bool) float64 {
	result := 0.0
	for rank := 1; rank <= 10; rank++ {
		result += p.p[rank] * p.continueHand(low+rank, ace || rank == 1)
	}
	return result
}
func (p *returnPolicy) continueHand(low int, ace bool) float64 {
	if handValue(low, ace) >= 21 {
		return p.stand(low, ace)
	}
	a := boolIndex(ace)
	if !p.known[low][a] {
		p.values[low][a] = math.Max(p.stand(low, ace), p.hit(low, ace))
		p.known[low][a] = true
	}
	return p.values[low][a]
}
func (p *returnPolicy) choose(low int, ace, double bool, pair int) (string, float64) {
	if handValue(low, ace) >= 21 {
		return "stand", p.stand(low, ace)
	}
	best, action := p.stand(low, ace), "stand"
	consider := func(kind string, value float64) {
		if value > best+1e-12 {
			best, action = value, kind
		}
	}
	consider("hit", p.hit(low, ace))
	if double {
		value := -1.0
		for rank := 1; rank <= 10; rank++ {
			value += 2 * p.p[rank] * p.stand(low+rank, ace || rank == 1)
		}
		consider("double", value)
	}
	if pair > 0 {
		value := -1.0
		for rank := 1; rank <= 10; rank++ {
			child := p.stand(pair+rank, pair == 1 || rank == 1)
			if pair != 1 {
				_, child = p.choose(pair+rank, rank == 1, true, 0)
			}
			value += 2 * p.p[rank] * child
		}
		consider("split", value)
	}
	return action, best
}
func (p *returnPolicy) action(hand engine.HandView, legal []string) string {
	low, ace := 0, false
	for _, c := range hand.Cards {
		low += min(c.Rank, 10)
		ace = ace || c.Rank == 1
	}
	pair := 0
	if slices.Contains(legal, "split") {
		pair = min(hand.Cards[0].Rank, 10)
	}
	a, _ := p.choose(low, ace, slices.Contains(legal, "double"), pair)
	return a
}
func analyticReturn(factor float64) float64 {
	result := 0.0
	for up := 1; up <= 10; up++ {
		p := makePolicy(up, 0, factor)
		natural := 0.0
		if up == 1 {
			natural = p.p[10]
		}
		if up == 10 {
			natural = p.p[1]
		}
		for a := 1; a <= 10; a++ {
			for b := 1; b <= 10; b++ {
				pair := 0
				if a == b {
					pair = a
				}
				_, value := p.choose(a+b, a == 1 || b == 1, true, pair)
				value *= 1 - natural
				if a+b == 11 && (a == 1 || b == 1) {
					value = factor * (natural + 2.5*(1-natural))
				}
				result += p.p[up] * p.p[a] * p.p[b] * value
			}
		}
	}
	return result - 1
}

// Public observations only: the dealer hole, undealt shoe and pending choices
// never enter the count. Each table reshuffles, so no count carries to the next.
func observedCount(v engine.View) int {
	count, seen := 0, 0
	add := func(c engine.CardView) {
		seen++
		if c.Rank >= 2 && c.Rank <= 6 {
			count++
		}
		if c.Rank == 1 || c.Rank >= 10 {
			count--
		}
	}
	for _, c := range v.Dealer {
		add(c)
	}
	for _, s := range v.Seats {
		for _, h := range s.Hands {
			for _, c := range h.Cards {
				add(c)
			}
		}
	}
	return min(20, max(-20, int(math.Round(float64(count)*104/float64(engine.DeckSize-seen)))))
}

// Ratio-of-means standard error with the table as an independent cluster.
// Splits, doubles and common dealer outcomes must not be treated as independent
// observations or as free additional stakes.
type returnSample struct {
	n                       int64
	x, y, xx, yy, xy, gross float64
}

func (s *returnSample) add(profit, stake, gross float64) {
	s.n++
	s.x += profit
	s.y += stake
	s.xx += profit * profit
	s.yy += stake * stake
	s.xy += profit * stake
	s.gross += gross
}
func (s returnSample) estimate(z float64) (float64, float64, float64) {
	n := float64(s.n)
	ratio := s.x / s.y
	variance := math.Max(0, (s.xx-2*ratio*s.xy+ratio*ratio*s.yy)/(n-1))
	se := math.Sqrt(variance/n) / (s.y / n)
	return ratio, se, ratio + z*se
}

func TestBlackjackStrategyOracle(t *testing.T) {
	for _, tc := range []float64{-10, 0, 10} {
		for up := 1; up <= 10; up++ {
			p := makePolicy(up, tc, 0.97)
			mass, dealer := 0.0, 0.0
			for _, v := range p.p {
				mass += v
			}
			for _, v := range p.dealer {
				dealer += v
			}
			if math.Abs(mass-1) > 1e-12 || math.Abs(dealer-1) > 1e-12 {
				t.Fatal("probability mass", tc, up, mass, dealer)
			}
		}
	}
	for _, c := range []struct {
		up, low, pair int
		ace           bool
		want          string
	}{
		{6, 16, 8, false, "split"}, {10, 11, 0, false, "double"}, {6, 20, 10, false, "stand"},
		{10, 2, 1, true, "split"}, {9, 18, 0, false, "stand"}, {10, 16, 0, false, "hit"},
	} {
		got, _ := makePolicy(c.up, 0, 1).choose(c.low, c.ace, true, c.pair)
		if got != c.want {
			t.Fatalf("%+v: %s", c, got)
		}
	}
	raw, taxed := analyticReturn(1), analyticReturn(.97)
	t.Logf("replacement-model optimal EV per base stake: pre-fee %.8f, post-fee %.8f", raw, taxed)
	if raw >= 0 || raw < -.02 || taxed >= -.03 || taxed < -.06 {
		t.Fatal("unexpected rule expectation", raw, taxed)
	}
}

func TestBlackjackFixedSeedReturn(t *testing.T) {
	rounds := 8000
	if value := os.Getenv("BLACKJACK_SIMULATION_ROUNDS"); value != "" {
		n, err := strconv.Atoi(value)
		if err != nil || n < 1_000_000 || n > 10_000_000 || n%32 != 0 {
			t.Fatal("simulation rounds must be 1..10 million and divisible by 32")
		}
		rounds = n
	}
	const seed int64 = 0x21_06_17_08
	const base int64 = 5_000_000
	var policies [41][11]*returnPolicy
	for index := range policies {
		for up := 1; up <= 10; up++ {
			policies[index][up] = makePolicy(up, float64(index-20)/2, .97)
		}
	}
	for _, seats := range []int{1, 8} {
		for _, counting := range []bool{false, true} {
			rng := rand.New(rand.NewSource(seed + int64(seats)*2 + int64(boolIndex(counting))))
			var whole returnSample
			var bySeat [8]returnSample
			maxCards, splits, doubles, naturals := 0, 0, 0, 0
			for round := range rounds / 4 {
				var deck [engine.DeckSize]engine.Card
				for i := range deck {
					deck[i] = engine.Card(i)
				}
				rng.Shuffle(len(deck), func(i, j int) { deck[i], deck[j] = deck[j], deck[i] })
				numbers := []int{round % 8}
				if seats == 8 {
					numbers = []int{0, 1, 2, 3, 4, 5, 6, 7}
				}
				state, err := engine.Deal(deck, numbers, round%8)
				if err != nil {
					t.Fatal(err)
				}
				for batch := 0; !state.Finished; batch++ {
					if batch >= 30 {
						t.Fatal("strategy did not finish within decision window")
					}
					view, err := engine.Project(state)
					if err != nil {
						t.Fatal(err)
					}
					index := 20
					if counting {
						index += observedCount(view)
					}
					policy := policies[index][min(view.Dealer[0].Rank, 10)]
					actions := make([]engine.Action, 0, seats)
					for _, seat := range view.Seats {
						for i, hand := range seat.Hands {
							legal := state.Legal(seat.Number, i)
							if len(legal) == 0 {
								continue
							}
							kind := policy.action(hand, legal)
							if kind == "split" {
								splits++
							}
							if kind == "double" {
								doubles++
							}
							actions = append(actions, engine.Action{Seat: seat.Number, Hand: i, Revision: hand.Revision, Kind: kind})
							break
						}
					}
					state, err = engine.ApplyBatch(state, actions)
					if err != nil {
						t.Fatal(err)
					}
				}
				maxCards = max(maxCards, state.Cursor)
				profit, stake, gross := 0.0, 0.0, 0.0
				for _, seat := range state.Seats {
					x, y, g := 0.0, 0.0, 0.0
					for _, h := range seat.Hands {
						settled, err := blackjack.SettleHand(base, h, config.Rates{Platform: 100, Welfare: 100, Thursday: 100})
						if err != nil {
							t.Fatal(err)
						}
						if settled.Net*100 != settled.Gross*97 {
							t.Fatal("default integer rounding differs from 97%")
						}
						if h.Natural() {
							naturals++
						}
						x += float64(settled.Net-settled.Stake) / float64(base)
						y += float64(settled.Stake) / float64(base)
						g += float64(settled.Gross) / float64(base)
					}
					bySeat[seat.Number].add(x, y, g)
					profit += x
					stake += y
					gross += g
				}
				whole.add(profit, stake, gross)
			}
			report := func(label string, sample returnSample) {
				mean, se, upper := sample.estimate(2.326347874)
				_, _, familyUpper := sample.estimate(3.5)
				t.Logf("seats=%d public-count=%t %s n=%d pre-fee-RTP=%.8f post-fee-ROI=%.8f SE=%.8f upper99=%.8f family-upper=%.8f", seats, counting, label, sample.n, sample.gross/sample.y, mean, se, upper, familyUpper)
				if rounds >= 1_000_000 && upper >= 0 {
					t.Fatal("99% upper confidence bound is not negative")
				}
			}
			report("table-cluster", whole)
			for i, sample := range bySeat {
				report("seat-"+strconv.Itoa(i+1), sample)
			}
			t.Logf("seed=%d splits=%d doubles=%d naturals=%d largest-deal=%d/312", seed+int64(seats)*2+int64(boolIndex(counting)), splits, doubles, naturals, maxCards)
		}
	}
}
