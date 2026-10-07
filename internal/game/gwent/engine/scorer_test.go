package engine

import (
	"compress/gzip"
	"encoding/json"
	"math"
	"os"
	"reflect"
	"slices"
	"testing"
)

type describedAction struct {
	Kind  string
	Card  string
	Index int
	Row   string
}

func describeAction(s State, seat int, a Action) describedAction {
	out := describedAction{Kind: a.Kind, Index: -1, Row: a.Row}
	if a.Card != 0 {
		out.Card = s.definition(a.Card).ID
		out.Index = slices.Index(s.Players[seat].Hand, a.Card)
	}
	return out
}

type originalForecast struct {
	Seat    int
	Actions []struct {
		describedAction
		Weight, Immediate float64
	}
	Picks []describedAction
}
type fractionRandom float64

func (fractionRandom) Read(p []byte) (int, error) { clear(p); return len(p), nil }
func (r fractionRandom) Uint64n(n uint64) (uint64, error) {
	return uint64(float64(n) * float64(r)), nil
}
func TestOriginalControllerForecasts(t *testing.T) {
	f, err := os.Open("testdata/original-scoring.json.gz")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	reader, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	var cases []struct {
		originalFixture
		Forecasts []*originalForecast
	}
	if err := json.NewDecoder(reader).Decode(&cases); err != nil {
		t.Fatal(err)
	}
	for _, test := range cases {
		t.Run(test.Name, func(t *testing.T) {
			s := originalInitial(test.originalFixture)
			for step, want := range test.Forecasts {
				if want != nil {
					e := newScorer(&s, want.Seat, zeroRandom{})
					legal := s.Legal(want.Seat)
					if len(legal) != len(want.Actions) {
						t.Fatalf("step %d: %d legal actions, original %d", step, len(legal), len(want.Actions))
					}
					before, _ := json.Marshal(s)
					for i, action := range legal {
						expected := want.Actions[i]
						if got := describeAction(s, want.Seat, action); got != expected.describedAction {
							t.Fatalf("step %d action %d: got %+v; original %+v", step, i, got, expected.describedAction)
						}
						if got := e.weight(action); math.Abs(got-expected.Weight) > 1e-9 {
							t.Fatalf("step %d %+v weight %g; original %g", step, expected.describedAction, got, expected.Weight)
						}
						if got := e.immediate(action, normalForecast()); math.Abs(got-expected.Immediate) > 1e-9 {
							t.Fatalf("step %d %+v gain %g; original %g", step, expected.describedAction, got, expected.Immediate)
						}
					}
					for i, roll := range []float64{0, 0.314159, 0.99} {
						action, err := LocalDecision(s, want.Seat, fractionRandom(roll))
						if err != nil {
							t.Fatal(err)
						}
						if got := describeAction(s, want.Seat, action); got != want.Picks[i] {
							t.Fatalf("step %d roll %g: got %+v; original %+v", step, roll, got, want.Picks[i])
						}
					}
					after, _ := json.Marshal(s)
					if !reflect.DeepEqual(before, after) {
						t.Fatal("forecast mutated authoritative state")
					}
				}
				if step == len(test.Actions) {
					break
				}
				command := test.Actions[step]
				if command.Row == "weather" {
					command.Row = ""
				}
				found := false
				for _, a := range s.Legal(command.Seat) {
					if a.Kind == command.Kind && a.Row == command.Row && (command.Card == "" || a.Card != 0 && s.definition(a.Card).ID == command.Card) {
						s = apply(t, s, command.Seat, a)
						found = true
						break
					}
				}
				if !found {
					t.Fatalf("step %d fixture action unavailable", step)
				}
			}
		})
	}
}
