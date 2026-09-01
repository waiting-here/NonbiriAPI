package adapters

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
	"github.com/waiting-here/NonbiriAPI/internal/lifecycle"
)

type fakeIdempotencyMaintenanceOwner struct {
	recoverNow      int64
	recoverLimit    int
	recoverDeadline time.Time
	recoverResult   idempotency.MaintenanceResult
	recoverErr      error
	retainNow       int64
	retainLimit     int
	retainDeadline  time.Time
	retainResult    idempotency.MaintenanceResult
	retainErr       error
}

func (owner *fakeIdempotencyMaintenanceOwner) Recover(
	_ context.Context,
	decisionNow int64,
	limit int,
	deadline time.Time,
) (idempotency.MaintenanceResult, error) {
	owner.recoverNow = decisionNow
	owner.recoverLimit = limit
	owner.recoverDeadline = deadline
	return owner.recoverResult, owner.recoverErr
}

func (owner *fakeIdempotencyMaintenanceOwner) Retain(
	_ context.Context,
	decisionNow int64,
	limit int,
	deadline time.Time,
) (idempotency.MaintenanceResult, error) {
	owner.retainNow = decisionNow
	owner.retainLimit = limit
	owner.retainDeadline = deadline
	return owner.retainResult, owner.retainErr
}

func TestIdempotencyMaintenanceAdapterMapsBothFixedSlots(t *testing.T) {
	deadline := time.Unix(1_900_000_000, 123)
	owner := &fakeIdempotencyMaintenanceOwner{
		recoverResult: idempotency.MaintenanceResult{Processed: 3, More: true},
		retainResult:  idempotency.MaintenanceResult{Processed: 2, More: false},
	}
	adapter := NewIdempotencyMaintenance(owner)

	recovery, err := adapter.RecoverBeforeListener(context.Background(), 101, 7, deadline)
	if err != nil {
		t.Fatal(err)
	}
	if recovery != (lifecycle.WorkResult{Processed: 3, More: true}) {
		t.Fatalf("recovery result = %+v", recovery)
	}
	if owner.recoverNow != 101 || owner.recoverLimit != 7 || !owner.recoverDeadline.Equal(deadline) {
		t.Fatalf(
			"recovery input = now:%d limit:%d deadline:%v",
			owner.recoverNow, owner.recoverLimit, owner.recoverDeadline,
		)
	}

	retention, err := adapter.Retain(context.Background(), 202, 9, deadline)
	if err != nil {
		t.Fatal(err)
	}
	if retention != (lifecycle.WorkResult{Processed: 2, More: false}) {
		t.Fatalf("retention result = %+v", retention)
	}
	if owner.retainNow != 202 || owner.retainLimit != 9 || !owner.retainDeadline.Equal(deadline) {
		t.Fatalf(
			"retention input = now:%d limit:%d deadline:%v",
			owner.retainNow, owner.retainLimit, owner.retainDeadline,
		)
	}
}

func TestIdempotencyMaintenanceAdapterPropagatesErrorsAndRejectsMissingOwner(t *testing.T) {
	wantRecoveryErr := errors.New("recovery failed")
	wantRetentionErr := errors.New("retention failed")
	owner := &fakeIdempotencyMaintenanceOwner{
		recoverResult: idempotency.MaintenanceResult{Processed: 1, More: true},
		recoverErr:    wantRecoveryErr,
		retainResult:  idempotency.MaintenanceResult{Processed: 1, More: true},
		retainErr:     wantRetentionErr,
	}
	adapter := NewIdempotencyMaintenance(owner)
	deadline := time.Now().Add(time.Second)

	if result, err := adapter.RecoverBeforeListener(
		context.Background(), 1, 1, deadline,
	); !errors.Is(err, wantRecoveryErr) || result != (lifecycle.WorkResult{}) {
		t.Fatalf("recovery error result = (%+v, %v)", result, err)
	}
	if result, err := adapter.Retain(
		context.Background(), 1, 1, deadline,
	); !errors.Is(err, wantRetentionErr) || result != (lifecycle.WorkResult{}) {
		t.Fatalf("retention error result = (%+v, %v)", result, err)
	}

	missing := NewIdempotencyMaintenance(nil)
	if _, err := missing.RecoverBeforeListener(
		context.Background(), 1, 1, deadline,
	); !errors.Is(err, lifecycle.ErrUnavailable) {
		t.Fatalf("missing recovery owner error = %v", err)
	}
	if _, err := missing.Retain(
		context.Background(), 1, 1, deadline,
	); !errors.Is(err, lifecycle.ErrUnavailable) {
		t.Fatalf("missing retention owner error = %v", err)
	}
}
