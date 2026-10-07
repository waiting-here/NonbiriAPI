package app

import (
	"context"
	"errors"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/adminapi"
	"github.com/waiting-here/NonbiriAPI/internal/egress"
	"github.com/waiting-here/NonbiriAPI/internal/flowcontrol"
	"github.com/waiting-here/NonbiriAPI/internal/ratelimit"
)

func TestCommittedConcurrencySnapshotSupersedesOlderCallbacks(t *testing.T) {
	stack, err := egress.NewStack(egress.StackOptions{})
	if err != nil {
		t.Fatal(err)
	}
	runtime := &auditRuntime{outbound: stack}
	apply := func(revision int64, global, endpoint string, keys ...string) {
		runtime.configurationChanged(adminapi.SiteConfigCommit{
			Revision: revision, Keys: keys,
			Values: map[string]string{egress.GlobalConcurrencyConfigKey: global, egress.PerEndpointConcurrencyConfigKey: endpoint},
		})
	}
	apply(3, "9", "2", egress.GlobalConcurrencyConfigKey, egress.PerEndpointConcurrencyConfigKey)
	if got := stack.ConcurrencyLimits(); got != (egress.ConcurrencyLimits{Global: 9, PerEndpoint: 2}) {
		t.Fatalf("saved limits were not applied: %+v", got)
	}
	apply(2, "64", "8", egress.GlobalConcurrencyConfigKey)
	if got := stack.ConcurrencyLimits(); got != (egress.ConcurrencyLimits{Global: 9, PerEndpoint: 2}) {
		t.Fatalf("late callback reverted newer limits: %+v", got)
	}
	// A later unrelated edit still carries the complete committed snapshot.
	apply(5, "12", "4", adminapi.KeySiteName)
	apply(4, "12", "4", egress.GlobalConcurrencyConfigKey, egress.PerEndpointConcurrencyConfigKey)
	if got := stack.ConcurrencyLimits(); got != (egress.ConcurrencyLimits{Global: 12, PerEndpoint: 4}) {
		t.Fatalf("overtaken limit update was lost: %+v", got)
	}
}

func TestCommittedRPMSnapshotChangesAllCapsWithoutResettingWindow(t *testing.T) {
	flow, err := flowcontrol.New(flowcontrol.Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { flow.Close() })
	runtime := &auditRuntime{flow: flow}
	apply := func(revision int64, global, user, charity string, keys ...string) {
		runtime.configurationChanged(adminapi.SiteConfigCommit{Revision: revision, Keys: keys, Values: map[string]string{
			adminapi.KeyGlobalRPM: global, adminapi.KeyGlobalRPMPerUser: user, adminapi.KeyDefaultRPMPerUser: charity,
		}})
	}
	apply(3, "20", "2", "7", adminapi.KeyGlobalRPMPerUser)
	for range 2 {
		reservation, _, err := flow.Admit(context.Background(), 1)
		if err != nil {
			t.Fatal(err)
		}
		reservation.Commit()
	}
	apply(2, "100", "60", "60", adminapi.KeyDefaultRPMPerUser)
	if got := flow.Limits(); got != (ratelimit.RPMLimits{GlobalLimit: 20, PerUserLimit: 2, CharityPerUserLimit: 7}) {
		t.Fatalf("stale snapshot reverted RPM caps: %+v", got)
	}
	// A callback for an unrelated setting can overtake the limit edit.
	apply(5, "30", "3", "4", adminapi.KeySiteName)
	apply(4, "30", "3", "4", adminapi.KeyGlobalRPM)
	if got := flow.Limits(); got != (ratelimit.RPMLimits{GlobalLimit: 30, PerUserLimit: 3, CharityPerUserLimit: 4}) {
		t.Fatalf("new caps were not applied: %+v", got)
	}
	reservation, _, err := flow.Admit(context.Background(), 1)
	if err != nil {
		t.Fatal("raised cap did not take effect", err)
	}
	reservation.Commit()
	if _, _, err := flow.Admit(context.Background(), 1); !errors.Is(err, flowcontrol.ErrRateLimited) {
		t.Fatal("configuration change discarded earlier requests", err)
	}
}
