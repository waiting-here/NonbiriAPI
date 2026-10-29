package donation

import (
	"context"
	"github.com/waiting-here/NonbiriAPI/internal/dbtest"
	"github.com/waiting-here/NonbiriAPI/internal/pagination"
	"testing"
)

func TestReaderPoolDonationBrowsePoliciesAndHandling(t *testing.T) {
	e := newDonationTestEnv(t)
	e.seedUser(t, "", nil, true)
	owner := e.seedUser(t, "reader-owner", nil, false)
	donation, _ := createBrowseDonation(t, e, owner, 'r', "")
	id, _ := parseCanonicalID(donation.ID)
	dbtest.WithoutWriter(t, e.store.DB(), e.store.ReadDB(), func(ctx context.Context) {
		if _, err := e.service.KeysOwnerPage(ctx, owner, id, pagination.Default()); err != nil {
			t.Fatal(err)
		}
		if _, err := e.service.BadgeAdmin(ctx); err != nil {
			t.Fatal(err)
		}
		if _, err := e.service.UserDonationHistoryPage(ctx, reviewerAdmin, 0, owner, ManagementFilter{}, pagination.Default()); err != nil {
			t.Fatal(err)
		}
		if _, err := e.service.selectDiscoveries(ctx, reviewerAdmin, 0, 0, ""); err != nil {
			t.Fatal(err)
		}
	})
}
