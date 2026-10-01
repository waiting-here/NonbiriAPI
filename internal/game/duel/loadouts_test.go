package duel_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/game/duel"
)

func TestCustomPresetsOwnershipCapacityAndValidation(t *testing.T) {
	f := newFixture(t, "likes")
	for slot := 1; slot <= 10; slot++ {
		result, err := f.s.SaveLoadout(f.ctx, duel.SaveLoadoutInput{
			Identity: f.identity(0), IdempotencyKey: f.key(), Slot: slot,
			ExpectedRevision: "0", Mode: "quick", Loadout: f.loadouts[0],
		})
		if err != nil || result.Status != 200 {
			t.Fatalf("save slot %d: %v %+v", slot, err, result)
		}
	}
	first, err := f.s.Loadouts(f.ctx, f.identity(0))
	if err != nil || first.Capacity != 10 || len(first.Slots) != 10 {
		t.Fatal("own slots", first, err)
	}
	for i, item := range first.Slots {
		if item.Slot != i+1 || item.Revision != "1" || item.Mode != "quick" || string(item.Loadout) != string(f.loadouts[0]) {
			// The service canonicalizes the JSON, so semantic equality is checked below.
			var actual, expected any
			_ = json.Unmarshal(item.Loadout, &actual)
			_ = json.Unmarshal(f.loadouts[0], &expected)
			if item.Slot != i+1 || item.Revision != "1" || item.Mode != "quick" || !equalJSON(actual, expected) {
				t.Fatal("slot changed", item)
			}
		}
	}
	other, err := f.s.Loadouts(f.ctx, f.identity(1))
	if err != nil || other.Capacity != 10 || len(other.Slots) != 0 {
		t.Fatal("cross-user slot leak", other, err)
	}
	for _, test := range []struct {
		slot           int
		revision, mode string
		loadout        []byte
	}{
		{0, "0", "quick", f.loadouts[0]},
		{11, "0", "quick", f.loadouts[0]},
		{1, "00", "quick", f.loadouts[0]},
		{1, "0", "unknown", f.loadouts[0]},
		{1, "0", "quick", []byte(`{"role":"ChatGPT","harness":null,"skills":["PUB01","PUB01"]}`)},
		{1, "0", "quick", []byte(`{"role":"ChatGPT","harness":null,"skills":["UNKNOWN"]}`)},
		{1, "0", "quick", []byte(`{"role":"ChatGPT","harness":null,"skills":["PUB41"]}`)},
		{1, "0", "quick", []byte(`{"role":"ChatGPT","harness":"UNKNOWN","skills":["PUB01"]}`)},
		{1, "0", "quick", []byte(`{"role":"ChatGPT","harness":null,"skills":["PUB01"],"owner":2}`)},
	} {
		_, err := f.s.SaveLoadout(f.ctx, duel.SaveLoadoutInput{Identity: f.identity(0), IdempotencyKey: f.key(), Slot: test.slot, ExpectedRevision: test.revision, Mode: test.mode, Loadout: test.loadout})
		if !errors.Is(err, duel.ErrInvalidRequest) {
			t.Fatalf("invalid custom preset accepted: %+v err=%v", test, err)
		}
	}
	if _, err := f.s.SaveLoadout(f.ctx, duel.SaveLoadoutInput{Identity: f.identity(1), IdempotencyKey: f.key(), Slot: 1, ExpectedRevision: "1", Mode: "quick", Loadout: f.loadouts[1]}); !errors.Is(err, duel.ErrConflict) {
		t.Fatal("other user overwrote owner slot", err)
	}
	f.ledger()
}

func equalJSON(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}

