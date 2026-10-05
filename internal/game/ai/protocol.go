// Package ai connects game-visible decision windows to interchangeable sources.
// Games own legality, fallback actions and authoritative state transitions.
package ai

import (
	"context"
	"errors"
	"time"
)

const ProtocolVersion = 1
const ChoiceSchema = "choice/v1"

var (
	ErrUnsupported   = errors.New("ai: unsupported capability")
	ErrInvalidResult = errors.New("ai: invalid result")
	ErrCapacity      = errors.New("ai: decision capacity reached")
	ErrClosed        = errors.New("ai: closed")
	ErrSourceFailure = errors.New("ai: source failed")
)

type Capability struct {
	ProtocolVersion                       int
	Game, ObservationSchema, ActionSchema string
}

type Window struct {
	Match, Actor, Token, Phase string
}

// Payloads are native game/source types. Remote sources own serialization;
// the shared protocol has no card, player-count or score assumptions.
type Request struct {
	Capability
	DecisionID      string
	Window          Window
	RulesVersion    string
	Deadline        time.Time
	Observation     any
	Actions         any
	RuleContext     any
	Personalization any
	Policy          any
	Random          Random
}

type Random interface {
	Float64() float64
	IntN(int) int
}

type Choice struct {
	ID     string
	Action any
}
type Choices []Choice
type Selection struct{ CandidateID string }

// Action carries a schema-specific value; future parameterized actions need
// no change to this envelope or to game-neutral scheduling.
type Result struct {
	ProtocolVersion int
	DecisionID      string
	ActionSchema    string
	Action          any
}

type Source interface {
	ID() string
	Supports(Capability) bool
	Decide(context.Context, Request) (Result, error)
}

func Selected(request Request, id string) Result {
	return Result{ProtocolVersion: ProtocolVersion, DecisionID: request.DecisionID, ActionSchema: ChoiceSchema, Action: Selection{CandidateID: id}}
}

func ResolveChoice(request Request, result Result) (any, error) {
	if result.ProtocolVersion != request.ProtocolVersion || result.DecisionID != request.DecisionID || result.ActionSchema != ChoiceSchema || request.ActionSchema != ChoiceSchema {
		return nil, ErrInvalidResult
	}
	selected, ok := result.Action.(Selection)
	if !ok {
		return nil, ErrInvalidResult
	}
	choices, ok := request.Actions.(Choices)
	if !ok {
		return nil, ErrInvalidResult
	}
	for _, candidate := range choices {
		if candidate.ID == selected.CandidateID {
			return candidate.Action, nil
		}
	}
	return nil, ErrInvalidResult
}
