package donation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/charityscope"
	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

func controlModel(t *testing.T, e *donationTestEnv, name string, mainstream bool) int64 {
	t.Helper()
	result, err := e.store.DB().Exec(`INSERT INTO charity_models(provider,model,full_name,enabled,pricing_mode,is_mainstream,created_at,updated_at) VALUES('example',?,?,1,'per_request',?,?,?)`, name, "[公益]example/"+name, mainstream, donationTestNow, donationTestNow)
	if err != nil {
		t.Fatal(err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestTraineeKeyScopeAllowsUnboundMainstreamAndOnlyBoundCustom(t *testing.T) {
	e := newDonationTestEnv(t)
	e.seedUser(t, "", nil, true)
	five, six := int64(5), int64(6)
	trainee := e.seedUser(t, "scoped-trainee", &five, false)
	owner := e.seedUser(t, "scoped-owner", &six, false)
	channel := seedMainstreamChannel(t, e, "Example channel", "subscription")
	mainPhysical := seedSourcedEndpointKey(t, e, owner, 31, "https://channel.example.test/v1", &donationEndpointSource{channelID: channel, revision: 1, name: "Example channel", category: "subscription"})
	mainDonation := e.createDonationWithSeed(t, owner, 'm', mainPhysical)
	mainID, mainKey := parseTestID(t, mainDonation.ID), parseTestID(t, mainDonation.Keys[0].ID)
	_, customPhysical := e.seedEndpointKey(t, owner, 'x')
	custom := e.createDonationWithSeed(t, owner, 'n', customPhysical)
	customID, customKey := parseTestID(t, custom.ID), parseTestID(t, custom.Keys[0].ID)
	if custom.Status != "approved" {
		_, err := e.service.ReviewAdmin(context.Background(), donationMutation(t, 'p', http.MethodPost, routeAdminReview, []int64{customID}, map[string]any{"approve": true}), customID, ReviewInput{Decision: "approve", ExpectedRevision: 1, Reason: "approved note", KeySettings: []KeySetting{{DonationKeyID: customKey, Enabled: true}}})
		if err != nil {
			t.Fatal(err)
		}
	}
	model := controlModel(t, e, "managed", true)
	other := controlModel(t, e, "private-model-name", false)
	ctx := charityscope.WithModel(context.Background(), model)
	if _, err := e.service.KeyStewardSession(context.Background(), trainee, mainID, mainKey); !errors.Is(err, ErrNotFound) {
		t.Fatal("missing model context", err)
	}
	if _, err := e.service.KeyStewardSession(charityscope.WithModel(ctx, other), trainee, mainID, mainKey); !errors.Is(err, ErrNotFound) {
		t.Fatal("ordinary model accepted", err)
	}
	view, err := e.service.KeyStewardSession(ctx, trainee, mainID, mainKey)
	if err != nil || view.BindingCount != "0" || view.DonationNote != "donor description" {
		t.Fatalf("unbound mainstream view: %+v %v", view, err)
	}
	if _, err := e.service.KeyStewardSession(ctx, trainee, customID, customKey); !errors.Is(err, ErrNotFound) {
		t.Fatal("unbound custom key exposed", err)
	}
	if _, err := e.store.DB().Exec(`INSERT INTO model_pair_catalog(endpoint_key_id,normalized_model_id,manual_supports,updated_at) VALUES(?,'upstream',1,?)`, customPhysical, donationTestNow); err != nil {
		t.Fatal(err)
	}
	for _, modelID := range []int64{model, other} {
		if _, err := e.store.DB().Exec(`INSERT INTO charity_model_bindings(charity_model_id,donation_key_id,endpoint_key_id,upstream_model_id,ord,created_at,updated_at) VALUES(?,?,?,'upstream',0,?,?)`, modelID, customKey, customPhysical, donationTestNow, donationTestNow); err != nil {
			t.Fatal(err)
		}
	}
	bound, err := e.service.KeyStewardSession(ctx, trainee, customID, customKey)
	if err != nil || bound.BindingCount != "2" || len(bound.VisibleModels) != 1 || bound.VisibleModels[0].ModelID != fmt.Sprint(model) {
		t.Fatalf("shared impact: %+v %v", bound, err)
	}
	body, _ := json.Marshal(bound)
	if bound.EndpointKeyID != nil {
		t.Fatal("scope disclosed a physical key identifier")
	}
	for _, forbidden := range []string{"private-model-name", "encrypted_secret", "scoped-owner"} {
		if strings.Contains(string(body), forbidden) {
			t.Fatal("scope disclosed", forbidden)
		}
	}
	if _, err := e.service.GetSteward(ctx, trainee, customID); !errors.Is(err, ErrForbidden) {
		t.Fatal("full donation exposed", err)
	}
	sources, err := e.service.SourcesStewardPage(ctx, trainee, SourceFilter{}, pagination.Default())
	if err != nil || sources.Pagination.TotalItems != "2" {
		t.Fatalf("scoped sources: %+v %v", sources, err)
	}
	note := "maintained without a model discovery"
	input := KeyManagementInput{ExpectedRevision: parseTestID(t, view.DonationRevision), SafeNote: &note}
	mutation := donationMutation(t, 'q', http.MethodPatch, routeStewardKey, []int64{mainID, mainKey}, map[string]any{"safe_note": note})
	mutation.Query = charityscope.Query(ctx)
	updated, err := e.service.ManageKeySession(ctx, trainee, mainID, mainKey, mutation, input)
	if err != nil {
		t.Fatal(err)
	}
	receipt, ok := updated.Value.(ManagedKeyReceipt)
	if !ok || receipt.Key.SafeNote != note {
		t.Fatalf("receipt=%+v", updated.Value)
	}
	if _, err := e.service.ManageKeySession(ctx, trainee, mainID, mainKey, mutation, input); err != nil {
		t.Fatal("replay", err)
	}
	e.auth.denySession.Store(true)
	if _, err := e.service.ManageKeySession(ctx, trainee, mainID, mainKey, mutation, input); err == nil {
		t.Fatal("revoked session replay accepted")
	}
	e.auth.denySession.Store(false)
	if _, err := e.store.DB().Exec(`UPDATE charity_models SET is_mainstream=0,revision=revision+1 WHERE id=?`, model); err != nil {
		t.Fatal(err)
	}
	if _, err := e.service.ManageKeySession(ctx, trainee, mainID, mainKey, mutation, input); !errors.Is(err, ErrNotFound) {
		t.Fatal("unflagged model replay accepted", err)
	}
}

func TestSplitTokenControlValidationAndAtomicProjection(t *testing.T) {
	e := newDonationTestEnv(t)
	e.seedUser(t, "", nil, true)
	owner := e.seedUser(t, "split-owner", nil, false)
	_, physical := e.seedEndpointKey(t, owner, 's')
	d := approvedResetDonation(t, e, owner, physical)
	id, key := parseTestID(t, d.ID), parseTestID(t, d.Keys[0].ID)
	pointer := func(value string) **string { v := &value; return &v }
	for n, invalid := range []string{"01", "-1", "+1", "1e3", "9223372036854775808"} {
		input := KeyManagementInput{ExpectedRevision: 2, InputTokensLimit: pointer(invalid), InputTokenReserve: pointer("1"), OutputTokenReserve: pointer("1")}
		if _, err := e.service.ManageKeyAdmin(context.Background(), id, key, donationMutation(t, byte('a'+n), http.MethodPatch, routeAdminKey, []int64{id, key}, map[string]any{"input": invalid}), input); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("invalid %s accepted: %v", invalid, err)
		}
	}
	input := KeyManagementInput{ExpectedRevision: 2, InputTokensLimit: pointer("0"), OutputTokensLimit: pointer("100"), InputTokenReserve: pointer("3"), OutputTokenReserve: pointer("7")}
	updated, err := e.service.ManageKeyAdmin(context.Background(), id, key, donationMutation(t, 'v', http.MethodPatch, routeAdminKey, []int64{id, key}, map[string]any{"split": true}), input)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Value.Revision != "3" || *updated.Value.Keys[0].Limits.InputTokens != "0" || updated.Value.Keys[0].CharityState != "exhausted" {
		t.Fatalf("projection %+v", updated.Value)
	}
	ownerView, err := e.service.GetOwner(context.Background(), owner, id)
	if err != nil || *ownerView.Keys[0].InputTokenReserve != "3" || *ownerView.Keys[0].OutputTokenReserve != "7" || ownerView.Keys[0].Usage.InputTokensUsed != "0" || ownerView.Keys[0].BreakdownStartedAt != donationTestNow {
		t.Fatalf("owner split projection %+v %v", ownerView, err)
	}
	var unlimited *string
	badClear := KeyManagementInput{ExpectedRevision: 3, InputTokenReserve: &unlimited, OutputTokenReserve: &unlimited}
	if _, err := e.service.ManageKeyAdmin(context.Background(), id, key, donationMutation(t, 'w', http.MethodPatch, routeAdminKey, []int64{id, key}, map[string]any{"clear": true}), badClear); !errors.Is(err, ErrInvalidRequest) {
		t.Fatal("active limit accepted absent reservations", err)
	}
	var revision int
	var in, out int64
	if err := e.store.DB().QueryRow(`SELECT d.revision,k.input_token_reserve,k.output_token_reserve FROM donations d JOIN donation_keys k ON k.donation_id=d.id WHERE k.id=?`, key).Scan(&revision, &in, &out); err != nil || revision != 3 || in != 3 || out != 7 {
		t.Fatal("partial invalid write", revision, in, out, err)
	}
}
