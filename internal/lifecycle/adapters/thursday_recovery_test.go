package adapters

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/activities"
	"github.com/waiting-here/NonbiriAPI/internal/lifecycle"
)

type fakeThursdayRecoveryOwner struct {
	decisionNow int64
	limit       int
	deadline    time.Time
	result      activities.ThursdayRecoveryResult
	err         error
}

func (owner *fakeThursdayRecoveryOwner) RecoverThursday(
	ctx context.Context,
	decisionNow int64,
	limit int,
	budgetDeadline time.Time,
) (activities.ThursdayRecoveryResult, error) {
	owner.decisionNow = decisionNow
	owner.limit = limit
	owner.deadline = budgetDeadline
	return owner.result, owner.err
}

func TestThursdayRecoveryAdapterDelegatesFrozenInputs(t *testing.T) {
	deadline := time.Unix(1_900_000_000, 987654321)
	owner := &fakeThursdayRecoveryOwner{result: activities.ThursdayRecoveryResult{Processed: 7, More: true}}
	result, err := NewThursdayRecovery(owner).RecoverBeforeListener(context.Background(), 1_800_000_000, 23, deadline)
	if err != nil || result != (lifecycle.WorkResult{Processed: 7, More: true}) {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if owner.decisionNow != 1_800_000_000 || owner.limit != 23 || !owner.deadline.Equal(deadline) {
		t.Fatalf("owner inputs now=%d limit=%d deadline=%v", owner.decisionNow, owner.limit, owner.deadline)
	}
}

func TestThursdayRecoveryAdapterReturnsZeroOnError(t *testing.T) {
	wantErr := errors.New("settlement failed")
	owner := &fakeThursdayRecoveryOwner{
		result: activities.ThursdayRecoveryResult{Processed: 1, More: true},
		err:    wantErr,
	}
	result, err := NewThursdayRecovery(owner).RecoverBeforeListener(
		context.Background(), 1, 1, time.Now().Add(time.Minute),
	)
	if !errors.Is(err, wantErr) || result != (lifecycle.WorkResult{}) {
		t.Fatalf("error result=%+v err=%v", result, err)
	}

	if result, err := (*ThursdayRecoveryAdapter)(nil).RecoverBeforeListener(
		context.Background(), 1, 1, time.Now().Add(time.Minute),
	); !errors.Is(err, lifecycle.ErrUnavailable) || result != (lifecycle.WorkResult{}) {
		t.Fatalf("nil adapter result=%+v err=%v", result, err)
	}
	if result, err := NewThursdayRecovery(nil).RecoverBeforeListener(
		context.Background(), 1, 1, time.Now().Add(time.Minute),
	); !errors.Is(err, lifecycle.ErrUnavailable) || result != (lifecycle.WorkResult{}) {
		t.Fatalf("nil owner result=%+v err=%v", result, err)
	}
}
