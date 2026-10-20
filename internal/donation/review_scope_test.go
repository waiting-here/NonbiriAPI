package donation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
	"github.com/waiting-here/NonbiriAPI/internal/pagination"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

func TestTraineeReviewsAllPendingSourcesWithPrivateReceipts(t *testing.T) {
	e := newDonationTestEnv(t)
	ctx := context.Background()
	e.seedUser(t, "", nil, true)
	five := int64(5)
	actor := e.seedUser(t, "review-trainee", &five, false)
	owner := e.seedUser(t, "private-review-donor", nil, false)
	var pending []Donation
	for i := range 11 {
		_, key := e.seedEndpointKey(t, owner, byte('a'+i))
		pending = append(pending, e.createDonationWithSeed(t, owner, byte('a'+i), key))
	}
	_, physical := e.seedEndpointKey(t, owner, 'z')
	approved := approvedResetDonation(t, e, owner, physical)
	page, err := e.service.DonationsStewardPage(ctx, actor, ManagementFilter{}, pagination.Request{Page: 1, Size: 10})
	if err != nil || page.Pagination.TotalItems != "11" || len(page.Data) != 10 {
		t.Fatalf("pending queue: %+v %v", page, err)
	}
	last, err := e.service.DonationsStewardPage(ctx, actor, ManagementFilter{Status: "pending"}, pagination.Request{Page: 2, Size: 10})
	if err != nil || len(last.Data) != 1 || last.Data[0].ID != pending[10].ID {
		t.Fatalf("pending final page: %+v %v", last, err)
	}
	for _, status := range []string{"approved", "rejected", "expired", "deleted"} {
		if _, err := e.service.DonationsStewardPage(ctx, actor, ManagementFilter{Status: status}, pagination.Default()); !errors.Is(err, ErrForbidden) {
			t.Fatalf("non-pending status %s: %v", status, err)
		}
	}
	id := parseTestID(t, pending[0].ID)
	view, err := e.service.GetSteward(ctx, actor, id)
	if err != nil || len(view.Keys) != 1 || view.Keys[0].EndpointKeyID != nil || view.Owner != nil || view.Reviewer != nil || view.CanForceReject {
		t.Fatalf("pending view: %+v %v", view, err)
	}
	for _, value := range []any{page, last, view} {
		body, _ := json.Marshal(value)
		if strings.Contains(string(body), "private-review-donor") {
			t.Fatal("pending review exposed donor identity")
		}
	}
	legacy, _, err := e.service.ListSteward(ctx, actor, "", 0, 100)
	if err != nil || len(legacy) != 11 || legacy[0].Owner != nil || legacy[0].Keys[0].EndpointKeyID != nil {
		t.Fatalf("cursor queue: %+v %v", legacy, err)
	}
	if _, err := e.service.GetSteward(ctx, actor, parseTestID(t, approved.ID)); !errors.Is(err, ErrForbidden) {
		t.Fatal("approved full detail was exposed", err)
	}
	input := ReviewInput{Decision: "approve", ExpectedRevision: 1, KeySettings: []KeySetting{{DonationKeyID: parseTestID(t, pending[0].Keys[0].ID), Enabled: true}}}
	mutation := donationMutation(t, 'A', http.MethodPost, routeStewardReview, []int64{id}, input)
	result, err := e.service.ReviewSteward(ctx, actor, id, mutation, input)
	if err != nil || result.Value.Status != "approved" || result.Value.Owner != nil || result.Value.Reviewer != nil || result.Value.CanForceReject || result.Value.Keys[0].EndpointKeyID != nil {
		t.Fatalf("approval receipt: %+v %v", result, err)
	}
	var auditRole string
	if err := e.store.DB().QueryRow(`SELECT reviewer_role FROM donation_reviews WHERE donation_id=? AND action='approve'`, id).Scan(&auditRole); err != nil || auditRole != "trainee5" {
		t.Fatal("review actor", auditRole, err)
	}
	if n := mutationRecordCount(t, e.store.DB(), idempotency.ScopeControlMutation, "trainee5", actor, mutation.IdempotencyKey); n != 1 {
		t.Fatal("missing trainee mutation namespace", n)
	}
	full, err := e.service.GetAdmin(ctx, id)
	if err != nil || full.Reviewer == nil || full.Reviewer.Role != "steward" || full.Reviewer.UserID == nil || *full.Reviewer.UserID != fmt.Sprint(actor) {
		t.Fatalf("public reviewer projection: %+v %v", full.Reviewer, err)
	}
	replayed, err := e.service.ReviewSteward(ctx, actor, id, mutation, input)
	if err != nil || !replayed.Replayed || string(replayed.Body) != string(result.Body) {
		t.Fatal("completed approval could not replay", err)
	}
	input.ExpectedRevision = 2
	if _, err := e.service.ReviewSteward(ctx, actor, id, donationMutation(t, 'B', http.MethodPost, routeStewardReview, []int64{id}, input), input); !errors.Is(err, ErrForbidden) {
		t.Fatal("trainee updated approved limits", err)
	}
	input = ReviewInput{Decision: "force_reject", ExpectedRevision: 2, Reason: "Not allowed"}
	if _, err := e.service.ReviewSteward(ctx, actor, id, donationMutation(t, 'C', http.MethodPost, routeStewardReview, []int64{id}, input), input); !errors.Is(err, ErrForbidden) {
		t.Fatal("trainee force rejected approval", err)
	}
	rejectID := parseTestID(t, pending[1].ID)
	reject := ReviewInput{Decision: "reject", ExpectedRevision: 1, Reason: "Not suitable"}
	rejected, err := e.service.ReviewSteward(ctx, actor, rejectID, donationMutation(t, 'D', http.MethodPost, routeStewardReview, []int64{rejectID}, reject), reject)
	if err != nil || rejected.Value.Status != "rejected" || rejected.Value.Owner != nil {
		t.Fatalf("rejection receipt %+v %v", rejected, err)
	}
	e.auth.denySession.Store(true)
	input = ReviewInput{Decision: "approve", ExpectedRevision: 1, KeySettings: []KeySetting{{DonationKeyID: parseTestID(t, pending[0].Keys[0].ID), Enabled: true}}}
	if _, err := e.service.ReviewSteward(ctx, actor, id, mutation, input); err == nil {
		t.Fatal("revoked session replay accepted")
	}
	e.auth.denySession.Store(false)
	if _, err := e.store.DB().Exec(`UPDATE users SET level=4 WHERE id=?`, actor); err != nil {
		t.Fatal(err)
	}
	if _, err := e.service.ReviewSteward(ctx, actor, id, mutation, input); !errors.Is(err, ErrForbidden) {
		t.Fatal("demoted reviewer replay accepted", err)
	}
}

