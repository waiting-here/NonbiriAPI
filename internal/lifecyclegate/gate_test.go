package lifecyclegate

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func allowValidator(context.Context, int64, string) (bool, error) { return true, nil }

func TestGateRejectsUnboundedCapacity(t *testing.T) {
	if _, err := New(Config{MaxUsers: DefaultMaxUsers + 1}); !errors.Is(err, ErrCapacity) {
		t.Fatalf("oversized capacity err=%v, want ErrCapacity", err)
	}
}

func waitFor(t *testing.T, signal <-chan struct{}, label string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for %s", label)
	}
}

func TestRetirementCancelsAndDrainsOnlyTargetUser(t *testing.T) {
	gate, err := New(Config{MaxUsers: 8})
	if err != nil {
		t.Fatal(err)
	}
	defer gate.Close()

	ctxA, releaseA, err := gate.Admit(context.Background(), 11, "session-a", allowValidator)
	if err != nil {
		t.Fatal(err)
	}
	ctxB, releaseB, err := gate.Admit(context.Background(), 12, "session-b", allowValidator)
	if err != nil {
		t.Fatal(err)
	}
	if ctxB == nil {
		t.Fatal("other user received nil context")
	}
	defer releaseB()

	drained := make(chan struct{})
	go func() {
		<-ctxA.Done()
		releaseA()
		close(drained)
	}()
	retirementDone := make(chan *UserRetirement, 1)
	go func() {
		retirement, beginErr := gate.BeginUserRetirement(11)
		if beginErr == nil {
			retirementDone <- retirement
			return
		}
		retirementDone <- nil
	}()
	waitFor(t, ctxA.Done(), "target cancellation")
	waitFor(t, drained, "target drain")
	retirement := <-retirementDone
	if retirement == nil {
		t.Fatal("target retirement failed")
	}
	if _, _, err := gate.Admit(context.Background(), 11, "new-a", allowValidator); !errors.Is(err, ErrRetiring) {
		t.Fatalf("new target admission err=%v, want ErrRetiring", err)
	}
	ctxB2, releaseB2, err := gate.Admit(context.Background(), 12, "new-b", allowValidator)
	if err != nil || ctxB2 == nil {
		t.Fatalf("other user admission err=%v", err)
	}
	releaseB2()
	if !retirement.Abort() {
		t.Fatal("retirement abort did not win")
	}
	if _, releaseA2, err := gate.Admit(context.Background(), 11, "recovered-a", allowValidator); err != nil {
		t.Fatalf("target did not recover after abort: %v", err)
	} else {
		releaseA2()
	}
}

func TestContextAwareRetirementDoesNotWaitForCurrentRequest(t *testing.T) {
	gate, err := New(Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer gate.Close()
	ctx, release, err := gate.Admit(context.Background(), 21, "caller-key", allowValidator)
	if err != nil {
		t.Fatal(err)
	}
	retirement, err := gate.BeginUserRetirementContext(ctx, 21)
	if err != nil {
		t.Fatal(err)
	}
	if ctx.Err() == nil {
		t.Fatal("retiring request was not canceled")
	}
	if _, _, err := gate.Admit(context.Background(), 21, "replacement", allowValidator); !errors.Is(err, ErrRetiring) {
		t.Fatalf("replacement admission err=%v, want ErrRetiring", err)
	}
	if !retirement.Commit() {
		t.Fatal("context-aware retirement did not commit")
	}
	release()
	if _, release2, err := gate.Admit(context.Background(), 21, "after-delete", allowValidator); err != nil {
		t.Fatalf("new state could not be created after commit: %v", err)
	} else {
		release2()
	}
}

func TestAccountDeletionRetirementKeepsExcludedRequestUsable(t *testing.T) {
	gate, err := New(Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer gate.Close()
	ctx, release, err := gate.Admit(context.Background(), 22, "browser-session", allowValidator)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	retirement, err := gate.BeginUserRetirementExcludingContext(ctx, 22)
	if err != nil {
		t.Fatal(err)
	}
	if err := ctx.Err(); err != nil {
		t.Fatalf("excluded request was canceled: %v", err)
	}
	if _, _, err := gate.Admit(context.Background(), 22, "replacement", allowValidator); !errors.Is(err, ErrRetiring) {
		t.Fatalf("replacement admission err=%v, want ErrRetiring", err)
	}
	if !retirement.Abort() {
		t.Fatal("account-deletion retirement did not abort")
	}
}

func TestSecondCredentialValidationRunsInsideLease(t *testing.T) {
	gate, err := New(Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer gate.Close()
	var calls atomic.Int32
	validator := func(ctx context.Context, userID int64, binding string) (bool, error) {
		calls.Add(1)
		if userID != 31 {
			t.Errorf("validator received user=%d", userID)
		}
		return binding == "exact-binding", nil
	}
	if _, _, err := gate.Admit(context.Background(), 31, "wrong-binding", validator); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid binding err=%v, want ErrInvalid", err)
	}
	ctx, release, err := gate.Admit(context.Background(), 31, "exact-binding", validator)
	if err != nil || ctx == nil {
		t.Fatalf("valid binding err=%v", err)
	}
	release()
	if got := calls.Load(); got != 2 {
		t.Fatalf("validator calls=%d, want 2", got)
	}
}
