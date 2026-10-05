package duel_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/continuity"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/game/ai"
	"github.com/waiting-here/NonbiriAPI/internal/game/bidding"
	"github.com/waiting-here/NonbiriAPI/internal/game/bidding/engine"
	"github.com/waiting-here/NonbiriAPI/internal/game/bidding/strategy"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
)

type decisionTimings struct {
	mu             sync.Mutex
	queue, compute []time.Duration
}
type timedObservation struct {
	created time.Time
	value   any
}
type timedAdapter struct {
	bidding.AIAdapter
	timings *decisionTimings
}

func (a timedAdapter) Request(raw json.RawMessage, seat int) (ai.Request, error) {
	r, err := a.AIAdapter.Request(raw, seat)
	r.Observation = timedObservation{time.Now(), r.Observation}
	return r, err
}
func (a timedAdapter) Source() ai.Source { return timedSource{timings: a.timings} }

type timedSource struct {
	strategy.Source
	timings *decisionTimings
}

func (s timedSource) Decide(ctx context.Context, r ai.Request) (ai.Result, error) {
	observation := r.Observation.(timedObservation)
	start := time.Now()
	queued := start.Sub(observation.created)
	r.Observation = observation.value
	result, err := s.Source.Decide(ctx, r)
	elapsed := time.Since(start)
	s.timings.mu.Lock()
	s.timings.queue = append(s.timings.queue, queued)
	s.timings.compute = append(s.timings.compute, elapsed)
	s.timings.mu.Unlock()
	return result, err
}
func percentiles(values []time.Duration) string {
	if len(values) == 0 {
		return "none"
	}
	copy := append([]time.Duration(nil), values...)
	slices.Sort(copy)
	return fmt.Sprintf("p50=%s p95=%s max=%s", copy[len(copy)/2], copy[(len(copy)-1)*95/100], copy[len(copy)-1])
}

func TestAIMatchPerformance(t *testing.T) {
	if os.Getenv("AI_EVALUATE") != "1" {
		t.Skip("set AI_EVALUATE=1 for complete-match concurrency measurements")
	}
	for _, concurrency := range []int{1, 5, 10} {
		t.Run(fmt.Sprint(concurrency), func(t *testing.T) {
			f, bot := aiFixture(t, false)
			if err := f.s.Close(); err != nil {
				t.Fatal(err)
			}
			timings := &decisionTimings{}
			f.options.AI = timedAdapter{timings: timings}
			var err error
			f.s, err = bidding.New(f.options)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.s.RecoverBeforeListenAt(f.ctx, 100, 100, time.Now().Add(time.Second)); err != nil {
				t.Fatal(err)
			}
			users := addAIUsers(t, f, concurrency)
			for _, user := range users {
				f.users[0] = user
				f.enqueueAI(bot.ID)
			}
			start := time.Now()
			deadline := start.Add(20 * time.Second)
			waits := []time.Duration{}
			done := map[int64]bool{}
			for len(done) < len(users) && time.Now().Before(deadline) {
				began := time.Now()
				tx, err := f.db.BeginTx(f.ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				_, err = tx.Exec(`UPDATE game_duel_user_slots SET game_key=game_key WHERE 0`)
				waits = append(waits, time.Since(began))
				tx.Rollback()
				if err != nil {
					t.Fatal(err)
				}
				f.tick()
				moved := false
				for _, user := range users {
					if done[user] {
						continue
					}
					f.users[0] = user
					home := f.read(0)
					if home.Current == nil && home.LatestResult != nil {
						done[user] = true
						continue
					}
					v := home.Current
					if v == nil || v.Locked[v.You] {
						continue
					}
					body := `{"kind":"joker","use":false}`
					if v.Phase == "bid" {
						var view engine.View
						if err := json.Unmarshal(v.View, &view); err != nil {
							t.Fatal(err)
						}
						hand := view.HandRemaining[v.You]
						body = fmt.Sprintf(`{"kind":"bid","card":%d}`, hand[len(hand)/2])
					}
					f.action(0, *v, body)
					moved = true
				}
				if moved {
					f.clock.Add(1)
				} else {
					time.Sleep(time.Millisecond)
				}
			}
			if len(done) != len(users) {
				t.Fatalf("completed %d/%d", len(done), len(users))
			}
			if err := f.s.Close(); err != nil {
				t.Fatal(err)
			}
			fallback := countAI(t, f, `SELECT count(*) FROM game_duel_sessions g,json_each(g.server_state_json,'$.actions') a WHERE json_extract(a.value,'$.origin')='fallback'`)
			var archived, derived float64
			if err := f.db.QueryRow(`SELECT avg(length(g.server_state_json)+length(g.initial_state_json)+length(g.terms_json)+length(a.snapshot_json)+(SELECT coalesce(sum(length(record_json)),0) FROM game_duel_rounds r WHERE r.session_id=g.id)) FROM game_duel_sessions g JOIN game_ai_sessions a ON a.session_id=g.id`).Scan(&archived); err != nil {
				t.Fatal(err)
			}
			if err := f.db.QueryRow(`SELECT coalesce(avg(length(features_json)),0) FROM game_ai_memories`).Scan(&derived); err != nil {
				t.Fatal(err)
			}
			timings.mu.Lock()
			defer timings.mu.Unlock()
			t.Logf("concurrent=%d completed=%d decisions=%d elapsed=%s queue_including_snapshot=%s compute=%s writer_wait=%s fallbacks=%d archive_json_bytes_per_match=%.0f derived_json_bytes_per_match=%.0f", concurrency, len(done), len(timings.compute), time.Since(start), percentiles(timings.queue), percentiles(timings.compute), percentiles(waits), fallback, archived, derived)
			if fallback != 0 {
				t.Fatal("unexpected local-source fallback")
			}
		})
	}
}

func addAIUsers(t *testing.T, f *fixture, count int) []int64 {
	users := []int64{f.users[0]}
	identity, err := continuity.New(f.db, deps{})
	if err != nil {
		t.Fatal(err)
	}
	defer identity.Close()
	for len(users) < count {
		tx, err := f.db.BeginTx(f.ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		discord, _ := db.GenerateOpaqueID("dah_")
		row, err := tx.Exec(`INSERT INTO users(discord_id,username,donation_credit_mag,total_requests,total_uncached_input_tokens,total_cache_write_input_tokens,total_cache_read_input_tokens,total_output_tokens,total_unknown_usage_requests,revision,created_at,updated_at) SELECT ?,'benchmark',zeroblob(16),zeroblob(16),zeroblob(16),zeroblob(16),zeroblob(16),zeroblob(16),zeroblob(16),zeroblob(16),100,100`, discord)
		if err != nil {
			tx.Rollback()
			t.Fatal(err)
		}
		id, _ := row.LastInsertId()
		if _, err := identity.BindUserTx(f.ctx, tx, id); err != nil {
			tx.Rollback()
			t.Fatal(err)
		}
		for _, asset := range []ledger.Asset{ledger.General, ledger.Game} {
			if _, err := ledger.CreateUserAssetAccount(f.ctx, tx, id, asset, 100); err != nil {
				tx.Rollback()
				t.Fatal(err)
			}
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		users = append(users, id)
	}
	return users
}
