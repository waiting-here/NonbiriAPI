package main

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestCustomPresetsUseCurrentBanAuthorization(t *testing.T) {
	f := newDuelWireFixture(t)
	setBan := func(until any) {
		t.Helper()
		if _, err := f.store.DB().Exec(`UPDATE users SET is_banned=1,banned_until=? WHERE id=?`, until, f.users[0]); err != nil {
			t.Fatal(err)
		}
	}
	save := func(slot, revision string, want int) {
		t.Helper()
		response := f.call(0, http.MethodPut, "/api/games/likes/loadouts/"+slot, map[string]any{
			"expected_revision": revision, "mode": "quick",
			"loadout": map[string]any{"role": "ChatGPT", "harness": nil, "skills": []string{"PUB01"}},
		}, false)
		if response.Code != want {
			t.Fatalf("save slot %s: status=%d want=%d body=%s", slot, response.Code, want, response.Body.String())
		}
		if want == http.StatusOK {
			var item struct {
				Revision string `json:"revision"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &item); err != nil || item.Revision == revision {
				t.Fatal("successful save did not advance revision", item, err)
			}
		}
	}
	save("1", "0", http.StatusOK)
	setBan(f.clock.Load() + 3600)
	save("2", "0", http.StatusForbidden)
	save("1", "1", http.StatusForbidden)
	setBan(f.clock.Load() - 1)
	save("2", "0", http.StatusOK)
	save("1", "1", http.StatusOK)
	setBan(nil)
	save("3", "0", http.StatusForbidden)
	save("1", "2", http.StatusForbidden)
	var count, revision int
	if err := f.store.DB().QueryRow(`SELECT count(*) FROM game_likes_loadouts WHERE user_id=?`, f.users[0]).Scan(&count); err != nil || count != 2 {
		t.Fatal("forbidden save changed slot count", count, err)
	}
	if err := f.store.DB().QueryRow(`SELECT revision FROM game_likes_loadouts WHERE user_id=? AND slot=1`, f.users[0]).Scan(&revision); err != nil || revision != 2 {
		t.Fatal("forbidden overwrite changed revision", revision, err)
	}
}
