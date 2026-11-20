package whalehaven

import (
	"context"
	"database/sql"
	"encoding/json"
	"math"
	"os"
	"testing"
	"time"

	_ "github.com/waiting-here/NonbiriAPI/internal/sqliteadapter"
	"github.com/waiting-here/NonbiriAPI/internal/whalehaven/assets"
	"github.com/waiting-here/NonbiriAPI/internal/whalehaven/config"
	"github.com/waiting-here/NonbiriAPI/internal/whalehaven/rules"
)

type socialDB struct {
	db  *sql.DB
	cfg config.Settings
}

func newSocialDB(t *testing.T, at int64) *socialDB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	db.SetMaxOpenConns(1)
	s := &socialDB{db: db, cfg: config.Defaults()}
	s.cfg.Enabled = true
	for k, v := range map[string]int64{"conversion_min_bp": 10000, "conversion_base_bp": 10000, "conversion_max_bp": 10000, "guest_worker_mg_per_second": 1000, "guest_sneak_mg_per_second": 1000, "feed_silver_fish_base_pearls": 180, "sleep_satiety_loss_milli_per_second": 0, "stove_required_grams": 0} {
		s.cfg.Values[k] = v
	}
	exec := func(query string, args ...any) {
		if _, err := db.Exec(query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`PRAGMA foreign_keys=ON; CREATE TABLE site_config(key TEXT PRIMARY KEY,value TEXT,updated_at INTEGER);
		INSERT INTO site_config VALUES('games_enabled','1',0);
		CREATE TABLE users(id INTEGER PRIMARY KEY,is_banned INTEGER DEFAULT 0,banned_until INTEGER,game_profile_public INTEGER DEFAULT 0);
		CREATE TABLE game_user_preferences(user_id INTEGER PRIMARY KEY,game_profile_public INTEGER);
		CREATE TABLE user_deletion_markers(user_id INTEGER PRIMARY KEY);`)
	schema, err := os.ReadFile("../db/whale_haven.sql")
	if err != nil {
		t.Fatal(err)
	}
	exec(string(schema))
	js, err := json.Marshal(s.cfg)
	if err != nil {
		t.Fatal(err)
	}
	exec("INSERT INTO whale_haven_settings VALUES(2,0,?)", string(js))
	for i, door := range []string{"HOST", "GUEST", "THIRD", "FOURTH", "UNRELATED"} {
		p := rules.NewState(at, time.UnixMilli(at).UTC().Format(time.DateOnly))
		if i == 0 {
			p.Home.TroughGrams = 180
			p.Whale.IsSleeping = true
			p.Whale.SleepingSince = at
		}
		raw, err := json.Marshal(p)
		if err != nil {
			t.Fatal(err)
		}
		exec("INSERT INTO users(id) VALUES(?)", i+1)
		exec("INSERT INTO whale_haven_profiles VALUES(?,?,?,?,?,?,?)", i+1, door, true, p.Whale.Name, 1, string(raw), at)
	}
	return s
}

func (s *socialDB) run(t *testing.T, at int64, f func(*world)) {
	t.Helper()
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	w := &world{ctx: context.Background(), tx: tx, now: at, location: time.UTC, config: s.cfg, master: true,
		residents: map[int64]*resident{}, visits: map[int64]*visit{}, days: map[dayKey]counters{}, wages: map[[2]int64]float64{}}
	f(w)
	if err = w.save("whale_haven", "test"); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
}
func socialActor(t *testing.T, w *world, id int64) *resident {
	t.Helper()
	p, err := w.load(id)
	if err != nil {
		t.Fatal(err)
	}
	if err = w.settle(p); err != nil {
		t.Fatal(err)
	}
	return p
}
func commandSocial(t *testing.T, w *world, r *resident, a actionRequest) {
	t.Helper()
	if err := w.socialAction(r, a); err != nil {
		t.Fatal(err)
	}
}
func socialNear(t *testing.T, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-8 {
		t.Fatalf("got %.12f, want %.12f", got, want)
	}
}

