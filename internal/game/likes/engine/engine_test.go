package engine

import (
	"encoding/json"
	"reflect"
	"testing"
)

func fixture(t *testing.T, mode string, selections ...Selection) (*Engine, State) {
	t.Helper()
	e, err := New(mode)
	if err != nil {
		t.Fatal(err)
	}
	seats := [2]Selection{{Role: "ChatGPT", Skills: []string{"PUB01", "GPT44"}}, {Role: "Claude", Skills: []string{"PUB01"}}}
	copy(seats[:], selections)
	s, err := e.Create(seats)
	if err != nil {
		t.Fatal(err)
	}
	return e, s
}
func plan(id string) Plan {
	p := EmptyPlan()
	if id != "" {
		p.Main = &Choice{SkillID: id}
	}
	return p
}

func TestRoundResolutionAndSeparateFullRoundStart(t *testing.T) {
	e, s := fixture(t, "quick")
	original := clone(s)
	next, record, err := e.Resolve(s, [2]Plan{plan("PUB01"), plan("PUB01")}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(s, original) {
		t.Fatal("resolution mutated the input")
	}
	if !next.AwaitingNextRound || next.Round != 1 || next.Energy != 190 || next.Players[0].Likes != 3 || next.Players[1].Likes != 3 || next.Players[0].Burst != 300 || next.Players[0].Sub != 700 {
		t.Fatalf("bad first round: %+v", next)
	}
	if len(record.Frames) != 7 || record.Plans[0].Main.SkillID != "PUB01" || len(record.Events) == 0 {
		t.Fatal("missing authoritative presentation")
	}
	if _, _, err := e.Resolve(next, [2]Plan{plan("PUB01"), plan("PUB01")}, nil); err == nil {
		t.Fatal("resolved round executed twice")
	}
	ready, events, err := e.BeginNextRound(next)
	if err != nil {
		t.Fatal(err)
	}
	if ready.AwaitingNextRound || ready.Round != 2 || ready.Players[0].NormalTurns != 2 || ready.Players[0].Burst != 300 || len(events) != 1 {
		t.Fatal("bad round-start transition")
	}
	if _, _, err := e.BeginNextRound(ready); err == nil {
		t.Fatal("round started twice")
	}
	next, _, err = e.Resolve(ready, [2]Plan{plan("PUB01"), plan("PUB01")}, nil)
	if err != nil {
		t.Fatal(err)
	}
	ready, _, err = e.BeginNextRound(next)
	if err != nil || ready.Players[0].Burst != 400 || ready.Players[0].Subscription.BurstResetAt != nil || ready.Players[0].Sub != 630 {
		t.Fatalf("subscription reset failed: %v %+v", err, ready.Players[0])
	}
	body, err := json.Marshal(record)
	if err != nil || len(body) >= MaxRoundBytes {
		t.Fatal("round record exceeded budget")
	}
}

func TestOnlyPositiveSharedEnergyQuoteOverloads(t *testing.T) {
	for positive := range 2 {
		seats := []Selection{{Role: "ChatGPT", Skills: []string{"PUB01", "GPT44"}}, {Role: "ChatGPT", Skills: []string{"PUB01", "GPT44"}}}
		e, s := fixture(t, "quick", seats...)
		s.Energy = 9
		plans := [2]Plan{plan("GPT44"), plan("GPT44")}
		plans[positive] = plan("PUB01")
		next, record, err := e.Resolve(s, plans, nil)
		if err != nil {
			t.Fatal(err)
		}
		if next.Result != nil || !hasStatus(next.Players[positive], "OVERLOAD") || hasStatus(next.Players[other(positive)], "OVERLOAD") || !hasStatus(next.Players[other(positive)], "SPEED_MODE") || next.Players[positive].Burst != s.Players[positive].Burst || next.Energy != 10 {
			t.Fatalf("zero-cost side affected: %+v", next)
		}
		casts := 0
		for _, event := range record.Events {
			if event.Kind == "overload" {
				shortage := event.Data["shortage"].(Shortage)
				if shortage.Payment != "energy" || len(shortage.Resources) != 1 || shortage.Resources[0] != (ResourceShortage{"energy", 10, 9}) {
					t.Fatalf("wrong shared shortage: %+v", shortage)
				}
			}
			if event.Kind == "cast" {
				casts++
				if optional(event.Seat, -1) != other(positive) {
					t.Fatal("overloaded skill executed")
				}
			}
		}
		if casts != 1 {
			t.Fatal("zero-cost skill did not execute")
		}
	}
}

func TestExactEnergyZeroQuotesAndDoubleOverload(t *testing.T) {
	e, s := fixture(t, "quick")
	s.Energy = 20
	exact, _, err := e.Resolve(s, [2]Plan{plan("PUB01"), plan("PUB01")}, nil)
	if err != nil || exact.Result != nil || exact.Energy != 10 || hasStatus(exact.Players[0], "OVERLOAD") {
		t.Fatalf("exact energy falsely overloaded: %v", err)
	}
	s.Energy = 19
	over, _, err := e.Resolve(s, [2]Plan{plan("PUB01"), plan("PUB01")}, nil)
	if err != nil || over.Result == nil || over.Result.Reason != "double-overload" || over.Energy != 0 || over.Players[0].Burst != 400 {
		t.Fatalf("double overload failed: %v", err)
	}
	s.Energy = 0
	zero, _, err := e.Resolve(s, [2]Plan{plan("GPT44"), plan("")}, nil)
	if err != nil || zero.Result != nil || hasStatus(zero.Players[0], "OVERLOAD") || hasStatus(zero.Players[1], "OVERLOAD") {
		t.Fatalf("zero quotes overloaded: %v", err)
	}
}
