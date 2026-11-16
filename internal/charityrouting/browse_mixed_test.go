package charityrouting

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"os"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/donationquota"
	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

const mixedWriterClaims = 32

// This opt-in fixed workload uses the real reader/writer pools and quota claim
// transitions. It measures service calls, not HTTP transport or upstream work.
func TestBrowseMixedLoad(t *testing.T) {
	if os.Getenv("NONBIRI_BROWSE_MIXED") != "1" {
		t.Skip("set NONBIRI_BROWSE_MIXED=1 for the fixed mixed workload")
	}
	for _, size := range []struct{ models, buckets int }{{100, 1000}, {327, 10000}} {
		for _, rules := range []bool{false, true} {
			t.Run(fmt.Sprintf("models=%d/rules=%t", size.models, rules), func(t *testing.T) {
				env := newBrowseProjectionFixture(t, size.models, 16)
				ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
				defer cancel()
				key, secret := seedMixedQuota(t, env, size.buckets, rules)
				now := env.clock.Load()
				// Borrow both readers together so each initializes its real schema.
				var conns []*sql.Conn
				for range 2 {
					conn, err := env.store.ReadDB().Conn(ctx)
					if err != nil {
						t.Fatal(err)
					}
					conns = append(conns, conn)
					var count int
					if err := conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM charity_models").Scan(&count); err != nil {
						t.Fatal(err)
					}
				}
				for _, conn := range conns {
					if err := conn.Close(); err != nil {
						t.Fatal(err)
					}
				}
				yes := true
				filter := CatalogFilter{CurrentlyAvailable: &yes}
				page := pagination.Request{Page: 1, Size: 50}
				catalog, err := env.service.Catalog(ctx, env.caller, filter, page)
				if err != nil {
					t.Fatal(err)
				}
				capability, err := env.service.Capability(ctx, env.caller, now)
				if err != nil {
					t.Fatal(err)
				}
				if len(capability.Models) != size.models-size.models/4 || len(catalog.Models) != 50 {
					t.Fatal("unexpected warm result")
				}
				wantCatalog, _ := json.Marshal(catalog)
				wantCapability, _ := json.Marshal(capability)
				type sample struct {
					kind    string
					elapsed time.Duration
					err     error
				}
				results := make(chan sample, 64)
				start := make(chan struct{})
				var ready, done sync.WaitGroup
				var writerHold atomic.Int64
				ready.Add(6)
				done.Add(6)
				for worker := 0; worker < 4; worker++ {
					go func(worker int) {
						defer done.Done()
						ready.Done()
						<-start
						for range 8 {
							began := time.Now()
							kind := "catalog"
							var encoded []byte
							var err error
							if worker%2 == 0 {
								result, readErr := env.service.Catalog(ctx, env.caller, filter, page)
								err = readErr
								if err == nil {
									encoded, err = json.Marshal(result)
								}
								if err == nil && !bytes.Equal(encoded, wantCatalog) {
									err = errors.New("catalog changed during quota writes")
								}
							} else {
								kind = "capability"
								result, readErr := env.service.Capability(ctx, env.caller, now)
								err = readErr
								if err == nil {
									encoded, err = json.Marshal(result)
								}
								if err == nil && !bytes.Equal(encoded, wantCapability) {
									err = errors.New("capability changed during quota writes")
								}
							}
							results <- sample{kind, time.Since(began), err}
							if err != nil {
								cancel()
								return
							}
						}
					}(worker)
				}
				for range 2 {
					go func() {
						defer done.Done()
						ready.Done()
						<-start
						for range mixedWriterClaims / 2 {
							began := time.Now()
							err := mixedQuotaClaim(ctx, env.store.DB(), key, secret, now, &writerHold)
							results <- sample{"claim", time.Since(began), err}
							if err != nil {
								cancel()
								return
							}
						}
					}()
				}
				ready.Wait()
				beforeWriter, beforeReader := env.store.StatsByPool()
				began := time.Now()
				close(start)
				done.Wait()
				wall := time.Since(began)
				afterWriter, afterReader := env.store.StatsByPool()
				hold := time.Duration(writerHold.Load())
				close(results)
				samples := map[string][]time.Duration{}
				for result := range results {
					if result.err != nil {
						t.Fatalf("%s failed: %v", result.kind, result.err)
					}
					samples[result.kind] = append(samples[result.kind], result.elapsed)
				}
				if len(samples["catalog"]) != 16 || len(samples["capability"]) != 16 || len(samples["claim"]) != mixedWriterClaims {
					t.Fatalf("incomplete workload: %+v", samples)
				}
				checkMixedQuota(t, env, key, secret, now, rules, &writerHold)
				type latency struct {
					Count         int `json:"count"`
					P50, P95, Max float64
				}
				summary := map[string]latency{}
				for kind, values := range samples {
					sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
					summary[kind] = latency{len(values), durationMS(values[(len(values)-1)/2]), durationMS(values[(95*len(values)+99)/100-1]), durationMS(values[len(values)-1])}
				}
				result := map[string]any{
					"models": size.models, "rules": rules, "hot_buckets": size.buckets, "readers": 4, "writers": 2,
					"wall_ms": durationMS(wall), "latency_ms": summary, "writer_hold_ms": durationMS(hold),
					"writer_waits": afterWriter.WaitCount - beforeWriter.WaitCount, "reader_waits": afterReader.WaitCount - beforeReader.WaitCount,
					"writer_wait_ms": durationMS(afterWriter.WaitDuration - beforeWriter.WaitDuration),
					"reader_wait_ms": durationMS(afterReader.WaitDuration - beforeReader.WaitDuration),
				}
				encoded, err := json.Marshal(result)
				if err != nil {
					t.Fatal(err)
				}
				t.Logf("mixed=%s", encoded)
			})
		}
	}
}

