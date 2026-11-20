package claim

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/requestattempt"
)

func TestPhysicalKeyAdmissionReasonsAndQueueDeadline(t *testing.T) {
	f := newClaimFixture(t)
	owner := f.seedUser("blocked", false)
	key := f.seedKey(owner, "fixture-private-marker")
	active, err := f.service.Claim(t.Context(), selfLimitInput(f, owner, key))
	if err != nil {
		t.Fatal(err)
	}
	input := selfLimitInput(f, owner, key)
	for _, tc := range []struct {
		concurrency, rpm int64
		want             requestattempt.AdmissionCause
	}{
		{1, 0, requestattempt.AdmissionConcurrency}, {0, 1, requestattempt.AdmissionRPM}, {1, 1, requestattempt.AdmissionBoth},
	} {
		setTestKeyLimits(t, f, key.keyID, tc.concurrency, tc.rpm)
		_, err := f.service.Claim(t.Context(), input)
		if !errors.Is(err, ErrKeyRateLimited) || AdmissionReason(err) != tc.want || len(KeyQueueCandidates(err)) != 0 {
			t.Fatalf("limit %d/%d: %v (%q)", tc.concurrency, tc.rpm, err, AdmissionReason(err))
		}
	}
	configureQueue(t, f, key.keyID, 1, 0, 1, 0, "queue", "reject")
	ctx, cancel := context.WithTimeout(t.Context(), 500*time.Millisecond)
	defer cancel()
	done := startQueuedClaim(t, f, ctx, input)
	awaitQueueSize(t, f.service, key.keyID, 1)
	_, limited := f.service.Claim(t.Context(), selfLimitInput(f, owner, key))
	_, err = f.service.WaitForKey(t.Context(), KeyQueueCandidates(limited))
	if !errors.Is(err, ErrKeyRateLimited) || AdmissionReason(err) != requestattempt.AdmissionQueueFull {
		t.Fatalf("full queue: %v (%q)", err, AdmissionReason(err))
	}
	timedOut := receiveQueuedClaim(t, done)
	if !errors.Is(timedOut.err, context.DeadlineExceeded) || AdmissionReason(timedOut.err) != requestattempt.AdmissionQueueTimeout {
		t.Fatalf("waiting deadline: %v (%q)", timedOut.err, AdmissionReason(timedOut.err))
	}
	awaitQueueSize(t, f.service, key.keyID, 0)
	if _, err = f.service.ReleaseUndispatched(t.Context(), active); err != nil {
		t.Fatal(err)
	}
}

func TestBalancedPhysicalLimitsKeepIndividualReasons(t *testing.T) {
	f, owner, model, keys := personalRoutingFixture(t, "cache_balanced")
	for i, key := range keys[:2] {
		setTestKeyLimits(t, f, key.keyID, int64(1-i), int64(i))
		if _, err := f.service.Claim(t.Context(), selfLimitInput(f, owner, key)); err != nil {
			t.Fatal(err)
		}
	}
	ctx, _, err := requestattempt.New(t.Context(), owner, "POST", "/v1/chat/completions")
	if err != nil {
		t.Fatal(err)
	}
	input := personalRoutingInput(t, f, owner, model, keys[0], keys[1])
	_, err = f.service.Claim(ctx, input)
	if !errors.Is(err, ErrKeyRateLimited) || AdmissionReason(err) != requestattempt.AdmissionMixed {
		t.Fatalf("combined: %v (%q)", err, AdmissionReason(err))
	}
	route := requestattempt.RouteSnapshot(ctx)
	for _, reason := range []string{"key_rpm", "key_concurrency"} {
		if route == nil || !slices.ContainsFunc(route.Observations, func(o requestattempt.RouteObservation) bool {
			return o.Reason == reason && o.Stage == "claim" && o.BindingID != ""
		}) {
			t.Fatalf("missing %s in %+v", reason, route)
		}
	}
}
