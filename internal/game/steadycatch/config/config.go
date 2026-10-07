// Package config describes the catch game's adjustable entry and first-clear terms.
package config

import (
	"encoding/json"
	"maps"
	"strconv"

	"github.com/waiting-here/NonbiriAPI/internal/game"
)

const (
	EnabledKey = "game_steadycatch_enabled"
	PriceKey   = "game_steadycatch_price_milli"
	RewardKey  = "game_steadycatch_first_reward_milli"
)

type Settings struct {
	Enabled       bool
	Price, Reward int64
}
type Wire struct {
	Enabled     bool   `json:"enabled"`
	Price       string `json:"price"`
	FirstReward string `json:"first_clear_reward"`
}
type Patch struct {
	Enabled     *bool   `json:"enabled,omitempty"`
	Price       *string `json:"price,omitempty"`
	FirstReward *string `json:"first_clear_reward,omitempty"`
}
type Codec struct{}
type compiled struct {
	settings Settings
	raw      map[string]string
}

func Compile(raw map[string]string) (Settings, error) {
	master, err := game.RawBool(raw, game.GamesEnabledKey, false)
	if err != nil {
		return Settings{}, err
	}
	enabled, err := game.RawBool(raw, EnabledKey, false)
	if err != nil || enabled && !master {
		return Settings{}, game.ErrInvalidConfig
	}
	price, err := game.RawAmount(raw, PriceKey, 0, 0)
	if err != nil {
		return Settings{}, err
	}
	reward, err := game.RawAmount(raw, RewardKey, 0, 0)
	return Settings{enabled, price, reward}, err
}
func (s Settings) Wire() Wire {
	return Wire{s.Enabled, game.FormatAmount(s.Price), game.FormatAmount(s.Reward)}
}
func (Codec) Keys() []string { return []string{EnabledKey, PriceKey, RewardKey} }
func (Codec) Compile(raw map[string]string) (game.ConfigValue, error) {
	s, err := Compile(raw)
	if err != nil {
		return nil, err
	}
	return compiled{s, map[string]string{EnabledKey: game.BoolRaw(s.Enabled), PriceKey: strconv.FormatInt(s.Price, 10), RewardKey: strconv.FormatInt(s.Reward, 10)}}, nil
}
func (Codec) CompileWire(body json.RawMessage, master bool) (game.ConfigValue, error) {
	var wire Wire
	if err := game.DecodeConfigPatch(body, &wire); err != nil {
		return nil, err
	}
	price, err := game.ParseAmount(wire.Price)
	if err != nil {
		return nil, err
	}
	reward, err := game.ParseAmount(wire.FirstReward)
	if err != nil {
		return nil, err
	}
	return (Codec{}).Compile(map[string]string{game.GamesEnabledKey: game.BoolRaw(master), EnabledKey: game.BoolRaw(wire.Enabled), PriceKey: strconv.FormatInt(price, 10), RewardKey: strconv.FormatInt(reward, 10)})
}
func (Codec) ValidatePatch(body json.RawMessage) error {
	var patch Patch
	return game.DecodeConfigPatch(body, &patch)
}
func (c Codec) Merge(current game.ConfigValue, body json.RawMessage, master bool) (game.ConfigValue, error) {
	if err := c.ValidatePatch(body); err != nil {
		return nil, err
	}
	var next Wire
	if err := game.MergeConfigFields(current.Wire(), body, &next); err != nil {
		return nil, err
	}
	return c.CompileWire(game.ConfigJSON(next), master)
}
func (c compiled) Enabled() bool          { return c.settings.Enabled }
func (c compiled) NeedsReady() bool       { return false }
func (c compiled) Raw() map[string]string { return maps.Clone(c.raw) }
func (c compiled) Wire() json.RawMessage  { return game.ConfigJSON(c.settings.Wire()) }
func (c compiled) UserWire(available func(string, string) bool) json.RawMessage {
	w := c.settings.Wire()
	w.Enabled = w.Enabled && available("", "")
	return game.ConfigJSON(w)
}
func Descriptor() game.ModuleDescriptor {
	return game.ModuleDescriptor{ID: game.SteadyCatchID, Version: 1, StableOrder: 7, ResourcePrefixes: []string{"sc_"}, HomeRouteID: "game-steady-catch", ContinuationIDs: []string{"steadycatch_session"}, Codec: Codec{}, BoardIDs: []string{"score"}, Routes: []game.RouteDeclaration{
		{Station: "user", Method: "GET", Pattern: "/api/games/steady-catch/catalog"},
		{Station: "user", Method: "POST", Pattern: "/api/games/steady-catch/sessions"},
		{Station: "user", Method: "GET", Pattern: "/api/games/steady-catch/session", Continuation: true},
		{Station: "user", Method: "POST", Pattern: "/api/games/steady-catch/sessions/{id}/controls", Continuation: true},
		{Station: "user", Method: "GET", Pattern: "/api/games/steady-catch/leaderboard"},
	}}
}
