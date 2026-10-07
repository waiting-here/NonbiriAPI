// Package engine implements the catch game with integer positions and 60 Hz
// inputs. Rendering, fonts, screen size and animation do not affect scoring.
package engine

import (
	_ "embed"
	"encoding/json"
	"errors"
	"slices"
)

const (
	Version       = 1
	Hz            = 60
	LastTick      = 90 * Hz
	Goal          = 600
	Width         = 600_000
	Height        = 560_000
	CatchY        = 446_000
	PadHalf       = 43_000
	MaxBatchTicks = 300
)

var ErrInput = errors.New("steadycatch: invalid input sequence")

//go:embed phrases.json
var phrasesJSON []byte

type Phrase struct {
	ID       string `json:"id"`
	Text     string `json:"text"`
	Category string `json:"category"`
	Gold     bool   `json:"gold"`
}

var phrases, normalPool, goldPool = loadPhrases()

func loadPhrases() ([]Phrase, []int, []int) {
	var values []Phrase
	if err := json.Unmarshal(phrasesJSON, &values); err != nil {
		panic(err)
	}
	var normal, gold []int
	for i, value := range values {
		if value.Gold {
			gold = append(gold, i)
		} else {
			normal = append(normal, i)
		}
	}
	if len(normal) == 0 || len(gold) == 0 {
		panic("steadycatch: empty phrase pool")
	}
	return values, normal, gold
}
func Catalog() []Phrase { return slices.Clone(phrases) }

type Input struct {
	Tick      int  `json:"tick"`
	Target    int  `json:"target"`
	Direction int  `json:"direction"`
	Shield    bool `json:"shield,omitempty"`
}
type Item struct {
	ID      int    `json:"id"`
	Kind    string `json:"kind"`
	Payload int    `json:"payload"`
	X       int    `json:"x"`
	Y       int    `json:"y"`
	Width   int    `json:"width"`
	Height  int    `json:"height"`
	Speed   int    `json:"speed"`
	Checked bool   `json:"checked,omitempty"`
}
type Effects struct {
	Shield int `json:"shield"`
	Slow   int `json:"slow"`
	Magnet int `json:"magnet"`
	Double int `json:"double"`
}
type State struct {
	Version      int     `json:"version"`
	Tick         int     `json:"tick"`
	Score        int     `json:"score"`
	HP           int     `json:"hp"`
	Combo        int     `json:"combo"`
	MaxCombo     int     `json:"max_combo"`
	Caught       int     `json:"caught"`
	Missed       int     `json:"missed"`
	Hits         int     `json:"hits"`
	Charge       int     `json:"charge"`
	X            int     `json:"x"`
	Target       int     `json:"target"`
	Direction    int     `json:"direction"`
	Invulnerable int     `json:"invulnerable"`
	Effects      Effects `json:"effects"`
	SpawnIn      int     `json:"spawn_in"`
	Serial       int     `json:"serial"`
	RNG          uint32  `json:"rng"`
	NormalBag    []int   `json:"normal_bag"`
	GoldBag      []int   `json:"gold_bag"`
	Items        []Item  `json:"items"`
	Cause        string  `json:"cause"`
}

func New(seed uint32) State {
	if seed == 0 {
		seed = 0x9e3779b9
	}
	return State{Version: Version, HP: 5, X: Width / 2, Target: Width / 2, SpawnIn: 33, RNG: seed,
		NormalBag: []int{}, GoldBag: []int{}, Items: []Item{}}
}
func (s State) Finished() bool { return s.Cause != "" }
func (s State) Cleared() bool  { return s.Cause == "time" && s.HP > 0 && s.Score >= Goal }

