package bidding

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"

	"github.com/waiting-here/NonbiriAPI/internal/game/bidding/config"
	"github.com/waiting-here/NonbiriAPI/internal/game/bidding/engine"
	"github.com/waiting-here/NonbiriAPI/internal/game/duel"
)

type Rules struct{}

var _ duel.Rules = Rules{}

func (Rules) ID() string { return config.ID }
func (Rules) Catalog(mode string) (duel.Catalog, error) {
	if !slices.Contains(config.Modes(), mode) {
		return duel.Catalog{}, duel.ErrInvalidRequest
	}
	body := json.RawMessage(`{"rules_version":1,"rounds":13,"joker_seconds":10,"bid_seconds":20,"reward_ranks":[1,2,3,4,5,6,7,8,9,10,11,12,13],"joker_multiplier":2,"ties":"carry_then_discard"}`)
	hash := sha256.Sum256(body)
	return duel.Catalog{Hash: hex.EncodeToString(hash[:]), DesignVersion: "1", SchemaVersion: 1, JSON: body}, nil
}
func (r Rules) Loadout(mode string, raw json.RawMessage) (json.RawMessage, error) {
	if _, err := r.Catalog(mode); err != nil {
		return nil, err
	}
	if len(raw) != 0 && string(raw) != "null" && string(raw) != "{}" {
		return nil, duel.ErrInvalidRequest
	}
	return json.RawMessage(`{}`), nil
}
func (r Rules) Create(mode string, loadouts [2]json.RawMessage) (json.RawMessage, error) {
	for _, raw := range loadouts {
		if _, err := r.Loadout(mode, raw); err != nil {
			return nil, err
		}
	}
	s, err := engine.New(nil)
	if err != nil {
		return nil, duel.ErrUnavailable
	}
	return duel.Encode(s)
}
func (r Rules) state(mode string, raw json.RawMessage) (engine.State, error) {
	if _, err := r.Catalog(mode); err != nil {
		return engine.State{}, err
	}
	var s engine.State
	if duel.Decode(raw, &s) != nil || s.Validate() != nil {
		return s, duel.ErrInvariant
	}
	return s, nil
}
func (r Rules) Inspect(mode string, raw json.RawMessage) (duel.RuleInfo, error) {
	s, err := r.state(mode, raw)
	if err != nil {
		return duel.RuleInfo{}, err
	}
	i := duel.RuleInfo{Round: s.Round, Phase: string(s.Phase), Scores: [2]int64{int64(s.Scores[0]), int64(s.Scores[1])}}
	switch s.Phase {
	case engine.Joker:
		i.Seconds = 10
		i.Required[*engine.Dealer(s.Round)] = true
	case engine.Bid:
		i.Seconds = 20
		i.Required = [2]bool{true, true}
	case engine.Terminal:
		i.Result = &duel.RuleResult{Winner: s.Result.Winner, Reason: "rounds", Scores: i.Scores}
	}
	return i, nil
}

type action struct {
	Kind string `json:"kind"`
	Use  *bool  `json:"use,omitempty"`
	Card *int   `json:"card,omitempty"`
}

