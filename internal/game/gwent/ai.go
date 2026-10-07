package gwent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/waiting-here/NonbiriAPI/internal/game/ai"
	"github.com/waiting-here/NonbiriAPI/internal/game/duel"
	"github.com/waiting-here/NonbiriAPI/internal/game/gwent/engine"
)

const policySchema = "gwent-local/v1"

type Policy struct {
	Schema  string `json:"schema"`
	Faction string `json:"faction"`
	Preset  string `json:"preset"`
}
type AIAdapter struct{}
type localSource struct{}
type observation struct {
	State engine.State
	Seat  int
}
type randomReader struct{ random ai.Random }

func (r randomReader) Read(body []byte) (int, error) {
	for i := range body {
		body[i] = byte(r.random.IntN(256))
	}
	return len(body), nil
}
func (localSource) ID() string { return "gwent-local" }
func (localSource) Supports(c ai.Capability) bool {
	return c.ProtocolVersion == ai.ProtocolVersion && c.Game == "gwent" && c.ObservationSchema == "gwent-seat/v1" && c.ActionSchema == ai.ChoiceSchema
}
func (s localSource) Decide(ctx context.Context, r ai.Request) (ai.Result, error) {
	if err := ctx.Err(); err != nil {
		return ai.Result{}, err
	}
	o, ok := r.Observation.(observation)
	if !ok || !s.Supports(r.Capability) || r.Random == nil {
		return ai.Result{}, ai.ErrUnsupported
	}
	a, err := engine.LocalChoice(o.State, o.Seat, randomReader{r.Random})
	if err != nil {
		return ai.Result{}, err
	}
	for _, c := range r.Actions.(ai.Choices) {
		if c.Action == a {
			return ai.Selected(r, c.ID), nil
		}
	}
	return ai.Result{}, ai.ErrInvalidResult
}
func (AIAdapter) Source() ai.Source    { return localSource{} }
func (AIAdapter) PolicySchema() string { return policySchema }
func (AIAdapter) RulesKey() string     { return "gwent/v1" }
func (AIAdapter) FeatureVersion() int  { return 1 }
func (AIAdapter) Compile(raw json.RawMessage) (any, error) {
	var p Policy
	if duel.Decode(raw, &p) != nil || p.Schema != policySchema {
		return nil, duel.ErrInvalidRequest
	}
	d, ok := engine.PresetDeck(p.Faction, p.Preset)
	if !ok || engine.ValidateDeck(d) != nil {
		return nil, duel.ErrInvalidRequest
	}
	return p, nil
}
func (AIAdapter) Presets() any {
	presets := []any{}
	for _, f := range []string{"openai", "deepseek", "claude", "gemini"} {
		presets = append(presets, struct {
			ID          string `json:"id"`
			Name        string `json:"name"`
			Description string `json:"description"`
			Policy      Policy `json:"policy"`
		}{f, f + " · 均衡部署", "原版本地评分器 · 均衡部署", Policy{policySchema, f, "standard-balanced"}})
	}
	return presets
}
func (AIAdapter) Scenarios() any { return []any{} }
func (AIAdapter) Preview(context.Context, json.RawMessage, string) (any, error) {
	return nil, duel.ErrInvalidRequest
}
func (AIAdapter) Request(raw json.RawMessage, seat int) (ai.Request, error) {
	s, err := (Rules{}).state("ai", raw)
	if err != nil || seat < 0 || seat > 1 {
		return ai.Request{}, duel.ErrInvariant
	}
	choices := ai.Choices{}
	for i, a := range s.Game.Legal(seat) {
		choices = append(choices, ai.Choice{ID: fmt.Sprint(i), Action: a})
	}
	return ai.Request{Capability: ai.Capability{ProtocolVersion: ai.ProtocolVersion, Game: "gwent", ObservationSchema: "gwent-seat/v1", ActionSchema: ai.ChoiceSchema}, Observation: observation{engine.DecisionState(s.Game, seat), seat}, Actions: choices}, nil
}
func (AIAdapter) Summarize([]duel.AISample, int64) (json.RawMessage, int, error) { return nil, 0, nil }
func (AIAdapter) Memory(json.RawMessage) (any, error)                            { return nil, nil }
func (AIAdapter) Extract([]duel.AIHistoryRound, int) (json.RawMessage, error)    { return nil, nil }
func (AIAdapter) UsesMemory() bool                                               { return false }
func (a AIAdapter) BotLoadout(raw json.RawMessage) (json.RawMessage, error) {
	v, err := a.Compile(raw)
	if err != nil {
		return nil, err
	}
	p := v.(Policy)
	d, _ := engine.PresetDeck(p.Faction, p.Preset)
	return duel.Encode(d)
}
func (Rules) AIFallback(mode string, raw json.RawMessage, seat int, random io.Reader) (json.RawMessage, error) {
	s, err := (Rules{}).state(mode, raw)
	if err != nil {
		return nil, err
	}
	a, err := engine.LocalChoice(s.Game, seat, random)
	if err != nil {
		return nil, err
	}
	return duel.Encode(a)
}
