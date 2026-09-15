// Package engine implements the deterministic rules of a two-player bidding game.
// Persistence, admission, deadlines and credit settlement belong to its caller.
package engine

import (
	"errors"
	"io"
	"slices"

	"github.com/waiting-here/NonbiriAPI/internal/game/randomness"
)

const (
	Rounds       = 13
	JokerSeconds = 10
	BidSeconds   = 20
	MaxPoints    = 208
)

type Phase string

const (
	Joker    Phase = "joker"
	Bid      Phase = "bid"
	Terminal Phase = "terminal"
)

var (
	ErrInvalidState  = errors.New("bidding: invalid state")
	ErrInvalidAction = errors.New("bidding: invalid action")
	ErrRandom        = errors.New("bidding: random source unavailable")
)

type Reward struct {
	Round      int    `json:"round"`
	Side       int    `json:"side"`
	Rank       int    `json:"rank"`
	Multiplier int    `json:"multiplier"`
	Status     string `json:"status"`
	Owner      *int   `json:"owner"`
}

func (card Reward) Value() int { return card.Rank * card.Multiplier }

type Result struct {
	Winner *int   `json:"winner"`
	Scores [2]int `json:"scores"`
}

// State is private server state. Use Project for every participant response.
type State struct {
	Round          int        `json:"round"`
	Phase          Phase      `json:"phase"`
	Decks          [2][13]int `json:"decks"`
	Hands          [2][]int   `json:"hands"`
	Played         [2][]int   `json:"played"`
	Rewards        []Reward   `json:"rewards"`
	JokerAvailable [2]bool    `json:"joker_available"`
	Pool           int        `json:"pool"`
	Discard        int        `json:"discard"`
	Scores         [2]int     `json:"scores"`
	Result         *Result    `json:"result"`
}

type View struct {
	Dealer          *int     `json:"dealer"`
	HandRemaining   [2][]int `json:"hand_remaining"`
	Played          [2][]int `json:"played"`
	Rewards         []Reward `json:"rewards"`
	JokerAvailable  [2]bool  `json:"joker_available"`
	PoolPoints      int      `json:"pool_points"`
	Scores          [2]int   `json:"scores"`
	OwnSelectedCard *int     `json:"own_selected_card"`
}

type RoundRecord struct {
	Round           int      `json:"round"`
	Bids            [2]int   `json:"bids"`
	JokerUsedBy     *int     `json:"joker_used_by"`
	FreshValue      int      `json:"fresh_value"`
	CarryBefore     int      `json:"carry_before"`
	AwardedTo       *int     `json:"awarded_to"`
	AwardedPoints   int      `json:"awarded_points"`
	CarryAfter      int      `json:"carry_after"`
	DiscardedPoints int      `json:"discarded_points"`
	ScoreAfter      [2]int   `json:"score_after"`
	ResolvedRewards []Reward `json:"resolved_rewards"`
}

// New shuffles the two reward decks independently, without modulo bias.
// A nil reader selects the operating system's cryptographic random source.
func New(random io.Reader) (State, error) {
	state := State{Round: 1, JokerAvailable: [2]bool{true, true}}
	for side := range 2 {
		state.Hands[side] = make([]int, Rounds)
		state.Played[side] = []int{}
		for index := range Rounds {
			state.Decks[side][index] = index + 1
			state.Hands[side][index] = index + 1
		}
		for index := Rounds - 1; index > 0; index-- {
			pick, err := randomness.Index(random, uint64(index+1))
			if err != nil {
				return State{}, ErrRandom
			}
			other := int(pick)
			state.Decks[side][index], state.Decks[side][other] = state.Decks[side][other], state.Decks[side][index]
		}
	}
	beginRound(&state)
	return state, state.Validate()
}

// Dealer returns no dealer for the last round or for an invalid round.
func Dealer(round int) *int {
	if round < 1 || round >= Rounds {
		return nil
	}
	return pointer((round - 1) % 2)
}

func DecideJoker(previous State, seat int, use bool) (State, error) {
	if err := previous.Validate(); err != nil {
		return State{}, err
	}
	dealer := Dealer(previous.Round)
	if previous.Phase != Joker || dealer == nil || seat != *dealer {
		return State{}, ErrInvalidAction
	}
	state := clone(previous)
	if use {
		index := (state.Round-1)*2 + seat
		state.Rewards[index].Multiplier = 2
		state.Pool += state.Rewards[index].Rank
		state.JokerAvailable[seat] = false
	}
	state.Phase = Bid
	return state, state.Validate()
}

