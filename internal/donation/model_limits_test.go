package donation

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/donationquota"
	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

func TestModelLimitsOwnershipRevisionReplayAndExport(t *testing.T) {
	e := newDonationTestEnv(t)
	level := int64(6)
	owner := e.seedUser(t, "model-quota-owner", nil, false)
	steward := e.seedUser(t, "model-quota-steward", &level, false)
	stranger := e.seedUser(t, "model-quota-stranger", nil, false)
	e.seedUser(t, "", nil, true)
	_, endpointKey := e.seedEndpointKey(t, owner, 'M')
	d := e.createDonation(t, owner, endpointKey)
	id, key := parseTestID(t, d.ID), parseTestID(t, d.Keys[0].ID)
	if _, err := e.store.DB().Exec(`INSERT INTO donation_key_manual_models(donation_key_id,normalized_model_id,display_name,revision,created_at,updated_at) VALUES(?,'model-a','Model A',1,?,?)`, key, donationTestNow, donationTestNow); err != nil {
		t.Fatal(err)
	}
	limit := "17"
	limits := donationquota.ModelLimits{Tokens: &limit}
	mutation := donationMutation(t, 'M', http.MethodPut, routeStewardModelLimits, []int64{id, key}, map[string]any{"expected_revision": "1", "upstream_model_id": "model-a", "tokens": "17", "input_tokens": nil, "output_tokens": nil})
	receipt, err := e.service.replaceModelLimits(t.Context(), reviewerSteward, steward, id, key, mutation, 1, "model-a", limits)
	if err != nil || receipt.Value.DonationRevision != "2" {
		t.Fatal(receipt, err)
	}
	replayed, err := e.service.replaceModelLimits(t.Context(), reviewerSteward, steward, id, key, mutation, 1, "model-a", limits)
	if err != nil || !replayed.Replayed || !bytes.Equal(receipt.Body, replayed.Body) {
		t.Fatal(replayed, err)
	}
	page, err := e.service.modelLimits(t.Context(), recurringOwner, owner, id, key, pagination.Default(), "", "model-a")
	if err != nil || len(page.Data) != 1 || *page.Data[0].Remaining.Tokens != "17" {
		t.Fatal(page, err)
	}
	if _, err = e.service.modelLimits(t.Context(), recurringOwner, stranger, id, key, pagination.Default(), "", ""); !errors.Is(err, ErrNotFound) {
		t.Fatal("foreign read", err)
	}
	rules := []donationquota.RuleInput{recurringRule()}
	rules[0].Metric = "tokens"
	command := donationMutation(t, 'N', http.MethodPut, routeStewardRecurring, []int64{id, key}, map[string]any{"expected_revision": "2", "rules": rules, "upstream_model_id": "model-a"})
	if _, err = e.service.replaceRecurring(t.Context(), reviewerSteward, steward, id, key, command, 2, rules, "model-a"); err != nil {
		t.Fatal(err)
	}
	whole, err := e.service.RecurringOwner(t.Context(), owner, id, key)
	if err != nil || len(whole.Rules) != 0 {
		t.Fatal("model rule leaked into whole-key scope", whole, err)
	}
	tx, err := e.store.DB().BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	export, err := e.service.ExportUserTx(t.Context(), tx, owner, e.clock.Load(), 100)
	tx.Rollback()
	if err != nil || len(export) != 1 || len(export[0].Keys[0].ModelLimits) != 1 || len(export[0].Keys[0].ModelLimits[0].RecurringLimits) != 1 {
		t.Fatal(export, err)
	}
	e.auth.denySteward.Store(true)
	if _, err = e.service.replaceModelLimits(t.Context(), reviewerSteward, steward, id, key, mutation, 1, "model-a", limits); !errors.Is(err, ErrForbidden) {
		t.Fatal("revoked replay", err)
	}
	tx, err = e.store.DB().BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = e.service.PrepareAccountDeletion(t.Context(), tx, owner, e.clock.Load()); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	var retained int
	if err = e.store.DB().QueryRow(`SELECT count(*) FROM donation_model_quotas q JOIN donation_keys k ON k.id=q.donation_key_id JOIN donations d ON d.id=k.donation_id WHERE q.donation_key_id=? AND d.user_id IS NULL AND d.status='deleted'`, key).Scan(&retained); err != nil || retained != 1 {
		t.Fatal("anonymized model accounting", retained, err)
	}
	if _, err = e.service.modelLimits(t.Context(), recurringOwner, owner, id, key, pagination.Default(), "", ""); !errors.Is(err, ErrNotFound) {
		t.Fatal("deleted owner read", err)
	}
}

