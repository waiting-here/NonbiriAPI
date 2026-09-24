package economyaudit

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/dbfixture"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

const auditNow int64 = 1_800_000_000

type testActorKey struct{}

type testFinalAuthorizer struct{ authority *authz.Authorizer }

func (a testFinalAuthorizer) AuthorizeAdmin(ctx context.Context, tx *sql.Tx, id int64) error {
	actor, ok := ctx.Value(testActorKey{}).(authz.Actor)
	if !ok || actor.UserID != id {
		return authz.ErrUnauthorized
	}
	_, err := a.authority.Authorize(ctx, tx, actor, authz.Requirement{Role: authz.RoleAdministrator})
	return err
}

type auditFixture struct {
	database                      *sql.DB
	service                       *Service
	admin, user, steward, trainee int64
	ctx                           context.Context
	actors                        map[int64]authz.Actor
	wallets                       map[ledger.Asset]int64
	external                      map[ledger.Asset]int64
}

func newAuditFixture(t *testing.T) *auditFixture {
	t.Helper()
	vault, err := secret.New(make([]byte, secret.MasterKeyBytes))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = vault.Close() })
	path := filepath.Join(t.TempDir(), "audit.db")
	dbfixture.Materialize(t, path)
	store, err := db.Open(path, vault)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	f := &auditFixture{database: store.DB(), actors: map[int64]authz.Actor{}, wallets: map[ledger.Asset]int64{}, external: map[ledger.Asset]int64{}}
	if _, err := f.database.Exec(`INSERT INTO site_config(key,value,updated_at) VALUES(?, '330', ?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, db.SiteTimezoneKey, auditNow); err != nil {
		t.Fatal(err)
	}
	seed := func(label string, admin, level int) int64 {
		zero := make([]byte, 16)
		result, err := f.database.Exec(`INSERT INTO users(discord_id,username,is_admin,level,donation_credit_mag,total_requests,total_uncached_input_tokens,total_cache_write_input_tokens,total_cache_read_input_tokens,total_output_tokens,total_unknown_usage_requests,revision,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, "audit-"+label, label, admin, level, zero, zero, zero, zero, zero, zero, zero, zero, auditNow-1000000, auditNow-1000000)
		if err != nil {
			t.Fatal(err)
		}
		id, err := result.LastInsertId()
		if err != nil {
			t.Fatal(err)
		}
		token, generation := "audit-session-"+label, "g1"
		if _, err := f.database.Exec(`INSERT INTO sessions(token_hash,user_id,last_seen_at,expires_at,absolute_expires_at,created_at,cred_gen) VALUES(?,?,?,?,?,?,?)`, token, id, auditNow, auditNow+3600, auditNow+7200, auditNow-10, generation); err != nil {
			t.Fatal(err)
		}
		kind := authz.ActorUserSession
		if admin == 1 {
			kind = authz.ActorAdminSession
		}
		f.actors[id] = authz.Actor{Kind: kind, UserID: id, SessionTokenHash: token, SessionGeneration: generation}
		return id
	}
	f.admin = seed("administrator", 1, 1)
	f.user = seed("user", 0, 1)
	f.steward = seed("steward", 0, 6)
	f.trainee = seed("trainee", 0, 5)
	f.ctx = context.WithValue(context.Background(), testActorKey{}, f.actors[f.admin])
	f.service, err = New(Config{Database: f.database, FinalAuth: testFinalAuthorizer{authz.New(authz.Options{Now: func() time.Time { return time.Unix(auditNow, 0) }})}, CursorKeys: vault, Now: func() time.Time { return time.Unix(auditNow, 0) }})
	if err != nil {
		t.Fatal(err)
	}
	f.tx(t, func(tx *sql.Tx) {
		for _, asset := range ledger.Assets() {
			wallet, err := ledger.CreateUserAssetAccount(context.Background(), tx, f.user, asset, auditNow-1000000)
			if err != nil {
				t.Fatal(err)
			}
			f.wallets[asset] = wallet.ID
			external, err := ledger.CodedAssetAccount(context.Background(), tx, "external", asset)
			if err != nil {
				t.Fatal(err)
			}
			f.external[asset] = external.ID
		}
	})
	return f
}

