package donation

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
)

func TestManagedKeyReplayKeepsStoredSnapshotAfterLaterEdit(t *testing.T) {
	for _, full := range []bool{false, true} {
		name := "redacted"
		if full {
			name = "full"
		}
		t.Run(name, func(t *testing.T) {
			e := newDonationTestEnv(t)
			e.seedUser(t, "", nil, true)
			five := int64(5)
			actor := e.seedUser(t, "receipt-maintainer", &five, false)
			owner := e.seedUser(t, "receipt-donor", nil, false)
			_, key := e.seedEndpointKey(t, owner, 'S')
			d := approvedResetDonation(t, e, owner, key)
			did, kid := parseTestID(t, d.ID), parseTestID(t, d.Keys[0].ID)
			p := authz.DefaultStewardPermissions(5)
			p.DonationScope = authz.DonationScopeFull
			p.Permissions["donations.read_all"] = full
			writeDonationPolicy(t, e, actor, p)
			ctx := authz.WithStewardPermission(context.Background(), "donations.manage_keys")
			firstNote := "Original maintenance decision"
			first := KeyManagementInput{ExpectedRevision: 2, SafeNote: &firstNote}
			mutation := donationMutation(t, '1', http.MethodPatch, routeStewardKey, []int64{did, kid}, first)
			saved, err := e.service.ManageKeySession(ctx, actor, did, kid, mutation, first)
			if err != nil {
				t.Fatal(err)
			}
			laterNote := "Later maintenance decision"
			later := KeyManagementInput{ExpectedRevision: 3, SafeNote: &laterNote}
			if _, err := e.service.ManageKeySession(ctx, actor, did, kid, donationMutation(t, '2', http.MethodPatch, routeStewardKey, []int64{did, kid}, later), later); err != nil {
				t.Fatal(err)
			}
			replayed, err := e.service.ManageKeySession(ctx, actor, did, kid, mutation, first)
			if err != nil || !replayed.Replayed || !bytes.Equal(saved.Body, replayed.Body) {
				t.Fatalf("stored decision replaced by current data: %s != %s, %v", saved.Body, replayed.Body, err)
			}
			p.Permissions["donations.read_all"] = false
			writeDonationPolicy(t, e, actor, p)
			redacted, err := e.service.ManageKeySession(ctx, actor, did, kid, mutation, first)
			if err != nil {
				t.Fatal(err)
			}
			value, ok := redacted.Value.(ManagedKeyReceipt)
			if !ok || value.Key.EndpointKeyID != nil || bytes.Contains(redacted.Body, []byte("receipt-donor")) {
				t.Fatalf("narrowed replay exposes identity: %s", redacted.Body)
			}
			p.Permissions["donations.manage_keys"] = false
			writeDonationPolicy(t, e, actor, p)
			if _, err := e.service.ManageKeySession(ctx, actor, did, kid, mutation, first); !errors.Is(err, authz.ErrPermissionDenied) {
				t.Fatal("revoked action replayed", err)
			}
		})
	}
}

func TestRejectedReviewReceiptPreservesDecisionKeysAndNarrowsScope(t *testing.T) {
	for _, initialScope := range []authz.DonationScope{authz.DonationScopeStandard, authz.DonationScopeFull} {
		t.Run(string(initialScope), func(t *testing.T) {
			e := newDonationTestEnv(t)
			e.seedUser(t, "", nil, true)
			five := int64(5)
			actor := e.seedUser(t, "decision-reviewer", &five, false)
			owner := e.seedUser(t, "decision-owner", nil, false)
			_, first := e.seedEndpointKey(t, owner, 'A')
			_, second := e.seedEndpointKey(t, owner, 'B')
			d := e.createDonation(t, owner, first, second)
			id := parseTestID(t, d.ID)
			// Resolve every service of the first member; only the second is pending
			// when this review begins. Whole-donation readers still see both members.
			var items []UseReviewItem
			for _, use := range ownerUsesForTest(t, e, owner, id) {
				if use.DonationKeyID == d.Keys[0].ID {
					items = append(items, UseReviewItem{UseID: parseTestID(t, use.ID), ExpectedRevision: parseTestID(t, use.Revision), Decision: "reject", ReviewNote: "Earlier decision"})
				}
			}
			if len(items) == 0 {
				t.Fatal("missing initial service fixture")
			}
			resolved, err := e.service.reviewUses(context.Background(), reviewerAdmin, 0, id, items, donationMutation(t, 'U', http.MethodPost, routeAdminUses+"/review", []int64{id}, items))
			if err != nil {
				t.Fatal(err)
			}
			for _, item := range resolved.Value.Results {
				if !item.OK {
					t.Fatal("earlier rejection failed", item)
				}
			}
			p := authz.DefaultStewardPermissions(5)
			p.DonationScope = initialScope
			p.Permissions["donations.read_all"] = true
			writeDonationPolicy(t, e, actor, p)
			current, err := e.service.GetAdmin(context.Background(), id)
			if err != nil {
				t.Fatal(err)
			}
			input := ReviewInput{Decision: "reject", ExpectedRevision: parseTestID(t, current.Revision), Reason: "Reviewed pending submission"}
			mutation := donationMutation(t, 'R', http.MethodPost, routeStewardReview, []int64{id}, input)
			saved, err := e.service.ReviewSteward(context.Background(), actor, id, mutation, input)
			wantKeys := 1
			if initialScope == authz.DonationScopeFull {
				wantKeys = 2
			}
			if err != nil || len(saved.Value.Keys) != wantKeys {
				t.Fatalf("fresh rejection lost authorized keys: %+v %v", saved, err)
			}
			replayed, err := e.service.ReviewSteward(context.Background(), actor, id, mutation, input)
			if err != nil || !replayed.Replayed || !bytes.Equal(saved.Body, replayed.Body) {
				t.Fatalf("rejection replay changed: %s != %s, %v", saved.Body, replayed.Body, err)
			}
			if bytes.Contains(saved.Body, []byte("standard_keys")) || bytes.Contains(saved.Body, []byte("donation_review_receipt")) {
				t.Fatal("internal receipt metadata exposed")
			}
			p.DonationScope = authz.DonationScopeStandard
			p.Permissions["donations.read_all"] = false
			writeDonationPolicy(t, e, actor, p)
			narrowed, err := e.service.ReviewSteward(context.Background(), actor, id, mutation, input)
			if err != nil || len(narrowed.Value.Keys) != 1 || narrowed.Value.Keys[0].ID != d.Keys[1].ID || narrowed.Value.Owner != nil || narrowed.Value.Reviewer != nil || narrowed.Value.Keys[0].EndpointKeyID != nil {
				t.Fatalf("narrowed review disclosed unrelated member/identity: %+v %v", narrowed, err)
			}
			p.DonationScope = authz.DonationScopeNone
			writeDonationPolicy(t, e, actor, p)
			if _, err := e.service.ReviewSteward(context.Background(), actor, id, mutation, input); !errors.Is(err, authz.ErrPermissionDenied) {
				t.Fatal("none replayed review", err)
			}
		})
	}
}
