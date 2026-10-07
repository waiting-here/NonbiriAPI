package lakenotes

import (
	"database/sql"
	"errors"
	"sync"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
)

func TestDefaultClosedAndFreeConcurrentStart(t *testing.T) {
	f := newFixture(t)
	view, err := f.service.Profile(f.ctx(f.user), f.user)
	if err != nil || !view.Readonly || view.Profile.Coins != "0" {
		t.Fatal(view, err)
	}
	for _, v := range view.Settings.Exchanges {
		if v.Enabled {
			t.Fatal("exchange enabled by default")
		}
	}
	if _, err = f.service.Start(f.ctx(f.user), f.user, testKey(3), StartInput{"1"}); !errors.Is(err, ErrClosed) {
		t.Fatal(err)
	}
	if _, err = f.service.Periods(f.ctx(f.user), 1, 20); !errors.Is(err, authz.ErrForbidden) {
		t.Fatal("non-admin period read", err)
	}
	f.enable(t)
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := f.service.Start(f.ctx(f.user), f.user, testKey(20+i), StartInput{"1"})
			errs <- err
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	var charges, entries, casts int
	if err = f.database.QueryRow("SELECT (SELECT count(*) FROM credit_operations WHERE kind='lake_entry'),(SELECT count(*) FROM lake_notes_entitlements),(SELECT count(*) FROM lake_notes_casts)").Scan(&charges, &entries, &casts); err != nil || charges != 0 || entries != 0 || casts != 1 {
		t.Fatal(charges, entries, casts, err)
	}
}
func TestFourExactExchangesReplayAndAtomicFailure(t *testing.T) {
	f := newFixture(t)
	f.fund(t, f.user, ledger.General, 100000)
	f.fund(t, f.user, ledger.Game, 100000)
	p := f.enable(t)
	v := f.profile(t)
	for i, d := range []Direction{GeneralToCoins, CoinsToGeneral, GameToCoins, CoinsToGame} {
		q, e := f.service.Quote(f.ctx(f.user), f.user, QuoteInput{d, "2"})
		if e != nil || q.SourceAmount != "14" || q.TargetAmount != "500" {
			t.Fatal(q, e)
		}
		in := ExchangeInput{QuoteInput: QuoteInput{d, "2"}, ExpectedSettingsRevision: p.Revision, ExpectedProfileRevision: v.Revision}
		k := testKey(40 + i)
		out, e := f.service.Exchange(f.ctx(f.user), f.user, k, in)
		if e != nil {
			t.Fatal(d, e)
		}
		replay, e := f.service.Exchange(f.ctx(f.user), f.user, k, in)
		if e != nil || !replay.Replayed || out.Value.Receipt.ID != replay.Value.Receipt.ID {
			t.Fatal(replay, e)
		}
		v = out.Value.Profile
	}
	if v.Profile.Coins != "972" || v.Wallet.GeneralMilli != "100486" || v.Wallet.GameMilli != "100486" {
		t.Fatal(v)
	}
	before := v
	bad := ExchangeInput{QuoteInput: QuoteInput{CoinsToGeneral, "200"}, ExpectedSettingsRevision: p.Revision, ExpectedProfileRevision: v.Revision}
	if _, e := f.service.Exchange(f.ctx(f.user), f.user, testKey(50), bad); !errors.Is(e, ledger.ErrInsufficientBalance) {
		t.Fatal(e)
	}
	after, e := f.service.Profile(f.ctx(f.user), f.user)
	if e != nil || after.Revision != before.Revision || after.Profile.Coins != before.Profile.Coins || after.Wallet != before.Wallet {
		t.Fatal(after, e)
	}
	for i, quantity := range []string{"0", "1.5", "01", "340282366920938463463374607431768211455"} {
		if _, e := f.service.Quote(f.ctx(f.user), f.user, QuoteInput{GeneralToCoins, quantity}); !errors.Is(e, ErrInvalid) {
			t.Fatal(i, e)
		}
	}
	p.Exchanges[GeneralToCoins] = ExchangeSetting{Enabled: true, SourceAmount: "7", TargetAmount: "251"}
	changed, e := f.configure(p.Wire)
	if e != nil {
		t.Fatal(e)
	}
	bad = ExchangeInput{QuoteInput: QuoteInput{GeneralToCoins, "1"}, ExpectedSettingsRevision: p.Revision, ExpectedProfileRevision: v.Revision}
	if _, e = f.service.Exchange(f.ctx(f.user), f.user, testKey(52), bad); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
	changed.Enabled = false
	if _, e = f.configure(changed.Wire); e != nil {
		t.Fatal(e)
	}
	bad.ExpectedSettingsRevision = changed.Revision
	if _, e = f.service.Exchange(f.ctx(f.user), f.user, testKey(53), bad); !errors.Is(e, ErrClosed) {
		t.Fatal(e)
	}
	f.tx(t, func(tx *sql.Tx) {
		var entries int
		if e := tx.QueryRow("SELECT count(*) FROM lake_notes_exchange_receipts WHERE user_id=?", f.user).Scan(&entries); e != nil || entries != 4 {
			t.Fatal(entries, e)
		}
	})
}
