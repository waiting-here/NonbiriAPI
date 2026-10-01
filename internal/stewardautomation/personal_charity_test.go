package stewardautomation

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/charityrouting"
	"github.com/waiting-here/NonbiriAPI/internal/donation"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

func automationMutation(key, method, route string, ids []string, input any) resources.ControlMutation {
	body, _ := json.Marshal(input)
	return resources.ControlMutation{IdempotencyKey: strings.Repeat(key, 22), Method: method, Route: route, PathIDs: ids, CanonicalBody: body}
}
func TestPersonalCharityReadsExcludeProfilesAndRequireLiveLevelSix(t *testing.T) {
	f := newPersonalFixture(t)
	key := f.key(t, "k")
	f.exec(t, `UPDATE endpoint_keys SET note='private-owner-note' WHERE id=?`, key)
	f.exec(t, `UPDATE site_config SET value='1' WHERE key IN ('donation_accept_enabled','charity_enabled')`)
	keyID, _ := strconv.ParseInt(key, 10, 64)
	input := donation.CreateInput{Description: "shared resource", OwnershipAuthorized: true, Keys: []donation.CreateKeyInput{{EndpointKeyID: keyID}}}
	created, err := f.service.donations.Create(f.ctx, f.userID, automationMutation("D", "POST", "/api/donations", nil, input), input)
	if err != nil {
		t.Fatal(err)
	}
	donationID := created.Value.ID
	member := created.Value.Keys[0].ID
	memberID, _ := strconv.ParseInt(member, 10, 64)
	dID, _ := strconv.ParseInt(donationID, 10, 64)
	f.exec(t, `UPDATE users SET level=6 WHERE id=?`, f.userID)
	steward := authz.WithStewardCaller(context.Background(), authz.StewardCaller{UserID: f.userID, Generation: 1})
	setting := donation.KeySetting{DonationKeyID: memberID, Enabled: true, SafeNote: "safe member note"}
	if revision := created.Value.Keys[0].Review.Revision; revision != nil {
		parsed, _ := strconv.ParseInt(*revision, 10, 64)
		setting.ExpectedReviewRevision = &parsed
	}
	review := donation.ReviewInput{Decision: "approve", ExpectedRevision: 1, Reason: "approved", KeySettings: []donation.KeySetting{setting}}
	if _, err = f.service.donations.ReviewSteward(steward, f.userID, dID, automationMutation("R", "POST", "/api/steward/donations/{id}/review", []string{donationID}, review), review); err != nil {
		t.Fatal(err)
	}
	bindBody := fmt.Sprintf(`{"endpoint_key_ids":["%s"],"upstream_model_id":"exact/model"}`, key)
	decodeBatch(t, f.request(f.ctx, "POST", f.bindingPath(), strings.Repeat("M", 22), bindBody), 200)
	price, reward := "0", "0"
	modelInput := charityrouting.ModelCreate{Provider: "safe", Model: "charity", Enabled: true, Pricing: charityrouting.PricingInput{Mode: "per_request", UserPrice: &price, DonorReward: &reward}}
	model, err := f.service.charity.CreateSteward(steward, f.userID, automationMutation("C", "POST", "/api/steward/charity-models", nil, modelInput), modelInput)
	if err != nil {
		t.Fatal(err)
	}
	modelID, _ := strconv.ParseInt(model.Value.ID, 10, 64)
	selections := charityrouting.BindingBatch{ExpectedBindingRevision: model.Value.BindingRevision, Selections: []charityrouting.BindingSelection{{DonationKeyID: member, UpstreamModelID: "exact/model"}}}
	if _, err = f.service.charity.AddBindingsSteward(steward, f.userID, modelID, automationMutation("B", "POST", "/api/steward/charity-models/{id}/bindings/batch", []string{model.Value.ID}, selections), selections); err != nil {
		t.Fatal(err)
	}
	paths := []string{DonationsPath, StewardPrefix + "donations/" + donationID, StewardPrefix + "donations/" + donationID + "/keys", StewardPrefix + "donations/" + donationID + "/keys/" + member, StewardPrefix + "donations/" + donationID + "/keys/" + member + "/catalog", StewardPrefix + "charity-models", StewardPrefix + "charity-models/" + model.Value.ID, StewardPrefix + "charity-models/" + model.Value.ID + "/bindings", StewardPrefix + "charity-models/" + model.Value.ID + "/binding-candidates"}
	before := f.count(t, `SELECT COUNT(*) FROM idempotency_records`)
	for _, path := range paths {
		w := f.request(steward, "GET", path, "", "")
		if w.Code != 200 {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
		}
		for _, forbidden := range []string{"private-owner-note", "fixture-upstream", "\"owner\"", "\"reviewer\"", "discord_id", "encrypted_secret", "fingerprint", "secret_ref"} {
			if strings.Contains(w.Body.String(), forbidden) {
				t.Fatalf("%s exposed %s", path, forbidden)
			}
		}
	}
	if f.count(t, `SELECT COUNT(*) FROM idempotency_records`) != before || f.calls.Load() != 0 {
		t.Fatal("charity GET wrote or called upstream")
	}
	f.exec(t, `UPDATE endpoint_keys SET enabled=0 WHERE id=?`, key)
	bindings := f.request(steward, "GET", StewardPrefix+"charity-models/"+model.Value.ID+"/bindings", "", "")
	if bindings.Code != 200 || !strings.Contains(bindings.Body.String(), `"state":"disabled"`) {
		t.Fatalf("disabled current binding %d %s", bindings.Code, bindings.Body.String())
	}
	f.exec(t, `UPDATE users SET level=5 WHERE id=?`, f.userID)
	for _, path := range append(paths, FailurePolicyPath) {
		if w := f.request(steward, "GET", path, "", ""); w.Code != 403 {
			t.Fatalf("L5 charity GET accepted: %s %d", path, w.Code)
		}
	}
	for _, path := range []string{DonationsPath, BindingsPath, FailurePolicyPath} {
		method := "POST"
		if path == FailurePolicyPath {
			method = "PATCH"
		}
		if w := f.request(steward, method, path, strings.Repeat("F", 22), `{}`); w.Code != 403 {
			t.Fatalf("L5 charity write accepted %s %d", path, w.Code)
		}
	}
	if w := f.request(f.ctx, "GET", PersonalPrefix+"models", "", ""); w.Code != 200 {
		t.Fatalf("L5 personal rejected %d", w.Code)
	}
	if w := f.request(f.ctx, "GET", StewardPrefix+"charity-models", "", ""); w.Code != 401 {
		t.Fatalf("personal purpose leaked steward authority %d", w.Code)
	}
}
