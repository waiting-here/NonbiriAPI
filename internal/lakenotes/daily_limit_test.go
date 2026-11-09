package lakenotes

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math/big"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
)

func setDailyLimit(t *testing.T, f *fixture, p Settings, d Direction, limit string) Settings {
	t.Helper()
	v := p.Exchanges[d]
	v.DailySourceLimit = limit
	p.Exchanges[d] = v
	next, err := f.configure(p.Wire)
	if err != nil {
		t.Fatal(err)
	}
	return next
}
func expectAllowance(t *testing.T, f *fixture, user int64, d Direction, amount string) Quote {
	t.Helper()
	q, err := f.service.Quote(f.ctx(user), user, QuoteInput{d, "1"})
	if err != nil || q.DailySourceRemaining == nil || *q.DailySourceRemaining != amount {
		t.Fatal(q, err, "want", amount)
	}
	return q
}
func TestDailyExchangeFourDirectionsActualSpendAndReplay(t *testing.T) {
	f := newFixture(t)
	f.fund(t, f.user, ledger.General, 100000)
	f.fund(t, f.user, ledger.Game, 100000)
	p := f.enable(t)
	v := f.profile(t)
	if _, err := f.database.Exec("INSERT INTO site_config(key,value,updated_at) VALUES(?, '480', ?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", db.SiteTimezoneKey, testNow); err != nil {
		t.Fatal(err)
	}
	for _, d := range directions {
		p = setDailyLimit(t, f, p, d, "21")
	}
	for i, d := range []Direction{GeneralToCoins, CoinsToGeneral, GameToCoins, CoinsToGame} {
		q := expectAllowance(t, f, f.user, d, "21")
		in := ExchangeInput{QuoteInput: QuoteInput{d, "2"}, ExpectedSettingsRevision: p.Revision, ExpectedProfileRevision: v.Revision}
		out, err := f.service.Exchange(f.ctx(f.user), f.user, testKey(900+i), in)
		if err != nil || out.Value.Receipt.DailySourceRemaining == nil || *out.Value.Receipt.DailySourceRemaining != "7" {
			t.Fatal(out, err)
		}
		v = out.Value.Profile
		var locked string
		if err = f.database.QueryRow("SELECT value FROM site_config WHERE key='site_timezone_offset_locked'").Scan(&locked); err != nil || locked != "1" {
			t.Fatal("limited exchange did not pin site day", err)
		}
		expectAllowance(t, f, f.other, d, "21")
		q = expectAllowance(t, f, f.user, d, "7")
		if q.DailyResetAt != db.SiteDayKey(testNow, 480)+86400 {
			t.Fatal("wrong site reset", q)
		}
		p = setDailyLimit(t, f, p, d, "0")
		replay, err := f.service.Exchange(f.ctx(f.user), f.user, testKey(900+i), in)
		if err != nil || !replay.Replayed || replay.Value.Receipt.ID != out.Value.Receipt.ID {
			t.Fatal("replay rechecked cap", replay, err)
		}
		if _, err = f.service.Exchange(f.ctx(f.user), f.user, testKey(910+i), ExchangeInput{QuoteInput: QuoteInput{d, "1"}, ExpectedSettingsRevision: p.Revision, ExpectedProfileRevision: v.Revision}); !errors.Is(err, ErrDailyLimit) {
			t.Fatal("zero allowed new exchange", err)
		}
		p = setDailyLimit(t, f, p, d, "21")
	}
	setting := p.Exchanges[GeneralToCoins]
	setting.SourceAmount = "1"
	p.Exchanges[GeneralToCoins] = setting
	var err error
	p, err = f.configure(p.Wire)
	if err != nil {
		t.Fatal(err)
	}
	expectAllowance(t, f, f.user, GeneralToCoins, "7")
	p = setDailyLimit(t, f, p, GeneralToCoins, "10")
	expectAllowance(t, f, f.user, GeneralToCoins, "0")
	before := v
	if _, err = f.service.Exchange(f.ctx(f.user), f.user, testKey(930), ExchangeInput{QuoteInput: QuoteInput{GeneralToCoins, "1"}, ExpectedSettingsRevision: p.Revision, ExpectedProfileRevision: v.Revision}); !errors.Is(err, ErrDailyLimit) {
		t.Fatal(err)
	}
	after := f.profile(t)
	if after.Wallet != before.Wallet || after.Profile.Coins != before.Profile.Coins || after.Revision != before.Revision {
		t.Fatal("failed cap changed money")
	}
	f.tx(t, func(tx *sql.Tx) {
		exported, err := f.service.ExportUserTx(f.ctx(f.user), tx, f.user, 100)
		if err != nil || len(exported.Exchanges) != 4 || exported.Exchanges[0].SourceAmount != "14" {
			t.Fatal(exported, err)
		}
	})
	reset := db.SiteDayKey(testNow, 480) + 86400
	f.now.Store((reset - 1) * int64(time.Second))
	expectAllowance(t, f, f.user, GeneralToCoins, "0")
	f.now.Store(reset * int64(time.Second))
	expectAllowance(t, f, f.user, GeneralToCoins, "10")
	f.tx(t, func(tx *sql.Tx) {
		if _, err := f.service.PrepareDeleteTx(f.ctx(f.admin), tx, f.user, reset); err != nil {
			t.Fatal(err)
		}
	})
	var count int
	if err = f.database.QueryRow("SELECT count(*) FROM lake_notes_exchange_receipts WHERE user_id=?", f.user).Scan(&count); err != nil || count != 0 {
		t.Fatal(count, err)
	}
}
func TestDailyExchangeLimitSerializesConcurrentRequests(t *testing.T) {
	f := newFixture(t)
	f.fund(t, f.user, ledger.General, 100000)
	p := f.enable(t)
	v := f.profile(t)
	if _, err := f.database.Exec("INSERT INTO site_config(key,value,updated_at) VALUES(?, '0', ?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", db.SiteTimezoneKey, testNow); err != nil {
		t.Fatal(err)
	}
	p = setDailyLimit(t, f, p, GeneralToCoins, "7")
	in := ExchangeInput{QuoteInput: QuoteInput{GeneralToCoins, "1"}, ExpectedSettingsRevision: p.Revision, ExpectedProfileRevision: v.Revision}
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := f.service.Exchange(f.ctx(f.user), f.user, testKey(940+i), in)
			errs <- err
		}(i)
	}
	wg.Wait()
	close(errs)
	successes := 0
	for err := range errs {
		if err == nil {
			successes++
		} else if !errors.Is(err, ErrDailyLimit) && !errors.Is(err, ErrConflict) {
			t.Fatal(err)
		}
	}
	if successes != 1 {
		t.Fatal("concurrent cap bypass", successes)
	}
	expectAllowance(t, f, f.user, GeneralToCoins, "0")
}

