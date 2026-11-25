package duel_test

import (
	_ "embed"
	"github.com/waiting-here/NonbiriAPI/internal/game/duel"
	"github.com/waiting-here/NonbiriAPI/internal/game/gwent"
	"testing"
	"time"
)

//go:embed testdata/retired_gwent.sql.gz
var retiredGwent []byte

func TestHistoricalGwentTicketRefund(t *testing.T) {
	f := newUnrecoveredFixture(t, "gwent")
	if err := f.s.Close(); err != nil {
		t.Fatal(err)
	}
	f.options.AI = gwent.AIAdapter{}
	var err error
	f.s, err = duel.New(f.options)
	if err != nil {
		t.Fatal(err)
	}
	loadHistoricalFixture(t, f, unzipFixture(t, retiredGwent))
	for range 2 {
		if _, err := f.s.RecoverBeforeListenAt(f.ctx, 100, 100, time.Now().Add(2*time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	r := f.read(0).LatestResult
	if r == nil || r.Outcome != "system_cancelled" || r.OwnRefund.Game != "3" || r.OwnRefund.General != "2" {
		t.Fatal(r)
	}
	if err := f.s.ValidatePersistedState(f.ctx); err != nil {
		t.Fatal(err)
	}
	f.ledger()
}
