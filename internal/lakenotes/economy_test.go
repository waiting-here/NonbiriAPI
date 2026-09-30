package lakenotes

import (
	"database/sql"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
)

func TestDefaultsPeriodsAndAuthority(t *testing.T) {
	f := newFixture(t)
	view, e := f.service.Profile(f.ctx(f.user), f.user)
	if e != nil || view.Profile.Coins != "0" || view.Profile.Day != 1 || view.Profile.ClockMinutes != 360 || view.Period != nil {
		t.Fatal(view, e)
	}
	if _, e = f.service.Start(f.ctx(f.user), f.user, testKey(3), StartInput{"1"}); !errors.Is(e, ErrClosed) {
		t.Fatal(e)
	}
	in := PeriodInput{ExpectedRevision: "0", Name: "Draft", Status: "draft", StartsAt: testNow, EndsAt: testNow + 100, Exchanges: map[Direction]ExchangeSetting{}}
	draft, e := f.service.SavePeriod(f.ctx(f.admin), f.admin, "", testKey(4), in)
	if e != nil || draft.Value.EntryFeeMilli != nil {
		t.Fatal(draft, e)
	}
	if _, e = f.service.SavePeriod(f.ctx(f.user), f.user, "", testKey(5), in); !errors.Is(e, authz.ErrUnauthorized) && !errors.Is(e, authz.ErrForbidden) {
		t.Fatal(e)
	}
	in.Status = "published"
	in.ExpectedRevision = draft.Value.Revision
	if _, e = f.service.SavePeriod(f.ctx(f.admin), f.admin, draft.Value.ID, testKey(6), in); !errors.Is(e, ErrInvalid) {
		t.Fatal(e)
	}
	in.EntryFeeMilli = ptr("0")
	published, e := f.service.SavePeriod(f.ctx(f.admin), f.admin, draft.Value.ID, testKey(7), in)
	if e != nil {
		t.Fatal(e)
	}
	in.ExpectedRevision = "0"
	if _, e = f.service.SavePeriod(f.ctx(f.admin), f.admin, "", testKey(8), in); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
	in.StartsAt, in.EndsAt = testNow+100, testNow+200
	if _, e = f.service.SavePeriod(f.ctx(f.admin), f.admin, "", testKey(9), in); e != nil {
		t.Fatal(e)
	}
	if len(published.Value.Exchanges) != 4 {
		t.Fatal(published)
	}
	for _, v := range published.Value.Exchanges {
		if v.Enabled {
			t.Fatal("default exchange enabled")
		}
	}
}
func TestEntryConcurrentFeeChangeAndNextPeriod(t *testing.T) {
	f := newFixture(t)
	f.fund(t, f.user, ledger.General, 10000)
	p := f.period(t, "250")
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, e := f.service.Entry(f.ctx(f.user), f.user, testKey(20+i), EntryInput{p.ID, p.Revision})
			errs <- e
		}(i)
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	var count int
	if e := f.database.QueryRow("SELECT count(*) FROM credit_operations WHERE kind='lake_entry'").Scan(&count); e != nil || count != 1 {
		t.Fatal(count, e)
	}
	in := PeriodInput{p.Revision, "Renamed", "published", p.StartsAt, p.EndsAt, ptr("900"), p.Exchanges}
	changed, e := f.service.SavePeriod(f.ctx(f.admin), f.admin, p.ID, testKey(22), in)
	if e != nil {
		t.Fatal(e)
	}
	receipt, e := f.service.Entry(f.ctx(f.user), f.user, testKey(23), EntryInput{p.ID, p.Revision})
	if e != nil || receipt.Value.Receipt.FeeMilli != "250" || receipt.Value.Profile.Wallet.GeneralMilli != "9750" {
		t.Fatal(receipt, e)
	}
	if _, e = f.service.Entry(f.ctx(f.other), f.other, testKey(24), EntryInput{p.ID, p.Revision}); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
	f.fund(t, f.other, ledger.General, 1000)
	if _, e = f.service.Entry(f.ctx(f.other), f.other, testKey(25), EntryInput{p.ID, changed.Value.Revision}); e != nil {
		t.Fatal(e)
	}
	in.ExpectedRevision = "0"
	in.StartsAt, in.EndsAt = p.EndsAt, p.EndsAt+100
	next, e := f.service.SavePeriod(f.ctx(f.admin), f.admin, "", testKey(26), in)
	if e != nil {
		t.Fatal(e)
	}
	f.now.Store(next.Value.StartsAt * int64(time.Second))
	second, e := f.service.Entry(f.ctx(f.user), f.user, testKey(27), EntryInput{next.Value.ID, next.Value.Revision})
	if e != nil || second.Value.Receipt.FeeMilli != "900" || second.Value.Profile.Wallet.GeneralMilli != "8850" {
		t.Fatal(second, e)
	}
}
func TestFourExactExchangesReplayAndAtomicFailure(t *testing.T) {
	f := newFixture(t)
	f.fund(t, f.user, ledger.General, 100000)
	f.fund(t, f.user, ledger.Game, 100000)
	p := f.period(t, "0")
	v := f.enter(t, p)
	for i, d := range []Direction{GeneralToCoins, CoinsToGeneral, GameToCoins, CoinsToGame} {
		q, e := f.service.Quote(f.ctx(f.user), f.user, QuoteInput{d, "2", p.ID})
		if e != nil || q.SourceAmount != "14" || q.TargetAmount != "500" {
			t.Fatal(q, e)
		}
		in := ExchangeInput{QuoteInput: QuoteInput{d, "2", p.ID}, ExpectedPeriodRevision: p.Revision, ExpectedProfileRevision: v.Revision}
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
	bad := ExchangeInput{QuoteInput: QuoteInput{CoinsToGeneral, "200", p.ID}, ExpectedPeriodRevision: p.Revision, ExpectedProfileRevision: v.Revision}
	if _, e := f.service.Exchange(f.ctx(f.user), f.user, testKey(50), bad); !errors.Is(e, ledger.ErrInsufficientBalance) {
		t.Fatal(e)
	}
	after, e := f.service.Profile(f.ctx(f.user), f.user)
	if e != nil || after.Revision != before.Revision || after.Profile.Coins != before.Profile.Coins || after.Wallet != before.Wallet {
		t.Fatal(after, e)
	}
	for i, quantity := range []string{"0", "1.5", "01", "340282366920938463463374607431768211455"} {
		if _, e := f.service.Quote(f.ctx(f.user), f.user, QuoteInput{GeneralToCoins, quantity, p.ID}); !errors.Is(e, ErrInvalid) {
			t.Fatal(i, e)
		}
	}
	in := PeriodInput{p.Revision, p.Name, p.Status, p.StartsAt, p.EndsAt, p.EntryFeeMilli, p.Exchanges}
	in.Exchanges[GeneralToCoins] = ExchangeSetting{true, "7", "251"}
	changed, e := f.service.SavePeriod(f.ctx(f.admin), f.admin, p.ID, testKey(51), in)
	if e != nil {
		t.Fatal(e)
	}
	bad = ExchangeInput{QuoteInput: QuoteInput{GeneralToCoins, "1", p.ID}, ExpectedPeriodRevision: p.Revision, ExpectedProfileRevision: v.Revision}
	if _, e = f.service.Exchange(f.ctx(f.user), f.user, testKey(52), bad); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
	if _, e = f.database.Exec("UPDATE limited_activity_configs SET paused=1 WHERE activity_key=?", Key); e != nil {
		t.Fatal(e)
	}
	bad.ExpectedPeriodRevision = changed.Value.Revision
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
