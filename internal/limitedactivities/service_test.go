package limitedactivities

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
	"github.com/waiting-here/NonbiriAPI/internal/maintenance"
	"math/big"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func TestDirectoryTimeAndReadiness(t *testing.T) {
	f := newFixture(t)
	ctx := f.ctx(f.user)
	list, e := f.service.List(ctx, f.user)
	if e != nil || len(list) != 0 {
		t.Fatalf("default directory: %v %v", list, e)
	}
	d, e := f.service.Detail(ctx, f.user, PictureBook)
	if e != nil || d.Status != "unconfigured" || d.Visible || supply(t, d).BrushRemaining != "10" {
		t.Fatalf("default detail: %+v %v", d, e)
	}
	if _, e = f.exchange(f.user, 1, ledger.SketchPaper, "1"); !errors.Is(e, ErrClosed) {
		t.Fatalf("unconfigured exchange: %v", e)
	}
	if _, e = f.service.Detail(ctx, f.user, "unknown"); !errors.Is(e, ErrNotFound) {
		t.Fatal(e)
	}
	var wallets int
	if e = f.database.QueryRow("SELECT COUNT(*) FROM credit_accounts WHERE user_id=? AND asset_type IN ('sketch_paper','sketch_brush')", f.user).Scan(&wallets); e != nil || wallets != 0 {
		t.Fatalf("read created wallets: %d %v", wallets, e)
	}
	start, end := testNow, testNow+30
	f.runtime.ready.Store(false)
	f.config(t, false, &start, &end, false, "10")
	d, e = f.service.Detail(ctx, f.user, PictureBook)
	if e != nil || d.Status != "unavailable" {
		t.Fatalf("runtime unavailable: %+v %v", d, e)
	}
	f.runtime.ready.Store(true)
	for _, row := range []struct {
		at     int64
		status string
	}{{start - 1, "scheduled"}, {start, "open"}, {end - 1, "open"}, {end, "ended"}} {
		f.now.Store(row.at)
		d, e = f.service.Detail(ctx, f.user, PictureBook)
		if e != nil || d.Status != row.status {
			t.Fatalf("boundary %d: %+v %v", row.at, d, e)
		}
	}
	list, e = f.service.List(ctx, f.user)
	if e != nil || len(list) != 0 {
		t.Fatalf("hidden entry in directory: %v", e)
	}
	f.service.registry = NewRegistry(nil)
	f.now.Store(testNow)
	if _, e = f.exchange(f.user, 2, ledger.SketchPaper, "1"); !errors.Is(e, ErrClosed) {
		t.Fatalf("nil runtime accepted: %v", e)
	}
}
func TestExchangeAtomicReplayAndPersistentCap(t *testing.T) {
	f := newFixture(t)
	f.open(t, "2")
	f.fund(t, f.user, 30000)
	first, e := f.exchange(f.user, 1, ledger.SketchBrush, "2")
	if e != nil {
		t.Fatal(e)
	}
	if first.Value.Wallet.General != "10000" || first.Value.Wallet.Brush != "2" || first.Value.Supply.BrushRemaining != "0" {
		t.Fatalf("wrong exchange: %+v", first)
	}
	second, e := f.exchange(f.user, 1, ledger.SketchBrush, "2")
	if e != nil || !second.Replayed || second.Value.Receipt.OperationID != first.Value.Receipt.OperationID {
		t.Fatalf("replay: %+v %v", second, e)
	}
	if f.recorder.calls.Load() != 1 {
		t.Fatal("replay counted as new activity")
	}
	if _, e = f.exchange(f.user, 1, ledger.SketchBrush, "1"); !errors.Is(e, ErrConflict) {
		t.Fatalf("changed body replay: %v", e)
	}
	if _, e = f.exchange(f.user, 2, ledger.SketchBrush, "1"); !errors.Is(e, ErrCapacity) {
		t.Fatalf("cap: %v", e)
	}
	f.config(t, true, nil, nil, false, "1")
	d := f.open(t, "1")
	if supply(t, d).BrushExchanged != "2" || supply(t, d).BrushRemaining != "0" {
		t.Fatalf("reopen lowered cap: %+v", d)
	}
	d = f.open(t, "3")
	if supply(t, d).BrushRemaining != "1" {
		t.Fatal("reopening reset total")
	}
	if _, e = f.exchange(f.user, 3, ledger.SketchBrush, "1"); e != nil {
		t.Fatal(e)
	}
	if _, e = f.exchange(f.user, 4, ledger.SketchPaper, "1"); !errors.Is(e, ledger.ErrInsufficientBalance) {
		t.Fatalf("insufficient payment: %v", e)
	}
	var count int
	if e = f.database.QueryRow("SELECT COUNT(*) FROM activity_exchange_receipts WHERE user_id=?", f.user).Scan(&count); e != nil || count != 2 {
		t.Fatalf("receipt atomicity: %d %v", count, e)
	}
	f.fund(t, f.user, 5000)
	f.recorder.fail.Store(true)
	if _, e = f.exchange(f.user, 5, ledger.SketchPaper, "1"); e == nil {
		t.Fatal("recorder rollback failure expected")
	}
	f.recorder.fail.Store(false)
	wallet, e := f.service.Wallet(f.ctx(f.user), f.user)
	if e != nil || wallet.General != "5000" || wallet.Paper != "0" {
		t.Fatalf("partial currency commit: %+v %v", wallet, e)
	}
	result, e := f.exchange(f.user, 5, ledger.SketchPaper, "1")
	if e != nil || result.Replayed || result.Value.Wallet.General != "4000" {
		t.Fatalf("rolled-back key poisoned: %+v %v", result, e)
	}
}
func TestLastBrushConcurrentExchanges(t *testing.T) {
	f := newFixture(t)
	f.open(t, "1")
	f.fund(t, f.user, 100000)
	var won, lost atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Go(func() {
			_, e := f.exchange(f.user, 100+i, ledger.SketchBrush, "1")
			switch {
			case e == nil:
				won.Add(1)
			case errors.Is(e, ErrCapacity):
				lost.Add(1)
			default:
				t.Errorf("unexpected contention: %v", e)
			}
		})
	}
	wg.Wait()
	if won.Load() != 1 || lost.Load() != 7 {
		t.Fatalf("winners=%d denied=%d", won.Load(), lost.Load())
	}
	wallet, e := f.service.Wallet(f.ctx(f.user), f.user)
	if e != nil || wallet.Brush != "1" || wallet.General != "90000" {
		t.Fatalf("concurrent wallet: %+v %v", wallet, e)
	}
}
func TestValidationAuthorizationAndReplayRecheck(t *testing.T) {
	f := newFixture(t)
	d := f.open(t, "10")
	f.fund(t, f.user, 10000)
	for _, quantity := range []string{"0", "-1", "1.5", "01", "", " 1", "170141183460469231731687303715884106", "340282366920938463463374607431768211456"} {
		if _, e := f.exchange(f.user, 1, ledger.SketchPaper, quantity); !errors.Is(e, ErrInvalid) {
			t.Fatalf("quantity %q: %v", quantity, e)
		}
	}
	if _, e := f.exchange(f.user, 2, ledger.SketchPaper, "1000000000000000000000000000000000"); !errors.Is(e, ErrInvalid) {
		t.Fatalf("cost overflow: %v", e)
	}
	in := ConfigInput{d.Revision, true, nil, nil, false, module(t, "1000", "10000", "10")}
	for _, id := range []int64{f.user, f.steward, f.trainee} {
		if _, e := f.service.UpdateConfig(f.ctx(id), id, PictureBook, key(91), in); !errors.Is(e, authz.ErrForbidden) {
			t.Fatalf("non-admin config %d: %v", id, e)
		}
	}
	if _, e := f.exchange(f.admin, 1, ledger.SketchPaper, "1"); !errors.Is(e, authz.ErrForbidden) {
		t.Fatalf("admin participation: %v", e)
	}
	if _, e := f.service.Exchange(f.ctx(f.other), f.user, key(1), ExchangeInput{ledger.SketchPaper, "1"}); !errors.Is(e, authz.ErrUnauthorized) {
		t.Fatalf("cross account: %v", e)
	}
	first, e := f.exchange(f.user, 3, ledger.SketchPaper, "1")
	if e != nil || first.Replayed {
		t.Fatal(e)
	}
	if _, e = f.database.Exec("UPDATE users SET is_banned=1,banned_until=NULL WHERE id=?", f.user); e != nil {
		t.Fatal(e)
	}
	if _, e = f.exchange(f.user, 3, ledger.SketchPaper, "1"); !errors.Is(e, authz.ErrForbidden) {
		t.Fatalf("banned replay: %v", e)
	}
	if _, e = f.database.Exec("UPDATE users SET is_banned=0 WHERE id=?", f.user); e != nil {
		t.Fatal(e)
	}
	if _, e = f.database.Exec("DELETE FROM sessions WHERE user_id=?", f.user); e != nil {
		t.Fatal(e)
	}
	if _, e = f.exchange(f.user, 3, ledger.SketchPaper, "1"); !errors.Is(e, authz.ErrUnauthorized) {
		t.Fatalf("revoked replay: %v", e)
	}
	if _, e = f.service.UpdateConfig(f.ctx(f.admin), f.admin, PictureBook, key(92), ConfigInput{ExpectedRevision: "1", ModuleConfig: in.ModuleConfig}); !errors.Is(e, ErrConflict) {
		t.Fatalf("stale config: %v", e)
	}
}
func TestMaintenancePauseAndDeletionFinalizers(t *testing.T) {
	f := newFixture(t)
	f.open(t, "10")
	f.fund(t, f.user, 3000)
	if _, e := f.exchange(f.user, 1, ledger.SketchPaper, "1"); e != nil {
		t.Fatal(e)
	}
	if _, e := f.database.Exec("UPDATE maintenance_state SET enabled=1 WHERE id=1"); e != nil {
		t.Fatal(e)
	}
	if _, e := f.exchange(f.user, 2, ledger.SketchPaper, "1"); !errors.Is(e, maintenance.ErrMaintenanceOn) {
		t.Fatalf("maintenance accepted: %v", e)
	}
	if _, e := f.database.Exec("UPDATE maintenance_state SET enabled=0 WHERE id=1"); e != nil {
		t.Fatal(e)
	}
	old, e := f.service.AdminConfig(f.ctx(f.admin), f.admin, PictureBook)
	if e != nil {
		t.Fatal(e)
	}
	input := ConfigInput{old.Revision, true, old.StartsAt, old.EndsAt, true, module(t, "1000", "10000", "10")}
	f.runtime.fail.Store(true)
	if _, e = f.service.UpdateConfig(f.ctx(f.admin), f.admin, PictureBook, key(80), input); e == nil {
		t.Fatal("prepare failure expected")
	}
	if f.runtime.aborts.Load() != 1 || f.runtime.commits.Load() != 0 {
		t.Fatal("failed prepare did not abort")
	}
	unchanged, e := f.service.AdminConfig(f.ctx(f.admin), f.admin, PictureBook)
	if e != nil || unchanged.Paused || unchanged.Revision != old.Revision {
		t.Fatalf("partial pause: %+v %v", unchanged, e)
	}
	f.runtime.fail.Store(false)
	paused, e := f.service.UpdateConfig(f.ctx(f.admin), f.admin, PictureBook, key(80), input)
	if e != nil || !paused.Value.Paused || f.runtime.commits.Load() != 1 {
		t.Fatalf("pause: %+v %v", paused, e)
	}
	if _, e = f.exchange(f.user, 2, ledger.SketchPaper, "1"); !errors.Is(e, ErrClosed) {
		t.Fatalf("paused accepted: %v", e)
	}
	tx, e := f.database.BeginTx(context.Background(), nil)
	if e != nil {
		t.Fatal(e)
	}
	final, e := f.service.PrepareDeleteTx(context.Background(), tx, f.user, testNow)
	if e != nil {
		t.Fatal(e)
	}
	if e = tx.Rollback(); e != nil {
		t.Fatal(e)
	}
	final.Abort()
	var owned int
	if e = f.database.QueryRow("SELECT COUNT(*) FROM activity_exchange_receipts WHERE user_id=?", f.user).Scan(&owned); e != nil || owned != 1 {
		t.Fatal("delete rollback deidentified receipt")
	}
	f.tx(t, func(tx *sql.Tx) {
		var err error
		final, err = f.service.PrepareDeleteTx(context.Background(), tx, f.user, testNow)
		if err != nil {
			t.Fatal(err)
		}
	})
	if !final.Commit() || final.Abort() {
		t.Fatal("finalizer not one-shot")
	}
	if e = f.database.QueryRow("SELECT COUNT(*) FROM activity_exchange_receipts WHERE user_id=?", f.user).Scan(&owned); e != nil || owned != 0 {
		t.Fatal("delete retained owner")
	}
	var total []byte
	if e = f.database.QueryRow("SELECT total_exchanged_mag FROM activity_exchange_state WHERE asset_type='sketch_paper'").Scan(&total); e != nil {
		t.Fatal(e)
	}
	units, e := db.DecodeU128(total)
	if e != nil || units.Decimal() != "1" {
		t.Fatal("delete reset totals")
	}
}
func TestExportSnapshotAndExactValues(t *testing.T) {
	f := newFixture(t)
	f.open(t, "10")
	f.fund(t, f.user, 5000)
	for i := 1; i <= 2; i++ {
		if _, e := f.exchange(f.user, i, ledger.SketchPaper, "1"); e != nil {
			t.Fatal(e)
		}
	}
	f.tx(t, func(tx *sql.Tx) {
		if _, e := f.service.ExportUserTx(context.Background(), tx, f.user, 1); !errors.Is(e, ErrExportLimit) {
			t.Fatalf("silent truncation: %v", e)
		}
		out, e := f.service.ExportUserTx(context.Background(), tx, f.user, 2)
		if e != nil || len(out.Exchanges) != 2 || out.Wallet.Paper != "2" {
			t.Fatalf("export: %+v %v", out, e)
		}
		other, e := f.service.ExportUserTx(context.Background(), tx, f.other, 2)
		if e != nil || len(other.Exchanges) != 0 {
			t.Fatalf("export isolation: %+v %v", other, e)
		}
	})
	n, _ := new(big.Int).SetString("123456789012345678901234567890001", 10)
	if got := points(n); got != "123456789012345678901234567890.001" {
		t.Fatal(got)
	}
	// Configuration DTOs never accept derived capacity counters as input.
	raw, _ := json.Marshal(supply(t, f.open(t, "10")))
	if _, _, _, e := decodeSettings(raw); !errors.Is(e, ErrInvalid) {
		t.Fatal("derived projection accepted as editable settings")
	}
	if strings.Contains(points(big.NewInt(-1)), "e") {
		t.Fatal("inexact amount")
	}
}
