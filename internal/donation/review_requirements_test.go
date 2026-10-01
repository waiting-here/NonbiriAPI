package donation

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

func reviewMutation(t *testing.T, seed byte, id int64) resources.ControlMutation {
	return donationMutation(t, seed, http.MethodPost, routeAdminReview, []int64{id}, map[string]any{"seed": string(seed)})
}

func sourcedDonation(t *testing.T, e *donationTestEnv, owner int64, seed byte, channel, body string) Donation {
	t.Helper()
	source := &donationEndpointSource{channelID: channel, revision: 1, name: "review source", category: "subscription"}
	key := seedSourcedEndpointKey(t, e, owner, int(seed), "https://review.example.test/v1", source, body)
	created, err := e.service.Create(context.Background(), owner, donationMutation(t, seed, http.MethodPost, routeDonations, nil, map[string]any{"seed": string(seed)}), CreateInput{Description: "review donation", OwnershipAuthorized: true, Keys: []CreateKeyInput{{EndpointKeyID: key}}})
	if err != nil {
		t.Fatal(err)
	}
	return created.Value
}

func TestForceRejectPreservesOriginAndRequiresLatestEnabledReview(t *testing.T) {
	e := newDonationTestEnv(t)
	ctx := context.Background()
	owner := e.seedUser(t, "review-owner", nil, false)
	e.seedUser(t, "", nil, true)
	channel := seedMainstreamChannel(t, e, "Review Channel", "subscription")
	first := sourcedDonation(t, e, owner, 'A', channel, "same body")
	id, _ := strconv.ParseInt(first.ID, 10, 64)
	kid, _ := strconv.ParseInt(first.Keys[0].ID, 10, 64)
	if first.Status != "approved" {
		t.Fatal("expected automatic approval")
	}
	limit := ReviewInput{Decision: "approve", ExpectedRevision: 1, Reason: "limits", KeySettings: []KeySetting{{DonationKeyID: kid, Enabled: true}}}
	changed, err := e.service.ReviewAdmin(ctx, reviewMutation(t, 'B', id), id, limit)
	if err != nil {
		t.Fatal(err)
	}
	if changed.Value.FirstApprovalOrigin != "auto" || !changed.Value.CanForceReject {
		t.Fatalf("origin lost: %+v", changed.Value)
	}
	force := ReviewInput{Decision: "force_reject", ExpectedRevision: 2, Reason: "review required"}
	rejected, err := e.service.ReviewAdmin(ctx, reviewMutation(t, 'C', id), id, force)
	if err != nil {
		t.Fatal(err)
	}
	if rejected.Value.Status != "rejected" || rejected.Value.ReviewResult.Decision != "force_reject" {
		t.Fatal("wrong terminal projection")
	}
	var memberships, physical int
	if err = e.store.DB().QueryRow(`SELECT (SELECT count(*) FROM donation_key_memberships WHERE donation_id=?),(SELECT count(*) FROM endpoint_keys WHERE id=?)`, id, *first.Keys[0].EndpointKeyID).Scan(&memberships, &physical); err != nil || memberships != 0 || physical != 1 {
		t.Fatalf("termination: %d %d %v", memberships, physical, err)
	}
	other := e.seedUser(t, "another-review-owner", nil, false)
	second := sourcedDonation(t, e, other, 'D', channel, "same body")
	if second.Status != "pending" || !second.Keys[0].Review.Required || second.Keys[0].Review.Revision == nil {
		t.Fatal("same body bypassed required review")
	}
	secondID, _ := strconv.ParseInt(second.ID, 10, 64)
	secondKey, _ := strconv.ParseInt(second.Keys[0].ID, 10, 64)
	approve := ReviewInput{Decision: "approve", ExpectedRevision: 1, KeySettings: []KeySetting{{DonationKeyID: secondKey, Enabled: true}}}
	if _, err = e.service.ReviewAdmin(ctx, reviewMutation(t, 'E', secondID), secondID, approve); !errors.Is(err, ErrConflict) {
		t.Fatalf("missing CAS: %v", err)
	}
	stale := int64(2)
	approve.KeySettings[0].ExpectedReviewRevision = &stale
	if _, err = e.service.ReviewAdmin(ctx, reviewMutation(t, 'F', secondID), secondID, approve); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale CAS: %v", err)
	}
	latest, _ := strconv.ParseInt(*second.Keys[0].Review.Revision, 10, 64)
	approve.KeySettings[0].ExpectedReviewRevision = &latest
	approve.KeySettings[0].Enabled = false
	if _, err = e.service.ReviewAdmin(ctx, reviewMutation(t, 'G', secondID), secondID, approve); err != nil {
		t.Fatal(err)
	}
	third := sourcedDonation(t, e, owner, 'H', channel, "same body")
	if third.Status != "pending" {
		t.Fatal("disabled approval cleared requirement")
	}
	thirdID, _ := strconv.ParseInt(third.ID, 10, 64)
	thirdKey, _ := strconv.ParseInt(third.Keys[0].ID, 10, 64)
	approve.KeySettings[0] = KeySetting{DonationKeyID: thirdKey, Enabled: true, ExpectedReviewRevision: &latest}
	approved, err := e.service.ReviewAdmin(ctx, reviewMutation(t, 'I', thirdID), thirdID, approve)
	if err != nil {
		t.Fatal(err)
	}
	if approved.Value.FirstApprovalOrigin != "manual" || approved.Value.Keys[0].Review.Required {
		t.Fatal("enabled CAS failed")
	}
	fourth := sourcedDonation(t, e, other, 'J', channel, "same body")
	if fourth.Status != "approved" {
		t.Fatal("future automatic qualification not restored")
	}
	tx, err := e.store.DB().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = e.service.PrepareAccountDeletion(ctx, tx, owner, e.clock.Load()); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	var requirementCount int
	if err = e.store.DB().QueryRow(`SELECT count(*) FROM donation_key_review_requirements`).Scan(&requirementCount); err != nil || requirementCount != 1 {
		t.Fatal("account deletion lost revision material")
	}
	if _, err = e.service.Cleanup(ctx, e.clock.Load()+int64(401*24*time.Hour/time.Second), 100); err != nil {
		t.Fatal(err)
	}
	if err = e.store.DB().QueryRow(`SELECT count(*) FROM donation_key_review_requirements`).Scan(&requirementCount); err != nil || requirementCount != 1 {
		t.Fatal("retention lost revision material")
	}
}

