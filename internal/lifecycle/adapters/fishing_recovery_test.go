package adapters

import (
	"context"
	"errors"
	"testing"
	"time"

	gameRuntime "github.com/waiting-here/NonbiriAPI/internal/game/runtime"
	"github.com/waiting-here/NonbiriAPI/internal/lifecycle"
)

type fakeFishingRecoveryOwner struct {
	decisionNow int64
	limit       int
	deadline    time.Time
	result      gameRuntime.RecoveryResult
	err         error
}

func (owner *fakeFishingRecoveryOwner) RecoverBeforeListenAt(
	_ context.Context,
	decisionNow int64,
	limit int,
	deadline time.Time,
) (gameRuntime.RecoveryResult, error) {
	owner.decisionNow, owner.limit, owner.deadline = decisionNow, limit, deadline
	return owner.result, owner.err
}

func TestFishingRecoveryAdapterMapsClosedResultAndInputs(t *testing.T) {
	deadline := time.Unix(1_800_000_100, 123)
	owner := &fakeFishingRecoveryOwner{result: gameRuntime.RecoveryResult{Processed: 19, More: true}}
	result, err := NewFishingRecovery(owner).RecoverBeforeListener(
		context.Background(), 1_800_000_000, 73, deadline,
	)
	if err != nil || result != (lifecycle.WorkResult{Processed: 19, More: true}) {
		t.Fatalf("result = (%+v,%v)", result, err)
	}
	if owner.decisionNow != 1_800_000_000 || owner.limit != 73 || !owner.deadline.Equal(deadline) {
		t.Fatalf("inputs now=%d limit=%d deadline=%v", owner.decisionNow, owner.limit, owner.deadline)
	}
}

func TestFishingRecoveryAdapterReturnsZeroOnErrorAndRejectsMissingOwner(t *testing.T) {
	want := errors.New("Fishing recovery failed")
	owner := &fakeFishingRecoveryOwner{
		result: gameRuntime.RecoveryResult{Processed: 1, More: true}, err: want,
	}
	result, err := NewFishingRecovery(owner).RecoverBeforeListener(
		context.Background(), 1, 1, time.Now().Add(time.Minute),
	)
	if !errors.Is(err, want) || result != (lifecycle.WorkResult{}) {
		t.Fatalf("error result = (%+v,%v)", result, err)
	}
	if result, err := NewFishingRecovery(nil).RecoverBeforeListener(
		context.Background(), 1, 1, time.Now().Add(time.Minute),
	); !errors.Is(err, lifecycle.ErrUnavailable) || result != (lifecycle.WorkResult{}) {
		t.Fatalf("nil owner result = (%+v,%v)", result, err)
	}
	var missing *FishingRecoveryAdapter
	if result, err := missing.RecoverBeforeListener(
		context.Background(), 1, 1, time.Now().Add(time.Minute),
	); !errors.Is(err, lifecycle.ErrUnavailable) || result != (lifecycle.WorkResult{}) {
		t.Fatalf("nil adapter result = (%+v,%v)", result, err)
	}
}
