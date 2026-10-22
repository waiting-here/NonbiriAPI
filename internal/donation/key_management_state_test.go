package donation

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

func TestConnectionUsesPendingHeaderKeyManagementRequiresThatKeysApproval(t *testing.T) {
	e := newDonationTestEnv(t)
	e.seedUser(t, "", nil, true)
	owner := e.seedUser(t, "partial-key-owner", nil, false)
	endpoint, first := e.seedEndpointKey(t, owner, 'a')
	_, second := e.seedEndpointKey(t, owner, 'b')
	d := e.createDonation(t, owner, first, second)
	id := parseTestID(t, d.ID)
	keyID := parseTestID(t, d.Keys[0].ID)
	ctx := context.Background()
	uses, err := e.service.UsesPage(ctx, recurringOwner, owner, id, UseFilter{DonationKeyID: keyID}, pagination.Default())
	if err != nil || len(uses.Data) == 0 {
		t.Fatalf("member use filter: %+v, %v", uses, err)
	}
	for _, use := range uses.Data {
		if use.DonationKeyID != d.Keys[0].ID {
			t.Fatal("member use filter returned another budget member", use)
		}
	}
	use := uses.Data[0]
	items := []UseReviewItem{{UseID: parseTestID(t, use.ID), ExpectedRevision: parseTestID(t, use.Revision), Decision: "approve", ReviewNote: "approved service"}}
	result, err := e.service.reviewUses(ctx, reviewerAdmin, 0, id, items, donationMutation(t, 'R', http.MethodPost, routeAdminUses+"/review", []int64{id}, items))
	if err != nil || !result.Value.Results[0].OK {
		t.Fatalf("approve selected service: %+v, %v", result, err)
	}
	if _, err := e.store.DB().Exec(`UPDATE donation_uses SET enabled=0,revision=revision+1 WHERE id=?`, use.ID); err != nil {
		t.Fatal(err)
	}
	view, err := e.service.GetAdmin(ctx, id)
	if err != nil || view.Status != "pending" || !view.Keys[0].CanManage || view.Keys[1].CanManage {
		t.Fatalf("per-key maintenance projection: %+v, %v", view, err)
	}
	ownerView, err := e.service.GetOwner(ctx, owner, id)
	if err != nil || ownerView.Keys[0].EndpointID == nil || parseTestID(t, *ownerView.Keys[0].EndpointID) != endpoint {
		t.Fatalf("owner endpoint: %+v, %v", ownerView, err)
	}
	for _, projection := range []any{view.Keys[0], managedKeySummary(view.Keys[0], view, RecurringSummary{})} {
		data, err := json.Marshal(projection)
		if err != nil {
			t.Fatal(err)
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(data, &fields); err != nil {
			t.Fatal(err)
		}
		if _, exists := fields["endpoint_id"]; exists {
			t.Fatal("owner-only endpoint leaked through role key projection", string(data))
		}
	}
	rpm := int64(20)
	input := KeyManagementInput{ExpectedRevision: parseTestID(t, view.Revision), ExpectedKeyLimitsRevision: 1, MaxRPM: &rpm}
	otherID := parseTestID(t, d.Keys[1].ID)
	if _, err := e.service.ManageKeyAdmin(ctx, id, otherID, donationMutation(t, 'S', http.MethodPatch, routeAdminKey, []int64{id, otherID}, input), input); !errors.Is(err, ErrConflict) {
		t.Fatal("another member borrowed approved use", err)
	}
	updated, err := e.service.ManageKeyAdmin(ctx, id, keyID, donationMutation(t, 'T', http.MethodPatch, routeAdminKey, []int64{id, keyID}, input), input)
	if err != nil || updated.Value.Status != "pending" || updated.Value.Keys[0].MaxRPM == nil || *updated.Value.Keys[0].MaxRPM != rpm {
		t.Fatalf("manage paused approved service: %+v, %v", updated, err)
	}
	var approved, pending int
	if err := e.store.DB().QueryRow(`SELECT COUNT(current_approved_revision_id),COUNT(current_pending_revision_id) FROM donation_uses WHERE donation_key_id=?`, keyID).Scan(&approved, &pending); err != nil || approved != 1 || pending != len(uses.Data)-1 {
		t.Fatalf("settings changed service approval: %d/%d, %v", approved, pending, err)
	}
}
