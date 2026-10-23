package donation

import (
	"context"
	"fmt"
	"net/http"
	"reflect"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

func TestUseReviewSummaryAcrossReadersAndFilters(t *testing.T) {
	for _, mode := range []string{"pending", "rejected", "approved", "partially_approved", "approved_pending", "withdrawn", "mixed"} {
		t.Run(mode, func(t *testing.T) {
			e := newDonationTestEnv(t)
			e.seedUser(t, "", nil, true)
			level := int64(6)
			reviewer := e.seedUser(t, "summary-reviewer", &level, false)
			owner := e.seedUser(t, "summary-owner", nil, false)
			_, key := e.seedEndpointKey(t, owner, 'u')
			donation := e.createDonation(t, owner, key)
			id := parseTestID(t, donation.ID)
			uses := ownerUsesForTest(t, e, owner, id)
			if len(uses) < 2 {
				t.Fatal("fixture needs multiple services")
			}
			ctx := context.Background()
			items := []UseReviewItem{}
			for index, use := range uses {
				if mode == "withdrawn" || mode == "mixed" && index > 0 {
					input := UsePatch{ExpectedRevision: parseTestID(t, use.Revision)}
					_, err := e.service.mutateUse(ctx, recurringOwner, owner, id, parseTestID(t, use.ID), input, "cancel", donationMutation(t, byte('a'+index), http.MethodDelete, routeOwnerUse+"/pending", []int64{id, parseTestID(t, use.ID)}, input))
					if err != nil {
						t.Fatal(err)
					}
					continue
				}

				if mode == "pending" || mode == "approved_pending" && index > 0 {
					continue
				}
				decision := "approve"
				if mode == "rejected" || mode == "mixed" || mode == "partially_approved" && index > 0 {
					decision = "reject"
				}
				items = append(items, UseReviewItem{UseID: parseTestID(t, use.ID), ExpectedRevision: parseTestID(t, use.Revision), Decision: decision, ReviewNote: "reviewed"})
			}
			if len(items) > 0 {
				receipt, err := e.service.reviewUses(ctx, reviewerAdmin, 0, id, items, donationMutation(t, 'S', http.MethodPost, routeAdminUses+"/review", []int64{id}, items))
				if err != nil {
					t.Fatal(err)
				}
				for _, result := range receipt.Value.Results {
					if !result.OK {
						t.Fatalf("review failed: %+v", result)
					}
				}
			}
			ownerDetail, err := e.service.GetOwner(ctx, owner, id)
			if err != nil || ownerDetail.Status != "pending" || ownerDetail.UseSummary.ReviewStatus != mode {
				t.Fatalf("summary changed lifecycle or misclassified reviews: %+v %v", ownerDetail, err)
			}
			adminDetail, err := e.service.GetAdmin(ctx, id)
			if err != nil || !reflect.DeepEqual(adminDetail.UseSummary, ownerDetail.UseSummary) {
				t.Fatalf("admin detail: %+v %v", adminDetail, err)
			}
			stewardDetail, err := e.service.GetSteward(ctx, reviewer, id)
			if err != nil || !reflect.DeepEqual(stewardDetail.UseSummary, ownerDetail.UseSummary) {
				t.Fatalf("steward detail: %+v %v", stewardDetail, err)
			}
			for _, filter := range []ManagementFilter{{}, {ReviewStatus: mode}, {Status: "pending", ReviewStatus: mode}} {
				ownerPage, err := e.service.DonationsOwnerPage(ctx, owner, filter, pagination.Default())
				if err != nil || len(ownerPage.Data) != 1 || !reflect.DeepEqual(ownerPage.Data[0].UseSummary, ownerDetail.UseSummary) {
					t.Fatalf("owner page: %+v %v", ownerPage, err)
				}
				adminPage, err := e.service.DonationsAdminPage(ctx, filter, pagination.Default())
				if err != nil || len(adminPage.Data) != 1 || !reflect.DeepEqual(adminPage.Data[0].UseSummary, ownerDetail.UseSummary) {
					t.Fatalf("admin page: %+v %v", adminPage, err)
				}
				stewardPage, err := e.service.DonationsStewardPage(ctx, reviewer, filter, pagination.Default())
				if err != nil || len(stewardPage.Data) != 1 || !reflect.DeepEqual(stewardPage.Data[0].UseSummary, ownerDetail.UseSummary) {
					t.Fatalf("steward page: %+v %v", stewardPage, err)
				}
			}
			if mode == "rejected" {
				if ownerDetail.UseSummary.Rejected != fmt.Sprint(len(uses)) || ownerDetail.UseSummary.Pending != "0" {
					t.Fatalf("rejection counts: %+v", ownerDetail.UseSummary)
				}
				pending, err := e.service.DonationsAdminPage(ctx, ManagementFilter{ReviewStatus: "pending"}, pagination.Default())
				if err != nil || len(pending.Data) != 0 {
					t.Fatalf("rejected uses remain pending: %+v %v", pending, err)
				}
			}
		})
	}
}
