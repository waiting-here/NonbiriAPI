// Package config describes the permanent lake game's availability and exchanges.
package config

import (
	"encoding/json"
	"maps"
	"math/big"
	"slices"

	"github.com/waiting-here/NonbiriAPI/internal/game"
)

const EnabledKey = "game_lakenotes_enabled"
const ExchangesKey = "game_lakenotes_exchanges"

type Direction string

const (
	CoinsToGeneral Direction = "coins_to_general"
	GeneralToCoins Direction = "general_to_coins"
	CoinsToGame    Direction = "coins_to_game"
	GameToCoins    Direction = "game_to_coins"
)

func (d Direction) Stored() string {
	return map[Direction]string{CoinsToGeneral: "coin_to_general", GeneralToCoins: "general_to_coin", CoinsToGame: "coin_to_game", GameToCoins: "game_to_coin"}[d]
}

var Directions = []Direction{CoinsToGeneral, GeneralToCoins, CoinsToGame, GameToCoins}

type ExchangeSetting struct {
	Enabled      bool   `json:"enabled"`
	SourceAmount string `json:"source_amount"`
	TargetAmount string `json:"target_amount"`
}
type Wire struct {
	Enabled   bool                          `json:"enabled"`
	Exchanges map[Direction]ExchangeSetting `json:"exchanges"`
}
type Patch struct {
	Enabled   *bool                         `json:"enabled,omitempty"`
	Exchanges map[Direction]ExchangeSetting `json:"exchanges,omitempty"`
}
type Codec struct{}
type compiled struct{ wire Wire }

func Defaults() map[Direction]ExchangeSetting {
	out := make(map[Direction]ExchangeSetting, len(Directions))
	for _, d := range Directions {
		out[d] = ExchangeSetting{}
	}
	return out
}
func validExchanges(values map[Direction]ExchangeSetting) bool {
	if len(values) != len(Directions) {
		return false
	}
	for d, v := range values {
		if !slices.Contains(Directions, d) {
			return false
		}
		if !v.Enabled && v.SourceAmount == "" && v.TargetAmount == "" {
			continue
		}
		for i, amount := range []string{v.SourceAmount, v.TargetAmount} {
			n, ok := new(big.Int).SetString(amount, 10)
			if !ok || n.Sign() <= 0 || n.BitLen() > 128 || n.String() != amount {
				return false
			}
			credit := i == 0 && (d == GeneralToCoins || d == GameToCoins) || i == 1 && (d == CoinsToGeneral || d == CoinsToGame)
			if credit && n.Cmp(big.NewInt(game.MaxMoneyMilli)) > 0 {
				return false
			}
		}
	}
	return true
}
func (Codec) Keys() []string { return []string{EnabledKey, ExchangesKey} }
func Compile(raw map[string]string) (Wire, error) {
	master, err := game.RawBool(raw, game.GamesEnabledKey, false)
	if err != nil {
		return Wire{}, err
	}
	enabled, err := game.RawBool(raw, EnabledKey, false)
	if err != nil || enabled && !master {
		return Wire{}, game.ErrInvalidConfig
	}
	wire := Wire{Enabled: enabled, Exchanges: Defaults()}
	if value := raw[ExchangesKey]; value != "" {
		if err = game.DecodeConfigPatch([]byte(value), &wire.Exchanges); err != nil {
			return Wire{}, err
		}
	}
	if !validExchanges(wire.Exchanges) {
		return Wire{}, game.ErrInvalidConfig
	}
	return wire, nil
}
func (Codec) Compile(raw map[string]string) (game.ConfigValue, error) {
	wire, err := Compile(raw)
	if err != nil {
		return nil, err
	}
	return compiled{wire}, nil
}
func (c Codec) CompileWire(body json.RawMessage, master bool) (game.ConfigValue, error) {
	var wire Wire
	if err := game.DecodeConfigPatch(body, &wire); err != nil {
		return nil, err
	}
	// An omitted module in older configuration projections starts closed.
	if wire.Exchanges == nil {
		wire.Exchanges = Defaults()
	}
	return c.Compile(map[string]string{game.GamesEnabledKey: game.BoolRaw(master), EnabledKey: game.BoolRaw(wire.Enabled), ExchangesKey: string(game.ConfigJSON(wire.Exchanges))})
}
func (Codec) ValidatePatch(body json.RawMessage) error {
	var patch Patch
	if err := game.DecodeConfigPatch(body, &patch); err != nil {
		return err
	}
	for d := range patch.Exchanges {
		if !slices.Contains(Directions, d) {
			return game.ErrInvalidConfig
		}
	}
	return nil
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
func (c compiled) Enabled() bool    { return c.wire.Enabled }
func (c compiled) NeedsReady() bool { return false }
func (c compiled) Raw() map[string]string {
	return map[string]string{EnabledKey: game.BoolRaw(c.wire.Enabled), ExchangesKey: string(game.ConfigJSON(c.wire.Exchanges))}
}
func (c compiled) Wire() json.RawMessage { return game.ConfigJSON(c.wire) }
func (c compiled) UserWire(available func(string, string) bool) json.RawMessage {
	wire := c.wire
	wire.Enabled = wire.Enabled && available("", "")
	wire.Exchanges = maps.Clone(wire.Exchanges)
	return game.ConfigJSON(wire)
}
func Descriptor() game.ModuleDescriptor {
	routes := []game.RouteDeclaration{
		{Station: "user", Method: "GET", Pattern: "/api/games/lake-notes/profile", Continuation: true},
		{Station: "user", Method: "GET", Pattern: "/api/games/lake-notes/rules", Continuation: true},
		{Station: "user", Method: "GET", Pattern: "/api/games/lake-notes/casts/{id}", Continuation: true},
		{Station: "user", Method: "POST", Pattern: "/api/games/lake-notes/exchange/quote"},
		{Station: "user", Method: "POST", Pattern: "/api/games/lake-notes/exchange"},
		{Station: "user", Method: "POST", Pattern: "/api/games/lake-notes/actions"},
		{Station: "user", Method: "POST", Pattern: "/api/games/lake-notes/casts"},
		{Station: "user", Method: "POST", Pattern: "/api/games/lake-notes/casts/{id}/checkpoint"},
		{Station: "user", Method: "POST", Pattern: "/api/games/lake-notes/casts/{id}/pause", Continuation: true},
		{Station: "user", Method: "POST", Pattern: "/api/games/lake-notes/casts/{id}/resume"},
		{Station: "admin", Method: "GET", Pattern: "/admin/api/games/lake-notes/periods"},
	}
	return game.ModuleDescriptor{ID: game.LakeNotesID, Version: 1, StableOrder: 8, ResourcePrefixes: []string{"lnc_"}, HomeRouteID: "game-lake-notes", Codec: Codec{}, Routes: routes}
}
