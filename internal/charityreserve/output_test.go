package charityreserve

import "testing"

func TestOutputFloorUsesExactPriceAndRetainsConfiguredReserve(t *testing.T) {
	for _, test := range []struct{ base, unit, tokens, want int64 }{
		{100, 1, 1, 100},
		{1, 1, 1, 1},
		{1, 1000000, 2500, 2500},
		{0, 1, 1, 1},
		{0, 2, 500001, 2},
	} {
		got, err := WithOutputFloor(test.base, test.unit, test.tokens)
		if err != nil || got != test.want {
			t.Errorf("WithOutputFloor(%d,%d,%d)=%d,%v want %d", test.base, test.unit, test.tokens, got, err, test.want)
		}
	}
	if _, err := WithOutputFloor(1, 9000000000000000, 2147483647); err == nil {
		t.Fatal("accepted over-limit reservation")
	}
}
