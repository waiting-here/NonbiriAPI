// Package rating keeps competitive results separate from game payments.
package rating

import "math"

const Initial = 1500

// Next applies a symmetric Elo update with K=32. Score is 0, 0.5 or 1 for
// the first seat. Both changes use the pre-match ratings and conserve points.
func Next(before [2]int, score float64) [2]int {
	expected := 1 / (1 + math.Pow(10, (float64(before[1])-float64(before[0]))/400))
	delta := int(math.Round(32 * (score - expected)))
	return [2]int{before[0] + delta, before[1] - delta}
}
