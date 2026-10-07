package rating

import "testing"

func TestEloUsesPreMatchStrengthAndConservesPoints(t *testing.T) {
	for _, sample := range []struct {
		before [2]int
		score  float64
		after  [2]int
	}{
		{[2]int{1500, 1500}, 1, [2]int{1516, 1484}},
		{[2]int{1500, 1500}, 0.5, [2]int{1500, 1500}},
		{[2]int{1500, 1900}, 1, [2]int{1529, 1871}},
		{[2]int{1900, 1500}, 1, [2]int{1903, 1497}},
		{[2]int{1900, 1500}, 0.5, [2]int{1887, 1513}},
	} {
		if got := Next(sample.before, sample.score); got != sample.after {
			t.Fatalf("%+v score %v: got %+v want %+v", sample.before, sample.score, got, sample.after)
		}
	}
}
