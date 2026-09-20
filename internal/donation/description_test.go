package donation

import (
	"context"
	"errors"
	"testing"
)

func TestRequiredDescriptionAtAllWriteBoundariesPreservesLegacyReview(t *testing.T) {
	env := newDonationTestEnv(t)
	owner := env.seedUser(t, "description-owner", nil, false)
	env.seedUser(t, "", nil, true)
	_, key := env.seedEndpointKey(t, owner, 'v')
	ctx := context.Background()
	for _, value := range []string{"", " ", "\u3000", "\u00a0"} {
		input := CreateInput{Description: value, OwnershipAuthorized: true, Keys: []CreateKeyInput{{EndpointKeyID: key}}}
		mutation := donationMutation(t, 'b', "POST", routeDonations, nil, map[string]any{"description": value})
		if _, err := env.service.Create(ctx, owner, mutation, input); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("create %q: %v", value, err)
		}
		tx, err := env.store.DB().BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		_, err = env.service.CreateInTransaction(ctx, tx, owner, input)
		tx.Rollback()
		if !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("transactional create %q: %v", value, err)
		}
	}
	created := env.createDonation(t, owner, key)
	id := parseTestID(t, created.ID)
	if _, err := env.store.DB().Exec(`UPDATE donations SET description='' WHERE id=?`, id); err != nil {
		t.Fatal(err)
	}
	old, err := env.service.GetOwner(ctx, owner, id)
	if err != nil || old.Description != "" {
		t.Fatalf("legacy read: %+v %v", old, err)
	}
	if _, err = env.service.Edit(ctx, owner, id, donationMutation(t, 'c', "PATCH", routeDonation, []int64{id}, map[string]any{"description": " "}), EditInput{Description: " ", ExpectedRevision: 1}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("edit blank: %v", err)
	}
	// An unchanged legacy description and an optional empty review reason do
	// not prevent approval. The new requirement only guards description writes.
	approved, err := env.service.ReviewAdmin(ctx, donationMutation(t, 'd', "POST", routeAdminReview, []int64{id}, map[string]any{"approve": true}), id, ReviewInput{Decision: "approve", ExpectedRevision: 1, Reason: "", KeySettings: []KeySetting{{DonationKeyID: parseTestID(t, created.Keys[0].ID), Enabled: true}}})
	if err != nil || approved.Value.Description != "" {
		t.Fatalf("legacy approval: %+v %v", approved, err)
	}
}
