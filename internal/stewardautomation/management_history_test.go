package stewardautomation

import (
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/managementlog"
)

func TestBindingHistorySeparatesRejectionsFromFailures(t *testing.T) {
	f := newPersonalFixture(t)
	for _, test := range []struct{ name, status, code, want string }{
		{"permission", "failed", "steward_permission_denied", "rejected"},
		{"conflict", "failed", "conflict", "rejected"},
		{"storage", "failed", "internal_error", "failed"},
		{"interrupted", "incomplete", "incomplete", "unknown"},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := managementlog.WithOperation(t.Context(), managementlog.Metadata{EventID: test.name, Source: managementlog.SourceCallerKey, ExecutorKind: "caller_key", Domain: "model-bindings", Action: "create"})
			if err := f.service.finishBindingHistory(ctx, 1, bindingsResult{Results: []bindingResult{{DonationKeyID: "1", Status: test.status, Code: test.code}}}); err != nil {
				t.Fatal(err)
			}
			var outcome string
			if err := f.store.ReadDB().QueryRow("SELECT outcome FROM management_operations WHERE event_id=?", test.name).Scan(&outcome); err != nil || outcome != test.want {
				t.Fatal(outcome, err)
			}
		})
	}
}
