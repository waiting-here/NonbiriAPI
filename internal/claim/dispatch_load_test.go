package claim

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/observability"
)

func dispatchTestPhysical(t *testing.T, key testKey) [32]byte {
	t.Helper()
	id, err := physicalCharityKeyID(key.keyID, key.secretID, key.candidate.CanonicalBaseURL)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestBalancedDuplicateTargetsRetainAuthorityAndFallback(t *testing.T) {
	f, owner, model, keys := personalRoutingFixture(t, "cache_balanced")
	input := personalRoutingInput(t, f, owner, model, keys[0], keys[1])
	valid := input.BalancedCandidates[0]
	staleGrant, staleTarget := valid, valid
	staleGrant.Candidate.Authorization.GrantRevision++
	staleTarget.Candidate.Authorization.TargetRevision++
	input.BalancedCandidates = []BalancedCandidate{staleGrant, staleTarget, valid, input.BalancedCandidates[1]}
	first, err := f.service.Claim(t.Context(), input)
	if err != nil || first.EndpointKeyID() != keys[0].keyID {
		t.Fatalf("same-physical fallback: key %d, error %v", first.EndpointKeyID(), err)
	}
	if receipts, dispatches, _ := routingCounts(t, f); receipts != 1 || dispatches != 0 {
		t.Fatalf("failed candidates leaked state: receipts %d dispatches %d", receipts, dispatches)
	}
	// A later invocation must see the preceding reservation, even for repeated
	// candidates whose authorization and physical identity did not change.
	secondInput := personalRoutingInput(t, f, owner, model, keys[0], keys[1])
	secondInput.BalancedCandidates = append([]BalancedCandidate{valid, valid}, secondInput.BalancedCandidates[1])
	second, err := f.service.Claim(t.Context(), secondInput)
	if err != nil || second.EndpointKeyID() != keys[1].keyID {
		t.Fatalf("new reservation not observed: key %d, error %v", second.EndpointKeyID(), err)
	}
	if receipts, dispatches, _ := routingCounts(t, f); receipts != 2 || dispatches != 0 {
		t.Fatalf("reservation count = %d/%d", receipts, dispatches)
	}
}

func TestBalancedDuplicateTargetsRespectAdmissionSavepoint(t *testing.T) {
	f, owner, model, keys := personalRoutingFixture(t, "cache_balanced")
	first := personalRoutingClaim(t, f, owner, model, keys[0])
	dispatchRoutingClaim(t, f, first)
	if _, err := f.db.Exec(`INSERT INTO endpoint_key_limits(endpoint_key_id,max_concurrency,max_rpm) VALUES(?,1,0) ON CONFLICT(endpoint_key_id) DO UPDATE SET max_concurrency=1`, keys[0].keyID); err != nil {
		t.Fatal(err)
	}
	input := personalRoutingInput(t, f, owner, model, keys[0], keys[1])
	input.BalancedCandidates = append([]BalancedCandidate{input.BalancedCandidates[0], input.BalancedCandidates[0]}, input.BalancedCandidates[1])
	selected, err := f.service.Claim(t.Context(), input)
	if err != nil || selected.EndpointKeyID() != keys[1].keyID {
		t.Fatalf("affinity bypassed admission: key %d, error %v", selected.EndpointKeyID(), err)
	}
	if receipts, dispatches, _ := routingCounts(t, f); receipts != 2 || dispatches != 1 {
		t.Fatalf("savepoint leaked reservation: %d/%d", receipts, dispatches)
	}
}

func TestPhysicalDispatchLoadWindowAndSaturation(t *testing.T) {
	f, owner, model, keys := personalRoutingFixture(t, "cache_balanced")
	personalRoutingClaim(t, f, owner, model, keys[0])
	physical := dispatchTestPhysical(t, keys[0])
	other := dispatchTestPhysical(t, keys[1])
	for _, item := range []struct {
		id        [32]byte
		at, count int64
	}{
		{physical, 700, 100}, {physical, 701, 2}, {physical, 1000, 3}, {physical, 1001, 200}, {other, 1000, 400},
	} {
		if _, err := f.db.Exec(`INSERT INTO charity_dispatch_buckets(physical_id,dispatched_at,dispatch_count) VALUES(?,?,?)`, item.id[:], item.at, item.count); err != nil {
			t.Fatal(err)
		}
	}
	tx, err := f.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	load, err := physicalDispatchLoadTx(t.Context(), tx, physical, 1000)
	if err != nil || load != 6 {
		t.Fatalf("window plus pending = %d, %v", load, err)
	}
	if _, err := tx.Exec(`UPDATE charity_dispatch_buckets SET dispatch_count=? WHERE physical_id=? AND dispatched_at=701`, int64(math.MaxInt64), physical[:]); err != nil {
		t.Fatal(err)
	}
	load, err = physicalDispatchLoadTx(t.Context(), tx, physical, 1000)
	if err != nil || load != math.MaxInt64 {
		t.Fatalf("saturated load = %d, %v", load, err)
	}
}

// This fixed, opt-in sample exercises the real scoring and claim path with
// rollback after each operation. Bucket and duplicate distributions are synthetic.
func TestBalancedDispatchLoadSample(t *testing.T) {
	if os.Getenv("NONBIRI_PERFORMANCE_SAMPLE") != "1" {
		t.Skip("opt-in dispatch scoring workload")
	}
	for _, count := range []int{1000, 100000} {
		t.Run(fmt.Sprintf("buckets-%d", count), func(t *testing.T) {
			f, owner, model, keys := personalRoutingFixture(t, "cache_balanced")
			input := personalRoutingInput(t, f, owner, model, keys[0], keys[1])
			physical := dispatchTestPhysical(t, keys[0])
			tx, err := f.db.BeginTx(t.Context(), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			stmt, err := tx.Prepare(`INSERT INTO charity_dispatch_buckets(physical_id,dispatched_at,dispatch_count) VALUES(?,?,1)`)
			if err != nil {
				t.Fatal(err)
			}
			for i := 0; i < count; i++ {
				id, at := physical, int64(701+i)
				if i >= 300 {
					id = [32]byte{0xff}
					binary.BigEndian.PutUint64(id[24:], uint64(i))
					at = 1000
				}
				if _, err := stmt.Exec(id[:], at); err != nil {
					t.Fatal(err)
				}
			}
			if err := stmt.Close(); err != nil {
				t.Fatal(err)
			}
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
			var rows int
			if err := f.db.QueryRow(`SELECT count(*) FROM charity_dispatch_buckets`).Scan(&rows); err != nil || rows != count {
				t.Fatalf("bucket count %d: %v", rows, err)
			}
			for _, duplicates := range []int{1, 10, 100} {
				t.Run(fmt.Sprintf("candidates-%d", duplicates), func(t *testing.T) {
					selected := input
					selected.BalancedCandidates = make([]BalancedCandidate, duplicates)
					for i := range selected.BalancedCandidates {
						selected.BalancedCandidates[i] = input.BalancedCandidates[0]
					}
					claimID, err := db.GenerateOpaqueID("clm_")
					if err != nil {
						t.Fatal(err)
					}
					const rounds = 20
					performance := observability.NewPerformance()
					f.store.SetConnectionObserver(performance)
					defer f.store.SetConnectionObserver(nil)
					latencies := make([]float64, 0, rounds)
					var memoryBefore, memoryAfter runtime.MemStats
					runtime.ReadMemStats(&memoryBefore)
					start := time.Now()
					for range rounds {
						began := time.Now()
						tx, err := f.db.BeginTx(context.Background(), nil)
						if err != nil {
							t.Fatal(err)
						}
						handle, err := f.service.claimBalancedCandidatesTx(t.Context(), tx, claimID, 1000, selected, model, 1, true)
						if err != nil || handle.EndpointKeyID() != keys[0].keyID {
							_ = tx.Rollback()
							t.Fatalf("sample selected key %d: %v", handle.EndpointKeyID(), err)
						}
						if err := tx.Rollback(); err != nil {
							t.Fatal(err)
						}
						latencies = append(latencies, float64(time.Since(began).Nanoseconds())/1e6)
					}
					wall := time.Since(start)
					runtime.ReadMemStats(&memoryAfter)
					var holds, holdMS int64
					for _, row := range performance.Drain() {
						if strings.HasPrefix(row.Route, "occupancy.hold.") {
							holds += row.Completed
							holdMS += row.ActiveMS
						}
					}
					slices.Sort(latencies)
					encoded, err := json.Marshal(map[string]any{"label": os.Getenv("NONBIRI_SAMPLE_LABEL"), "buckets": count, "candidates": duplicates, "distinct_physical": 1, "active_buckets_per_physical": 300, "rounds": rounds, "writer_holds": holds, "writer_hold_ms": holdMS, "wall_ms": float64(wall.Nanoseconds()) / 1e6, "p50_ms": latencies[rounds/2], "p95_ms": latencies[rounds-2], "max_ms": latencies[rounds-1], "allocated_bytes": memoryAfter.TotalAlloc - memoryBefore.TotalAlloc})
					if err != nil {
						t.Fatal(err)
					}
					t.Log(string(encoded))
				})
			}
		})
	}
}
