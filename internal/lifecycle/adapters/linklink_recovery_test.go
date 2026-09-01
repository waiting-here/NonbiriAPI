package adapters

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/game/linklink"
	"github.com/waiting-here/NonbiriAPI/internal/lifecycle"
)

type fakeLinkLinkRecoveryOwner struct {
	decisionNow int64
	limit       int
	deadline    time.Time
	result      linklink.RecoveryResult
	err         error
}

func (owner *fakeLinkLinkRecoveryOwner) RecoverBeforeListenAt(
	_ context.Context,
	decisionNow int64,
	limit int,
	deadline time.Time,
) (linklink.RecoveryResult, error) {
	owner.decisionNow = decisionNow
	owner.limit = limit
	owner.deadline = deadline
	return owner.result, owner.err
}

func TestLinkLinkRecoveryAdapterDelegatesFrozenInputs(t *testing.T) {
	deadline := time.Unix(1_900_000_000, 123)
	owner := &fakeLinkLinkRecoveryOwner{result: linklink.RecoveryResult{Processed: 7, More: true}}
	result, err := newLinkLinkRecovery(owner).RecoverBeforeListener(
		context.Background(), 1_800_000_000, 9, deadline,
	)
	if err != nil || result != (lifecycle.WorkResult{Processed: 7, More: true}) {
		t.Fatalf("adapter result = (%+v,%v)", result, err)
	}
	if owner.decisionNow != 1_800_000_000 || owner.limit != 9 || !owner.deadline.Equal(deadline) {
		t.Fatalf("owner inputs = now:%d limit:%d deadline:%v", owner.decisionNow, owner.limit, owner.deadline)
	}
}

func TestLinkLinkRecoveryAdapterReturnsZeroOnError(t *testing.T) {
	wantErr := errors.New("recovery failed")
	owner := &fakeLinkLinkRecoveryOwner{
		result: linklink.RecoveryResult{Processed: 1, More: true},
		err:    wantErr,
	}
	result, err := newLinkLinkRecovery(owner).RecoverBeforeListener(
		context.Background(), 1, 1, time.Now().Add(time.Minute),
	)
	if !errors.Is(err, wantErr) || result != (lifecycle.WorkResult{}) {
		t.Fatalf("adapter error result = (%+v,%v)", result, err)
	}
	if _, err := NewLinkLinkRecovery(nil).RecoverBeforeListener(
		context.Background(), 1, 1, time.Now().Add(time.Minute),
	); !errors.Is(err, lifecycle.ErrUnavailable) {
		t.Fatalf("missing owner error = %v", err)
	}
}
