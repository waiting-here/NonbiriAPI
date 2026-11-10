package economyaudit

import (
	"context"
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
)

// Retain the former query as a result and workload comparison, not as a
// fallback: its sequence scan grows with unrelated history for sparse filters.
func sequenceScanPageQuery(f Filter, head, before int64) (string, []any) {
	query := `SELECT o.id,o.ledger_seq,o.kind,o.source_type,o.source_id,o.created_at
FROM credit_operations o WHERE o.ledger_seq<=? AND +o.created_at>=? AND +o.created_at<?
AND EXISTS(SELECT 1 FROM credit_entries e WHERE e.operation_id=o.id AND e.asset_type=?)`
	args := []any{head, f.From, f.To, string(f.Asset)}
	if before > 0 {
		query += ` AND o.ledger_seq<?`
		args = append(args, before)
	}
	if f.Kind != "" {
		query += ` AND +o.kind=?`
		args = append(args, f.Kind)
	}
	query += operationChannelCondition(f, "o.source_id")
	return query + ` ORDER BY o.ledger_seq DESC LIMIT 101`, args
}

func seedAuditQueryHistory(t *testing.T, f *auditFixture, count int) {
	t.Helper()
	f.tx(t, func(tx *sql.Tx) {
		ops, err := tx.Prepare(`INSERT INTO credit_operations(id,ledger_seq,kind,source_type,source_id,source_seq,donation_credit_delta_sign,donation_credit_delta_mag,created_at)
VALUES(?,?,?,'operation',?,zeroblob(16),0,zeroblob(16),?)`)
		if err != nil {
			t.Fatal(err)
		}
		defer ops.Close()
		entries, err := tx.Prepare(`INSERT INTO credit_entries(operation_id,line_no,account_id,account_kind_snapshot,asset_type,delta_sign,delta_mag,balance_after_sign,balance_after_mag)
VALUES(?,?,?,'user',?,1,?,NULL,NULL),(?,?,?,'external',?,-1,?,-1,?)`)
		if err != nil {
			t.Fatal(err)
		}
		defer entries.Close()
		counts := map[ledger.Asset]int64{}
		var magnitude [16]byte
		binary.BigEndian.PutUint64(magnitude[8:], 1000)
		for i := 1; i <= count; i++ {
			id := fmt.Sprintf("op_%021dA", i)
			assets, kind := []ledger.Asset{ledger.General}, "admin_user_adjustment"
			source := id
			if i%5 == 0 {
				kind, source = "duel_terminal", "bid_"+id
			}
			if i%4999 == 0 {
				kind, source = "duel_terminal", "lik_"+id
			}
			if i%1000 == 0 {
				assets = []ledger.Asset{ledger.Game}
			}
			if i%997 == 0 {
				assets = []ledger.Asset{ledger.General, ledger.SketchPaper}
				kind, source = "activity_exchange", id
			}
			if _, err := ops.Exec(id, i, kind, source, auditNow-int64(count)+int64(i)-1); err != nil {
				t.Fatal(err)
			}
			for line, asset := range assets {
				counts[asset]++
				var balance [16]byte
				binary.BigEndian.PutUint64(balance[8:], uint64(counts[asset]*1000))
				if _, err := entries.Exec(id, 2*line, f.wallets[asset], string(asset), magnitude[:], id, 2*line+1, f.external[asset], string(asset), magnitude[:], balance[:]); err != nil {
					t.Fatal(err)
				}
			}
		}
		for asset, count := range counts {
			var balance [16]byte
			binary.BigEndian.PutUint64(balance[8:], uint64(count*1000))
			for _, account := range []struct{ id, sign int64 }{{f.wallets[asset], 1}, {f.external[asset], -1}} {
				if _, err := tx.Exec(`UPDATE credit_accounts SET balance_sign=?,balance_mag=? WHERE id=?`, account.sign, balance[:], account.id); err != nil {
					t.Fatal(err)
				}
			}
		}
		if _, err := tx.Exec(`UPDATE credit_capacity SET last_ledger_seq=? WHERE id=1`, count); err != nil {
			t.Fatal(err)
		}
	})
}

