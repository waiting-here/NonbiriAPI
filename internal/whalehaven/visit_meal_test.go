package whalehaven

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

func visitAction(profile map[string]any, action string) map[string]any {
	revision := profile["snapshot"].(map[string]any)["revision"].(float64)
	return map[string]any{"type": action, "expected_profile_revision": strconv.Itoa(int(revision)), "expected_settings_revision": profile["settings_revision"]}
}

func TestDefaultVisitMealPaysBothKeepersOnce(t *testing.T) {
	for _, automatic := range []bool{false, true} {
		name := "manual, short polls"
		if automatic {
			name = "automatic, offline"
		}
		t.Run(name, func(t *testing.T) {
			f := newForagingFixture(t)
			if automatic {
				// Depart at 22:30 site time so the meal ends just before midnight.
				day := f.at.UTC().AddDate(0, 0, 1)
				f.at = time.Date(day.Year(), day.Month(), day.Day(), 14, 30, 0, 0, time.UTC)
			}
			start := f.at
			guest, host := f.user(t, "guest"), f.user(t, "host")
			g, h := f.adopt(t, guest, "Fin"), f.adopt(t, host, "Shell")
			// Both homes have opened their stoves. Only the host has food left.
			hour := start.UnixMilli() / 3_600_000
			if _, err := f.store.DB().Exec(`UPDATE whale_haven_profiles SET snapshot=json_set(snapshot,
				'$.home.recentFedHistory',json_array(json_object('hourSlot',?,'grams',180)),
				'$.whale.satiety',CASE WHEN user_id=? THEN 24 ELSE 100 END,
				'$.home.troughGrams',CASE WHEN user_id=? THEN 180 ELSE 0 END,
				'$.whale.isSleeping',json(CASE WHEN user_id=? THEN 'true' ELSE 'false' END))`, hour, guest, host, host); err != nil {
				t.Fatal(err)
			}
			f.at = start.Add(30 * time.Second)
			if automatic {
				if _, err := f.service.forageBatch(t.Context(), "", func(int) int { return 0 }); err != nil {
					t.Fatal(err)
				}
			} else {
				send := visitAction(g, "send_visit")
				send["target"] = h["social"].(map[string]any)["door"]
				f.call(t, guest, "actions", send, "guest-leaves-for-default-meal", 200)
			}
			departure := f.at
			// At 6 mg/s the whale finishes 28 g after 4,666,666 2/3 ms.
			if !automatic {
				for i := 1; i <= 7; i++ {
					f.at = departure.Add(time.Duration(i) * 10 * time.Minute)
					g = f.call(t, guest, "profile", nil, "", 200)
					whale := g["snapshot"].(map[string]any)["whale"].(map[string]any)
					socialNear(t, whale["location"].(map[string]any)["eatenGrams"].(float64), float64(i)*3.6)
					socialNear(t, whale["satiety"].(float64), 24-30*0.045+float64(i)*3.6*2.25)
					if i == 3 {
						save := visitAction(g, "set_open")
						save["open"] = true
						f.call(t, guest, "actions", save, "post-partial-meal-before-clock-step", 200)
						f.at = f.at.Add(-time.Second)
						p := f.call(t, guest, "profile", nil, "", 200)
						whale = p["snapshot"].(map[string]any)["whale"].(map[string]any)
						socialNear(t, whale["location"].(map[string]any)["eatenGrams"].(float64), float64(i)*3.6)
					}
				}
				f.at = departure.Add(4_666_666 * time.Millisecond)
				if g = f.call(t, guest, "profile", nil, "", 200); g["social"].(map[string]any)["away"] == nil {
					t.Fatal("returned before eating the last fraction of a gram")
				}
			}
			mealEnd := departure.Add(4_666_667 * time.Millisecond)
			f.at = mealEnd
			wantToday := float64(1)
			if automatic {
				// Both keepers were offline when the whale returned the previous day.
				f.at = mealEnd.Add(20 * time.Minute)
				wantToday = 0
			}
			g = f.call(t, guest, "profile", nil, "", 200)
			snap := g["snapshot"].(map[string]any)
			wallet := snap["wallet"].(map[string]any)
			if g["social"].(map[string]any)["away"] != nil || wallet["pendingSocialPearls"] != float64(1) || wallet["pendingConchShards"] != float64(1) || wallet["pearls"] != float64(0) {
				t.Fatalf("guest meal projected at return: %v, %v", g["social"], wallet)
			}
			settle := visitAction(g, "harvest_pearls")
			for range 2 {
				f.call(t, guest, "actions", settle, "post-default-visit-rewards-once", 200)
			}
			for _, id := range []int64{guest, host} {
				p := f.call(t, id, "profile", nil, "", 200)["snapshot"].(map[string]any)
				wallet = p["wallet"].(map[string]any)
				if wallet["pearls"] != float64(1) || wallet["conchShards"] != float64(1) || wallet["pendingSocialPearls"] != float64(0) || wallet["dailySocialPearlsGained"] != wantToday {
					t.Fatalf("posted reward for %d: %v", id, wallet)
				}
				var count int
				var at int64
				if err := f.store.ReadDB().QueryRow("SELECT count(*),MIN(created_at) FROM whale_haven_movements WHERE user_id=? AND reason='social_reward'", id).Scan(&count, &at); err != nil || count != 1 || at != mealEnd.UnixMilli() {
					t.Fatal("posted movement", id, count, at, err)
				}
				home := p["home"].(map[string]any)
				if id == host && (home["troughGrams"] != float64(152) || home["unclaimedPearls"] != float64(0)) {
					t.Fatal("household food or bank", home)
				}
			}
		})
	}
}

