package donation

import (
	"context"
	"database/sql"
	"net/http"
	"testing"

	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
)

func TestOwnerProtocolResubmissionKeepsOldApprovalPauseAndBudget(t *testing.T) {
	f := newAppendUseFixture(t)
	before := appendRows(t, f.e, "SELECT * FROM donation_keys WHERE id=?", f.member)
	if _, err := f.e.store.DB().Exec("UPDATE donation_uses SET enabled=0,discovery_enabled=0,revision=revision+1 WHERE donation_key_id=?", f.member); err != nil {
		t.Fatal(err)
	}
	old := ownerUsesForTest(t, f.e, f.owner, f.donation)
	full, _ := contract.SupportedNativeServices(contract.TypeOpenAICompatible)
	in := f.input
	in.Items = nil
	for _, service := range full.Services() {
		item := AppendUseItem{NativeService: service}
		for _, use := range old {
			if use.NativeService == service {
				revision := parseTestID(t, use.Revision)
				item.ExpectedRevision = &revision
			}
		}
		in.Items = append(in.Items, item)
	}
	callbacks := 0
	f.e.service.pendingSubmission = func(ctx context.Context, tx *sql.Tx, id int64) error {
		callbacks++
		var pending int
		if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM donation_uses WHERE donation_key_id=? AND current_pending_revision_id IS NOT NULL", f.member).Scan(&pending); err != nil {
			return err
		}
		if pending != len(full.Services()) || id != f.donation {
			t.Fatalf("submission callback ran before pending content: %d", pending)
		}
		return nil
	}
	result, err := f.append(t, 'P', in)
	if err != nil {
		t.Fatal(err)
	}
	current := ownerUsesForTest(t, f.e, f.owner, f.donation)
	if len(current) != len(full.Services()) {
		t.Fatalf("full protocol not submitted: %d", len(current))
	}
	for _, use := range current {
		if use.Pending == nil || use.Enabled || use.DiscoveryEnabled {
			t.Fatalf("resubmission changed pause state: %+v", use)
		}
		for _, prior := range old {
			if prior.ID == use.ID && (use.Approved == nil || use.Approved.GrantRevision != prior.Approved.GrantRevision || use.Approved.CredentialVersion != prior.Approved.CredentialVersion) {
				t.Fatal("historical approval replaced")
			}
		}
	}
	if after := appendRows(t, f.e, "SELECT * FROM donation_keys WHERE id=?", f.member); after != before {
		t.Fatal("resubmission changed key budget")
	}
	if replay, err := f.append(t, 'P', in); err != nil || !replay.Replayed || string(replay.Body) != string(result.Body) || callbacks != 1 {
		t.Fatalf("resubmission replay/callback: %v calls=%d", err, callbacks)
	}
}

func TestOwnerEditProtocolResubmitsWithoutExpandingOldApproval(t *testing.T) {
	e := newDonationTestEnv(t)
	e.seedUser(t, "", nil, true)
	owner := e.seedUser(t, "protocol-edit-owner", nil, false)
	_, key := e.seedEndpointKey(t, owner, 'p')
	donation := e.createDonation(t, owner, key)
	id := parseTestID(t, donation.ID)
	first := ownerUsesForTest(t, e, owner, id)[0]
	item := UseReviewItem{UseID: parseTestID(t, first.ID), ExpectedRevision: parseTestID(t, first.Revision), Decision: "approve"}
	if result, err := e.service.reviewUses(context.Background(), reviewerAdmin, 0, id, []UseReviewItem{item}, donationMutation(t, 'C', http.MethodPost, routeAdminUses+"/review", []int64{id}, item)); err != nil || !result.Value.Results[0].OK {
		t.Fatalf("approve old content: %v", err)
	}
	prior := ownerUsesForTest(t, e, owner, id)[0].Approved
	input := EditInput{Description: "Updated sharing", ExpectedRevision: parseTestID(t, donation.Revision)}
	result, err := e.service.Edit(context.Background(), owner, id, donationMutation(t, 'E', http.MethodPatch, routeDonation, []int64{id}, input), input)
	if err != nil {
		t.Fatal(err)
	}
	if result.Value.UseSummary.Pending != "15" || result.Value.UseSummary.Approved != "1" {
		t.Fatalf("edit did not create complete pending content: %+v", result.Value.UseSummary)
	}
	for _, use := range ownerUsesForTest(t, e, owner, id) {
		if use.ID == first.ID && (use.Approved == nil || use.Approved.GrantRevision != prior.GrantRevision) {
			t.Fatal("old approved grant expanded")
		}
	}
}
