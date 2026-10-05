package bidding

import (
	"context"
	"encoding/json"

	"github.com/waiting-here/NonbiriAPI/internal/game/ai"
	"github.com/waiting-here/NonbiriAPI/internal/game/bidding/engine"
	"github.com/waiting-here/NonbiriAPI/internal/game/bidding/strategy"
	"github.com/waiting-here/NonbiriAPI/internal/game/duel"
)

// AIAdapter projects bidding rules into the source-independent decision protocol.
type AIAdapter struct{}

func (AIAdapter) Source() ai.Source                        { return strategy.Source{} }
func (AIAdapter) PolicySchema() string                     { return strategy.PolicySchema }
func (AIAdapter) RulesKey() string                         { return "bidding/v1" }
func (AIAdapter) FeatureVersion() int                      { return strategy.FeatureVersion }
func (AIAdapter) Compile(raw json.RawMessage) (any, error) { return strategy.Decode(raw) }
func (AIAdapter) Presets() any                             { return strategy.Presets() }
func (AIAdapter) Scenarios() any                           { return strategy.Scenarios() }
func (AIAdapter) Preview(ctx context.Context, raw json.RawMessage, id string) (any, error) {
	p, err := strategy.Decode(raw)
	if err != nil {
		return nil, duel.ErrInvalidRequest
	}
	for _, scenario := range strategy.Scenarios() {
		if scenario.ID == id {
			return strategy.Analyze(ctx, scenario.Observation, p, nil)
		}
	}
	return nil, duel.ErrInvalidRequest
}
func (AIAdapter) Request(raw json.RawMessage, seat int) (ai.Request, error) {
	var state engine.State
	if err := duel.Decode(raw, &state); err != nil {
		return ai.Request{}, err
	}
	return strategy.Request(state, seat)
}
func (AIAdapter) Summarize(samples []duel.AISample, now int64) (json.RawMessage, int, error) {
	valid := make([]strategy.Sample, 0, len(samples))
	for _, sample := range samples {
		var features strategy.Features
		if err := json.Unmarshal(sample.Features, &features); err != nil || !features.Valid() {
			return nil, 0, duel.ErrInvariant
		}
		valid = append(valid, strategy.Sample{At: sample.At, Features: features})
	}
	memory := strategy.Summarize(valid, now)
	raw, err := json.Marshal(memory)
	return raw, memory.Matches, err
}
func (AIAdapter) Memory(raw json.RawMessage) (any, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var memory strategy.Summary
	if err := json.Unmarshal(raw, &memory); err != nil || memory.Version != strategy.FeatureVersion {
		return nil, duel.ErrInvariant
	}
	return &memory, nil
}
func (AIAdapter) Extract(rounds []duel.AIHistoryRound, seat int) (json.RawMessage, error) {
	var features strategy.Features
	for _, round := range rounds {
		if round.Sources[seat] != "human" {
			continue
		}
		var state engine.State
		var action strategy.Action
		if json.Unmarshal(round.Before, &state) != nil || json.Unmarshal(round.Actions[seat], &action) != nil || action.Kind != "bid" || action.Card == nil {
			return nil, duel.ErrInvariant
		}
		request, err := strategy.Request(state, seat)
		if err != nil {
			return nil, err
		}
		features.AddHumanBid(request.Observation.(strategy.Observation), *action.Card)
	}
	if !features.Valid() {
		return nil, nil
	}
	return json.Marshal(features)
}
