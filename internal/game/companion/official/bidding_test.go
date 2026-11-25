package official

import (
	"encoding/json"
	"math/rand"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/game/bidding/engine"
)

func TestBiddingCompletesFromEitherSeat(t *testing.T) {
	for seat := range 2 {
		state, err := engine.New(rand.New(rand.NewSource(30 + int64(seat))))
		if err != nil {
			t.Fatal(err)
		}
		for state.Result == nil {
			if state.Phase == engine.Joker {
				dealer := *engine.Dealer(state.Round)
				view, _ := engine.Project(state, dealer, nil)
				raw, _ := json.Marshal(view)
				action, err := biddingDecision("joker", dealer, state.Round, raw, 0)
				if err != nil {
					t.Fatal(err)
				}
				var joker struct{ Use bool }
				raw, _ = json.Marshal(action)
				if err := json.Unmarshal(raw, &joker); err != nil {
					t.Fatal(err)
				}
				state, err = engine.DecideJoker(state, dealer, joker.Use)
				if err != nil {
					t.Fatal(err)
				}
			}
			view, _ := engine.Project(state, seat, nil)
			var bids [2]int
			bids[seat] = bid(view, seat, 0)
			opponent := view.HandRemaining[1-seat]
			bids[1-seat] = opponent[(3*state.Round)%len(opponent)]
			state, _, err = engine.ResolveBids(state, bids)
			if err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestBiddingSavesHighCardForLargePool(t *testing.T) {
	v := engine.View{HandRemaining: [2][]int{{2, 12}, {3, 10}}, Rewards: []engine.Reward{}}
	for rank := 1; rank <= 12; rank++ {
		v.Rewards = append(v.Rewards, engine.Reward{Rank: rank}, engine.Reward{Rank: rank})
	}
	v.PoolPoints = 5
	low := bid(v, 0, 0)
	v.PoolPoints = 42
	if high := bid(v, 0, 0); low != 2 || high != 12 {
		t.Fatalf("small pool bid %d, large pool bid %d", low, high)
	}
}
