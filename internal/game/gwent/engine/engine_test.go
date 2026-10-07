package engine

import (
	"bytes"
	"encoding/json"
	"io"
	"reflect"
	"slices"
	"testing"
)

type zeroRandom struct{}

func (zeroRandom) Read(p []byte) (int, error) { clear(p); return len(p), nil }
func (zeroRandom) Uint64n(n uint64) (uint64, error) {
	if n == 0 {
		return 0, ErrInvalid
	}
	return 0, nil
}

func fixture() State {
	s := State{Version: Version, Stage: "playing", Round: 1, Cards: []Card{}, Weather: []int{}, Queue: []Effect{}, Rounds: []RoundRecord{}, Events: []Event{}, GrowthTriggers: []int{}, Mustering: []string{}}
	for seat, faction := range []string{"openai", "claude"} {
		s.Players[seat] = Player{Faction: faction, Lives: 2, LeaderUsed: true, Hand: []int{}, Deck: []int{}, Grave: []int{}, KnownTop: []int{}}
		s.Players[seat].Leader = s.makeCard(faction+"_leader", seat)
		for row := range 3 {
			s.Board[seat][row].Cards = []int{}
		}
	}
	return s
}
func hand(s *State, seat int, defs ...string) []int {
	var ids []int
	for _, def := range defs {
		id := s.makeCard(def, seat)
		s.addHand(seat, id, 0)
		ids = append(ids, id)
	}
	return ids
}
func field(s *State, seat, row int, defs ...string) []int {
	ids := hand(s, seat, defs...)
	for _, id := range ids {
		s.detach(id)
		s.Board[seat][row].Cards = s.insertSorted(s.Board[seat][row].Cards, id)
	}
	s.refreshAll()
	return ids
}
func apply(t *testing.T, s State, seat int, a Action) State {
	t.Helper()
	out, err := Apply(s, seat, a, zeroRandom{})
	if err != nil {
		t.Fatalf("%+v, %s: %v", a, s.Phase(), err)
	}
	return out
}
func TestCatalogAndCompleteGames(t *testing.T) {
	if len(definitions) != 232 {
		t.Fatalf("catalog definitions: %d", len(definitions))
	}
	for i, faction := range factions {
		t.Run(faction, func(t *testing.T) {
			decks := [2]Deck{StarterDeck(faction), StarterDeck(factions[(i+1)%len(factions)])}
			s, err := New(decks, zeroRandom{})
			if err != nil {
				t.Fatal(err)
			}
			for step := 0; s.Result == nil; step++ {
				if step > 700 {
					t.Fatal("game did not finish")
				}
				seat := 0
				if !s.Required()[seat] {
					seat = 1
				}
				legal := s.Legal(seat)
				if len(legal) == 0 {
					t.Fatal("no legal action")
				}
				a := legal[0]
				if s.waiting() {
					for _, candidate := range legal {
						if candidate.Kind == "continue" {
							a = candidate
							break
						}
					}
				}
				raw, _ := json.Marshal(s)
				restored, err := Decode(raw)
				if err != nil {
					t.Fatal(err)
				}
				before := apply(t, s, seat, a)
				after := apply(t, restored, seat, a)
				if !reflect.DeepEqual(before, after) {
					t.Fatal("restore changed transition")
				}
				s = after
			}
			if len(s.Rounds) < 2 || len(s.Rounds) > 3 {
				t.Fatalf("rounds: %d", len(s.Rounds))
			}
		})
	}
}

func TestDurableStateRejectsUnsupportedVersionAndBrokenReferences(t *testing.T) {
	valid, err := New([2]Deck{StarterDeck("openai"), StarterDeck("claude")}, zeroRandom{})
	if err != nil {
		t.Fatal(err)
	}
	for name, change := range map[string]func(*State){
		"version":        func(s *State) { s.Version++ },
		"card reference": func(s *State) { s.Players[0].Hand[0] = len(s.Cards) + 1 },
		"duplicate zone": func(s *State) { s.Players[1].Hand = append(s.Players[1].Hand, s.Players[0].Hand[0]) },
		"definition":     func(s *State) { s.Cards[0].Definition = "unknown" },
	} {
		t.Run(name, func(t *testing.T) {
			raw, _ := json.Marshal(valid)
			s, err := Decode(raw)
			if err != nil {
				t.Fatal(err)
			}
			change(&s)
			raw, _ = json.Marshal(s)
			if _, err := Decode(raw); err != ErrState {
				t.Fatalf("error: %v", err)
			}
		})
	}
}