func durationMS(value time.Duration) float64 { return float64(value) / float64(time.Millisecond) }

func seedMixedQuota(t *testing.T, env *routingTestEnv, hotBuckets int, enabled bool) (int64, int64) {
	t.Helper()
	rows, err := env.store.DB().Query(`SELECT dk.id,g.secret_ref_id FROM donation_keys dk JOIN endpoint_key_current_credentials g ON g.id=dk.endpoint_key_id ORDER BY dk.id`)
	if err != nil {
		t.Fatal(err)
	}
	type source struct{ key, secret int64 }
	var sources []source
	for rows.Next() {
		var s source
		if err := rows.Scan(&s.key, &s.secret); err != nil {
			t.Fatal(err)
		}
		sources = append(sources, s)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	if len(sources) != 16 {
		t.Fatalf("sources=%d", len(sources))
	}
	if !enabled {
		return sources[0].key, sources[0].secret
	}
	tx, err := env.store.DB().BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	now := env.clock.Load()
	total := 0
	for index, s := range sources {
		count := 16
		if index == 0 {
			count = hotBuckets
		}
		total += count
		rule := donationquota.RuleInput{Mode: "sliding", Interval: "day", TimeZone: "UTC", Metric: "tokens", Limit: fmt.Sprint(count + mixedWriterClaims*10)}
		if err := donationquota.Replace(t.Context(), tx, s.key, now-86400, []donationquota.RuleInput{rule}); err != nil {
			t.Fatal(err)
		}
		var id string
		if err := tx.QueryRow(`SELECT id FROM donation_quota_rules WHERE donation_key_id=? AND current_epoch IS NOT NULL`, s.key).Scan(&id); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(`WITH RECURSIVE samples(n) AS (VALUES(1) UNION ALL SELECT n+1 FROM samples WHERE n<?)
INSERT INTO donation_quota_buckets(rule_id,epoch,success_at,used_mag,reserved_mag) SELECT ?,1,?+n,nbi_u128(1),zeroblob(16) FROM samples`, count, id, now-int64(count)-120); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(`UPDATE donation_quota_epochs SET last_observed_at=?,window_left=?,window_at=?,window_used=nbi_u128(?),window_reserved=zeroblob(16) WHERE rule_id=?`, now-60, now-60-86400, now-60, count, id); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := tx.Exec("UPDATE donation_quota_capacity SET rows_used=? WHERE id=1", total); err != nil {
		t.Fatal(err)
	}
	if err := donationquota.ValidateState(t.Context(), tx); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return sources[0].key, sources[0].secret
}

func mixedTransaction(ctx context.Context, q *sql.DB, hold *atomic.Int64, fn func(*sql.Tx) error) error {
	tx, err := q.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	started := time.Now()
	defer func() { _ = tx.Rollback(); hold.Add(int64(time.Since(started))) }()
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

func mixedQuotaClaim(ctx context.Context, q *sql.DB, key, secret, now int64, hold *atomic.Int64) error {
	id, err := db.GenerateOpaqueID("clm_")
	if err != nil {
		return err
	}
	request, err := db.GenerateOpaqueID("req_")
	if err != nil {
		return err
	}
	if err := mixedTransaction(ctx, q, hold, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO logical_requests(id,route_kind,state,attempt_limit,accounting_state,settlement_destination,ledger_rows_remaining,created_at) VALUES(?,'charity_chat_completions','accepted',1,'none','user',zeroblob(16),?)`, request, now); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO dispatch_claims(id,logical_request_id,attempt_seq,purpose,secret_ref_id,donation_key_id,claim_now,state,reserved_calls,reserved_tokens,reserved_price_milli,donor_reward_state) VALUES(?,?,1,'charity',?,?,?,'claimed',1,10,1,'pending')`, id, request, secret, key, now); err != nil {
			return err
		}
		return donationquota.Reserve(ctx, tx, id, now)
	}); err != nil {
		return err
	}
	if err := mixedTransaction(ctx, q, hold, func(tx *sql.Tx) error {
		if err := donationquota.Reconcile(ctx, tx, id, now); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, "UPDATE dispatch_claims SET state='dispatched',dispatched_at=? WHERE id=?", now, id)
		return err
	}); err != nil {
		return err
	}
	if err := mixedTransaction(ctx, q, hold, func(tx *sql.Tx) error {
		at, err := donationquota.Start(ctx, tx, id, now)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, "INSERT INTO dispatch_response_starts(claim_id,started_at) VALUES(?,?)", id, at)
		return err
	}); err != nil {
		return err
	}
	return mixedTransaction(ctx, q, hold, func(tx *sql.Tx) error {
		amount := func(value int64) db.U128 { result, _ := db.U128FromBig(big.NewInt(value)); return result }
		actual := donationquota.Amounts{Calls: amount(1), Tokens: amount(10), Credits: amount(1)}
		if err := donationquota.Settle(ctx, tx, id, now, actual, true); err != nil {
			return err
		}
		if err := donationquota.Settle(ctx, tx, id, now, actual, true); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE dispatch_claims SET state='committed',secret_ref_id=NULL,donor_reward_actual_milli=0,donor_reward_state='zero',terminal_at=?,streak_disposition='success',failure_origin='none' WHERE id=?`, now, id); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE logical_requests SET state='terminal',terminal_at=?,caller_result_class='success',caller_status=200 WHERE id=?`, now, request)
		return err
	})
}

