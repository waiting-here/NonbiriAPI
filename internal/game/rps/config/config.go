package config

import (
	"encoding/json"
	"fmt"
	"maps"
	"strconv"

	"github.com/waiting-here/NonbiriAPI/internal/game"
)

const (
	RPSEnabledKey         = "game_rps_enabled"
	RPSQueueCapacity      = 4096
	RPSStandardMultiplier = 5
	RPSFreeTieReminder    = 3
	RPSFreeTieLimit       = 6
)

type PumpsBP struct {
	Platform int `json:"platform"`
	Welfare  int `json:"welfare"`
	Thursday int `json:"thursday"`
}

type RPSModeConfig struct {
	Enabled         bool
	BaseMilli       int64
	PumpsBP         PumpsBP
	QueueSeconds    int
	GestureSeconds  int
	DealerSeconds   int
	FollowerSeconds int
}

type RPSConfig struct {
	Enabled bool
	Modes   map[string]RPSModeConfig
}

type RPSWireConfig struct {
	Enabled bool                   `json:"enabled"`
	Modes   map[string]RPSWireMode `json:"modes"`
}
type RPSWireMode struct {
	Enabled         bool    `json:"enabled"`
	Base            string  `json:"base"`
	PumpsBP         PumpsBP `json:"pumps_bp"`
	QueueSeconds    int     `json:"queue_seconds"`
	GestureSeconds  int     `json:"gesture_seconds"`
	DealerSeconds   int     `json:"dealer_seconds"`
	FollowerSeconds int     `json:"follower_seconds"`
	QueueCapacity   int     `json:"queue_capacity"`
}

type RPSConfigPatch struct {
	Enabled *bool                    `json:"enabled,omitempty"`
	Modes   *map[string]RPSModePatch `json:"modes,omitempty"`
}
type RPSModePatch struct {
	Enabled         *bool         `json:"enabled,omitempty"`
	Base            *string       `json:"base,omitempty"`
	PumpsBP         *PumpsBPPatch `json:"pumps_bp,omitempty"`
	QueueSeconds    *int          `json:"queue_seconds,omitempty"`
	GestureSeconds  *int          `json:"gesture_seconds,omitempty"`
	DealerSeconds   *int          `json:"dealer_seconds,omitempty"`
	FollowerSeconds *int          `json:"follower_seconds,omitempty"`
}
type PumpsBPPatch struct {
	Platform *int `json:"platform,omitempty"`
	Welfare  *int `json:"welfare,omitempty"`
	Thursday *int `json:"thursday,omitempty"`
}

type Snapshot struct {
	GamesEnabled bool
	RPS          RPSConfig
}

func CompileConfig(raw map[string]string) (Snapshot, error) {
	master, err := game.RawBool(raw, game.GamesEnabledKey, false)
	if err != nil {
		return Snapshot{}, err
	}
	rpsEnabled, err := game.RawBool(raw, RPSEnabledKey, false)
	if err != nil || rpsEnabled && !master {
		return Snapshot{}, game.ErrInvalidConfig
	}
	rps := RPSConfig{Enabled: rpsEnabled, Modes: make(map[string]RPSModeConfig, 3)}
	for _, mode := range rpsModes {
		enabled, parseErr := game.RawBool(raw, RPSModeEnabledKey(mode), false)
		if parseErr != nil {
			return Snapshot{}, parseErr
		}
		base, parseErr := game.RawAmount(raw, RPSModeBaseKey(mode), 0, 0)
		if parseErr != nil {
			return Snapshot{}, parseErr
		}
		modeConfig := RPSModeConfig{Enabled: enabled, BaseMilli: base}
		modeConfig.PumpsBP.Platform, parseErr = game.RawInt(raw, RPSModeBPKey(mode, "platform"), 100, 0, 9999)
		if parseErr != nil {
			return Snapshot{}, parseErr
		}
		modeConfig.PumpsBP.Welfare, parseErr = game.RawInt(raw, RPSModeBPKey(mode, "welfare"), 100, 0, 9999)
		if parseErr != nil {
			return Snapshot{}, parseErr
		}
		modeConfig.PumpsBP.Thursday, parseErr = game.RawInt(raw, RPSModeBPKey(mode, "thursday"), 100, 0, 9999)
		if parseErr != nil {
			return Snapshot{}, parseErr
		}
		modeConfig.QueueSeconds, parseErr = game.RawInt(raw, RPSModeTimeKey(mode, "queue"), 120, 30, 120)
		if parseErr != nil {
			return Snapshot{}, parseErr
		}
		modeConfig.GestureSeconds, parseErr = game.RawInt(raw, RPSModeTimeKey(mode, "gesture"), 20, 5, 20)
		if parseErr != nil {
			return Snapshot{}, parseErr
		}
		modeConfig.DealerSeconds, parseErr = game.RawInt(raw, RPSModeTimeKey(mode, "dealer"), 15, 5, 15)
		if parseErr != nil {
			return Snapshot{}, parseErr
		}
		modeConfig.FollowerSeconds, parseErr = game.RawInt(raw, RPSModeTimeKey(mode, "follower"), 15, 5, 15)
		if parseErr != nil {
			return Snapshot{}, parseErr
		}
		if modeConfig.PumpsBP.Platform+modeConfig.PumpsBP.Welfare+modeConfig.PumpsBP.Thursday >= 10000 {
			return Snapshot{}, fmt.Errorf("%w: RPS basis points", game.ErrInvalidConfig)
		}
		if enabled && (!rpsEnabled || base == 0) {
			return Snapshot{}, fmt.Errorf("%w: enabled RPS mode", game.ErrInvalidConfig)
		}
		if mode == game.RPSModeStandard && base > game.MaxMoneyMilli/RPSStandardMultiplier {
			return Snapshot{}, fmt.Errorf("%w: RPS standard 5B bound", game.ErrInvalidConfig)
		}
		rps.Modes[mode] = modeConfig
	}
	return Snapshot{GamesEnabled: master, RPS: rps}, nil
}

