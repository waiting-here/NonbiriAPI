package economyaudit

import (
	"context"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/game/ranking"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
	"github.com/waiting-here/NonbiriAPI/internal/observability"
	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

type projectionSampleAuthority struct{}

func (projectionSampleAuthority) AuthorizeUserMutation(ctx context.Context, tx *sql.Tx, user int64) error {
	var allowed bool
	err := tx.QueryRowContext(ctx, "SELECT is_admin=0 AND is_banned=0 FROM users WHERE id=?", user).Scan(&allowed)
	if err == nil && !allowed {
		err = fmt.Errorf("sample user unavailable")
	}
	return err
}

// The foreground path is identical in both modes so this sample isolates
// background projection contention. It is not a whole-application load test.
func TestIdleProjectionMixedLoadSample(t *testing.T) {
	if os.Getenv("NONBIRI_PERFORMANCE_SAMPLE") != "1" {
		t.Skip("opt-in fixed projection workload")
	}
	for _, workload := range []struct {
		name             string
		rounds, requests int
	}{
		{"probe-heavy", 600, 120},
		{"write-heavy", 60, 600},
	} {
		for _, count := range []int{1000, 100000} {
			for _, mode := range []string{"writer", "reader"} {
				t.Run(fmt.Sprintf("%s-history-%d-%s", workload.name, count, mode), func(t *testing.T) {
					f := newAuditFixture(t)
					if _, err := f.database.Exec("UPDATE maintenance_state SET enabled=0 WHERE id=1"); err != nil {
						t.Fatal(err)
					}
					var epoch int64
					if err := f.database.QueryRow("SELECT started_at FROM game_statistics_epoch WHERE id=1").Scan(&epoch); err != nil {
						t.Fatal(err)
					}
					now := max(epoch, auditNow)
					f.service.now = func() time.Time { return time.Unix(now, 0) }
					f.tx(t, func(tx *sql.Tx) {
						statement, err := tx.PrepareContext(t.Context(), `INSERT INTO game_rank_events(seq,user_id,game_key,source_id,settled_at,loss_sign,loss_mag,positive_profit,charity_expires_at) VALUES(?,?,'fishing',?,?,0,zeroblob(16),zeroblob(16),?)`)
						if err != nil {
							t.Fatal(err)
						}
						defer statement.Close()
						for i := 1; i <= count; i++ {
							var seq [16]byte
							binary.BigEndian.PutUint64(seq[8:], uint64(i))
							if _, err := statement.ExecContext(t.Context(), seq[:], f.user, fmt.Sprintf("history_%d", i), now, now+604800); err != nil {
								t.Fatal(err)
							}
						}
						var next [16]byte
						binary.BigEndian.PutUint64(next[8:], uint64(count+1))
						if _, err := tx.ExecContext(t.Context(), "UPDATE game_rank_counters SET next_event_seq=? WHERE id=1", next[:]); err != nil {
							t.Fatal(err)
						}
					})
					reader := f.service.readDB
					foreground, err := ranking.NewWithReader(f.database, reader, projectionSampleAuthority{}, f.service.now)
					if err != nil {
						t.Fatal(err)
					}
					for {
						ready, err := foreground.Advance(t.Context(), now)
						if err != nil {
							t.Fatal(err)
						}
						if ready {
							break
						}
					}
					if ready, err := foreground.AdvanceBidding(t.Context(), now); err != nil || !ready {
						t.Fatal("prepare completed rebuild", ready, err)
					}
					backgroundReader := reader
					if mode == "writer" {
						backgroundReader = f.database
					}
					background, err := ranking.NewWithReader(f.database, backgroundReader, projectionSampleAuthority{}, f.service.now)
					if err != nil {
						t.Fatal(err)
					}
					audit := *f.service
					audit.readDB = backgroundReader
					var serial atomic.Int64
					handler := http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
						ctx := request.Context()
						if request.Method == http.MethodGet {
							board, err := foreground.Read(ctx, f.user, "game_charity", "7d", pagination.Default())
							if err != nil {
								http.Error(w, err.Error(), http.StatusInternalServerError)
								return
							}
							_ = json.NewEncoder(w).Encode(board)
							return
						}
						err := func() error {
							tx, err := f.database.BeginTx(ctx, nil)
							if err != nil {
								return err
							}
							defer tx.Rollback()
							if err := (projectionSampleAuthority{}).AuthorizeUserMutation(ctx, tx, f.user); err != nil {
								return err
							}
							id, err := db.GenerateOpaqueID("op_")
							if err != nil {
								return err
							}
							plan, err := ledger.NewAdminUserAdjustment(ledger.Meta{OperationID: id, ActorUserID: f.user, CreatedAt: now}, f.wallets[ledger.General], f.external[ledger.General], ledger.AmountFromMilli(1000), 0, ledger.Amount{}, "funding")
							if err != nil {
								return err
							}
							if _, err := ledger.Apply(ctx, tx, plan); err != nil {
								return err
							}
							if err := ranking.RecordTx(ctx, tx, ranking.Contribution{UserID: f.user, Game: "fishing", Source: fmt.Sprintf("mixed_%d", serial.Add(1)), SettledAt: now, Loss: big.NewInt(1), PositiveProfit: new(big.Int)}); err != nil {
								return err
							}
							return tx.Commit()
						}()
						if err != nil {
							http.Error(w, err.Error(), http.StatusInternalServerError)
							return
						}
						w.WriteHeader(http.StatusNoContent)
					})
					performance := observability.NewPerformance()
					f.store.SetConnectionObserver(performance)
					server := httptest.NewServer(performance.Wrap(handler))
					defer server.Close()
					client := server.Client()
					client.Timeout = 5 * time.Second
					ctx := performance.Context(t.Context())
					// Warm both pools and code paths before measuring fixed work.
					for range 3 {
						if _, err := background.Advance(ctx, now); err != nil {
							t.Fatal(err)
						}
						if _, err := background.AdvanceBidding(ctx, now); err != nil {
							t.Fatal(err)
						}
						if _, err := audit.Advance(ctx); err != nil {
							t.Fatal(err)
						}
					}
					performance.Drain()
					if os.Getenv("NONBIRI_PREWARM_READERS") == "1" {
						beforeOpen := reader.Stats().OpenConnections
						began := time.Now()
						var connections []*sql.Conn
						for range 2 {
							conn, err := reader.Conn(ctx)
							if err != nil {
								t.Fatal(err)
							}
							connections = append(connections, conn)
							var phase int
							if err := conn.QueryRowContext(ctx, "SELECT phase FROM game_rank_net_rebuild WHERE id=1").Scan(&phase); err != nil {
								t.Fatal(err)
							}
						}
						for _, conn := range connections {
							if err := conn.Close(); err != nil {
								t.Fatal(err)
							}
						}
						t.Logf("READERS before_open=%d after_open=%d preparation=%s", beforeOpen, reader.Stats().OpenConnections, time.Since(began))
					}
					beforeWriter, beforeReader := f.database.Stats(), reader.Stats()
					var beforeMemory, afterMemory runtime.MemStats
					runtime.ReadMemStats(&beforeMemory)
					start := make(chan struct{})
					errors := make(chan error, 3)
					var wg sync.WaitGroup
					latencies := make([][]time.Duration, 2)
					for index, method := range []string{http.MethodGet, http.MethodPost} {
						wg.Go(func() {
							<-start
							for range workload.requests {
								began := time.Now()
								request, err := http.NewRequestWithContext(ctx, method, server.URL, nil)
								if err != nil {
									errors <- err
									return
								}
								response, err := client.Do(request)
								if err != nil {
									errors <- err
									return
								}
								_, readErr := io.Copy(io.Discard, response.Body)
								response.Body.Close()
								if readErr != nil || response.StatusCode >= 300 {
									errors <- fmt.Errorf("HTTP %s status=%d read=%v", method, response.StatusCode, readErr)
									return
								}
								latencies[index] = append(latencies[index], time.Since(began))
							}
						})
					}
					wg.Go(func() {
						<-start
						for range workload.rounds {
							if _, err := background.Advance(ctx, now); err != nil {
								errors <- err
								return
							}
							if _, err := background.AdvanceBidding(ctx, now); err != nil {
								errors <- err
								return
							}
							if _, err := audit.Advance(ctx); err != nil {
								errors <- err
								return
							}
						}
					})
					began := time.Now()
					close(start)
					wg.Wait()
					elapsed := time.Since(began)
					runtime.ReadMemStats(&afterMemory)
					close(errors)
					for err := range errors {
						t.Error(err)
					}
					if t.Failed() {
						return
					}
					writer, readerStats := f.database.Stats(), reader.Stats()
					var holds, holdMS int64
					for _, row := range performance.Drain() {
						if strings.HasPrefix(row.Route, "occupancy.hold.") {
							holds += row.Completed
							holdMS += row.ActiveMS
						}
					}
					for index, method := range []string{http.MethodGet, http.MethodPost} {
						slices.Sort(latencies[index])
						values := latencies[index]
						t.Logf("HTTP workload=%s history=%d mode=%s method=%s count=%d p50=%s p95=%s max=%s", workload.name, count, mode, method, len(values), values[len(values)/2], values[(len(values)*95+99)/100-1], values[len(values)-1])
					}
					t.Logf("MIX workload=%s history=%d mode=%s rounds=%d writes=%d reads=%d elapsed=%s writer_waits=%d writer_wait=%s reader_waits=%d reader_wait=%s writer_holds=%d writer_hold_ms=%d allocated_bytes=%d", workload.name, count, mode, workload.rounds, workload.requests, workload.requests, elapsed, writer.WaitCount-beforeWriter.WaitCount, writer.WaitDuration-beforeWriter.WaitDuration, readerStats.WaitCount-beforeReader.WaitCount, readerStats.WaitDuration-beforeReader.WaitDuration, holds, holdMS, afterMemory.TotalAlloc-beforeMemory.TotalAlloc)
					f.project(t)
					var ledgerSeq int64
					if err := f.database.QueryRow("SELECT last_ledger_seq FROM economy_audit_checkpoint WHERE id=1").Scan(&ledgerSeq); err != nil || ledgerSeq != int64(workload.requests) {
						t.Fatal("ledger projection", ledgerSeq, err)
					}
					var amount []byte
					if err := f.database.QueryRow("SELECT amount_mag FROM game_rank_totals WHERE user_id=? AND board='game_charity' AND window='7d'", f.user).Scan(&amount); err != nil || new(big.Int).SetBytes(amount).Int64() != int64(workload.requests) {
						t.Fatal("ranking projection", amount, err)
					}
				})
			}
		}
	}
}
