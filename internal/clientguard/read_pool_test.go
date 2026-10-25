package clientguard

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"
)

func TestClientGuardPrecheckReleasesWriter(t *testing.T) {
	f := newGuardFixture(t)
	f.rule("Unmatched", "Blocked/", true, nil)
	held, err := f.store.DB().Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	ctx, cancel := context.WithTimeout(f.context("self", "Allowed/1"), time.Second)
	decision, err := f.service.CheckCall(ctx, f.user, "provider/model", testNow)
	cancel()
	held.Close()
	if err != nil || decision.Banned || f.gateCalls != 0 {
		t.Fatalf("precheck blocked or changed decision: %+v %v", decision, err)
	}

	cfg := f.service.config
	cfg.ReadDatabase = nil
	legacy, err := New(cfg)
	if err != nil || legacy.config.ReadDatabase != f.store.DB() {
		t.Fatal("default database changed", err)
	}

	cancelled, cancel := context.WithCancel(f.context("self", "Allowed/1"))
	cancel()
	if _, err := f.service.CheckCall(cancelled, f.user, "provider/model", testNow); err == nil {
		t.Fatal("cancelled precheck succeeded")
	}
}

func TestClientGuardReaderPerformanceSample(t *testing.T) {
	if os.Getenv("NONBIRI_PERFORMANCE_SAMPLE") != "1" {
		t.Skip("opt-in bounded performance sample")
	}
	for _, count := range []int{0, 100} {
		t.Run(fmt.Sprintf("rules-%d", count), func(t *testing.T) {
			f := newGuardFixture(t)
			for i := 0; i < count; i++ {
				f.rule(fmt.Sprintf("Unmatched %d", i), fmt.Sprintf("Blocked-%d/", i), true, nil)
			}
			ctx := f.context("self", "Allowed/1")
			for _, mode := range []string{"writer", "reader"} {
				candidate := *f.service
				candidate.config.ReadDatabase = f.store.DB()
				if mode == "reader" {
					candidate.config.ReadDatabase = f.store.ReadDB()
				}
				result := testing.Benchmark(func(b *testing.B) {
					b.ReportAllocs()
					for i := 0; i < b.N; i++ {
						decision, err := candidate.CheckCall(ctx, f.user, "provider/model", testNow)
						if err != nil || decision.Banned {
							b.Fatalf("decision=%+v err=%v", decision, err)
						}
					}
				})
				t.Logf("rules=%d pool=%s %s %s", count, mode, result.String(), result.MemString())
			}
		})
	}
}
