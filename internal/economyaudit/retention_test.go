package economyaudit

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
)

func drainLedgerRetention(t *testing.T, f *auditFixture, now int64) {
	t.Helper()
	for range 100 {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		result, err := ledger.RetainDetails(ctx, f.database, now, 3)
		cancel()
		if err != nil {
			t.Fatal(err)
		}
		if !result.More {
			return
		}
		if result.Processed == 0 || result.Processed > 3 {
			t.Fatal(result)
		}
	}
	t.Fatal("retention did not drain")
}

func TestLedgerRetentionDrainsFullBatches(t *testing.T) {
	f := newAuditFixture(t)
	f.tx(t, func(tx *sql.Tx) {
		for range 101 {
			p, err := ledger.NewAdminUserAdjustment(f.meta(t, auditNow-ledger.DetailRetentionSeconds-1), f.wallets[ledger.General], f.external[ledger.General], ledger.AmountFromMilli(1), 0, ledger.Amount{}, "funding")
			applyAuditPlan(t, tx, p, err)
		}
	})
	for i := range 2 {
		if ready, err := f.service.Advance(f.ctx); err != nil || ready != (i == 1) {
			t.Fatal(ready, err)
		}
	}
	for _, count := range []int{100, 1, 0} {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		result, err := ledger.RetainDetails(ctx, f.database, auditNow, 100)
		cancel()
		if err != nil || result.Processed != count || result.More != (count > 0) {
			t.Fatal(result, err)
		}
	}
	f.tx(t, func(tx *sql.Tx) {
		if err := ledger.ValidateRecovery(f.ctx, tx); err != nil {
			t.Fatal(err)
		}
	})
}

