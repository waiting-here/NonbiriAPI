package config

import (
	"encoding/json"
	"maps"
	"slices"
	"strconv"

	"github.com/waiting-here/NonbiriAPI/internal/game"
)

const (
	ID            = "likes"
	Version       = 1
	EnabledKey    = "game_likes_enabled"
	QueueSeconds  = 120
	QueueCapacity = 4096
)

var modeKeys = [...]string{"quick", "standard"}
var defaultTickets = [...]int64{5_000_000, 25_000_000}

type RakeBP struct {
	Platform int `json:"platform"`
	Welfare  int `json:"welfare"`
	Thursday int `json:"thursday"`
}

type Mode struct {
	Enabled     bool
	TicketMilli int64
	RakeBP      RakeBP
}

type Snapshot struct {
	Enabled bool
	Modes   map[string]Mode
}

type Wire struct {
	Enabled bool                `json:"enabled"`
	Modes   map[string]WireMode `json:"modes"`
}

type WireMode struct {
	Enabled bool   `json:"enabled"`
	Ticket  string `json:"ticket"`
	RakeBP  RakeBP `json:"rake_bp"`
}

type Patch struct {
	Enabled *bool                 `json:"enabled,omitempty"`
	Modes   *map[string]ModePatch `json:"modes,omitempty"`
}

type ModePatch struct {
	Enabled *bool      `json:"enabled,omitempty"`
	Ticket  *string    `json:"ticket,omitempty"`
	RakeBP  *RakePatch `json:"rake_bp,omitempty"`
}

type RakePatch struct {
	Platform *int `json:"platform,omitempty"`
	Welfare  *int `json:"welfare,omitempty"`
	Thursday *int `json:"thursday,omitempty"`
}

func Modes() []string                   { return slices.Clone(modeKeys[:]) }
func ModeEnabledKey(mode string) string { return "game_likes_" + mode + "_enabled" }
func TicketKey(mode string) string      { return "game_likes_" + mode + "_ticket_milli" }
func RakeKey(mode, destination string) string {
	return "game_likes_" + mode + "_rake_" + destination + "_bp"
}

func CompileConfig(raw map[string]string) (Snapshot, error) {
	master, err := game.RawBool(raw, game.GamesEnabledKey, false)
	if err != nil {
		return Snapshot{}, err
	}
	enabled, err := game.RawBool(raw, EnabledKey, false)
	if err != nil || enabled && !master {
		return Snapshot{}, game.ErrInvalidConfig
	}
	result := Snapshot{Enabled: enabled, Modes: make(map[string]Mode, len(modeKeys))}
	for index, key := range modeKeys {
		item := Mode{}
		if item.Enabled, err = game.RawBool(raw, ModeEnabledKey(key), false); err != nil {
			return Snapshot{}, err
		}
		if item.TicketMilli, err = game.RawAmount(raw, TicketKey(key), defaultTickets[index], 1); err != nil {
			return Snapshot{}, err
		}
		if item.RakeBP.Platform, err = game.RawInt(raw, RakeKey(key, "platform"), 100, 0, 9999); err != nil {
			return Snapshot{}, err
		}
		if item.RakeBP.Welfare, err = game.RawInt(raw, RakeKey(key, "welfare"), 100, 0, 9999); err != nil {
			return Snapshot{}, err
		}
		if item.RakeBP.Thursday, err = game.RawInt(raw, RakeKey(key, "thursday"), 100, 0, 9999); err != nil {
			return Snapshot{}, err
		}
		if item.Enabled && !enabled || item.RakeBP.Platform+item.RakeBP.Welfare+item.RakeBP.Thursday >= 10000 {
			return Snapshot{}, game.ErrInvalidConfig
		}
		result.Modes[key] = item
	}
	return result, nil
}

func (snapshot Snapshot) Wire() Wire {
	result := Wire{Enabled: snapshot.Enabled, Modes: make(map[string]WireMode, len(modeKeys))}
	for _, key := range modeKeys {
		item := snapshot.Modes[key]
		result.Modes[key] = WireMode{Enabled: item.Enabled, Ticket: game.FormatAmount(item.TicketMilli), RakeBP: item.RakeBP}
	}
	return result
}

