package resources

import (
	"context"
	"os"
	"testing"
)

func TestCallerKeyPerformanceSample(t *testing.T) {
	if os.Getenv("NONBIRI_PERFORMANCE_SAMPLE") != "1" {
		t.Skip("explicit bounded performance sample")
	}
	e := newResourceTestEnvironment(t)
	owner := e.seedUser(t, "caller-performance-owner")
	key, err := e.repository.RegenerateCallerKey(t.Context(), owner, 0)
	if err != nil {
		t.Fatal(err)
	}
	before := e.store.DB().Stats()
	result := testing.Benchmark(func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			// Keep both independent observations performed at public ingress.
			for range 2 {
				identity, err := e.repository.ResolveCallerKey(context.Background(), key.Secret)
				if err != nil || identity.UserID != owner || identity.Generation != 1 {
					b.Fatal("invalid caller identity", err)
				}
			}
		}
	})
	after := e.store.DB().Stats()
	t.Logf("caller_checks=2 sql=2 writer_waits=%d writer_wait_ns=%d %s %s", after.WaitCount-before.WaitCount, after.WaitDuration-before.WaitDuration, result.String(), result.MemString())
}
