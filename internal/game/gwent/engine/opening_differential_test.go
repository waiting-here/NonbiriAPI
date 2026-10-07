package engine

import (
	"compress/gzip"
	"encoding/json"
	"os"
	"testing"
)

// The reference deals its ten cards sequentially: the source's parallel draw
// loop captures the same top card before removal. Pre-round counters are mapped
// to the upcoming first round; shuffle, redraw and initiative code is unchanged.
func TestOriginalOpeningsAndMulligans(t *testing.T) {
	f, err := os.Open("testdata/original-openings.json.gz")
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
		Name    string
		Decks   [2]Deck
		Roll    float64
		Actions []struct {
			Seat       int
			Kind, Card string
		}
		Expected []json.RawMessage
	}
	if err = json.NewDecoder(reader).Decode(&cases); err != nil {
		t.Fatal(err)
	}
	for _, test := range cases {
		t.Run(test.Name, func(t *testing.T) {
			random := fractionRandom(test.Roll)
			s, err := New(test.Decks, random)
			if err != nil {
				t.Fatal(err)
			}
			assertOriginalSnapshot(t, s, test.Expected[0], 0)
			for step, command := range test.Actions {
				found := false
				for _, a := range s.Legal(command.Seat) {
					if a.Kind != command.Kind || command.Card != "" && (a.Card == 0 || s.definition(a.Card).ID != command.Card) {
						continue
					}
					s, err = Apply(s, command.Seat, a, random)
					if err != nil {
						t.Fatal(err)
					}
					found = true
					break
				}
				if !found {
					t.Fatalf("step %d action unavailable", step+1)
				}
				assertOriginalSnapshot(t, s, test.Expected[step+1], step+1)
			}
		})
	}
}
