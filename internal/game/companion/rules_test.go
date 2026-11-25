package companion

import (
	"bytes"
	"encoding/json"
	"github.com/waiting-here/NonbiriAPI/internal/game/companion/kit"
	"testing"
)

func example(t *testing.T, w turn) Raw {
	t.Helper()
	if len(w.Space.Examples) == 0 {
		t.Fatalf("no legal choices in %s", w.Phase)
	}
	return w.Space.Examples[0]
}
func TestSeatWindowsAndDeterministicReplay(t *testing.T) {
	for _, test := range []struct{ game, mode, limits string }{{"bidding", "standard", "{}"}, {"likes", "quick", `{"role":"Claude","harness":null}`}, {"likes", "standard", "{}"}, {"gwent", "standard", `{"faction":"claude"}`}} {
		t.Run(test.game+"/"+test.mode, func(t *testing.T) {
			r, err := rulesFor(test.game, test.mode)
			if err != nil {
				t.Fatal(err)
			}
			fixture, err := kit.Catalog(test.game, test.mode)
			if err != nil || !member(r.catalog, []Raw{fixture}) {
				t.Fatal("decision catalog differs from downloadable SDK", err)
			}
			h := record{Initial: initial{Game: test.game, Mode: test.mode, Rules: r.hash, Human: 0, Constraints: Raw(test.limits), Program: "official", Difficulty: 1}, Chance: "test-seed", Actions: []window{}, Result: result{Status: "active"}}
			m := match{summary: summary{ID: "test", Game: test.game, Mode: test.mode, result: h.Result}, Record: h}
			m.Frame, err = r.begin(h)
			if err != nil {
				t.Fatal(err)
			}
			var humanViews []Raw
			last := -1
			for m.Status == "active" && len(m.Record.Actions) < 512 {
				step := len(m.Record.Actions)
				start, err := r.observe(m.Frame, &m.Record, 0, false)
				if err != nil {
					t.Fatal(err)
				}
				if last != step {
					humanViews = append(humanViews, encode(start.View))
					last = step
				}
				acting := 0
				if !start.Need[0] || m.Frame.Locked[0] != nil {
					acting = 1
				}
				a, err := r.observe(m.Frame, &m.Record, acting, false)
				if err != nil {
					t.Fatal(err)
				}
				before, err := r.observe(m.Frame, &m.Record, 1-acting, false)
				if err != nil {
					t.Fatal(err)
				}
				proposed := example(t, a)
				if err = m.apply(r, acting, proposed); err != nil {
					t.Fatal(err)
				}
				if step == len(m.Record.Actions) {
					after, err := r.observe(m.Frame, &m.Record, 1-acting, false)
					if err != nil {
						t.Fatal(err)
					}
					if !bytes.Equal(encode(before.View), encode(after.View)) {
						t.Fatal("opponent's sealed choice changed the seat projection")
					}
					if a.Phase == "setup" && string(encode(after.View)) != "{}" {
						t.Fatal("exposed blind setup")
					}
				}
			}
			if m.Status != "completed" {
				t.Fatalf("unfinished after %d windows", len(m.Record.Actions))
			}
			b := encode(m.Record)
			var persisted record
			if err = json.Unmarshal(b, &persisted); err != nil {
				t.Fatal(err)
			}
			for _, step := range []int{0, len(persisted.Actions) / 2, len(persisted.Actions)} {
				f, h, err := replayAt(t.Context(), persisted, step)
				if err != nil {
					t.Fatal(err)
				}
				if step == len(persisted.Actions) {
					if !bytes.Equal(encode(f), encode(m.Frame)) || !bytes.Equal(encode(h.Result), encode(m.result)) {
						t.Fatal("final replay differs")
					}
					continue
				}
				front, err := r.observe(f, &h, 0, false)
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(encode(front.View), humanViews[step]) {
					t.Fatalf("replay at %d reveals a different view", step)
				}
			}
			t.Logf("%d decision windows, %d replay bytes", len(persisted.Actions), len(b))
		})
	}
}
