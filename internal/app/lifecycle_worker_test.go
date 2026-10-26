package app

import (
	"context"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/lifecycle"
)

func TestLifecycleWorkerDelayedDecisionDoesNotSkipNextPass(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan struct{})
		var attempts, passes atomic.Int32
		nextAt := time.Now().Unix() + int64(lifecycle.WorkerSweepInterval/time.Second)
		const delay = 1100 * time.Millisecond
		go func() {
			defer close(done)
			runLifecycleWorker(ctx, func(context.Context) error {
				if attempts.Add(1) == 1 {
					// Delay before the coordinator reads its decision time. A fixed
					// ticker's next wake would then precede the persisted deadline.
					time.Sleep(delay)
				}
				now := time.Now().Unix()
				if now >= nextAt {
					passes.Add(1)
					nextAt = now + int64(lifecycle.WorkerSweepInterval/time.Second)
				}
				return nil
			})
		}()
		defer func() {
			cancel()
			<-done
		}()
		time.Sleep(lifecycle.WorkerSweepInterval + delay)
		synctest.Wait()
		if got := passes.Load(); got != 1 {
			t.Fatalf("first completed passes = %d, want 1", got)
		}
		time.Sleep(lifecycle.WorkerSweepInterval)
		synctest.Wait()
		if got := passes.Load(); got != 2 {
			t.Fatalf("completed passes after delayed decision = %d, want 2 (attempts=%d)", got, attempts.Load())
		}
		cancel()
		synctest.Wait()
		time.Sleep(2 * lifecycle.WorkerSweepInterval)
		synctest.Wait()
		if got := attempts.Load(); got != 2 {
			t.Fatalf("attempts after cancellation while waiting = %d, want 2", got)
		}
	})
}

func TestLifecycleWorkerCancellationDuringPass(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		started := make(chan struct{})
		done := make(chan struct{})
		go func() {
			defer close(done)
			runLifecycleWorker(ctx, func(ctx context.Context) error {
				close(started)
				<-ctx.Done()
				return ctx.Err()
			})
		}()
		<-started
		cancel()
		synctest.Wait()
		select {
		case <-done:
		default:
			t.Fatal("lifecycle worker did not stop after its in-flight pass was canceled")
		}
	})
}
