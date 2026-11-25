package companion

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"sync"

	bid "github.com/waiting-here/NonbiriAPI/internal/game/bidding/engine"
	gw "github.com/waiting-here/NonbiriAPI/internal/game/gwent/engine"
	lk "github.com/waiting-here/NonbiriAPI/internal/game/likes/engine"
)

const protocol = "nonbiri.decision/1"

type Raw = json.RawMessage
type Obj = map[string]any
type space struct {
	Kind     string `json:"kind"`
	Examples []Raw  `json:"examples"`
}
type turn struct {
	Phase string
	Round int
	Need  [2]bool
	View  any
	Space space
}

// A JSON null marks a seat with no sealed action. RawMessage alone retains the literal bytes "null".
type sealed [2]Raw

func (s *sealed) UnmarshalJSON(raw []byte) error {
	var values [2]*Raw
	if err := json.Unmarshal(raw, &values); err != nil {
		return err
	}
	*s = sealed{}
	for i, v := range values {
		if v != nil {
			s[i] = *v
		}
	}
	return nil
}

type frame struct {
	Bidding *bid.State `json:"b,omitempty"`
	Gwent   *gw.State  `json:"g,omitempty"`
	Likes   *lk.State  `json:"l,omitempty"`
	Locked  sealed     `json:"locked"`
	Memory  string     `json:"memory,omitempty"`
	Recent  [2][]Raw   `json:"recent,omitempty"`
}
type result struct {
	Status string   `json:"status"`
	Reason string   `json:"reason"`
	Winner *int     `json:"winner"`
	Scores [2]int64 `json:"scores"`
}
type initial struct {
	Program     string `json:"program"`
	Version     int64  `json:"program_version"`
	Difficulty  int    `json:"difficulty"`
	Game        string `json:"game"`
	Mode        string `json:"mode"`
	Rules       string `json:"rules_hash"`
	Human       int    `json:"human_seat"`
	Constraints Raw    `json:"constraints"`
}
type window struct {
	Phase   string `json:"phase"`
	Actions sealed `json:"actions"`
}
type record struct {
	Initial initial  `json:"initial"`
	Chance  string   `json:"chance"`
	Actions []window `json:"actions"`
	Result  result   `json:"result"`
}
type rules struct {
	catalog Raw
	hash    string
	likes   *lk.Engine
}

var ruleCache sync.Map

func rulesFor(game, mode string) (rules, error) {
	key := game + "/" + mode
	if entry, ok := ruleCache.Load(key); ok {
		return entry.(rules), nil
	}
	v, h, err := catalogFor(game, mode)
	if err != nil {
		return rules{}, errInput
	}
	r := rules{catalog: encode(Obj{"game": game, "mode": mode, "rules_hash": h, "catalog": v}), hash: h}
	if game == "likes" {
		r.likes, err = lk.New(mode)
		if err != nil {
			return rules{}, err
		}
	}
	ruleCache.Store(key, r)
	return r, nil
}

type random struct {
	*rand.ChaCha8
	r *rand.Rand
}

