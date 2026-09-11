package stewardautomation

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

func TestBindingBatchKeepsCompletedItemsAndStopsOnDeadlineOrRevocation(t *testing.T) {
	input := bindingInput{CharityModelID: "1", DonationKeyIDs: []string{"2", "3", "4"}}
	for _, firstSuccess := range []bool{false, true} {
		ctx, cancel := context.WithCancel(context.Background())
		calls := 0
		out, status := runBindings(ctx, input, []int64{2, 3, 4}, func(context.Context, int64) error {
			calls++
			cancel()
			if firstSuccess {
				return nil
			}
			return context.DeadlineExceeded
		})
		want := http.StatusGatewayTimeout
		if firstSuccess {
			want = http.StatusOK
			if out.Results[0].Status != "success" {
				t.Fatal("committed item lost")
			}
		}
		if status != want || calls != 1 || out.Results[1].Status != "incomplete" || out.Results[2].Status != "incomplete" {
			t.Fatalf("cancelled result: %d %#v calls=%d", status, out, calls)
		}
	}
	for _, err := range []error{authz.ErrForbidden, resources.ErrUnauthorized} {
		calls := 0
		out, status := runBindings(context.Background(), input, []int64{2, 3, 4}, func(context.Context, int64) error { calls++; return err })
		if status != 422 || calls != 1 {
			t.Fatalf("revocation did not stop later work: %d %d", status, calls)
		}
		for i, result := range out.Results {
			if result.DonationKeyID != input.DonationKeyIDs[i] || result.Status != "failed" {
				t.Fatalf("missing input result: %#v", result)
			}
		}
	}
}

func TestAutomationAdmissionIsSharedAndReleasesCapacity(t *testing.T) {
	s := &Service{active: make(map[int64]bool)}
	var wg sync.WaitGroup
	var mu sync.Mutex
	accepted := 0
	for i := 0; i < 40; i++ {
		wg.Go(func() {
			if s.admit(1) {
				mu.Lock()
				accepted++
				mu.Unlock()
			}
		})
	}
	wg.Wait()
	if accepted != 1 {
		t.Fatalf("same user admitted %d", accepted)
	}
	for _, id := range []int64{2, 3, 4} {
		if !s.admit(id) {
			t.Fatal("available slot refused")
		}
	}
	if s.admit(5) {
		t.Fatal("global bound exceeded")
	}
	s.release(1)
	if !s.admit(5) {
		t.Fatal("capacity not released")
	}
}

func TestAutomationErrorsNeverExposeCauses(t *testing.T) {
	for _, err := range []error{errors.New("upstream-secret"), atStep("keys[1]", errors.New("upstream-secret")), &discoveryFailureError{class: "upstream-secret"}} {
		_, message := safeError(err)
		if strings.Contains(message, "upstream-secret") {
			t.Fatal("raw error exposed")
		}
	}
}
