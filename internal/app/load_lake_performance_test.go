package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/accountstream"
	"github.com/waiting-here/NonbiriAPI/internal/activities"
	"github.com/waiting-here/NonbiriAPI/internal/auth"
	"github.com/waiting-here/NonbiriAPI/internal/lakenotes"
	lakeconfig "github.com/waiting-here/NonbiriAPI/internal/lakenotes/config"
	"github.com/waiting-here/NonbiriAPI/internal/lakenotes/rules"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
)

type loadLakeRandom struct{}

func (loadLakeRandom) Float53() (float64, error) { return .5, nil }

// TestApplicationLakeMixedLoad uses real HTTP checkpoint handlers and rules.
// Per-user queues preserve controller ordering while all arrival deadlines stay
// fixed. Queue delay is reported rather than silently lowering offered load.
func TestApplicationLakeMixedLoad(t *testing.T) {
	if os.Getenv("NONBIRI_LOAD_TEST") != "1" {
		t.Skip("set NONBIRI_LOAD_TEST=1 to run the synthetic load experiment")
	}
	t.Setenv("NONBIRI_LOAD_SCALE", "production")
	seconds := 30
	if raw := os.Getenv("NONBIRI_LOAD_SECONDS"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 6 || value > 60 || value%2 != 0 {
			t.Fatal("NONBIRI_LOAD_SECONDS must be an even integer from 6 to 60")
		}
		seconds = value
	}
	for _, tc := range []struct{ users, rps, burst, basket, subscribers int }{
		{4, 15, 25, 0, 4}, {8, 30, 50, 20, 8}, {16, 30, 50, 40, 16}, {32, 30, 50, 70, 32},
	} {
		t.Run(fmt.Sprintf("users_%d_basket_%d", tc.users, tc.basket), func(t *testing.T) {
			f := newLoadFixture(t, &loadUpstream{release: make(chan struct{}), mode: "short"}, func(options *applicationRuntimeOptions) {
				options.LakeRandom = loadLakeRandom{}
				options.LakeMotionSeed = func() (uint32, error) { return 123456789, nil }
			})
			ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
			defer cancel()
			if _, err := f.store.DB().ExecContext(ctx, "UPDATE site_config SET value='1' WHERE key=?", lakeconfig.EnabledKey); err != nil {
				t.Fatal(err)
			}
			states := make([]lakenotes.CastResult, tc.users)
			for i := range states {
				loadSeedLakeProfile(t, f, f.users[i], tc.basket)
				sample := f.lakeCall(ctx, f.users[i], "/api/games/lake-notes/casts", fmt.Sprintf("load-start-%022d", i), lakenotes.StartInput{ExpectedProfileRevision: "1"}, &states[i])
				if sample.status != 200 || sample.err != "" {
					t.Fatalf("start: %+v", sample)
				}
			}
			streamCtx, stopStreams := context.WithCancel(f.app.performance.Context(ctx))
			defer stopStreams()
			var consumers sync.WaitGroup
			var frames atomic.Int64
			for i := range tc.subscribers {
				sub, err := f.app.activityEvents.Subscribe(streamCtx, accountstream.SubscribeRequest{AccountID: f.users[i].id, Channels: []accountstream.Channel{accountstream.ChannelActivities}})
				if err != nil {
					t.Fatal(err)
				}
				consumers.Go(func() {
					defer sub.Close()
					for {
						if _, err := sub.Next(streamCtx); err != nil {
							return
						}
						frames.Add(1)
					}
				})
			}
			publisher, err := activities.NewAccountstreamPublisher(f.app.activityRepo, f.app.activityEvents)
			if err != nil {
				t.Fatal(err)
			}
			routes := []string{"/v1/models", "/api/charity/models", "/api/charity/models?view=catalog&page=1&page_size=50", "/api/session", "/api/me", "/api/games/blackjack/state"}
			pages, lake, lag, projections, models := &loadMeasurements{}, &loadMeasurements{}, &loadMeasurements{}, &loadMeasurements{}, &loadMeasurements{}
			var submitted, checkpoints atomic.Int64
			var work, players sync.WaitGroup
			queues := make([]chan time.Time, tc.users)
			runtime.GC()
			f.app.performance.Drain()
			before := loadReadMemory()
			writerBefore, readerBefore := f.store.StatsByPool()
			var changesBefore, changesAfter int64
			if err := f.store.DB().QueryRowContext(ctx, "SELECT total_changes()").Scan(&changesBefore); err != nil {
				t.Fatal(err)
			}
			start := time.Now()
			for i := range queues {
				queues[i] = make(chan time.Time, seconds/2)
				players.Go(func() {
					current := states[i]
					ordinal := 0
					for due := range queues[i] {
						ordinal++
						lag.add(loadSample{route: "lake_queue", duration: time.Since(due)})
						fresh := current.Cast.State.Terminal()
						if fresh {
							s := f.lakeCall(ctx, f.users[i], "/api/games/lake-notes/casts", fmt.Sprintf("load-recast-%08d-%012d", i, ordinal), lakenotes.StartInput{ExpectedProfileRevision: current.Profile.Revision}, &current)
							s.route = "start"
							lake.add(s)
							if s.status != 200 || s.err != "" {
								continue
							}
						}
						from := current.Cast.AckTick + 1
						input := lakenotes.CheckpointInput{ControlInput: lakenotes.ControlInput{Generation: current.Cast.Generation, ExpectedRevision: current.Cast.Revision}, FromTick: from, ToTick: from + 119, InitialHeld: current.Cast.State.Held, Edges: []lakenotes.Edge{}}
						// A recast has just started; its first control edge must not
						// consume two seconds of simulation before wall time passes.
						if fresh {
							input.ToTick = from
						}
						held := input.InitialHeld
						for offset := uint64(0); offset < 120; offset += 30 {
							if next := (offset/30)%2 == 0; from+offset <= input.ToTick && next != held {
								input.Edges = append(input.Edges, lakenotes.Edge{Tick: from + offset, Held: next})
								held = next
							}
						}
						checkpoints.Add(1)
						s := f.lakeCall(ctx, f.users[i], "/api/games/lake-notes/casts/"+current.Cast.ID+"/checkpoint", fmt.Sprintf("load-check-%08d-%012d", i, ordinal), input, &current)
						s.route = "checkpoint"
						if s.status == 200 && s.err == "" && (current.Cast.AckTick < from || current.Cast.AckTick > input.ToTick || !current.Cast.State.Terminal() && current.Cast.AckTick != input.ToTick) {
							s.err = "ack_tick"
						}
						lake.add(s)
					}
					states[i] = current
				})
			}
			// One event queue defines all arrivals; handlers run independently.
			for tick := 0; tick <= seconds*tc.rps; tick++ {
				due := start.Add(time.Duration(tick) * time.Second / time.Duration(tc.rps))
				if delay := time.Until(due); delay > 0 {
					time.Sleep(delay)
				}
				if tick > 0 && tick%(2*tc.rps) == 0 {
					for _, queue := range queues {
						queue <- due
					}
					work.Go(func() {
						began := time.Now()
						err := publisher.Publish(streamCtx, activities.PublishFacts{Global: true})
						s := loadSample{route: "publish", duration: time.Since(began), status: 200}
						if err != nil {
							s.err = "projection"
						}
						projections.add(s)
					})
				}
				count := 1
				if tick == seconds*tc.rps {
					count = tc.burst
				}
				for b := range count {
					index := tick + b
					path := routes[index%len(routes)]
					if count != 1 {
						path = routes[b%3]
					}
					work.Go(func() {
						submitted.Add(1)
						lag.add(loadSample{route: "page_submission", duration: time.Since(due)})
						pages.add(f.get(ctx, path, f.users[index%len(f.users)]))
					})
				}
				if tick == seconds*tc.rps/2 {
					for i := range tc.burst {
						work.Go(func() { f.model(ctx, i, "short", models) })
					}
				}
			}
			for _, queue := range queues {
				close(queue)
			}
			players.Wait()
			work.Wait()
			for f.inbound.Load() != 0 {
				select {
				case <-ctx.Done():
					t.Fatal("model handlers did not drain")
				case <-time.After(10 * time.Millisecond):
				}
			}
			writerAfter, readerAfter := f.store.StatsByPool()
			after := loadReadMemory()
			elapsed := time.Since(start).Seconds()
			operations := f.app.performance.Drain()
			if err := f.store.DB().QueryRowContext(ctx, "SELECT total_changes()").Scan(&changesAfter); err != nil {
				t.Fatal(err)
			}
			for _, samples := range []*loadMeasurements{pages, lake, projections} {
				for route, summary := range samples.summaries() {
					if len(summary.Errors) > 0 || summary.Status[200] != summary.Count {
						t.Errorf("operation failure %s: %+v", route, summary)
					}
				}
			}
			for i, current := range states {
				var stored []byte
				var revision int64
				if err := f.store.DB().QueryRowContext(ctx, "SELECT revision,profile FROM lake_notes_profiles WHERE user_id=?", f.users[i].id).Scan(&revision, &stored); err != nil {
					t.Fatal(err)
				}
				expected, err := rules.EncodeProfile(current.Profile.Profile)
				if err != nil || !bytes.Equal(stored, expected) || strconv.FormatInt(revision, 10) != current.Profile.Revision {
					t.Error("final profile did not converge")
				}
			}
			accepted, rejected, completed := 0, 0, 0
			for route, summary := range models.summaries() {
				if !strings.HasPrefix(route, "complete ") {
					continue
				}
				completed += summary.Count
				accepted += summary.Status[200]
				rejected += summary.Status[429] + summary.Status[503]
				for kind := range summary.Errors {
					if kind != "http_429" && kind != "http_503" {
						t.Errorf("unexpected model error: %s %+v", route, summary)
					}
				}
			}
			var outstanding int
			if err := f.store.DB().QueryRowContext(ctx, `SELECT
			 (SELECT count(*) FROM dispatch_claims WHERE state IN ('claimed','dispatched')) +
			 (SELECT count(*) FROM logical_requests WHERE state<>'terminal') +
			 (SELECT count(*) FROM donation_usage_reservations WHERE state='reserved')`).Scan(&outstanding); err != nil || outstanding != 0 {
				t.Fatalf("model state did not converge: %d %v", outstanding, err)
			}
			tx, err := f.store.DB().BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			err = ledger.ValidateRecovery(ctx, tx)
			_ = tx.Rollback()
			if err != nil {
				t.Fatal(err)
			}
			stopStreams()
			consumers.Wait()
			report := map[string]any{
				"users": tc.users, "basket": tc.basket, "subscribers": tc.subscribers, "frames": frames.Load(), "seed": 123456789, "random_sample": .5,
				"rps": tc.rps, "directory_concurrency": tc.burst, "seconds": seconds, "planned_pages": seconds*tc.rps + tc.burst, "submitted_pages": submitted.Load(), "planned_checkpoints": seconds / 2 * tc.users, "submitted_checkpoints": checkpoints.Load(),
				"pages": pages.summaries(), "lake": lake.summaries(), "projections": projections.summaries(), "models": models.summaries(), "schedule": lag.summaries(),
				"planned_models": tc.burst, "completed_models": completed, "accepted_models": accepted, "rejected_models": rejected, "outstanding_model_facts": outstanding,
				"writer_waits": writerAfter.WaitCount - writerBefore.WaitCount, "writer_wait_ms": float64(writerAfter.WaitDuration-writerBefore.WaitDuration) / float64(time.Millisecond), "reader_waits": readerAfter.WaitCount - readerBefore.WaitCount, "reader_wait_ms": float64(readerAfter.WaitDuration-readerBefore.WaitDuration) / float64(time.Millisecond), "writer_changes": changesAfter - changesBefore,
				"memory_before": before, "memory_after": after, "elapsed_seconds": elapsed, "last_window_operations": operations, "go": runtime.Version(), "os": runtime.GOOS, "arch": runtime.GOARCH, "gomaxprocs": runtime.GOMAXPROCS(0),
			}
			encoded, err := json.Marshal(report)
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("LAKE_LOAD_RESULT %s", encoded)
		})
	}
}

