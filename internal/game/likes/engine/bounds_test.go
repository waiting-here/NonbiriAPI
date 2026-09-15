package engine

import (
	"encoding/json"
	"testing"
)

func TestDenseRoundAndFullHistoryFitWithoutTruncation(t *testing.T) {
	for _, mode := range []string{"quick", "standard"} {
		e, s := fixture(t, mode, Selection{Role: "Gemini", Harness: ptr("H03"), Skills: []string{"PUB01", "GEM61"}}, Selection{Role: "Gemini", Harness: ptr("H03"), Skills: []string{"PUB01", "GEM61"}})
		for seat := range 2 {
			s.Players[seat].API = 1_000_000
			for _, buff := range e.c.Buffs {
				if buff.Kind == "OVERLOAD" || buff.Kind == "STUN" || buff.Kind == "SUBSCRIPTION_BAN" {
					continue
				}
				count := max(1, int(buff.Cap))
				if buff.Kind == "COMBO" {
					count = 2
				}
				initialBuff(e, &s, seat, buff.ID, count)
			}
		}
		s.Energy = e.param("ENERGY_CAP")
		p := plan("GEM61")
		p.Extra = []Choice{{SkillID: "GEM61"}}
		next, record, err := e.Resolve(s, [2]Plan{p, p}, nil)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := json.Marshal(record)
		if len(body) >= MaxRoundBytes {
			t.Fatal("dense round exceeds history limit")
		}
		view, err := e.Project(next, 0, next.Result != nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		live, _ := json.Marshal(view)
		if len(live) >= 256<<10 {
			t.Fatal("dense live view exceeds response limit")
		}
		casts := [2]int{}
		for _, event := range record.Events {
			if event.Kind == "cast" {
				casts[*event.Seat]++
			}
		}
		if casts[0] > MaxCastsPerSeat || casts[1] > MaxCastsPerSeat {
			t.Fatal("cast bound exceeded")
		}
		t.Logf("%s dense round=%d bytes live=%d bytes casts=%v", mode, len(body), len(live), casts)
		e, s = fixture(t, mode)
		total := 0
		for s.Result == nil {
			next, record, err := e.Resolve(s, [2]Plan{EmptyPlan(), EmptyPlan()}, nil)
			if err != nil {
				t.Fatal(err)
			}
			body, _ := json.Marshal(record)
			total += len(body)
			s = next
			if s.Result == nil {
				s, _, err = e.BeginNextRound(s)
				if err != nil {
					t.Fatal(err)
				}
			}
		}
		if s.Round != e.param("MAX_ROUNDS") || total >= 75*MaxRoundBytes {
			t.Fatal("full game history exceeds fixed bound")
		}
		t.Logf("%s full history=%d bytes rounds=%d", mode, total, s.Round)
	}
}