func AutomaticBid(state State, seat int) (int, error) {
	if err := state.Validate(); err != nil {
		return 0, err
	}
	if seat < 0 || seat > 1 || state.Phase != Bid || len(state.Hands[seat]) == 0 {
		return 0, ErrInvalidAction
	}
	return state.Hands[seat][0], nil
}

// ResolveBids is called only after both seats have locked or received timeout
// defaults. Merely accepting one seat's choice never changes this state.
func ResolveBids(previous State, bids [2]int) (State, RoundRecord, error) {
	if err := previous.Validate(); err != nil {
		return State{}, RoundRecord{}, err
	}
	if previous.Phase != Bid {
		return State{}, RoundRecord{}, ErrInvalidAction
	}
	for seat, card := range bids {
		if !slices.Contains(previous.Hands[seat], card) {
			return State{}, RoundRecord{}, ErrInvalidAction
		}
	}
	state := clone(previous)
	record := RoundRecord{Round: state.Round, Bids: bids, ResolvedRewards: []Reward{}}
	for seat, card := range bids {
		index := slices.Index(state.Hands[seat], card)
		state.Hands[seat] = slices.Delete(state.Hands[seat], index, index+1)
		state.Played[seat] = append(state.Played[seat], card)
		reward := state.Rewards[(state.Round-1)*2+seat]
		record.FreshValue += reward.Value()
		if reward.Multiplier == 2 {
			record.JokerUsedBy = pointer(seat)
		}
	}
	record.CarryBefore = state.Pool - record.FreshValue
	if bids[0] != bids[1] {
		winner := 0
		if bids[1] > bids[0] {
			winner = 1
		}
		record.AwardedTo = pointer(winner)
		record.AwardedPoints = state.Pool
		state.Scores[winner] += state.Pool
		resolvePool(&state, "awarded", pointer(winner), &record)
	} else if state.Round == Rounds {
		record.DiscardedPoints = state.Pool
		state.Discard += state.Pool
		resolvePool(&state, "discarded", nil, &record)
	}
	record.CarryAfter, record.ScoreAfter = state.Pool, state.Scores
	if state.Round == Rounds {
		state.Phase = Terminal
		state.Result = &Result{Scores: state.Scores}
		if state.Scores[0] > state.Scores[1] {
			state.Result.Winner = pointer(0)
		} else if state.Scores[1] > state.Scores[0] {
			state.Result.Winner = pointer(1)
		}
	} else {
		state.Round++
		beginRound(&state)
	}
	if err := state.Validate(); err != nil {
		return State{}, RoundRecord{}, err
	}
	return state, record, nil
}

func resolvePool(state *State, status string, owner *int, record *RoundRecord) {
	for index := range state.Rewards {
		if state.Rewards[index].Status != "pool" {
			continue
		}
		state.Rewards[index].Status = status
		state.Rewards[index].Owner = copyPointer(owner)
		record.ResolvedRewards = append(record.ResolvedRewards, copyReward(state.Rewards[index]))
	}
	state.Pool = 0
}

func beginRound(state *State) {
	for side := range 2 {
		card := Reward{Round: state.Round, Side: side, Rank: state.Decks[side][state.Round-1], Multiplier: 1, Status: "pool"}
		state.Rewards = append(state.Rewards, card)
		state.Pool += card.Value()
	}
	state.Phase = Bid
	if dealer := Dealer(state.Round); dealer != nil && state.JokerAvailable[*dealer] {
		state.Phase = Joker
	}
}

// Project copies only public cards and the viewer's own optional locked card.
// The caller must obtain that card from the authenticated viewer's seat.
func Project(state State, viewer int, ownSelected *int) (View, error) {
	if err := state.Validate(); err != nil {
		return View{}, err
	}
	if viewer < 0 || viewer > 1 || ownSelected != nil && (state.Phase != Bid || !slices.Contains(state.Hands[viewer], *ownSelected)) {
		return View{}, ErrInvalidAction
	}
	copy := clone(state)
	return View{Dealer: Dealer(state.Round), HandRemaining: copy.Hands, Played: copy.Played, Rewards: copy.Rewards, JokerAvailable: state.JokerAvailable, PoolPoints: state.Pool, Scores: state.Scores, OwnSelectedCard: copyPointer(ownSelected)}, nil
}