func TestVisitPausedByHostBankResumesAfterCollection(t *testing.T) {
	f := newForagingFixture(t)
	guest, host := f.user(t, "guest"), f.user(t, "host")
	g, h := f.adopt(t, guest, "Fin"), f.adopt(t, host, "Shell")
	send := visitAction(g, "send_visit")
	send["target"] = h["social"].(map[string]any)["door"]
	if _, err := f.store.DB().Exec(`UPDATE whale_haven_profiles SET snapshot=json_set(snapshot,
		'$.home.troughGrams',180,'$.home.unclaimedPearls',25) WHERE user_id=?`, host); err != nil {
		t.Fatal(err)
	}
	f.call(t, guest, "actions", send, "guest-arrives-at-a-full-bank", 409)
	if result := f.call(t, guest, "neighbors/search?filter=has_food", nil, "", 200); len(result["items"].([]any)) != 0 {
		t.Fatal("full bank offered food", result)
	}
	// Begin just below capacity. The owner fills the last sliver mid-meal.
	if _, err := f.store.DB().Exec(`UPDATE whale_haven_profiles SET snapshot=json_set(snapshot,
		'$.home.unclaimedPearls',24,'$.home.pearlProgress',99) WHERE user_id=?`, host); err != nil {
		t.Fatal(err)
	}
	f.call(t, guest, "actions", send, "guest-arrives-as-dinner-starts", 200)
	f.at = f.at.Add(10 * time.Minute)
	paused := func() float64 {
		t.Helper()
		p := f.call(t, guest, "profile", nil, "", 200)
		if p["social"].(map[string]any)["away"].(map[string]any)["meal_paused"] != true {
			t.Fatal("paused meal should be visible", p["social"])
		}
		location := p["snapshot"].(map[string]any)["whale"].(map[string]any)["location"].(map[string]any)
		return location["eatenGrams"].(float64)
	}
	eaten := paused()
	f.at = f.at.Add(time.Hour)
	socialNear(t, paused(), eaten)
	h = f.call(t, host, "profile", nil, "", 200)
	f.call(t, host, "actions", visitAction(h, "harvest_pearls"), "host-collects-to-resume-meal", 200)
	f.at = f.at.Add(time.Minute)
	g = f.call(t, guest, "profile", nil, "", 200)
	if g["social"].(map[string]any)["away"].(map[string]any)["meal_paused"] != false {
		t.Fatal(g["social"])
	}
	location := g["snapshot"].(map[string]any)["whale"].(map[string]any)["location"].(map[string]any)
	socialNear(t, location["eatenGrams"].(float64), eaten+0.36)
	f.call(t, guest, "actions", visitAction(g, "recall_home"), "guest-recalls-after-resumed-meal", 200)
}

