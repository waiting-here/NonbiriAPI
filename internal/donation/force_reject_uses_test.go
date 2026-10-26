package donation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

func approveForceRejectUses(t *testing.T, e *donationTestEnv, owner, id int64, all bool) []Use {
	t.Helper()
	uses := ownerUsesForTest(t, e, owner, id)
	if !all {
		uses = uses[:1]
	}
	items := make([]UseReviewItem, len(uses))
	for i, use := range uses {
		items[i] = UseReviewItem{UseID: parseTestID(t, use.ID), ExpectedRevision: parseTestID(t, use.Revision), Decision: "approve", ReviewNote: "Service approved"}
	}
	result, err := e.service.reviewUses(context.Background(), reviewerAdmin, 0, id, items, donationMutation(t, 'A', http.MethodPost, routeAdminUses+"/review", []int64{id}, items))
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range result.Value.Results {
		if !item.OK {
			t.Fatalf("use approval: %+v", item)
		}
	}
	return ownerUsesForTest(t, e, owner, id)
}

func TestForceRejectReviewedUsesWithPendingHeader(t *testing.T) {
	for _, all := range []bool{true, false} {
		for _, actor := range []string{"admin", "level6"} {
			t.Run(fmt.Sprintf("all=%t/%s", all, actor), func(t *testing.T) {
				e := newDonationTestEnv(t)
				ctx := context.Background()
				e.seedUser(t, "", nil, true)
				five, six := int64(5), int64(6)
				trainee := e.seedUser(t, "use-force-trainee", &five, false)
				steward := e.seedUser(t, "use-force-steward", &six, false)
				owner := e.seedUser(t, "use-force-owner", nil, false)
				_, first := e.seedEndpointKey(t, owner, 'a')
				_, second := e.seedEndpointKey(t, owner, 'b')
				d := e.createDonation(t, owner, first, second)
				qualifyStandardDonation(t, e, d.ID)
				id := parseTestID(t, d.ID)
				uses := approveForceRejectUses(t, e, owner, id, all)
				if !all {
					// A replacement awaiting review must not hide the retained approval.
					stageUseForTest(t, e, uses[0])
				}
				if _, err := e.store.DB().Exec(`UPDATE donation_uses SET enabled=0,revision=revision+1 WHERE id=?`, uses[0].ID); err != nil {
					t.Fatal(err)
				}
				before, err := e.service.GetAdmin(ctx, id)
				if err != nil || before.Status != "pending" || !before.CanForceReject || before.ForceRejectUnavailableReason != nil {
					t.Fatalf("reviewed-use projection: %+v %v", before, err)
				}
				stewardView, err := e.service.GetSteward(ctx, steward, id)
				if err != nil || !stewardView.CanForceReject {
					t.Fatalf("steward projection: %+v %v", stewardView, err)
				}
				if !all {
					traineeView, err := e.service.GetSteward(ctx, trainee, id)
					if err != nil || traineeView.CanForceReject {
						t.Fatalf("trainee projection: %+v %v", traineeView, err)
					}
				}
				input := ReviewInput{Decision: "force_reject", ExpectedRevision: parseTestID(t, before.Revision), Reason: "Recheck all credential bodies"}
				if _, err := e.service.ReviewSteward(ctx, trainee, id, donationMutation(t, 'T', http.MethodPost, routeStewardReview, []int64{id}, input), input); !errors.Is(err, authz.ErrPermissionDenied) {
					t.Fatalf("trainee force rejection: %v", err)
				}
				stale := input
				stale.ExpectedRevision++
				if _, err := e.service.ReviewAdmin(ctx, reviewMutation(t, 'S', id), id, stale); !errors.Is(err, ErrConflict) {
					t.Fatalf("stale force rejection: %v", err)
				}
				emptyReason := input
				emptyReason.Reason = " "
				if _, err := e.service.ReviewAdmin(ctx, reviewMutation(t, 'N', id), id, emptyReason); !errors.Is(err, ErrInvalidRequest) {
					t.Fatalf("empty reason: %v", err)
				}
				force := func() ([]byte, bool, error) {
					if actor == "admin" {
						result, err := e.service.ReviewAdmin(ctx, reviewMutation(t, 'F', id), id, input)
						return result.Body, result.Replayed, err
					}
					result, err := e.service.ReviewSteward(ctx, steward, id, donationMutation(t, 'F', http.MethodPost, routeStewardReview, []int64{id}, input), input)
					return result.Body, result.Replayed, err
				}
				body, replayed, err := force()
				if err != nil || replayed {
					t.Fatalf("force rejection: replay=%t %v", replayed, err)
				}
				replayBody, replayed, err := force()
				if err != nil || !replayed || string(replayBody) != string(body) {
					t.Fatalf("force replay: replay=%t %v", replayed, err)
				}
				after, err := e.service.GetAdmin(ctx, id)
				if err != nil || after.Status != "rejected" || after.CanForceReject || after.ReviewResult == nil || after.ReviewResult.Decision != "force_reject" || after.FirstApprovalOrigin != before.FirstApprovalOrigin {
					t.Fatalf("terminal projection: %+v %v", after, err)
				}
				var members, pointers, activeKeys, physical, required, reviews int
				err = e.store.DB().QueryRow(`SELECT
     (SELECT count(*) FROM donation_key_memberships WHERE donation_id=?),
     (SELECT count(*) FROM donation_uses u JOIN donation_keys dk ON dk.id=u.donation_key_id WHERE dk.donation_id=? AND (u.current_approved_revision_id IS NOT NULL OR u.current_pending_revision_id IS NOT NULL OR u.enabled<>0 OR u.discovery_enabled<>0)),
     (SELECT count(*) FROM donation_keys WHERE donation_id=? AND (ended_at IS NULL OR enabled<>0)),
     (SELECT count(*) FROM endpoint_keys),
     (SELECT count(*) FROM donation_key_review_requirements WHERE required=1),
     (SELECT count(*) FROM donation_reviews WHERE donation_id=? AND action='force_reject')`, id, id, id, id).Scan(&members, &pointers, &activeKeys, &physical, &required, &reviews)
				if err != nil || members != 0 || pointers != 0 || activeKeys != 0 || physical != 2 || required != 2 || reviews != 1 {
					t.Fatalf("termination: members=%d pointers=%d active=%d private=%d required=%d reviews=%d %v", members, pointers, activeKeys, physical, required, reviews, err)
				}
				recreated := e.createDonationWithSeed(t, owner, 'R', first)
				if recreated.Status != "pending" || !recreated.Keys[0].Review.Required {
					t.Fatalf("body review requirement lost: %+v", recreated)
				}
			})
		}
	}
}

