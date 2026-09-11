package config

import (
	"encoding/json"
	"fmt"
	"maps"
	"strconv"

	"github.com/waiting-here/NonbiriAPI/internal/game"
)

const (
	LinkLinkEnabledKey = "game_linklink_enabled"
)

type LinkLinkSpecConfig struct {
	Enabled    bool
	PriceMilli int64
}

type LinkLinkConfig struct {
	Enabled bool
	Specs   map[string]LinkLinkSpecConfig
}

type LinkLinkWireConfig struct {
	Enabled bool                        `json:"enabled"`
	Specs   map[string]LinkLinkWireSpec `json:"specs"`
}
type LinkLinkWireSpec struct {
	Enabled bool   `json:"enabled"`
	Price   string `json:"price"`
	Seconds int    `json:"seconds,omitempty"`
}
type LinkLinkConfigPatch struct {
	Enabled *bool                         `json:"enabled,omitempty"`
	Specs   *map[string]LinkLinkSpecPatch `json:"specs,omitempty"`
}
type LinkLinkSpecPatch struct {
	Enabled *bool   `json:"enabled,omitempty"`
	Price   *string `json:"price,omitempty"`
}
type Snapshot struct {
	GamesEnabled bool
	LinkLink     LinkLinkConfig
}

func CompileConfig(raw map[string]string) (Snapshot, error) {
	master, err := game.RawBool(raw, game.GamesEnabledKey, false)
	if err != nil {
		return Snapshot{}, err
	}
	linkEnabled, err := game.RawBool(raw, LinkLinkEnabledKey, false)
	if err != nil || linkEnabled && !master {
		return Snapshot{}, game.ErrInvalidConfig
	}
	link := LinkLinkConfig{Enabled: linkEnabled, Specs: make(map[string]LinkLinkSpecConfig, 3)}
	for _, spec := range linkLinkSpecs {
		enabled, parseErr := game.RawBool(raw, LinkLinkSpecEnabledKey(spec), false)
		if parseErr != nil {
			return Snapshot{}, parseErr
		}
		price, parseErr := game.RawAmount(raw, LinkLinkSpecPriceKey(spec), 0, 0)
		if parseErr != nil {
			return Snapshot{}, parseErr
		}
		if enabled && (!linkEnabled || price == 0) {
			return Snapshot{}, fmt.Errorf("%w: enabled LinkLink spec", game.ErrInvalidConfig)
		}
		link.Specs[spec] = LinkLinkSpecConfig{Enabled: enabled, PriceMilli: price}
	}

	return Snapshot{GamesEnabled: master, LinkLink: link}, nil
}

var linkLinkSpecs = [...]string{game.LinkLinkSpec6x8, game.LinkLinkSpec8x8, game.LinkLinkSpec10x10}

func LinkLinkSpecEnabledKey(spec string) string { return "game_linklink_" + spec + "_enabled" }
func LinkLinkSpecPriceKey(spec string) string   { return "game_linklink_" + spec + "_price_milli" }
func Seconds(spec string) int {
	switch spec {
	case game.LinkLinkSpec6x8:
		return 150
	case game.LinkLinkSpec8x8:
		return 180
	case game.LinkLinkSpec10x10:
		return 240
	}
	return 0
}

func (snapshot Snapshot) Wire() LinkLinkWireConfig {
	var result LinkLinkWireConfig
	result = LinkLinkWireConfig{Enabled: snapshot.LinkLink.Enabled, Specs: make(map[string]LinkLinkWireSpec, 3)}
	for _, spec := range linkLinkSpecs {
		value := snapshot.LinkLink.Specs[spec]
		result.Specs[spec] = LinkLinkWireSpec{Enabled: value.Enabled, Price: game.FormatAmount(value.PriceMilli)}
	}
	return result
}
func wireRaw(config LinkLinkWireConfig) (map[string]string, error) {
	raw := map[string]string{}
	setBool := func(key string, value bool) { raw[key] = game.BoolRaw(value) }
	setBool(LinkLinkEnabledKey, config.Enabled)
	if len(config.Specs) != 3 {
		return nil, fmt.Errorf("%w: LinkLink specs", game.ErrInvalidConfig)
	}
	for _, spec := range linkLinkSpecs {
		value, ok := config.Specs[spec]
		if !ok || value.Seconds != 0 {
			return nil, fmt.Errorf("%w: LinkLink spec", game.ErrInvalidConfig)
		}
		setBool(LinkLinkSpecEnabledKey(spec), value.Enabled)
		milli, err := game.ParseAmount(value.Price)
		if err != nil {
			return nil, err
		}
		raw[LinkLinkSpecPriceKey(spec)] = strconv.FormatInt(milli, 10)
	}
	return raw, nil
}

type Codec struct{}
type compiled struct {
	snapshot Snapshot
	raw      map[string]string
}

func (Codec) Keys() []string {
	keys := []string{LinkLinkEnabledKey}
	for _, spec := range linkLinkSpecs {
		keys = append(keys, LinkLinkSpecEnabledKey(spec), LinkLinkSpecPriceKey(spec))
	}
	return keys
}
func (Codec) Compile(raw map[string]string) (game.ConfigValue, error) {
	snapshot, err := CompileConfig(raw)
	if err != nil {
		return nil, err
	}
	canonical, err := wireRaw(snapshot.Wire())
	if err != nil {
		return nil, err
	}
	return compiled{snapshot: snapshot, raw: canonical}, nil
}
func (Codec) Merge(current game.ConfigValue, body json.RawMessage, master bool) (game.ConfigValue, error) {
	value, ok := current.(compiled)
	if !ok {
		return nil, game.ErrInvalidConfig
	}
	var patch LinkLinkConfigPatch
	if err := game.DecodeConfigPatch(body, &patch); err != nil {
		return nil, err
	}
	var next LinkLinkWireConfig
	if err := game.MergeConfigFields(value.Wire(), body, &next); err != nil {
		return nil, err
	}
	raw, err := wireRaw(next)
	if err != nil {
		return nil, err
	}
	raw[game.GamesEnabledKey] = game.BoolRaw(master)
	return (Codec{}).Compile(raw)
}
func (value compiled) Enabled() bool          { return value.snapshot.LinkLink.Enabled }
func (value compiled) NeedsReady() bool       { return false }
func (value compiled) Raw() map[string]string { return maps.Clone(value.raw) }
func (value compiled) Wire() json.RawMessage  { return game.ConfigJSON(value.snapshot.Wire()) }
func (value compiled) UserWire(available func(mode, spec string) bool) json.RawMessage {
	wire := value.snapshot.Wire()
	ready := false
	for _, spec := range linkLinkSpecs {
		item := wire.Specs[spec]
		supported := available("", spec)
		ready = ready || supported
		item.Enabled = item.Enabled && supported
		item.Seconds = Seconds(spec)
		wire.Specs[spec] = item
	}
	wire.Enabled = wire.Enabled && ready
	return game.ConfigJSON(wire)
}