func auditQueryPlan(t *testing.T, database *sql.DB, query string, args ...any) string {
	t.Helper()
	rows, err := database.Query("EXPLAIN QUERY PLAN "+query, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var lines []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		lines = append(lines, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return strings.Join(lines, "; ")
}

func sampleAuditSeries(t *testing.T, f *auditFixture) {
	t.Helper()
	start := time.Now()
	f.project(t)
	t.Logf("prepare aggregate projection elapsed=%s", time.Since(start))
	for _, shape := range []struct {
		bucket string
		step   int64
		points int
	}{{"hour", 3600, 744}, {"day", 86400, 366}} {
		end := bucketStart(auditNow, 330, shape.step) + shape.step
		filter := Filter{Asset: ledger.General, From: end - int64(shape.points)*shape.step, To: end, Bucket: shape.bucket}
		var expected []Point
		for _, mode := range []string{"per-bucket", "batched"} {
			start := time.Now()
			var got []Point
			err := f.service.read(f.ctx, f.admin, filter, func(ctx context.Context, tx *sql.Tx, c checkpoint, _ int64) error {
				if mode == "batched" {
					var err error
					got, err = seriesMeasures(ctx, tx, c, filter, filter.From, shape.step)
					return err
				}
				for at := filter.From; at < filter.To; at += shape.step {
					part := filter
					part.From, part.To = at, at+shape.step
					values, err := rangeMeasures(ctx, tx, c, part)
					if err != nil {
						return err
					}
					m, err := sumDimensions(values)
					if err != nil {
						return err
					}
					got = append(got, Point{part.From, part.To, m.wire()})
				}
				return nil
			})
			t.Logf("series bucket=%s points=%d mode=%s elapsed=%s error=%v", shape.bucket, shape.points, mode, time.Since(start), err)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "per-bucket" {
				expected = got
			} else if !reflect.DeepEqual(got, expected) {
				t.Fatal("batched series changed metrics")
			}
		}
	}
	if summary, err := f.service.Summary(f.ctx, f.admin, auditFilter(ledger.General)); err != nil || summary.Reconciliation.Status != "matched" {
		t.Fatal("synthetic history did not conserve exact stocks", summary, err)
	}
}

func sampleAuditProjectionWriteCost(t *testing.T, f *auditFixture) {
	t.Helper()
	f.fund(t, 1, auditNow-200)
	start := time.Now()
	f.fund(t, 100, auditNow-200)
	t.Logf("normal ledger writes=100 projection=on elapsed=%s", time.Since(start))
	// Only this private disposable fixture loses the derived search table.
	// Rebuild it after measuring the same normal ledger path without its cost.
	f.tx(t, func(tx *sql.Tx) {
		if _, err := tx.Exec(`DROP TRIGGER credit_operation_assets_insert; DROP TRIGGER credit_operation_assets_delete; DROP TABLE credit_operation_assets`); err != nil {
			t.Fatal(err)
		}
	})
	f.fund(t, 1, auditNow-200)
	start = time.Now()
	f.fund(t, 100, auditNow-200)
	t.Logf("normal ledger writes=100 projection=off elapsed=%s", time.Since(start))
	start = time.Now()
	f.tx(t, func(tx *sql.Tx) {
		if _, err := tx.Exec(db.CreditOperationAssetsSQL); err != nil {
			t.Fatal(err)
		}
	})
	t.Logf("projection rebuild including DDL/backfill/commit elapsed=%s", time.Since(start))
	var missing int
	if err := f.database.QueryRow(`SELECT count(*) FROM (SELECT operation_id,asset_type FROM credit_entries GROUP BY operation_id,asset_type EXCEPT SELECT operation_id,asset_type FROM credit_operation_assets)`).Scan(&missing); err != nil || missing != 0 {
		t.Fatal("projection rebuild missed retained assets", missing, err)
	}
}

func TestAuditQueryScaleSample(t *testing.T) {
	if os.Getenv("NONBIRI_AUDIT_QUERY_SAMPLE") != "1" {
		t.Skip("opt-in paired query workload")
	}
	for _, count := range []int{10000, 100000} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			f := newAuditFixture(t)
			start := time.Now()
			seedAuditQueryHistory(t, f, count)
			t.Logf("setup operations=%d elapsed=%s", count, time.Since(start))
			var sqlite string
			if err := f.database.QueryRow(`SELECT sqlite_version()`).Scan(&sqlite); err != nil {
				t.Fatal(err)
			}
			var projectionBytes int64
			if err := f.database.QueryRow(`SELECT coalesce(sum(pgsize),0) FROM dbstat WHERE name='credit_operation_assets' OR name LIKE 'idx_credit_operation_assets_%'`).Scan(&projectionBytes); err != nil {
				t.Fatal(err)
			}
			t.Logf("sqlite=%s projection_bytes=%d", sqlite, projectionBytes)
			base := auditFilter(ledger.General)
			old := base
			old.From, old.To = auditNow-int64(count), auditNow-int64(count)+50
			empty := old
			empty.From, empty.To = old.From-100, old.From
			rare := base
			rare.Kind = "activity_exchange"
			missingKind := base
			missingKind.Kind = "welfare_claim"
			channel := base
			channel.Kind, channel.Channel = "duel_terminal", "gwent"
			rareChannel := channel
			rareChannel.Channel = "likes"
			for _, scenario := range []struct {
				name   string
				filter Filter
				before int64
			}{
				{"broad", base, 0}, {"deep-page", base, int64(count / 2)}, {"old-time", old, 0}, {"empty-time", empty, 0},
				{"rare-asset", auditFilter(ledger.Game), 0}, {"missing-asset", auditFilter(ledger.SketchBrush), 0},
				{"cross-asset", auditFilter(ledger.SketchPaper), 0}, {"rare-kind", rare, 0}, {"missing-kind", missingKind, 0},
				{"rare-channel", rareChannel, 0}, {"missing-channel", channel, 0},
			} {
				t.Run(scenario.name, func(t *testing.T) {
					var expected []operation
					var expectedEntries map[string][]OperationEntry
					for _, mode := range []string{"sequence-first", "range-first", "range-warm", "sequence-warm"} {
						start := time.Now()
						var got []operation
						var entries map[string][]OperationEntry
						err := f.service.read(f.ctx, f.admin, scenario.filter, func(ctx context.Context, tx *sql.Tx, c checkpoint, _ int64) error {
							var err error
							if strings.HasPrefix(mode, "sequence") {
								query, args := sequenceScanPageQuery(scenario.filter, c.head, scenario.before)
								got, err = queryOperations(ctx, tx, query, args...)
							} else {
								got, err = readOperationPage(ctx, tx, scenario.filter, c.head, scenario.before)
							}
							if err != nil {
								return err
							}
							ids := make([]string, 0, len(got))
							for _, o := range got {
								ids = append(ids, o.id)
							}
							entries, err = readOperationEntriesBatch(ctx, tx, ids)
							return err
						})
						t.Logf("mode=%s elapsed=%s rows=%d error=%v", mode, time.Since(start), len(got), err)
						if err != nil {
							t.Fatal(err)
						}
						if mode == "sequence-first" {
							expected, expectedEntries = got, entries
						} else if !reflect.DeepEqual(got, expected) || !reflect.DeepEqual(entries, expectedEntries) {
							t.Fatal("page or exact entries changed")
						}
					}
					query, args := operationRangePageQuery(scenario.filter, int64(count), 101)
					t.Log("range plan:", auditQueryPlan(t, f.database, query, args...))
				})
			}
			query, args := operationProbeQuery(base, int64(count))
			t.Log("probe plan:", auditQueryPlan(t, f.database, query, args...))
			if count == 100000 {
				// Force the historical fallback over half the retained operations.
				// Its selection must finish while QueryContext still observes the
				// deadline, and must release the reader for subsequent requests.
				slow := base
				slow.To = auditNow - int64(count/2)
				ctx, cancel := context.WithTimeout(f.ctx, 10*time.Millisecond)
				start := time.Now()
				_, err := f.service.Operations(ctx, f.admin, slow)
				cancel()
				t.Logf("cancel historical selection elapsed=%s err=%v", time.Since(start), err)
				if !errors.Is(err, context.DeadlineExceeded) {
					t.Fatal("expected selection deadline", err)
				}
				if got, err := f.service.Operations(f.ctx, f.admin, base); err != nil || len(got.Data) != 100 {
					t.Fatal("reader unavailable after interrupted selection", got, err)
				}
			}
			sampleAuditSeries(t, f)
			sampleAuditProjectionWriteCost(t, f)
		})
	}
}
