package strategy

import (
	"strconv"

	"github.com/waiting-here/NonbiriAPI/internal/game/ai"
	"github.com/waiting-here/NonbiriAPI/internal/game/bidding/engine"
)

type Observation struct {
	Round int          `json:"round"`
	Phase engine.Phase `json:"phase"`
	Seat  int          `json:"seat"`
	View  engine.View  `json:"view"`
}
type Action struct {
	Kind string `json:"kind"`
	Use  *bool  `json:"use,omitempty"`
	Card *int   `json:"card,omitempty"`
}
type PublicRules struct {
	Rounds                       int
	RewardRanks                  []int
	Tie, FinalTie, Winner, Joker string
}

func Capability() ai.Capability {
	return ai.Capability{ProtocolVersion: ai.ProtocolVersion, Game: "bidding", ObservationSchema: ObservationSchema, ActionSchema: ai.ChoiceSchema}
}

// Request projects state before creating any source input. Hidden future
// reward order and the other participant's uncommitted action never enter it.
func Request(state engine.State, seat int) (ai.Request, error) {
	view, err := engine.Project(state, seat, nil)
	if err != nil {
		return ai.Request{}, err
	}
	o := Observation{state.Round, state.Phase, seat, view}
	choices := ai.Choices{}
	switch state.Phase {
	case engine.Joker:
		if view.Dealer == nil || *view.Dealer != seat {
			return ai.Request{}, ErrObservation
		}
		no, yes := false, true
		choices = append(choices, ai.Choice{ID: "keep", Action: Action{Kind: "joker", Use: &no}}, ai.Choice{ID: "use", Action: Action{Kind: "joker", Use: &yes}})
	case engine.Bid:
		for _, card := range view.HandRemaining[seat] {
			c := card
			choices = append(choices, ai.Choice{ID: "bid-" + strconv.Itoa(c), Action: Action{Kind: "bid", Card: &c}})
		}
	default:
		return ai.Request{}, ErrObservation
	}
	return ai.Request{Capability: Capability(), Observation: o, Actions: choices, RuleContext: PublicRules{Rounds: 13, RewardRanks: []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13}, Tie: "carry pool", FinalTie: "discard pool", Winner: "highest total score", Joker: "dealer may double only their current reward once, before round 13"}}, nil
}

func (o Observation) valid() bool {
	if o.Seat < 0 || o.Seat > 1 || o.Round < 1 || o.Round > 13 || o.Phase != engine.Joker && o.Phase != engine.Bid {
		return false
	}
	if len(o.View.HandRemaining[0]) != 14-o.Round || len(o.View.HandRemaining[1]) != 14-o.Round {
		return false
	}
	if o.View.PoolPoints < 0 || o.View.PoolPoints > engine.MaxPoints {
		return false
	}
	return o.Phase != engine.Joker || o.View.Dealer != nil && *o.View.Dealer == o.Seat && o.View.JokerAvailable[o.Seat]
}

func future(o Observation) (sum float64, ownMean float64) {
	sum = 182
	ownSum, ownSeen := 0, 0
	for _, r := range o.View.Rewards {
		sum -= float64(r.Rank)
		if r.Side == o.Seat {
			ownSum += r.Rank
			ownSeen++
		}
	}
	if ownSeen < 13 {
		ownMean = float64(91-ownSum) / float64(13-ownSeen)
	}
	return
}
