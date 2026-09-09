package adapters

import (
	"context"
	"errors"
	"testing"

	fishingruntime "github.com/waiting-here/NonbiriAPI/internal/game/runtime"
	"github.com/waiting-here/NonbiriAPI/internal/lifecycle"
)

func TestFishingExportCopiesExactEasterEggAndRecentBest(t *testing.T) {
	length := "9007199254740993"
	owner := &fakeFishingLifecycle{export: fishingruntime.UserExport{
		Terminal:    []fishingruntime.FishingTerminalExport{{Outcomes: []fishingruntime.FishingOutcome{{SpeciesKey: "koi", Tier: "legend", SizeCM: 137, BlueFatFishLengthCM: &length, Reward: "123.45"}}}},
		Single:      &fishingruntime.FishingLeaderboardRow{Rank: "2", SpeciesKey: "koi", SizeCM: 137, BlueFatFishLengthCM: &length},
		RollingBest: &fishingruntime.FishingLeaderboardRow{Rank: "1", SpeciesKey: "koi", SizeCM: 137, BlueFatFishLengthCM: &length},
	}}
	value, _, err := NewFishing(owner).ExportFishing(context.Background(), nil, lifecycle.ExportRequest{})
	if err != nil || value.SingleBest == nil || value.RollingBest == nil || len(value.Terminal) != 1 || *value.RollingBest.BlueFatFishLengthCM != length || *value.SingleBest.BlueFatFishLengthCM != length || *value.Terminal[0].Outcomes[0].BlueFatFishLengthCM != length {
		t.Fatalf("export: %+v %v", value, err)
	}
	length = "201"
	if *value.RollingBest.BlueFatFishLengthCM != "9007199254740993" || *value.SingleBest.BlueFatFishLengthCM != "9007199254740993" || *value.Terminal[0].Outcomes[0].BlueFatFishLengthCM != "9007199254740993" {
		t.Fatal("presentation pointers were not copied")
	}
	owner.export.RollingBest.SpeciesKey = "boot"
	if _, _, err := NewFishing(owner).ExportFishing(context.Background(), nil, lifecycle.ExportRequest{}); !errors.Is(err, lifecycle.ErrInvariant) {
		t.Fatal("non-legend egg exported", err)
	}
}
