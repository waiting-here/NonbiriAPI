package donation

import (
	"context"
	"errors"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/connectionstore"
	"github.com/waiting-here/NonbiriAPI/internal/pagination"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

func TestManagedDiscoveryDoesNotRequireCatalogSharing(t *testing.T) {
	for _, approved := range []bool{false, true} {
		for _, scenario := range []string{"legacy no pin", "sharing stopped", "standard fallback"} {
			t.Run(map[bool]string{false: "pending", true: "approved"}[approved]+"/"+scenario, func(t *testing.T) {
				f := newDiscoveryStatusFixture(t, false, scenario == "sharing stopped", approved)
				ctx := context.Background()
				if _, err := f.store.DB().Exec("UPDATE donation_uses SET discovery_enabled=0,revision=revision+1 WHERE donation_key_id=?", f.donationKey); err != nil {
					t.Fatal(err)
				}
				if scenario == "standard fallback" {
					if _, err := f.store.DB().Exec("DELETE FROM discovery_sources WHERE user_id=?", f.owner); err != nil {
						t.Fatal(err)
					}
					before := 0
					if err := f.store.DB().QueryRow("SELECT COUNT(*) FROM discovery_sources").Scan(&before); err != nil {
						t.Fatal(err)
					}
					evidence, err := f.repository.ManagedDiscoveryEvidence(ctx, "admin", f.admin, f.donation, f.donationKey)
					if err != nil || evidence.State != "unknown" {
						t.Fatalf("unknown evidence: %+v %v", evidence, err)
					}
					after := 0
					if err := f.store.DB().QueryRow("SELECT COUNT(*) FROM discovery_sources").Scan(&after); err != nil || after != before {
						t.Fatalf("evidence read created source: %d %d %v", before, after, err)
					}
				}
				page, err := f.service.UsesPage(ctx, reviewerAdmin, 0, f.donation, UseFilter{}, pagination.Request{Page: 1, Size: 20})
				if err != nil || len(page.Data) != 1 || !page.Data[0].Capabilities.CanDiscover || (!approved && page.Data[0].Capabilities.CanBind) {
					t.Fatalf("capabilities: %+v %v", page, err)
				}
				for i, role := range []string{"admin", "level6"} {
					actor := f.admin
					if role == "level6" {
						actor = f.steward
					}
					accepted, err := f.start(t, role, actor, byte('U'+i))
					if err != nil {
						t.Fatalf("%s catalog: %v", role, err)
					}
					f.wait(t, accepted.Value.OperationID)
					evidence, err := f.repository.ManagedDiscoveryEvidence(ctx, role, actor, f.donation, f.donationKey)
					if err != nil || evidence.State != "succeeded" {
						t.Fatalf("%s evidence: %+v %v", role, evidence, err)
					}
				}
				if _, err := f.start(t, "admin", f.owner, 'W'); !errors.Is(err, resources.ErrForbidden) {
					t.Fatalf("unauthorized caller: %v", err)
				}
				tx, err := f.store.DB().BeginTx(ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				uses, err := connectionstore.LoadApprovedUsesTx(ctx, tx, []int64{f.donationKey})
				want := 0
				if approved {
					want = 1
				}
				if err != nil || len(uses[f.donationKey]) != want {
					t.Fatalf("inference authority changed: %+v %v", uses, err)
				}
				tx.Rollback()
				f.clock.Add(3601)
				if _, err := f.start(t, "admin", f.admin, 'X'); !errors.Is(err, resources.ErrResourceLocked) {
					t.Fatalf("expired key: %v", err)
				}
			})
		}
	}
}
