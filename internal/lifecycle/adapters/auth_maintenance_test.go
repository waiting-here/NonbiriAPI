package adapters

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/auth"
	"github.com/waiting-here/NonbiriAPI/internal/lifecycle"
)

type authSessionRetentionOwnerStub struct {
	decisionNow int64
	limit       int
	deadline    time.Time
	result      auth.LifecycleRetentionResult
	err         error
}

func (owner *authSessionRetentionOwnerStub) RetainSessionsAt(
	_ context.Context,
	decisionNow int64,
	limit int,
	deadline time.Time,
) (auth.LifecycleRetentionResult, error) {
	owner.decisionNow = decisionNow
	owner.limit = limit
	owner.deadline = deadline
	return owner.result, owner.err
}

func TestAuthSessionRetentionAdapterMapsExactInputsAndResult(t *testing.T) {
	wantErr := errors.New("retention failed")
	owner := &authSessionRetentionOwnerStub{
		result: auth.LifecycleRetentionResult{Processed: 37, More: true},
		err:    wantErr,
	}
	deadline := time.Unix(1_900_000_000, 0)
	result, err := NewAuthSessionRetention(owner).Retain(
		context.Background(), 1_800_000_000, 100, deadline,
	)
	if !errors.Is(err, wantErr) {
		t.Fatalf("error=%v", err)
	}
	if result != (lifecycle.WorkResult{}) {
		t.Fatalf("result=%+v", result)
	}
	if owner.decisionNow != 1_800_000_000 || owner.limit != 100 || !owner.deadline.Equal(deadline) {
		t.Fatalf("inputs now=%d limit=%d deadline=%v", owner.decisionNow, owner.limit, owner.deadline)
	}
}

func TestAuthSessionRetentionAdapterRejectsMissingOwner(t *testing.T) {
	var nilAdapter *AuthSessionRetentionAdapter
	for _, adapter := range []*AuthSessionRetentionAdapter{nilAdapter, NewAuthSessionRetention(nil)} {
		if _, err := adapter.Retain(
			context.Background(), 1, 1, time.Now().Add(time.Minute),
		); !errors.Is(err, lifecycle.ErrUnavailable) {
			t.Fatalf("error=%v", err)
		}
	}
}
