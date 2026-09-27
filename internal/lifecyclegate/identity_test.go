package lifecyclegate

import (
	"context"
	"errors"
	"testing"
	"time"
)

func identityFixture(t *testing.T) *Gate {
	t.Helper()
	gate, err := New(Config{MaxUsers: 4, IdentityResolver: func(_ context.Context, id int64) ([32]byte, error) {
		key := [32]byte{1}
		if id == 3 {
			key[0] = 2
		}
		return key, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = gate.Close() })
	return gate
}

func TestIdentityRetirementExcludesDeletingLeaseAndClosesNewAccountAdmission(t *testing.T) {
	gate := identityFixture(t)
	ctx, release, err := gate.Admit(context.Background(), 1, "old-account", allowValidator)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	other, releaseOther, err := gate.Admit(context.Background(), 2, "same-identity", allowValidator)
	if err != nil {
		t.Fatal(err)
	}
	unrelated, releaseUnrelated, err := gate.Admit(context.Background(), 3, "different-identity", allowValidator)
	if err != nil {
		t.Fatal(err)
	}
	defer releaseUnrelated()
	go func() { <-other.Done(); releaseOther() }()
	retirement, err := gate.BeginUserRetirementExcludingContext(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if ctx.Err() != nil || unrelated.Err() != nil || other.Err() == nil {
		t.Fatal("identity cancellation scope is wrong")
	}
	if _, _, err := gate.Admit(context.Background(), 2, "new-account", allowValidator); !errors.Is(err, ErrRetiring) {
		t.Fatalf("same identity admitted during deletion: %v", err)
	}
	if _, err := gate.BeginIdentityChange(context.Background(), [32]byte{1}); !errors.Is(err, ErrRetiring) {
		t.Fatalf("registration crossed deletion: %v", err)
	}
	if !retirement.Commit() || retirement.Abort() {
		t.Fatal("retirement is not one-shot")
	}
	_, releaseNew, err := gate.Admit(context.Background(), 2, "new-account", allowValidator)
	if err != nil {
		t.Fatal(err)
	}
	releaseNew()
}

func TestIdentityDrainDeadlineReopensAdmission(t *testing.T) {
	gate := identityFixture(t)
	leaseCtx, release, err := gate.Admit(context.Background(), 1, "slow-request", allowValidator)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	if _, err := gate.BeginIdentityChange(ctx, [32]byte{1}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("drain error: %v", err)
	}
	if leaseCtx.Err() == nil {
		t.Fatal("blocked reader was not canceled")
	}
	_, releaseNew, err := gate.Admit(context.Background(), 2, "after-abort", allowValidator)
	if err != nil {
		t.Fatal(err)
	}
	releaseNew()
}

func TestIdentityChangeAndCredentialValidationAreOrdered(t *testing.T) {
	gate := identityFixture(t)
	change, err := gate.BeginIdentityChange(context.Background(), [32]byte{1})
	if err != nil {
		t.Fatal(err)
	}
	called := false
	validator := func(context.Context, int64, string) (bool, error) { called = true; return true, nil }
	if _, _, err := gate.Admit(context.Background(), 2, "stale-lookup", validator); !errors.Is(err, ErrRetiring) {
		t.Fatalf("admit: %v", err)
	}
	if called {
		t.Fatal("user validation ran across the identity write barrier")
	}
	if !change.Abort() || change.Commit() {
		t.Fatal("identity change is not one-shot")
	}
	if _, _, err := gate.Admit(context.Background(), 2, "revoked", func(context.Context, int64, string) (bool, error) { return false, nil }); !errors.Is(err, ErrInvalid) {
		t.Fatalf("stale credential accepted: %v", err)
	}
	gate.mu.Lock()
	defer gate.mu.Unlock()
	if len(gate.identities) != 0 || len(gate.users) != 0 {
		t.Fatal("failed admission leaked bounded state")
	}
}

func TestAutomaticIdentityRetirementCancelsItsOwnLeaseWithoutDeadlock(t *testing.T) {
	gate := identityFixture(t)
	ctx, release, err := gate.Admit(context.Background(), 1, "request", allowValidator)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	retirement, err := gate.BeginUserRetirementContext(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if ctx.Err() == nil {
		t.Fatal("automatic restriction left its request alive")
	}
	retirement.Abort()
}
