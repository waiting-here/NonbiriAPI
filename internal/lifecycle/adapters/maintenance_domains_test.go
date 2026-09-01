package adapters

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/donation"
	"github.com/waiting-here/NonbiriAPI/internal/lifecycle"
)

type fakeDonationRecoveryOwner struct {
	decisionNow int64
	limit       int
	deadline    time.Time
	processed   int
	err         error
}

func (owner *fakeDonationRecoveryOwner) MaterializeExpiries(
	ctx context.Context,
	decisionNow int64,
	limit int,
) (int, error) {
	owner.decisionNow = decisionNow
	owner.limit = limit
	owner.deadline, _ = ctx.Deadline()
	return owner.processed, owner.err
}

func TestDonationRecoveryAdapterDelegatesFrozenWorkerInputs(t *testing.T) {
	deadline := time.Unix(1_800_000_000, 123_456_789)
	owner := &fakeDonationRecoveryOwner{processed: 2}
	result, err := NewDonationRecovery(owner).RecoverBeforeListener(
		context.Background(), 1_700_000_000, 3, deadline,
	)
	if err != nil {
		t.Fatal(err)
	}
	if result != (lifecycle.WorkResult{Processed: 2, More: false}) {
		t.Fatalf("result = %+v", result)
	}
	if owner.decisionNow != 1_700_000_000 || owner.limit != 3 || !owner.deadline.Equal(deadline) {
		t.Fatalf("owner input = now:%d limit:%d deadline:%v", owner.decisionNow, owner.limit, owner.deadline)
	}

	owner.processed = 3
	result, err = NewDonationRecovery(owner).RecoverBeforeListener(
		context.Background(), 1_700_000_001, 3, deadline,
	)
	if err != nil {
		t.Fatal(err)
	}
	if result != (lifecycle.WorkResult{Processed: 3, More: true}) {
		t.Fatalf("full result = %+v", result)
	}
}

func TestDonationRecoveryAdapterPropagatesErrorsAndRejectsMissingOwner(t *testing.T) {
	owner := &fakeDonationRecoveryOwner{processed: 1, err: donation.ErrInvariant}
	result, err := NewDonationRecovery(owner).RecoverBeforeListener(
		context.Background(), 1, 2, time.Now().Add(time.Second),
	)
	if !errors.Is(err, lifecycle.ErrInvariant) || result != (lifecycle.WorkResult{}) {
		t.Fatalf("error result = (%+v, %v)", result, err)
	}

	_, err = NewDonationRecovery(nil).RecoverBeforeListener(
		context.Background(), 1, 2, time.Now().Add(time.Second),
	)
	if !errors.Is(err, lifecycle.ErrUnavailable) {
		t.Fatalf("missing owner error = %v", err)
	}
}

func TestDonationRecoveryAdapterRejectsInvalidWorkerInputs(t *testing.T) {
	deadline := time.Now().Add(time.Second)
	tests := []struct {
		name        string
		ctx         context.Context
		decisionNow int64
		limit       int
		deadline    time.Time
		want        error
	}{
		{name: "nil context", ctx: nil, decisionNow: 1, limit: 1, deadline: deadline, want: lifecycle.ErrInvalid},
		{name: "negative time", ctx: context.Background(), decisionNow: -1, limit: 1, deadline: deadline, want: lifecycle.ErrInvalid},
		{name: "future time", ctx: context.Background(), decisionNow: maxUnixSecond + 1, limit: 1, deadline: deadline, want: lifecycle.ErrInvalid},
		{name: "zero limit", ctx: context.Background(), decisionNow: 1, limit: 0, deadline: deadline, want: lifecycle.ErrInvalid},
		{name: "large limit", ctx: context.Background(), decisionNow: 1, limit: lifecycle.WorkerBatchLimit + 1, deadline: deadline, want: lifecycle.ErrInvalid},
		{name: "zero deadline", ctx: context.Background(), decisionNow: 1, limit: 1, want: lifecycle.ErrInvalid},
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	tests = append(tests, struct {
		name        string
		ctx         context.Context
		decisionNow int64
		limit       int
		deadline    time.Time
		want        error
	}{name: "canceled context", ctx: canceled, decisionNow: 1, limit: 1, deadline: deadline, want: lifecycle.ErrUnavailable})

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			owner := &fakeDonationRecoveryOwner{}
			result, err := NewDonationRecovery(owner).RecoverBeforeListener(
				test.ctx, test.decisionNow, test.limit, test.deadline,
			)
			if !errors.Is(err, test.want) || result != (lifecycle.WorkResult{}) {
				t.Fatalf("result = (%+v, %v), want %v", result, err, test.want)
			}
			if owner.limit != 0 {
				t.Fatalf("owner was called with limit %d", owner.limit)
			}
		})
	}
}
