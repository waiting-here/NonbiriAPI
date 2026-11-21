package whalehaven

import (
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"testing"
	"time"
)

func stationHouse(t *testing.T, f *stationFixture, viewer int64, door string, status int) map[string]any {
	t.Helper()
	return f.call(t, viewer, "neighbors/house?door="+url.QueryEscape(door), nil, "", status)
}

func stationHouseAction(t *testing.T, f *stationFixture, user int64, requestKey string, action map[string]any) {
	t.Helper()
	profile := f.call(t, user, "profile", nil, "", http.StatusOK)
	revision := profile["snapshot"].(map[string]any)["revision"].(float64)
	action["expected_profile_revision"] = strconv.FormatInt(int64(revision), 10)
	action["expected_settings_revision"] = profile["settings_revision"]
	f.call(t, user, "actions", action, requestKey, http.StatusOK)
}

func TestStationNeighborHouseProjectsSettledGuests(t *testing.T) {
	f := newStationFixture(t)
	f.enable(t)
	host := f.user(t, "private-host-login")
	eater := f.user(t, "private-eater-login")
	worker := f.user(t, "public-worker-login")
	observer := f.user(t, "observer-login")
	hostDoor := f.adopt(t, host, "Host")["social"].(map[string]any)["door"].(string)
	f.adopt(t, eater, "Eater")
	workerDoor := f.adopt(t, worker, "Worker")["social"].(map[string]any)["door"].(string)
	f.adopt(t, observer, "Observer")

	write := f.store.DB()
	if _, err := write.Exec(`UPDATE whale_haven_profiles SET snapshot=json_set(snapshot,'$.home.troughGrams',180) WHERE user_id=?`, host); err != nil {
		t.Fatal(err)
	}
	if _, err := write.Exec(`UPDATE whale_haven_profiles SET snapshot=json_set(snapshot,'$.whale.outfit','knit_beret') WHERE user_id=?`, worker); err != nil {
		t.Fatal(err)
	}
	if _, err := write.Exec("UPDATE users SET game_profile_public=1 WHERE id=?", worker); err != nil {
		t.Fatal(err)
	}
	stationHouseAction(t, f, eater, "house-test-guest-enters-to-eat", map[string]any{"type": "send_visit", "target": hostDoor})
	stationHouseAction(t, f, worker, "house-test-guest-enters-to-work", map[string]any{"type": "send_visit", "target": hostDoor, "allowHire": true})
	stationHouseAction(t, f, host, "house-test-host-hires-guest", map[string]any{"type": "catch_guest", "neighborId": workerDoor, "action": "hire"})

	view := stationHouse(t, f, observer, hostDoor, http.StatusOK)
	eating := map[string]any{"whaleName": "Eater", "whaleOutfit": "maid_classic", "stage": "eating", "mine": false}
	working := map[string]any{"whaleName": "Worker", "whaleOutfit": "knit_beret", "stage": "working", "mine": false}
	if len(view) != 2 || !reflect.DeepEqual(view["guests"], []any{eating, working}) {
		t.Fatalf("observer projection = %+v", view)
	}
	neighbors := f.call(t, observer, "neighbors/search?q="+url.QueryEscape(hostDoor), nil, "", http.StatusOK)["items"].([]any)
	if len(neighbors) != 1 || !reflect.DeepEqual(view["house"], neighbors[0]) {
		t.Fatalf("house differs from the neighbor projection: house=%+v neighbors=%+v", view["house"], neighbors)
	}
	if house := view["house"].(map[string]any); house["uid"] != nil || house["discordUsername"] != nil || house["publicNickname"] != "" {
		t.Fatalf("private owner identity appeared in the house: %+v", house)
	}

	eating["mine"] = true
	if got := stationHouse(t, f, eater, hostDoor, http.StatusOK)["guests"]; !reflect.DeepEqual(got, []any{eating, working}) {
		t.Fatalf("visiting viewer projection = %+v", got)
	}
	hostProfile := f.call(t, host, "profile", nil, "", http.StatusOK)
	if got := hostProfile["social"].(map[string]any)["guests"].([]any); len(got) != 2 {
		t.Fatalf("host cannot manage both visitors: %+v", got)
	}

	savedAt := f.at.UnixMilli()
	f.at = f.at.Add(80 * time.Minute)
	working["mine"] = true
	afterMeal := stationHouse(t, f, worker, hostDoor, http.StatusOK)
	if !reflect.DeepEqual(afterMeal["guests"], []any{working}) {
		t.Fatalf("after the meal, worker projection = %+v", afterMeal)
	}
	if house := afterMeal["house"].(map[string]any); house["updatedAt"] != float64(f.at.UnixMilli()) || house["troughGrams"].(float64) >= 180 {
		t.Fatalf("house did not reflect offline feeding: %+v", house)
	}

	f.at = f.at.Add(41 * time.Minute)
	afterShift := stationHouse(t, f, observer, hostDoor, http.StatusOK)
	if !reflect.DeepEqual(afterShift["guests"], []any{}) {
		t.Fatalf("after the shift, observer projection = %+v", afterShift)
	}
	var storedVisits, storedTick int64
	err := f.store.ReadDB().QueryRow(`
 SELECT (SELECT count(*) FROM whale_haven_visits WHERE host_id=?), json_extract(snapshot,'$.lastTickAt')
 FROM whale_haven_profiles WHERE user_id=?`, host, host).Scan(&storedVisits, &storedTick)
	if err != nil || storedVisits != 2 || storedTick != savedAt {
		t.Fatalf("GET wrote settlement: visits=%d tick=%d err=%v", storedVisits, storedTick, err)
	}
	stationHouseAction(t, f, host, "house-test-host-persists-settlement", map[string]any{"type": "chat"})
	err = f.store.ReadDB().QueryRow("SELECT count(*) FROM whale_haven_visits WHERE host_id=?", host).Scan(&storedVisits)
	if err != nil || storedVisits != 0 {
		t.Fatalf("host's write did not settle visits: count=%d err=%v", storedVisits, err)
	}
}

