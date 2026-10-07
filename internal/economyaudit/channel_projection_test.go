package economyaudit

import (
	"context"
	"database/sql"
	"net/http/httptest"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/ledger"
)

func TestEveryClosedLedgerKindFitsAuditChannelsForEveryAsset(t *testing.T) {
	f := newAuditFixture(t)
	var definition string
	if err := f.database.QueryRow("SELECT sql FROM sqlite_schema WHERE name='credit_operations'").Scan(&definition); err != nil {
		t.Fatal(err)
	}
	kinds := regexp.MustCompile(`kind TEXT NOT NULL CHECK\(kind IN \(([^)]+)\)\)`).FindStringSubmatch(definition)
	if len(kinds) != 2 {
		t.Fatal("closed ledger kind constraint missing")
	}
	tx, err := f.database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec("INSERT INTO economy_audit_buckets VALUES('general','audit_fixture','operation','unknown','day',0,0,'0','0','0','0','0',1,1,1)"); err == nil {
		t.Fatal("fresh schema accepted unknown channel")
	}
	for _, raw := range strings.Split(kinds[1], ",") {
		kind := ledger.Kind(strings.Trim(raw, "'"))
		seen := map[string]bool{}
		for _, prefix := range []string{"bid_", "lik_", "gwt_", "future_"} {
			source := prefix + strings.Repeat("A", 22)
			classification := ledger.ClassifyForAudit(kind, source)
			usesDuelSource := strings.HasPrefix(string(kind), "duel_") || kind == ledger.KindAITicket || kind == ledger.KindAITerminal
			wantKnown := !usesDuelSource || prefix != "future_"
			if classification.Known != wantKnown {
				t.Fatal("closed kind classification coverage changed", kind, source, classification)
			}
			if seen[classification.Channel] {
				continue
			}
			seen[classification.Channel] = true
			for _, asset := range ledger.Assets() {
				_, err := tx.Exec(`INSERT INTO economy_audit_buckets VALUES(?,?,'operation',?,'day',0,0,'0','0','0','0','0',1,1,1)`, asset, kind, classification.Channel)
				if err != nil {
					t.Fatalf("asset %s kind %s channel %s: %v", asset, kind, classification.Channel, err)
				}
			}
		}
	}
}

