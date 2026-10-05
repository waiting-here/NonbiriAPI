package donation

import (
	"context"
	"net/http"
	"testing"
)

func TestDirectRejectionHasNoFirstApproval(t *testing.T) {
	e := newDonationTestEnv(t)
	level := int64(6)
	owner := e.seedUser(t, "pending-donor", &level, false)
	e.seedUser(t, "", nil, true)
	_, key := e.seedEndpointKey(t, owner, 'n')
	donation := e.createDonation(t, owner, key)
	id := parseTestID(t, donation.ID)
	ctx := context.Background()
	pending, err := e.service.GetAdmin(ctx, id)
	if err != nil || pending.FirstApprovalOrigin != "none" {
		t.Fatalf("pending=%+v err=%v", pending, err)
	}
	review := ReviewInput{Decision: "reject", ExpectedRevision: 1, Reason: "Unsupported source"}
	result, err := e.service.ReviewAdmin(ctx, donationMutation(t, 'R', http.MethodPost, routeAdminReview, []int64{id}, review), id, review)
	if err != nil {
		t.Fatal(err)
	}
	if result.Value.FirstApprovalOrigin != "none" || result.Value.CanForceReject {
		t.Fatalf("rejection=%+v", result.Value)
	}
	admin, err := e.service.GetAdmin(ctx, id)
	if err != nil || admin.FirstApprovalOrigin != "none" {
		t.Fatalf("admin origin=%s err=%v", admin.FirstApprovalOrigin, err)
	}
	steward, err := e.service.GetSteward(ctx, owner, id)
	if err != nil || steward.FirstApprovalOrigin != "none" {
		t.Fatalf("steward origin=%s err=%v", steward.FirstApprovalOrigin, err)
	}
}