func TestWhaleSocialWagesAcrossHarvestAndClose(t *testing.T) {
	db := newSocialDB(t, 1000)
	allow, closeDoor := true, false
	db.run(t, 1000, func(w *world) {
		g := socialActor(t, w, 2)
		commandSocial(t, w, g, actionRequest{Type: "send_visit", Target: "HOST", AllowHire: &allow, PresetID: "visit_2"})
		h := socialActor(t, w, 1)
		commandSocial(t, w, h, actionRequest{Type: "catch_guest", NeighborID: "GUEST", Action: "hire", PresetID: "hire_1"})
	})
	db.run(t, 3500, func(w *world) {
		h := socialActor(t, w, 1)
		socialNear(t, h.state.Home.TroughGrams, 177.5)
		socialNear(t, h.state.Home.WageProgress, 50)
		if len(w.residents) != 2 {
			t.Fatal("unrelated households loaded", len(w.residents))
		}
		result, err := h.state.Apply(rules.Action{Type: "harvest_pearls"}, w.now, w.config)
		if err != nil || result.Harvested != 2 {
			t.Fatal(result, err)
		}
		h.dirty = true
	})
	db.run(t, 6000, func(w *world) {
		g := socialActor(t, w, 2)
		h := w.residents[1]
		if g.state.Wallet.Pearls != 1 || g.state.Wallet.DailySocialPearlsGained != 1 || h.state.Home.UnclaimedPearls != 2 {
			t.Fatal(g.state.Wallet, h.state.Home)
		}
		commandSocial(t, w, h, actionRequest{Type: "set_open", Open: &closeDoor})
		if g.state.Whale.Location.Kind != "home" || h.open {
			t.Fatal(g, h)
		}
		socialNear(t, h.state.Home.TroughGrams, 175)
	})
	db.run(t, 7000, func(w *world) {
		for id, want := range map[int64]int64{1: 2, 2: 1} {
			b, err := assets.Read(w.ctx, w.tx, id)
			if err != nil || b.Pearls != want {
				t.Fatal(id, b, err)
			}
		}
		var count int
		if err := w.tx.QueryRow("SELECT count(*) FROM whale_haven_visits").Scan(&count); err != nil || count != 0 {
			t.Fatal(count, err)
		}
		if err := w.tx.QueryRow("SELECT count(*) FROM whale_haven_events WHERE kind IN('visit_out','guest_in')").Scan(&count); err != nil || count != 2 {
			t.Fatal(count, err)
		}
	})
}

func TestWhaleSocialSlotsConsentRewardsAndPins(t *testing.T) {
	db := newSocialDB(t, 1000)
	db.run(t, 1000, func(w *world) {
		for _, id := range []int64{2, 3} {
			g := socialActor(t, w, id)
			commandSocial(t, w, g, actionRequest{Type: "send_visit", Target: "HOST"})
		}
		other := socialActor(t, w, 4)
		if err := w.socialAction(other, actionRequest{Type: "send_visit", Target: "HOST"}); err == nil {
			t.Fatal("third visitor")
		}
		h := socialActor(t, w, 1)
		if err := w.socialAction(h, actionRequest{Type: "catch_guest", NeighborID: "GUEST", Action: "hire"}); err == nil {
			t.Fatal("hire without consent")
		}
		commandSocial(t, w, other, actionRequest{Type: "toggle_pin_neighbor", NeighborID: "HOST"})
	})
	db.run(t, 7000, func(w *world) {
		h := socialActor(t, w, 1)
		socialNear(t, h.state.Home.TroughGrams, 168)
		commandSocial(t, w, h, actionRequest{Type: "catch_guest", NeighborID: "GUEST", Action: "flick"})
		socialNear(t, h.state.Home.TroughGrams, 171)
		commandSocial(t, w, h, actionRequest{Type: "catch_guest", NeighborID: "THIRD", Action: "treat"})
		socialNear(t, h.state.Home.TroughGrams, 151)
		if h.state.Wallet.Pearls != 3 || h.state.Wallet.ConchShards != 2 || w.residents[3].state.Wallet.ConchShards != 1 {
			t.Fatal(h.state.Wallet, w.residents[3].state.Wallet)
		}
		closed := false
		commandSocial(t, w, h, actionRequest{Type: "set_open", Open: &closed})
		commandSocial(t, w, socialActor(t, w, 4), actionRequest{Type: "toggle_pin_neighbor", NeighborID: "HOST"})
		var count int
		if err := w.tx.QueryRow("SELECT count(*) FROM whale_haven_pins").Scan(&count); err != nil || count != 0 {
			t.Fatal(count, err)
		}
	})
}