func TestForceRejectCapacityFailureRollsBackAllEffects(t *testing.T) {
	e := newDonationTestEnv(t)
	ctx := context.Background()
	owner := e.seedUser(t, "capacity-owner", nil, false)
	e.seedUser(t, "", nil, true)
	channel := seedMainstreamChannel(t, e, "Capacity", "subscription")
	first := sourcedDonation(t, e, owner, 'K', channel, "capacity body")
	id, _ := strconv.ParseInt(first.ID, 10, 64)
	// The schema's real capacity guard is covered separately; inject its exact
	// failure at the writer boundary to prove the whole business transaction rolls back.
	if _, err := e.store.DB().Exec(`CREATE TEMP TRIGGER deny_new_review_requirement BEFORE INSERT ON donation_key_review_requirements BEGIN SELECT RAISE(ABORT,'review requirement capacity exhausted'); END`); err != nil {
		t.Fatal(err)
	}
	force := ReviewInput{Decision: "force_reject", ExpectedRevision: 1, Reason: "capacity reason"}
	if _, err := e.service.ReviewAdmin(ctx, reviewMutation(t, 'L', id), id, force); !errors.Is(err, ErrResourceLimit) {
		t.Fatalf("capacity result: %v", err)
	}
	var status string
	var revision, members, requirements, reviews int
	if err := e.store.DB().QueryRow(`SELECT status,revision,(SELECT count(*) FROM donation_key_memberships WHERE donation_id=donations.id),(SELECT count(*) FROM donation_key_review_requirements),(SELECT count(*) FROM donation_reviews WHERE donation_id=donations.id AND action='force_reject') FROM donations WHERE id=?`, id).Scan(&status, &revision, &members, &requirements, &reviews); err != nil {
		t.Fatal(err)
	}
	if status != "approved" || revision != 1 || members != 1 || requirements != 0 || reviews != 0 {
		t.Fatalf("partial failed rejection: %s %d %d %d %d", status, revision, members, requirements, reviews)
	}
	if _, err := e.store.DB().Exec(`DROP TRIGGER deny_new_review_requirement`); err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.DB().Exec(`UPDATE donation_keys SET key_body_review_hmac=NULL WHERE donation_id=?`, id); err != nil {
		t.Fatal(err)
	}
	if _, err := e.service.ReviewAdmin(ctx, reviewMutation(t, 'M', id), id, force); !errors.Is(err, ErrReviewMaterialUnavailable) {
		t.Fatalf("unknown source result: %v", err)
	}
}

