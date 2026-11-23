package rules

import (
	"encoding/json"
	"math"
	"testing"
)

type settings map[string]int64

func (p settings) Value(key string) int64 { return p[key] }

func basic() settings {
	return settings{
		"lot_pearls": 5, "lot_gross_milli": 5_000_000, "daily_exchange_pearls": 75, "wallet_cap_milli": 300_000_000,
		"platform_cut_bp": 300, "welfare_cut_bp": 300, "thursday_cut_bp": 300,
		"conversion_base_bp": 10_000, "conversion_min_bp": 8000, "conversion_max_bp": 12000,
		"affection_per_level": 100, "mood_low_threshold": 35, "mood_high_threshold": 80,
		"sleep_qualify_seconds": 1800, "sleep_bonus_seconds": 14400, "sleep_bonus_bp": 500, "wake_satiety": 14,
		"fed_satiety_gain_milli_per_second": 90, "fed_mood_gain_milli_per_second": 30, "hungry_mood_loss_milli_per_second": 25,
		"hungry_satiety_loss_milli_per_second": 45, "sleep_satiety_loss_milli_per_second": 12, "sleep_mood_gain_milli_per_second": 140,
		"trough_grams": 360, "bank_base_pearls": 25, "bank_upgraded_pearls": 60,
		"feed_silver_fish_grams": 180, "feed_sweet_shrimp_grams": 180, "feed_silver_fish_base_pearls": 10, "feed_sweet_shrimp_base_pearls": 25,
		"feed_silver_fish_digest_seconds": 9000, "feed_sweet_shrimp_digest_seconds": 14400,
		"feed_silver_fish_price_milli": 10000000, "feed_sweet_shrimp_price_milli": 25000000, "feed_berry_teacake_price_milli": 3000000,
		"feed_berry_teacake_satiety": 45, "feed_berry_teacake_mood": 22, "feed_berry_teacake_affection": 12,
		"aroma_grams": 180, "aroma_speed_bonus_bp": 3500, "guest_worker_mg_per_second": 14, "guest_sneak_mg_per_second": 6,
		"daily_social_pearls": 5, "daily_conch_shards": 10, "stove_required_grams": 180, "stove_window_seconds": 172800,
	}
}

func near(t *testing.T, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-7 {
		t.Fatalf("got %.12g, want %.12g", got, want)
	}
}
func mustApply(t *testing.T, s *State, p Parameters, a Action) Result {
	t.Helper()
	got, err := s.Apply(a, s.LastTickAt, p)
	if err != nil {
		t.Fatal(a.Type, err)
	}
	return got
}

// Feed age and productive time are tied to the wall clock. Server polls,
// persisted reloads and long absences must not drop elapsed milliseconds.
func TestBowlsAndPolling(t *testing.T) {
	p := basic()
	for _, id := range []string{"silver_fish", "sweet_shrimp"} {
		end := p.Value("feed_"+id+"_digest_seconds") * 1000
		newBowl := func() *State {
			s := NewState(1234, "day")
			s.Inventory[id] = 1
			mustApply(t, s, p, Action{Type: "use_feed", FeedID: id})
			return s
		}
		single, chopped := newBowl(), newBowl()
		single.Advance(1234+end, p, true, nil)
		for stamp := int64(1234 + 857); stamp < 1234+end; stamp += 857 {
			chopped.Advance(stamp, p, true, nil)
		}
		data, err := json.Marshal(chopped)
		if err != nil {
			t.Fatal(err)
		}
		chopped = &State{}
		if err = json.Unmarshal(data, chopped); err != nil {
			t.Fatal(err)
		}
		chopped.Advance(1234+end, p, true, nil)
		for _, s := range []*State{single, chopped} {
			near(t, s.Home.TroughGrams, 0)
			near(t, s.Home.PearlProgress, 0)
			if s.Home.UnclaimedPearls != p.Value("feed_"+id+"_base_pearls") {
				t.Fatalf("%s produced %d", id, s.Home.UnclaimedPearls)
			}
			if !s.StoveReady(s.LastTickAt, p) {
				t.Fatal("the whole real bowl must qualify")
			}
		}
	}
}

func TestFullBankPausesFeedingAndUnusedTicket(t *testing.T) {
	p := basic()
	s := NewState(0, "today")
	s.Home.PearlProgress = 50
	s.Home.UnclaimedPearls = 23
	s.Home.PaidWorkerWages = 1
	s.Home.TroughGrams = 180
	s.Inventory["silver_fish"] = 1
	s.Home.Buffs.AutoRefillEnabled = true
	s.Advance(360_000_000, p, true, []Guest{{Working: true}, {RemainingGrams: 28}})
	if s.Home.UnclaimedPearls != 24 {
		t.Fatal(s.Home)
	}
	food := s.Home.TroughGrams
	if food < 160 || food >= 180 {
		t.Fatal("bank saturation did not stop consumption", food)
	}
	s.Advance(720_000_000, p, true, []Guest{{Working: true}, {RemainingGrams: 28}})
	near(t, s.Home.TroughGrams, food)
	if s.Inventory["silver_fish"] != 1 || s.Home.Buffs.AutoRefillTickets != 1 {
		t.Fatal("unused ticket was spent")
	}
	r := mustApply(t, s, p, Action{Type: "harvest_pearls"})
	if r.Harvested != 24 || s.Home.PaidWorkerWages != 0 {
		t.Fatal(r, s.Home)
	}
	s.Home.PaidWorkerWages = 25
	mustApply(t, s, p, Action{Type: "harvest_pearls"})
	if s.Home.PaidWorkerWages != 0 {
		t.Fatal("worker-only bank remained full")
	}
}