// Advance checks the entire batch before changing the state. Inputs describe
// controls, never positions, collisions or scores supplied by the client.
func Advance(s State, inputs []Input, until int) (State, error) {
	if s.Version != Version || s.Finished() || until <= s.Tick || until > LastTick || until-s.Tick > MaxBatchTicks || len(inputs) > MaxBatchTicks {
		return State{}, ErrInput
	}
	previous := s.Tick
	for _, input := range inputs {
		if input.Tick <= previous || input.Tick > until || input.Target < 0 || input.Target > Width || input.Direction < -1 || input.Direction > 1 {
			return State{}, ErrInput
		}
		previous = input.Tick
	}
	s.Items = slices.Clone(s.Items)
	s.NormalBag = slices.Clone(s.NormalBag)
	s.GoldBag = slices.Clone(s.GoldBag)
	index := 0
	for s.Tick < until && !s.Finished() {
		s.Tick++
		if index < len(inputs) && inputs[index].Tick == s.Tick {
			input := inputs[index]
			index++
			s.Target, s.Direction = input.Target, input.Direction
			if input.Shield && s.Charge >= 10 {
				s.Charge = 0
				s.Effects.Shield = max(s.Effects.Shield, s.Tick+4*Hz)
			}
		}
		s.step()
	}
	return s, nil
}
func clamp(value, low, high int) int { return min(high, max(low, value)) }
func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
func (s *State) random(n int) int {
	x := s.RNG
	x ^= x << 13
	x ^= x >> 17
	x ^= x << 5
	s.RNG = x
	return int(x % uint32(n))
}
func (s *State) phrase() int {
	// Five gold phrases out of the original 96; expanding the normal pool
	// must not silently reduce the chance of a gold drop.
	bag, pool := &s.NormalBag, normalPool
	if s.random(96) < 5 {
		bag, pool = &s.GoldBag, goldPool
	}
	if len(*bag) == 0 {
		*bag = slices.Clone(pool)
		for i := len(*bag) - 1; i > 0; i-- {
			j := s.random(i + 1)
			(*bag)[i], (*bag)[j] = (*bag)[j], (*bag)[i]
		}
	}
	last := len(*bag) - 1
	value := (*bag)[last]
	*bag = (*bag)[:last]
	return value
}
func (s *State) spawn() {
	n := s.Serial
	item := Item{ID: n + 1, Kind: "phrase", Width: 152_000, Height: 62_000, Y: -80_000}
	chance := 18
	if s.Tick >= 30*Hz {
		chance = 25
	}
	if n >= 4 && n%13 == 8 {
		item.Kind, item.Payload, item.Width = "prop", n/13%5, 160_000
	} else if n >= 4 && s.random(100) < chance {
		item.Kind, item.Payload, item.Width = "hazard", s.random(6), 128_000
	} else {
		item.Payload = s.phrase()
	}
	item.X = Width / 2
	if n > 0 {
		item.X = s.random(Width-100_000) + 50_000
	}
	item.X = clamp(item.X, item.Width/2+10_000, Width-item.Width/2-10_000)
	// Frozen speed at spawn; all intermediates fit exactly in a JS integer.
	item.Speed = (CatchY + 80_000) * 2 * (54_000 + 7*s.Tick) / (9 * Hz * 54_000)
	if item.Kind == "hazard" {
		for attempt := 0; attempt < 10; attempt++ {
			blocked := false
			for _, other := range s.Items {
				if other.Y < 85_000 && other.Kind != "prop" && abs(other.X-item.X) < (other.Width+item.Width)/2+30_000 {
					blocked = true
					break
				}
			}
			if !blocked {
				break
			}
			item.X = clamp(s.random(Width), item.Width/2+10_000, Width-item.Width/2-10_000)
			if attempt == 9 {
				item.Kind, item.Payload = "phrase", s.phrase()
			}
		}
	}
	s.Serial++
	s.Items = append(s.Items, item)
}
func (s *State) collect(item Item) {
	switch item.Kind {
	case "phrase":
		s.Combo++
		s.MaxCombo = max(s.MaxCombo, s.Combo)
		s.Caught++
		s.Charge = min(10, s.Charge+1)
		base := 10
		if phrases[item.Payload].Gold {
			base = 20
		}
		points := base * (1 + min(2, s.Combo/5))
		if s.Effects.Double > s.Tick {
			points *= 2
		}
		s.Score += points
	case "hazard":
		if s.Effects.Shield > s.Tick || s.Invulnerable > s.Tick {
			return
		}
		s.HP--
		s.Combo = 0
		s.Hits++
		s.Invulnerable = s.Tick + 66
		if s.HP == 0 {
			s.Cause = "hp"
		}
	case "prop":
		switch item.Payload {
		case 0:
			s.Effects.Shield = max(s.Effects.Shield, s.Tick) + 7*Hz
		case 1:
			s.Effects.Slow = max(s.Effects.Slow, s.Tick) + 7*Hz
		case 2:
			s.Effects.Magnet = max(s.Effects.Magnet, s.Tick) + 7*Hz
		case 3:
			s.Effects.Double = max(s.Effects.Double, s.Tick) + 7*Hz
		case 4:
			s.HP = min(5, s.HP+1)
		}
	}
}
func (s *State) step() {
	if s.Direction != 0 {
		s.X = clamp(s.X+s.Direction*9_000, PadHalf+12_000, Width-PadHalf-12_000)
		s.Target = s.X
	} else {
		s.X = clamp(s.X+clamp(s.Target-s.X, -15_000, 15_000), PadHalf+12_000, Width-PadHalf-12_000)
	}
	s.SpawnIn--
	if s.SpawnIn <= 0 && s.Tick < LastTick-2*Hz {
		s.spawn()
		s.SpawnIn = (518_400 - 41*s.Tick + 8_999) / 9_000
	}
	remaining := s.Items[:0]
	for _, item := range s.Items {
		if s.Finished() {
			remaining = append(remaining, item)
			continue
		}
		before := item.Y
		speed := item.Speed
		if s.Effects.Slow > s.Tick {
			speed = speed * 58 / 100
		}
		item.Y += speed
		if s.Effects.Magnet > s.Tick && item.Kind == "phrase" && item.Y > CatchY-145_000 && abs(item.X-s.X) < 145_000 {
			item.X += (s.X - item.X) * 8 / 150
		}
		if !item.Checked && before+item.Height/2 < CatchY && item.Y+item.Height/2 >= CatchY {
			item.Checked = true
			if abs(item.X-s.X) < item.Width/2+PadHalf {
				s.collect(item)
				continue
			}
			if item.Kind == "phrase" {
				s.Combo = 0
				s.Missed++
			}
		}
		if item.Y-item.Height/2 < Height+10_000 {
			remaining = append(remaining, item)
		}
	}
	s.Items = remaining
	if !s.Finished() && s.Tick >= LastTick {
		s.Cause = "time"
	}
}
