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

func TestCustomPresetNamesUnicodeOmissionClearingAndRename(t *testing.T) {
	f := newFixture(t, "likes")
	name := strings.Repeat("🐟", 20)
	in := duel.SaveLoadoutInput{Identity: f.identity(0), IdempotencyKey: f.key(), Slot: 1, ExpectedRevision: "0", Mode: "quick", Loadout: f.loadouts[0], Name: &name}
	if _, err := f.s.SaveLoadout(f.ctx, in); err != nil {
		t.Fatal(err)
	}
	in.Name = nil
	in.IdempotencyKey = f.key()
	in.ExpectedRevision = "1"
	in.Mode = "standard"
	if _, err := f.s.SaveLoadout(f.ctx, in); err != nil {
		t.Fatal(err)
	}
	list, err := f.s.Loadouts(f.ctx, f.identity(0))
	if err != nil || list.Slots[0].Name != name {
		t.Fatal("old PUT omitted name did not preserve", list, err)
	}
	original := list.Slots[0]
	renamed := duel.RenameLoadoutInput{Identity: f.identity(0), IdempotencyKey: f.key(), Slot: 1, ExpectedRevision: "2", Name: "<b>鱼</b>"}
	first, err := f.s.RenameLoadout(f.ctx, renamed)
	if err != nil {
		t.Fatal(err)
	}
	again, err := f.s.RenameLoadout(f.ctx, renamed)
	if err != nil || !again.Replayed || string(first.Body) != string(again.Body) {
		t.Fatal("rename receipt", again, err)
	}
	var got duel.LoadoutItem
	if json.Unmarshal(first.Body, &got) != nil || got.Name != renamed.Name || got.Mode != original.Mode || string(got.Loadout) != string(original.Loadout) || got.Revision != "3" {
		t.Fatal("rename changed selection", got)
	}
	renamed.Name = "different"
	if _, err := f.s.RenameLoadout(f.ctx, renamed); !errors.Is(err, duel.ErrConflict) {
		t.Fatal("key accepted different name", err)
	}
	empty := ""
	in.Name = &empty
	in.ExpectedRevision = "3"
	in.IdempotencyKey = f.key()
	if _, err := f.s.SaveLoadout(f.ctx, in); err != nil {
		t.Fatal(err)
	}
	list, err = f.s.Loadouts(f.ctx, f.identity(0))
	if err != nil || list.Slots[0].Name != "" {
		t.Fatal("explicit empty name did not clear")
	}
	for _, invalid := range []string{strings.Repeat("🐟", 21), "a\nb", "a\rb", "a\tb", "a\x00b", "a\x7fb", "a\u0085b", "a\u2028b", string([]byte{0xff})} {
		in.Name = &invalid
		in.ExpectedRevision = "4"
		in.IdempotencyKey = f.key()
		if _, err := f.s.SaveLoadout(f.ctx, in); !errors.Is(err, duel.ErrInvalidRequest) {
			t.Fatalf("invalid name accepted %q: %v", invalid, err)
		}
		if _, err := f.s.RenameLoadout(f.ctx, duel.RenameLoadoutInput{Identity: f.identity(0), IdempotencyKey: f.key(), Slot: 1, ExpectedRevision: "4", Name: invalid}); !errors.Is(err, duel.ErrInvalidRequest) {
			t.Fatalf("invalid rename accepted %q: %v", invalid, err)
		}
	}
	for _, slot := range []int{2, 3} {
		in.Slot = slot
		in.ExpectedRevision = "0"
		in.IdempotencyKey = f.key()
		in.Name = &name
		if _, err := f.s.SaveLoadout(f.ctx, in); err != nil {
			t.Fatal("duplicate names forbidden", err)
		}
	}
	if _, err := f.s.RenameLoadout(f.ctx, duel.RenameLoadoutInput{Identity: f.identity(1), IdempotencyKey: f.key(), Slot: 1, ExpectedRevision: "4", Name: "x"}); !errors.Is(err, duel.ErrNotFound) {
		t.Fatal("foreign slot rename", err)
	}
	f.ledger()
}