func (r Rules) Accept(mode string, raw json.RawMessage, seat int, body json.RawMessage) (json.RawMessage, error) {
	s, err := r.state(mode, raw)
	if err != nil {
		return nil, err
	}
	if seat < 0 || seat > 1 {
		return nil, duel.ErrInvalidRequest
	}
	var a action
	if duel.Decode(body, &a) != nil {
		return nil, duel.ErrInvalidRequest
	}
	switch s.Phase {
	case engine.Joker:
		if seat != *engine.Dealer(s.Round) || a.Kind != "joker" || a.Use == nil || a.Card != nil || duel.HasFields(body, "card") {
			return nil, duel.ErrInvalidRequest
		}
	case engine.Bid:
		if a.Kind != "bid" || a.Card == nil || a.Use != nil || duel.HasFields(body, "use") || !slices.Contains(s.Hands[seat], *a.Card) {
			return nil, duel.ErrInvalidRequest
		}
	default:
		return nil, duel.ErrConflict
	}
	return duel.Encode(a)
}
func (r Rules) Automatic(mode string, raw json.RawMessage, seat int) (json.RawMessage, error) {
	s, err := r.state(mode, raw)
	if err != nil {
		return nil, err
	}
	if seat < 0 || seat > 1 {
		return nil, duel.ErrInvalidRequest
	}
	if s.Phase == engine.Joker {
		if seat != *engine.Dealer(s.Round) {
			return json.RawMessage(`{"kind":"pass"}`), nil
		}
		return json.RawMessage(`{"kind":"joker","use":false}`), nil
	}
	card, err := engine.AutomaticBid(s, seat)
	if err != nil {
		return nil, duel.ErrConflict
	}
	return duel.Encode(action{Kind: "bid", Card: &card})
}
func (r Rules) Resolve(mode string, raw json.RawMessage, actions [2]json.RawMessage) (duel.Transition, error) {
	s, err := r.state(mode, raw)
	if err != nil {
		return duel.Transition{}, err
	}
	var next engine.State
	var record json.RawMessage
	if s.Phase == engine.Joker {
		dealer := *engine.Dealer(s.Round)
		body, err := r.Accept(mode, raw, dealer, actions[dealer])
		if err != nil {
			return duel.Transition{}, err
		}
		if string(actions[1-dealer]) != `{"kind":"pass"}` {
			return duel.Transition{}, duel.ErrInvariant
		}
		var a action
		_ = json.Unmarshal(body, &a)
		next, err = engine.DecideJoker(s, dealer, *a.Use)
		if err != nil {
			return duel.Transition{}, duel.ErrInvariant
		}
	} else if s.Phase == engine.Bid {
		var bids [2]int
		for seat, body := range actions {
			validated, err := r.Accept(mode, raw, seat, body)
			if err != nil {
				return duel.Transition{}, err
			}
			var a action
			_ = json.Unmarshal(validated, &a)
			bids[seat] = *a.Card
		}
		var round engine.RoundRecord
		next, round, err = engine.ResolveBids(s, bids)
		if err != nil {
			return duel.Transition{}, duel.ErrInvariant
		}
		record, err = duel.Encode(round)
		if err != nil {
			return duel.Transition{}, err
		}
	} else {
		return duel.Transition{}, duel.ErrConflict
	}
	encoded, err := duel.Encode(next)
	return duel.Transition{State: encoded, Record: record, Round: s.Round}, err
}
func (Rules) Begin(string, json.RawMessage) (json.RawMessage, json.RawMessage, error) {
	return nil, nil, duel.ErrConflict
}
func (r Rules) View(mode string, raw json.RawMessage, viewer int, _ bool, own json.RawMessage) (json.RawMessage, error) {
	s, err := r.state(mode, raw)
	if err != nil {
		return nil, err
	}
	var card *int
	if len(own) > 0 {
		var a action
		if duel.Decode(own, &a) != nil {
			return nil, duel.ErrInvariant
		}
		card = a.Card
	}
	v, err := engine.Project(s, viewer, card)
	if err != nil {
		return nil, duel.ErrInvariant
	}
	return duel.Encode(v)
}
func (r Rules) RoundView(mode string, raw json.RawMessage, viewer int, _ bool) (json.RawMessage, error) {
	if _, err := r.Catalog(mode); err != nil {
		return nil, err
	}
	if viewer < 0 || viewer > 1 {
		return nil, duel.ErrInvalidRequest
	}
	var record engine.RoundRecord
	if duel.Decode(raw, &record) != nil || record.Round < 1 || record.Round > 13 {
		return nil, duel.ErrInvariant
	}
	return duel.Encode(record)
}
func (r Rules) Archive(mode string, raw json.RawMessage) (json.RawMessage, error) {
	s, err := r.state(mode, raw)
	if err != nil {
		return nil, err
	}
	view, err := engine.Project(s, 0, nil)
	if err != nil {
		return nil, duel.ErrInvariant
	}
	return duel.Encode(struct {
		View        engine.View `json:"view"`
		RewardDecks [2][13]int  `json:"reward_decks"`
	}{View: view, RewardDecks: s.Decks})
}