func TestForceRejectEveryApprovalOrigin(t *testing.T) {
	for _, origin := range []string{"admin", "level6", "trainee5", "auto", "unknown"} {
		for _, forceActor := range []string{"admin", "level6"} {
			t.Run(origin+"/"+forceActor, func(t *testing.T) {
				e := newDonationTestEnv(t)
				ctx := context.Background()
				e.seedUser(t, "", nil, true)
				five, six := int64(5), int64(6)
				trainee := e.seedUser(t, "origin-trainee", &five, false)
				steward := e.seedUser(t, "origin-steward", &six, false)
				owner := e.seedUser(t, "origin-owner", nil, false)
				var d Donation
				if origin == "auto" {
					channel := seedMainstreamChannel(t, e, "Origin channel", "subscription")
					d = sourcedDonation(t, e, owner, 'a', channel, "origin body")
				} else {
					_, physical := e.seedEndpointKey(t, owner, 'a')
					d = e.createDonationWithSeed(t, owner, 'a', physical)
					id := parseTestID(t, d.ID)
					input := ReviewInput{Decision: "approve", ExpectedRevision: 1, KeySettings: []KeySetting{{DonationKeyID: parseTestID(t, d.Keys[0].ID), Enabled: true}}}
					var err error
					if origin == "level6" || origin == "trainee5" {
						actor := steward
						if origin == "trainee5" {
							actor = trainee
						}
						_, err = e.service.ReviewSteward(ctx, actor, id, donationMutation(t, 'b', http.MethodPost, routeStewardReview, []int64{id}, input), input)
					} else {
						_, err = e.service.ReviewAdmin(ctx, reviewMutation(t, 'b', id), id, input)
					}
					if err != nil {
						t.Fatal(err)
					}
					if origin == "unknown" {
						if _, err := e.store.DB().Exec(`UPDATE donations SET first_approval_origin='unknown' WHERE id=?`, id); err != nil {
							t.Fatal(err)
						}
					}
				}
				id := parseTestID(t, d.ID)
				before, err := e.service.GetAdmin(ctx, id)
				if err != nil || !before.CanForceReject {
					t.Fatalf("unavailable %+v %v", before, err)
				}
				input := ReviewInput{Decision: "force_reject", ExpectedRevision: parseTestID(t, before.Revision), Reason: "Requires another review"}
				var result AdminDonation
				if forceActor == "admin" {
					r, err := e.service.ReviewAdmin(ctx, reviewMutation(t, 'c', id), id, input)
					if err != nil {
						t.Fatal(err)
					}
					result = r.Value
				} else {
					_, err := e.service.ReviewSteward(ctx, steward, id, donationMutation(t, 'c', http.MethodPost, routeStewardReview, []int64{id}, input), input)
					if err != nil {
						t.Fatal(err)
					}
					result, err = e.service.GetAdmin(ctx, id)
					if err != nil {
						t.Fatal(err)
					}
				}
				if result.Status != "rejected" || result.FirstApprovalOrigin != before.FirstApprovalOrigin || result.ReviewResult.Decision != "force_reject" {
					t.Fatalf("result %+v", result)
				}
				var members, keys, required int
				if err := e.store.DB().QueryRow(`SELECT (SELECT COUNT(*) FROM donation_key_memberships WHERE donation_id=?),(SELECT COUNT(*) FROM endpoint_keys),(SELECT COUNT(*) FROM donation_key_review_requirements WHERE required=1)`, id).Scan(&members, &keys, &required); err != nil || members != 0 || keys != 1 || required != 1 {
					t.Fatal(members, keys, required, err)
				}
			})
		}
	}
}

