package config

import (
	"encoding/json"

	"github.com/waiting-here/NonbiriAPI/internal/game"
)

func Descriptor() game.ModuleDescriptor {
	return game.ModuleDescriptor{ID: game.LinkLinkID, Version: game.LinkLinkVersion, StableOrder: 1, ResourcePrefixes: []string{"ll_"}, Specs: append([]string(nil), linkLinkSpecs[:]...), HomeRouteID: "game-linklink", ContinuationIDs: []string{"linklink_session"}, Codec: Codec{}, Routes: []game.RouteDeclaration{
		{Station: "user", Method: "POST", Pattern: "/api/games/linklink/sessions"},
		{Station: "user", Method: "GET", Pattern: "/api/games/linklink/session", Continuation: true},
		{Station: "user", Method: "POST", Pattern: "/api/games/linklink/sessions/{id}/matches", Continuation: true},
		{Station: "user", Method: "POST", Pattern: "/api/games/linklink/sessions/{id}/abandon", Continuation: true},
		{Station: "user", Method: "POST", Pattern: "/api/games/linklink/sessions/{id}/lease", Continuation: true},
	}}
}

func (Codec) ValidatePatch(body json.RawMessage) error {
	var patch LinkLinkConfigPatch
	return game.DecodeConfigPatch(body, &patch)
}
func (Codec) CompileWire(body json.RawMessage, master bool) (game.ConfigValue, error) {
	var wire LinkLinkWireConfig
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
