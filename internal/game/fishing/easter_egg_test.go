package fishing

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

type eggSource struct {
	values []uint64
	bounds []uint64
	err    error
}

func (source *eggSource) Uint64n(bound uint64) (uint64, error) {
	source.bounds = append(source.bounds, bound)
	if len(source.values) == 0 {
		return 1, source.err
	}
	value := source.values[0]
	source.values = source.values[1:]
	return value, nil
}

func TestEasterEggSelectionTailAndEconomicIdentity(t *testing.T) {
	for _, species := range []string{"yellowcheek", "taimen", "koi"} {
		for selected := uint64(0); selected < 10; selected++ {
			input := []Result{{Outcome: Outcome{Bait: BaitPremium, Key: species, Tier: TierLegend, SizeCentimetre: 137}, Settlement: SettlementIntent{EntryMilli: 7500000, PayoutMilli: 121234567}}}
			source := &eggSource{values: []uint64{selected, 99, 1, 0}}
			got, err := DecorateBatch(context.Background(), input, source)
			if err != nil || len(got) != 1 {
				t.Fatalf("%s/%d: %v %v", species, selected, got, err)
			}
			wantLength := ""
			wantBounds := []uint64{10}
			if selected == 0 {
				wantLength = "203"
				wantBounds = []uint64{10, 100, 100, 100}
			}
			if got[0].Outcome.BlueFatFishLengthCM != wantLength || !reflect.DeepEqual(source.bounds, wantBounds) {
				t.Fatalf("selection/tail: %+v bounds=%v", got, source.bounds)
			}
			got[0].Outcome.BlueFatFishLengthCM = ""
			if !reflect.DeepEqual(got, input) {
				t.Fatalf("economic outcome changed: %+v != %+v", got, input)
			}
		}
	}
	// The minimum is attainable, and a non-legend does not consume a new draw.
	input := []Result{{Outcome: Outcome{Tier: TierSmall}}, {Outcome: Outcome{Tier: TierLegend, Key: "koi", SizeCentimetre: 200}}}
	source := &eggSource{values: []uint64{0, 0}}
	got, err := DecorateBatch(context.Background(), input, source)
	if err != nil || got[1].Outcome.BlueFatFishLengthCM != "201" || len(source.bounds) != 2 || input[1].Outcome.BlueFatFishLengthCM != "" {
		t.Fatalf("minimum/copy: %+v %v", got, err)
	}
}

func TestEasterEggFailuresReturnNoPartialBatch(t *testing.T) {
	injected := errors.New("random failed")
	input := []Result{{Outcome: Outcome{Tier: TierLegend, Key: "koi", SizeCentimetre: 100}}, {Outcome: Outcome{Tier: TierLegend, Key: "taimen", SizeCentimetre: 200}}}
	for name, source := range map[string]*eggSource{
		"second outcome error": {values: []uint64{0, 0}, err: injected},
		"invalid selection":    {values: []uint64{10}},
		"invalid tail":         {values: []uint64{0, 100}},
		"never ending tail":    {values: []uint64{0}},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := DecorateBatch(context.Background(), input, source)
			if got != nil || err == nil || input[0].Outcome.BlueFatFishLengthCM != "" {
				t.Fatalf("partial draw accepted: %+v %v", got, err)
			}
			if name == "never ending tail" && (!errors.Is(err, ErrEasterEggBudget) || len(source.bounds) != easterEggDrawBudget) {
				t.Fatalf("budget: %v %d", err, len(source.bounds))
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	got, err := DecorateBatch(ctx, input, &eggSource{})
	if got != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel: %+v %v", got, err)
	}
}

func TestEasterEggLengthExactRepresentation(t *testing.T) {
	for _, value := range []string{"201", "999", "1000", "9007199254740993", strings.Repeat("9", 128)} {
		if !ValidBlueFatFishLength(value) {
			t.Fatalf("valid length rejected: %q", value)
		}
	}
	for _, value := range []string{"", "0", "200", "0201", "+201", "201.0", "2e3", "２０１", "201\x00", strings.Repeat("9", 129)} {
		if ValidBlueFatFishLength(value) {
			t.Fatalf("invalid length accepted: %q", value)
		}
	}
	for _, pair := range [][2]string{{"200", "201"}, {"999", "1000"}, {"9007199254740992", "9007199254740993"}} {
		if CompareLengths(pair[0], pair[1]) != -1 || CompareLengths(pair[1], pair[0]) != 1 || CompareLengths(pair[0], pair[0]) != 0 {
			t.Fatalf("inexact compare: %v", pair)
		}
	}
}