func TestMealReturnHonorsStoveAndDailyAllowance(t *testing.T) {
	for _, tc := range []struct {
		name              string
		rate, stove, used int64
		wantShards        int64
		reason            string
	}{
		{"released 35 g rate", 35, 0, 0, 1, ""},
		{"no recent home meal", 28, 180, 0, 2, "开火用粮不足"},
		{"daily allowance used", 28, 0, 5, 2, "社交鲸币已达上限"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			game := newSocialDB(t, 1000)
			game.cfg.Values["visit_grams_per_pearl"] = tc.rate
			game.cfg.Values["stove_required_grams"] = tc.stove
			raw, err := json.Marshal(game.cfg)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := game.db.Exec("UPDATE whale_haven_settings SET document=? WHERE revision=2", string(raw)); err != nil {
				t.Fatal(err)
			}
			game.run(t, 1000, func(w *world) {
				g := socialActor(t, w, 2)
				g.state.Wallet.DailySocialPearlsGained = tc.used
				w.keepDay(g)
				commandSocial(t, w, g, actionRequest{Type: "send_visit", Target: "HOST"})
			})
			game.run(t, 30_000, func(w *world) {
				g := socialActor(t, w, 2)
				if g.state.Wallet.Pearls != 0 || g.state.Wallet.ConchShards != tc.wantShards || g.state.Wallet.DailySocialPearlsGained != tc.used {
					t.Fatal(g.state.Wallet)
				}
				for _, e := range w.events {
					if e.user == g.id && e.kind == "settlement" && e.at == 29_000 && strings.Contains(e.text, tc.reason) {
						return
					}
				}
				t.Fatal("missing meal receipt", w.events)
			})
		})
	}
}

func TestRecallRacesHiringAfterVisitMeal(t *testing.T) {
	f := newForagingFixture(t)
	guest, host := f.user(t, "guest"), f.user(t, "host")
	g, h := f.adopt(t, guest, "Fin"), f.adopt(t, host, "Shell")
	if _, err := f.store.DB().Exec("UPDATE whale_haven_profiles SET snapshot=json_set(snapshot,'$.home.troughGrams',180) WHERE user_id=?", host); err != nil {
		t.Fatal(err)
	}
	send := visitAction(g, "send_visit")
	send["allowHire"], send["target"] = true, h["social"].(map[string]any)["door"]
	g = f.call(t, guest, "actions", send, "guest-visit-with-hiring-consent", 200)
	f.at = f.at.Add(70 * time.Minute) // 25.2 g, enough for one shard.
	h = f.call(t, host, "profile", nil, "", 200)
	hire := visitAction(h, "catch_guest")
	hire["neighborId"], hire["action"] = g["social"].(map[string]any)["door"], "hire"
	type result struct {
		id  int64
		err error
	}
	done := make(chan result, 2)
	for id, action := range map[int64]map[string]any{host: hire, guest: visitAction(g, "recall_home")} {
		raw, err := json.Marshal(action)
		if err != nil {
			t.Fatal(err)
		}
		go func() {
			req := httptest.NewRequestWithContext(t.Context(), "POST", "/api/games/whale-haven/actions", bytes.NewReader(raw))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Idempotency-Key", "concurrent-whale-visit-action")
			_, err := f.service.handle(req, id, "actions")
			done <- result{id, err}
		}()
	}
	var won int64
	for range 2 {
		r := <-done
		if r.err == nil {
			if won != 0 {
				t.Fatal("both conflicting commands succeeded")
			}
			won = r.id
		} else if p, ok := r.err.(*problem); !ok || p.code != "conflict" {
			t.Fatal(r.err)
		}
	}
	if won == 0 {
		t.Fatal("neither command succeeded")
	}
	if won == host {
		g = f.call(t, guest, "profile", nil, "", 200)
		f.call(t, guest, "actions", visitAction(g, "recall_home"), "recall-after-the-hire-committed", 200)
	}
	var count, pearls, shards int
	if err := f.store.ReadDB().QueryRow("SELECT count(*),sum(pearls_delta),sum(shards_delta) FROM whale_haven_movements WHERE user_id=? AND reason='social_reward'", guest).Scan(&count, &pearls, &shards); err != nil || count != 1 || pearls != 0 || shards != 1 {
		t.Fatal(count, pearls, shards, err)
	}
}