func TestForceRejectUseEligibilityAndUnknownOriginalMaterial(t *testing.T) {
	for _, state := range []string{"pending_only", "rejected", "deleted", "expired", "unknown_original"} {
		t.Run(state, func(t *testing.T) {
			e := newDonationTestEnv(t)
			ctx := context.Background()
			e.seedUser(t, "", nil, true)
			owner := e.seedUser(t, "force-boundary-owner", nil, false)
			_, key := e.seedEndpointKey(t, owner, 'a')
			d := e.createDonation(t, owner, key)
			qualifyStandardDonation(t, e, d.ID)
			id := parseTestID(t, d.ID)
			want := error(ErrConflict)
			reason := "not_approved"
			if state != "pending_only" {
				approveForceRejectUses(t, e, owner, id, true)
				if state == "unknown_original" {
					if _, err := e.store.DB().Exec(`UPDATE donation_keys SET key_body_review_hmac=NULL WHERE donation_id=?`, id); err != nil {
						t.Fatal(err)
					}
					want, reason = ErrReviewMaterialUnavailable, "review_material_unavailable"
				} else if _, err := e.store.DB().Exec(`UPDATE donations SET status=?,terminal_at=? WHERE id=?`, state, e.clock.Load(), id); err != nil {
					t.Fatal(err)
				}
			}
			view, err := e.service.GetAdmin(ctx, id)
			if err != nil || view.CanForceReject || view.ForceRejectUnavailableReason == nil || *view.ForceRejectUnavailableReason != reason {
				t.Fatalf("ineligible projection: %+v %v", view, err)
			}
			input := ReviewInput{Decision: "force_reject", ExpectedRevision: parseTestID(t, view.Revision), Reason: "Must remain unavailable"}
			if _, err := e.service.ReviewAdmin(ctx, reviewMutation(t, 'F', id), id, input); !errors.Is(err, want) {
				t.Fatalf("ineligible action: got %v want %v", err, want)
			}
			var required, reviews int
			if err := e.store.DB().QueryRow(`SELECT (SELECT count(*) FROM donation_key_review_requirements),(SELECT count(*) FROM donation_reviews WHERE action='force_reject')`).Scan(&required, &reviews); err != nil || required != 0 || reviews != 0 {
				t.Fatalf("rejected intent wrote effects: %d %d %v", required, reviews, err)
			}
		})
	}
}