func TestLedgerRetentionPreservesBalancesAuditReceiptsAndRecovery(t *testing.T) {
	f := newAuditFixture(t)
	ctx := context.Background()
	cutoff := auditNow - ledger.DetailRetentionSeconds
	old := cutoff - 7200
	var oldPlan, receiptPlan ledger.Plan
	var oldID, receiptID string
	f.tx(t, func(tx *sql.Tx) {
		meta := f.meta(t, old)
		oldID = meta.OperationID
		p, e := ledger.NewAdminUserAdjustment(meta, f.wallets[ledger.General], f.external[ledger.General], ledger.AmountFromMilli(1000000), f.user, ledger.AmountFromMilli(7000), "old funding")
		oldPlan = p
		applyAuditPlan(t, tx, p, e)
		p, e = ledger.NewAdminGameAdjustment(f.meta(t, old), f.wallets[ledger.Game], f.external[ledger.Game], ledger.AmountFromMilli(-5000), "old debt")
		applyAuditPlan(t, tx, p, e)
		for _, asset := range []ledger.Asset{ledger.SketchPaper, ledger.SketchBrush} {
			p, e = ledger.NewActivityExchange(f.meta(t, old+1), f.wallets[ledger.General], f.external[ledger.General], f.wallets[asset], f.external[asset], asset, ledger.AmountFromMilli(10000), ledger.AmountFromMilli(3000))
			applyAuditPlan(t, tx, p, e)
		}
		meta = f.meta(t, old+2)
		receiptID = meta.OperationID
		p, e = ledger.NewCheckinAward(meta, f.wallets[ledger.General], f.external[ledger.General], ledger.AmountFromMilli(1000))
		receiptPlan = p
		applyAuditPlan(t, tx, p, e)
		day := time.Unix(db.SiteDayKey(old+2, 330), 0).UTC().Format("2006-01-02")
		if _, e = tx.Exec(`INSERT INTO checkins(user_id,site_day,award_milli,operation_id,created_at) VALUES(?,?,?,?,?)`, f.user, day, 1000, receiptID, old+2); e != nil {
			t.Fatal(e)
		}
		for _, at := range []int64{cutoff, cutoff + 1, auditNow - 1} {
			p, e = ledger.NewAdminUserAdjustment(f.meta(t, at), f.wallets[ledger.General], f.external[ledger.General], ledger.AmountFromMilli(1), 0, ledger.Amount{}, "recent funding")
			applyAuditPlan(t, tx, p, e)
		}
	})
	// Without projected totals, no ledger detail may be removed.
	drainLedgerRetention(t, f, auditNow)
	var through int64
	if err := f.database.QueryRow(`SELECT through_seq FROM credit_compaction`).Scan(&through); err != nil || through != 0 {
		t.Fatal(through, err)
	}
	from := bucketStart(old, 330, 3600)
	filter := Filter{Asset: ledger.General, From: from, To: auditNow + 1, Bucket: "day"}
	before := map[ledger.Asset]Summary{}
	for _, asset := range ledger.Assets() {
		filter.Asset = asset
		result, err := f.service.Summary(f.ctx, f.admin, filter)
		if err != nil || result.Reconciliation.Status != "matched" {
			t.Fatal(asset, result, err)
		}
		before[asset] = result
	}
	checkViews := func() {
		f.tx(t, func(tx *sql.Tx) {
			page, err := ledger.UserHistory(ctx, tx, f.user, auditNow, ledger.HistoryFilter{Asset: "all", Page: 1, PageSize: 20})
			if err != nil || page.Total != "2" {
				t.Fatal(page, err)
			}
			entries, err := ledger.ExportUserEntries(ctx, tx, f.user, auditNow, 100)
			if err != nil || len(entries) != 2 {
				t.Fatal(entries, err)
			}
		})
	}
	checkViews()
	drainLedgerRetention(t, f, auditNow)
	f.reopen(t)
	f.tx(t, func(tx *sql.Tx) {
		if err := ledger.ValidateRecovery(ctx, tx); err != nil {
			t.Fatal("full recovery", err)
		}
		if err := ledger.ValidateRecovery(db.ActiveRecoveryContext(ctx), tx); err != nil {
			t.Fatal("active recovery", err)
		}
		if err := db.ValidateAssetLedger(ctx, tx); err != nil {
			t.Fatal("asset validation", err)
		}
		for _, plan := range []ledger.Plan{oldPlan, receiptPlan} {
			if _, err := ledger.Apply(ctx, tx, plan); !errors.Is(err, ledger.ErrConflict) {
				t.Fatal("expired replay", err)
			}
		}
	})
	var count int
	if err := f.database.QueryRow(`SELECT count(*) FROM credit_operations WHERE id=?`, oldID).Scan(&count); err != nil || count != 0 {
		t.Fatal(count, err)
	}
	if err := f.database.QueryRow(`SELECT compacted FROM credit_operations WHERE id=? AND actor_user_id IS NULL AND reason IS NULL`, receiptID).Scan(&count); err != nil || count != 1 {
		t.Fatal("receipt", count, err)
	}
	if err := f.database.QueryRow(`SELECT count(*) FROM credit_entries WHERE operation_id=?`, receiptID).Scan(&count); err != nil || count != 0 {
		t.Fatal(count, err)
	}
	var donation []byte
	if err := f.database.QueryRow(`SELECT donation_credit_mag FROM users WHERE id=?`, f.user).Scan(&donation); err != nil {
		t.Fatal(err)
	}
	value, err := db.DecodeU128(donation)
	if err != nil || value.Big().String() != "7000" {
		t.Fatal(value, err)
	}
	checkViews()
	for _, asset := range ledger.Assets() {
		filter.Asset = asset
		after, err := f.service.Summary(f.ctx, f.admin, filter)
		if err != nil || after.Flows != before[asset].Flows || !reflect.DeepEqual(after.Inventory, before[asset].Inventory) || !reflect.DeepEqual(after.Reconciliation, before[asset].Reconciliation) {
			t.Fatal(asset, after, err)
		}
	}
	filter.Asset = ledger.General
	filter.From, filter.To = old+1, old+3
	oldSummary, err := f.service.Summary(f.ctx, f.admin, filter)
	if err != nil || !oldSummary.Metadata.RangeAdjusted || oldSummary.Metadata.From != from || oldSummary.Metadata.To != from+3600 || oldSummary.Flows.Issued != "1001000" {
		t.Fatal(oldSummary, err)
	}
	operations, err := f.service.Operations(f.ctx, f.admin, filter)
	if err != nil || len(operations.Data) != 0 {
		t.Fatal(operations, err)
	}
	// A later operation remains valid, and the next pass can compact a ledger
	// with no remaining suffix. A second pass must not count balances twice.
	f.tx(t, func(tx *sql.Tx) {
		p, e := ledger.NewAdminUserAdjustment(f.meta(t, auditNow+10), f.wallets[ledger.General], f.external[ledger.General], ledger.AmountFromMilli(5), 0, ledger.Amount{}, "new funding")
		applyAuditPlan(t, tx, p, e)
	})
	if _, err := f.service.Advance(ctx); err != nil {
		t.Fatal(err)
	}
	for _, now := range []int64{auditNow + ledger.DetailRetentionSeconds + 20, auditNow + ledger.DetailRetentionSeconds + 21} {
		drainLedgerRetention(t, f, now)
	}
	f.tx(t, func(tx *sql.Tx) {
		if err := ledger.ValidateRecovery(ctx, tx); err != nil {
			t.Fatal(err)
		}
	})
	// Account deletion removes its opening baselines along with its wallets.
	f.tx(t, func(tx *sql.Tx) {
		wallets := []ledger.AssetWallet{}
		for _, asset := range ledger.Assets() {
			wallets = append(wallets, ledger.AssetWallet{Asset: asset, WalletID: f.wallets[asset], ExternalID: f.external[asset]})
		}
		p, e := ledger.NewAssetWalletsDeleteZero(f.meta(t, auditNow+ledger.DetailRetentionSeconds+30), wallets)
		applyAuditPlan(t, tx, p, e)
		if _, e = tx.Exec(`DELETE FROM credit_accounts WHERE user_id=?`, f.user); e != nil {
			t.Fatal(e)
		}
		if err := ledger.ValidateRecovery(ctx, tx); err != nil {
			t.Fatal("deleted account recovery", err)
		}
	})
}

