package whalehaven

import (
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"
)

func newForagingFixture(t *testing.T) *stationFixture {
	t.Helper()
	f := newStationFixture(t)
	f.enable(t)
	if _, err := f.store.DB().Exec("UPDATE maintenance_state SET enabled=0 WHERE id=1"); err != nil {
		t.Fatal(err)
	}
	return f
}

func TestForagingFindsFoodWhileKeepersAreOffline(t *testing.T) {
	f := newForagingFixture(t)
	guest, host := f.user(t, "hungry"), f.user(t, "neighbor")
	f.adopt(t, guest, "Fin")
	f.adopt(t, host, "Shell")
	set := func(id int64, expr string, values ...any) {
		t.Helper()
		if _, err := f.store.DB().Exec("UPDATE whale_haven_profiles SET "+expr+" WHERE user_id=?", append(values, id)...); err != nil {
			t.Fatal(err)
		}
	}
	set(guest, "snapshot=json_set(snapshot,'$.whale.satiety',24)")
	set(host, "snapshot=json_set(snapshot,'$.home.troughGrams',50,'$.whale.isSleeping',json('true'))")
	plan := func(roll int) []foodVisit {
		t.Helper()
		_, pairs, err := f.service.findFoodVisits(t.Context(), "", func(n int) int { return roll % n })
		if err != nil {
			t.Fatal(err)
		}
		return pairs
	}
	f.at = f.at.Add(19 * time.Second)
	if len(plan(3)) != 0 || len(plan(2)) != 1 {
		t.Fatal("short steps should have a three percent chance")
	}
	f.at = f.at.Add(time.Second)
	if len(plan(38)) != 0 || len(plan(37)) != 1 {
		t.Fatal("twenty-second steps should have a 38 percent chance")
	}
	if _, err := f.store.DB().Exec("INSERT INTO whale_haven_master_history VALUES(?,0),(?,1)", f.at.Add(-12*time.Second).UnixMilli(), f.at.Add(-3*time.Second).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	if len(plan(3)) != 0 || len(plan(2)) != 1 {
		t.Fatal("time with the game closed must not count toward a full step")
	}
	for _, guard := range []struct {
		id                      int64
		path, blocked, restored string
	}{
		{guest, "$.whale.isSleeping", "true", "false"},
		{guest, "$.whale.satiety", "35", "24"},
		{guest, "$.home.troughGrams", "15", "0"},
		{host, "$.home.troughGrams", "49", "50"},
		{host, "$.home.unclaimedPearls", "25", "0"},
	} {
		set(guard.id, "snapshot=json_set(snapshot,?,json(?))", guard.path, guard.blocked)
		if len(plan(0)) != 0 {
			t.Fatalf("unexpected departure with %s=%v", guard.path, guard.blocked)
		}
		set(guard.id, "snapshot=json_set(snapshot,?,json(?))", guard.path, guard.restored)
	}
	set(guest, "snapshot=json_set(snapshot,'$.home.buffs.autoRefillEnabled',json('true'),'$.home.buffs.autoRefillTickets',1,'$.inventory.silver_fish',1)")
	if len(plan(0)) != 0 {
		t.Fatal("authorized refill must precede foraging")
	}
	if _, err := f.store.DB().Exec("UPDATE whale_haven_settings SET document=json_set(document,'$.values.feed_silver_fish_grams',400) WHERE revision=2"); err != nil {
		t.Fatal(err)
	}
	if len(plan(0)) != 1 {
		t.Fatal("an oversized serving must leave the hungry whale free to forage")
	}
	set(guest, "snapshot=json_set(snapshot,'$.home.buffs.autoRefillEnabled',json('false'))")
	set(host, "snapshot=json_set(snapshot,'$.home.unclaimedPearls',24,'$.home.pearlProgress',70,'$.home.wageProgress',30)")
	if len(plan(0)) != 0 {
		t.Fatal("a bank filled by fractional production and wages cannot feed a guest")
	}
	set(host, "snapshot=json_set(snapshot,'$.home.unclaimedPearls',0,'$.home.pearlProgress',0,'$.home.wageProgress',0)")
	pairs := plan(0)
	if len(pairs) != 1 {
		t.Fatalf("wanted one visit, got %v", pairs)
	}
	pair := pairs[0]
	for _, id := range []int64{guest, host} {
		set(id, "is_open=0")
		if err := f.service.commitFoodVisit(t.Context(), pair); err != nil || len(plan(0)) != 0 {
			t.Fatalf("closed home admitted a visit: %v", err)
		}
		set(id, "is_open=1")
	}
	// A ban or a meal between the read-pool selection and the write must win.
	if _, err := f.store.DB().Exec("UPDATE users SET is_banned=1 WHERE id=?", host); err != nil {
		t.Fatal(err)
	}
	if err := f.service.commitFoodVisit(t.Context(), pair); err != nil || len(plan(0)) != 0 {
		t.Fatalf("banned host: %v", err)
	}
	if _, err := f.store.DB().Exec("UPDATE users SET is_banned=0 WHERE id=?", host); err != nil {
		t.Fatal(err)
	}
	set(host, "snapshot=json_set(snapshot,'$.home.troughGrams',49)")
	if err := f.service.commitFoodVisit(t.Context(), pair); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := f.store.DB().QueryRow("SELECT count(*) FROM whale_haven_visits").Scan(&n); err != nil || n != 0 {
		t.Fatalf("stale eligibility created a visit: %d %v", n, err)
	}
	set(host, "snapshot=json_set(snapshot,'$.home.troughGrams',50)")
	if _, err := f.service.forageBatch(t.Context(), "", func(int) int { return 0 }); err != nil {
		t.Fatal(err)
	}
	if err := f.service.commitFoodVisit(t.Context(), pair); err != nil {
		t.Fatal(err)
	}
	p := f.call(t, guest, "profile", nil, "", http.StatusOK)
	away := p["social"].(map[string]any)["away"].(map[string]any)
	if away["target"] != pair.host || away["allow_hire"] != false {
		t.Fatalf("unexpected away state: %v", away)
	}
	for _, table := range []string{"whale_haven_footprints", "whale_haven_events WHERE kind IN ('visit_out','guest_in')"} {
		if err := f.store.DB().QueryRow("SELECT count(*) FROM " + table).Scan(&n); err != nil || n != 2 {
			t.Fatalf("%s: %d %v", table, n, err)
		}
	}
	f.at = f.at.Add(time.Minute)
	p = f.call(t, guest, "profile", nil, "", http.StatusOK)
	location := p["snapshot"].(map[string]any)["whale"].(map[string]any)["location"].(map[string]any)
	if location["eatenGrams"].(float64) <= 0 {
		t.Fatalf("guest did not eat: %v", location)
	}
	rev := strconv.Itoa(int(p["snapshot"].(map[string]any)["revision"].(float64)))
	p = f.call(t, guest, "actions", map[string]any{"type": "recall_home", "expected_profile_revision": rev, "expected_settings_revision": p["settings_revision"]}, "recall-foraging-whale-home", 200)
	if p["social"].(map[string]any)["away"] != nil {
		t.Fatal("automatic visitor could not be recalled")
	}
}

func TestForagingDoorSweepAndWrappedHostSample(t *testing.T) {
	f := newForagingFixture(t)
	for i := range forageBatchSize + 2 {
		door := fmt.Sprintf("BAY%02d", i)
		id := f.user(t, door)
		if _, err := f.store.DB().Exec("INSERT INTO whale_haven_profiles VALUES(?,?,1,'Fin',1,'{}',0)", id, door); err != nil {
			t.Fatal(err)
		}
	}
	tx, err := f.store.ReadDB().BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	first, err := forageDoors(t.Context(), tx, "", false)
	if err != nil || len(first) != forageBatchSize {
		t.Fatalf("first page: %v %v", first, err)
	}
	last, err := forageDoors(t.Context(), tx, first[len(first)-1], false)
	if err != nil || !slices.Equal(last, []string{"BAY16", "BAY17"}) {
		t.Fatalf("last page: %v %v", last, err)
	}
	hosts, err := forageDoors(t.Context(), tx, "BAY16", true)
	if err != nil || len(hosts) != forageBatchSize || hosts[0] != "BAY17" || !slices.Equal(hosts[1:], first[:forageBatchSize-1]) {
		t.Fatalf("wrapped host sample: %v %v", hosts, err)
	}
}

func TestForagingCanFindARefillFromAnEarlierServingSize(t *testing.T) {
	f := newForagingFixture(t)
	guest, host := f.user(t, "hungry"), f.user(t, "cook")
	guestDoor := f.adopt(t, guest, "Fin")["social"].(map[string]any)["door"].(string)
	hostDoor := f.adopt(t, host, "Shell")["social"].(map[string]any)["door"].(string)
	if _, err := f.store.DB().Exec("UPDATE whale_haven_profiles SET snapshot=json_set(snapshot,'$.whale.satiety',15) WHERE user_id=?", guest); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.DB().Exec("UPDATE whale_haven_profiles SET snapshot=json_set(snapshot,'$.home.buffs.autoRefillEnabled',json('true'),'$.inventory.silver_fish',1) WHERE user_id=?", host); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.DB().Exec("INSERT INTO whale_haven_settings SELECT 3,?,json_set(document,'$.values.feed_silver_fish_grams',400) FROM whale_haven_settings WHERE revision=2", f.at.Add(10*time.Second).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	f.at = f.at.Add(30 * time.Second)
	doors := []string{guestDoor, hostDoor}
	slices.Sort(doors)
	draw := func(n int) int {
		if n == 100 {
			door := doors[0]
			doors = doors[1:]
			if door == hostDoor {
				return 99
			}
		}
		return 0
	}
	_, pairs, err := f.service.findFoodVisits(t.Context(), "", draw)
	if err != nil || !slices.Equal(pairs, []foodVisit{{guestDoor, hostDoor}}) {
		t.Fatalf("host had a fitting serving before the policy change: %v %v", pairs, err)
	}
}

func TestForagingRespectsLastGuestSlotAndGameSwitches(t *testing.T) {
	f := newForagingFixture(t)
	host := f.user(t, "host")
	door := f.adopt(t, host, "Bay")["social"].(map[string]any)["door"].(string)
	if _, err := f.store.DB().Exec("UPDATE whale_haven_profiles SET snapshot=json_set(snapshot,'$.home.troughGrams',100) WHERE user_id=?", host); err != nil {
		t.Fatal(err)
	}
	var pairs []foodVisit
	for _, nick := range []string{"one", "two", "three"} {
		id := f.user(t, nick)
		p := f.adopt(t, id, nick)
		if _, err := f.store.DB().Exec("UPDATE whale_haven_profiles SET snapshot=json_set(snapshot,'$.whale.satiety',12) WHERE user_id=?", id); err != nil {
			t.Fatal(err)
		}
		pairs = append(pairs, foodVisit{p["social"].(map[string]any)["door"].(string), door})
	}
	f.at = f.at.Add(30 * time.Second)
	switches := []struct{ off, on string }{
		{"UPDATE maintenance_state SET enabled=1 WHERE id=1", "UPDATE maintenance_state SET enabled=0 WHERE id=1"},
		{"UPDATE site_config SET value='0' WHERE key='games_enabled'", "UPDATE site_config SET value='1' WHERE key='games_enabled'"},
		{"UPDATE whale_haven_settings SET document=json_set(document,'$.enabled',json('false')) WHERE revision=2", "UPDATE whale_haven_settings SET document=json_set(document,'$.enabled',json('true')) WHERE revision=2"},
	}
	for _, toggle := range switches {
		if _, err := f.store.DB().Exec(toggle.off); err != nil {
			t.Fatal(err)
		}
		if _, picked, err := f.service.findFoodVisits(t.Context(), "", func(int) int { return 0 }); err != nil || len(picked) != 0 {
			t.Fatalf("disabled game selected %v: %v", picked, err)
		}
		if err := f.service.commitFoodVisit(t.Context(), pairs[0]); err != nil {
			t.Fatal(err)
		}
		if _, err := f.store.DB().Exec(toggle.on); err != nil {
			t.Fatal(err)
		}
	}
	if _, picked, err := f.service.findFoodVisits(t.Context(), "", func(int) int { return 0 }); err != nil || len(picked) != 2 {
		t.Fatalf("two guest slots: %v %v", picked, err)
	}
	var wg sync.WaitGroup
	for _, p := range pairs {
		wg.Go(func() {
			if err := f.service.commitFoodVisit(t.Context(), p); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	var n int
	if err := f.store.DB().QueryRow("SELECT count(*) FROM whale_haven_visits WHERE host_id=?", host).Scan(&n); err != nil || n != 2 {
		t.Fatalf("overlapping visitors: %d %v", n, err)
	}
	if _, picked, err := f.service.findFoodVisits(t.Context(), "", func(int) int { return 0 }); err != nil || len(picked) != 0 {
		t.Fatalf("full host selected %v: %v", picked, err)
	}
}
