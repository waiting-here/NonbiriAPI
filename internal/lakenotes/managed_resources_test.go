package lakenotes

import (
	"database/sql"
	"errors"
	"testing"
)

func TestManagedResourcesRefuseLiveCastAndPreserveProfile(t *testing.T) {
	f := newFixture(t)
	f.enable(t)
	v := f.profile(t)
	start, err := f.service.Start(f.ctx(f.user), f.user, testKey(920), StartInput{v.Revision})
	if err != nil {
		t.Fatal(err)
	}
	if start.Value.Cast.Phase != "waiting" {
		t.Fatal(start.Value.Cast.Phase)
	}
	f.tx(t, func(tx *sql.Tx) {
		before, err := profileTx(f.ctx(f.user), tx, f.user, false, testNow)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := SetManagedResourceTx(f.ctx(f.user), tx, f.user, testNow, "lake:coins", "100"); !errors.Is(err, ErrConflict) {
			t.Fatal("live cast adjustment accepted", err)
		}
		after, err := profileTx(f.ctx(f.user), tx, f.user, false, testNow)
		if err != nil || before.profile.Coins != after.profile.Coins || before.revision != after.revision {
			t.Fatal("rejected write changed profile", err)
		}
	})
}
