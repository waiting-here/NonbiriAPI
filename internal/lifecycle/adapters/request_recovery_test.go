package adapters

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/claim"
	"github.com/waiting-here/NonbiriAPI/internal/lifecycle"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

type recoveryCall struct {
	ctx      context.Context
	now      int64
	limit    int
	deadline time.Time
}

type discoveryRecoveryStub struct {
	call   recoveryCall
	result resources.DiscoveryRecoveryResult
	err    error
}

func (stub *discoveryRecoveryStub) RecoverStaleDiscoveriesAt(
	ctx context.Context,
	now int64,
	limit int,
	deadline time.Time,
) (resources.DiscoveryRecoveryResult, error) {
	stub.call = recoveryCall{ctx: ctx, now: now, limit: limit, deadline: deadline}
	return stub.result, stub.err
}

type claimRecoveryStub struct {
	call   recoveryCall
	result claim.RecoveryReport
	err    error
}

func (stub *claimRecoveryStub) RecoverNonterminalAt(
	ctx context.Context,
	now int64,
	limit int,
	deadline time.Time,
) (claim.RecoveryReport, error) {
	stub.call = recoveryCall{ctx: ctx, now: now, limit: limit, deadline: deadline}
	return stub.result, stub.err
}

type secretRecoveryStub struct {
	call   recoveryCall
	result claim.MaintenanceReport
	err    error
}

func (stub *secretRecoveryStub) MaintainOrphanSecretsAt(
	ctx context.Context,
	now int64,
	limit int,
	deadline time.Time,
) (claim.MaintenanceReport, error) {
	stub.call = recoveryCall{ctx: ctx, now: now, limit: limit, deadline: deadline}
	return stub.result, stub.err
}

func TestRequestRecoveryAdaptersMapFrozenBudget(t *testing.T) {
	ctx := context.WithValue(context.Background(), struct{}{}, "request-recovery")
	deadline := time.Now().Add(time.Minute)
	const decisionNow int64 = 1_700_000_123
	const limit = 73

	discoveryOwner := &discoveryRecoveryStub{result: resources.DiscoveryRecoveryResult{Processed: 7, More: true}}
	discoveryResult, err := newDiscoveryRecovery(discoveryOwner).RecoverBeforeListener(ctx, decisionNow, limit, deadline)
	if err != nil || discoveryResult != (lifecycle.WorkResult{Processed: 7, More: true}) {
		t.Fatalf("discovery result=%+v err=%v", discoveryResult, err)
	}
	assertRecoveryCall(t, discoveryOwner.call, ctx, decisionNow, limit, deadline)

	claimOwner := &claimRecoveryStub{result: claim.RecoveryReport{
		ReleasedClaims: 2, CommittedClaims: 3, CompletedRequests: 5, More: true,
	}}
	claimResult, err := newClaimRecovery(claimOwner).RecoverBeforeListener(ctx, decisionNow, limit, deadline)
	if err != nil || claimResult != (lifecycle.WorkResult{Processed: 10, More: true}) {
		t.Fatalf("claim result=%+v err=%v", claimResult, err)
	}
	assertRecoveryCall(t, claimOwner.call, ctx, decisionNow, limit, deadline)

	secretOwner := &secretRecoveryStub{result: claim.MaintenanceReport{Marked: 11, Deleted: 13, More: false}}
	secretResult, err := newOrphanSecretRecovery(secretOwner).RecoverBeforeListener(ctx, decisionNow, limit, deadline)
	if err != nil || secretResult != (lifecycle.WorkResult{Processed: 24}) {
		t.Fatalf("secret result=%+v err=%v", secretResult, err)
	}
	assertRecoveryCall(t, secretOwner.call, ctx, decisionNow, limit, deadline)
}

func TestRequestRecoveryAdaptersRejectMissingOwnerAndPreserveErrors(t *testing.T) {
	deadline := time.Now().Add(time.Minute)
	for name, adapter := range map[string]lifecycle.RecoveryAdapter{
		"discovery": NewDiscoveryRecovery(nil),
		"claims":    NewClaimRecovery(nil),
		"secrets":   NewOrphanSecretRecovery(nil),
	} {
		t.Run(name+" missing", func(t *testing.T) {
			if _, err := adapter.RecoverBeforeListener(context.Background(), 1, 1, deadline); !errors.Is(err, lifecycle.ErrUnavailable) {
				t.Fatalf("missing owner error=%v", err)
			}
		})
	}

	wantErr := errors.New("recovery owner failed")
	for name, adapter := range map[string]lifecycle.RecoveryAdapter{
		"discovery": newDiscoveryRecovery(&discoveryRecoveryStub{err: wantErr}),
		"claims":    newClaimRecovery(&claimRecoveryStub{err: wantErr}),
		"secrets":   newOrphanSecretRecovery(&secretRecoveryStub{err: wantErr}),
	} {
		t.Run(name+" error", func(t *testing.T) {
			result, err := adapter.RecoverBeforeListener(context.Background(), 1, 1, deadline)
			if !errors.Is(err, wantErr) || result != (lifecycle.WorkResult{}) {
				t.Fatalf("result=%+v error=%v", result, err)
			}
		})
	}
}

func assertRecoveryCall(
	t *testing.T,
	call recoveryCall,
	ctx context.Context,
	now int64,
	limit int,
	deadline time.Time,
) {
	t.Helper()
	if call.ctx != ctx || call.now != now || call.limit != limit || !call.deadline.Equal(deadline) {
		t.Fatalf("recovery call=%+v, want ctx=%p now=%d limit=%d deadline=%s", call, ctx, now, limit, deadline)
	}
}
