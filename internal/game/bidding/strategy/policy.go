// Package strategy implements bounded, visible-information bidding policies.
package strategy

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"slices"
)

const PolicySchema = "bidding-local/v1"
const SourceID = "bidding-local"
const ObservationSchema = "bidding-visible/v1"
const FeatureVersion = 1

var ErrPolicy = errors.New("bidding strategy: invalid policy")
var ErrObservation = errors.New("bidding strategy: invalid observation")

type Parameters struct {
	HandValue    float64 `json:"hand_value"`
	Urgency      float64 `json:"urgency"`
	Exploration  float64 `json:"exploration"`
	MemoryWeight float64 `json:"memory_weight"`
	JokerCost    float64 `json:"joker_cost"`
	EndgameGuard bool    `json:"endgame_guard"`
}
type Overrides struct {
	HandValue    *float64 `json:"hand_value,omitempty"`
	Urgency      *float64 `json:"urgency,omitempty"`
	Exploration  *float64 `json:"exploration,omitempty"`
	MemoryWeight *float64 `json:"memory_weight,omitempty"`
	JokerCost    *float64 `json:"joker_cost,omitempty"`
	EndgameGuard *bool    `json:"endgame_guard,omitempty"`
}
type Condition struct {
	Field    string  `json:"field"`
	Operator string  `json:"operator"`
	Value    float64 `json:"value"`
}
type Rule struct {
	Match      string      `json:"match"`
	Conditions []Condition `json:"conditions"`
	Override   Overrides   `json:"override"`
	Filter     string      `json:"filter"`
}
type Policy struct {
	Schema     string     `json:"schema"`
	Parameters Parameters `json:"parameters"`
	Rules      []Rule     `json:"rules"`
}
type compiledRule struct {
	match      string
	conditions []Condition
	parameters Parameters
	filter     string
}
type Compiled struct {
	base  Parameters
	rules []compiledRule
}
type Preset struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Policy      Policy `json:"policy"`
}

func Decode(raw []byte) (*Compiled, error) {
	if len(raw) > 16384 {
		return nil, ErrPolicy
	}
	var policy Policy
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&policy) != nil {
		return nil, ErrPolicy
	}
	if d.Decode(new(any)) != io.EOF {
		return nil, ErrPolicy
	}
	return Compile(policy)
}

func validParameters(p Parameters) bool {
	return p.HandValue >= 0 && p.HandValue <= 1.5 && p.Urgency >= 0 && p.Urgency <= 1 && p.Exploration >= 0 && p.Exploration <= 0.15 && p.MemoryWeight >= 0 && p.MemoryWeight <= 0.85 && p.JokerCost >= 0 && p.JokerCost <= 1
}
func apply(p Parameters, o Overrides) Parameters {
	if o.HandValue != nil {
		p.HandValue = *o.HandValue
	}
	if o.Urgency != nil {
		p.Urgency = *o.Urgency
	}
	if o.Exploration != nil {
		p.Exploration = *o.Exploration
	}
	if o.MemoryWeight != nil {
		p.MemoryWeight = *o.MemoryWeight
	}
	if o.JokerCost != nil {
		p.JokerCost = *o.JokerCost
	}
	if o.EndgameGuard != nil {
		p.EndgameGuard = *o.EndgameGuard
	}
	return p
}

