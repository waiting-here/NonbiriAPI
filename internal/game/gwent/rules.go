// Package gwent connects card rules to the shared session and payment host.
package gwent

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"slices"
	"strconv"

	"github.com/waiting-here/NonbiriAPI/internal/game/duel"
	"github.com/waiting-here/NonbiriAPI/internal/game/gwent/engine"
)

type Rules struct{}

var _ duel.Rules = Rules{}
var _ duel.SequentialRules = Rules{}

type state struct {
	Game          engine.State    `json:"game"`
	Windows       [2]uint64       `json:"windows"`
	TimeoutStreak [2]int          `json:"timeout_streak"`
	RoundStart    json.RawMessage `json:"round_start,omitempty"`
	Actions       []stepRecord    `json:"actions,omitempty"`
}

type stepRecord struct {
	Seat      int           `json:"seat"`
	Action    engine.Action `json:"action"`
	Automatic bool          `json:"automatic,omitempty"`
}

type roundRecord struct {
	engine.RoundRecord
	Actions []stepRecord `json:"actions"`
}

var catalog = func() duel.Catalog {
	body, err := duel.Encode(struct {
		Version int                 `json:"rules_version"`
		Cards   []engine.Definition `json:"cards"`
	}{engine.Version, engine.Catalog()})
	if err != nil {
		panic(err)
	}
	hash := sha256.Sum256(body)
	return duel.Catalog{Hash: hex.EncodeToString(hash[:]), DesignVersion: "1", SchemaVersion: 1, JSON: body}
}()

