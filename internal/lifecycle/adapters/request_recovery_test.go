package adapters

import (
	"context"
	"errors"
	"sync"
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
	calls  int
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
	stub.calls++
	stub.call = recoveryCall{ctx: ctx, now: now, limit: limit, deadline: deadline}
	return stub.result, stub.err
}

func TestClaimRecoveryDrainsOnceAndRetriesIncompleteStartup(t *testing.T) {
	owner := &claimRecoveryStub{result: claim.RecoveryReport{ReleasedClaims: 1, More: true}}
	adapter := newClaimRecovery(owner)
	run := func() (lifecycle.WorkResult, error) {
		return adapter.RecoverBeforeListener(context.Background(), 1, 10, time.Now().Add(time.Minute))
	}
	if got, err := run(); err != nil || !got.More {
		t.Fatalf("first batch: %+v %v", got, err)
	}
	wantErr := errors.New("temporary recovery failure")
	owner.err = wantErr
	if _, err := run(); !errors.Is(err, wantErr) {
		t.Fatalf("retry error: %v", err)
	}
	owner.err = nil
	owner.result = claim.RecoveryReport{CompletedRequests: 11}
	if _, err := run(); !errors.Is(err, lifecycle.ErrInvariant) {
		t.Fatalf("invalid report: %v", err)
	}
	owner.result = claim.RecoveryReport{CompletedRequests: 1}
	if got, err := run(); err != nil || got.Processed != 1 || got.More {
		t.Fatalf("last batch: %+v %v", got, err)
	}
	owner.err = wantErr
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			if got, err := run(); err != nil || got != (lifecycle.WorkResult{}) {
				t.Errorf("maintenance: %+v %v", got, err)
			}
		})
	}
	wg.Wait()
	if owner.calls != 4 {
		t.Fatalf("startup owner called %d times", owner.calls)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := adapter.RecoverBeforeListener(ctx, 1, 10, time.Now().Add(time.Minute)); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled: %v", err)
	}
	// A newly constructed adapter belongs to a new process and must recover again.
	owner.err = nil
	if _, err := newClaimRecovery(owner).RecoverBeforeListener(context.Background(), 1, 10, time.Now().Add(time.Minute)); err != nil || owner.calls != 5 {
		t.Fatalf("restart: calls=%d %v", owner.calls, err)
	}
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
