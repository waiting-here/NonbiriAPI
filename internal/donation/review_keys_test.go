package donation

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

func TestDefaultStewardCommonReviewReadsPendingKeysAndUsesWithoutMaintenanceScope(t *testing.T) {
	e := newDonationTestEnv(t)
	e.seedUser(t, "", nil, true)
	five := int64(5)
	reviewer := e.seedUser(t, "common-reviewer", &five, false)
	owner := e.seedUser(t, "common-owner", nil, false)
	_, physical := e.seedEndpointKey(t, owner, 'r')
	donation := e.createDonation(t, owner, physical)
	id := parseTestID(t, donation.ID)
	uses := ownerUsesForTest(t, e, owner, id)
	item := []UseReviewItem{{UseID: parseTestID(t, uses[0].ID), ExpectedRevision: parseTestID(t, uses[0].Revision), Decision: "approve", ReviewNote: "accepted"}}
	if _, err := e.service.reviewUses(context.Background(), reviewerAdmin, 0, id, item, donationMutation(t, 'c', http.MethodPost, routeAdminUses+"/review", []int64{id}, item)); err != nil {
		t.Fatal(err)
	}
	detail, err := e.service.GetSteward(context.Background(), reviewer, id)
	if err != nil {
		t.Fatal(err)
	}
	api := &httpAPI{service: e.service}
	r := httptest.NewRequest(http.MethodGet, "/api/steward/donations/"+donation.ID+"/keys?page=1&page_size=10", nil)
	r.SetPathValue("id", donation.ID)
	w := httptest.NewRecorder()
	api.keysSteward(w, r, UserPrincipal{UserID: reviewer})
	if w.Code != http.StatusOK {
		t.Fatalf("common key page: %d %s", w.Code, w.Body.String())
	}
	var keys Page[ManagedKeySummary]
	if err := json.Unmarshal(w.Body.Bytes(), &keys); err != nil {
		t.Fatal(err)
	}
	if len(keys.Data) != len(detail.Keys) || len(keys.Data) == 0 {
		t.Fatalf("key subset differs: %+v %+v", keys, detail)
	}
	for index, key := range keys.Data {
		if key.ID != detail.Keys[index].ID || key.EndpointKeyID != nil || key.CanManage || !key.MaintenanceScopeRequired || len(key.VisibleModels) != 0 {
			t.Fatalf("review key scope: %+v", key)
		}
	}
	r = httptest.NewRequest(http.MethodGet, "/api/steward/donations/"+donation.ID+"/uses?page=1&page_size=10", nil)
	r.SetPathValue("id", donation.ID)
	w = httptest.NewRecorder()
	api.usesPageHandler(reviewerSteward)(w, r, UserPrincipal{UserID: reviewer})
	if w.Code != http.StatusOK {
		t.Fatalf("common use page: %d %s", w.Code, w.Body.String())
	}
	var pending Page[Use]
	if err := json.Unmarshal(w.Body.Bytes(), &pending); err != nil {
		t.Fatal(err)
	}
	if len(pending.Data) != len(uses)-1 {
		t.Fatalf("approved uses leaked into default review: %+v", pending)
	}
	if _, err := e.service.KeyStewardSession(context.Background(), reviewer, id, parseTestID(t, keys.Data[0].ID)); !errors.Is(err, authz.ErrPermissionDenied) {
		t.Fatalf("review read granted maintenance: %v", err)
	}
	page, err := e.service.DonationsStewardPage(context.Background(), reviewer, ManagementFilter{}, pagination.Default())
	if err != nil || len(page.Data) != 1 || page.Data[0].Owner != nil {
		t.Fatalf("common queue: %+v %v", page, err)
	}
}
