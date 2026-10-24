package donation

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

func TestDonorIDFiltersAreExactCombinedAndPaginated(t *testing.T) {
	e := newDonationTestEnv(t)
	e.seedUser(t, "", nil, true)
	level := int64(6)
	steward := e.seedUser(t, "filter-steward", &level, false)
	owner := e.seedUser(t, "810000000000000001", nil, false)
	other := e.seedUser(t, "810000000000000002", nil, false)
	for i := 0; i < 12; i++ {
		createBrowseDonation(t, e, owner, byte('a'+i), "")
	}
	createBrowseDonation(t, e, other, 'z', "")
	for _, tc := range []struct {
		filter ManagementFilter
		total  string
	}{
		{ManagementFilter{UserID: fmt.Sprint(owner)}, "12"},
		{ManagementFilter{DiscordID: "810000000000000001"}, "12"},
		{ManagementFilter{UserID: fmt.Sprint(owner), DiscordID: "810000000000000001", Status: "pending", Handling: "pending", Query: "safe shared"}, "12"},
		{ManagementFilter{UserID: fmt.Sprint(owner), DiscordID: "810000000000000002"}, "0"},
		{ManagementFilter{DiscordID: "81000000000000000"}, "0"},
		{ManagementFilter{UserID: fmt.Sprint(owner), ReviewStatus: "approved"}, "0"},
	} {
		admin, err := e.service.DonationsAdminPage(context.Background(), tc.filter, pagination.Request{Page: 99, Size: 10})
		if err != nil || admin.Pagination.TotalItems != tc.total {
			t.Fatalf("filter %+v: %+v %v", tc.filter, admin, err)
		}
		stewardPage, err := e.service.DonationsStewardPage(context.Background(), steward, tc.filter, pagination.Request{Page: 99, Size: 10})
		if err != nil || stewardPage.Pagination.TotalItems != tc.total {
			t.Fatalf("steward filter %+v: %+v %v", tc.filter, stewardPage, err)
		}
		if tc.total == "12" && (admin.Pagination.Page != "2" || len(admin.Data) != 2) {
			t.Fatalf("filter applied after pagination: %+v", admin)
		}
	}
}

func TestDonorFilterHTTPRejectsInvalidAndOwnerSelectors(t *testing.T) {
	e := newDonationTestEnv(t)
	e.seedUser(t, "", nil, true)
	owner := e.seedUser(t, "820000000000000001", nil, false)
	api := &httpAPI{service: e.service}
	for _, query := range []string{
		"user_id=", "user_id=01", "user_id=-1", "user_id=9223372036854775808", "user_id=1&user_id=2",
		"discord_id=", "discord_id=abc", "discord_id=01", "discord_id=1&discord_id=2", "discord_id=123456789012345678901",
	} {
		r := httptest.NewRequest(http.MethodGet, "/donations?page=1&page_size=10&"+query, nil)
		w := httptest.NewRecorder()
		api.listRole(w, r, UserPrincipal{}, reviewerAdmin)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("query %s: %d %s", query, w.Code, w.Body.String())
		}
	}
	for _, query := range []string{"user_id=1", "discord_id=820000000000000001"} {
		r := httptest.NewRequest(http.MethodGet, "/donations?page=1&page_size=10&"+query, nil)
		w := httptest.NewRecorder()
		api.listOwner(w, r, UserPrincipal{UserID: owner})
		if w.Code != http.StatusBadRequest {
			t.Fatalf("owner query %s: %d", query, w.Code)
		}
	}
}

func TestOwnerSummaryPendingLifecycleCanHaveApprovedLiveKey(t *testing.T) {
	e := newDonationTestEnv(t)
	e.seedUser(t, "", nil, true)
	owner := e.seedUser(t, "summary-projection-owner", nil, false)
	_, key := e.seedEndpointKey(t, owner, 'm')
	donation := e.createDonation(t, owner, key)
	id := parseTestID(t, donation.ID)
	uses := ownerUsesForTest(t, e, owner, id)
	if len(uses) < 2 {
		t.Fatal("requires separate service uses")
	}
	items := []UseReviewItem{{UseID: parseTestID(t, uses[0].ID), ExpectedRevision: parseTestID(t, uses[0].Revision), Decision: "approve", ReviewNote: "accepted"}}
	result, err := e.service.reviewUses(context.Background(), reviewerAdmin, 0, id, items, donationMutation(t, 'v', http.MethodPost, routeAdminUses+"/review", []int64{id}, items))
	if err != nil || !result.Value.Results[0].OK {
		t.Fatalf("review: %+v %v", result, err)
	}
	page, err := e.service.DonationsOwnerPage(context.Background(), owner, ManagementFilter{OwnerView: "current"}, pagination.Default())
	if err != nil || len(page.Data) != 1 {
		t.Fatalf("page: %+v %v", page, err)
	}
	row := page.Data[0]
	if row.Status != "pending" || row.ReviewResult != nil || row.UseSummary.Approved != "1" || row.UseSummary.ReviewStatus != "approved_pending" || row.StateCounts["available"] != "1" || row.StateCounts["pending"] != "0" {
		t.Fatalf("projection changed: %+v", row)
	}
}
