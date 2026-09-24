package fishing

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestBlueFishChanceEndpointsAndExactConditionalDraw(t *testing.T) {
	input := []Result{
		{Outcome: Outcome{Tier: TierSmall, Key: "small", SizeCentimetre: 10}, Settlement: SettlementIntent{EntryMilli: 7, PayoutMilli: 3}},
		{Outcome: Outcome{Tier: TierLegend, Key: "koi", SizeCentimetre: 150}, Settlement: SettlementIntent{EntryMilli: 7, PayoutMilli: 900}},
	}
	if DefaultConfig().BlueFishChanceBPS != 1000 {
		t.Fatal("unexpected default chance")
	}
	for _, test := range []struct {
		chance int
		values []uint64
		bounds []uint64
		length string
	}{
		{0, nil, nil, ""},
		{10000, []uint64{0}, []uint64{100}, "201"},
		{3750, []uint64{2, 0}, []uint64{8, 100}, "201"},
		{3750, []uint64{3}, []uint64{8}, ""},
		{1, []uint64{0, 0}, []uint64{10000, 100}, "201"},
		{1, []uint64{1}, []uint64{10000}, ""},
	} {
		source := &eggSource{values: test.values}
		out, err := DecorateBatchWithChance(context.Background(), input, source, test.chance)
		if err != nil || out[0].Outcome.BlueFatFishLengthCM != "" || out[1].Outcome.BlueFatFishLengthCM != test.length || !reflect.DeepEqual(source.bounds, test.bounds) {
			t.Fatalf("chance %d: %+v, %v, %v", test.chance, out, source.bounds, err)
		}
		out[1].Outcome.BlueFatFishLengthCM = ""
		if !reflect.DeepEqual(out, input) {
			t.Fatal("chance changed economic outcome or original length")
		}
	}
	for _, chance := range []int{-1, 10001} {
		if out, err := DecorateBatchWithChance(context.Background(), input, &eggSource{}, chance); out != nil || !errors.Is(err, ErrInvalidConfig) {
			t.Fatal("invalid chance accepted", out, err)
		}
		cfg := DefaultConfig()
		cfg.BlueFishChanceBPS = chance
		if _, err := Compile(cfg); !errors.Is(err, ErrInvalidConfig) {
			t.Fatal("invalid snapshot accepted", err)
		}
	}
}
