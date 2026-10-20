package v1

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestProgressSerializesHeartbeatStagesAndStopsBeforeReturn(t *testing.T) {
	var inside atomic.Bool
	var overlap atomic.Bool
	var events []Progress
	heartbeat := make(chan struct{}, 1)
	ctx, stop := withProgress(t.Context(), 25, func(event Progress) {
		if inside.Swap(true) {
			overlap.Store(true)
		}
		defer inside.Store(false)
		events = append(events, event)
		if event.State == "heartbeat" {
			select {
			case heartbeat <- struct{}{}:
			default:
			}
		}
	}, time.Millisecond)
	setProgressSource(ctx, 20)
	finish := beginProgress(ctx, "migration", 21, "")
	rebuild := beginProgress(ctx, "rebuild", 0, "model_discovery_evidence")
	select {
	case <-heartbeat:
	case <-time.After(5 * time.Second):
		t.Fatal("heartbeat missing")
	}
	rebuild(context.Canceled)
	finish(context.Canceled)
	stop()
	if overlap.Load() || inside.Load() {
		t.Fatal("concurrent or still-running callback")
	}
	if len(events) < 5 {
		t.Fatal(events)
	}
	for i, event := range events {
		if event.Type != "upgrade_progress" || event.SourceSchemaVersion != 20 || event.TargetSchemaVersion != 25 || event.MigrationVersion != 21 || event.ElapsedMS < event.PhaseElapsedMS {
			t.Fatal("invalid progress event", event)
		}
		if i > 0 && event.ElapsedMS < events[i-1].ElapsedMS {
			t.Fatal("elapsed time moved backwards")
		}
	}
	if last := events[len(events)-1]; last.Phase != "migration" || last.State != "failed" {
		t.Fatal(last)
	}
}

func TestProgressCancellationBeforeStepDoesNotExecuteIt(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	ctx, stop := withProgress(ctx, 25, func(event Progress) {
		if event.State == "started" {
			cancel()
		}
	}, time.Second)
	defer stop()
	executed := false
	err := progressStep(ctx, "target", 0, "", func() error { executed = true; return nil })
	if err != context.Canceled || executed {
		t.Fatal(executed, err)
	}
}