func TestDonationManualCandidatePersonalExportAndDeletion(t *testing.T) {
	e := newDonationTestEnv(t)
	ctx := context.Background()
	owner := e.seedUser(t, "manual-export-owner", nil, false)
	_, key := e.seedEndpointKey(t, owner, 'p')
	created := e.createDonationWithSeed(t, owner, 'N', key)
	kid, _ := strconv.ParseInt(created.Keys[0].ID, 10, 64)
	if _, err := e.store.DB().Exec(`INSERT INTO donation_key_manual_models(donation_key_id,normalized_model_id,display_name,revision,created_at,updated_at) VALUES(?,'manual export','manual export',1,?,?)`, kid, e.clock.Load(), e.clock.Load()); err != nil {
		t.Fatal(err)
	}
	tx, err := e.store.DB().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	values, err := e.service.ExportUserTx(ctx, tx, owner, e.clock.Load(), 100)
	tx.Rollback()
	if err != nil || len(values) != 1 || len(values[0].Keys[0].ManualModels) != 1 || values[0].Keys[0].ManualModels[0].UpstreamModelID != "manual export" {
		t.Fatalf("manual export %+v %v", values, err)
	}
	tx, err = e.store.DB().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = e.service.PrepareAccountDeletion(ctx, tx, owner, e.clock.Load()); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	var candidates int
	if err = e.store.DB().QueryRow(`SELECT count(*) FROM donation_key_manual_models`).Scan(&candidates); err != nil || candidates != 0 {
		t.Fatalf("deleted candidate count %d %v", candidates, err)
	}
}

func TestForceRejectAllMembersAndOnlyEnabledBodyClears(t *testing.T) {
	e := newDonationTestEnv(t)
	ctx := context.Background()
	owner := e.seedUser(t, "multi-review-owner", nil, false)
	e.seedUser(t, "", nil, true)
	channel := seedMainstreamChannel(t, e, "Multiple bodies", "subscription")
	source := &donationEndpointSource{channelID: channel, revision: 1, name: "source", category: "subscription"}
	firstKey := seedSourcedEndpointKey(t, e, owner, 501, "https://first.example.test/v1", source, "first body")
	secondKey := seedSourcedEndpointKey(t, e, owner, 502, "https://second.example.test/v1", source, "second body")
	create := func(seed byte, keys ...int64) Donation {
		t.Helper()
		inputs := make([]CreateKeyInput, len(keys))
		for i, key := range keys {
			inputs[i] = CreateKeyInput{EndpointKeyID: key}
		}
		result, err := e.service.Create(ctx, owner, donationMutation(t, seed, http.MethodPost, routeDonations, nil, map[string]any{"seed": string(seed)}), CreateInput{Description: "multiple", OwnershipAuthorized: true, Keys: inputs})
		if err != nil {
			t.Fatal(err)
		}
		return result.Value
	}
	original := create('O', firstKey, secondKey)
	id, _ := strconv.ParseInt(original.ID, 10, 64)
	if _, err := e.service.ReviewAdmin(ctx, reviewMutation(t, 'P', id), id, ReviewInput{Decision: "force_reject", ExpectedRevision: 1, Reason: "all original members"}); err != nil {
		t.Fatal(err)
	}
	pending := create('Q', firstKey, secondKey)
	id, _ = strconv.ParseInt(pending.ID, 10, 64)
	if pending.Status != "pending" || len(pending.Keys) != 2 || !pending.Keys[0].Review.Required || !pending.Keys[1].Review.Required {
		t.Fatal("not all bodies marked")
	}
	settings := make([]KeySetting, 2)
	for i, key := range pending.Keys {
		kid, _ := strconv.ParseInt(key.ID, 10, 64)
		settings[i] = KeySetting{DonationKeyID: kid, Enabled: i == 0}
		if i == 0 {
			revision, _ := strconv.ParseInt(*key.Review.Revision, 10, 64)
			settings[i].ExpectedReviewRevision = &revision
		}
	}
	approved, err := e.service.ReviewAdmin(ctx, reviewMutation(t, 'R', id), id, ReviewInput{Decision: "approve", ExpectedRevision: 1, KeySettings: settings})
	if err != nil {
		t.Fatal(err)
	}
	if approved.Value.Keys[0].Review.Required || !approved.Value.Keys[1].Review.Required {
		t.Fatal("disabled body's review was cleared")
	}
	var active, cleared int
	if err = e.store.DB().QueryRow(`SELECT sum(required),sum(required=0) FROM donation_key_review_requirements`).Scan(&active, &cleared); err != nil || active != 1 || cleared != 1 {
		t.Fatalf("review counts %d %d %v", active, cleared, err)
	}
}
