package engine

import (
	"compress/gzip"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"testing"
)

func TestSavedBrowserCheckpoints(t *testing.T) {
	file, err := os.Open("testdata/browser-parity.json.gz")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	reader, err := gzip.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	var cases []struct {
		Seed   uint32
		Inputs []Input
		States []State
	}
	if err = json.NewDecoder(reader).Decode(&cases); err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		s := New(c.Seed)
		for _, want := range c.States {
			var inputs []Input
			for _, in := range c.Inputs {
				if in.Tick > s.Tick && in.Tick <= want.Tick {
					inputs = append(inputs, in)
				}
			}
			s, err = Advance(s, inputs, want.Tick)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(s, want) {
				t.Fatalf("seed %d at tick %d changed", c.Seed, want.Tick)
			}
		}
	}
}

func falling(kind string, payload, x int) Item {
	return Item{Kind: kind, Payload: payload, X: x, Y: CatchY - 32_000, Width: 152_000, Height: 62_000, Speed: 2_000}
}
func next(t *testing.T, s State, input ...Input) State {
	t.Helper()
	got, err := Advance(s, input, s.Tick+1)
	if err != nil {
		t.Fatal(err)
	}
	return got
}
func TestCollectMissAndCombo(t *testing.T) {
	s := New(1)
	s.Combo = 4
	s.Items = []Item{falling("phrase", normalPool[0], s.X)}
	s = next(t, s)
	if s.Score != 20 || s.Combo != 5 || s.Caught != 1 || s.Charge != 1 {
		t.Fatalf("catch=%+v", s)
	}
	s.Items = []Item{falling("phrase", normalPool[0], 55_000)}
	s = next(t, s)
	if s.Combo != 0 || s.Missed != 1 || s.Score != 20 {
		t.Fatalf("miss=%+v", s)
	}
	s.Combo = 9
	s.Effects.Double = s.Tick + 10
	s.Items = []Item{falling("phrase", goldPool[0], s.X)}
	s = next(t, s)
	if s.Score != 140 || s.MaxCombo != 10 {
		t.Fatalf("gold=%+v", s)
	}
}
func TestShieldDamageAndProps(t *testing.T) {
	s := New(1)
	s.Charge = 10
	s.Items = []Item{falling("hazard", 0, s.X)}
	s = next(t, s, Input{Tick: 1, Target: s.X, Shield: true})
	if s.HP != 5 || s.Charge != 0 || s.Effects.Shield != 241 {
		t.Fatalf("shield=%+v", s)
	}
	s.Effects.Shield = 0
	s.Combo = 7
	s.Items = []Item{falling("hazard", 0, s.X), falling("hazard", 1, s.X)}
	s = next(t, s)
	if s.HP != 4 || s.Hits != 1 || s.Combo != 0 {
		t.Fatalf("damage=%+v", s)
	}
	s.Items = []Item{falling("prop", 4, s.X)}
	s = next(t, s)
	if s.HP != 5 {
		t.Fatal("heal")
	}
	s.Effects.Slow = 100
	s.Items = []Item{falling("prop", 1, s.X)}
	s.Items[0].Y += 950
	s = next(t, s)
	if s.Effects.Slow != 520 {
		t.Fatalf("extension=%d", s.Effects.Slow)
	}
}
func TestControlSpeedAndInvalidBatchesAreAtomic(t *testing.T) {
	s := New(1)
	s = next(t, s, Input{Tick: 1, Direction: 1, Target: 0})
	if s.X != 309_000 || s.Target != s.X {
		t.Fatalf("keyboard=%d", s.X)
	}
	s = next(t, s, Input{Tick: 2, Direction: 0, Target: Width})
	if s.X != 324_000 {
		t.Fatalf("pointer=%d", s.X)
	}
	before := s
	for _, inputs := range [][]Input{{{Tick: 2}}, {{Tick: 3, Target: Width + 1}}, {{Tick: 3, Direction: 2}}, {{Tick: 3}, {Tick: 3}}} {
		if _, err := Advance(s, inputs, 4); !errors.Is(err, ErrInput) {
			t.Fatal("invalid batch accepted")
		}
	}
	if !reflect.DeepEqual(s, before) {
		t.Fatal("invalid input mutated prior state")
	}
	if _, err := Advance(s, nil, s.Tick+301); !errors.Is(err, ErrInput) {
		t.Fatal("unbounded batch accepted")
	}
}
func TestBatchingDoesNotChangeGameAndStopsAtTerminal(t *testing.T) {
	one, bulk := New(42), New(42)
	for !one.Finished() {
		one = next(t, one)
	}
	for !bulk.Finished() {
		var err error
		bulk, err = Advance(bulk, nil, min(LastTick, bulk.Tick+300))
		if err != nil {
			t.Fatal(err)
		}
	}
	if !reflect.DeepEqual(one, bulk) {
		t.Fatalf("batch-dependent score %d/%d", one.Score, bulk.Score)
	}
	if bulk.Tick > LastTick || bulk.HP < 0 || bulk.Serial > 125 {
		t.Fatalf("unbounded state=%+v", bulk)
	}
	if _, err := Advance(bulk, nil, LastTick); !errors.Is(err, ErrInput) {
		t.Fatal("terminal accepted input")
	}
}
func TestDeathAndClearBoundary(t *testing.T) {
	s := New(1)
	s.HP = 1
	s.Tick = LastTick - 1
	s.Score = Goal
	s.Items = []Item{falling("hazard", 0, s.X)}
	s = next(t, s)
	if s.Cause != "hp" || s.Cleared() {
		t.Fatal("death awarded clear")
	}
	s = New(1)
	s.Tick = LastTick - 1
	s.Score = Goal
	s = next(t, s)
	if !s.Cleared() {
		t.Fatal("surviving goal rejected")
	}
	s.Score = Goal - 1
	if s.Cleared() {
		t.Fatal("under-goal awarded clear")
	}
}

func BenchmarkFullRound(b *testing.B) {
	for b.Loop() {
		s := New(17)
		for !s.Finished() {
			var err error
			s, err = Advance(s, nil, min(LastTick, s.Tick+300))
			if err != nil {
				b.Fatal(err)
			}
		}
	}
}
