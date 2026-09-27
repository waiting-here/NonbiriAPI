package duel_test

import (
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/game/duel"
)

func TestCustomPresetsExportAndTransactionalDeletion(t *testing.T) {
	f := newFixture(t, "likes")
	for _, user := range f.users {
		if _, err := f.db.Exec(`INSERT INTO game_likes_loadouts(user_id,slot,revision,mode,loadout_json,updated_at) VALUES(?,1,1,'quick',?,100)`, user, string(f.loadouts[0])); err != nil {
			t.Fatal(err)
		}
	}
	tx, err := f.db.BeginTx(f.ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	exported, _, err := f.s.ExportTx(f.ctx, tx, f.users[0], 100, 10000)
	if err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	items := exported.(duel.Export).Loadouts
	if len(items) != 1 || items[0].Slot != 1 || items[0].Revision != "1" || string(items[0].Loadout) != string(f.loadouts[0]) {
		_ = tx.Rollback()
		t.Fatal(items)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	count := func(user int64) int {
		t.Helper()
		var n int
		if err := f.db.QueryRow(`SELECT count(*) FROM game_likes_loadouts WHERE user_id=?`, user).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	for _, commit := range []bool{false, true} {
		tx, err := f.db.BeginTx(f.ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		end, err := f.s.PrepareDeleteTx(f.ctx, tx, f.users[0], 100)
		if err != nil {
			_ = tx.Rollback()
			t.Fatal(err)
		}
		if commit {
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
			end.Commit()
			if count(f.users[0]) != 0 {
				t.Fatal("deleted presets survived")
			}
		} else {
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			end.Abort()
			if count(f.users[0]) != 1 {
				t.Fatal("rollback removed presets")
			}
		}
		if count(f.users[1]) != 1 {
			t.Fatal("another account's preset changed")
		}
	}
}