func TestCustomPresetRenameCompetesWithSaveAndCannotResurrectDeletedOwner(t *testing.T) {
	f := newFixture(t, "likes")
	if _, err := f.s.SaveLoadout(f.ctx, duel.SaveLoadoutInput{Identity: f.identity(0), IdempotencyKey: f.key(), Slot: 1, ExpectedRevision: "0", Mode: "quick", Loadout: f.loadouts[0]}); err != nil {
		t.Fatal(err)
	}
	rename := duel.RenameLoadoutInput{Identity: f.identity(0), IdempotencyKey: f.key(), Slot: 1, ExpectedRevision: "1", Name: "renamed"}
	save := duel.SaveLoadoutInput{Identity: f.identity(0), IdempotencyKey: f.key(), Slot: 1, ExpectedRevision: "1", Mode: "standard", Loadout: f.loadouts[0]}
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	wg.Go(func() { _, err := f.s.RenameLoadout(f.ctx, rename); errs <- err })
	wg.Go(func() { _, err := f.s.SaveLoadout(f.ctx, save); errs <- err })
	wg.Wait()
	close(errs)
	ok, conflict := 0, 0
	for err := range errs {
		if err == nil {
			ok++
		} else if errors.Is(err, duel.ErrConflict) {
			conflict++
		} else {
			t.Fatal(err)
		}
	}
	if ok != 1 || conflict != 1 {
		t.Fatal("save and rename did not share CAS", ok, conflict)
	}
	if _, err := f.db.Exec("DELETE FROM users WHERE id=?", f.identity(0).UserID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.RenameLoadout(f.ctx, rename); !errors.Is(err, duel.ErrUnauthorized) {
		t.Fatal("deleted owner replay", err)
	}
	var count int
	if err := f.db.QueryRow("SELECT COUNT(*) FROM game_likes_loadouts WHERE user_id=?", f.identity(0).UserID).Scan(&count); err != nil || count != 0 {
		t.Fatal("rename resurrected preset", count, err)
	}
}

func TestCustomPresetRenameHTTPStrictBodyAndNoQueueSideEffects(t *testing.T) {
	f := newFixture(t, "likes")
	routes := &registrar{mux: http.NewServeMux(), identity: f.identity(0)}
	if err := f.s.RegisterRoutes(routes, routes); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.SaveLoadout(f.ctx, duel.SaveLoadoutInput{Identity: f.identity(0), IdempotencyKey: f.key(), Slot: 1, ExpectedRevision: "0", Mode: "quick", Loadout: f.loadouts[0]}); err != nil {
		t.Fatal(err)
	}
	request := func(path, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("PATCH", path, strings.NewReader(body))
		r.Header.Set("Idempotency-Key", f.key())
		w := httptest.NewRecorder()
		routes.mux.ServeHTTP(w, r)
		return w
	}
	for _, body := range []string{`{"expected_revision":"1"}`, `{"expected_revision":"1","name":null}`, `{"expected_revision":"1","name":"x","mode":"standard"}`, `{"expected_revision":"1","name":"x","loadout":{}}`, `{"expected_revision":"1","name":"x","name":"y"}`} {
		if w := request("/api/games/likes/loadouts/1", body); w.Code != 400 {
			t.Fatal("non-name PATCH accepted", body, w.Code)
		}
	}
	if w := request("/api/games/likes/loadouts/2", `{"expected_revision":"0","name":"x"}`); w.Code != 404 {
		t.Fatal("PATCH created empty slot", w.Code)
	}
	if w := request("/api/games/likes/loadouts/1", `{"expected_revision":"1","name":""}`); w.Code != 200 {
		t.Fatal("rename", w.Code, w.Body.String())
	}
	if f.read(0).Queue != nil || f.read(0).Current != nil {
		t.Fatal("rename caused game side effect")
	}
}