func wireRaw(wire Wire) (map[string]string, error) {
	if len(wire.Modes) != len(modeKeys) {
		return nil, game.ErrInvalidConfig
	}
	raw := map[string]string{EnabledKey: game.BoolRaw(wire.Enabled)}
	for _, key := range modeKeys {
		item, ok := wire.Modes[key]
		if !ok {
			return nil, game.ErrInvalidConfig
		}
		ticket, err := game.ParseAmount(item.Ticket)
		if err != nil || ticket < 1 {
			return nil, game.ErrInvalidConfig
		}
		raw[ModeEnabledKey(key)] = game.BoolRaw(item.Enabled)
		raw[TicketKey(key)] = strconv.FormatInt(ticket, 10)
		raw[RakeKey(key, "platform")] = strconv.Itoa(item.RakeBP.Platform)
		raw[RakeKey(key, "welfare")] = strconv.Itoa(item.RakeBP.Welfare)
		raw[RakeKey(key, "thursday")] = strconv.Itoa(item.RakeBP.Thursday)
	}
	return raw, nil
}

type Codec struct{}
type compiled struct {
	snapshot Snapshot
	raw      map[string]string
}

func (Codec) Keys() []string {
	keys := []string{EnabledKey}
	for _, mode := range modeKeys {
		keys = append(keys, ModeEnabledKey(mode), TicketKey(mode))
		for _, destination := range []string{"platform", "welfare", "thursday"} {
			keys = append(keys, RakeKey(mode, destination))
		}
	}
	return keys
}

func (Codec) ValidatePatch(body json.RawMessage) error {
	var patch Patch
	if err := game.DecodeConfigPatch(body, &patch); err != nil {
		return err
	}
	if patch.Modes != nil {
		for key := range *patch.Modes {
			if !slices.Contains(modeKeys[:], key) {
				return game.ErrInvalidConfig
			}
		}
	}
	return nil
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

func (Codec) CompileWire(body json.RawMessage, master bool) (game.ConfigValue, error) {
	var wire Wire
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

func (Codec) Merge(current game.ConfigValue, body json.RawMessage, master bool) (game.ConfigValue, error) {
	value, ok := current.(compiled)
	if !ok {
		return nil, game.ErrInvalidConfig
	}
	if err := (Codec{}).ValidatePatch(body); err != nil {
		return nil, err
	}
	var next Wire
	if err := game.MergeConfigFields(value.Wire(), body, &next); err != nil {
		return nil, err
	}
	return (Codec{}).CompileWire(game.ConfigJSON(next), master)
}

func (value compiled) Enabled() bool { return value.snapshot.Enabled }
func (value compiled) NeedsReady() bool {
	for _, item := range value.snapshot.Modes {
		if item.Enabled {
			return true
		}
	}
	return false
}
func (value compiled) Raw() map[string]string { return maps.Clone(value.raw) }
func (value compiled) Wire() json.RawMessage  { return game.ConfigJSON(value.snapshot.Wire()) }
func (value compiled) UserWire(available func(mode, spec string) bool) json.RawMessage {
	type userMode struct {
		WireMode
		Available bool `json:"available"`
	}
	wire := struct {
		Enabled           bool                `json:"enabled"`
		Available         bool                `json:"available"`
		Modes             map[string]userMode `json:"modes"`
		QueueSeconds      int                 `json:"queue_seconds"`
		PlanSeconds       int                 `json:"plan_seconds"`
		SettlementSeconds int                 `json:"settlement_seconds"`
		QueueCapacity     int                 `json:"queue_capacity"`
	}{Enabled: value.Enabled(), Modes: make(map[string]userMode), QueueSeconds: QueueSeconds, PlanSeconds: 20, SettlementSeconds: 5, QueueCapacity: QueueCapacity}
	for key, item := range value.snapshot.Wire().Modes {
		ready := available(key, "")
		wire.Available = wire.Available || ready
		item.Enabled = item.Enabled && ready
		wire.Modes[key] = userMode{WireMode: item, Available: ready}
	}
	wire.Enabled = wire.Enabled && wire.Available
	return game.ConfigJSON(wire)
}