func (r random) Uint64n(n uint64) (uint64, error) { return r.r.Uint64N(n), nil }
func rng(seed string, step int) random {
	s := rand.NewChaCha8(sha256.Sum256([]byte(fmt.Sprintf("companion-v1/%s/%d", seed, step))))
	return random{s, rand.New(s)}
}
func (r rules) begin(h record) (frame, error) {
	f := frame{}
	if h.Initial.Game == "bidding" {
		state, err := bid.New(rng(h.Chance, -1))
		f.Bidding = &state
		return f, err
	}
	return f, nil
}
func (r rules) observe(f frame, h *record, seat int, terminal bool) (turn, error) {
	t := turn{Space: space{"enumerated", []Raw{}}}
	switch {
	case f.Bidding != nil:
		s := f.Bidding
		t.Phase = string(s.Phase)
		t.Round = s.Round
		var selected *int
		if t.Phase == "bid" && f.Locked[seat] != nil {
			var action struct{ Card int }
			json.Unmarshal(f.Locked[seat], &action)
			selected = &action.Card
		}
		v, err := bid.Project(*s, seat, selected)
		if err != nil {
			return t, err
		}
		t.View = v
		switch s.Phase {
		case bid.Joker:
			t.Need[*bid.Dealer(s.Round)] = true
			if t.Need[seat] {
				for _, use := range []bool{false, true} {
					t.Space.Examples = append(t.Space.Examples, encode(Obj{"kind": "joker", "use": use}))
				}
			}
		case bid.Bid:
			t.Need = [2]bool{true, true}
			for _, card := range v.HandRemaining[seat] {
				t.Space.Examples = append(t.Space.Examples, encode(Obj{"kind": "bid", "card": card}))
			}
		}
	case f.Gwent != nil:
		s := f.Gwent
		t.Phase = s.Phase()
		t.Round = s.Round
		t.Need = s.Required()
		v, err := gw.Project(*s, seat)
		if err != nil {
			return t, err
		}
		t.View = v
		for _, a := range v.Legal {
			t.Space.Examples = append(t.Space.Examples, encode(a))
		}
	case f.Likes != nil:
		s := f.Likes
		t.Round = int(s.Round)
		t.Phase = "plan"
		t.Need = [2]bool{!s.Blocked[0], !s.Blocked[1]}
		var locked *lk.Plan
		if f.Locked[seat] != nil {
			var a planAction
			json.Unmarshal(f.Locked[seat], &a)
			locked = a.Plan
		}
		v, err := r.likes.Project(*s, seat, terminal || s.Result != nil, locked)
		if err != nil {
			return t, err
		}
		t.View = v
		t.Space.Kind = "structured"
		if s.Result != nil {
			t.Phase = "terminal"
			t.Need = [2]bool{}
		}
		if t.Need[seat] {
			plans := []lk.Plan{lk.EmptyPlan()}
			for _, id := range s.Players[seat].Loadout {
				p := lk.EmptyPlan()
				p.Main = &lk.Choice{SkillID: id}
				plans = append(plans, p)
			}
			for _, p := range plans {
				if _, err := r.likes.ValidateManualPlan(*s, seat, p); err == nil {
					t.Space.Examples = append(t.Space.Examples, encode(planAction{"plan", &p}))
				}
			}
		}
	default:
		t.Phase = "setup"
		t.Need = [2]bool{true, true}
		t.View = Obj{}
		t.Space.Kind = "setup"
		limits := Raw("{}")
		if seat != h.Initial.Human {
			limits = h.Initial.Constraints
		}
		switch h.Initial.Game {
		case "likes":
			var l likesLimits
			if strict(limits, &l) != nil {
				return t, errInput
			}
			t.Space.Examples = likesExamples(r.likes, r.likes.Catalog(), l)
		case "gwent":
			var l gwLimits
			if strict(limits, &l) != nil {
				return t, errInput
			}
			for _, f := range []string{"openai", "deepseek", "claude", "gemini"} {
				if l.Faction == "" || l.Faction == f {
					t.Space.Examples = append(t.Space.Examples, encode(gw.StarterDeck(f)))
				}
			}
		default:
			return t, errInput
		}
		if len(t.Space.Examples) == 0 {
			return t, errInput
		}
	}
	return t, nil
}

type planAction struct {
	Kind string   `json:"kind"`
	Plan *lk.Plan `json:"plan"`
}

func (r rules) validate(f frame, h *record, seat int, t turn, action Raw) error {
	if !t.Need[seat] || f.Locked[seat] != nil {
		return errConflict
	}
	switch t.Space.Kind {
	case "setup":
		limits := Raw("{}")
		if seat != h.Initial.Human {
			limits = h.Initial.Constraints
		}
		if h.Initial.Game == "gwent" {
			var d gw.Deck
			var l gwLimits
			if strict(action, &d) == nil && strict(limits, &l) == nil && gw.ValidateDeck(d) == nil && (l.Faction == "" || d.Faction == l.Faction) {
				return nil
			}
		}
		if h.Initial.Game == "likes" {
			var d lk.Selection
			var l likesLimits
			if strict(action, &d) == nil && strict(limits, &l) == nil && l.allows(d) && r.likes.ValidateSelection(d) == nil {
				return nil
			}
		}
	case "structured":
		var a planAction
		var keys struct{ Plan map[string]Raw }
		if strict(action, &a) == nil && a.Kind == "plan" && a.Plan != nil && a.Plan.Extra != nil && a.Plan.Purchases != nil && json.Unmarshal(action, &keys) == nil && keys.Plan["main"] != nil {
			if _, err := r.likes.ValidateManualPlan(*f.Likes, seat, *a.Plan); err == nil {
				return nil
			}
		}
	case "enumerated":
		if member(action, t.Space.Examples) {
			return nil
		}
	}
	return errInput
}
func (f *frame) appendSeen(seat int, value any) {
	f.Recent[seat] = append(f.Recent[seat], encode(value))
	if len(f.Recent[seat]) > 32 {
		f.Recent[seat] = f.Recent[seat][1:]
	}
}

