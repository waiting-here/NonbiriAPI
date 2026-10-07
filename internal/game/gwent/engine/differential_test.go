package engine

import (
	"compress/gzip"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"
)

type originalFixture struct {
	Name              string
	Factions          [2]string
	Leaders           [2]string
	Hand, Deck, Grave [2][]string
	Shield            [2]bool
	Board             []struct {
		Seat    int
		Row     string
		Cards   []string
		Special string
	}
	Weather []string
	Actions []struct {
		Seat            int
		Kind, Card, Row string
	}
	Expected []json.RawMessage
}

func TestOriginalRuleTransitions(t *testing.T) {
	file, err := os.Open("testdata/original-transitions.json.gz")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	reader, err := gzip.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	var cases []originalFixture
	if err = json.NewDecoder(reader).Decode(&cases); err != nil {
		t.Fatal(err)
	}
	for _, test := range cases {
		t.Run(test.Name, func(t *testing.T) {
			s := originalInitial(test)
			assertOriginalSnapshot(t, s, test.Expected[0], 0)
			for step, command := range test.Actions {
				if command.Row == "weather" {
					command.Row = ""
				}
				var action *Action
				for _, candidate := range s.Legal(command.Seat) {
					if candidate.Kind != command.Kind || candidate.Row != command.Row {
						continue
					}
					if command.Card != "" && (candidate.Card == 0 || s.definition(candidate.Card).ID != command.Card) {
						continue
					}
					action = &candidate
					break
				}
				if action == nil {
					t.Fatalf("step %d: action unavailable: %+v", step+1, command)
				}
				// The fixture's next input always starts from the durable representation.
				body, _ := json.Marshal(s)
				restored, err := Decode(body)
				if err != nil {
					t.Fatal(err)
				}
				s = apply(t, restored, command.Seat, *action)
				assertOriginalSnapshot(t, s, test.Expected[step+1], step+1)
			}
		})
	}
}

func originalSnapshot(s State) map[string]any {
	card := func(id int) map[string]any {
		c := s.card(id)
		return map[string]any{"definition": c.Definition, "owner": c.Owner, "power": s.Power(id), "bonus": c.Bonus, "growth": c.Growth}
	}
	list := func(ids []int) []any {
		items := make([]any, 0, len(ids))
		for _, id := range ids {
			items = append(items, card(id))
		}
		sort.SliceStable(items, func(i, j int) bool {
			a, b := items[i].(map[string]any), items[j].(map[string]any)
			if a["definition"] != b["definition"] {
				return a["definition"].(string) < b["definition"].(string)
			}
			for _, field := range []string{"owner", "power", "bonus", "growth"} {
				if a[field] != b[field] {
					return fmt.Sprint(a[field]) < fmt.Sprint(b[field])
				}
			}
			return false
		})
		return items
	}
	defs := func(ids []int) []string {
		out := make([]string, 0, len(ids))
		for _, id := range ids {
			out = append(out, s.definition(id).ID)
		}
		return out
	}
	players, board, choices, roundsOut := []any{}, []any{}, []any{}, []any{}
	for seat, p := range s.Players {
		players = append(players, map[string]any{"lives": p.Lives, "passed": p.Passed, "boost": p.Boost, "shield": p.Shield, "leader_used": p.LeaderUsed, "hand": list(p.Hand), "deck": defs(p.Deck), "grave": list(p.Grave), "known": defs(p.KnownTop)})
		var side []any
		for row, r := range s.Board[seat] {
			special := ""
			if r.Special != 0 {
				special = s.definition(r.Special).ID
			}
			side = append(side, map[string]any{"total": s.RowTotal(seat, row), "cards": list(r.Cards), "special": special})
		}
		board = append(board, side)
		if c := s.Choices[seat]; c != nil {
			cards := defs(c.Options)
			slices.Sort(cards)
			rowNames := append([]string{}, c.Rows...)
			choices = append(choices, map[string]any{"remaining": c.Remaining, "can_quit": c.CanQuit || c.Remaining == 0, "cards": cards, "rows": rowNames})
		} else {
			choices = append(choices, nil)
		}
	}
	for _, r := range s.Rounds {
		roundsOut = append(roundsOut, map[string]any{"scores": r.Scores, "winner": r.Winner})
	}
	weather := defs(s.Weather)
	slices.Sort(weather)
	var result any
	if s.Result != nil {
		result = map[string]any{"winner": s.Result.Winner}
	}
	return map[string]any{"round": s.Round, "turn": s.Turn, "waiting": s.waiting(), "players": players, "board": board, "weather": weather, "choices": choices, "rounds": roundsOut, "result": result}
}
func assertOriginalSnapshot(t *testing.T, s State, want json.RawMessage, step int) {
	t.Helper()
	got, _ := json.Marshal(originalSnapshot(s))
	var a, b any
	if json.Unmarshal(got, &a) != nil || json.Unmarshal(want, &b) != nil {
		t.Fatal("fixture JSON")
	}
	if difference := firstDifference(a, b, "state"); difference != "" {
		t.Fatalf("step %d: %s", step, difference)
	}
}
func firstDifference(got, want any, path string) string {
	if reflect.DeepEqual(got, want) {
		return ""
	}
	switch a := got.(type) {
	case map[string]any:
		if b, ok := want.(map[string]any); ok {
			keys := make([]string, 0, len(a))
			for k := range a {
				keys = append(keys, k)
			}
			slices.Sort(keys)
			for _, k := range keys {
				if diff := firstDifference(a[k], b[k], path+"."+k); diff != "" {
					return diff
				}
			}
		}
	case []any:
		if b, ok := want.([]any); ok && len(a) == len(b) {
			for i := range a {
				if diff := firstDifference(a[i], b[i], fmt.Sprintf("%s[%d]", path, i)); diff != "" {
					return diff
				}
			}
		}
	}
	return strings.TrimSpace(fmt.Sprintf("%s: got %v; original %v", path, got, want))
}

func originalInitial(test originalFixture) State {
	s := fixture()
	for seat := range 2 {
		p := &s.Players[seat]
		p.Faction = test.Factions[seat]
		p.Leader = s.makeCard(test.Leaders[seat], seat)
		p.LeaderUsed = s.has(p.Leader, "leader_deepseek_rebirth")
		p.Shield = test.Shield[seat]
		hand(&s, seat, test.Hand[seat]...)
		for _, def := range test.Deck[seat] {
			p.Deck = append(p.Deck, s.makeCard(def, seat))
		}
		for _, def := range test.Grave[seat] {
			p.Grave = append(p.Grave, s.makeCard(def, seat))
		}
	}
	for _, row := range test.Board {
		ri := slices.Index(rows, row.Row)
		field(&s, row.Seat, ri, row.Cards...)
		if row.Special != "" {
			s.Board[row.Seat][ri].Special = s.makeCard(row.Special, row.Seat)
		}
	}
	for _, def := range test.Weather {
		s.Weather = append(s.Weather, s.makeCard(def, 1))
	}
	s.refreshAll()
	return s
}