func Compile(policy Policy) (*Compiled, error) {
	if policy.Schema != PolicySchema || !validParameters(policy.Parameters) || len(policy.Rules) > 8 {
		return nil, ErrPolicy
	}
	compiled := &Compiled{base: policy.Parameters}
	for _, rule := range policy.Rules {
		if !slices.Contains([]string{"all", "any"}, rule.Match) || len(rule.Conditions) < 1 || len(rule.Conditions) > 4 || !slices.Contains([]string{"", "lower_half", "upper_half", "non_losing"}, rule.Filter) {
			return nil, ErrPolicy
		}
		params := apply(policy.Parameters, rule.Override)
		if !validParameters(params) {
			return nil, ErrPolicy
		}
		for _, condition := range rule.Conditions {
			bounds, ok := map[string][2]float64{"phase": {0, 1}, "round": {1, 13}, "cards": {1, 13}, "pool": {0, 208}, "lead": {-208, 208}}[condition.Field]
			if !ok || !(condition.Value >= bounds[0] && condition.Value <= bounds[1]) || !slices.Contains([]string{"lt", "lte", "eq", "gte", "gt"}, condition.Operator) {
				return nil, ErrPolicy
			}
			if condition.Field == "phase" && (condition.Operator != "eq" || condition.Value != 0 && condition.Value != 1) {
				return nil, ErrPolicy
			}
		}
		compiled.rules = append(compiled.rules, compiledRule{rule.Match, slices.Clone(rule.Conditions), params, rule.Filter})
	}
	return compiled, nil
}

func matches(c Condition, v float64) bool {
	switch c.Operator {
	case "lt":
		return v < c.Value
	case "lte":
		return v <= c.Value
	case "eq":
		return v == c.Value
	case "gte":
		return v >= c.Value
	case "gt":
		return v > c.Value
	}
	return false
}
func (p *Compiled) settings(o Observation) (Parameters, string, int) {
	fields := map[string]float64{"phase": 1, "round": float64(o.Round), "cards": float64(len(o.View.HandRemaining[o.Seat])), "pool": float64(o.View.PoolPoints), "lead": float64(o.View.Scores[o.Seat] - o.View.Scores[1-o.Seat])}
	if o.Phase == "joker" {
		fields["phase"] = 0
	}
	for index, rule := range p.rules {
		matched := rule.match == "all"
		for _, c := range rule.conditions {
			hit := matches(c, fields[c.Field])
			if rule.match == "all" {
				matched = matched && hit
			} else {
				matched = matched || hit
			}
		}
		if matched {
			return rule.parameters, rule.filter, index
		}
	}
	return p.base, "", -1
}

func Defaults() Policy {
	return Policy{Schema: PolicySchema, Parameters: Parameters{HandValue: .65, Urgency: .15, Exploration: .03, MemoryWeight: .65, JokerCost: .25, EndgameGuard: true}, Rules: []Rule{}}
}
func number(v float64) *float64 { return &v }

// Presets describe play styles, not unmeasured difficulty or win rates.
func Presets() []Preset {
	balanced, pot, patient, comeback := Defaults(), Defaults(), Defaults(), Defaults()
	pot.Parameters.HandValue = .45
	pot.Parameters.JokerCost = .12
	pot.Rules = []Rule{{Match: "all", Conditions: []Condition{{"pool", "gte", 23}}, Override: Overrides{HandValue: number(.25), Exploration: number(.01)}}}
	patient.Parameters.HandValue = .95
	patient.Parameters.JokerCost = .4
	patient.Rules = []Rule{{Match: "all", Conditions: []Condition{{"pool", "lte", 12}, {"cards", "gte", 4}}, Override: Overrides{HandValue: number(1.2)}, Filter: "lower_half"}}
	comeback.Parameters.HandValue = .6
	comeback.Parameters.Urgency = .55
	comeback.Parameters.Exploration = .02
	comeback.Rules = []Rule{{Match: "all", Conditions: []Condition{{"round", "gte", 9}, {"lead", "lt", 0}}, Override: Overrides{HandValue: number(.3), Urgency: number(.9), JokerCost: number(.05)}}}
	return []Preset{
		{"balanced", "均衡", "兼顾当前奖池与剩余手牌，残局优先保住确定胜局。", balanced},
		{"pot-first", "争池", "更重视高价值奖池，较积极使用 Joker。", pot},
		{"patient", "蓄势", "小奖池倾向保留强牌，等待更合适的竞争时机。", patient},
		{"comeback", "追分", "落后进入后半局时提高追分倾向，提前投入强牌。", comeback},
	}
}
