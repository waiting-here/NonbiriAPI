package official

import (
	"encoding/json"
	"math"
	"slices"

	"github.com/waiting-here/NonbiriAPI/internal/game/bidding/engine"
)

// bid estimates what a card can win now and what it would be worth saved for
// an average future pool. All ranks and previous bids come from the seat view.
func bid(view engine.View, seat int, openingShift float64) int {
	own, other := view.HandRemaining[seat], view.HandRemaining[1-seat]
	pool := float64(view.PoolPoints)
	unseen := 182
	for _, card := range view.Rewards {
		unseen -= card.Rank
	}
	mean := 14.0
	if len(own) > 1 {
		mean = float64(unseen) / float64(len(own)-1)
	}
	// A valuable prize encourages the opponent to spend a larger card. Their
	// last few revealed bids can shift this estimate in either direction.
	tilt := openingShift * float64(min(4, len(other))) / 4
	played := view.Played[1-seat]
	for i := max(0, len(played)-4); i < len(played); i++ {
		tilt += float64(played[i]-7) / 32
	}
	predicted := math.Max(1, math.Min(13, 7+(pool-mean)*0.48+tilt))
	if irregularBids(view, seat) {
		return priceBid(own, other, predicted, pool, mean, 0)
	}
	// A player facing this pool also has to conserve their best cards. Price
	// that likely reply, including the option to throw a low card when their
	// best bid cannot be beaten. Keep weight on unexpected bids as well.
	likely := priceBid(other, own, predicted, pool, mean, 0)
	if len(own) > 1 {
		// When similar hands repeatedly meet at the top, a tie only carries
		// the prize. Sacrifice a cheap card on a modest pool to keep the top
		// rank for a later guaranteed win.
		if likely >= own[len(own)-1] && pool < 2.3*mean {
			return own[0]
		}
		// Spend the least card that beats a plausible bid on a contested pot.
		if pool >= 0.8*mean {
			for _, card := range own {
				if card > likely {
					return card
				}
			}
		}
	}
	return priceBid(own, other, predicted, pool, mean, likely)
}

// Compare revealed decisions with two common bidding habits. If neither fit
// the public record, spread the forecast across the hand instead of trying
// to beat a single predicted card.
func irregularBids(v engine.View, seat int) bool {
	n := len(v.Played[1-seat])
	if n < 3 || len(v.Played[seat]) != n || len(v.Rewards) < 2*n {
		return false
	}
	ranks := []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13}
	own, other := slices.Clone(ranks), slices.Clone(ranks)
	unseen, carry := 182, 0
	var priceError, rankError float64
	for i, actual := range v.Played[1-seat] {
		a, b := v.Rewards[2*i], v.Rewards[2*i+1]
		unseen -= a.Rank + b.Rank
		pool := float64(carry + a.Value() + b.Value())
		mean := float64(unseen) / float64(12-i)
		point := math.Max(1, math.Min(13, 7+(pool-mean)*0.48))
		guess := priceBid(other, own, point, pool, mean, 0)
		index := min(len(other)-1, max(0, (int(pool)-2)*len(other)/25))
		priceError += math.Abs(float64(actual - guess))
		rankError += math.Abs(float64(actual - other[index]))
		ax, bx := slices.Index(own, v.Played[seat][i]), slices.Index(other, actual)
		if ax < 0 || bx < 0 {
			return false
		}
		own = slices.Delete(own, ax, ax+1)
		other = slices.Delete(other, bx, bx+1)
		carry = 0
		if v.Played[seat][i] == actual {
			carry = int(pool)
		}
	}
	return min(priceError, rankError) > float64(n)*2.8
}

func priceBid(own, other []int, predicted, pool, mean float64, likely int) int {
	best, bestScore := own[0], math.Inf(-1)
	for _, card := range own {
		var weight, now, future float64
		for _, opponent := range other {
			difference := float64(opponent) - predicted
			w := 0.22 + math.Exp(-difference*difference/10)
			if likely != 0 {
				d := float64(opponent - likely)
				w += float64(len(other)) * 0.65 * math.Exp(-d*d/1.5)
			}
			result := 0.0
			if card > opponent {
				result = 1
			} else if card < opponent {
				result = -1
			}
			weight += w
			now += result * w
			future += result
		}
		score := pool * now / weight
		if len(own) > 1 {
			score -= mean * future / float64(len(other))
		}
		if score > bestScore {
			bestScore, best = score, card
		}
	}
	return best
}

func biddingDecision(phase string, seat, round int, raw json.RawMessage, shift float64) (any, error) {
	var view engine.View
	if err := json.Unmarshal(raw, &view); err != nil {
		return nil, err
	}
	if seat < 0 || seat > 1 || len(view.HandRemaining[0]) == 0 || len(view.HandRemaining[1]) == 0 {
		return nil, errDecision
	}
	switch phase {
	case "bid":
		return struct {
			Kind string `json:"kind"`
			Card int    `json:"card"`
		}{"bid", bid(view, seat, shift)}, nil
	case "joker":
		rank := 0
		for _, reward := range view.Rewards {
			if reward.Side == seat && reward.Round == round {
				rank = reward.Rank
			}
		}
		own, other := view.HandRemaining[seat], view.HandRemaining[1-seat]
		canContest := own[len(own)-1] >= other[len(other)-1]
		return struct {
			Kind string `json:"kind"`
			Use  bool   `json:"use"`
		}{"joker", canContest && (rank >= 9 || round >= 11)}, nil
	default:
		return nil, errDecision
	}
}
