package donation

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
)

func TestForceRejectPermissionDoesNotGrantPrivateProjection(t *testing.T) {
	e := newDonationTestEnv(t)
	e.seedUser(t, "", nil, true)
	five := int64(5)
	actor := e.seedUser(t, "configured-reviewer", &five, false)
	owner := e.seedUser(t, "private-donor", nil, false)
	_, key := e.seedEndpointKey(t, owner, 'p')
	approved := approvedResetDonation(t, e, owner, key)
	policy := authz.DefaultStewardPermissions(5)
	policy.Permissions["donations.force_reject"] = true
	permissions, _ := json.Marshal(policy.Permissions)
	limits, _ := json.Marshal(policy.Limits)
	if _, err := e.store.DB().Exec(`INSERT INTO steward_permissions(user_id,revision,permissions,limits,created_at,updated_at) VALUES(?,1,?,?,?,?)`, actor, string(permissions), string(limits), donationTestNow, donationTestNow); err != nil {
		t.Fatal(err)
	}
	id := parseTestID(t, approved.ID)
	current, err := e.service.GetAdmin(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	input := ReviewInput{Decision: "force_reject", ExpectedRevision: parseTestID(t, current.Revision), Reason: "Requires review"}
	mutation := donationMutation(t, 'F', http.MethodPost, routeStewardReview, []int64{id}, input)
	for attempt := range 2 {
		got, err := e.service.ReviewSteward(context.Background(), actor, id, mutation, input)
		if err != nil || got.Replayed != (attempt == 1) || got.Value.Owner != nil || got.Value.Reviewer != nil {
			t.Fatalf("safe force-reject receipt: %+v %v", got, err)
		}
		for _, key := range got.Value.Keys {
			if key.EndpointKeyID != nil {
				t.Fatal("physical credential identity exposed")
			}
		}
	}
	policy.Permissions["donations.force_reject"] = false
	permissions, _ = json.Marshal(policy.Permissions)
	if _, err := e.store.DB().Exec(`UPDATE steward_permissions SET permissions=?,revision=2 WHERE user_id=?`, string(permissions), actor); err != nil {
		t.Fatal(err)
	}
	if _, err := e.service.ReviewSteward(context.Background(), actor, id, mutation, input); !errors.Is(err, authz.ErrPermissionDenied) {
		t.Fatal("revoked grant accepted replay", err)
	}
}

func TestManagedKeyReceiptKeepsReadGrantSeparateFromSourceScope(t *testing.T) {
	e := newDonationTestEnv(t)
	e.seedUser(t, "", nil, true)
	five := int64(5)
	actor := e.seedUser(t, "configured-maintainer", &five, false)
	owner := e.seedUser(t, "private-owner", nil, false)
	_, physical := e.seedEndpointKey(t, owner, 'z')
	d := approvedResetDonation(t, e, owner, physical)
	did, kid := parseTestID(t, d.ID), parseTestID(t, d.Keys[0].ID)
	p := authz.DefaultStewardPermissions(5)
	p.Permissions["charity.non_mainstream"] = true
	writePolicy := func() {
		permissions, _ := json.Marshal(p.Permissions)
		limits, _ := json.Marshal(p.Limits)
		if _, err := e.store.DB().Exec(`INSERT INTO steward_permissions(user_id,revision,permissions,limits,created_at,updated_at) VALUES(?,1,?,?,?,?) ON CONFLICT(user_id) DO UPDATE SET permissions=excluded.permissions,revision=steward_permissions.revision+1`, actor, string(permissions), string(limits), donationTestNow, donationTestNow); err != nil {
			t.Fatal(err)
		}
	}
	writePolicy()
	note := "Reviewed maintenance"
	input := KeyManagementInput{ExpectedRevision: 2, SafeNote: &note}
	mutation := donationMutation(t, 'M', http.MethodPatch, routeStewardKey, []int64{did, kid}, input)
	ctx := authz.WithStewardPermission(context.Background(), "donations.manage_keys")
	for i, full := range []bool{false, true, false} {
		p.Permissions["donations.read_all"] = full
		writePolicy()
		got, err := e.service.ManageKeySession(ctx, actor, did, kid, mutation, input)
		if err != nil || got.Replayed != (i > 0) {
			t.Fatalf("toggle replay: %+v %v", got, err)
		}
		if full {
			value, ok := got.Value.(StewardDonation)
			if !ok || value.ID != d.ID || value.Owner == nil {
				t.Fatalf("full replay projection: %+v", got.Value)
			}
		} else {
			value, ok := got.Value.(ManagedKeyReceipt)
			if !ok || value.Key.EndpointKeyID != nil || value.DonationID != d.ID {
				t.Fatalf("private replay projection: %+v", got.Value)
			}
		}
	}
}
