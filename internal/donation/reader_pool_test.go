package donation

import (
	"context"
	"errors"
	"github.com/waiting-here/NonbiriAPI/internal/dbtest"
	"github.com/waiting-here/NonbiriAPI/internal/pagination"
	"testing"
)

func TestReaderPoolDonationBrowsePoliciesAndHandling(t *testing.T) {
	e := newDonationTestEnv(t)
	e.seedUser(t, "", nil, true)
	owner := e.seedUser(t, "reader-owner", nil, false)
	level := int64(6)
	steward := e.seedUser(t, "reader-reviewer", &level, false)
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
		listed, err := e.service.AutomationDonations(ctx, steward, "", pagination.Default())
		if err != nil || len(listed.Data) != 1 || listed.Data[0].ID != donation.ID {
			t.Fatalf("automation donation list: %+v, %v", listed, err)
		}
	})
	if _, err := e.store.DB().Exec(`UPDATE users SET level=4 WHERE id=?`, steward); err != nil {
		t.Fatal(err)
	}
	dbtest.WithoutWriter(t, e.store.DB(), e.store.ReadDB(), func(ctx context.Context) {
		if _, err := e.service.AutomationDonations(ctx, steward, "", pagination.Default()); !errors.Is(err, ErrForbidden) {
			t.Fatalf("revoked automation authority: %v", err)
		}
	})
}
