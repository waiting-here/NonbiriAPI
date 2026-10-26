package donation

import (
	"errors"
	"net/http"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

func TestMainstreamTagRulesApproveOnlyNewWholeSubmissionsAndRevokeScope(t *testing.T) {
	e := newDonationTestEnv(t)
	e.seedUser(t, "", nil, true)
	five := int64(5)
	actor := e.seedUser(t, "tag-qualified-reviewer", &five, false)
	owner := e.seedUser(t, "tag-qualified-owner", nil, false)
	_, oldKey := e.seedEndpointKeyAt(t, owner, 'A', "https://old.example.test/v1")
	old := e.createDonationWithSeed(t, owner, 'a', oldKey)
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := e.store.DB().Exec(query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO endpoint_tag_rules VALUES('example.test','add')`)
	exec(`INSERT INTO endpoint_tag_rule_tags VALUES('example.test','mainstream')`)
	unchanged, err := e.service.GetOwner(t.Context(), owner, parseTestID(t, old.ID))
	if err != nil || unchanged.Status != "pending" {
		t.Fatal("rule automatically approved old pending", unchanged, err)
	}
	_, first := e.seedEndpointKeyAt(t, owner, 'B', "https://one.example.test/v1")
	_, second := e.seedEndpointKeyAt(t, owner, 'C', "http://nested.two.example.test:8080/api")
	qualified := e.createDonationWithSeed(t, owner, 'b', first, second)
	if qualified.Status != "approved" || qualified.Keys[0].SafeSource.Kind != "custom" || qualified.Keys[1].SafeSource.Kind != "custom" {
		t.Fatal(qualified)
	}
	id, kid := parseTestID(t, qualified.ID), parseTestID(t, qualified.Keys[0].ID)
	if _, err := e.service.GetSteward(t.Context(), actor, id); err != nil {
		t.Fatal("standard excluded all-qualified donation", err)
	}
	_, taggedMixed := e.seedEndpointKeyAt(t, owner, 'D', "https://mixed.example.test/v1")
	_, plain := e.seedEndpointKeyAt(t, owner, 'E', "https://other.test/v1")
	mixed := e.createDonationWithSeed(t, owner, 'c', taggedMixed, plain)
	if mixed.Status != "pending" {
		t.Fatal("partly qualified auto-approved")
	}
	if _, err := e.service.GetSteward(t.Context(), actor, parseTestID(t, mixed.ID)); !errors.Is(err, ErrNotFound) {
		t.Fatal("partly qualified entered standard", err)
	}
	model := controlModel(t, e, "original-mainstream-qualification", true)
	e.seedBinding(t, model, parseTestID(t, mixed.Keys[1].ID), "model", 0)
	if _, err := e.service.GetSteward(t.Context(), actor, parseTestID(t, mixed.ID)); err != nil {
		t.Fatal("original qualification OR label rejected", err)
	}
	note := "Maintained"
	input := KeyManagementInput{ExpectedRevision: 1, SafeNote: &note}
	mutation := donationMutation(t, 'm', http.MethodPatch, routeStewardKey, []int64{id, kid}, input)
	ctx := authz.WithStewardPermission(t.Context(), "donations.manage_keys")
	if _, err := e.service.ManageKeySession(ctx, actor, id, kid, mutation, input); err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO endpoint_tag_rules VALUES('one.example.test','remove')`)
	if _, err := e.service.ManageKeySession(ctx, actor, id, kid, mutation, input); !errors.Is(err, ErrNotFound) {
		t.Fatal("rule removal allowed replay", err)
	}
	if _, err := e.service.GetSteward(t.Context(), actor, id); !errors.Is(err, ErrNotFound) {
		t.Fatal("rule removal allowed read", err)
	}
	kept, err := e.service.GetOwner(t.Context(), owner, id)
	if err != nil || kept.Status != "approved" {
		t.Fatal("tag removal changed historical approval", kept, err)
	}
	exec(`DELETE FROM endpoint_tag_rules WHERE domain='one.example.test'`)
	if _, err := e.service.ManageKeySession(ctx, actor, id, kid, mutation, input); err != nil {
		t.Fatal("remaining rule did not restore scope", err)
	}
	policy := authz.DefaultStewardPermissions(5)
	policy.DonationScope = authz.DonationScopeEnhanced
	writeDonationPolicy(t, e, actor, policy)
	exec(`DELETE FROM endpoint_tag_rule_tags; DELETE FROM endpoint_tag_rules; INSERT INTO endpoint_tag_rules VALUES('one.example.test','add'); INSERT INTO endpoint_tag_rule_tags VALUES('one.example.test','mysterious')`)
	page, err := e.service.KeysStewardPage(t.Context(), actor, id, pagination.Default())
	if err != nil || len(page.Data) != 2 {
		t.Fatal("enhanced did not qualify whole donation from one effective tag", page, err)
	}
}

func TestMainstreamTagResubmissionHonorsWholeScopeAndForcedReview(t *testing.T) {
	for _, mode := range []string{"qualified", "mixed", "forced", "forced-other-key"} {
		t.Run(mode, func(t *testing.T) {
			e := newDonationTestEnv(t)
			e.seedUser(t, "", nil, true)
			owner := e.seedUser(t, "resubmit-tag-owner", nil, false)
			_, first := e.seedEndpointKeyAt(t, owner, 'R', "https://resubmit.example.test/v1")
			keys := []int64{first}
			if mode == "mixed" {
				_, plain := e.seedEndpointKeyAt(t, owner, 'S', "https://plain.test/v1")
				keys = append(keys, plain)
			}
			if mode == "forced-other-key" {
				_, second := e.seedEndpointKeyAt(t, owner, 'S', "https://other.example.test/v1")
				keys = append(keys, second)
			}
			d := e.createDonationWithSeed(t, owner, 'D', keys...)
			id := parseTestID(t, d.ID)
			if _, err := e.store.DB().Exec(`INSERT INTO admin_endpoint_tags VALUES('https://resubmit.example.test/v1','mainstream')`); err != nil {
				t.Fatal(err)
			}
			if mode == "forced-other-key" {
				if _, err := e.store.DB().Exec(`INSERT INTO admin_endpoint_tags VALUES('https://other.example.test/v1','mainstream')`); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "forced" || mode == "forced-other-key" {
				if _, err := e.store.DB().Exec(`INSERT INTO donation_key_review_requirements(hmac,required,revision,created_at,updated_at) SELECT key_body_review_hmac,1,1,?,? FROM donation_keys WHERE donation_id=? ORDER BY id DESC LIMIT 1`, donationTestNow, donationTestNow, id); err != nil {
					t.Fatal(err)
				}
			}
			input := EditInput{Description: "Resubmitted", ExpectedRevision: 1}
			result, err := e.service.Edit(t.Context(), owner, id, donationMutation(t, 'E', http.MethodPatch, routeDonation, []int64{id}, input), input)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "qualified" {
				if result.Value.Status != "approved" || result.Value.UseSummary.Pending != "0" || result.Value.UseSummary.Approved != "15" {
					t.Fatal(result.Value)
				}
			} else if result.Value.Status != "pending" || result.Value.UseSummary.Approved != "0" {
				t.Fatal("bypassed whole-scope or force requirement", result.Value)
			}
		})
	}
}

func TestMainstreamTagAppendApprovesOnlyNewRevisionsAndPreservesControls(t *testing.T) {
	f := newAppendUseFixture(t)
	before := appendBudgetSnapshot(t, f)
	old := ownerUsesForTest(t, f.e, f.owner, f.donation)
	input := f.input
	input.Items = []AppendUseItem{{NativeService: contract.NativeServiceResponses}}
	first, err := f.append(t, 'J', input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.e.store.DB().Exec(`INSERT INTO admin_endpoint_tags(base_url,tag) SELECT canonical_base_url,'mainstream' FROM donation_keys WHERE id=?`, f.member); err != nil {
		t.Fatal(err)
	}
	input = f.input
	input.Items = []AppendUseItem{{NativeService: contract.NativeServiceCompletions}}
	input.ExpectedRevision = parseTestID(t, first.Value.DonationRevision)
	result, err := f.append(t, 'K', input)
	if err != nil {
		t.Fatal(err)
	}
	if replay, err := f.append(t, 'K', input); err != nil || !replay.Replayed || string(replay.Body) != string(result.Body) {
		t.Fatal(replay, err)
	}
	current := ownerUsesForTest(t, f.e, f.owner, f.donation)
	for _, use := range current {
		switch use.NativeService {
		case contract.NativeServiceResponses:
			if use.Pending == nil {
				t.Fatal("old pending got swept into approval")
			}
		case contract.NativeServiceCompletions:
			if use.Approved == nil || use.Pending != nil {
				t.Fatal("new use not auto approved", use)
			}
		}
		for _, prior := range old {
			if use.ID == prior.ID && use.Approved.GrantRevision != prior.Approved.GrantRevision {
				t.Fatal("historical approved revision changed")
			}
		}
	}
	if appendBudgetSnapshot(t, f) != before {
		t.Fatal("automatic approval changed budget or membership")
	}
}

func TestReviewReceiptRechecksTagsAndGrantsAfterTermination(t *testing.T) {
	for _, mode := range []string{"original-mainstream", "tag-only", "mixed-history"} {
		t.Run(mode, func(t *testing.T) {
			e := newDonationTestEnv(t)
			e.seedUser(t, "", nil, true)
			five := int64(5)
			actor := e.seedUser(t, "terminal-tag-reviewer", &five, false)
			owner := e.seedUser(t, "terminal-tag-owner", nil, false)
			policy := authz.DefaultStewardPermissions(5)
			policy.Permissions["donations.force_reject"] = true
			writeDonationPolicy(t, e, actor, policy)
			var d Donation
			action := "reject"
			if mode == "original-mainstream" {
				channel := seedMainstreamChannel(t, e, "Terminal channel", "subscription")
				d = sourcedDonation(t, e, owner, 'T', channel, "terminal-original-body")
				action = "force_reject"
			} else {
				_, first := e.seedEndpointKeyAt(t, owner, 'T', "https://terminal.example.test/v1")
				keys := []int64{first}
				if mode == "mixed-history" {
					_, second := e.seedEndpointKeyAt(t, owner, 'U', "https://binding.example.test/v1")
					keys = append(keys, second)
				}
				d = e.createDonation(t, owner, keys...)
				if mode == "mixed-history" {
					model := controlModel(t, e, "terminal-original-binding", true)
					e.seedBinding(t, model, parseTestID(t, d.Keys[1].ID), "model", 0)
				}
				if _, err := e.store.DB().Exec(`INSERT INTO admin_endpoint_tags VALUES('https://terminal.example.test/v1','mainstream')`); err != nil {
					t.Fatal(err)
				}
			}
			id := parseTestID(t, d.ID)
			input := ReviewInput{Decision: action, ExpectedRevision: parseTestID(t, d.Revision), Reason: "Terminal review"}
			mutation := donationMutation(t, 'V', http.MethodPost, routeStewardReview, []int64{id}, input)
			result, err := e.service.ReviewSteward(t.Context(), actor, id, mutation, input)
			if err != nil || result.Value.Status != "rejected" {
				t.Fatal(result, err)
			}
			if mode == "mixed-history" {
				// Termination removes the other key's model binding. One remaining
				// tagged key must not qualify this entire historical donation.
				if _, err := e.service.ReviewSteward(t.Context(), actor, id, mutation, input); !errors.Is(err, ErrNotFound) {
					t.Fatal("partially qualified history replayed", err)
				}
				return
			}
			if replay, err := e.service.ReviewSteward(t.Context(), actor, id, mutation, input); err != nil || !replay.Replayed || string(result.Body) != string(replay.Body) {
				t.Fatal("authorized terminal receipt failed", replay, err)
			}
			permission := "donations.review"
			if action == "force_reject" {
				permission = "donations.force_reject"
			}
			policy.Permissions[permission] = false
			writeDonationPolicy(t, e, actor, policy)
			if _, err := e.service.ReviewSteward(t.Context(), actor, id, mutation, input); !errors.Is(err, authz.ErrPermissionDenied) {
				t.Fatal("revoked action replayed", err)
			}
			policy.Permissions[permission] = true
			writeDonationPolicy(t, e, actor, policy)
			if mode != "original-mainstream" {
				if _, err := e.store.DB().Exec(`DELETE FROM admin_endpoint_tags`); err != nil {
					t.Fatal(err)
				}
				if _, err := e.service.ReviewSteward(t.Context(), actor, id, mutation, input); !errors.Is(err, ErrNotFound) {
					t.Fatal("revoked tag replayed terminal history", err)
				}
			}
			if _, err := e.service.GetSteward(t.Context(), actor, id); !errors.Is(err, ErrNotFound) {
				t.Fatal("receipt exception granted ordinary terminal access", err)
			}
		})
	}
}

func TestUseReviewReplayRechecksEffectiveTags(t *testing.T) {
	e := newDonationTestEnv(t)
	e.seedUser(t, "", nil, true)
	five := int64(5)
	actor := e.seedUser(t, "use-tag-reviewer", &five, false)
	owner := e.seedUser(t, "use-tag-owner", nil, false)
	_, key := e.seedEndpointKeyAt(t, owner, 'W', "https://use.example.test/v1")
	d := e.createDonation(t, owner, key)
	id := parseTestID(t, d.ID)
	if _, err := e.store.DB().Exec(`INSERT INTO endpoint_tag_rules VALUES('example.test','add'); INSERT INTO endpoint_tag_rule_tags VALUES('example.test','mainstream')`); err != nil {
		t.Fatal(err)
	}
	use := ownerUsesForTest(t, e, owner, id)[0]
	items := []UseReviewItem{{UseID: parseTestID(t, use.ID), ExpectedRevision: parseTestID(t, use.Revision), Decision: "approve", ReviewNote: "Use review"}}
	mutation := donationMutation(t, 'X', http.MethodPost, routeStewardUses+"/review", []int64{id}, items)
	if result, err := e.service.reviewUses(t.Context(), reviewerSteward, actor, id, items, mutation); err != nil || !result.Value.Results[0].OK {
		t.Fatal(result, err)
	}
	if _, err := e.store.DB().Exec(`DELETE FROM endpoint_tag_rules`); err != nil {
		t.Fatal(err)
	}
	if _, err := e.service.reviewUses(t.Context(), reviewerSteward, actor, id, items, mutation); !errors.Is(err, ErrNotFound) {
		t.Fatal("revoked rule replayed use decision", err)
	}
}
