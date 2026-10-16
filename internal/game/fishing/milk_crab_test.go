package fishing

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestMilkCrabPartitionsLegendaryCatchExactly(t *testing.T) {
	input := []Result{{Outcome: Outcome{Tier: TierLegend, Key: "koi", SizeCentimetre: 137}, Settlement: SettlementIntent{PayoutMilli: 500}}}
	for _, test := range []struct {
		blue, milk     int
		values, bounds []uint64
		kind           string
	}{
		{1000, 1000, []uint64{0, 0}, []uint64{10, 100}, "blue"},
		{1000, 1000, []uint64{1, 0}, []uint64{10, 100}, "milk"},
		{1000, 1000, []uint64{2}, []uint64{10}, ""},
		{3750, 6250, []uint64{2, 0}, []uint64{8, 100}, "blue"},
		{3750, 6250, []uint64{3, 0}, []uint64{8, 100}, "milk"},
		{0, 10000, []uint64{0}, []uint64{100}, "milk"},
		{0, 0, nil, nil, ""},
	} {
		source := &eggSource{values: test.values}
		out, err := DecorateBatchWithChances(context.Background(), input, source, test.blue, test.milk)
		if err != nil || len(out) != 1 || !reflect.DeepEqual(source.bounds, test.bounds) {
			t.Fatal(out, err, source.bounds)
		}
		wantBlue, wantMilk := "", ""
		if test.kind == "blue" {
			wantBlue = "201"
		}
		if test.kind == "milk" {
			wantMilk = "201"
		}
		if out[0].Outcome.BlueFatFishLengthCM != wantBlue || out[0].Outcome.MilkCrabLengthCM != wantMilk {
			t.Fatal(out)
		}
		out[0].Outcome.BlueFatFishLengthCM = ""
		out[0].Outcome.MilkCrabLengthCM = ""
		if !reflect.DeepEqual(input, out) {
			t.Fatal("presentation changed economic result")
		}
	}
	for _, pair := range [][2]int{{-1, 0}, {0, -1}, {9001, 1000}, {10000, 1}, {0, 10001}} {
		if out, err := DecorateBatchWithChances(context.Background(), input, &eggSource{}, pair[0], pair[1]); out != nil || !errors.Is(err, ErrInvalidConfig) {
			t.Fatal(pair, out, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if out, err := DecorateBatchWithChances(ctx, input, &eggSource{}, 0, 10000); out != nil || !errors.Is(err, context.Canceled) {
		t.Fatal(out, err)
	}
}
