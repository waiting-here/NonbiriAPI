package adminalerts

import (
	"context"
	"testing"
)

func TestManualResolutionHistoryAfterAutomaticResolution(t *testing.T) {
	environment := newAlertTestEnvironment(t)
	ctx := context.Background()
	for _, source := range []string{"automatic_blacklist", "worker_recovered"} {
		for _, bulk := range []bool{false, true} {
			id := environment.seedAlert(t, string(KindWorkerCheckpointFailed), "Worker outcome", "lifecycle_recovery_v1", nil, alertTestNow-1, true)
			if _, err := environment.store.DB().Exec(`UPDATE admin_alerts SET resolution_kind=? WHERE id=?`, source, id); err != nil {
				t.Fatal(err)
			}
			resolve := func() {
				t.Helper()
				if bulk {
					if _, err := environment.repository.ResolveMany(ctx, environment.adminID, []int64{id}); err != nil {
						t.Fatal(err)
					}
				} else if _, err := environment.repository.SetResolved(ctx, environment.adminID, id, true); err != nil {
					t.Fatal(err)
				}
			}
			check := func(want string) {
				t.Helper()
				detail, err := environment.repository.GetDetail(ctx, environment.adminID, id)
				if err != nil || detail.ResolutionKind != want {
					t.Fatalf("resolution source=%q, want %q: %v", detail.ResolutionKind, want, err)
				}
			}
			resolve()
			check(source)
			if _, err := environment.repository.SetResolved(ctx, environment.adminID, id, false); err != nil {
				t.Fatal(err)
			}
			check("")
			resolve()
			check("manual")
			resolve()
			check("manual")
		}
	}
}