func TestLedgerRetentionProtectsSettlementAndHeldRequest(t *testing.T) {
	f := newAuditFixture(t)
	ctx := context.Background()
	old := auditNow - ledger.DetailRetentionSeconds - 100
	requestID := auditID(t, "req_")
	ref, _ := ledger.LogicalRequestReservation(requestID)
	one, _ := db.ParseU128Decimal("1")
	var forward, platform ledger.Account
	var reserveID string
	f.tx(t, func(tx *sql.Tx) {
		var e error
		forward, e = ledger.CodedAccount(ctx, tx, "forward_reserve")
		if e != nil {
			t.Fatal(e)
		}
		platform, e = ledger.CodedAccount(ctx, tx, "platform")
		if e != nil {
			t.Fatal(e)
		}
		p, e := ledger.NewAdminUserAdjustment(f.meta(t, old), f.wallets[ledger.General], f.external[ledger.General], ledger.AmountFromMilli(1000), 0, ledger.Amount{}, "funding")
		applyAuditPlan(t, tx, p, e)
		if e = ledger.Reserve(ctx, tx, ref, one, func(ctx context.Context, tx *sql.Tx) error {
			_, e := tx.Exec(`INSERT INTO logical_requests(id,user_id,route_kind,model_snapshot,state,attempt_limit,accounting_state,account_reserved_milli,settlement_destination,ledger_rows_remaining,created_at) VALUES(?,?,'openai_chat_completions','test','accepted',1,'reserved',100,'user',?,?)`, requestID, f.user, db.EncodeU128(one), old)
			return e
		}); e != nil {
			t.Fatal(e)
		}
		meta := f.meta(t, old+1)
		reserveID = meta.OperationID
		p, e = ledger.NewForwardReserve(meta, requestID, f.wallets[ledger.General], forward.ID, ledger.AmountFromMilli(100))
		applyAuditPlan(t, tx, p, e)
	})
	if _, err := f.service.Advance(ctx); err != nil {
		t.Fatal(err)
	}
	drainLedgerRetention(t, f, auditNow)
	assertReserve := func(compacted int) {
		t.Helper()
		var flag, entries int
		err := f.database.QueryRow(`SELECT compacted,(SELECT count(*) FROM credit_entries WHERE operation_id=o.id) FROM credit_operations o WHERE id=?`, reserveID).Scan(&flag, &entries)
		if err != nil || flag != compacted || compacted == 0 && entries != 2 || compacted == 1 && entries != 0 {
			t.Fatal(flag, entries, err)
		}
	}
	assertReserve(0)
	hold := auditID(t, "lgh_")
	f.tx(t, func(tx *sql.Tx) {
		if err := ledger.ValidateRecovery(ctx, tx); err != nil {
			t.Fatal(err)
		}
		destination, _ := ledger.UserSettlementDestination(f.wallets[ledger.General])
		p, e := ledger.NewForwardSettle(f.meta(t, auditNow), requestID, forward.ID, platform.ID, destination, ledger.AmountFromMilli(100), ledger.AmountFromMilli(80))
		if e != nil {
			t.Fatal(e)
		}
		if _, e = ledger.ConsumeReserved(ctx, tx, ref, p, func(ctx context.Context, tx *sql.Tx) error {
			_, e := tx.Exec(`UPDATE logical_requests SET state='terminal',caller_result_class='success',caller_status=200,accounting_state='committed',account_reserved_milli=0,ledger_rows_remaining=zeroblob(16),terminal_at=? WHERE id=?`, auditNow, requestID)
			return e
		}); e != nil {
			t.Fatal(e)
		}
		result, e := tx.Exec(`INSERT INTO request_logs(logical_request_id,user_id,model,route_kind,started_at,completed_at,caller_result_class,caller_status,status_code) VALUES(?,?,'test','openai_chat_completions',?,?,'success',200,200)`, requestID, f.user, old, auditNow)
		if e != nil {
			t.Fatal(e)
		}
		id, _ := result.LastInsertId()
		if _, e = tx.Exec(`INSERT INTO legal_holds(id,object_kind,object_ref,state,revision,basis,created_by_user_id,created_at,expires_at) VALUES(?,'request_log',?,'active',1,'test hold',?,?,?)`, hold, strconv.FormatInt(id, 10), f.admin, auditNow, auditNow+3600); e != nil {
			t.Fatal(e)
		}
	})
	drainLedgerRetention(t, f, auditNow+1)
	assertReserve(0)
	if _, err := f.database.Exec(`UPDATE legal_holds SET state='released',revision=2,ended_by_user_id=?,ended_at=?,end_reason='resolved',retain_until=? WHERE id=?`, f.admin, auditNow+2, auditNow+2+400*86400, hold); err != nil {
		t.Fatal(err)
	}
	drainLedgerRetention(t, f, auditNow+2)
	assertReserve(1)
	f.reopen(t)
	f.tx(t, func(tx *sql.Tx) {
		if err := ledger.ValidateRecovery(ctx, tx); err != nil {
			t.Fatal(err)
		}
		wallet, err := ledger.UserAccount(ctx, tx, f.user)
		if err != nil || wallet.Balance.Decimal() != "920" {
			t.Fatal(wallet, err)
		}
	})
}
