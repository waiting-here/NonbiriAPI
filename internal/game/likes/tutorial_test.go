package likes

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/game/likes/catalog"
	"github.com/waiting-here/NonbiriAPI/internal/game/likes/engine"
)

// The browser tutorial is an offline replay of the same rules and presentation
// used by live games. A catalog change must regenerate and review this route.
func TestTutorialCompletesWithNarrowVictory(t *testing.T) {
	e, err := engine.New("quick")
	if err != nil {
		t.Fatal(err)
	}
	harness := "H01"
	selections := [2]engine.Selection{
		{Role: "ChatGPT", Harness: &harness, Skills: []string{"PUB42", "GPT01", "GPT41", "GPT44", "GPT61"}},
		{Role: "Claude", Harness: &harness, Skills: []string{"CLA01", "CLA22", "CLA61", "PUB21"}},
	}
	s, err := e.Create(selections)
	if err != nil {
		t.Fatal(err)
	}
	c, err := catalog.Public("quick")
	if err != nil {
		t.Fatal(err)
	}
	type tutorialRound struct {
		Before  engine.View    `json:"before"`
		After   engine.View    `json:"after"`
		Plans   [2]engine.Plan `json:"plans"`
		Summary presentation   `json:"summary"`
		Seconds int64          `json:"seconds"`
		Start   []engine.Event `json:"start"`
	}
	fixture := struct {
		ContentHash string           `json:"content_hash"`
		Selection   engine.Selection `json:"selection"`
		Rounds      []tutorialRound  `json:"rounds"`
	}{c.ContentHash, selections[0], []tutorialRound{}}
	playerSkills := []string{"GPT01", "GPT41", "GPT01", "GPT41", "GPT61", "PUB42", "GPT44", "GPT01", "GPT01", "GPT01"}
	botSkills := []string{"CLA22", "PUB21", "CLA61", "CLA01", "CLA22", "CLA01", "PUB21", "CLA01", "CLA01", "CLA22"}
	scores := [][2]int64{{4, 6}, {8, 14}, {12, 28}, {16, 32}, {34, 38}, {42, 42}, {42, 50}, {50, 54}, {58, 58}, {66, 64}}
	energy := []int64{178, 151, 86, 271, 194, 154, 142, 112, 82, 30}
	api := []int64{300, 160, 160, 20, 20, 20, 20, 20, 20, 20}
	gold := []int64{120, 120, 100, 80, 80, 60, 60, 60, 60, 60}
	start := []engine.Event{}
	for i := range playerSkills {
		before, err := e.Project(s, 0, false, nil)
		if err != nil {
			t.Fatal(err)
		}
		plans := [2]engine.Plan{engine.EmptyPlan(), engine.EmptyPlan()}
		plans[0].Main = &engine.Choice{SkillID: playerSkills[i], Pay: "auto"}
		plans[1].Main = &engine.Choice{SkillID: botSkills[i], Pay: "auto"}
		switch i {
		case 0:
			plans[0].Main.Pay = "api"
			plans[1].Purchases = []engine.Purchase{{Item: "sub"}}
		case 1:
			plans[0].Main.Targets = []string{"B18:原版"}
		case 2:
			plans[0].Purchases = []engine.Purchase{{Item: "sub"}}
			plans[1].Purchases = []engine.Purchase{{Item: "api"}}
		case 3:
			plans[0].Purchases = []engine.Purchase{{Item: "charge"}}
			plans[1].Purchases = []engine.Purchase{{Item: "charge"}}
			plans[0].Main.Targets = []string{"B20:原版"}
		}
		for seat := range 2 {
			if _, err := e.ValidateManualPlan(s, seat, plans[seat]); err != nil {
				t.Fatalf("round %d seat %d: %v", i+1, seat, err)
			}
		}
		next, record, err := e.Resolve(s, plans, func(int) (int, error) { t.Fatal("tutorial unexpectedly uses randomness"); return 0, nil })
		if err != nil {
			t.Fatalf("round %d: %v", i+1, err)
		}
		if [2]int64{next.Players[0].Likes, next.Players[1].Likes} != scores[i] || next.Energy != energy[i] || next.Players[0].API != api[i] || next.Players[0].Gold != gold[i] {
			t.Fatalf("round %d: unexpected scores/resources: %+v", i+1, next)
		}
		if i < 9 && next.Result != nil {
			t.Fatalf("early end on round %d", i+1)
		}
		if i == 5 && (next.Players[0].Burst != 800 || next.Players[0].Sub != 1600 || next.Players[0].Resources["R_IMAGE"] != 4) {
			t.Fatal("reset did not refill teaching resources")
		}
		after, err := e.Project(next, 0, next.Result != nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		summary := present(record)
		body, _ := json.Marshal(summary)
		seconds, err := (&Rules{}).PresentationDuration(body)
		if err != nil {
			t.Fatal(err)
		}
		fixture.Rounds = append(fixture.Rounds, tutorialRound{before, after, plans, summary, seconds, start})
		s = next
		if i < 9 {
			s, start, err = e.BeginNextRound(s)
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	if s.Result == nil || s.Result.Winner == nil || *s.Result.Winner != 0 || s.Result.Reason != "target" {
		t.Fatal("tutorial must end with the player winning")
	}
	data, err := json.Marshal(fixture)
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, '\n')
	if len(data) > 400_000 {
		t.Fatal("tutorial fixture exceeds budget")
	}
	if path := os.Getenv("GAME_TUTORIAL_FIXTURE_OUT"); path != "" {
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
	} else {
		checked, err := os.ReadFile("../../../web/src/user/games/likes/tutorial/authority.json")
		if err != nil || !bytes.Equal(data, checked) {
			t.Fatal("tutorial differs from authoritative replay", err)
		}
	}
}
