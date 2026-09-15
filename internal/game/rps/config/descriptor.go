package config

import (
	"encoding/json"

	"github.com/waiting-here/NonbiriAPI/internal/game"
)

func Descriptor() game.ModuleDescriptor {
	return game.ModuleDescriptor{ID: game.RPSID, Version: game.RPSVersion, StableOrder: 2, ResourcePrefixes: []string{"rps_", "rpsq_"}, Modes: append([]string(nil), rpsModes[:]...), BoardIDs: []string{"profit_rate", "net_profit"}, HomeRouteID: "game-rps", SnapshotFields: []string{"tutorial_rps_seen"}, ContinuationIDs: []string{"rps_session"}, Codec: Codec{}, Onboarding: []game.OnboardingTask{{Key: "quick", RewardMilli: 1_000_000}, {Key: "standard", RewardMilli: 2_000_000}, {Key: "deathmatch", RewardMilli: 5_000_000}}, Routes: []game.RouteDeclaration{
		{Station: "user", Method: "GET", Pattern: "/api/games/rps/randomness/{id}", Continuation: true},
		{Station: "user", Method: "POST", Pattern: "/api/games/rps/tutorial/seen"},
		{Station: "user", Method: "POST", Pattern: "/api/games/rps/queue"},
		{Station: "user", Method: "DELETE", Pattern: "/api/games/rps/queue/{id}"},
		{Station: "user", Method: "GET", Pattern: "/api/games/rps/leaderboard"},
		{Station: "user", Method: "GET", Pattern: "/api/games/rps/state", Continuation: true},
		{Station: "user", Method: "POST", Pattern: "/api/games/rps/sessions/{id}/actions", Continuation: true},
		{Station: "user", Method: "POST", Pattern: "/api/games/rps/sessions/{id}/lease", Continuation: true},
		{Station: "user", Method: "POST", Pattern: "/api/games/rps/pending-result/ack", Continuation: true},
	}}
}

func (Codec) ValidatePatch(body json.RawMessage) error {
	var patch RPSConfigPatch
	return game.DecodeConfigPatch(body, &patch)
}
func (Codec) CompileWire(body json.RawMessage, master bool) (game.ConfigValue, error) {
	var wire RPSWireConfig
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
