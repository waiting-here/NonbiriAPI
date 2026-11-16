package imageactivity

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/observability"
)

// Retain the unconditional transaction as a paired performance reference.
func expireQueuedUnconditionally(ctx context.Context, s *Service, now int64) error {
	tx, err := s.config.Database.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	ids, err := s.cancelQueuedTx(ctx, tx, "id IN (SELECT id FROM image_activity_tasks WHERE state='queued' AND queue_deadline<=? ORDER BY queue_deadline,accepted_seq LIMIT 100)", []any{now}, "queue_timeout", now)
	if err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	for _, id := range ids {
		s.memory.purge(id)
	}
	return nil
}

func TestIdleQueueExpiryScale(t *testing.T) {
	if os.Getenv("NONBIRI_QUERY_SCALE") != "1" {
		t.Skip("opt-in full-schema idle worker measurement")
	}
	f := newFixture(t)
	f.configure(t)
	previous := 0
	for _, size := range []int{1_000, 10_000} {
		seedTaskQueryRows(t, f, previous+1, size)
		previous = size
		for _, mixed := range []bool{false, true} {
			for _, candidate := range []bool{false, true} {
				t.Run(fmt.Sprintf("history_%d/mixed_%v/probe_%v", size, mixed, candidate), func(t *testing.T) {
					performance := observability.NewPerformance()
					f.store.SetConnectionObserver(performance)
					defer f.store.SetConnectionObserver(nil)
					ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
					defer cancel()
					writerBefore, readerBefore := f.database.Stats(), f.service.config.ReadDatabase.Stats()
					started := time.Now()
					errors := make(chan error, 3)
					var workers sync.WaitGroup
					workers.Add(1)
					go func() {
						defer workers.Done()
						for range 100 {
							var err error
							if candidate {
								err = f.service.expireQueued(ctx, testNow)
							} else {
								err = expireQueuedUnconditionally(ctx, f.service, testNow)
							}
							if err != nil {
								errors <- err
								return
							}
						}
					}()
					if mixed {
						workers.Add(2)
						go func() {
							defer workers.Done()
							for range 100 {
								if _, err := f.database.ExecContext(ctx, "UPDATE site_config SET updated_at=updated_at+1 WHERE key='site_name'"); err != nil {
									errors <- err
									return
								}
							}
						}()
						go func() {
							defer workers.Done()
							for range 100 {
								var value string
								if err := f.service.config.ReadDatabase.QueryRowContext(ctx, "SELECT value FROM site_config WHERE key='site_name'").Scan(&value); err != nil {
									errors <- err
									return
								}
							}
						}()
					}
					workers.Wait()
					close(errors)
					for err := range errors {
						t.Fatal(err)
					}
					elapsed := time.Since(started)
					writerAfter, readerAfter := f.database.Stats(), f.service.config.ReadDatabase.Stats()
					var transactions, holdMS int64
					for _, row := range performance.Drain() {
						if row.Route == "occupancy.transaction.unclassified" {
							transactions += row.Completed
						}
						if row.Route == "occupancy.hold.unclassified" {
							holdMS += row.ActiveMS
						}
					}
					t.Logf("history=%d mixed=%v probe=%v loops=100 elapsed=%s writer_transactions=%d writer_hold_ms=%d writer_wait=%s reader_wait=%s", size, mixed, candidate, elapsed, transactions, holdMS, writerAfter.WaitDuration-writerBefore.WaitDuration, readerAfter.WaitDuration-readerBefore.WaitDuration)
					if candidate && transactions != 0 || !candidate && transactions != 100 {
						t.Fatalf("unexpected expiry transactions: %d", transactions)
					}
				})
			}
		}
	}
}