func TestForceRejectPendingHeaderHTTPProjectionAndAction(t *testing.T) {
	e := newDonationTestEnv(t)
	e.seedUser(t, "", nil, true)
	owner := e.seedUser(t, "force-http-owner", nil, false)
	_, key := e.seedEndpointKey(t, owner, 'a')
	d := e.createDonation(t, owner, key)
	qualifyStandardDonation(t, e, d.ID)
	id := parseTestID(t, d.ID)
	approveForceRejectUses(t, e, owner, id, true)
	api := &httpAPI{service: e.service}
	r := httptest.NewRequest(http.MethodGet, "/admin/api/donations/"+d.ID, nil)
	r.SetPathValue("id", d.ID)
	w := httptest.NewRecorder()
	api.getAdmin(w, r)
	var view AdminDonation
	if err := json.Unmarshal(w.Body.Bytes(), &view); err != nil || w.Code != http.StatusOK || !view.CanForceReject || view.Status != "pending" {
		t.Fatalf("HTTP detail: %d %s %v", w.Code, w.Body.String(), err)
	}
	input := fmt.Sprintf(`{"decision":"force_reject","expected_revision":%q,"reason":"Recheck approved services"}`, view.Revision)
	for range 2 {
		r = httptest.NewRequest(http.MethodPost, "/admin/api/donations/"+d.ID+"/review", strings.NewReader(input))
		r.SetPathValue("id", d.ID)
		r.Header.Set("Idempotency-Key", strings.Repeat("F", 22))
		w = httptest.NewRecorder()
		api.reviewAdmin(w, r)
		if err := json.Unmarshal(w.Body.Bytes(), &view); err != nil || w.Code != http.StatusOK || view.Status != "rejected" || view.CanForceReject {
			t.Fatalf("HTTP force/replay: %d %s %v", w.Code, w.Body.String(), err)
		}
	}
}

func TestForceRejectProjectionExcludesLogicallyExpiredApprovedUse(t *testing.T) {
	e := newDonationTestEnv(t)
	e.seedUser(t, "", nil, true)
	owner := e.seedUser(t, "force-expired-owner", nil, false)
	_, key := e.seedEndpointKey(t, owner, 'a')
	d := e.createDonation(t, owner, key)
	qualifyStandardDonation(t, e, d.ID)
	id := parseTestID(t, d.ID)
	approveForceRejectUses(t, e, owner, id, true)
	if _, err := e.store.DB().Exec(`UPDATE donation_keys SET expires_at=? WHERE donation_id=?`, e.clock.Load(), id); err != nil {
		t.Fatal(err)
	}
	page, err := e.service.DonationsAdminPage(context.Background(), ManagementFilter{}, pagination.Default())
	if err != nil || len(page.Data) != 1 || page.Data[0].Status != "expired" {
		t.Fatalf("logical expiry: %+v %v", page, err)
	}
	tx, err := e.store.DB().BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	view, err := logicalHeaderTx(context.Background(), tx, id, e.clock.Load())
	if err != nil || view.CanForceReject {
		t.Fatalf("expired header: %+v %v", view, err)
	}
}