// advance applies a complete simultaneous window once. Its random stream depends only on the recorded seed and window number.
func (r rules) advance(f *frame, h *record, t turn) error {
	rand := rng(h.Chance, len(h.Actions))
	actions := f.Locked
	h.Actions = append(h.Actions, window{t.Phase, actions})
	f.Locked = [2]Raw{}
	switch t.Phase {
	case "setup":
		if h.Initial.Game == "likes" {
			var sels [2]lk.Selection
			for i := range 2 {
				if err := json.Unmarshal(actions[i], &sels[i]); err != nil {
					return err
				}
			}
			s, err := r.likes.Create(sels)
			if err != nil {
				return err
			}
			f.Likes = &s
		} else {
			var decks [2]gw.Deck
			for i := range 2 {
				if err := json.Unmarshal(actions[i], &decks[i]); err != nil {
					return err
				}
			}
			s, err := gw.New(decks, rand)
			if err != nil {
				return err
			}
			f.Gwent = &s
		}
	case "joker":
		seat := *bid.Dealer(t.Round)
		var a struct{ Use bool }
		json.Unmarshal(actions[seat], &a)
		s, err := bid.DecideJoker(*f.Bidding, seat, a.Use)
		if err != nil {
			return err
		}
		f.Bidding = &s
		for i := range 2 {
			f.appendSeen(i, Obj{"round": t.Round, "seat": seat, "action": actions[seat]})
		}
	case "bid":
		var cards [2]int
		for i := range 2 {
			var a struct{ Card int }
			json.Unmarshal(actions[i], &a)
			cards[i] = a.Card
		}
		s, entry, err := bid.ResolveBids(*f.Bidding, cards)
		if err != nil {
			return err
		}
		f.Bidding = &s
		for i := range 2 {
			f.appendSeen(i, entry)
		}
		if s.Result != nil {
			h.Result = result{"completed", "rounds", s.Result.Winner, [2]int64{int64(s.Scores[0]), int64(s.Scores[1])}}
		}
	case "plan":
		var plans [2]lk.Plan
		for i := range 2 {
			plans[i] = lk.EmptyPlan()
			if t.Need[i] {
				var a planAction
				if err := json.Unmarshal(actions[i], &a); err != nil {
					return err
				}
				p, err := r.likes.ValidateManualPlan(*f.Likes, i, *a.Plan)
				if err != nil {
					return err
				}
				plans[i] = p
			}
		}
		s, _, err := r.likes.Resolve(*f.Likes, plans, func(n int) (int, error) { return rand.r.IntN(n), nil })
		if err != nil {
			return err
		}
		for i := range 2 {
			f.appendSeen(i, Obj{"round": t.Round, "records": s.Records, "scores": [2]int64{s.Players[0].Likes, s.Players[1].Likes}})
		}
		if s.Result != nil {
			h.Result = result{"completed", s.Result.Reason, s.Result.Winner, s.Result.Scores}
		} else if s.AwaitingNextRound {
			s, _, err = r.likes.BeginNextRound(s)
			if err != nil {
				return err
			}
		}
		f.Likes = &s
	default:
		if f.Gwent == nil {
			return errors.New("unknown game window")
		}
		for i, need := range t.Need {
			if !need {
				continue
			}
			var a gw.Action
			if err := json.Unmarshal(actions[i], &a); err != nil {
				return err
			}
			entry := Obj{"round": t.Round, "phase": t.Phase, "seat": i, "action": a}
			f.appendSeen(i, entry)
			if a.Kind == "play" || a.Kind == "leader" || a.Kind == "pass" {
				f.appendSeen(1-i, entry)
			}
			s, err := gw.Apply(*f.Gwent, i, a, rand)
			if err != nil {
				return err
			}
			f.Gwent = &s
		}
		if s := f.Gwent; s.Result != nil {
			scores := s.Scores()
			h.Result = result{"completed", s.Result.Reason, s.Result.Winner, [2]int64{int64(scores[0]), int64(scores[1])}}
		}
	}
	return nil
}
