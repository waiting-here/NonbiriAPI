package economyaudit

import (
	"database/sql"
	"fmt"
	"math/big"
	"reflect"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/ledger"
)

func TestPartialHoursMatchImmutableEntriesAcrossAssetsAndFilters(t *testing.T) {
	f := newAuditFixture(t)
	start := bucketStart(auditNow-7200, 330, 3600)
	f.fund(t, 300, start+100)
	f.fund(t, 180, start+3400)
	f.tx(t, func(tx *sql.Tx) {
		for i := 0; i < 125; i++ {
			plan, err := ledger.NewAdminUserAdjustment(f.meta(t, start+900), f.wallets[ledger.General], f.external[ledger.General], ledger.AmountFromMilli(1000), 0, ledger.Amount{}, "same second")
			applyAuditPlan(t, tx, plan, err)
		}
		for _, at := range []int64{start + 15, start + 3000} {
			plan, err := ledger.NewAdminGameAdjustment(f.meta(t, at), f.wallets[ledger.Game], f.external[ledger.Game], ledger.AmountFromMilli(9_000_000_000_000_000), "game funding")
			applyAuditPlan(t, tx, plan, err)
		}
		plan, err := ledger.NewActivityExchange(f.meta(t, start+25), f.wallets[ledger.General], f.external[ledger.General], f.wallets[ledger.SketchPaper], f.external[ledger.SketchPaper], ledger.SketchPaper, ledger.AmountFromMilli(1000), ledger.AmountFromMilli(2000))
		applyAuditPlan(t, tx, plan, err)
	})
	f.project(t)
	f.tx(t, func(tx *sql.Tx) {
		c, err := readCheckpoint(f.ctx, tx)
		if err != nil {
			t.Fatal(err)
		}
		bounds := [][2]int64{{start + 1, start + 3599}, {start + 100, start + 400}, {start + 900, start + 901}, {start + 3500, start + 3700}, {start + 3600, start + 3900}, {start + 2000, start + 2100}}
		for _, asset := range ledger.Assets() {
			for _, kind := range []string{"", string(ledger.KindAdminUserAdjustment)} {
				for i, interval := range bounds {
					t.Run(fmt.Sprintf("%s/%s/%d", asset, kind, i), func(t *testing.T) {
						filter := Filter{Asset: asset, From: interval[0], To: interval[1], Bucket: "hour", Kind: kind}
						if kind != "" {
							filter.Channel = "admin"
						}
						got, err := rangeMeasures(f.ctx, tx, c, filter)
						if err != nil {
							t.Fatal(err)
						}
						want := map[dimension]*measures{}
						if err := rawEdgeMeasures(f.ctx, tx, c, filter, filter.From, filter.To, want); err != nil {
							t.Fatal(err)
						}
						if !reflect.DeepEqual(channelsWire(got), channelsWire(want)) {
							t.Fatalf("hour aggregate changed exact entries: got %+v, want %+v", channelsWire(got), channelsWire(want))
						}
						if asset == ledger.General && kind == "" && i == 2 {
							total, err := sumDimensions(got)
							if err != nil || total.wire().Issued != "125000" || total.count != 125 {
								t.Fatal("same-second cursor skipped entries", total, err)
							}
						}
					})
				}
			}
		}
		rows, err := tx.QueryContext(f.ctx, "EXPLAIN QUERY PLAN "+edgeCountQuery, start+1, start+3599, start, start+3600, c.last)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var plan []string
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
				t.Fatal(err)
			}
			plan = append(plan, detail)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		if joined := strings.Join(plan, "\n"); !strings.Contains(joined, "COVERING INDEX idx_credit_operations_created") || strings.Contains(joined, "TEMP B-TREE") {
			t.Fatal("edge selection loads operation records", joined)
		}
	})
}

func TestSummaryStocksExcludeLaterFlowsAndKeepUnfilteredInterval(t *testing.T) {
	f := newAuditFixture(t)
	start := bucketStart(auditNow-7200, 330, 3600)
	const large int64 = 9_007_199_254_740_993
	f.tx(t, func(tx *sql.Tx) {
		for _, entry := range []struct{ at, amount int64 }{{start - 1, 1000}, {start + 10, 9_000_000_000_000_000}, {start + 11, large - 9_000_000_000_000_000}, {start + 200, -2000}, {start + 3600, 7000}, {maxUnix, 11000}} {
			plan, err := ledger.NewAdminUserAdjustment(f.meta(t, entry.at), f.wallets[ledger.General], f.external[ledger.General], ledger.AmountFromMilli(entry.amount), 0, ledger.Amount{}, "interval funding")
			applyAuditPlan(t, tx, plan, err)
		}
	})
	f.project(t)
	for _, kind := range []string{"", "welfare_claim"} {
		filter := Filter{Asset: ledger.General, From: start, To: start + 300, Bucket: "hour", Kind: kind}
		got, err := f.service.Summary(f.ctx, f.admin, filter)
		if err != nil {
			t.Fatal(err)
		}
		closing := new(big.Int).Sub(big.NewInt(large), big.NewInt(1000)).String()
		delta := new(big.Int).Sub(big.NewInt(large), big.NewInt(2000)).String()
		if got.Reconciliation.Status != "matched" || *got.Reconciliation.IntervalOpeningNet != "1000" || *got.Reconciliation.IntervalClosingNet != closing || *got.Reconciliation.IntervalNetChange != delta {
			t.Fatalf("later or filtered flows changed stock: %+v", got)
		}
		if kind == "" && got.Flows.Issued != fmt.Sprint(large) || kind != "" && got.Flows.Operations != "0" {
			t.Fatal("selected flows lost precision or filtering", got)
		}
	}
	filter := Filter{Asset: ledger.General, From: 0, To: maxUnix, Bucket: "day"}
	got, err := f.service.Summary(f.ctx, f.admin, filter)
	closing := new(big.Int).Add(big.NewInt(large), big.NewInt(6000)).String()
	if err != nil || *got.Reconciliation.IntervalOpeningNet != "0" || *got.Reconciliation.IntervalClosingNet != closing {
		t.Fatal("epoch or final supported timestamp changed interval stock", got, err)
	}
}