func loadSeedLakeProfile(t *testing.T, f *loadFixture, user loadIdentity, basket int) {
	t.Helper()
	p := rules.InitialProfile()
	fish, ok := rules.Fish("gold")
	if !ok {
		t.Fatal("synthetic fish missing")
	}
	for i := range basket {
		p.Basket = append(p.Basket, rules.Catch{ID: uint64(i + 1), Kind: fish.Kind, Length: fish.Length[0]})
	}
	if basket > 0 {
		p.Records[fish.Kind] = rules.Record{Caught: rules.Amount(strconv.Itoa(basket)), MaxLength: fish.Length[0], PerfectCount: "0"}
		p.NextCatchID = uint64(basket + 1)
	}
	raw, err := rules.EncodeProfile(p)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.store.DB().Exec("INSERT INTO lake_notes_profiles(user_id,revision,rules_id,storage_version,coin_mag,profile,updated_at) VALUES(?,1,?,1,zeroblob(16),?,?)", user.id, rules.RulesID, raw, time.Now().Unix())
	if err != nil {
		t.Fatal(err)
	}
}

func (f *loadFixture) lakeCall(ctx context.Context, user loadIdentity, path, key string, input any, output *lakenotes.CastResult) loadSample {
	start := time.Now()
	body, err := json.Marshal(input)
	if err != nil {
		return loadSample{err: "encode"}
	}
	r, err := http.NewRequestWithContext(ctx, "POST", f.server.URL+path, bytes.NewReader(body))
	if err != nil {
		return loadSample{err: "request"}
	}
	r.Host = auditUserHost
	r.Header.Set("Origin", "http://"+auditUserHost)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Idempotency-Key", key)
	r.AddCookie(&http.Cookie{Name: auth.UserSessionCookieName, Value: user.session})
	response, err := f.client.Do(r)
	if err != nil {
		return loadSample{duration: time.Since(start), err: "transport"}
	}
	defer response.Body.Close()
	s := loadSample{status: response.StatusCode}
	if response.StatusCode == 200 {
		var next lakenotes.CastResult
		if json.NewDecoder(response.Body).Decode(&next) != nil {
			s.err = "decode"
		} else {
			*output = next
		}
	} else {
		_, _ = io.Copy(io.Discard, response.Body)
	}
	s.duration = time.Since(start)
	return s
}