func TestCustomPresetsCASReplayConcurrentWritesAndDeletion(t *testing.T) {
	f := newFixture(t, "likes")
	in := duel.SaveLoadoutInput{Identity: f.identity(0), IdempotencyKey: f.key(), Slot: 1, ExpectedRevision: "0", Mode: "quick", Loadout: f.loadouts[0]}
	first, err := f.s.SaveLoadout(f.ctx, in)
	if err != nil || first.Status != 200 {
		t.Fatal(first, err)
	}
	replayed, err := f.s.SaveLoadout(f.ctx, in)
	if err != nil || !replayed.Replayed || string(first.Body) != string(replayed.Body) {
		t.Fatal("idempotent retry duplicated save", replayed, err)
	}
	if _, err := f.s.SaveLoadout(f.ctx, duel.SaveLoadoutInput{Identity: f.identity(0), IdempotencyKey: in.IdempotencyKey, Slot: 2, ExpectedRevision: "0", Mode: "quick", Loadout: f.loadouts[0]}); !errors.Is(err, duel.ErrConflict) {
		t.Fatal("same idempotency key accepted a different slot", err)
	}
	if _, err := f.s.SaveLoadout(f.ctx, duel.SaveLoadoutInput{Identity: f.identity(0), IdempotencyKey: f.key(), Slot: 1, ExpectedRevision: "0", Mode: "quick", Loadout: f.loadouts[0]}); !errors.Is(err, duel.ErrConflict) {
		t.Fatal("stale empty-slot revision overwrote saved preset", err)
	}
	second, err := f.s.SaveLoadout(f.ctx, duel.SaveLoadoutInput{Identity: f.identity(0), IdempotencyKey: f.key(), Slot: 1, ExpectedRevision: "1", Mode: "standard", Loadout: f.loadouts[0]})
	if err != nil || second.Status != 200 {
		t.Fatal("overwrite", second, err)
	}
	var item duel.LoadoutItem
	if json.Unmarshal(second.Body, &item) != nil || item.Revision != "2" || item.Mode != "standard" {
		t.Fatal("overwrite response", string(second.Body))
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for range 2 {
		key := f.key()
		wg.Go(func() {
			_, err := f.s.SaveLoadout(f.ctx, duel.SaveLoadoutInput{Identity: f.identity(0), IdempotencyKey: key, Slot: 1, ExpectedRevision: "2", Mode: "quick", Loadout: f.loadouts[0]})
			results <- err
		})
	}
	wg.Wait()
	close(results)
	successes, conflicts := 0, 0
	for err := range results {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, duel.ErrConflict):
			conflicts++
		default:
			t.Fatal("unexpected concurrent save error", err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatal("CAS did not serialize concurrent writes", successes, conflicts)
	}
	list, err := f.s.Loadouts(f.ctx, f.identity(0))
	if err != nil || len(list.Slots) != 1 || list.Slots[0].Revision != "3" {
		t.Fatal("concurrent revision", list, err)
	}
	if _, err := f.db.Exec(`UPDATE users SET is_banned=1,banned_until=NULL WHERE id=?`, f.identity(0).UserID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.SaveLoadout(f.ctx, in); !errors.Is(err, duel.ErrUnauthorized) {
		t.Fatal("banned account replayed a saved preset", err)
	}
	if _, err := f.db.Exec(`UPDATE users SET is_banned=0 WHERE id=?`, f.identity(0).UserID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`DELETE FROM users WHERE id=?`, f.identity(0).UserID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.SaveLoadout(f.ctx, in); !errors.Is(err, duel.ErrUnauthorized) {
		t.Fatal("deleted account replayed a saved preset", err)
	}
	if _, err := f.s.SaveLoadout(f.ctx, duel.SaveLoadoutInput{Identity: f.identity(0), IdempotencyKey: f.key(), Slot: 2, ExpectedRevision: "0", Mode: "quick", Loadout: f.loadouts[0]}); !errors.Is(err, duel.ErrUnauthorized) {
		t.Fatal("deleted account saved a preset", err)
	}
	var count int
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM game_likes_loadouts WHERE user_id=?`, f.identity(0).UserID).Scan(&count); err != nil || count != 0 {
		t.Fatal("deleted account retained presets", count, err)
	}
}

func TestCustomPresetHTTPStrictOwnerAndNoQueueSideEffect(t *testing.T) {
	f := newFixture(t, "likes")
	routes := &registrar{mux: http.NewServeMux(), identity: f.identity(0)}
	if err := f.s.RegisterRoutes(routes, routes); err != nil {
		t.Fatal(err)
	}
	request := func(method, path, body string, keys ...string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		for _, key := range keys {
			r.Header.Add("Idempotency-Key", key)
		}
		w := httptest.NewRecorder()
		routes.mux.ServeHTTP(w, r)
		return w
	}
	path := "/api/games/likes/loadouts/1"
	body := `{"expected_revision":"0","mode":"quick","loadout":` + string(f.loadouts[0]) + `}`
	for _, test := range []struct {
		method, path, body string
		keys               []string
		status             int
	}{
		{"GET", "/api/games/likes/loadouts?x=1", "", nil, 400},
		{"GET", "/api/games/likes/loadouts", "{}", nil, 400},
		{"PUT", path, body, nil, 400},
		{"PUT", path, strings.TrimSuffix(body, "}") + `,"name":null}`, []string{f.key()}, 400},
		{"PUT", path, body, []string{f.key(), f.key()}, 400},
		{"PUT", "/api/games/likes/loadouts/01", body, []string{f.key()}, 400},
		{"PUT", "/api/games/likes/loadouts/11", body, []string{f.key()}, 400},
		{"PUT", path, `{"expected_revision":"0","mode":"quick","loadout":{"role":"ChatGPT","harness":null,"skills":["PUB01"],"skills":["PUB02"]}}`, []string{f.key()}, 400},
	} {
		got := request(test.method, test.path, test.body, test.keys...)
		if got.Code != test.status {
			t.Fatalf("%s %s status=%d body=%s", test.method, test.path, got.Code, got.Body.String())
		}
	}
	key := f.key()
	first := request("PUT", path, body, key)
	if first.Code != 200 || first.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("save failed", first.Code, first.Body.String())
	}
	if replay := request("PUT", path, body, key); replay.Code != 200 || replay.Body.String() != first.Body.String() {
		t.Fatal("HTTP retry changed receipt", replay.Code, replay.Body.String())
	}
	if stale := request("PUT", path, body, f.key()); stale.Code != 409 {
		t.Fatal("stale HTTP save", stale.Code, stale.Body.String())
	}
	owner := request("GET", "/api/games/likes/loadouts", "")
	if owner.Code != 200 || !strings.Contains(owner.Body.String(), `"capacity":10`) {
		t.Fatal("owner list", owner.Code, owner.Body.String())
	}
	routes.identity = f.identity(1)
	other := request("GET", "/api/games/likes/loadouts", "")
	if other.Code != 200 || strings.Contains(other.Body.String(), `"slot":1`) {
		t.Fatal("cross-user HTTP list", other.Code, other.Body.String())
	}
	if f.read(0).Queue != nil || f.read(0).Current != nil {
		t.Fatal("saving a custom preset enqueued or started a game")
	}
}
