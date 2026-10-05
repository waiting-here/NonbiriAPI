package strategy

import (
	"math/rand"

	"github.com/waiting-here/NonbiriAPI/internal/game/bidding/engine"
)

type Scenario struct {
	ID          string      `json:"id"`
	Name        string      `json:"name"`
	Observation Observation `json:"observation"`
}

// Scenarios are reproducible public projections. Preview never accepts private
// server state, reads player data or consumes the live game's random stream.
func Scenarios() []Scenario {
	state, err := engine.New(rand.New(rand.NewSource(1729)))
	if err != nil {
		panic(err)
	}
	result := []Scenario{}
	for state.Phase != engine.Terminal {
		seat := 0
		if state.Phase == engine.Joker {
			seat = *engine.Dealer(state.Round)
		}
		request, err := Request(state, seat)
		if err != nil {
			panic(err)
		}
		o := request.Observation.(Observation)
		id, name := "", ""
		switch {
		case state.Round == 1 && state.Phase == engine.Joker:
			id, name = "opening-joker", "开局 Joker"
		case state.Round == 1 && state.Phase == engine.Bid:
			id, name = "opening-bid", "开局出价"
		case state.Round == 7 && state.Phase == engine.Bid:
			id, name = "middle", "中盘奖池"
		case state.Round == 12 && state.Phase == engine.Bid:
			id, name = "endgame", "最后两张牌"
		}
		if id != "" {
			result = append(result, Scenario{id, name, o})
		}
		if state.Phase == engine.Joker {
			state, err = engine.DecideJoker(state, seat, false)
		} else {
			bids := [2]int{state.Hands[0][len(state.Hands[0])/2], state.Hands[1][0]}
			state, _, err = engine.ResolveBids(state, bids)
		}
		if err != nil {
			panic(err)
		}
	}
	return result
}
