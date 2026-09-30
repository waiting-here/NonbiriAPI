package imageactivity

import (
	"encoding/json"
	"errors"
	"strconv"
	"testing"
)

func TestFreeDimensionsRequireExactPricesAcrossQuoteSubmitAndPreview(t *testing.T) {
	f := newFixture(t)
	f.configure(t)
	before, err := f.service.GetAdminModel(f.ctx(f.admin), f.admin, f.model)
	if err != nil {
		t.Fatal(err)
	}
	capability := &SizeCapability{Mode: WidthHeight, Width: &DimensionRange{256, 1024, 256}, Height: &DimensionRange{256, 1024, 256}, MaxPixels: 786432, Auto: true,
		Combinations: []SizeCombination{{Width: 512, Height: 768, Tier: "standard"}, {Width: 768, Height: 512, Tier: "standard"}}}
	pricing := &PricingPolicy{Default: Price{"1", "0"}, Fallback: "unavailable",
		Tiers: []TierPrice{{Tier: "standard", Price: Price{"2", "0"}}, {Tier: "auto", Price: Price{"4", "0"}}},
		Sizes: []SizePrice{{Width: 512, Height: 768, Price: Price{"7", "1"}}, {Width: 257, Height: 256, Price: Price{"3", "0"}}, {Width: 1024, Height: 1024, Price: Price{"3", "0"}}, {Width: 256, Height: 512, Price: Price{"3", "0"}}}}
	rules := append([]ParameterRule(nil), before.Parameters...)
	rules = append(rules, ParameterRule{Key: Size, Supported: true, Required: true, Type: "string", LengthUnit: "utf8_bytes"})
	draft := ModelInput{ExpectedRevision: before.Revision, DisplayName: before.DisplayName, Description: before.Description, Enabled: true, CapabilityConfirmed: true,
		Price: pricing.Default, Pricing: pricing, SizeCapability: capability, Parameters: rules, Combinations: []CombinationRule{},
		Mapping: Mapping{ModelPointer: "/model", Parameters: map[ParameterKey]string{Prompt: "/prompt", N: "/n", Size: "/size"}, Constants: []Constant{}}}
	saved, err := f.service.PutModel(f.ctx(f.admin), f.admin, f.model, f.key(), draft)
	if err != nil {
		t.Fatal(err)
	}
	draft.ExpectedRevision = saved.Value.Revision
	for _, tc := range []struct {
		size string
		want error
	}{
		{"768x512", ErrUnavailable}, // The declared tier cannot bypass a missing exact row.
		{"512x512", ErrInvalid},     // Cartesian recombination is not a capability choice.
		{"257x256", ErrInvalid},     // Priced but off-step.
		{"1024x1024", ErrInvalid},   // Priced but above max_pixels.
		{"256x512", ErrInvalid},     // Priced but excluded by combinations.
	} {
		input := SubmitInput{ModelID: f.model, ExpectedModelRevision: saved.Value.Revision, ExpectedPricingRevision: saved.Value.PricingRevision, Prompt: "Synthetic dimensions", Size: json.RawMessage(strconv.Quote(tc.size))}
		if _, err = f.service.Quote(f.ctx(f.user), f.user, input); !errors.Is(err, tc.want) {
			t.Fatalf("quote %s: %v", tc.size, err)
		}
		if _, err = f.service.Submit(f.ctx(f.user), f.user, f.key(), input); !errors.Is(err, tc.want) {
			t.Fatalf("submit %s: %v", tc.size, err)
		}
		check, err := f.service.CheckModel(f.ctx(f.admin), f.admin, CheckInput{ModelID: f.model, Draft: draft, Parameters: input})
		if err != nil || check.Valid || check.Quote != nil {
			t.Fatalf("preview %s: %+v %v", tc.size, check, err)
		}
	}
	input := SubmitInput{ModelID: f.model, ExpectedModelRevision: saved.Value.Revision, ExpectedPricingRevision: saved.Value.PricingRevision, Prompt: "Synthetic dimensions", Size: json.RawMessage(strconv.Quote("512x768"))}
	quote, err := f.service.Quote(f.ctx(f.user), f.user, input)
	if err != nil || quote.Basis != "size" || quote.Unit != (Price{"7", "1"}) {
		t.Fatalf("exact quote %+v %v", quote, err)
	}
	check, err := f.service.CheckModel(f.ctx(f.admin), f.admin, CheckInput{ModelID: f.model, Draft: draft, Parameters: input})
	if err != nil || !check.Valid || check.Quote == nil || check.Quote.Total != quote.Total {
		t.Fatalf("exact preview %+v %v", check, err)
	}
	input.Size = json.RawMessage(strconv.Quote("auto"))
	auto, err := f.service.Quote(f.ctx(f.user), f.user, input)
	if err != nil || auto.Basis != "auto" || auto.Unit != (Price{"4", "0"}) {
		t.Fatalf("auto quote %+v %v", auto, err)
	}
	var tasks int
	if err = f.database.QueryRow("SELECT count(*) FROM image_activity_tasks").Scan(&tasks); err != nil || tasks != 0 || f.upstream.posts.Load() != 0 {
		t.Fatalf("quote/preview side effects: tasks=%d err=%v", tasks, err)
	}
	input.Size = json.RawMessage(strconv.Quote("512x768"))
	accepted, err := f.service.Submit(f.ctx(f.user), f.user, f.key(), input)
	if err != nil || accepted.Value.Task.Charge != quote.Total {
		t.Fatalf("exact submission %+v %v", accepted, err)
	}
	f.checkLedger(t)
}

func TestLegacyDimensionsExactPriceAndOtherFallback(t *testing.T) {
	dimensions := &DimensionRule{Format: "width_height", Width: DimensionRange{256, 1024, 256}, Height: DimensionRange{256, 1024, 256}}
	model := modelSnapshot{input: ModelInput{Parameters: []ParameterRule{{Key: Size, Supported: true, Type: "string", Dimensions: dimensions}}},
		pricing: PricingPolicy{Default: Price{"1", "0"}, Fallback: "unavailable", Sizes: []SizePrice{{Width: 512, Height: 768, Price: Price{"7", "0"}}}}}
	_, quote, err := selectedPrice(model, map[ParameterKey]any{Size: "512x768"}, 2)
	if err != nil || quote.Basis != "size" || quote.Total.Paper != "14" {
		t.Fatalf("legacy exact %+v %v", quote, err)
	}
	if _, _, err := selectedPrice(model, map[ParameterKey]any{Size: "768x512"}, 1); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("legacy unpriced %v", err)
	}
	model.pricing.Fallback = "default"
	_, quote, err = selectedPrice(model, map[ParameterKey]any{Size: "512x768"}, 2)
	if err != nil || quote.Basis != "default" || quote.Total.Paper != "2" {
		t.Fatalf("legacy fallback changed %+v %v", quote, err)
	}
}