func TestFatFishAndLakeChannelsProjectRetryAndAllReadAPIs(t *testing.T) {
	f := newAuditFixture(t)
	f.fund(t, 1, auditNow-30)
	if ready, err := f.service.Advance(f.ctx); err != nil || !ready {
		t.Fatal(ready, err)
	}
	f.tx(t, func(tx *sql.Tx) {
		ctx := context.Background()
		platform, err := ledger.CodedAccount(ctx, tx, "platform")
		if err != nil {
			t.Fatal(err)
		}
		for i, constructor := range []func(ledger.Meta, int64, int64, ledger.Amount) (ledger.Plan, error){ledger.NewFatFishUnlock, ledger.NewFatFishTicket} {
			p, e := constructor(f.meta(t, auditNow-20+int64(i)), f.wallets[ledger.General], platform.ID, ledger.AmountFromMilli(100))
			applyAuditPlan(t, tx, p, e)
		}
		meta := f.meta(t, auditNow-18)
		meta.ActorUserID = 0
		p, e := ledger.NewFatFishReward(meta, f.external[ledger.General], f.wallets[ledger.General], ledger.AmountFromMilli(50))
		applyAuditPlan(t, tx, p, e)
		meta = f.meta(t, auditNow-17)
		meta.ActorUserID = 0
		p, e = ledger.NewFatFishRefund(meta, platform.ID, f.wallets[ledger.General], ledger.AmountFromMilli(100))
		applyAuditPlan(t, tx, p, e)
		p, e = ledger.NewLakeEntry(f.meta(t, auditNow-16), f.wallets[ledger.General], f.external[ledger.General], ledger.AmountFromMilli(50))
		applyAuditPlan(t, tx, p, e)
		p, e = ledger.NewLakeExchange(f.meta(t, auditNow-15), f.wallets[ledger.Game], f.external[ledger.Game], ledger.Game, ledger.AmountFromMilli(75))
		applyAuditPlan(t, tx, p, e)
	})
	// Both new channels share the same atomic batch. A rollback must preserve
	// the previous checkpoint and the already populated admin bucket.
	tx, err := f.database.BeginTx(f.ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if ready, err := AdvanceTx(f.ctx, tx, auditNow); err != nil || !ready {
		t.Fatal(ready, err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	var last int
	if err := f.database.QueryRow("SELECT last_ledger_seq FROM economy_audit_checkpoint").Scan(&last); err != nil || last != 1 {
		t.Fatal(last, err)
	}
	if ready, err := f.service.Advance(f.ctx); err != nil || !ready {
		t.Fatal(ready, err)
	}
	if ready, err := f.service.Advance(f.ctx); err != nil || !ready {
		t.Fatal(ready, err)
	}
	checkAuditHTTPAssets(t, f, auditNow-60)
	general, err := f.service.Summary(f.ctx, f.admin, auditFilter(ledger.General))
	if err != nil || general.Flows.Issued != "1050" || general.Flows.Reclaimed != "50" || general.Flows.InternalTransfer != "300" || general.Reconciliation.Status != "matched" {
		t.Fatal(general, err)
	}
	for _, v := range []struct {
		kind, channel string
		count         int
	}{
		{"fatfish_unlock", "fat_fish", 1}, {"fatfish_ticket", "fat_fish", 1}, {"fatfish_reward", "fat_fish", 1}, {"fatfish_refund", "fat_fish", 1}, {"lake_entry", "lake_notes", 1},
	} {
		filter := auditFilter(ledger.General)
		filter.Kind = v.kind
		filter.Channel = v.channel
		operations, err := f.service.Operations(f.ctx, f.admin, filter)
		if err != nil || len(operations.Data) != v.count || operations.Data[0].Classification.Channel != v.channel {
			t.Fatal(v, operations, err)
		}
		channels, err := f.service.Channels(f.ctx, f.admin, filter)
		if err != nil || len(channels.Data) != 1 || channels.Data[0].Channel != v.channel {
			t.Fatal(v, channels, err)
		}
	}
	f.fund(t, 1, auditNow-1)
	if ready, err := f.service.Advance(f.ctx); err != nil || !ready {
		t.Fatal(ready, err)
	}
	general, err = f.service.Summary(f.ctx, f.admin, auditFilter(ledger.General))
	if err != nil || general.Flows.Issued != "2050" || general.Metadata.ProjectedSeq != "8" {
		t.Fatal(general, err)
	}
}

func checkAuditHTTPAssets(t *testing.T, f *auditFixture, from int64) {
	t.Helper()
	routes := auditTestRoutes{}
	if err := RegisterRoutes(routes, f.service); err != nil {
		t.Fatal(err)
	}
	for _, asset := range ledger.Assets() {
		for _, name := range []string{"summary", "series", "channels", "operations"} {
			r := httptest.NewRequest("GET", "/admin/api/economy-audit/"+name+"?asset="+string(asset)+"&from="+strconv.FormatInt(from, 10), nil).WithContext(f.ctx)
			w := httptest.NewRecorder()
			routes[r.URL.Path](w, r, AdminPrincipal{f.admin})
			if w.Code != 200 {
				t.Fatalf("%s %s: %d %s", asset, name, w.Code, w.Body.String())
			}
		}
	}
}

func TestEconomyAuditCatchUpFromReleasedBinary(t *testing.T) {
	source := os.Getenv("NONBIRI_ECONOMY_AUDIT_FIXTURE")
	if source == "" {
		t.Skip("released-source gate supplies a consistent fixture")
	}
	f := newAuditFixtureFromSource(t, source)
	var last int64
	if err := f.database.QueryRow("SELECT last_ledger_seq FROM economy_audit_checkpoint").Scan(&last); err != nil || last != 1 {
		t.Fatal("checkpoint was not preserved", last, err)
	}
	var buckets int
	if err := f.database.QueryRow("SELECT count(*) FROM economy_audit_buckets").Scan(&buckets); err != nil || buckets != 2 {
		t.Fatal("source buckets were not preserved", buckets, err)
	}
	f.fund(t, 101, auditNow-200)
	if ready, err := f.service.Advance(f.ctx); err != nil || ready {
		t.Fatal("catch-up exceeded one bounded batch", ready, err)
	}
	if err := f.database.QueryRow("SELECT last_ledger_seq FROM economy_audit_checkpoint").Scan(&last); err != nil || last != 101 {
		t.Fatal("catch-up did not resume at next operation", last, err)
	}
	for attempts := 0; attempts < 10; attempts++ {
		ready, err := f.service.Advance(f.ctx)
		if err != nil {
			t.Fatal(err)
		}
		if ready {
			break
		}
		if attempts == 9 {
			t.Fatal("bounded catch-up did not reach source watermark")
		}
	}
	filter := auditFilter(ledger.General)
	filter.From = 0
	before, err := f.service.Summary(f.ctx, f.admin, filter)
	if err != nil || before.Metadata.ProjectedSeq != before.Metadata.LedgerSeq || before.Reconciliation.Status != "matched" {
		t.Fatal(before, err)
	}
	channels, err := f.service.Channels(f.ctx, f.admin, filter)
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, channel := range channels.Data {
		found[channel.Channel] = true
	}
	if !found["fat_fish"] || !found["lake_notes"] {
		t.Fatal("source new-channel operations did not catch up", channels)
	}
	f.fund(t, 1, auditNow-1)
	after, err := f.service.Summary(f.ctx, f.admin, filter)
	if err != nil || after.Metadata.ProjectedSeq != after.Metadata.LedgerSeq || after.Reconciliation.Status != "matched" {
		t.Fatal(after, err)
	}
	f.reopen(t)
	reopened, err := f.service.Summary(f.ctx, f.admin, filter)
	if err != nil || reopened.Flows != after.Flows || reopened.Metadata.ProjectedSeq != after.Metadata.ProjectedSeq || reopened.Reconciliation.Status != "matched" {
		t.Fatal("reopen changed projection or replayed existing operations", reopened, err)
	}
	checkAuditHTTPAssets(t, f, auditNow-86400)
}
