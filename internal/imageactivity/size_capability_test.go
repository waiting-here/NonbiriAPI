package imageactivity

import "testing"

func TestResolveSizeModes(t *testing.T) {
	grid := SizeCapability{Mode: ResolutionRatioGrid, Combinations: []SizeCombination{{Ratio: "4:3", Resolution: "medium", Width: 1024, Height: 768, Tier: "medium"}}}
	got, err := grid.ResolveSize(SizeInput{Ratio: "4:3", Resolution: "medium"})
	if err != nil || got.Values[Size] != "1024x768" || got.Values[AspectRatio] != "4:3" || got.Values[Resolution] != "medium" || got.Selection.Tier != "medium" {
		t.Fatalf("grid lost a linked field: %+v %v", got, err)
	}
	if _, err := grid.ResolveSize(SizeInput{Ratio: "4:3", Resolution: "medium", Size: "768x1024"}); err == nil {
		t.Fatal("conflicting explicit size accepted")
	}
	byRatio := SizeCapability{Mode: RatioSizeMap, Combinations: []SizeCombination{{Ratio: "portrait", Width: 768, Height: 1024}}}
	got, err = byRatio.ResolveSize(SizeInput{Ratio: "portrait"})
	if err != nil || got.Values[Size] != "768x1024" || got.Values[AspectRatio] != "portrait" {
		t.Fatalf("ratio-size mapping: %+v %v", got, err)
	}
	unknown := SizeCapability{Mode: RatioResolution, Combinations: []SizeCombination{{Ratio: "4:3", Resolution: "single", Tier: "single"}}}
	got, err = unknown.ResolveSize(SizeInput{Ratio: "4:3", Resolution: "single"})
	if err != nil || got.Selection.Width != 0 || got.Values[Size] != nil || got.Selection.Tier != "single" || got.Values[Resolution] != "single" {
		t.Fatalf("unknown exact dimensions must remain unknown: %+v %v", got, err)
	}
	widthHeight := SizeCapability{Mode: WidthHeight, Width: &DimensionRange{Minimum: 256, Maximum: 1024, Step: 256}, Height: &DimensionRange{Minimum: 256, Maximum: 1024, Step: 256}, MaxPixels: 786432, Combinations: []SizeCombination{{Width: 768, Height: 1024, Tier: "large"}}, Auto: true}
	got, err = widthHeight.ResolveSize(SizeInput{Size: "768x1024"})
	if err != nil || got.Selection.Tier != "large" || got.Selection.Width != 768 || got.Selection.Height != 1024 {
		t.Fatalf("explicit grid tier: %+v %v", got, err)
	}
	for _, size := range []string{"000768x1024", "1024x1024", "512x768x1", "257x512", "512x512"} {
		if _, err := widthHeight.ResolveSize(SizeInput{Size: size}); err == nil {
			t.Fatalf("invalid dimensions accepted: %q", size)
		}
	}
	got, err = widthHeight.ResolveSize(SizeInput{Auto: true})
	if err != nil || !got.Selection.Auto || got.Values[Size] != "auto" || got.Selection.Width != 0 {
		t.Fatalf("explicit auto: %+v %v", got, err)
	}
}

func TestSizeCapabilityRejectsAmbiguity(t *testing.T) {
	for _, bad := range []SizeCapability{
		{Mode: ""},
		{Mode: ResolutionRatioGrid},
		{Mode: ResolutionRatioGrid, Combinations: []SizeCombination{{Ratio: "1:1", Resolution: "low"}}},
		{Mode: RatioSizeMap, Combinations: []SizeCombination{{Ratio: "wide", Width: 1024, Height: 768}, {Ratio: "wide", Width: 768, Height: 1024}}},
		{Mode: RatioResolution, Combinations: []SizeCombination{{Ratio: "1:1", Resolution: "low", Width: 256, Height: 256, Tier: "small"}, {Ratio: "4:4", Resolution: "low", Width: 256, Height: 256, Tier: "large"}}},
		{Mode: RatioResolution, Combinations: []SizeCombination{{Ratio: "1:1", Resolution: "low", Width: 256, Height: 256, Tier: "small"}, {Ratio: "4:4", Resolution: "low", Width: 256, Height: 256, Tier: "small"}}},
		{Mode: WidthHeight, Width: &DimensionRange{Minimum: 256, Maximum: 512, Step: 256}, Height: &DimensionRange{Minimum: 256, Maximum: 512, Step: 256}, Combinations: []SizeCombination{{Width: 257, Height: 256}}},
	} {
		if err := bad.Validate(); err == nil {
			t.Fatalf("invalid size capability accepted: %+v", bad)
		}
	}
}