func BenchmarkApplyCard(b *testing.B) {
	s := fixture()
	s.Players[1].LeaderUsed = false
	id := hand(&s, 0, "openai_cluster")[0]
	a := Action{Kind: "play", Card: id, Row: "ranged"}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := Apply(s, 0, a, zeroRandom{}); err != nil {
			b.Fatal(err)
		}
	}
}
func TestWeatherBondMoraleHornAndHeroOrder(t *testing.T) {
	s := fixture()
	ids := field(&s, 0, 1, "openai_cluster", "openai_cluster", "openai_alignment", "openai_sunburst_hero")
	horn := s.makeCard("compute_surge", 0)
	s.Board[0][1].Special = horn
	s.Weather = []int{s.makeCard("data_noise", 1)}
	s.refreshAll()
	want := []int{6, 6, 2, 14}
	for i, id := range ids {
		if s.Power(id) != want[i] {
			t.Fatalf("%s=%d want %d", s.definition(id).ID, s.Power(id), want[i])
		}
	}
	s.clearWeather()
	if s.Power(ids[0]) != 18 {
		t.Fatal("weather removal did not restore base before multipliers")
	}
}
func TestSpyChangesHolderAndDecoyRetrievesIt(t *testing.T) {
	s := fixture()
	s.Players[0].LeaderUsed = false
	s.Players[1].LeaderUsed = false
	spy := hand(&s, 0, "openai_scout")[0]
	decoy := hand(&s, 1, "circuit_breaker")[0]
	s.Players[0].Deck = []int{s.makeCard("openai_alignment", 0), s.makeCard("openai_cluster", 0)}
	s = apply(t, s, 0, Action{Kind: "play", Card: spy, Row: "close"})
	if s.card(spy).Owner != 1 || len(s.Players[0].Hand) != 2 || s.Scores()[1] != 5 {
		t.Fatal("spy ownership/draw")
	}
	s = apply(t, s, 1, Action{Kind: "play", Card: decoy})
	s = apply(t, s, 1, Action{Kind: "choose", Card: spy})
	if !slices.Contains(s.Players[1].Hand, spy) || s.Scores()[1] != 0 {
		t.Fatal("decoy retrieval")
	}
}
func TestMedicContinuesThroughAgileRowChoice(t *testing.T) {
	s := fixture()
	s.Players[0].LeaderUsed = false
	s.Players[1].LeaderUsed = false
	var agile string
	for _, d := range definitions {
		if d.Type == "unit" && d.Row == "agile" {
			agile = d.ID
			break
		}
	}
	if agile == "" {
		t.Fatal("fixture")
	}
	medic := hand(&s, 0, "openai_restore")[0]
	resurrect := s.makeCard(agile, 0)
	s.Players[0].Grave = []int{resurrect}
	s = apply(t, s, 0, Action{Kind: "play", Card: medic, Row: "siege"})
	s = apply(t, s, 0, Action{Kind: "choose", Card: resurrect})
	if s.Choices[0] == nil || s.Choices[0].Kind != "row" || s.Turn != 0 {
		t.Fatal("lost nested choice")
	}
	raw, _ := json.Marshal(s)
	var restored State
	_ = json.Unmarshal(raw, &restored)
	s = apply(t, restored, 0, Action{Kind: "choose_row", Row: "ranged"})
	if !slices.Contains(s.Board[0][1].Cards, resurrect) || s.Turn != 1 || s.waiting() {
		t.Fatal("did not resume medic")
	}
}
func TestScorchProtectsAllTargetsWithOneShield(t *testing.T) {
	s := fixture()
	s.Players[0].LeaderUsed = false
	s.Players[1].LeaderUsed = false
	field(&s, 0, 0, "openai_scout")
	protected := field(&s, 1, 0, "claude_scout", "claude_scout")
	s.Players[1].Shield = true
	var scorch string
	for _, d := range definitions {
		if d.Type == "skill" && slices.Contains(d.Abilities, "scorch") {
			scorch = d.ID
			break
		}
	}
	id := hand(&s, 0, scorch)[0]
	s = apply(t, s, 0, Action{Kind: "play", Card: id})
	if len(s.Board[0][0].Cards) != 0 || !slices.Equal(s.Board[1][0].Cards, protected) || s.Players[1].Shield {
		t.Fatal("simultaneous scorch/shield")
	}
}
func TestCatalystAndAvengerForms(t *testing.T) {
	s := fixture()
	s.Players[0].LeaderUsed = false
	s.Players[1].LeaderUsed = false
	field(&s, 0, 1, "openai_infra_distiller")
	catalyst := hand(&s, 0, "openai_infra_vector")[0]
	s = apply(t, s, 0, Action{Kind: "play", Card: catalyst, Row: "ranged"})
	if len(s.Players[0].Grave) != 0 || s.RowTotal(0, 1) != 12 {
		t.Fatal("transformation")
	}
	s.Turn = 0
	guard := field(&s, 0, 0, "openai_infra_guard")[0]
	decoy := hand(&s, 0, "circuit_breaker")[0]
	s = apply(t, s, 0, Action{Kind: "play", Card: decoy})
	s = apply(t, s, 0, Action{Kind: "choose", Card: guard})
	if !slices.Contains(s.Players[0].Hand, guard) || s.RowTotal(0, 0) != 8 {
		t.Fatal("avenger after hand departure")
	}
	for _, id := range slices.Clone(s.Board[0][0].Cards) {
		s.leave(id, "grave")
	}
	for _, id := range s.Players[0].Grave {
		if s.definition(id).Generated {
			t.Fatal("ephemeral token entered grave")
		}
	}
}
func TestProjectionDoesNotExposeOpposingHandDeckOrChoices(t *testing.T) {
	s := fixture()
	hand(&s, 0, "openai_scout")
	hand(&s, 1, "claude_fable51_hero")
	hidden := s.makeCard("claude_opus55_hero", 1)
	s.Players[1].Deck = []int{hidden}
	s.Players[1].KnownTop = []int{hidden}
	s.Choices[1] = &Choice{Kind: "reveal", Options: []int{hidden}, CanQuit: true}
	view, err := Project(s, 0)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(view)
	for _, secret := range []string{"claude_fable51_hero", "claude_opus55_hero", "known_top", "queue"} {
		if bytes.Contains(raw, []byte(secret)) {
			t.Fatal("private projection:", secret)
		}
	}
	if view.Choice != nil || len(view.Legal) != 0 || view.Enemy.HandCount != 1 {
		t.Fatal("private choice")
	}
}

var _ io.Reader = zeroRandom{}
