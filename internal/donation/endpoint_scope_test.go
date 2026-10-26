package donation

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/charityscope"
	"github.com/waiting-here/NonbiriAPI/internal/pagination"
	"net/http"
	"testing"
)

func writeDonationPolicy(t *testing.T, e *donationTestEnv, id int64, p authz.StewardPermissions) {
	t.Helper()
	grants, _ := json.Marshal(p.Permissions)
	limits, _ := json.Marshal(p.Limits)
	_, err := e.store.DB().Exec(`INSERT INTO steward_permissions(user_id,revision,permissions,limits,donation_scope,created_at,updated_at) VALUES(?,1,?,?,?,?,?) ON CONFLICT(user_id) DO UPDATE SET permissions=excluded.permissions,donation_scope=excluded.donation_scope,revision=steward_permissions.revision+1`, id, string(grants), string(limits), p.DonationScope, donationTestNow, donationTestNow)
	if err != nil {
		t.Fatal(err)
	}
}
func TestDonationScopesUseAnyEndpointTagAndRevokeReplay(t *testing.T) {
	e := newDonationTestEnv(t)
	e.seedUser(t, "", nil, true)
	five := int64(5)
	actor := e.seedUser(t, "scope-reviewer", &five, false)
	owner := e.seedUser(t, "scope-donor", nil, false)
	_, first := e.seedEndpointKeyAt(t, owner, 'A', "https://tagged.example.test/v1")
	_, second := e.seedEndpointKeyAt(t, owner, 'B', "https://untagged.example.test/v1")
	_, other := e.seedEndpointKeyAt(t, owner, 'C', "https://other.example.test/v1")
	_, pendingKey := e.seedEndpointKeyAt(t, owner, 'D', "https://pending.example.test/v1")
	ctx := context.Background()
	mixed := e.createDonationWithSeed(t, owner, 'a', first, second)
	id := parseTestID(t, mixed.ID)
	keyID := parseTestID(t, mixed.Keys[1].ID)
	approve := ReviewInput{Decision: "approve", ExpectedRevision: 1, Reason: "Reviewed"}
	for _, key := range mixed.Keys {
		approve.KeySettings = append(approve.KeySettings, KeySetting{DonationKeyID: parseTestID(t, key.ID), Enabled: true})
	}
	if _, err := e.service.ReviewAdmin(ctx, donationMutation(t, 'b', http.MethodPost, routeAdminReview, []int64{id}, approve), id, approve); err != nil {
		t.Fatal(err)
	}
	unrelated := approvedResetDonation(t, e, owner, other)
	pending := e.createDonationWithSeed(t, owner, 'c', pendingKey)
	p := authz.DefaultStewardPermissions(5)
	p.Permissions["donations.read_all"] = true
	writeDonationPolicy(t, e, actor, p)
	if _, err := e.service.GetSteward(ctx, actor, id); err == nil {
		t.Fatal("identity grant widened standard scope")
	}
	if _, err := e.service.GetSteward(ctx, actor, parseTestID(t, pending.ID)); !errors.Is(err, ErrNotFound) {
		t.Fatal("unqualified pending exposed", err)
	}
	p.Permissions["donations.review"] = false
	writeDonationPolicy(t, e, actor, p)
	if _, err := e.service.UsesPage(authz.WithStewardPermission(ctx, "donations.browse"), reviewerSteward, actor, parseTestID(t, pending.ID), UseFilter{}, pagination.Default()); !errors.Is(err, ErrNotFound) {
		t.Fatal("maintenance grant exposed unscoped pending uses", err)
	}
	p.Permissions["donations.review"] = true
	p.DonationScope = authz.DonationScopeEnhanced
	p.Permissions["donations.read_all"] = false
	p.Permissions["donations.force_reject"] = true
	writeDonationPolicy(t, e, actor, p)
	tags := []string{"abusive_third_party", "community_charity", "noncommercial_gateway", "user_gateway", "steward_gateway", "invalid_entry"}
	for _, tag := range tags {
		if _, err := e.store.DB().Exec(`INSERT INTO admin_endpoint_tags(base_url,tag) VALUES('https://tagged.example.test/v1',?)`, tag); err != nil {
			t.Fatal(err)
		}
		got, err := e.service.GetSteward(ctx, actor, id)
		if err != nil || len(got.Keys) != 2 || got.Owner != nil || !got.CanForceReject || !got.Keys[1].CanManage {
			t.Fatalf("whole tagged donation %s: %+v %v", tag, got, err)
		}
		if _, err := e.store.DB().Exec(`DELETE FROM admin_endpoint_tags WHERE tag=?`, tag); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := e.store.DB().Exec(`INSERT INTO admin_endpoint_tags(base_url,tag) VALUES('https://tagged.example.test/v1','user_gateway')`); err != nil {
		t.Fatal(err)
	}
	list, err := e.service.DonationsStewardPage(ctx, actor, ManagementFilter{}, pagination.Default())
	if err != nil || list.Pagination.TotalItems != "1" {
		t.Fatalf("scope page count %+v %v", list, err)
	}
	sources, err := e.service.SourcesStewardPage(ctx, actor, SourceFilter{Scope: "all"}, pagination.Default())
	if err != nil || sources.Pagination.TotalItems != "2" {
		t.Fatalf("tagged source expansion %+v %v", sources, err)
	}
	keys, err := e.service.KeysStewardPage(ctx, actor, id, pagination.Default())
	if err != nil || keys.Pagination.TotalItems != "2" {
		t.Fatalf("all donation keys %+v %v", keys, err)
	}
	if _, err := e.service.GetSteward(ctx, actor, parseTestID(t, unrelated.ID)); err == nil {
		t.Fatal("unrelated donation visible")
	}
	note := "Maintained"
	input := KeyManagementInput{ExpectedRevision: 2, SafeNote: &note}
	mutation := donationMutation(t, 'm', http.MethodPatch, routeStewardKey, []int64{id, keyID}, input)
	manageCtx := authz.WithStewardPermission(ctx, "donations.manage_keys")
	if _, err := e.service.ManageKeySession(manageCtx, actor, id, keyID, mutation, input); err != nil {
		t.Fatal("untagged member of tagged donation not writable", err)
	}
	if _, err := e.store.DB().Exec(`DELETE FROM admin_endpoint_tags`); err != nil {
		t.Fatal(err)
	}
	if _, err := e.service.ManageKeySession(manageCtx, actor, id, keyID, mutation, input); !errors.Is(err, ErrNotFound) {
		t.Fatal("tag removal accepted replay", err)
	}
	p.DonationScope = authz.DonationScopeFull
	writeDonationPolicy(t, e, actor, p)
	if _, err := e.service.KeyStewardSession(manageCtx, actor, parseTestID(t, unrelated.ID), parseTestID(t, unrelated.Keys[0].ID)); err != nil {
		t.Fatal("full source scope incorrectly required non-mainstream model grant", err)
	}
	p.Permissions["donations.manage_keys"] = false
	writeDonationPolicy(t, e, actor, p)
	if _, err := e.service.KeyStewardSession(manageCtx, actor, id, keyID); !errors.Is(err, authz.ErrPermissionDenied) {
		t.Fatal("scope granted action", err)
	}
	p.DonationScope = authz.DonationScopeNone
	writeDonationPolicy(t, e, actor, p)
	if _, err := e.service.DonationsStewardPage(ctx, actor, ManagementFilter{}, pagination.Default()); !errors.Is(err, authz.ErrPermissionDenied) {
		t.Fatal("none browsed", err)
	}
	history, err := e.service.UserDonationHistoryPage(ctx, reviewerSteward, actor, owner, ManagementFilter{}, pagination.Default())
	if err != nil || history.Pagination.TotalItems != "3" {
		t.Fatalf("independent history %+v %v", history, err)
	}
	model := controlModel(t, e, "scope-target", true)
	if _, err := e.service.KeyStewardSession(charityscope.WithModel(manageCtx, model), actor, id, keyID); !errors.Is(err, authz.ErrPermissionDenied) {
		t.Fatal("model selector bypassed none", err)
	}
}

func TestCompletedStandardReviewReplaysAcrossIdentityGrantChanges(t *testing.T) {
	e := newDonationTestEnv(t)
	e.seedUser(t, "", nil, true)
	five := int64(5)
	actor := e.seedUser(t, "replay-reviewer", &five, false)
	owner := e.seedUser(t, "replay-donor", nil, false)
	_, key := e.seedEndpointKey(t, owner, 'R')
	d := e.createDonation(t, owner, key)
	model := controlModel(t, e, "review-mainstream", true)
	e.seedBinding(t, model, parseTestID(t, d.Keys[0].ID), "model", 0)
	id := parseTestID(t, d.ID)
	p := authz.DefaultStewardPermissions(5)
	p.Permissions["donations.read_all"] = true
	writeDonationPolicy(t, e, actor, p)
	input := ReviewInput{Decision: "approve", ExpectedRevision: 1, KeySettings: []KeySetting{{DonationKeyID: parseTestID(t, d.Keys[0].ID), Enabled: true}}}
	mutation := donationMutation(t, 'P', http.MethodPost, routeStewardReview, []int64{id}, input)
	for i, full := range []bool{true, false, true} {
		p.Permissions["donations.read_all"] = full
		writeDonationPolicy(t, e, actor, p)
		got, err := e.service.ReviewSteward(context.Background(), actor, id, mutation, input)
		if err != nil || got.Replayed != (i > 0) || len(got.Value.Keys) != 1 {
			t.Fatalf("completed review replay %+v %v", got, err)
		}
		if !full && (got.Value.Owner != nil || got.Value.Keys[0].EndpointKeyID != nil) {
			t.Fatal("stale identity in replay")
		}
	}
	p.DonationScope = authz.DonationScopeNone
	writeDonationPolicy(t, e, actor, p)
	if _, err := e.service.ReviewSteward(context.Background(), actor, id, mutation, input); !errors.Is(err, authz.ErrPermissionDenied) {
		t.Fatal("none replayed review", err)
	}
}

func TestEnhancedHandlingIncludesTaggedPendingAndRevokesReplay(t *testing.T) {
	e := newDonationTestEnv(t)
	e.seedUser(t, "", nil, true)
	five := int64(5)
	actor := e.seedUser(t, "tag-handler", &five, false)
	owner := e.seedUser(t, "tag-owner", nil, false)
	_, physical := e.seedEndpointKey(t, owner, 'T')
	d := e.createDonation(t, owner, physical)
	id := parseTestID(t, d.ID)
	p := authz.DefaultStewardPermissions(5)
	p.DonationScope = authz.DonationScopeEnhanced
	p.Permissions["donations.review"] = false
	writeDonationPolicy(t, e, actor, p)
	ctx := authz.WithStewardPermission(t.Context(), "donations.handle")
	if _, err := e.store.DB().Exec(`INSERT INTO admin_endpoint_tags(base_url,tag) SELECT canonical_base_url,'invalid_entry' FROM donation_keys WHERE donation_id=?`, id); err != nil {
		t.Fatal(err)
	}
	badge, err := e.service.BadgeSteward(ctx, actor)
	if err != nil || badge.PendingCount != "1" {
		t.Fatal("tagged pending badge", badge, err)
	}
	mutation := donationMutation(t, 'H', http.MethodPost, routeStewardProcessed, []int64{id}, map[string]any{"expected_handling_revision": "1"})
	if _, err := e.service.ProcessSteward(ctx, actor, id, mutation, 1); err != nil {
		t.Fatal("tagged pending processing", err)
	}
	if _, err := e.store.DB().Exec(`DELETE FROM admin_endpoint_tags`); err != nil {
		t.Fatal(err)
	}
	if _, err := e.service.ProcessSteward(ctx, actor, id, mutation, 1); !errors.Is(err, ErrNotFound) {
		t.Fatal("removed tag replayed pending handling", err)
	}
}

func TestPagedAndSingleKeyCapabilitiesFollowIndependentGrant(t *testing.T) {
	e := newDonationTestEnv(t)
	e.seedUser(t, "", nil, true)
	five := int64(5)
	actor := e.seedUser(t, "capability-reviewer", &five, false)
	owner := e.seedUser(t, "capability-donor", nil, false)
	_, physical := e.seedEndpointKey(t, owner, 'V')
	d := approvedResetDonation(t, e, owner, physical)
	did, kid := parseTestID(t, d.ID), parseTestID(t, d.Keys[0].ID)
	if _, err := e.store.DB().Exec(`INSERT INTO admin_endpoint_tags(base_url,tag) SELECT canonical_base_url,'user_gateway' FROM donation_keys WHERE id=?`, kid); err != nil {
		t.Fatal(err)
	}
	p := authz.DefaultStewardPermissions(5)
	ctx := authz.WithStewardPermission(t.Context(), "donations.browse")
	for _, scope := range []authz.DonationScope{authz.DonationScopeEnhanced, authz.DonationScopeFull} {
		p.DonationScope = scope
		for _, allowed := range []bool{false, true} {
			p.Permissions["donations.manage_keys"] = allowed
			writeDonationPolicy(t, e, actor, p)
			keys, err := e.service.KeysStewardPage(ctx, actor, did, pagination.Default())
			if err != nil || len(keys.Data) != 1 || keys.Data[0].CanManage != allowed {
				t.Fatalf("key page %s allowed=%t: %+v %v", scope, allowed, keys, err)
			}
			key, err := e.service.KeyStewardSession(ctx, actor, did, kid)
			if err != nil || key.CanManage != allowed {
				t.Fatal("single key capability", key, err)
			}
			sources, err := e.service.SourcesStewardPage(ctx, actor, SourceFilter{Scope: "all"}, pagination.Default())
			if err != nil || len(sources.Data) != 1 {
				t.Fatal(sources, err)
			}
			sourceKeys, err := e.service.SourceKeysStewardPage(ctx, actor, sources.Data[0].SourceKey, SourceFilter{Scope: "all"}, pagination.Default())
			if err != nil || len(sourceKeys.Data) != 1 || sourceKeys.Data[0].CanManage != allowed {
				t.Fatal("source key capability", sourceKeys, err)
			}
		}
	}
}

func TestReviewReplayClearsForceRejectReasonAfterGrantRevocation(t *testing.T) {
	e := newDonationTestEnv(t)
	e.seedUser(t, "", nil, true)
	five := int64(5)
	actor := e.seedUser(t, "receipt-reviewer", &five, false)
	owner := e.seedUser(t, "receipt-donor", nil, false)
	_, physical := e.seedEndpointKey(t, owner, 'W')
	d := e.createDonation(t, owner, physical)
	id := parseTestID(t, d.ID)
	p := authz.DefaultStewardPermissions(5)
	p.DonationScope = authz.DonationScopeFull
	p.Permissions["donations.force_reject"] = true
	writeDonationPolicy(t, e, actor, p)
	input := ReviewInput{Decision: "reject", ExpectedRevision: 1, Reason: "Review rejected"}
	mutation := donationMutation(t, 'J', http.MethodPost, routeStewardReview, []int64{id}, input)
	first, err := e.service.ReviewSteward(t.Context(), actor, id, mutation, input)
	if err != nil || first.Value.CanForceReject || first.Value.ForceRejectUnavailableReason == nil {
		t.Fatal("authorized reason lost", first, err)
	}
	p.Permissions["donations.force_reject"] = false
	writeDonationPolicy(t, e, actor, p)
	replayed, err := e.service.ReviewSteward(t.Context(), actor, id, mutation, input)
	if err != nil || !replayed.Replayed || replayed.Value.CanForceReject || replayed.Value.ForceRejectUnavailableReason != nil {
		t.Fatal("revoked capability kept reason", replayed, err)
	}
}
