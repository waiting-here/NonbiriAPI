package blackjack

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math/rand"
	"path/filepath"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/dbfixture"
	"github.com/waiting-here/NonbiriAPI/internal/game/blackjack/engine"
)

// Compare seat-query work with the former repeated read on the real schema
// and driver. This measures an idle decision advance, not HTTP or settlement.
func BenchmarkIdleDecisionAdvance(b *testing.B) {
	for _, seats := range []int{1, engine.MaxSeats} {
		b.Run(fmt.Sprintf("seats_%d", seats), func(b *testing.B) {
			path := filepath.Join(b.TempDir(), "clock.sqlite")
			dbfixture.Materialize(b, path)
			database, err := sql.Open("sqlite", path+"?_pragma=foreign_keys(1)")
			if err != nil {
				b.Fatal(err)
			}
			defer database.Close()
			numbers := make([]int, seats)
			for i := range numbers {
				numbers[i] = i
			}
			state, err := engine.New(numbers, 0, rand.New(rand.NewSource(4)))
			if err != nil || state.Finished {
				b.Fatalf("decision fixture: %v", err)
			}
			body, err := json.Marshal(state)
			if err != nil {
				b.Fatal(err)
			}
			id, err := db.GenerateOpaqueID("bjt_")
			if err != nil {
				b.Fatal(err)
			}
			if _, err := database.Exec(`INSERT INTO game_blackjack_sessions(id,started_at,phase,revision,last_batch,state_json,view_json) VALUES(?,120,'decision',1,125,?,'{}')`, id, string(body)); err != nil {
				b.Fatal(err)
			}
			for number := range seats {
				entry, err := db.GenerateOpaqueID("bjq_")
				if err != nil {
					b.Fatal(err)
				}
				if _, err := database.Exec(`INSERT INTO game_blackjack_entries(id,user_id,state,stake_milli,platform_bp,welfare_bp,thursday_bp,created_at,session_id,seat_no) VALUES(?,NULL,'playing',1000,0,0,0,120,?,?)`, entry, id, number); err != nil {
					b.Fatal(err)
				}
			}
			for _, elapsed := range []int64{1, 19} {
				for _, reuse := range []bool{false, true} {
					b.Run(fmt.Sprintf("seconds_%d/reuse_%t", elapsed, reuse), func(b *testing.B) {
						ctx := context.Background()
						tx, err := database.BeginTx(ctx, nil)
						if err != nil {
							b.Fatal(err)
						}
						defer tx.Rollback()
						v, err := readSession(ctx, tx, id)
						if err != nil {
							b.Fatal(err)
						}
						service := &Service{}
						b.ReportAllocs()
						b.ResetTimer()
						for range b.N {
							if reuse {
								if _, err := service.advanceDecisions(ctx, tx, &v, 125+elapsed); err != nil {
									b.Fatal(err)
								}
							} else {
								if _, err := decodeState(v); err != nil {
									b.Fatal(err)
								}
								for range elapsed {
									list, err := sessionEntries(ctx, tx, id)
									if err != nil || len(list) != seats {
										b.Fatalf("seat snapshot: %d, %v", len(list), err)
									}
									for _, entry := range list {
										if entry.Pending.Valid || entry.Stopped {
											b.Fatal("fixture requires an action")
										}
									}
								}
							}
						}
						b.StopTimer()
						if v.LastBatch != 125 || v.Revision != 1 || v.StateJSON.String != string(body) {
							b.Fatal("idle advance changed persisted state")
						}
					})
				}
			}
		})
	}
}
