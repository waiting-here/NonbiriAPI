package charity

import (
	"context"
	"fmt"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/claim"
	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/headerfacts"
	"github.com/waiting-here/NonbiriAPI/internal/observability"
)

type hotpathSampleKey struct{}

type hotpathSampleObserver struct {
	hold  [3]atomic.Int64
	count [3]atomic.Int64
}

func (o *hotpathSampleObserver) StartConnectionSpan(ctx context.Context, kind string) func(error) {
	stage, ok := ctx.Value(hotpathSampleKey{}).(int)
	if !ok || kind != "hold" {
		return func(error) {}
	}
	start := time.Now()
	return func(error) {
		o.hold[stage].Add(int64(time.Since(start)))
		o.count[stage].Add(1)
	}
}

// This bounded sample exercises the production claim, charity and ledger
// implementations on the real engine. Fixture setup and terminal cleanup are
// outside the measured acceptance, claim and dispatch calls.
func TestClaimHotPathPerformanceSample(t *testing.T) {
	if os.Getenv("NONBIRI_PERFORMANCE_SAMPLE") != "1" {
		t.Skip("explicit bounded sample")
	}
	const count = 100
	for _, size := range []int{16, 2048} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			e := newCharityTestEnv(t)
			repository, err := observability.NewRepository(e.store.DB())
			if err != nil {
				t.Fatal(err)
			}
			rail := e.billingRail(t, repository)
			t.Cleanup(func() { _ = rail.Close() })
			observer := &hotpathSampleObserver{}
			e.store.SetConnectionObserver(observer)
			t.Cleanup(func() { e.store.SetConnectionObserver(nil) })
			incoming := httptest.NewRequest("POST", "https://example.invalid/v1/chat/completions", nil)
			incoming.Header.Set("User-Agent", strings.Repeat("a", size))
			ctx := observability.WithSource(headerfacts.WithRequest(t.Context(), incoming),
				observability.Source{EffectiveIP: "192.0.2.1", IPQuality: "direct_peer", UserAgent: strings.Repeat("a", size)})
			var elapsed [3]time.Duration
			var version string
			if err = e.store.DB().QueryRow("SELECT sqlite_version()").Scan(&version); err != nil {
				t.Fatal(err)
			}
			for range count {
				now := charityTestNow + 30
				start := time.Now()
				request, err := rail.Accept(context.WithValue(ctx, hotpathSampleKey{}, 0), claim.AcceptInput{
					UserID: e.callerID, Route: claim.RouteCharityChat, ModelSnapshot: "[公益]provider/model",
					AttemptLimit: 1, ReservedMilli: 5, CharityModelID: e.tokenModel, CharityDecisionNow: &now,
				})
				elapsed[0] += time.Since(start)
				if err != nil {
					t.Fatal(err)
				}
				input := e.quotaClaimInput(t, request.ID, 1)
				start = time.Now()
				handle, err := rail.Claim(context.WithValue(ctx, hotpathSampleKey{}, 1), input)
				elapsed[1] += time.Since(start)
				if err != nil {
					t.Fatal(err)
				}
				start = time.Now()
				dispatch, err := rail.TakeForDispatch(context.WithValue(ctx, hotpathSampleKey{}, 2), handle)
				elapsed[2] += time.Since(start)
				if err != nil {
					t.Fatal(err)
				}
				dispatch.Clear()
				if _, err = rail.CompleteAttempt(ctx, handle, claim.AttemptOutcome{
					Kind: claim.ResultResponse, UpstreamStatus: 200, ProtocolSuccess: true, ResponseStarted: true,
					Usage: contract.Usage{Present: true},
				}); err != nil {
					t.Fatal(err)
				}
				if _, err = rail.CompleteRequest(ctx, claim.CompleteRequestInput{
					RequestID: request.ID, Caller: claim.CallerResult{Class: claim.ResultSuccess, Status: 200},
					Disposition: claim.AccountingCommit,
				}); err != nil {
					t.Fatal(err)
				}
			}
			e.billingBalance(t, e.callerID, 10000)
			e.billingBalance(t, e.donorID, 10000)
			var sources, terminals int
			if err = e.store.DB().QueryRow(`SELECT
(SELECT count(*) FROM request_source_facts WHERE kind='charity'),
(SELECT count(*) FROM logical_requests WHERE state='terminal')`).Scan(&sources, &terminals); err != nil || sources != count || terminals != count {
				t.Fatalf("source/terminal=%d/%d err=%v", sources, terminals, err)
			}
			for stage, name := range []string{"accept", "claim", "dispatch"} {
				if observer.count[stage].Load() != count {
					t.Fatalf("stage=%s holds=%d", name, observer.count[stage].Load())
				}
				t.Logf("sqlite=%s agent_bytes=%d count=%d stage=%s wall_us=%.3f hold_us=%.3f",
					version, size, count, name, float64(elapsed[stage].Nanoseconds())/count/1000,
					float64(observer.hold[stage].Load())/count/1000)
			}
		})
	}
}