func TestOfflineAuthorizedRefillsAndAroma(t *testing.T) {
	s := NewState(0, "today")
	p := basic()
	s.Inventory["silver_fish"] = 4
	s.Home.TroughGrams = 90
	mustApply(t, s, p, Action{Type: "toggle_auto_refill"})
	mustApply(t, s, p, Action{Type: "activate_aroma"})
	s.Advance(2_500_000, p, true, nil)
	near(t, s.Home.TroughGrams, 22.5)
	near(t, s.Home.Buffs.AromaActiveGrams, 112.5)
	s.Advance(10*365*24*3_600_000, p, true, nil)
	if s.Inventory["silver_fish"] != 3 || s.Home.Buffs.AutoRefillTickets != 0 || s.Home.Buffs.AutoRefillEnabled || s.Home.UnclaimedPearls != 15 {
		t.Fatal(s.Home, s.Inventory)
	}
	near(t, s.Home.Buffs.AromaActiveGrams, 0)
	if s.StoveReady(s.LastTickAt, p) {
		t.Fatal("stale offline food qualified")
	}
}

func TestRefillTicketsKeepOversizedServings(t *testing.T) {
	p := basic()
	p["trough_grams"], p["feed_sweet_shrimp_grams"], p["feed_silver_fish_grams"] = 100, 180, 100
	s := NewState(0, "today")
	s.Inventory["sweet_shrimp"] = 1
	mustApply(t, s, p, Action{Type: "toggle_auto_refill"})
	s.Advance(1000, p, true, nil)
	if s.CanRefill(p) || s.Home.TroughGrams != 0 || s.Inventory["sweet_shrimp"] != 1 || s.Home.Buffs.AutoRefillTickets != 1 {
		t.Fatalf("oversized automatic serving: %v %v", s.Home, s.Inventory)
	}
	s.Inventory["silver_fish"] = 1
	s.Advance(2000, p, true, nil)
	if s.Home.TroughGrams <= 0 || s.Home.TroughFeedType != "silver_fish" || s.Inventory["silver_fish"] != 0 || s.Inventory["sweet_shrimp"] != 1 || s.Home.Buffs.AutoRefillTickets != 0 {
		t.Fatalf("expected the whole serving that fits: %v %v", s.Home, s.Inventory)
	}
}

func TestWorkerContributionLimitsAndSneakConservation(t *testing.T) {
	s := NewState(0, "today")
	p := basic()
	s.Whale.Location = Location{Kind: "visiting", NeighborID: "other"}
	s.Home.TroughGrams = 180
	report := s.Advance(7_200_000, p, true, []Guest{{Working: true, MaxPearls: 1.2}, {RemainingGrams: 7.25}})
	near(t, report.Guests[0].Pearls, 1.2)
	near(t, report.Guests[0].Grams, 21.6)
	near(t, report.Guests[1].Grams, 7.25)
	near(t, report.Guests[1].Pearls, 0)
	near(t, s.Home.TroughGrams, 180-21.6-7.25)
	near(t, float64(s.Home.UnclaimedPearls)+s.Home.PearlProgress/100, 1.2)
}

func TestProspectiveRatesAndDisabledClock(t *testing.T) {
	p := basic()
	s := NewState(0, "today")
	s.Home.TroughGrams = 180
	s.Advance(4_500_000, p, true, nil)
	s.Advance(7_500_000, p, false, nil)
	p["feed_silver_fish_base_pearls"] = 20
	s.Advance(12_000_000, p, true, nil)
	near(t, s.Home.TroughGrams, 0)
	if s.Home.UnclaimedPearls != 15 {
		t.Fatal(s.Home)
	}
	s.Whale.Satiety = 100
	mustApply(t, s, p, Action{Type: "toggle_sleep"})
	s.Advance(13_000_000, p, true, nil)
	s.Advance(23_000_000, p, false, nil)
	mustApply(t, s, p, Action{Type: "toggle_sleep"})
	if s.Whale.EnergizedUntil != 0 {
		t.Fatal("disabled time qualified sleep")
	}
	mustApply(t, s, p, Action{Type: "toggle_sleep"})
	s.Advance(24_800_000, p, true, nil)
	mustApply(t, s, p, Action{Type: "toggle_sleep"})
	energy := s.Whale.EnergizedUntil
	if energy != 39_200_000 {
		t.Fatal("wake bonus", energy)
	}
	s.Advance(30_000_000, p, false, nil)
	if s.Whale.EnergizedUntil-energy != 5_200_000 {
		t.Fatal("energy expired while disabled")
	}
}

func TestMoodAndEnergyBoundaries(t *testing.T) {
	p := basic()
	p["mood_high_bonus_bp"] = 400
	p["mood_low_penalty_bp"] = 800
	s := NewState(1, "today")
	s.Home.TroughGrams = 180
	s.Whale.Mood = 25
	s.Whale.EnergizedUntil = 500001
	// Mood crosses 35 after 333 1/3 s and 80 after 1833 1/3 s; sleep bonus expires at 500s.
	s.Advance(2_000_001, p, true, nil)
	want := (2000 - 0.08*1000/3 + 0.04*500/3 + 0.05*500) / 900
	near(t, float64(s.Home.UnclaimedPearls)+s.Home.PearlProgress/100, want)
}
func TestRapidPollingCompletesTheLastPearl(t *testing.T) {
	s := NewState(0, "today")
	s.Home.TroughGrams = 180
	p := basic()
	for stamp := int64(10); stamp <= 9_000_000; stamp += 10 {
		s.Advance(stamp, p, true, nil)
	}
	if s.Home.TroughGrams != 0 || s.Home.UnclaimedPearls != 10 || !s.StoveReady(s.LastTickAt, p) {
		t.Fatal("ten-millisecond polls lost the end of a full bowl", s.Home)
	}
}
