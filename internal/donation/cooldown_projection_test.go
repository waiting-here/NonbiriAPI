package donation

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

func TestCooldownProjectionMatchesOwnerManagementSummaryAndExport(t *testing.T) {
	e := newDonationTestEnv(t)
	ctx := context.Background()
	owner := e.seedUser(t, "cooldown-owner", nil, false)
	level := int64(6)
	steward := e.seedUser(t, "cooldown-steward", &level, false)
	e.seedUser(t, "", nil, true)
	d, _ := createBrowseDonation(t, e, owner, 'c', "")
	id, key := parseTestID(t, d.ID), parseTestID(t, d.Keys[0].ID)
	until := e.clock.Load() + 60
	if _, err := e.store.DB().Exec("UPDATE donations SET status='approved' WHERE id=?", id); err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.DB().Exec("INSERT INTO donation_key_cooldowns(donation_key_id,rate_limited_until,updated_at) VALUES(?,?,?)", key, until, e.clock.Load()); err != nil {
		t.Fatal(err)
	}
	assertState := func(want string, deadline *int64) {
		t.Helper()
		own, err := e.service.GetOwner(ctx, owner, id)
		if err != nil {
			t.Fatal(err)
		}
		admin, err := e.service.GetAdmin(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		managed, err := e.service.GetSteward(ctx, steward, id)
		if err != nil {
			t.Fatal(err)
		}
		ownerPage, err := e.service.KeysOwnerPage(ctx, owner, id, pagination.Default())
		if err != nil {
			t.Fatal(err)
		}
		adminPage, err := e.service.KeysAdminPage(ctx, id, pagination.Default())
		if err != nil {
			t.Fatal(err)
		}
		stewardPage, err := e.service.KeysStewardPage(ctx, steward, id, pagination.Default())
		if err != nil {
			t.Fatal(err)
		}
		for _, got := range []DonationKey{own.Keys[0], ownerKey(admin.Keys[0]), ownerKey(AdminDonationKey(managed.Keys[0])), ownerPage.Data[0].DonationKey, adminPage.Data[0].DonationKey, stewardPage.Data[0].DonationKey} {
			if got.CharityState != want || (got.RateLimitedUntil == nil) != (deadline == nil) || deadline != nil && *got.RateLimitedUntil != *deadline {
				t.Fatalf("want %s/%v got %+v", want, deadline, got)
			}
		}
		summary, err := e.service.DonationsOwnerPage(ctx, owner, ManagementFilter{}, pagination.Default())
		if err != nil {
			t.Fatal(err)
		}
		if summary.Data[0].StateCounts[want] != "1" {
			t.Fatal(summary)
		}
		tx, err := e.store.DB().BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		exported, err := e.service.ExportUser(ctx, tx, owner, 100)
		tx.Rollback()
		if err != nil {
			t.Fatal(err)
		}
		got := exported[0].Keys[0]
		if got.CharityState != want || (got.RateLimitedUntil == nil) != (deadline == nil) || deadline != nil && *got.RateLimitedUntil != *deadline {
			t.Fatal(got)
		}
		encoded, _ := json.Marshal(exported)
		if !strings.Contains(string(encoded), "\"rate_limited_until\":") {
			t.Fatal("export missing explicit deadline")
		}
	}
	assertState("rate_limited", &until)
	// Split token exhaustion and physical/manual suspension remain authoritative.
	if _, err := e.store.DB().Exec("UPDATE donation_keys SET input_token_limit_mag=nbi_u128(0),input_token_reserve=1,output_token_reserve=1 WHERE id=?", key); err != nil {
		t.Fatal(err)
	}
	assertState("exhausted", &until)
	if _, err := e.store.DB().Exec("UPDATE donation_keys SET input_token_limit_mag=NULL,enabled=0 WHERE id=?", key); err != nil {
		t.Fatal(err)
	}
	assertState("disabled", &until)
	e.clock.Store(until)
	assertState("disabled", nil)
	if _, err := e.store.DB().Exec("UPDATE donation_keys SET enabled=1 WHERE id=?", key); err != nil {
		t.Fatal(err)
	}
	assertState("available", nil)
}