func TestStationNeighborHouseRespectsSocialVisibility(t *testing.T) {
	f := newStationFixture(t)
	f.enable(t)
	host := f.user(t, "house-owner-login")
	viewer := f.user(t, "house-viewer-login")
	unadopted := f.user(t, "no-house-login")
	first := f.user(t, "first-guest-login")
	second := f.user(t, "second-guest-login")
	hostDoor := f.adopt(t, host, "Host")["social"].(map[string]any)["door"].(string)
	viewerDoor := f.adopt(t, viewer, "Viewer")["social"].(map[string]any)["door"].(string)
	f.adopt(t, first, "First")
	f.adopt(t, second, "Second")

	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := f.store.DB().Exec(query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`UPDATE whale_haven_profiles SET snapshot=json_set(snapshot,'$.home.troughGrams',180) WHERE user_id=?`, host)
	stationHouseAction(t, f, first, "visibility-test-first-guest-enters", map[string]any{"type": "send_visit", "target": hostDoor})
	stationHouseAction(t, f, second, "visibility-test-second-guest-enters", map[string]any{"type": "send_visit", "target": hostDoor})
	exec("UPDATE users SET is_banned=1,banned_until=? WHERE id=?", f.at.Unix()+60, first)
	exec("INSERT INTO user_deletion_markers(user_id) VALUES(?)", second)
	if got := stationHouse(t, f, viewer, hostDoor, http.StatusOK)["guests"]; !reflect.DeepEqual(got, []any{}) {
		t.Fatalf("banned or deleting visitor was visible: %+v", got)
	}
	exec("UPDATE users SET banned_until=? WHERE id=?", f.at.Unix(), first)
	want := []any{map[string]any{"whaleName": "First", "whaleOutfit": "maid_classic", "stage": "eating", "mine": false}}
	if got := stationHouse(t, f, viewer, hostDoor, http.StatusOK)["guests"]; !reflect.DeepEqual(got, want) {
		t.Fatalf("visitor with expired ban was not visible: %+v", got)
	}
	exec("UPDATE whale_haven_profiles SET is_open=0 WHERE user_id=?", first)
	if got := stationHouse(t, f, viewer, hostDoor, http.StatusOK)["guests"]; !reflect.DeepEqual(got, []any{}) {
		t.Fatalf("visitor from a closed home was visible: %+v", got)
	}

	stationHouse(t, f, unadopted, hostDoor, http.StatusConflict)
	stationHouse(t, f, viewer, viewerDoor, http.StatusNotFound)
	stationHouse(t, f, viewer, "unknown-door", http.StatusNotFound)
	stationHouse(t, f, viewer, "", http.StatusNotFound)
	exec("UPDATE users SET is_banned=1 WHERE id=?", host)
	stationHouse(t, f, viewer, hostDoor, http.StatusNotFound)
	exec("UPDATE users SET is_banned=0 WHERE id=?", host)
	exec("INSERT INTO user_deletion_markers(user_id) VALUES(?)", host)
	stationHouse(t, f, viewer, hostDoor, http.StatusNotFound)
	exec("DELETE FROM user_deletion_markers WHERE user_id=?", host)
	stationHouseAction(t, f, host, "visibility-test-host-closes-home", map[string]any{"type": "set_open", "open": false})
	stationHouse(t, f, viewer, hostDoor, http.StatusNotFound)
	stationHouseAction(t, f, viewer, "visibility-test-viewer-closes-home", map[string]any{"type": "set_open", "open": false})
	stationHouse(t, f, viewer, hostDoor, http.StatusConflict)
}