func TestConcurrentPendingReviewHasOneWinner(t *testing.T) {
	e := newDonationTestEnv(t)
	ctx := context.Background()
	e.seedUser(t, "", nil, true)
	five := int64(5)
	actor := e.seedUser(t, "concurrent-reviewer", &five, false)
	owner := e.seedUser(t, "concurrent-donor", nil, false)
	_, key := e.seedEndpointKey(t, owner, 'a')
	d := e.createDonationWithSeed(t, owner, 'a', key)
	id := parseTestID(t, d.ID)
	inputs := []ReviewInput{{Decision: "approve", ExpectedRevision: 1, KeySettings: []KeySetting{{DonationKeyID: parseTestID(t, d.Keys[0].ID), Enabled: true}}}, {Decision: "reject", ExpectedRevision: 1, Reason: "Rejected"}}
	mutations := make([]resources.ControlMutation, len(inputs))
	for i := range inputs {
		mutations[i] = donationMutation(t, byte('d'+i), http.MethodPost, routeStewardReview, []int64{id}, inputs[i])
	}
	start := make(chan struct{})
	errorsOut := make(chan error, 2)
	var wg sync.WaitGroup
	for i := range inputs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, err := e.service.ReviewSteward(ctx, actor, id, mutations[i], inputs[i])
			errorsOut <- err
		}(i)
	}
	close(start)
	wg.Wait()
	close(errorsOut)
	success := 0
	for err := range errorsOut {
		if err == nil {
			success++
		} else if !errors.Is(err, ErrForbidden) && !errors.Is(err, ErrConflict) {
			t.Fatal(err)
		}
	}
	if success != 1 {
		t.Fatal("review winners", success)
	}
	var count int
	if err := e.store.DB().QueryRow(`SELECT COUNT(*) FROM donation_reviews WHERE donation_id=? AND action IN ('approve','reject')`, id).Scan(&count); err != nil || count != 1 {
		t.Fatal(fmt.Sprint(count), err)
	}
}

func TestTraineeReviewPartialExpiryRequiresFreshRevision(t *testing.T) {
	e := newDonationTestEnv(t)
	ctx := context.Background()
	e.seedUser(t, "", nil, true)
	five := int64(5)
	actor := e.seedUser(t, "expiry-reviewer", &five, false)
	owner := e.seedUser(t, "expiry-donor", nil, false)
	_, finite := e.seedEndpointKey(t, owner, 'a')
	_, remaining := e.seedEndpointKey(t, owner, 'b')
	expires := donationTestNow + 1
	create := CreateInput{Description: "Partial expiry", OwnershipAuthorized: true, Keys: []CreateKeyInput{{EndpointKeyID: finite, ExpiresAt: &expires}, {EndpointKeyID: remaining}}}
	d, err := e.service.Create(ctx, owner, donationCreateMutation(t, 'p', create), create)
	if err != nil {
		t.Fatal(err)
	}
	id := parseTestID(t, d.Value.ID)
	input := ReviewInput{Decision: "reject", ExpectedRevision: 1, Reason: "Review remaining key"}
	mutation := donationMutation(t, 'q', http.MethodPost, routeStewardReview, []int64{id}, input)
	e.clock.Store(expires)
	if _, err := e.service.ReviewSteward(ctx, actor, id, mutation, input); !errors.Is(err, ErrConflict) {
		t.Fatal("stale partial expiry", err)
	}
	view, err := e.service.GetSteward(ctx, actor, id)
	if err != nil || view.Status != "pending" || view.Revision != "2" || view.Keys[0].EndedReason == nil {
		t.Fatalf("expiry did not persist: %+v %v", view, err)
	}
	if n := mutationRecordCount(t, e.store.DB(), idempotency.ScopeControlMutation, "trainee5", actor, mutation.IdempotencyKey); n != 0 {
		t.Fatal("failed review consumed idempotency key", n)
	}
	input.ExpectedRevision = 2
	if _, err := e.service.ReviewSteward(ctx, actor, id, donationMutation(t, 'r', http.MethodPost, routeStewardReview, []int64{id}, input), input); err != nil {
		t.Fatal(err)
	}
}
