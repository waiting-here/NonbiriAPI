package donation

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/connectionstore"
	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

func stageUseForTest(t *testing.T, e *donationTestEnv, use Use) connectionstore.UseRevisionIdentity {
	t.Helper()
	ctx := context.Background()
	tx, err := e.store.DB().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var grant int64
	if err := tx.QueryRowContext(ctx, `SELECT k.current_grant_revision_id FROM donation_uses u JOIN donation_keys dk ON dk.id=u.donation_key_id JOIN endpoint_keys k ON k.id=dk.endpoint_key_id WHERE u.id=?`, use.ID).Scan(&grant); err != nil {
		t.Fatal(err)
	}
	result, err := connectionstore.StagePendingUseRevisionTx(ctx, tx, connectionstore.StagePendingUseRevisionInput{UseID: parseTestID(t, use.ID), ExpectedRevision: parseTestID(t, use.Revision), GrantRevisionID: grant, Origin: connectionstore.UseOriginReattachment, At: e.clock.Load()})
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return result
}

func ownerUsesForTest(t *testing.T, e *donationTestEnv, owner, donationID int64) []Use {
	t.Helper()
	page, err := e.service.UsesPage(context.Background(), recurringOwner, owner, donationID, UseFilter{}, pagination.Request{Page: 1, Size: 100})
	if err != nil {
		t.Fatal(err)
	}
	return page.Data
}

func TestConnectionUsesPartialReviewPreservesHeaderAndBudgetMember(t *testing.T) {
	e := newDonationTestEnv(t)
	e.seedUser(t, "", nil, true)
	owner := e.seedUser(t, "partial-use-owner", nil, false)
	_, key := e.seedEndpointKey(t, owner, 'u')
	donation := e.createDonation(t, owner, key)
	id := parseTestID(t, donation.ID)
	uses := ownerUsesForTest(t, e, owner, id)
	if len(uses) < 2 {
		t.Fatal("fixture must contain two separately reviewable services")
	}
	items := []UseReviewItem{
		{UseID: parseTestID(t, uses[0].ID), ExpectedRevision: parseTestID(t, uses[0].Revision), Decision: "approve", ReviewNote: "service approved"},
		{UseID: parseTestID(t, uses[1].ID), ExpectedRevision: parseTestID(t, uses[1].Revision) + 1, Decision: "reject", ReviewNote: "stale selection"},
	}
	mutation := donationMutation(t, 'V', http.MethodPost, routeAdminUses+"/review", []int64{id}, items)
	result, err := e.service.reviewUses(context.Background(), reviewerAdmin, 0, id, items, mutation)
	if err != nil || len(result.Value.Results) != 2 || !result.Value.Results[0].OK || result.Value.Results[1].OK || result.Value.Results[1].Error == nil {
		t.Fatalf("independent review results: %+v, %v", result, err)
	}
	replayed, err := e.service.reviewUses(context.Background(), reviewerAdmin, 0, id, items, mutation)
	if err != nil || !replayed.Replayed || string(replayed.Body) != string(result.Body) {
		t.Fatalf("review replay: %+v, %v", replayed, err)
	}
	view, err := e.service.GetOwner(context.Background(), owner, id)
	if err != nil || view.Status != "pending" || view.UseSummary.Approved != "1" || view.UseSummary.Pending != fmt.Sprint(len(uses)-1) || len(view.Keys) != 1 {
		t.Fatalf("partial approval changed header/member: %+v, %v", view, err)
	}
	var memberships, members int
	if err := e.store.DB().QueryRow(`SELECT (SELECT count(*) FROM donation_key_memberships WHERE donation_id=?),(SELECT count(*) FROM donation_keys WHERE donation_id=?)`, id, id).Scan(&memberships, &members); err != nil || memberships != 1 || members != 1 {
		t.Fatalf("service approval duplicated budget member: %d/%d, %v", memberships, members, err)
	}
}

func TestConnectionUsesTraineeSeesOnlyPendingMemberOnApprovedHeader(t *testing.T) {
	e := newDonationTestEnv(t)
	e.seedUser(t, "", nil, true)
	five := int64(5)
	actor := e.seedUser(t, "use-review-trainee", &five, false)
	owner := e.seedUser(t, "use-review-owner", nil, false)
	_, first := e.seedEndpointKey(t, owner, 'x')
	_, second := e.seedEndpointKey(t, owner, 'y')
	donation := approvedResetDonation(t, e, owner, first, second)
	id := parseTestID(t, donation.ID)
	uses := ownerUsesForTest(t, e, owner, id)
	selected := uses[0]
	stageUseForTest(t, e, selected)
	queue, err := e.service.DonationsStewardPage(context.Background(), actor, ManagementFilter{Status: "pending"}, pagination.Default())
	if err != nil || len(queue.Data) != 1 || queue.Data[0].Status != "approved" || queue.Data[0].UseSummary.Pending != "1" || queue.Data[0].KeyCount != "1" || queue.Data[0].SourceCount != "1" {
		t.Fatalf("pending-use queue: %+v, %v", queue, err)
	}
	view, err := e.service.GetSteward(context.Background(), actor, id)
	if err != nil || len(view.Keys) != 1 || view.Keys[0].ID != selected.DonationKeyID || view.Keys[0].EndpointKeyID != nil || view.Status != "approved" {
		t.Fatalf("pending member detail: %+v, %v", view, err)
	}
	page, err := e.service.UsesPage(context.Background(), reviewerSteward, actor, id, UseFilter{}, pagination.Default())
	if err != nil || len(page.Data) != 1 || page.Data[0].ID != selected.ID || page.Data[0].Approved == nil || page.Data[0].Pending == nil || page.Data[0].EndpointKeyID != nil {
		t.Fatalf("same-use accurate material: %+v, %v", page, err)
	}
}

func TestConnectionUsesRejectPendingKeepsApprovedAuthorization(t *testing.T) {
	e := newDonationTestEnv(t)
	e.seedUser(t, "", nil, true)
	owner := e.seedUser(t, "retained-use-owner", nil, false)
	_, key := e.seedEndpointKey(t, owner, 'r')
	donation := approvedResetDonation(t, e, owner, key)
	id := parseTestID(t, donation.ID)
	selected := ownerUsesForTest(t, e, owner, id)[0]
	staged := stageUseForTest(t, e, selected)
	item := UseReviewItem{UseID: staged.UseID, ExpectedRevision: staged.UseRevision, Decision: "reject", ReviewNote: "retain the approved material"}
	result, err := e.service.reviewUses(context.Background(), reviewerAdmin, 0, id, []UseReviewItem{item}, donationMutation(t, 'Z', http.MethodPost, routeAdminUses+"/review", []int64{id}, item))
	if err != nil || len(result.Value.Results) != 1 || !result.Value.Results[0].OK {
		t.Fatalf("reject pending: %+v, %v", result, err)
	}
	current := ownerUsesForTest(t, e, owner, id)[0]
	if current.Approved == nil || current.Pending != nil || current.Latest == nil || current.Latest.Status != "rejected" || current.Approved.CredentialVersion != selected.Approved.CredentialVersion {
		t.Fatalf("pending decision replaced approved material: %+v", current)
	}
	tx, err := e.store.DB().BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := connectionstore.LoadApprovedUseTx(context.Background(), tx, parseTestID(t, selected.DonationKeyID), selected.NativeService); err != nil {
		t.Fatalf("approved service no longer usable: %v", err)
	}
}
