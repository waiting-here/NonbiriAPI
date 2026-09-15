package config

import (
	"encoding/json"

	"github.com/waiting-here/NonbiriAPI/internal/game"
)

func Descriptor() game.ModuleDescriptor {
	return game.ModuleDescriptor{ID: game.FishingID, Version: game.FishingVersion, StableOrder: 0, ResourcePrefixes: []string{"fb_"}, BoardIDs: []string{"single", "recent_single", "total"}, HomeRouteID: "game-fishing", Codec: Codec{}, Onboarding: []game.OnboardingTask{{Key: "worm", RewardMilli: 1_000_000}, {Key: "lure", RewardMilli: 1_000_000}, {Key: "premium", RewardMilli: 1_000_000}}, Routes: []game.RouteDeclaration{
		{Station: "user", Method: "GET", Pattern: "/api/games/fishing/randomness/{id}"},
		{Station: "user", Method: "POST", Pattern: "/api/games/fishing/batches"},
		{Station: "user", Method: "GET", Pattern: "/api/games/fishing/state"},
		{Station: "user", Method: "POST", Pattern: "/api/games/fishing/batches/{id}/ack"},
		{Station: "user", Method: "POST", Pattern: "/api/games/fishing/batches/{id}/recover"},
		{Station: "user", Method: "GET", Pattern: "/api/games/fishing/leaderboard"},
	}}
}

func (Codec) ValidatePatch(body json.RawMessage) error {
	var patch FishingConfigPatch
	return game.DecodeConfigPatch(body, &patch)
}
func (Codec) CompileWire(body json.RawMessage, master bool) (game.ConfigValue, error) {
	var wire FishingWireConfig
	if err := game.DecodeConfigPatch(body, &wire); err != nil {
		return nil, err
	}
	raw, err := wireRaw(wire)
	if err != nil {
		return nil, err
	}
	raw[game.GamesEnabledKey] = game.BoolRaw(master)
	return (Codec{}).Compile(raw)
}