func TestModelLimitsExportCapacityIsResourceLimit(t *testing.T) {
	e := newDonationTestEnv(t)
	owner := e.seedUser(t, "model-export-owner", nil, false)
	_, endpointKey := e.seedEndpointKey(t, owner, 'E')
	d := e.createDonation(t, owner, endpointKey)
	key := parseTestID(t, d.Keys[0].ID)
	tx, err := e.store.DB().BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for _, model := range []string{"one", "two"} {
		if err = donationquota.SetModelLimits(t.Context(), tx, key, e.clock.Load(), model, donationquota.ModelLimits{}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = e.service.ExportUserTx(t.Context(), tx, owner, e.clock.Load(), 1); !errors.Is(err, ErrResourceLimit) {
		t.Fatal("model export capacity mapping", err)
	}
	values, err := e.service.ExportUserTx(t.Context(), tx, owner, e.clock.Load(), 2)
	if err != nil || len(values) != 1 || len(values[0].Keys[0].ModelLimits) != 2 {
		t.Fatal("exact model export bound", values, err)
	}
}

func TestModelLimitsL5PermissionAndReplayRecheck(t *testing.T) {
	e := newDonationTestEnv(t)
	e.seedUser(t, "", nil, true)
	five := int64(5)
	actor := e.seedUser(t, "model-policy-reviewer", &five, false)
	owner := e.seedUser(t, "model-policy-owner", nil, false)
	channel := seedMainstreamChannel(t, e, "Quota example", "subscription")
	physical := seedSourcedEndpointKey(t, e, owner, 42, "https://channel.example.test/v1", &donationEndpointSource{channelID: channel, revision: 1, name: "Quota example", category: "subscription"})
	d := e.createDonation(t, owner, physical)
	id, key := parseTestID(t, d.ID), parseTestID(t, d.Keys[0].ID)
	if _, err := e.store.DB().Exec(`INSERT INTO donation_key_manual_models(donation_key_id,normalized_model_id,display_name,revision,created_at,updated_at) VALUES(?,'model-a','Model A',1,?,?)`, key, donationTestNow, donationTestNow); err != nil {
		t.Fatal(err)
	}
	policy := authz.DefaultStewardPermissions(5)
	for name := range policy.Permissions {
		policy.Permissions[name] = false
	}
	policy.Permissions["donations.recurring"] = true
	writeDonationPolicy(t, e, actor, policy)
	mux := http.NewServeMux()
	if err := RegisterStewardRoutes(recurringHTTPRegistrar{mux: mux, actor: actor}, e.service); err != nil {
		t.Fatal(err)
	}
	path := "/api/steward/donations/" + d.ID + "/keys/" + d.Keys[0].ID + "/model-limits"
	body := `{"expected_revision":"1","upstream_model_id":"model-a","tokens":"17","input_tokens":null,"output_tokens":null}`
	request := func(method string) *httptest.ResponseRecorder {
		t.Helper()
		input := ""
		if method == http.MethodPut {
			input = body
		}
		r := httptest.NewRequest(method, path, strings.NewReader(input))
		r.Header.Set("Idempotency-Key", strings.Repeat("P", 22))
		// The service also declares its operation, so an accidentally broad
		// enclosing route context cannot widen this transaction's authority.
		r = r.WithContext(authz.WithStewardPermission(r.Context(), "donations.manage_keys"))
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w
	}
	for _, method := range []string{http.MethodGet, http.MethodPut, http.MethodPut} {
		if response := request(method); response.Code != 200 {
			t.Fatal("recurring-only L5", method, response.Code, response.Body.String())
		}
	}
	policy.Permissions["donations.recurring"] = false
	policy.Permissions["donations.manage_keys"] = true
	writeDonationPolicy(t, e, actor, policy)
	for _, method := range []string{http.MethodGet, http.MethodPut} {
		if response := request(method); response.Code != 403 {
			t.Fatal("manage-keys-only L5", method, response.Code, response.Body.String())
		}
	}
	limit := "17"
	mutation := donationMutation(t, 'P', http.MethodPut, routeStewardModelLimits, []int64{id, key}, map[string]any{"expected_revision": "1", "upstream_model_id": "model-a", "tokens": "17", "input_tokens": nil, "output_tokens": nil})
	ctx := authz.WithStewardPermission(t.Context(), "donations.recurring")
	if _, err := e.service.replaceModelLimits(ctx, reviewerSteward, actor, id, key, mutation, 1, "model-a", donationquota.ModelLimits{Tokens: &limit}); !errors.Is(err, authz.ErrPermissionDenied) {
		t.Fatal("final transaction accepted revoked permission", err)
	}
	var revision int64
	if err := e.store.DB().QueryRow("SELECT revision FROM donations WHERE id=?", id).Scan(&revision); err != nil || revision != 2 {
		t.Fatal("replay or revoked command changed revision", revision, err)
	}
}