func checkMixedQuota(t *testing.T, env *routingTestEnv, key, secret, now int64, rules bool, hold *atomic.Int64) {
	t.Helper()
	var claims, committed, starts, pending int
	err := env.store.DB().QueryRow(`SELECT COUNT(*),SUM(state='committed'),(SELECT COUNT(*) FROM dispatch_response_starts),(SELECT COUNT(*) FROM donation_quota_receipts WHERE state<>'settled') FROM dispatch_claims`).Scan(&claims, &committed, &starts, &pending)
	if err != nil || claims != mixedWriterClaims || committed != claims || starts != claims || pending != 0 {
		t.Fatalf("claims=%d committed=%d starts=%d pending=%d err=%v", claims, committed, starts, pending, err)
	}
	value, _ := db.U128FromBig(big.NewInt(10))
	available, err := donationquota.Available(t.Context(), env.store.ReadDB(), key, now, donationquota.Amounts{Tokens: value})
	if err != nil || available == rules {
		t.Fatalf("quota available=%v rules=%v err=%v", available, rules, err)
	}
	if rules {
		if err := mixedQuotaClaim(t.Context(), env.store.DB(), key, secret, now, hold); !errors.Is(err, donationquota.ErrLimited) {
			t.Fatalf("limit rejection=%v", err)
		}
		var count int
		if err := env.store.DB().QueryRow("SELECT COUNT(*) FROM dispatch_claims").Scan(&count); err != nil || count != claims {
			t.Fatalf("rejected claim remained: %d %v", count, err)
		}
	}
	tx, err := env.store.DB().BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := donationquota.ValidateState(t.Context(), tx); err != nil {
		t.Fatal(err)
	}
}