func TestLegacyReviewCannotOverrideApprovedUses(t *testing.T) {
	for _, actor := range []string{"admin", "trainee5"} {
		t.Run(actor, func(t *testing.T) {
			e := newDonationTestEnv(t)
			ctx := context.Background()
			e.seedUser(t, "", nil, true)
			five := int64(5)
			trainee := e.seedUser(t, "legacy-use-trainee", &five, false)
			owner := e.seedUser(t, "legacy-use-owner", nil, false)
			_, key := e.seedEndpointKey(t, owner, 'a')
			d := e.createDonation(t, owner, key)
			qualifyStandardDonation(t, e, d.ID)
			id := parseTestID(t, d.ID)
			before := approveForceRejectUses(t, e, owner, id, false)
			for i, decision := range []string{"reject", "approve"} {
				input := ReviewInput{Decision: decision, ExpectedRevision: 1, Reason: "Must preserve approved services"}
				if decision == "approve" {
					input.KeySettings = []KeySetting{{DonationKeyID: parseTestID(t, d.Keys[0].ID), Enabled: false}}
				}
				var err error
				if actor == "admin" {
					_, err = e.service.ReviewAdmin(ctx, reviewMutation(t, byte('L'+i), id), id, input)
				} else {
					_, err = e.service.ReviewSteward(ctx, trainee, id, donationMutation(t, byte('L'+i), http.MethodPost, routeStewardReview, []int64{id}, input), input)
				}
				if !errors.Is(err, ErrConflict) {
					t.Fatalf("legacy %s changed reviewed uses: %v", decision, err)
				}
			}
			after := ownerUsesForTest(t, e, owner, id)
			beforeJSON, _ := json.Marshal(before)
			afterJSON, _ := json.Marshal(after)
			if string(beforeJSON) != string(afterJSON) {
				t.Fatal("failed legacy reviews changed use authorization")
			}
			header, err := e.service.GetAdmin(ctx, id)
			if err != nil || header.Status != "pending" || header.Revision != "1" || !header.CanForceReject {
				t.Fatalf("failed legacy review changed member: %+v %v", header, err)
			}
			var enabled bool
			if err := e.store.DB().QueryRow(`SELECT enabled FROM donation_keys WHERE id=?`, d.Keys[0].ID).Scan(&enabled); err != nil || !enabled {
				t.Fatalf("failed legacy approval changed member enablement: %t %v", enabled, err)
			}
			selected := after[1]
			item := UseReviewItem{UseID: parseTestID(t, selected.ID), ExpectedRevision: parseTestID(t, selected.Revision), Decision: "reject", ReviewNote: "Review only this pending service"}
			role, actorID, route := reviewerAdmin, int64(0), routeAdminUses+"/review"
			if actor == "trainee5" {
				role, actorID, route = reviewerSteward, trainee, routeStewardUses+"/review"
			}
			result, err := e.service.reviewUses(ctx, role, actorID, id, []UseReviewItem{item}, donationMutation(t, 'U', http.MethodPost, route, []int64{id}, item))
			if err != nil || len(result.Value.Results) != 1 || !result.Value.Results[0].OK {
				t.Fatalf("valid per-use review blocked: %+v %v", result, err)
			}
			current := ownerUsesForTest(t, e, owner, id)
			if current[0].Approved == nil || current[1].Pending != nil {
				t.Fatal("per-use rejection lost existing approval or retained pending version")
			}
		})
	}
}
