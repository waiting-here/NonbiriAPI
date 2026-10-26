package charity

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/claim"
	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/db"
)

// The stored result backlog is synthetic; the schema, driver and folding code
// are real. Rollback restores the identical backlog for every timed iteration.
func TestFoldPerformanceSample(t *testing.T) {
	if os.Getenv("NONBIRI_PERFORMANCE_SAMPLE") != "1" {
		t.Skip("explicit bounded sample")
	}
	for _, count := range []int64{1, 32, 256} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			e := newCharityTestEnv(t)
			one := u128Blob(t, 1)
			generation, err := db.DecodeU128(one)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = e.store.DB().Exec("UPDATE donation_keys SET next_claim_seq=?,next_fold_seq=?,failure_streak=?,failure_disabled=0 WHERE id=?", u128Blob(t, count+1), one, u128Blob(t, 0), e.donationKey); err != nil {
				t.Fatal(err)
			}
			tx := beginTestTx(t, e.store.DB())
			for sequence := int64(1); sequence <= count; sequence++ {
				_, err = tx.Exec(`INSERT INTO donation_usage_reservations(claim_id,donation_key_id,streak_generation,claim_seq,price_reserved_milli,price_actual_milli,reward_actual_milli,calls_reserved,calls_actual,tokens_reserved,tokens_actual,protocol_success,usage_unknown,state,created_at,finalized_at,streak_disposition,failure_origin)
VALUES(?,?,?,?,0,0,0,0,0,0,0,0,0,'committed',?,?,'upstream_failure','upstream_protocol')`, mustOpaqueID(t, "clm_"), e.donationKey, one, u128Blob(t, sequence), charityTestNow, charityTestNow+sequence)
				if err != nil {
					t.Fatal(err)
				}
			}
			commitTestTx(t, tx)
			result := testing.Benchmark(func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					tx, err := e.store.DB().BeginTx(context.Background(), nil)
					if err != nil {
						b.Fatal(err)
					}
					err = foldStreak(context.Background(), tx, e.donationKey, generation, charityTestNow+count)
					rollback := tx.Rollback()
					if err != nil || rollback != nil {
						b.Fatalf("fold=%v rollback=%v", err, rollback)
					}
				}
			})
			t.Logf("backlog=%d %s %s", count, result.String(), result.MemString())
		})
	}
}

func TestServiceFoldPerformanceSample(t *testing.T) {
	if os.Getenv("NONBIRI_PERFORMANCE_SAMPLE") != "1" {
		t.Skip("explicit bounded sample")
	}
	const count = 32
	e := newBackoffEnv(t)
	first, _, _ := connectionDispatch(t, e, claim.RouteCharityChat)
	for range count - 1 {
		handle, _, _ := connectionDispatch(t, e, claim.RouteCharityChat)
		e.complete(t, handle, claim.AttemptOutcome{
			Kind: claim.ResultResponse, UpstreamStatus: 503,
			StreakDisposition: contract.StreakUpstreamFailure, FailureOrigin: contract.OriginUpstreamResponse,
		})
	}
	result := testing.Benchmark(func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			tx, err := e.store.DB().BeginTx(context.Background(), nil)
			if err != nil {
				b.Fatal(err)
			}
			// Close the leading gap without timing unrelated completion work.
			// The rollback retains the same real dispatch/service associations.
			_, err = tx.Exec(`UPDATE donation_usage_reservations SET state='committed',
price_actual_milli=0,reward_actual_milli=0,calls_actual=0,tokens_actual=0,
protocol_success=0,usage_unknown=1,finalized_at=?,streak_disposition='upstream_failure',
failure_origin='network' WHERE claim_id=?`, e.clock.Load(), first.ClaimID())
			if err == nil {
				err = foldServiceStreak(context.Background(), tx, first.ClaimID(), e.clock.Load())
			}
			rollback := tx.Rollback()
			if err != nil || rollback != nil {
				b.Fatalf("fold=%v rollback=%v", err, rollback)
			}
		}
	})
	t.Logf("service backlog=%d %s %s", count, result.String(), result.MemString())
}
