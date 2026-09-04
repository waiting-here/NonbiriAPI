package adapters

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/game/rps"
	"github.com/waiting-here/NonbiriAPI/internal/lifecycle"
)

type fakeRPSRecoveryOwner struct {
	decisionNow int64
	limit       int
	deadline    time.Time
	result      rps.RecoveryResult
	err         error
}

func (owner *fakeRPSRecoveryOwner) RecoverBeforeListenAt(
	_ context.Context,
	decisionNow int64,
	limit int,
	deadline time.Time,
) (rps.RecoveryResult, error) {
	owner.decisionNow, owner.limit, owner.deadline = decisionNow, limit, deadline
	return owner.result, owner.err
}

func TestRPSRecoveryAdapterMapsClosedResultAndInputs(t *testing.T) {
	deadline := time.Unix(1_800_000_100, 0)
	owner := &fakeRPSRecoveryOwner{result: rps.RecoveryResult{Processed: 17, More: true}}
	result, err := NewRPSRecovery(owner).RecoverBeforeListener(
		context.Background(), 1_800_000_000, 73, deadline,
	)
	if err != nil || result != (lifecycle.WorkResult{Processed: 17, More: true}) {
		t.Fatalf("result=(%+v,%v)", result, err)
	}
	if owner.decisionNow != 1_800_000_000 || owner.limit != 73 || !owner.deadline.Equal(deadline) {
		t.Fatalf("inputs now=%d limit=%d deadline=%v", owner.decisionNow, owner.limit, owner.deadline)
	}
}

func TestRPSRecoveryAdapterPreservesErrorAndRejectsMissingOwner(t *testing.T) {
	want := errors.New("rps recovery failed")
	owner := &fakeRPSRecoveryOwner{result: rps.RecoveryResult{Processed: 1, More: true}, err: want}
	result, err := NewRPSRecovery(owner).RecoverBeforeListener(
		context.Background(), 1, 1, time.Now().Add(time.Minute),
	)
	if !errors.Is(err, want) || result != (lifecycle.WorkResult{}) {
		t.Fatalf("error result=(%+v,%v)", result, err)
	}
	if _, err := NewRPSRecovery(nil).RecoverBeforeListener(
		context.Background(), 1, 1, time.Now().Add(time.Minute),
	); !errors.Is(err, lifecycle.ErrUnavailable) {
		t.Fatalf("missing owner error=%v", err)
	}
	var missing *RPSRecoveryAdapter
	if _, err := missing.RecoverBeforeListener(
		context.Background(), 1, 1, time.Now().Add(time.Minute),
	); !errors.Is(err, lifecycle.ErrUnavailable) {
		t.Fatalf("nil adapter error=%v", err)
	}
}
