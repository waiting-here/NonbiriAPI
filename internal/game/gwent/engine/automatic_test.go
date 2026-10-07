package engine

import (
	"compress/gzip"
	"encoding/json"
	"os"
	"testing"
)

func TestOriginalAutomaticSkillResolution(t *testing.T) {
	f, err := os.Open("testdata/original-automatic.json.gz")
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
		Chosen   describedAction
		Expected json.RawMessage
	}
	if err = json.NewDecoder(reader).Decode(&cases); err != nil {
		t.Fatal(err)
	}
	for _, test := range cases {
		t.Run(test.Name, func(t *testing.T) {
			s := originalInitial(test.originalFixture)
			first := s.Round
			a, err := LocalDecision(s, 0, zeroRandom{})
			if err != nil {
				t.Fatal(err)
			}
			if got := describeAction(s, 0, a); got != test.Chosen {
				t.Fatalf("got %+v, original %+v", got, test.Chosen)
			}
			var choices AutomaticChoices
			for i := 0; i < 512; i++ {
				s = apply(t, s, 0, a)
				if s.Result != nil || s.Round != first || s.Choices[0] == nil {
					break
				}
				a, err = choices.Next(s, 0, zeroRandom{})
				if err != nil {
					t.Fatal(err)
				}
			}
			assertOriginalSnapshot(t, s, test.Expected, 1)
		})
	}
}
func BenchmarkLocalControllerRecovery(b *testing.B) {
	s := fixture()
	s.Players[0].Faction = "openai"
	for _, d := range definitions {
		if d.Generated || d.Type == "leader" || d.Type == "hero" || (d.Faction != "openai" && d.Faction != "neutral") {
			continue
		}
		for range d.MaxCopies {
			id := s.makeCard(d.ID, 0)
			if d.Type == "unit" {
				s.Players[0].Grave = append(s.Players[0].Grave, id)
			} else {
				s.Players[0].Hand = append(s.Players[0].Hand, id)
			}
		}
	}
	hand(&s, 0, "openai_restore")
	field(&s, 1, 0, "claude_agent")
	for b.Loop() {
		if _, err := LocalDecision(s, 0, zeroRandom{}); err != nil {
			b.Fatal(err)
		}
	}
}