func TestDailyLimitQuoteUsesReaderAndCancelledQueryReleasesConnection(t *testing.T) {
	f := newFixture(t)
	p := f.enable(t)
	f.profile(t)
	if _, err := f.database.Exec("INSERT INTO site_config(key,value,updated_at) VALUES(?, '0', ?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", db.SiteTimezoneKey, testNow); err != nil {
		t.Fatal(err)
	}
	setDailyLimit(t, f, p, GeneralToCoins, "7")
	writer, err := f.database.BeginTx(f.ctx(f.user), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Rollback()
	ctx, cancel := context.WithTimeout(f.ctx(f.user), time.Second)
	defer cancel()
	q, err := f.service.Quote(ctx, f.user, QuoteInput{GeneralToCoins, "1"})
	if err != nil || q.DailySourceRemaining == nil || *q.DailySourceRemaining != "7" {
		t.Fatal("reader waited for writer", q, err)
	}
	cancelled, stop := context.WithCancel(f.ctx(f.user))
	stop()
	if _, err = f.service.Quote(cancelled, f.user, QuoteInput{GeneralToCoins, "1"}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	expectAllowance(t, f, f.user, GeneralToCoins, "7")
}

func TestDailyAllowanceScale(t *testing.T) {
	if os.Getenv("NONBIRI_LAKE_DAILY_SCALE") != "1" {
		t.Skip("explicit bounded scale probe")
	}
	f := newFixture(t)
	f.fund(t, f.user, ledger.General, 100000)
	p := f.enable(t)
	v := f.profile(t)
	if _, err := f.database.Exec("INSERT INTO site_config(key,value,updated_at) VALUES(?, '0', ?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", db.SiteTimezoneKey, testNow); err != nil {
		t.Fatal(err)
	}
	out, err := f.service.Exchange(f.ctx(f.user), f.user, testKey(980), ExchangeInput{QuoteInput: QuoteInput{GeneralToCoins, "1"}, ExpectedSettingsRevision: p.Revision, ExpectedProfileRevision: v.Revision})
	if err != nil {
		t.Fatal(err)
	}
	p = setDailyLimit(t, f, p, GeneralToCoins, "340282366920938463463374607431768211455")
	one, _ := db.ParseU128Decimal("1")
	day := db.SiteDayKey(testNow, 0)
	for _, scale := range []int{1000, 100000} {
		var count int
		if err = f.database.QueryRow("SELECT count(*) FROM lake_notes_exchange_receipts WHERE user_id=?", f.user).Scan(&count); err != nil {
			t.Fatal(err)
		}
		_, err = f.database.Exec(`WITH RECURSIVE series(n) AS (SELECT ? UNION ALL SELECT n+1 FROM series WHERE n<?)
INSERT INTO lake_notes_exchange_receipts(id,user_id,config_revision,direction,quantity_mag,source_lot,target_lot,source_amount_mag,target_amount_mag,operation_key_hash,ledger_operation_id,created_at)
SELECT 'lne_'||printf('%021d',n)||'A',r.user_id,r.config_revision,r.direction,?,?,?, ?,?,CAST(printf('%032d',n) AS BLOB),r.ledger_operation_id,r.created_at
FROM series CROSS JOIN lake_notes_exchange_receipts r WHERE r.id=?`, count, scale-1, db.EncodeU128(one), db.EncodeU128(one), db.EncodeU128(one), db.EncodeU128(one), db.EncodeU128(one), out.Value.Receipt.ID)
		if err != nil {
			t.Fatal(err)
		}
		rows, err := f.service.readDatabase.Query("EXPLAIN QUERY PLAN "+dailySourceAmountsSQL, f.user, GeneralToCoins.Stored(), day, day+86400)
		if err != nil {
			t.Fatal(err)
		}
		plan := ""
		for rows.Next() {
			var a, b, c int
			var detail string
			if err = rows.Scan(&a, &b, &c, &detail); err != nil {
				t.Fatal(err)
			}
			plan += detail
		}
		err = rows.Err()
		rows.Close()
		if err != nil || !strings.Contains(plan, "COVERING INDEX idx_lake_exchange_daily_source") {
			t.Fatal(plan, err)
		}
		want := new(big.Int)
		want.SetString("340282366920938463463374607431768211455", 10)
		want.Sub(want, big.NewInt(int64(scale+6)))
		for sample := 0; sample < 3; sample++ {
			started := time.Now()
			q, err := f.service.Quote(f.ctx(f.user), f.user, QuoteInput{GeneralToCoins, "1"})
			elapsed := time.Since(started)
			if err != nil || q.DailySourceRemaining == nil || *q.DailySourceRemaining != want.String() {
				t.Fatal(q, err)
			}
			t.Logf("receipts=%d sample=%d quote=%s budget=%s plan=%s", scale, sample+1, elapsed, dailyAllowanceBudget, plan)
		}
		// Exhausted limits stop at the first sufficient receipt.
		p = setDailyLimit(t, f, p, GeneralToCoins, "1")
		started := time.Now()
		expectAllowance(t, f, f.user, GeneralToCoins, "0")
		t.Logf("receipts=%d exhausted=%s", scale, time.Since(started))
		p = setDailyLimit(t, f, p, GeneralToCoins, "340282366920938463463374607431768211455")
	}
	t.Log(fmt.Sprintf("actual SQLite engine, same-user same-day same-direction facts; database=%s", t.TempDir()))
}

func TestFailedDailyExchangePreservesBalanceAllowanceAndTimezone(t *testing.T) {
	f := newFixture(t)
	p := f.enable(t)
	v := f.profile(t)
	if _, err := f.database.Exec("INSERT INTO site_config(key,value,updated_at) VALUES(?, '0', ?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", db.SiteTimezoneKey, testNow); err != nil {
		t.Fatal(err)
	}
	p = setDailyLimit(t, f, p, GeneralToCoins, "7")
	var before int
	if err := f.database.QueryRow("SELECT count(*) FROM site_config WHERE key='site_timezone_offset_locked'").Scan(&before); err != nil {
		t.Fatal(err)
	}
	_, err := f.service.Exchange(f.ctx(f.user), f.user, testKey(990), ExchangeInput{QuoteInput: QuoteInput{GeneralToCoins, "1"}, ExpectedSettingsRevision: p.Revision, ExpectedProfileRevision: v.Revision})
	if !errors.Is(err, ledger.ErrInsufficientBalance) {
		t.Fatal(err)
	}
	expectAllowance(t, f, f.user, GeneralToCoins, "7")
	after := f.profile(t)
	if after.Wallet != v.Wallet || after.Profile.Coins != v.Profile.Coins || after.Revision != v.Revision {
		t.Fatal("failed payment changed profile")
	}
	var locks, receipts int
	if err = f.database.QueryRow("SELECT (SELECT count(*) FROM site_config WHERE key='site_timezone_offset_locked'),(SELECT count(*) FROM lake_notes_exchange_receipts WHERE user_id=?)", f.user).Scan(&locks, &receipts); err != nil || locks != before || receipts != 0 {
		t.Fatal(locks, receipts, err)
	}
}
