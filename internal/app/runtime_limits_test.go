package app

import (
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/adminapi"
	"github.com/waiting-here/NonbiriAPI/internal/egress"
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