func (f *auditFixture) tx(t *testing.T, fn func(*sql.Tx)) {
	t.Helper()
	tx, err := f.database.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	fn(tx)
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func auditID(t *testing.T, prefix string) string {
	t.Helper()
	id, err := db.GenerateOpaqueID(prefix)
	if err != nil {
		t.Fatal(err)
	}
	return id
}
func (f *auditFixture) meta(t *testing.T, at int64) ledger.Meta {
	return ledger.Meta{OperationID: auditID(t, "op_"), ActorUserID: f.user, CreatedAt: at}
}
func applyAuditPlan(t *testing.T, tx *sql.Tx, plan ledger.Plan, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.Apply(context.Background(), tx, plan); err != nil {
		t.Fatal(err)
	}
}
func auditFilter(asset ledger.Asset) Filter {
	return Filter{Asset: asset, From: auditNow - 604800, To: auditNow + 1, Bucket: "day"}
}

func (f *auditFixture) fund(t *testing.T, count int, at int64) {
	t.Helper()
	f.tx(t, func(tx *sql.Tx) {
		for i := 0; i < count; i++ {
			p, e := ledger.NewAdminUserAdjustment(f.meta(t, at+int64(i)), f.wallets[ledger.General], f.external[ledger.General], ledger.AmountFromMilli(1000), 0, ledger.Amount{}, "funding")
			applyAuditPlan(t, tx, p, e)
		}
	})
}

func TestProjectionBatchesRollbackAndReplayWithoutDoubleIssuance(t *testing.T) {
	f := newAuditFixture(t)
	f.fund(t, 101, auditNow-200)
	tx, err := f.database.BeginTx(f.ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	ready, err := AdvanceTx(f.ctx, tx, auditNow)
	if err != nil || ready {
		t.Fatalf("batch ready=%v err=%v", ready, err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	var last, rows int
	if err := f.database.QueryRow(`SELECT last_ledger_seq FROM economy_audit_checkpoint`).Scan(&last); err != nil {
		t.Fatal(err)
	}
	if err := f.database.QueryRow(`SELECT count(*) FROM economy_audit_buckets`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if last != 0 || rows != 0 {
		t.Fatal("rollback left a partial projection")
	}
	first, err := f.service.Summary(f.ctx, f.admin, auditFilter(ledger.General))
	if err != nil {
		t.Fatal(err)
	}
	if first.Metadata.ProjectedSeq != "100" || first.Metadata.LedgerSeq != "101" || first.Inventory != nil || first.Reconciliation.Status != "catching_up" {
		t.Fatalf("mixed-watermark summary: %+v", first)
	}
	second, err := f.service.Summary(f.ctx, f.admin, auditFilter(ledger.General))
	if err != nil {
		t.Fatal(err)
	}
	if second.Flows.Issued != "101000" || second.Inventory.Net != "101000" || second.Reconciliation.Status != "matched" {
		t.Fatalf("reconciliation: %+v", second)
	}
	for i := 0; i < 2; i++ {
		ready, err := f.service.Advance(f.ctx)
		if err != nil || !ready {
			t.Fatal(ready, err)
		}
	}
	again, err := f.service.Summary(f.ctx, f.admin, auditFilter(ledger.General))
	if err != nil || again.Flows != second.Flows {
		t.Fatal("replay duplicated projection", err)
	}
	var locked string
	if err := f.database.QueryRow(`SELECT value FROM site_config WHERE key='site_timezone_offset_locked'`).Scan(&locked); err != nil || locked != "1" {
		t.Fatal("projection did not freeze business time zone", err)
	}
}

func TestFourAssetInventoryRefundAndDeletedUserHistory(t *testing.T) {
	f := newAuditFixture(t)
	at := auditNow - 500
	ctx := context.Background()
	id := auditID(t, "img_")
	var escrow ledger.SketchAccounts
	f.tx(t, func(tx *sql.Tx) {
		p, e := ledger.NewAdminUserAdjustment(f.meta(t, at), f.wallets[ledger.General], f.external[ledger.General], ledger.AmountFromMilli(1000000), 0, ledger.Amount{}, "funding")
		applyAuditPlan(t, tx, p, e)
		p, e = ledger.NewAdminGameAdjustment(f.meta(t, at), f.wallets[ledger.Game], f.external[ledger.Game], ledger.AmountFromMilli(-5000), "debt")
		applyAuditPlan(t, tx, p, e)
		for _, v := range []struct {
			asset          ledger.Asset
			cost, quantity int64
		}{{ledger.SketchPaper, 20000, 10000}, {ledger.SketchBrush, 100000, 2000}} {
			p, e = ledger.NewActivityExchange(f.meta(t, at+1), f.wallets[ledger.General], f.external[ledger.General], f.wallets[v.asset], f.external[v.asset], v.asset, ledger.AmountFromMilli(v.cost), ledger.AmountFromMilli(v.quantity))
			applyAuditPlan(t, tx, p, e)
		}
		paper, err := ledger.CodedAssetAccount(ctx, tx, "image_activity_reserve", ledger.SketchPaper)
		if err != nil {
			t.Fatal(err)
		}
		brush, err := ledger.CodedAssetAccount(ctx, tx, "image_activity_reserve", ledger.SketchBrush)
		if err != nil {
			t.Fatal(err)
		}
		escrow = ledger.SketchAccounts{Paper: paper.ID, Brush: brush.ID}
		ref, err := ledger.ImageTaskReservation(id)
		if err != nil {
			t.Fatal(err)
		}
		one, _ := db.ParseU128Decimal("1")
		control, model := auditID(t, "iup_"), auditID(t, "imdl_")
		paperPrice, _ := db.ParseU128Decimal("4000")
		brushPrice, _ := db.ParseU128Decimal("1000")
		if _, err := tx.ExecContext(ctx, `INSERT INTO image_upstream_control(id,identity_hash,rpm_limit,concurrency_limit,protection_paused,protection_reason,protection_revision,updated_at)
VALUES(?,randomblob(32),10,1,0,'',1,?)`, control, at); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO image_activity_models(id,control_id,upstream_model_id,metadata_json,current_revision,discovered_at) VALUES(?,?,'inventory-fixture','{}',1,?)`, model, control, at); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO image_model_revisions(model_id,revision,display_name,description,enabled,parameters_json,combinations_json,mapping_json,paper_price_mag,brush_price_mag,created_at)
VALUES(?,1,'Inventory fixture','',1,'[]','[]','{}',?,?,?)`, model, db.EncodeU128(paperPrice), db.EncodeU128(brushPrice), at); err != nil {
			t.Fatal(err)
		}
		if err := ledger.Reserve(ctx, tx, ref, one, func(ctx context.Context, tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, `INSERT INTO image_activity_tasks(id,user_id,model_id,model_revision,n,paper_charge_mag,brush_charge_mag,state,finance_state,slot_state,ledger_rows_remaining,created_at,updated_at,queue_deadline,execution_timeout_seconds)
VALUES(?,?,?,1,1,?,?,'queued','reserved','none',?,?,?,?,1800)`, id, f.user, model, db.EncodeU128(paperPrice), db.EncodeU128(brushPrice), db.EncodeU128(one), at+2, at+2, at+1802)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		p, e = ledger.NewImageReserve(f.meta(t, at+2), id, ledger.SketchAccounts{Paper: f.wallets[ledger.SketchPaper], Brush: f.wallets[ledger.SketchBrush]}, escrow, ledger.SketchPayment{Paper: ledger.AmountFromMilli(4000), Brush: ledger.AmountFromMilli(1000)})
		applyAuditPlan(t, tx, p, e)
	})
	for _, v := range []struct {
		asset                                               ledger.Asset
		issued, reclaimed, available, frozen, negative, net string
	}{{ledger.General, "1000000", "120000", "880000", "0", "0", "880000"}, {ledger.Game, "0", "5000", "0", "0", "5000", "-5000"}, {ledger.SketchPaper, "10000", "0", "6000", "4000", "0", "10000"}, {ledger.SketchBrush, "2000", "0", "1000", "1000", "0", "2000"}} {
		got, err := f.service.Summary(f.ctx, f.admin, auditFilter(v.asset))
		if err != nil {
			t.Fatal(err)
		}
		if got.Flows.Issued != v.issued || got.Flows.Reclaimed != v.reclaimed || got.Inventory.UserAvailable != v.available || got.Inventory.Frozen != v.frozen || got.Inventory.NegativeUsers != v.negative || got.Inventory.Net != v.net || got.Reconciliation.Status != "matched" {
			t.Fatalf("%s: %+v stock=%+v", v.asset, got, got.Inventory)
		}
	}
	f.tx(t, func(tx *sql.Tx) {
		meta := f.meta(t, at+3)
		meta.ActorUserID = 0
		plan, err := ledger.NewImageRefund(meta, id, escrow, ledger.SketchAccounts{Paper: f.wallets[ledger.SketchPaper], Brush: f.wallets[ledger.SketchBrush]}, ledger.SketchPayment{Paper: ledger.AmountFromMilli(4000), Brush: ledger.AmountFromMilli(1000)})
		if err != nil {
			t.Fatal(err)
		}
		ref, _ := ledger.ImageTaskReservation(id)
		_, err = ledger.ConsumeReserved(ctx, tx, ref, plan, func(ctx context.Context, tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, `UPDATE image_activity_tasks SET state='cancelled',finance_state='refunded',completed_at=?,ledger_rows_remaining=?,updated_at=? WHERE id=?`, at+3, make([]byte, 16), at+3, id)
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
	})
	refunded, err := f.service.Summary(f.ctx, f.admin, auditFilter(ledger.SketchPaper))
	if err != nil {
		t.Fatal(err)
	}
	if refunded.Flows.Issued != "10000" || refunded.Flows.InternalTransfer != "8000" || refunded.Inventory.Frozen != "0" || refunded.Inventory.UserAvailable != "10000" {
		t.Fatal("refund was minted or not released", refunded)
	}
	f.tx(t, func(tx *sql.Tx) {
		wallets := []ledger.AssetWallet{}
		for _, asset := range ledger.Assets() {
			wallets = append(wallets, ledger.AssetWallet{Asset: asset, WalletID: f.wallets[asset], ExternalID: f.external[asset]})
		}
		p, e := ledger.NewAssetWalletsDeleteZero(f.meta(t, at+4), wallets)
		applyAuditPlan(t, tx, p, e)
		if _, err := tx.Exec(`DELETE FROM image_activity_tasks WHERE id=?`, id); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(`DELETE FROM users WHERE id=?`, f.user); err != nil {
			t.Fatal(err)
		}
	})
	for _, asset := range ledger.Assets() {
		got, err := f.service.Summary(f.ctx, f.admin, auditFilter(asset))
		if err != nil || got.Inventory.Net != "0" || got.Reconciliation.Status != "matched" {
			t.Fatal("deleted account lost conservation", asset, got, err)
		}
	}
	page, err := f.service.Operations(f.ctx, f.admin, auditFilter(ledger.SketchPaper))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, o := range page.Data {
		for _, entry := range o.Entries {
			if entry.AccountKind == "user" {
				found = true
				if entry.UserID != nil {
					t.Fatal("deleted identity retained")
				}
			}
		}
	}
	if !found {
		t.Fatal("anonymous ledger history disappeared")
	}
}

func TestExactTimeRangesUseSiteBucketsAndDoNotDoubleCount(t *testing.T) {
	f := newAuditFixture(t)
	start := bucketStart(auditNow-3*86400, 330, 86400)
	for _, at := range []int64{start - 1, start, start + 1800, start + 86399, start + 86400, start + 2*86400 + 1, start + 3*86400} {
		f.fund(t, 1, at)
	}
	filter := Filter{Asset: ledger.General, From: start, To: start + 3*86400, Bucket: "day"}
	got, err := f.service.Summary(f.ctx, f.admin, filter)
	if err != nil {
		t.Fatal(err)
	}
	if got.Flows.Issued != "5000" || *got.Reconciliation.IntervalOpeningNet != "1000" || *got.Reconciliation.IntervalClosingNet != "6000" {
		t.Fatal("time-boundary flow mismatch", got)
	}
	series, err := f.service.Series(f.ctx, f.admin, filter)
	if err != nil {
		t.Fatal(err)
	}
	if len(series.Data) != 3 || series.Data[0].Metrics.Issued != "3000" || series.Data[1].Metrics.Issued != "1000" || series.Data[2].Metrics.Issued != "1000" {
		t.Fatal("daily buckets mismatch", series)
	}
	filter.From = start + 1
	filter.To = start + 86400 + 1
	filter.Bucket = "hour"
	got, err = f.service.Summary(f.ctx, f.admin, filter)
	if err != nil || got.Flows.Issued != "3000" {
		t.Fatal("partial hours were rounded", got, err)
	}
	filter.From = start
	filter.To = start + 744*3600 + 1
	if _, err := f.service.Series(f.ctx, f.admin, filter); !errors.Is(err, ErrInvalid) {
		t.Fatal("unbounded series accepted", err)
	}
}

func TestPoolAndPlatformTransfersAreNotReissuedAndLoansKeepSeparateAssets(t *testing.T) {
	f := newAuditFixture(t)
	f.tx(t, func(tx *sql.Tx) {
		var poolID int64
		if err := tx.QueryRow(`SELECT account_id FROM shared_pools WHERE pool_type='thursday' AND state='open' AND period_id IS NULL`).Scan(&poolID); err != nil {
			t.Fatal(err)
		}
		platform, err := ledger.CodedAccount(context.Background(), tx, "platform")
		if err != nil {
			t.Fatal(err)
		}
		p, e := ledger.NewAdminPoolAdjustment(f.meta(t, auditNow-9), poolID, f.external[ledger.General], ledger.AmountFromMilli(10000), "fund pool")
		applyAuditPlan(t, tx, p, e)
		p, e = ledger.NewWelfareClaim(f.meta(t, auditNow-8), poolID, f.wallets[ledger.General], ledger.AmountFromMilli(3000))
		applyAuditPlan(t, tx, p, e)
		p, e = ledger.NewLinkLinkEntry(f.meta(t, auditNow-7), auditID(t, "ll_"), f.wallets[ledger.General], platform.ID, ledger.AmountFromMilli(1000))
		applyAuditPlan(t, tx, p, e)
		p, e = ledger.NewActivityLoan(f.meta(t, auditNow-6), ledger.AccountPair{General: f.wallets[ledger.General], Game: f.wallets[ledger.Game]}, ledger.AccountPair{General: f.external[ledger.General], Game: f.external[ledger.Game]}, ledger.AmountFromMilli(4000), ledger.AmountFromMilli(5000))
		applyAuditPlan(t, tx, p, e)
	})
	g, err := f.service.Summary(f.ctx, f.admin, auditFilter(ledger.General))
	if err != nil {
		t.Fatal(err)
	}
	if g.Flows.Issued != "10000" || g.Flows.Reclaimed != "5000" || g.Flows.InternalTransfer != "4000" || g.Inventory.Pools != "7000" || g.Inventory.Platform != "1000" || g.Inventory.NegativeUsers != "3000" || g.Inventory.Net != "5000" || g.Reconciliation.Status != "matched" {
		t.Fatalf("general metrics %+v stock %+v", g, g.Inventory)
	}
	g, err = f.service.Summary(f.ctx, f.admin, auditFilter(ledger.Game))
	if err != nil || g.Flows.Issued != "4000" || g.Inventory.Net != "4000" || g.Reconciliation.Status != "matched" {
		t.Fatal("loan mixed currencies", g, err)
	}
	channels, err := f.service.Channels(f.ctx, f.admin, auditFilter(ledger.General))
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range channels.Data {
		if c.Channel == "welfare" && (c.Metrics.Issued != "0" || c.Metrics.UserIncome != "3000") {
			t.Fatal("pool payout counted as new issuance", c)
		}
	}
}

func TestAdminAuthorizationPrecedesProjectionAndEveryPageRead(t *testing.T) {
	f := newAuditFixture(t)
	f.fund(t, 1, auditNow-1)
	for _, id := range []int64{f.user, f.steward, f.trainee} {
		ctx := context.WithValue(context.Background(), testActorKey{}, f.actors[id])
		filter := auditFilter(ledger.General)
		for _, read := range []func() error{func() error { _, e := f.service.Summary(ctx, id, filter); return e }, func() error { _, e := f.service.Series(ctx, id, filter); return e }, func() error { _, e := f.service.Channels(ctx, id, filter); return e }, func() error { _, e := f.service.Operations(ctx, id, filter); return e }} {
			if err := read(); !errors.Is(err, authz.ErrForbidden) {
				t.Fatalf("role %d allowed audit: %v", id, err)
			}
		}
	}
	var last int
	if err := f.database.QueryRow(`SELECT last_ledger_seq FROM economy_audit_checkpoint`).Scan(&last); err != nil || last != 0 {
		t.Fatal("unauthorized request mutated projection", last, err)
	}
	if _, err := f.database.Exec(`DELETE FROM sessions WHERE user_id=?`, f.admin); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.Summary(f.ctx, f.admin, auditFilter(ledger.General)); !errors.Is(err, authz.ErrUnauthorized) {
		t.Fatal("revoked session retained authority", err)
	}
}

func TestOperationsCursorAnchorsBindsFiltersAndRejectsTampering(t *testing.T) {
	f := newAuditFixture(t)
	f.fund(t, 102, auditNow-200)
	filter := auditFilter(ledger.General)
	first, err := f.service.Operations(f.ctx, f.admin, filter)
	if err != nil || len(first.Data) != 100 || first.NextCursor == nil || first.AnchorSeq != "102" {
		t.Fatal("first page", first, err)
	}
	f.fund(t, 1, auditNow-1)
	filter.Cursor = *first.NextCursor
	second, err := f.service.Operations(f.ctx, f.admin, filter)
	if err != nil || len(second.Data) != 2 || second.Data[0].LedgerSeq != "2" || second.AnchorSeq != "102" || second.Metadata.LedgerSeq != "103" || second.NextCursor != nil {
		t.Fatal("anchored page", second, err)
	}
	filter.Asset = ledger.Game
	if _, err := f.service.Operations(f.ctx, f.admin, filter); !errors.Is(err, ErrInvalid) {
		t.Fatal("cross-asset cursor accepted", err)
	}
	filter.Asset = ledger.General
	filter.Cursor = filter.Cursor[:len(filter.Cursor)/2] + "x" + filter.Cursor[len(filter.Cursor)/2+1:]
	if _, err := f.service.Operations(f.ctx, f.admin, filter); !errors.Is(err, ErrInvalid) {
		t.Fatal("tampered cursor accepted", err)
	}
}

func TestConcurrentLedgerWritesAndAuditReadsUseSameWatermark(t *testing.T) {
	f := newAuditFixture(t)
	ctx := context.Background()
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 12; i++ {
			tx, err := f.database.BeginTx(ctx, nil)
			if err != nil {
				errs <- err
				return
			}
			id, err := db.GenerateOpaqueID("op_")
			if err == nil {
				var plan ledger.Plan
				plan, err = ledger.NewAdminUserAdjustment(ledger.Meta{OperationID: id, ActorUserID: f.user, CreatedAt: auditNow - 1}, f.wallets[ledger.General], f.external[ledger.General], ledger.AmountFromMilli(1000), 0, ledger.Amount{}, "funding")
				if err == nil {
					_, err = ledger.Apply(ctx, tx, plan)
				}
			}
			if err == nil {
				err = tx.Commit()
			} else {
				_ = tx.Rollback()
			}
			if err != nil {
				errs <- err
				return
			}
		}
	}()
	for i := 0; i < 12; i++ {
		got, err := f.service.Summary(f.ctx, f.admin, auditFilter(ledger.General))
		if err != nil {
			errs <- err
			break
		}
		if got.Inventory != nil && (got.Metadata.LedgerSeq != got.Metadata.ProjectedSeq || got.Inventory.Net != *got.Reconciliation.LedgerNet || got.Reconciliation.Status != "matched") {
			errs <- fmt.Errorf("mixed watermark: %+v", got)
			break
		}
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	got, err := f.service.Summary(f.ctx, f.admin, auditFilter(ledger.General))
	if err != nil || got.Flows.Issued != "12000" {
		t.Fatal("concurrent projection lost operations", got, err)
	}
}

type auditTestRoutes map[string]AuthorizedAdminHandler

func (r auditTestRoutes) RegisterAdminRoute(method, path string, h AuthorizedAdminHandler) error {
	if method != http.MethodGet || r[path] != nil {
		return ErrInvalid
	}
	r[path] = h
	return nil
}

func TestHTTPQueryBoundsAndSafeErrors(t *testing.T) {
	f := newAuditFixture(t)
	routes := auditTestRoutes{}
	if err := RegisterRoutes(routes, f.service); err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{"?from=01", "?asset=general&asset=game", "?asset=cash", "?unknown=1", "?from=5&to=4", "?bucket=week"} {
		r := httptest.NewRequest("GET", "/admin/api/economy-audit/summary"+suffix, nil).WithContext(f.ctx)
		w := httptest.NewRecorder()
		routes[r.URL.Path](w, r, AdminPrincipal{f.admin})
		if w.Code != 400 {
			t.Fatalf("%s: %d %s", suffix, w.Code, w.Body.String())
		}
	}
	for _, name := range []string{"summary", "series", "channels", "operations"} {
		r := httptest.NewRequest("GET", "/admin/api/economy-audit/"+name+"?from="+strconv.FormatInt(auditNow-86400, 10), nil).WithContext(f.ctx)
		w := httptest.NewRecorder()
		routes[r.URL.Path](w, r, AdminPrincipal{f.admin})
		if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("%s: %d %s", name, w.Code, w.Body.String())
		}
	}
	w := httptest.NewRecorder()
	writeError(w, fmt.Errorf("secret database detail"))
	if strings.Contains(w.Body.String(), "secret") || w.Code != 503 {
		t.Fatal("internal error leaked")
	}
}
