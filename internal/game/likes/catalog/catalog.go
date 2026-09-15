// Package catalog contains the fixed rules and descriptions for the likes game.
package catalog

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
)

const DesignVersion = "0.17.0"
const SchemaVersion = 15
const RulesVersion = 1

//go:embed quick.json standard.json
var presets embed.FS

var ErrCatalog = errors.New("likes: invalid catalog")

type Cost struct {
	Energy  int64  `json:"energy"`
	Token   int64  `json:"token"`
	Payment string `json:"payment"`
	Gold    int64  `json:"gold,omitempty"`
}

type Effect struct {
	Kind          string `json:"kind"`
	Likes         int64  `json:"likes"`
	P             int64  `json:"p"`
	Q             int64  `json:"q"`
	N             int64  `json:"n"`
	BuffID        string `json:"buffId,omitempty"`
	ExtraBuffID   string `json:"extraBuffId,omitempty"`
	CacheTarget   string `json:"cacheTarget,omitempty"`
	Meme          string `json:"meme,omitempty"`
	Combo         int64  `json:"combo,omitempty"`
	RandomTargets bool   `json:"randomTargets,omitempty"`
	AuditTarget   string `json:"auditTarget,omitempty"`
}

type Effects struct {
	Base Effect `json:"base"`
	I    Effect `json:"I"`
	II   Effect `json:"II"`
}

type Skill struct {
	ID            string           `json:"id"`
	Owner         string           `json:"owner"`
	Kind          string           `json:"kind"`
	Name          string           `json:"name"`
	Energy        int64            `json:"energy"`
	Token         int64            `json:"token"`
	Payment       string           `json:"payment"`
	Image         int64            `json:"image"`
	MaxUses       *int64           `json:"maxUses"`
	Learn         int64            `json:"learn"`
	Copyable      bool             `json:"copyable"`
	Stable        bool             `json:"stable"`
	Meme          string           `json:"meme"`
	Note          string           `json:"note"`
	Effects       Effects          `json:"effects"`
	ResourceCosts map[string]int64 `json:"resourceCosts"`
	Gold          int64            `json:"gold,omitempty"`
}

func (skill Skill) Cost() Cost {
	return Cost{Energy: skill.Energy, Token: skill.Token, Payment: skill.Payment, Gold: skill.Gold}
}

type Buff struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Kind           string `json:"kind"`
	Category       string `json:"category,omitempty"`
	Source         string `json:"source,omitempty"`
	P              int64  `json:"p"`
	Q              int64  `json:"q"`
	N              int64  `json:"n"`
	Cap            int64  `json:"cap"`
	Trigger        string `json:"trigger"`
	Expiry         string `json:"expiry"`
	Overwrite      string `json:"overwrite"`
	Target         string `json:"target"`
	Description    string `json:"description,omitempty"`
	Meme           string `json:"meme,omitempty"`
	StackGroup     string `json:"stackGroup,omitempty"`
	Reapply        string `json:"reapply,omitempty"`
	CacheScope     string `json:"cacheScope,omitempty"`
	RefreshOnCombo bool   `json:"refreshOnCombo,omitempty"`
}

type Allocation struct {
	Initial int64 `json:"initial"`
	Cap     int64 `json:"cap"`
}
type Resource struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Unit         string `json:"unit"`
	Description  string `json:"description"`
	Meme         string `json:"meme"`
	Subscription bool   `json:"subscription,omitempty"`
	Upgrade      int64  `json:"upgrade,omitempty"`
}
type Role struct {
	ID         string                `json:"id"`
	Name       string                `json:"name"`
	Focus      string                `json:"focus"`
	Difficulty string                `json:"difficulty"`
	Note       string                `json:"note"`
	Weakness   string                `json:"weakness"`
	Overrides  map[string]int64      `json:"overrides"`
	Resources  map[string]Allocation `json:"resources"`
}
type Loadout struct {
	ID     string   `json:"id"`
	Role   string   `json:"role"`
	Name   string   `json:"name"`
	Skills []string `json:"skills"`
	Note   string   `json:"note"`
}
type Passive struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Kind        string `json:"kind"`
	P           int64  `json:"p"`
	Q           int64  `json:"q"`
	BuffID      string `json:"buffId,omitempty"`
	Description string `json:"description"`
	Meme        string `json:"meme"`
}
type Harness struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	ActiveSlots int      `json:"activeSlots"`
	Passives    []string `json:"passives"`
	Description string   `json:"description"`
	Meme        string   `json:"meme"`
}
type ParamMeta struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Unit string `json:"unit"`
	Note string `json:"note"`
}
type Rules struct {
	CacheWindow   string `json:"cacheWindow"`
	UniqueSamples bool   `json:"uniqueSamples"`
	StrictSamples bool   `json:"strictSamples"`
	ImageShortage string `json:"imageShortage"`
}
type Config struct {
	SchemaVersion int              `json:"schemaVersion"`
	Mode          string           `json:"mode"`
	Name          string           `json:"name"`
	Parameters    map[string]int64 `json:"parameters"`
	ParamMeta     []ParamMeta      `json:"paramMeta"`
	Rules         Rules            `json:"rules"`
	Roles         []Role           `json:"roles"`
	Skills        []Skill          `json:"skills"`
	Buffs         []Buff           `json:"buffs"`
	Loadouts      []Loadout        `json:"loadouts"`
	Resources     []Resource       `json:"resources"`
	Harnesses     []Harness        `json:"harnesses"`
	Passives      []Passive        `json:"passives"`
}

