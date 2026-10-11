package gacha

import (
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/economyaudit"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
)

func TestLedgerCompactionPreservesActivityReceiptsAndDonationOnlyRewards(t *testing.T) {
	f := newFixture(t)
	f.enter(t)
	f.fund(t, ledger.Game, 10000000)
	buy := Action{Action: "buy", Ticket: "permanent", Quantity: 1, Revision: 1}
	if _, err := f.act(f.user, "purchase", buy); err != nil {
		t.Fatal(err)
	}
	for _, card := range catalog.Cards {
		if card.Theme == "a" {
			f.exec(t, "INSERT INTO gacha_owned(user_id,card_id,obtained_at,source) VALUES(?,?,?,'draw')", f.user, card.ID, f.at.Load())
		}
	}
	claim := Action{Action: "claim", Theme: "a"}
	if _, err := f.act(f.user, "theme", claim); err != nil {
		t.Fatal(err)
	}
	f.at.Add(ledger.DetailRetentionSeconds + 1)
	tx, err := f.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	ready, err := economyaudit.AdvanceTx(t.Context(), tx, f.at.Load())
	if err != nil || !ready {
		tx.Rollback()
		t.Fatal(ready, err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	drained := false
	for range 10 {
		result, err := ledger.RetainDetails(t.Context(), f.db, f.at.Load(), 100)
		if err != nil {
			t.Fatal(err)
		}
		if !result.More {
			drained = true
			break
		}
	}
	if !drained {
		t.Fatal("ledger retention did not finish")
	}
	if count := f.scalar(t, "SELECT count(*) FROM credit_operations c JOIN gacha_operations g ON g.credit_operation_id=c.id WHERE c.compacted=1"); count != 2 {
		t.Fatal("activity receipts lost after compaction", count)
	}
	if count := f.scalar(t, "SELECT count(*) FROM credit_entries"); count != 0 {
		t.Fatal(count)
	}
	for _, item := range []struct {
		key   string
		input Action
	}{{"purchase", buy}, {"theme", claim}} {
		out, err := f.act(f.user, item.key, item.input)
		if err != nil || out.Snapshot.Tickets["permanent"] != 12 || out.Snapshot.Donation != "10" {
			t.Fatal(out, err)
		}
	}
	tx, err = f.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err = ledger.ValidateRecovery(t.Context(), tx); err != nil {
		t.Fatal(err)
	}
}
