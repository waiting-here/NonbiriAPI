package adapters

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	fishingruntime "github.com/waiting-here/NonbiriAPI/internal/game/fishing/runtime"
	"github.com/waiting-here/NonbiriAPI/internal/lifecycle"
)

func TestFishingPondExportProjectsOwnQueueAndExcludesReceivedIdentity(t *testing.T) {
	returnedAt, avatar := int64(123), "https://example.test/own.png"
	owner := &fakeFishingLifecycle{export: fishingruntime.UserExport{
		Terminal: []fishingruntime.FishingTerminalExport{{Outcomes: []fishingruntime.FishingOutcome{
			{SpeciesKey: "boot", Tier: "junk", Reward: "0", Pond: &fishingruntime.PondOutcome{ReturnState: "gift", Gift: &fishingruntime.Identity{Kind: "public", DisplayName: "other-private-name"}}},
			{SpeciesKey: "fry", Tier: "junk", Reward: "0", Pond: &fishingruntime.PondOutcome{ReturnState: "released", ReturnedAt: &returnedAt}},
		}}},
		PondQueue: []fishingruntime.PondQueuedExport{{BatchID: "fb_AAAAAAAAAAAAAAAAAAAAAA", Ordinal: 0, SpeciesKey: "branch", ReturnedAt: 120, Identity: fishingruntime.Identity{Kind: "public", DisplayName: "Own signature", AvatarURL: &avatar}}},
		PondStats: []fishingruntime.PondStatExport{{Board: "cleanup", Period: "all", ItemCount: "1024"}},
	}}
	value, _, err := NewFishing(owner).ExportFishing(context.Background(), nil, lifecycle.ExportRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(value.PondQueue) != 1 || value.PondQueue[0].Identity.DisplayName != "Own signature" || len(value.PondStats) != 1 || value.PondStats[0].ItemCount != "1024" {
		t.Fatalf("pond export lost own facts: %#v", value)
	}
	if gift := value.Terminal[0].Outcomes[0].Pond.Gift; gift == nil || gift.Kind != "anonymous" {
		t.Fatalf("gift projection = %#v", gift)
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"other-private-name", "donor_user_id", "public_tie_key", "queue_id"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("export exposed %s", forbidden)
		}
	}
	returnedAt, avatar = 999, "changed"
	if *value.Terminal[0].Outcomes[1].Pond.ReturnedAt != 123 || *value.PondQueue[0].Identity.AvatarURL != "https://example.test/own.png" {
		t.Fatal("pond export aliases mutable runtime fields")
	}
}