// Load returns a fresh snapshot; caller mutations never change another game.
func Load(mode string) (Config, string, error) {
	if mode != "quick" && mode != "standard" {
		return Config{}, "", ErrCatalog
	}
	body, err := presets.ReadFile(mode + ".json")
	if err != nil {
		return Config{}, "", err
	}
	var config Config
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&config); err != nil {
		return Config{}, "", fmt.Errorf("%w: %v", ErrCatalog, err)
	}
	if config.Mode != mode {
		return Config{}, "", ErrCatalog
	}
	if err := config.Validate(); err != nil {
		return Config{}, "", err
	}
	// Pin the shared-energy exception, settlement split and manual casting rule.
	hash := sha256.New()
	hash.Write([]byte("likes@1;positive-energy-overload;separate-round-start;manual-main-unless-stunned;overload-state\n"))
	hash.Write(body)
	return config, hex.EncodeToString(hash.Sum(nil)), nil
}

var timedKinds = []string{"AMPLIFY", "SUPPRESS", "TOKEN_TAX", "NONBASIC_TAX", "SAVE_ENERGY", "API_DISCOUNT", "REGULATOR"}

// Validate checks the fixed content's references and the termination assumptions
// that keep a complete round and its event history within the service bounds.
func (c Config) Validate() error {
	if c.SchemaVersion != SchemaVersion || len(c.Roles) != 5 || len(c.Skills) != 48 || len(c.Buffs) != 46 || len(c.Resources) != 1 || len(c.Harnesses) != 8 || len(c.Passives) != 8 {
		return ErrCatalog
	}
	if c.Mode != "quick" && c.Mode != "standard" || c.Rules != (Rules{CacheWindow: "round", UniqueSamples: true, StrictSamples: true, ImageShortage: "illegal"}) {
		return ErrCatalog
	}
	for _, value := range c.Parameters {
		if value < 0 || value > 1_000_000 {
			return ErrCatalog
		}
	}
	if c.Parameters["PREP_MAX"] != 2 || c.Parameters["INSERT_CAP"] != 1 || c.Parameters["POWER_GENERATION"] != 10 || c.Parameters["TURN_SECONDS"] != 20 || c.Parameters["TOKEN_FLOOR"] != 1 || c.Parameters["ENERGY_FLOOR"] != 1 {
		return ErrCatalog
	}
	if c.Parameters["ENERGY_START"] > c.Parameters["ENERGY_CAP"] || c.Parameters["SUB_START"] != c.Parameters["BURST_CAP"]*2 || c.Parameters["SUB_TOTAL_UPGRADE"] != c.Parameters["SUB_BURST_UPGRADE"]*2 {
		return ErrCatalog
	}
	if c.Mode == "quick" && (c.Parameters["MAX_ROUNDS"] != 25 || c.Parameters["TARGET_LIKES"] != 60) || c.Mode == "standard" && (c.Parameters["MAX_ROUNDS"] != 75 || c.Parameters["TARGET_LIKES"] != 300) {
		return ErrCatalog
	}
	roles, buffs, skills, passives := map[string]Role{}, map[string]Buff{}, map[string]Skill{}, map[string]Passive{}
	for _, role := range c.Roles {
		if _, exists := roles[role.ID]; exists || !slices.Contains([]string{"ChatGPT", "Claude", "Gemini", "GLM", "DeepSeek"}, role.ID) {
			return ErrCatalog
		}
		roles[role.ID] = role
	}
	states := 0
	for _, buff := range c.Buffs {
		if _, exists := buffs[buff.ID]; exists || buff.ID == "" {
			return ErrCatalog
		}
		buffs[buff.ID] = buff
		for _, value := range []int64{buff.P, buff.Q, buff.N, buff.Cap} {
			if value < 0 || value > 1_000 {
				return ErrCatalog
			}
		}
		if buff.Category == "state" {
			states++
			if buff.Kind != "OVERLOAD" && buff.Kind != "SPEED_MODE" {
				return ErrCatalog
			}
		}
		if buff.Kind == "COMBO" && (buff.P != 3 || buff.Q != 1) {
			return ErrCatalog
		}
		if buff.Kind == "CACHE" && buff.Cap != 3 {
			return ErrCatalog
		}
		if buff.Reapply == "add" && slices.Contains(timedKinds, buff.Kind) && buff.Cap != 4 {
			return ErrCatalog
		}
	}
	if states != 2 || c.Resources[0].ID != "R_IMAGE" || !c.Resources[0].Subscription {
		return ErrCatalog
	}
	variants := 0
	for _, sk := range c.Skills {
		if _, exists := skills[sk.ID]; exists || sk.ID == "" {
			return ErrCatalog
		}
		skills[sk.ID] = sk
		if sk.Owner != "全局公共" {
			if _, exists := roles[sk.Owner]; !exists {
				return ErrCatalog
			}
		}
		if !slices.Contains([]string{"basic", "normal", "special", "ultimate"}, sk.Kind) || !slices.Contains([]string{"mix", "api", "sub"}, sk.Payment) {
			return ErrCatalog
		}
		for _, value := range []int64{sk.Energy, sk.Token, sk.Image, sk.Learn, sk.Gold} {
			if value < 0 || value > 1_000_000 {
				return ErrCatalog
			}
		}
		if sk.MaxUses != nil && (*sk.MaxUses < 0 || *sk.MaxUses > 100) {
			return ErrCatalog
		}
		for key, value := range sk.ResourceCosts {
			if key != "R_IMAGE" || value < 0 || value > 2 {
				return ErrCatalog
			}
		}
		variants++
		if sk.Copyable {
			variants += 2
		}
		for _, effect := range []Effect{sk.Effects.Base, sk.Effects.I, sk.Effects.II} {
			for _, value := range []int64{effect.Likes, effect.P, effect.Q, effect.N, effect.Combo} {
				if value < 0 || value > 1_000 {
					return ErrCatalog
				}
			}
			for _, key := range []string{effect.BuffID, effect.ExtraBuffID} {
				if key != "" {
					if _, exists := buffs[key]; !exists {
						return ErrCatalog
					}
				}
			}
			if effect.Kind == "INSERT" && effect.P != 1 {
				return ErrCatalog
			}
			progress := effect.Combo
			if effect.Kind == "COMBO" {
				progress += effect.P
			}
			if effect.ExtraBuffID != "" && effect.N > 0 && buffs[effect.ExtraBuffID].Kind == "COMBO" {
				progress++
			}
			if progress > 4 || effect.Kind == "CACHE_CONVERT" && progress != 0 {
				return ErrCatalog
			}
		}
	}
	if variants != 130 {
		return ErrCatalog
	}
	flash, distill := skills["GEM01"], skills["PUB41"]
	if flash.MaxUses != nil || flash.Effects.Base.Kind != "CACHE_COMBO" || flash.Effects.Base.N != 1 || flash.Effects.Base.ExtraBuffID == "" || buffs[flash.Effects.Base.ExtraBuffID].Kind != "COMBO" || flash.Effects.Base.Combo != 0 {
		return ErrCatalog
	}
	if distill.Copyable || distill.MaxUses != nil || distill.Payment != "api" || distill.Energy != 25 || distill.Token != 200 {
		return ErrCatalog
	}
	for _, sk := range c.Skills {
		for _, e := range []Effect{sk.Effects.Base, sk.Effects.I, sk.Effects.II} {
			if e.CacheTarget != "" {
				if _, exists := skills[e.CacheTarget]; !exists {
					return ErrCatalog
				}
			}
		}
	}
	for _, item := range c.Passives {
		if _, exists := passives[item.ID]; exists {
			return ErrCatalog
		}
		passives[item.ID] = item
		if item.BuffID != "" {
			if _, exists := buffs[item.BuffID]; !exists {
				return ErrCatalog
			}
		}
	}
	seen := map[string]bool{}
	for _, item := range c.Harnesses {
		if seen[item.ID] || item.ActiveSlots < 0 || item.ActiveSlots > 2 {
			return ErrCatalog
		}
		seen[item.ID] = true
		for _, key := range item.Passives {
			if _, exists := passives[key]; !exists {
				return ErrCatalog
			}
		}
	}
	return nil
}