func (Rules) ID() string { return "gwent" }
func (Rules) Catalog(mode string) (duel.Catalog, error) {
	if mode != "standard" {
		return duel.Catalog{}, duel.ErrInvalidRequest
	}
	return catalog, nil
}
func (r Rules) Loadout(mode string, raw json.RawMessage) (json.RawMessage, error) {
	if _, err := r.Catalog(mode); err != nil {
		return nil, err
	}
	deck := engine.StarterDeck("openai")
	if len(raw) > 0 && string(raw) != "null" && string(raw) != "{}" {
		if duel.Decode(raw, &deck) != nil {
			return nil, duel.ErrInvalidRequest
		}
	}
	if engine.ValidateDeck(deck) != nil {
		return nil, duel.ErrInvalidRequest
	}
	return duel.Encode(deck)
}
func (r Rules) Create(mode string, loadouts [2]json.RawMessage) (json.RawMessage, error) {
	return r.CreateWithRandom(mode, loadouts, nil)
}
func (r Rules) CreateWithRandom(mode string, loadouts [2]json.RawMessage, random io.Reader) (json.RawMessage, error) {
	var decks [2]engine.Deck
	for seat, raw := range loadouts {
		body, err := r.Loadout(mode, raw)
		if err != nil {
			return nil, err
		}
		if json.Unmarshal(body, &decks[seat]) != nil {
			return nil, duel.ErrInvariant
		}
	}
	game, err := engine.New(decks, random)
	if err != nil {
		return nil, err
	}
	s := state{Game: game, Windows: [2]uint64{1, 1}}
	s.RoundStart, err = snapshot(game)
	if err != nil {
		return nil, err
	}
	return duel.Encode(s)
}
func (r Rules) state(mode string, raw json.RawMessage) (state, error) {
	if _, err := r.Catalog(mode); err != nil {
		return state{}, err
	}
	var s state
	if len(raw) > 1<<20 || json.Unmarshal(raw, &s) != nil || s.Game.Validate() != nil {
		return state{}, duel.ErrInvariant
	}
	return s, nil
}
func snapshot(game engine.State) (json.RawMessage, error) {
	return duel.Encode(state{Game: game})
}
func (r Rules) Inspect(mode string, raw json.RawMessage) (duel.RuleInfo, error) {
	s, err := r.state(mode, raw)
	if err != nil {
		return duel.RuleInfo{}, err
	}
	scores := s.Game.Scores()
	i := duel.RuleInfo{Round: s.Game.Round, Phase: s.Game.Phase(), Seconds: s.Game.Seconds(), Required: s.Game.Required(), Scores: [2]int64{int64(scores[0]), int64(scores[1])}}
	if i.Phase == "initiative" {
		i.Phase = "choice"
	}
	if s.Game.Result != nil {
		i.Result = &duel.RuleResult{Winner: s.Game.Result.Winner, Reason: s.Game.Result.Reason, Scores: i.Scores}
	}
	return i, nil
}
func (r Rules) Decisions(mode string, raw json.RawMessage) ([2]string, error) {
	s, err := r.state(mode, raw)
	var ids [2]string
	if err != nil {
		return ids, err
	}
	for seat, required := range s.Game.Required() {
		if required {
			if s.Windows[seat] == 0 {
				return ids, duel.ErrInvariant
			}
			ids[seat] = strconv.FormatUint(s.Windows[seat], 10)
		}
	}
	return ids, nil
}
func (r Rules) Accept(mode string, raw json.RawMessage, seat int, body json.RawMessage) (json.RawMessage, error) {
	s, err := r.state(mode, raw)
	if err != nil {
		return nil, err
	}
	var action engine.Action
	if seat < 0 || seat > 1 || duel.Decode(body, &action) != nil || !slices.Contains(s.Game.Legal(seat), action) {
		return nil, duel.ErrInvalidRequest
	}
	return duel.Encode(action)
}
func (r Rules) Automatic(mode string, raw json.RawMessage, seat int) (json.RawMessage, error) {
	s, err := r.state(mode, raw)
	if err != nil {
		return nil, err
	}
	if seat < 0 || seat > 1 {
		return nil, duel.ErrInvalidRequest
	}
	if !s.Game.Required()[seat] {
		return json.RawMessage(`{"kind":"wait"}`), nil
	}
	return json.RawMessage(`{"kind":"timeout"}`), nil
}
func (r Rules) Step(mode string, raw json.RawMessage, seat int, body json.RawMessage, timeout bool, random io.Reader) (duel.StepTransition, error) {
	s, err := r.state(mode, raw)
	if err != nil {
		return duel.StepTransition{}, err
	}
	if len(s.RoundStart) == 0 || seat < 0 || seat > 1 {
		return duel.StepTransition{}, duel.ErrInvariant
	}
	var a engine.Action
	if timeout {
		if duel.Decode(body, &a) != nil || a != (engine.Action{Kind: "timeout"}) || !s.Game.Required()[seat] {
			return duel.StepTransition{}, duel.ErrInvalidRequest
		}
	} else {
		action, err := r.Accept(mode, raw, seat, body)
		if err != nil {
			return duel.StepTransition{}, err
		}
		_ = json.Unmarshal(action, &a)
	}
	before := s.Game
	automaticTurn := timeout && before.Phase() == "turn"
	if before.Phase() == "turn" {
		if timeout {
			s.TimeoutStreak[seat]++
		} else {
			s.TimeoutStreak[seat] = 0
		}
		if s.TimeoutStreak[seat] >= 3 {
			winner := 1 - seat
			s.Game.Result = &engine.Result{Winner: &winner, Reason: "afk"}
			s.Game.Stage = "terminal"
			s.Game.Queue = nil
			next, err := duel.Encode(s)
			return duel.StepTransition{State: next}, err
		}
	}
	if timeout {
		if automaticTurn {
			a, err = engine.LocalDecision(before, seat, random)
		} else {
			a, err = engine.TimeoutChoice(before, seat)
		}
		if err != nil {
			return duel.StepTransition{}, err
		}
	}
	current := before
	result := duel.StepTransition{}
	var choices engine.AutomaticChoices
	for {
		next, rounds, err := engine.ApplyWithRounds(current, seat, a, random)
		if err != nil {
			return duel.StepTransition{}, err
		}
		s.Actions = append(s.Actions, stepRecord{Seat: seat, Action: a, Automatic: timeout})
		for _, boundary := range rounds {
			after, err := snapshot(boundary)
			if err != nil {
				return duel.StepTransition{}, err
			}
			record := boundary.Rounds[len(boundary.Rounds)-1]
			facts, err := duel.Encode(roundRecord{RoundRecord: record, Actions: s.Actions})
			if err != nil {
				return duel.StepTransition{}, err
			}
			result.Rounds = append(result.Rounds, duel.CompletedRound{Round: record.Round, Before: s.RoundStart, After: after, Facts: facts})
			s.RoundStart, s.Actions = after, nil
		}
		current = next
		if !automaticTurn || next.Result != nil || next.Round != before.Round || next.Choices[seat] == nil {
			break
		}
		a, err = choices.Next(next, seat, random)
		if err != nil {
			return duel.StepTransition{}, err
		}
	}
	result.KeepDeadline = sameWindow(before, current, seat)
	for other := range 2 {
		if other != seat && before.Phase() == "mulligan" && current.Phase() == "mulligan" {
			continue
		}
		s.Windows[other]++
	}
	s.Game = current
	result.State, err = duel.Encode(s)
	return result, err
}
func sameWindow(before, after engine.State, seat int) bool {
	if before.Phase() == "mulligan" && after.Phase() == "mulligan" {
		return true
	}
	b, a := before.Choices[seat], after.Choices[seat]
	return b != nil && a != nil && b.Kind == a.Kind && b.Source == a.Source && a.Remaining < b.Remaining
}
func (r Rules) Resume(mode string, raw json.RawMessage) (json.RawMessage, error) {
	s, err := r.state(mode, raw)
	if err != nil {
		return nil, err
	}
	for seat := range 2 {
		s.Windows[seat]++
	}
	return duel.Encode(s)
}
func (Rules) Resolve(string, json.RawMessage, [2]json.RawMessage) (duel.Transition, error) {
	return duel.Transition{}, duel.ErrInvariant
}
func (Rules) Begin(string, json.RawMessage) (json.RawMessage, json.RawMessage, error) {
	return nil, nil, duel.ErrInvariant
}
func (r Rules) View(mode string, raw json.RawMessage, viewer int, _ bool, _ json.RawMessage) (json.RawMessage, error) {
	s, err := r.state(mode, raw)
	if err != nil {
		return nil, err
	}
	v, err := engine.Project(s.Game, viewer)
	if err != nil {
		return nil, err
	}
	return duel.Encode(v)
}
func (r Rules) RoundView(mode string, raw json.RawMessage, viewer int, terminal bool) (json.RawMessage, error) {
	if _, err := r.Catalog(mode); err != nil {
		return nil, err
	}
	var record roundRecord
	if viewer < 0 || viewer > 1 || json.Unmarshal(raw, &record) != nil {
		return nil, duel.ErrInvariant
	}
	if !terminal {
		record.Actions = slices.DeleteFunc(record.Actions, func(step stepRecord) bool {
			return step.Seat != viewer && step.Action.Kind != "play" && step.Action.Kind != "pass" && step.Action.Kind != "leader"
		})
	}
	return duel.Encode(record)
}
func (r Rules) Archive(mode string, raw json.RawMessage) (json.RawMessage, error) {
	s, err := r.state(mode, raw)
	if err != nil {
		return nil, err
	}
	return duel.Encode(s.Game)
}
