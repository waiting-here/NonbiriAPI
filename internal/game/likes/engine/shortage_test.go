package engine

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestOverloadRecordsActualPaymentResources(t *testing.T) {
	for _, tc := range []struct {
		name, role, skill, pay, payment string
		burst, sub, api, trial          int64
		want                            []ResourceShortage
	}{
		{"mixed", "ChatGPT", "GPT01", "", "mix", 20, 800, 10, 0, []ResourceShortage{{"burst", 90, 20}, {"api", 80, 10}}},
		{"total-bound", "ChatGPT", "GPT01", "", "mix", 80, 10, 0, 0, []ResourceShortage{{"burst", 100, 80}, {"api", 90, 0}, {"sub", 100, 10}}},
		{"manual-api", "ChatGPT", "GPT01", "api", "api", 400, 800, 10, 0, []ResourceShortage{{"api", 100, 10}}},
		{"api-only", "DeepSeek", "DS01", "", "api", 0, 0, 10, 0, []ResourceShortage{{"api", 100, 10}}},
		{"subscription-burst", "ChatGPT", "PUB02", "", "sub", 400, 800, 900, 0, []ResourceShortage{{"burst", 420, 400}}},
		{"subscription-both", "ChatGPT", "PUB02", "", "sub", 100, 200, 900, 0, []ResourceShortage{{"burst", 420, 100}, {"sub", 420, 200}}},
		{"subscription-total", "ChatGPT", "GPT42", "", "sub", 400, 100, 900, 0, []ResourceShortage{{"sub", 180, 100}}},
		{"trial", "ChatGPT", "GPT01", "api", "api", 400, 800, 10, 50, []ResourceShortage{{"api", 50, 10}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			selection := Selection{Role: tc.role, Skills: []string{"PUB01", tc.skill}}
			if tc.trial > 0 {
				selection.Harness = ptr("H04")
			}
			e, s := fixture(t, "quick", selection)
			p := &s.Players[0]
			p.Burst, p.Sub, p.API = tc.burst, tc.sub, tc.api
			if tc.trial > 0 {
				p.Trial = ptr(tc.trial)
			}
			choice := plan(tc.skill)
			choice.Main.Pay = tc.pay
			next, record, err := e.Resolve(s, [2]Plan{choice, plan("")}, nil)
			if err != nil {
				t.Fatal(err)
			}
			if !hasStatus(next.Players[0], "OVERLOAD") {
				t.Fatal("missing overload")
			}
			found := false
			for _, event := range record.Events {
				if event.Kind != "overload" {
					continue
				}
				found = true
				got := event.Data["shortage"].(Shortage)
				if got.Payment != tc.payment || !reflect.DeepEqual(got.Resources, tc.want) {
					t.Fatalf("got %+v, want %s %+v", got, tc.payment, tc.want)
				}
				body, _ := json.Marshal(event)
				if len(body) > 1024 {
					t.Fatal("unbounded shortage event")
				}
			}
			if !found {
				t.Fatal("missing shortage event")
			}
		})
	}
}

func TestExtraSkillShortageUsesBalanceAfterMainPayment(t *testing.T) {
	e, s := fixture(t, "quick", Selection{Role: "Gemini", Skills: []string{"GEM41", "GEM01"}})
	s.Players[0].Burst, s.Players[0].Sub, s.Players[0].API = 0, 0, 200
	p := plan("GEM41")
	p.Extra = []Choice{{SkillID: "GEM01"}}
	_, record, err := e.Resolve(s, [2]Plan{p, plan("")}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range record.Events {
		if event.Kind == "overload" {
			got := event.Data["shortage"].(Shortage)
			if event.Data["skillId"] != "GEM01" || !reflect.DeepEqual(got.Resources, []ResourceShortage{{"burst", 110, 0}, {"api", 110, 0}}) {
				t.Fatal(event)
			}
			return
		}
	}
	t.Fatal("missing extra-skill overload")
}