// Validate reconstructs reward ownership from the played cards. Stored totals,
// revealed ranks, joker claims and terminal outcomes cannot contradict the game.
func (state State) Validate() error {
	if state.Round < 1 || state.Round > Rounds || len(state.Rewards) != state.Round*2 {
		return ErrInvalidState
	}
	completed := state.Round - 1
	if state.Phase == Terminal {
		if state.Round != Rounds || state.Result == nil {
			return ErrInvalidState
		}
		completed = Rounds
	} else if state.Phase != Joker && state.Phase != Bid || state.Result != nil {
		return ErrInvalidState
	}
	for side := range 2 {
		if !permutation(state.Decks[side][:]) || len(state.Played[side]) != completed || len(state.Hands[side]) != Rounds-completed || !slices.IsSorted(state.Hands[side]) {
			return ErrInvalidState
		}
		cards := append(slices.Clone(state.Hands[side]), state.Played[side]...)
		if !permutation(cards) {
			return ErrInvalidState
		}
	}
	var jokers [2]int
	for index, card := range state.Rewards {
		round, side := index/2+1, index%2
		if card.Round != round || card.Side != side || card.Rank != state.Decks[side][round-1] || card.Multiplier < 1 || card.Multiplier > 2 {
			return ErrInvalidState
		}
		if card.Multiplier == 2 {
			dealer := Dealer(round)
			if dealer == nil || *dealer != side {
				return ErrInvalidState
			}
			jokers[side]++
		}
	}
	for side := range 2 {
		if jokers[side] > 1 || state.JokerAvailable[side] != (jokers[side] == 0) {
			return ErrInvalidState
		}
	}
	if state.Phase == Joker {
		dealer := Dealer(state.Round)
		if dealer == nil || !state.JokerAvailable[*dealer] {
			return ErrInvalidState
		}
	}
	owners := make([]int, len(state.Rewards))
	for index := range owners {
		owners[index] = -1
	}
	start := 0
	for round := 0; round < completed; round++ {
		first, second := state.Played[0][round], state.Played[1][round]
		owner := -1
		if first > second {
			owner = 0
		} else if second > first {
			owner = 1
		} else if round == Rounds-1 {
			owner = 2
		}
		if owner != -1 {
			for index := start; index < (round+1)*2; index++ {
				owners[index] = owner
			}
			start = (round + 1) * 2
		}
	}
	var scores [2]int
	pool, discard := 0, 0
	for index, card := range state.Rewards {
		switch owners[index] {
		case -1:
			if card.Status != "pool" || card.Owner != nil {
				return ErrInvalidState
			}
			pool += card.Value()
		case 2:
			if card.Status != "discarded" || card.Owner != nil {
				return ErrInvalidState
			}
			discard += card.Value()
		default:
			if card.Status != "awarded" || card.Owner == nil || *card.Owner != owners[index] {
				return ErrInvalidState
			}
			scores[*card.Owner] += card.Value()
		}
	}
	if state.Scores != scores || state.Pool != pool || state.Discard != discard || scores[0]+scores[1]+pool+discard > MaxPoints {
		return ErrInvalidState
	}
	if state.Result != nil {
		if state.Result.Scores != scores || pool != 0 {
			return ErrInvalidState
		}
		if scores[0] == scores[1] {
			if state.Result.Winner != nil {
				return ErrInvalidState
			}
		} else {
			winner := 0
			if scores[1] > scores[0] {
				winner = 1
			}
			if state.Result.Winner == nil || *state.Result.Winner != winner {
				return ErrInvalidState
			}
		}
	}
	return nil
}

func permutation(cards []int) bool {
	if len(cards) != Rounds {
		return false
	}
	seen := [Rounds + 1]bool{}
	for _, card := range cards {
		if card < 1 || card > Rounds || seen[card] {
			return false
		}
		seen[card] = true
	}
	return true
}

func clone(state State) State {
	for side := range 2 {
		state.Hands[side] = slices.Clone(state.Hands[side])
		state.Played[side] = slices.Clone(state.Played[side])
	}
	state.Rewards = slices.Clone(state.Rewards)
	for index := range state.Rewards {
		state.Rewards[index] = copyReward(state.Rewards[index])
	}
	if state.Result != nil {
		value := *state.Result
		value.Winner = copyPointer(value.Winner)
		state.Result = &value
	}
	return state
}

func pointer(value int) *int { return &value }
func copyPointer(value *int) *int {
	if value == nil {
		return nil
	}
	return pointer(*value)
}
func copyReward(card Reward) Reward {
	card.Owner = copyPointer(card.Owner)
	return card
}
