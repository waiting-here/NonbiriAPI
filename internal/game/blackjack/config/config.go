package config

import (
	"encoding/json"
	"maps"
	"strconv"

	"github.com/waiting-here/NonbiriAPI/internal/game"
)

const (
	ID            = "blackjack"
	Version       = 1
	EnabledKey    = "game_blackjack_enabled"
	QueueCapacity = 4096
	MaxStakeMilli = game.MaxMoneyMilli / 64
)

type Rates struct {
	Platform int `json:"platform"`
	Welfare  int `json:"welfare"`
	Thursday int `json:"thursday"`
}

func (r Rates) Valid() bool {
	return r.Platform >= 0 && r.Welfare >= 0 && r.Thursday >= 0 && r.Platform < 10000 && r.Welfare < 10000 && r.Thursday < 10000 && r.Platform+r.Welfare+r.Thursday < 10000
}

type Snapshot struct {
	Enabled                                     bool
	MinStake, MaxStake, StakeStep, DefaultStake int64
	Rake                                        Rates
}

func (s Snapshot) Accepts(stake int64) bool {
	return s.StakeStep > 0 && stake >= s.MinStake && stake <= s.MaxStake && (stake-s.MinStake)%s.StakeStep == 0
}

type Wire struct {
	Enabled      bool   `json:"enabled"`
	MinStake     string `json:"min_stake"`
	MaxStake     string `json:"max_stake"`
	StakeStep    string `json:"stake_step"`
	DefaultStake string `json:"default_stake"`
	Rake         Rates  `json:"rake_bp"`
}

func (s Snapshot) Wire() Wire {
	return Wire{s.Enabled, game.FormatAmount(s.MinStake), game.FormatAmount(s.MaxStake), game.FormatAmount(s.StakeStep), game.FormatAmount(s.DefaultStake), s.Rake}
}

type Patch struct {
	Enabled      *bool   `json:"enabled,omitempty"`
	MinStake     *string `json:"min_stake,omitempty"`
	MaxStake     *string `json:"max_stake,omitempty"`
	StakeStep    *string `json:"stake_step,omitempty"`
	DefaultStake *string `json:"default_stake,omitempty"`
	Rake         *struct {
		Platform *int `json:"platform,omitempty"`
		Welfare  *int `json:"welfare,omitempty"`
		Thursday *int `json:"thursday,omitempty"`
	} `json:"rake_bp,omitempty"`
}

func AmountKey(field string) string { return "game_blackjack_" + field + "_milli" }
func RakeKey(field string) string   { return "game_blackjack_rake_" + field + "_bp" }

func CompileConfig(raw map[string]string) (Snapshot, error) {
	s := Snapshot{}
	master, err := game.RawBool(raw, game.GamesEnabledKey, false)
	if err != nil {
		return s, err
	}
	if s.Enabled, err = game.RawBool(raw, EnabledKey, false); err != nil || s.Enabled && !master {
		return Snapshot{}, game.ErrInvalidConfig
	}
	for _, item := range []struct {
		name     string
		target   *int64
		fallback int64
	}{
		{"min_stake", &s.MinStake, 1_000_000}, {"max_stake", &s.MaxStake, 50_000_000},
		{"stake_step", &s.StakeStep, 1_000_000}, {"default_stake", &s.DefaultStake, 5_000_000},
	} {
		if *item.target, err = game.RawAmount(raw, AmountKey(item.name), item.fallback, 1); err != nil || *item.target > MaxStakeMilli {
			return Snapshot{}, game.ErrInvalidConfig
		}
	}
	for _, item := range []struct {
		name   string
		target *int
	}{{"platform", &s.Rake.Platform}, {"welfare", &s.Rake.Welfare}, {"thursday", &s.Rake.Thursday}} {
		if *item.target, err = game.RawInt(raw, RakeKey(item.name), 100, 0, 9999); err != nil {
			return Snapshot{}, err
		}
	}
	if !s.Rake.Valid() || s.MinStake > s.MaxStake || !s.Accepts(s.DefaultStake) || (s.MaxStake-s.MinStake)%s.StakeStep != 0 {
		return Snapshot{}, game.ErrInvalidConfig
	}
	return s, nil
}

func wireRaw(w Wire) (map[string]string, error) {
	raw := map[string]string{EnabledKey: game.BoolRaw(w.Enabled)}
	for name, value := range map[string]string{"min_stake": w.MinStake, "max_stake": w.MaxStake, "stake_step": w.StakeStep, "default_stake": w.DefaultStake} {
		amount, err := game.ParseAmount(value)
		if err != nil {
			return nil, err
		}
		raw[AmountKey(name)] = strconv.FormatInt(amount, 10)
	}
	for name, value := range map[string]int{"platform": w.Rake.Platform, "welfare": w.Rake.Welfare, "thursday": w.Rake.Thursday} {
		raw[RakeKey(name)] = strconv.Itoa(value)
	}
	return raw, nil
}

type Codec struct{}
type compiled struct {
	snapshot Snapshot
	raw      map[string]string
}

func (Codec) Keys() []string {
	return []string{EnabledKey, AmountKey("min_stake"), AmountKey("max_stake"), AmountKey("stake_step"), AmountKey("default_stake"), RakeKey("platform"), RakeKey("welfare"), RakeKey("thursday")}
}
func (Codec) ValidatePatch(body json.RawMessage) error {
	var p Patch
	return game.DecodeConfigPatch(body, &p)
}
func (Codec) Compile(raw map[string]string) (game.ConfigValue, error) {
	s, err := CompileConfig(raw)
	if err != nil {
		return nil, err
	}
	canonical, err := wireRaw(s.Wire())
	if err != nil {
		return nil, err
	}
	return compiled{s, canonical}, nil
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
func (c compiled) Enabled() bool          { return c.snapshot.Enabled }
func (c compiled) NeedsReady() bool       { return c.snapshot.Enabled }
func (c compiled) Raw() map[string]string { return maps.Clone(c.raw) }
func (c compiled) Wire() json.RawMessage  { return game.ConfigJSON(c.snapshot.Wire()) }
func (c compiled) UserWire(available func(string, string) bool) json.RawMessage {
	w := struct {
		Wire
		Available       bool `json:"available"`
		QueueCapacity   int  `json:"queue_capacity"`
		Seats           int  `json:"seats"`
		SeatingSeconds  int  `json:"seating_seconds"`
		DecisionSeconds int  `json:"decision_seconds"`
		RoundSeconds    int  `json:"round_seconds"`
	}{c.snapshot.Wire(), available("table", ""), QueueCapacity, 8, 15, 30, 60}
	w.Enabled = w.Enabled && w.Available
	return game.ConfigJSON(w)
}