func TestWhaleSocialFreezeThenNewDayWageCap(t *testing.T) {
	const start int64 = 86_390_000
	db := newSocialDB(t, start)
	db.cfg.Values["daily_social_pearls"] = 1
	for i, item := range []struct {
		at      int64
		enabled bool
	}{{start, true}, {start + 3000, false}, {start + 11000, true}} {
		cfg := db.cfg
		cfg.Enabled = item.enabled
		raw, err := json.Marshal(cfg)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = db.db.Exec("INSERT INTO whale_haven_settings VALUES(?,?,?)", i+3, item.at, string(raw)); err != nil {
			t.Fatal(err)
		}
	}
	db.run(t, start, func(w *world) {
		g := socialActor(t, w, 2)
		agree := true
		commandSocial(t, w, g, actionRequest{Type: "send_visit", Target: "HOST", AllowHire: &agree})
		commandSocial(t, w, socialActor(t, w, 1), actionRequest{Type: "catch_guest", Action: "hire", NeighborID: "GUEST"})
	})
	db.run(t, start+12000, func(w *world) {
		h := socialActor(t, w, 1)
		socialNear(t, h.state.Home.TroughGrams, 176)
		if v := w.visits[2]; v.ended || v.workLeft != 7200000-4000 {
			t.Fatal(v)
		}
		if w.residents[2].state.Wallet.Pearls != 0 {
			t.Fatal("fractional wage paid twice")
		}
	})
	db.run(t, start+14000, func(w *world) {
		g := socialActor(t, w, 2)
		h := w.residents[1]
		if !w.visits[2].ended || g.state.Wallet.Pearls != 1 || g.state.Wallet.DailySocialPearlsGained != 1 {
			t.Fatal(w.visits[2], g.state.Wallet)
		}
		socialNear(t, h.state.Home.TroughGrams, 175)
		if h.state.Home.UnclaimedPearls != 4 {
			t.Fatal("owner share", h.state.Home)
		}
	})
}
func TestWhaleSocialCycleReturnsAtActualMealEnd(t *testing.T) {
	db := newSocialDB(t, 1000)
	db.run(t, 1000, func(w *world) {
		for _, id := range []int64{1, 2, 3} {
			r := socialActor(t, w, id)
			r.state.Whale.IsSleeping = false
			r.state.Home.TroughGrams = 180
			r.dirty = true
		}
		for i, door := range []string{"GUEST", "THIRD", "HOST"} {
			commandSocial(t, w, socialActor(t, w, int64(i+1)), actionRequest{Type: "send_visit", Target: door})
		}
	})
	db.run(t, 31000, func(w *world) {
		socialActor(t, w, 1)
		if len(w.residents) != 3 {
			t.Fatal("unrelated accounts loaded", len(w.residents))
		}
		for _, r := range w.residents {
			if r.state.Whale.Location.Kind != "home" || !w.visits[r.id].ended {
				t.Fatal(r)
			}
			socialNear(t, r.state.Home.TroughGrams, 151.96)
		}
	})
}

func TestWhaleSocialEmptyBowlAndSiteWalletCap(t *testing.T) {
	db := newSocialDB(t, 1000)
	db.run(t, 1000, func(w *world) {
		h := socialActor(t, w, 1)
		h.state.Home.TroughGrams = 1
		h.dirty = true
		g := socialActor(t, w, 2)
		g.state.Wallet.Pearls = assets.MaxBalance
		g.dirty = true
		allow := true
		commandSocial(t, w, g, actionRequest{Type: "send_visit", Target: "HOST", AllowHire: &allow})
		if err := w.socialAction(h, actionRequest{Type: "catch_guest", NeighborID: "GUEST", Action: "hire"}); err == nil {
			t.Fatal("hired when wallet full")
		}
	})
	db.run(t, 7000, func(w *world) {
		g := socialActor(t, w, 2)
		if !w.visits[2].ended || g.state.Whale.Location.Kind != "home" {
			t.Fatal(w.visits[2], g.state.Whale)
		}
		// One second of snacking, then five seconds at home with an empty bowl.
		socialNear(t, g.state.Whale.Satiety, 76+2.25-5*.045)
	})
}
