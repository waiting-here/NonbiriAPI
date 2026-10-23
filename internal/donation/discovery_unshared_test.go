package donation

import (
	"context"
	"errors"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/connectionstore"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

func TestManagedDiscoveryUnsharedCatalog(t *testing.T) {
	for _, scenario := range []string{"not shared", "sharing stopped"} {
		t.Run(scenario, func(t *testing.T) {
			f := newDiscoverySharingFixture(t, false, scenario != "not shared")
			if scenario == "sharing stopped" {
				if _, err := f.store.DB().Exec(`UPDATE donation_uses SET discovery_enabled=0,revision=revision+1 WHERE donation_key_id=?`, f.donationKey); err != nil {
					t.Fatal(err)
				}
			}
			ctx := context.Background()
			for _, role := range []string{"admin", "level6"} {
				actor := f.admin
				if role == "level6" {
					actor = f.steward
				}
				if _, err := f.start(t, role, actor, 'U'); !errors.Is(err, resources.ErrDiscoveryNotShared) {
					t.Fatalf("%s unshared catalog: %v", role, err)
				}
				if _, err := f.repository.ManagedDiscoveryEvidence(ctx, role, actor, f.donation, f.donationKey); !errors.Is(err, resources.ErrDiscoveryNotShared) {
					t.Fatalf("%s unshared evidence: %v", role, err)
				}
			}
			if _, err := f.start(t, "admin", f.owner, 'V'); !errors.Is(err, resources.ErrForbidden) {
				t.Fatalf("unauthorized caller learned sharing state: %v", err)
			}
			page, err := f.service.selectDiscoveries(ctx, reviewerAdmin, 0, 0, "")
			if err != nil || len(page.Items) != 0 {
				t.Fatalf("unshared catalog entered batch: %+v %v", page, err)
			}
			tx, err := f.store.DB().BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			uses, err := connectionstore.LoadApprovedUsesTx(ctx, tx, []int64{f.donationKey})
			if err != nil || len(uses[f.donationKey]) != 1 {
				t.Fatalf("approved inference use was lost: %+v %v", uses, err)
			}
			use := uses[f.donationKey][0]
			_, err = f.service.AuthorizeManagedDiscovery(ctx, tx, "admin", f.admin, f.donation, f.donationKey,
				resources.ManagedDiscoverySelection{UseID: use.UseID, ExpectedRevision: use.UseRevision + 1}, true)
			if !errors.Is(err, resources.ErrConflict) {
				t.Fatalf("stale explicit selection: %v", err)
			}
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			if f.rail.calls.Load() != 0 {
				t.Fatal("borrowed personal catalog authorization")
			}
			f.clock.Add(3601)
			if _, err := f.start(t, "admin", f.admin, 'W'); !errors.Is(err, resources.ErrResourceLocked) {
				t.Fatalf("expired key must retain availability refusal: %v", err)
			}
		})
	}
}