var rpsModes = [...]string{game.RPSModeQuick, game.RPSModeStandard, game.RPSModeDeathmatch}

func RPSModeEnabledKey(mode string) string     { return "game_rps_" + mode + "_enabled" }
func RPSModeBaseKey(mode string) string        { return "game_rps_" + mode + "_b_milli" }
func RPSModeBPKey(mode, cut string) string     { return "game_rps_" + mode + "_" + cut + "_bp" }
func RPSModeTimeKey(mode, phase string) string { return "game_rps_" + mode + "_" + phase + "_seconds" }
func anyModeEnabled(snapshot Snapshot) bool {
	for _, mode := range snapshot.RPS.Modes {
		if mode.Enabled {
			return true
		}
	}
	return false
}

func (snapshot Snapshot) Wire() RPSWireConfig {
	var result RPSWireConfig
	result = RPSWireConfig{Enabled: snapshot.RPS.Enabled, Modes: make(map[string]RPSWireMode, 3)}
	for _, mode := range rpsModes {
		value := snapshot.RPS.Modes[mode]
		result.Modes[mode] = RPSWireMode{Enabled: value.Enabled, Base: game.FormatAmount(value.BaseMilli), PumpsBP: value.PumpsBP, QueueSeconds: value.QueueSeconds, GestureSeconds: value.GestureSeconds, DealerSeconds: value.DealerSeconds, FollowerSeconds: value.FollowerSeconds, QueueCapacity: RPSQueueCapacity}
	}
	return result
}
func wireRaw(config RPSWireConfig) (map[string]string, error) {
	raw := map[string]string{}
	setBool := func(key string, value bool) { raw[key] = game.BoolRaw(value) }
	setBool(RPSEnabledKey, config.Enabled)
	if len(config.Modes) != 3 {
		return nil, fmt.Errorf("%w: RPS modes", game.ErrInvalidConfig)
	}
	for _, mode := range rpsModes {
		value, ok := config.Modes[mode]
		if !ok || value.QueueCapacity != RPSQueueCapacity {
			return nil, fmt.Errorf("%w: RPS mode", game.ErrInvalidConfig)
		}
		setBool(RPSModeEnabledKey(mode), value.Enabled)
		milli, err := game.ParseAmount(value.Base)
		if err != nil {
			return nil, err
		}
		raw[RPSModeBaseKey(mode)] = strconv.FormatInt(milli, 10)
		raw[RPSModeBPKey(mode, "platform")] = strconv.Itoa(value.PumpsBP.Platform)
		raw[RPSModeBPKey(mode, "welfare")] = strconv.Itoa(value.PumpsBP.Welfare)
		raw[RPSModeBPKey(mode, "thursday")] = strconv.Itoa(value.PumpsBP.Thursday)
		raw[RPSModeTimeKey(mode, "queue")] = strconv.Itoa(value.QueueSeconds)
		raw[RPSModeTimeKey(mode, "gesture")] = strconv.Itoa(value.GestureSeconds)
		raw[RPSModeTimeKey(mode, "dealer")] = strconv.Itoa(value.DealerSeconds)
		raw[RPSModeTimeKey(mode, "follower")] = strconv.Itoa(value.FollowerSeconds)
	}
	return raw, nil
}

type Codec struct{}
type compiled struct {
	snapshot Snapshot
	raw      map[string]string
}

func (Codec) Keys() []string {
	keys := []string{RPSEnabledKey}
	for _, mode := range rpsModes {
		keys = append(keys, RPSModeEnabledKey(mode), RPSModeBaseKey(mode))
		for _, cut := range []string{"platform", "welfare", "thursday"} {
			keys = append(keys, RPSModeBPKey(mode, cut))
		}
		for _, phase := range []string{"queue", "gesture", "dealer", "follower"} {
			keys = append(keys, RPSModeTimeKey(mode, phase))
		}
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
	var patch RPSConfigPatch
	if err := game.DecodeConfigPatch(body, &patch); err != nil {
		return nil, err
	}
	var next RPSWireConfig
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
func (value compiled) Enabled() bool          { return value.snapshot.RPS.Enabled }
func (value compiled) NeedsReady() bool       { return anyModeEnabled(value.snapshot) }
func (value compiled) Raw() map[string]string { return maps.Clone(value.raw) }
func (value compiled) Wire() json.RawMessage  { return game.ConfigJSON(value.snapshot.Wire()) }
func (value compiled) UserWire(available func(mode, spec string) bool) json.RawMessage {
	wire := value.snapshot.Wire()
	ready := false
	for _, mode := range rpsModes {
		item := wire.Modes[mode]
		supported := available(mode, "")
		ready = ready || supported
		item.Enabled = item.Enabled && supported
		wire.Modes[mode] = item
	}
	wire.Enabled = wire.Enabled && ready
	return game.ConfigJSON(wire)
}
