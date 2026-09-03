package charityrouting

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/connector/openai"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/dbtest"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

const routingTestNow int64 = 1_700_000_000

type routingTestAuth struct{ denySteward atomic.Bool }

func (auth *routingTestAuth) AuthorizeAdminMutation(ctx context.Context, tx *sql.Tx, userID int64) error {
	var admin int
	if err := tx.QueryRowContext(ctx, `SELECT is_admin FROM users WHERE id=?`, userID).Scan(&admin); err != nil {
		return ErrUnauthorized
	}
	if admin != 1 {
		return ErrForbidden
	}
	return nil
}

func (auth *routingTestAuth) AuthorizeStewardMutation(ctx context.Context, tx *sql.Tx, userID int64) error {
	if auth.denySteward.Load() {
		return ErrForbidden
	}
	var level sql.NullInt64
	var banned int
	if err := tx.QueryRowContext(ctx, `SELECT level,is_banned FROM users WHERE id=?`, userID).Scan(&level, &banned); err != nil {
		return ErrUnauthorized
	}
	if banned != 0 || !level.Valid || level.Int64 != 5 {
		return ErrForbidden
	}
	return nil
}

type routingDonationState struct {
	dueCalls atomic.Int64
	dueHook  func(context.Context, *sql.Tx, int64, int) error
}

func (*routingDonationState) MaterializeExpiryTx(context.Context, *sql.Tx, int64, int64) (bool, error) {
	return false, nil
}
func (state *routingDonationState) MaterializeDueExpiriesTx(ctx context.Context, tx *sql.Tx, now int64, limit int) error {
	state.dueCalls.Add(1)
	if state.dueHook != nil {
		return state.dueHook(ctx, tx, now, limit)
	}
	return nil
}

type routingTestEnv struct {
	store   *db.Store
	vault   *secret.Vault
	service *Service
	auth    *routingTestAuth
	state   *routingDonationState
	clock   *atomic.Int64
}

func newRoutingTestEnv(t *testing.T) *routingTestEnv {
	t.Helper()
	master := bytes.Repeat([]byte{0x52}, secret.MasterKeyBytes)
	vault, err := secret.New(master)
	clear(master)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = vault.Close() })
	path := filepath.Join(t.TempDir(), "routing.sqlite")
	dbtest.EnsureOwnerOnlyParent(t, path)
	store, err := db.Open(path, vault)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if _, err := store.DB().Exec(`UPDATE site_config SET value='1' WHERE key='charity_enabled'`); err != nil {
		t.Fatal(err)
	}
	clock := &atomic.Int64{}
	clock.Store(routingTestNow)
	auth := &routingTestAuth{}
	state := &routingDonationState{}
	service, err := New(Config{Store: store, RoleAuth: auth, DonationState: state, CursorKeys: vault,
		Now: func() time.Time { return time.Unix(clock.Load(), 0) }})
	if err != nil {
		t.Fatal(err)
	}
	return &routingTestEnv{store: store, vault: vault, service: service, auth: auth, state: state, clock: clock}
}

func (environment *routingTestEnv) seedUser(t *testing.T, admin bool, level *int64) int64 {
	t.Helper()
	zero := make([]byte, 16)
	var discord any = fmt.Sprintf("routing-%d-%v", time.Now().UnixNano(), admin)
	if admin {
		discord = nil
	}
	result, err := environment.store.DB().Exec(`INSERT INTO users(
discord_id,username,is_admin,donation_credit_mag,level,total_requests,total_uncached_input_tokens,
total_cache_write_input_tokens,total_cache_read_input_tokens,total_output_tokens,total_unknown_usage_requests,
revision,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, discord, "routing", boolInt(admin), zero,
		level, zero, zero, zero, zero, zero, zero, zero, routingTestNow, routingTestNow)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := result.LastInsertId()
	return id
}

func (environment *routingTestEnv) seedUserBalance(t *testing.T, userID int64, decimal string) {
	t.Helper()
	magnitude, err := db.ParseU128Decimal(decimal)
	if err != nil {
		t.Fatal(err)
	}
	sign := 1
	if decimal == "0" {
		sign = 0
	}
	if _, err := environment.store.DB().Exec(`INSERT INTO credit_accounts(
kind,user_id,code,balance_sign,balance_mag,created_at,updated_at)
VALUES('user',?,NULL,?,?,?,?)`, userID, sign, db.EncodeU128(magnitude), routingTestNow, routingTestNow); err != nil {
		t.Fatal(err)
	}
}

func routingMutation(t *testing.T, seed byte, method, route string, ids []int64, body any) resources.ControlMutation {
	t.Helper()
	canonical, err := idempotency.CanonicalJSON(body)
	if err != nil {
		t.Fatal(err)
	}
	pathIDs := make([]string, len(ids))
	for index, id := range ids {
		pathIDs[index] = fmt.Sprintf("%d", id)
	}
	return resources.ControlMutation{IdempotencyKey: strings.Repeat(string(seed), 22), Method: method,
		Route: route, PathIDs: pathIDs, CanonicalBody: canonical}
}

func testModelCreate() ModelCreate {
	userPrice, reward := "3", "1.25"
	return ModelCreate{Provider: "provider", Model: "model", Enabled: true,
		Pricing:          PricingInput{Mode: "per_request", UserPrice: &userPrice, DonorReward: &reward},
		Discount:         DiscountInput{Enabled: true, Percent: 80, StartAt: pointerInt64(routingTestNow - 1), EndAt: pointerInt64(routingTestNow + 100)},
		FlattenToolCalls: true}
}

func (environment *routingTestEnv) createModel(t *testing.T, seed byte) AdminCharityModel {
	t.Helper()
	input := testModelCreate()
	result, err := environment.service.CreateAdmin(context.Background(),
		routingMutation(t, seed, http.MethodPost, routeAdminModels, nil, map[string]any{"seed": string(seed)}), input)
	if err != nil {
		t.Fatal(err)
	}
	return result.Value
}

func (environment *routingTestEnv) setCapabilityGates(t *testing.T, charity, donation string) {
	t.Helper()
	result, err := environment.store.DB().Exec(`UPDATE site_config
SET value=CASE key WHEN 'charity_enabled' THEN ? WHEN 'donation_accept_enabled' THEN ? END
WHERE key IN ('charity_enabled','donation_accept_enabled')`, charity, donation)
	if err != nil {
		t.Fatal(err)
	}
	changed, err := result.RowsAffected()
	if err != nil || changed != 2 {
		t.Fatalf("capability gate rows changed = %d, %v", changed, err)
	}
}

func TestModelRevisionNestedDiscountAndRoleAuthorization(t *testing.T) {
	environment := newRoutingTestEnv(t)
	environment.seedUser(t, true, nil)
	level := int64(5)
	steward := environment.seedUser(t, false, &level)
	input := testModelCreate()
	mutation := routingMutation(t, 'A', http.MethodPost, routeAdminModels, nil, map[string]any{"create": true})
	created, err := environment.service.CreateAdmin(context.Background(), mutation, input)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := environment.service.CreateAdmin(context.Background(), mutation, input)
	if err != nil || !replay.Replayed || !bytes.Equal(created.Body, replay.Body) {
		t.Fatalf("create replay mismatch: replayed=%v err=%v", replay.Replayed, err)
	}
	modelID, _ := parsePositiveID(created.Value.ID)
	percent := 60
	patch := ModelPatch{ExpectedRevision: "1", Discount: &DiscountPatchInput{Percent: &percent}}
	patched, err := environment.service.PatchAdmin(context.Background(), modelID,
		routingMutation(t, 'B', http.MethodPatch, routeAdminModel, []int64{modelID}, map[string]any{"discount": 60}), patch)
	if err != nil {
		t.Fatal(err)
	}
	if patched.Value.Revision != "2" || patched.Value.Discount.Percent != 60 || !patched.Value.Discount.Enabled ||
		patched.Value.Discount.StartAt == nil || *patched.Value.Discount.StartAt != routingTestNow-1 {
		t.Fatalf("nested discount merge lost fields: %+v", patched.Value.Discount)
	}
	if _, err := environment.service.PatchAdmin(context.Background(), modelID,
		routingMutation(t, 'C', http.MethodPatch, routeAdminModel, []int64{modelID}, map[string]any{"stale": true}), patch); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale patch error = %v, want conflict", err)
	}
	environment.auth.denySteward.Store(true)
	if _, err := environment.service.PatchSteward(context.Background(), steward, modelID,
		routingMutation(t, 'D', http.MethodPatch, routeStewardModel, []int64{modelID}, map[string]any{"enabled": false}),
		ModelPatch{ExpectedRevision: "2", Enabled: pointerBool(false)}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("revoked steward patch error = %v, want forbidden", err)
	}
}

func (environment *routingTestEnv) seedCandidate(t *testing.T, ownerID int64, suffix byte, upstream string) (int64, int64, int64) {
	return environment.seedCandidateWithConnector(t, ownerID, string(rune(suffix)), upstream,
		connectorcontract.TypeOpenAICompatible)
}

func (environment *routingTestEnv) seedCandidateWithConnector(t *testing.T, ownerID int64, identity, upstream string, connectorType connectorcontract.Type) (int64, int64, int64) {
	t.Helper()
	now := environment.clock.Load()
	baseURL := fmt.Sprintf("https://%s.routing.test/v1", identity)
	result, err := environment.store.DB().Exec(`INSERT INTO endpoints(user_id,connector_type,base_url,note,enabled,revision,created_at,updated_at)
VALUES(?,?,?,'private',1,1,?,?)`, ownerID, string(connectorType), baseURL, now, now)
	if err != nil {
		t.Fatal(err)
	}
	endpointID, _ := result.LastInsertId()
	digest := sha256.Sum256([]byte(identity))
	contextID := append([]byte(nil), digest[:16]...)
	fingerprint := append([]byte(nil), digest[:]...)
	result, err = environment.store.DB().Exec(`INSERT INTO endpoint_key_secrets(
context_id,canonical_base_url,connector_type,encrypted_secret,created_at) VALUES(?,?,?,'envelope',?)`, contextID, baseURL, string(connectorType), now)
	if err != nil {
		t.Fatal(err)
	}
	secretID, _ := result.LastInsertId()
	result, err = environment.store.DB().Exec(`INSERT INTO endpoint_keys(
endpoint_id,secret_ref_id,secret_fingerprint,display_head,display_tail,note,enabled,force_store_false,revision,created_at,updated_at)
VALUES(?,?,?,'head','tail','private key note',1,1,1,?,?)`, endpointID, secretID, fingerprint, now, now)
	if err != nil {
		t.Fatal(err)
	}
	endpointKeyID, _ := result.LastInsertId()
	result, err = environment.store.DB().Exec(`INSERT INTO donations(
user_id,status,revision,description,review_note,reviewed_by_role,created_at,updated_at)
VALUES(?,'approved',1,'description','','admin',?,?)`, ownerID, now, now)
	if err != nil {
		t.Fatal(err)
	}
	donationID, _ := result.LastInsertId()
	zero, one := db.EncodeU128(db.U128{}), db.EncodeU128(db.U128{15: 1})
	result, err = environment.store.DB().Exec(`INSERT INTO donation_keys(
donation_id,endpoint_key_id,display_head,display_tail,canonical_base_url,connector_type,
price_used_mag,price_reserved_mag,calls_used,calls_reserved,tokens_used,tokens_reserved,
failure_streak,streak_generation,next_claim_seq,next_fold_seq,enabled,token_reserve,safe_note,created_at,updated_at,
authorized_expires_at,expires_at,source_endpoint_key_id,report_fingerprint)
VALUES(?,?,?,?,?,?,
?,?,?,?,?,?,?,?,?,?,1,10,'safe label',?,?,NULL,NULL,?,?)`, donationID, endpointKeyID,
		"head", "tail", baseURL, string(connectorType), zero, zero, zero, zero, zero, zero, zero, one, one, one, now, now,
		endpointKeyID, fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	donationKeyID, _ := result.LastInsertId()
	if _, err := environment.store.DB().Exec(`INSERT INTO donation_key_memberships(endpoint_key_id,donation_key_id,donation_id,created_at)
VALUES(?,?,?,?)`, endpointKeyID, donationKeyID, donationID, now); err != nil {
		t.Fatal(err)
	}
	if _, err := environment.store.DB().Exec(`INSERT INTO model_pair_catalog(
endpoint_key_id,normalized_model_id,automatic_supports,manual_supports,automatic_revision,pair_revision,updated_at)
VALUES(?,?,1,1,1,1,?)`, endpointKeyID, upstream, now); err != nil {
		t.Fatal(err)
	}
	return donationID, donationKeyID, endpointKeyID
}

func TestBindingCandidateRuntimeCapacityAndSignedCursor(t *testing.T) {
	environment := newRoutingTestEnv(t)
	environment.seedUser(t, true, nil)
	owner := environment.seedUser(t, false, nil)
	model := environment.createModel(t, 'E')
	modelID, _ := parsePositiveID(model.ID)
	donationID, donationKeyID, _ := environment.seedCandidate(t, owner, 'x', "upstream-model")

	items, nextKey, nextModel, err := environment.service.BindingCandidatesAdmin(context.Background(), modelID,
		CandidateQuery{DonationID: donationID, Limit: 50})
	if err != nil || len(items) != 1 || nextKey != 0 || nextModel != "" || items[0].DonationKeyID != fmt.Sprint(donationKeyID) {
		t.Fatalf("candidate list = %+v next=%d/%q err=%v", items, nextKey, nextModel, err)
	}
	batch := BindingBatch{ExpectedBindingRevision: "0", Selections: []BindingSelection{{
		DonationKeyID: fmt.Sprint(donationKeyID), UpstreamModelID: "upstream-model",
	}}}
	bindings, err := environment.service.AddBindingsAdmin(context.Background(), modelID,
		routingMutation(t, 'F', http.MethodPost, routeAdminBindingBatch, []int64{modelID}, map[string]any{"binding": true}), batch)
	if err != nil || len(bindings.Value.Bindings) != 1 || bindings.Value.BindingRevision != "1" {
		t.Fatalf("add bindings = %+v err=%v", bindings.Value, err)
	}
	snapshot, err := environment.service.Snapshot(context.Background(), modelID, routingTestNow,
		[]connectorcontract.Type{connectorcontract.TypeOpenAICompatible})
	if err != nil || len(snapshot.Candidates()) != 1 || snapshot.Candidates()[0].DonationKeyID != donationKeyID || snapshot.ReservedMilli != 2400 {
		t.Fatalf("snapshot = %+v candidates=%+v err=%v", snapshot, snapshot.Candidates(), err)
	}
	zero := db.EncodeU128(db.U128{})
	if _, err := environment.store.DB().Exec(`UPDATE donation_keys SET call_limit_mag=? WHERE id=?`, zero, donationKeyID); err != nil {
		t.Fatal(err)
	}
	if _, err := environment.service.Snapshot(context.Background(), modelID, routingTestNow,
		[]connectorcontract.Type{connectorcontract.TypeOpenAICompatible}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("zero call cap snapshot error = %v, want unavailable", err)
	}
	if _, err := environment.store.DB().Exec(`UPDATE donation_keys SET call_limit_mag=NULL WHERE id=?`, donationKeyID); err != nil {
		t.Fatal(err)
	}

	scope := "admin-charity-models"
	ownerCursor := paginationOwner(roleAdmin, 0, "", "")
	token, err := environment.service.encodeModelCursor(scope, ownerCursor, routingTestNow, modelID)
	if err != nil {
		t.Fatal(err)
	}
	if decoded, err := environment.service.decodeModelCursor(token, scope, ownerCursor, routingTestNow); err != nil || decoded != modelID {
		t.Fatalf("cursor decode = %d, %v", decoded, err)
	}
	if _, err := environment.service.decodeModelCursor(token, scope, paginationOwner(roleAdmin, 0, "changed", ""), routingTestNow); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("cross-filter cursor error = %v, want invalid", err)
	}
	if environment.state.dueCalls.Load() == 0 {
		t.Fatal("routing reads did not invoke expiry owner")
	}
}

func TestRuntimePreflightIsCandidateFreeAndEnforcesCallerPolicy(t *testing.T) {
	environment := newRoutingTestEnv(t)
	environment.seedUser(t, true, nil)
	caller := environment.seedUser(t, false, nil)
	environment.seedUserBalance(t, caller, "100000")
	model := environment.createModel(t, 'M')
	modelID, _ := parsePositiveID(model.ID)
	if _, err := environment.store.DB().Exec(`UPDATE site_config SET value='3' WHERE key='charity_min_chars'`); err != nil {
		t.Fatal(err)
	}
	request, err := openai.DecodeChatRequest(strings.NewReader(`{"model":"[公益]provider/model","messages":[{"role":"user","content":"hello"}]}`), openai.MaxRequestBodyBytes)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(request.Clear)

	preflight, err := environment.service.Preflight(context.Background(), caller, request.Model, request, routingTestNow)
	if err != nil {
		t.Fatal(err)
	}
	if preflight.ModelID != modelID || preflight.FullName != request.Model || preflight.ReservedMilli != 2400 || !preflight.FlattenToolCalls {
		t.Fatalf("preflight=%+v", preflight)
	}
	if environment.state.dueCalls.Load() != 0 {
		t.Fatalf("candidate-free preflight materialized donation state %d times", environment.state.dueCalls.Load())
	}
	if _, err := environment.service.Snapshot(context.Background(), modelID, routingTestNow,
		[]connectorcontract.Type{connectorcontract.TypeOpenAICompatible}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("candidate-free model snapshot error=%v", err)
	}
	if environment.state.dueCalls.Load() != 1 {
		t.Fatalf("snapshot expiry calls=%d", environment.state.dueCalls.Load())
	}

	short, err := openai.DecodeChatRequest(strings.NewReader(`{"model":"[公益]provider/model","messages":[{"role":"user","content":"hi"}]}`), openai.MaxRequestBodyBytes)
	if err != nil {
		t.Fatal(err)
	}
	defer short.Clear()
	if _, err := environment.service.Preflight(context.Background(), caller, short.Model, short, routingTestNow); !errors.Is(err, ErrContentTooShort) {
		t.Fatalf("short-content error=%v", err)
	}
	if _, err := environment.store.DB().Exec(`UPDATE users SET charity_suspended_until=? WHERE id=?`, routingTestNow+1, caller); err != nil {
		t.Fatal(err)
	}
	if _, err := environment.service.Preflight(context.Background(), caller, request.Model, request, routingTestNow); !errors.Is(err, ErrCharitySuspended) {
		t.Fatalf("suspended error=%v", err)
	}
	if _, err := environment.store.DB().Exec(`UPDATE users SET charity_suspended_until=NULL WHERE id=?`, caller); err != nil {
		t.Fatal(err)
	}
	zero := db.EncodeU128(db.U128{})
	if _, err := environment.store.DB().Exec(`UPDATE credit_accounts SET balance_sign=0,balance_mag=? WHERE user_id=?`, zero, caller); err != nil {
		t.Fatal(err)
	}
	if _, err := environment.service.Preflight(context.Background(), caller, request.Model, request, routingTestNow); !errors.Is(err, ErrInsufficientCredits) {
		t.Fatalf("insufficient-credit error=%v", err)
	}
}

func TestAvailableModelListTracksGateCandidateAndStableBound(t *testing.T) {
	environment := newRoutingTestEnv(t)
	environment.seedUser(t, true, nil)
	owner := environment.seedUser(t, false, nil)
	first := environment.createModel(t, 'N')
	firstID, _ := parsePositiveID(first.ID)
	secondInput := testModelCreate()
	secondInput.Provider = "alpha"
	secondInput.Model = "second"
	secondResult, err := environment.service.CreateAdmin(context.Background(),
		routingMutation(t, 'O', http.MethodPost, routeAdminModels, nil, map[string]any{"second": true}), secondInput)
	if err != nil {
		t.Fatal(err)
	}
	secondID, _ := parsePositiveID(secondResult.Value.ID)

	models, err := environment.service.ListAvailableModels(context.Background(), routingTestNow, 10)
	if err != nil || len(models) != 0 {
		t.Fatalf("unbound available models=%+v err=%v", models, err)
	}
	_, firstKey, _ := environment.seedCandidate(t, owner, 'm', "upstream-first")
	_, secondKey, _ := environment.seedCandidate(t, owner, 'n', "upstream-second")
	for _, binding := range []struct {
		modelID  int64
		keyID    int64
		upstream string
		seed     byte
	}{
		{modelID: firstID, keyID: firstKey, upstream: "upstream-first", seed: 'P'},
		{modelID: secondID, keyID: secondKey, upstream: "upstream-second", seed: 'Q'},
	} {
		if _, err := environment.service.AddBindingsAdmin(context.Background(), binding.modelID,
			routingMutation(t, binding.seed, http.MethodPost, routeAdminBindingBatch, []int64{binding.modelID}, map[string]any{"bind": binding.upstream}),
			BindingBatch{ExpectedBindingRevision: "0", Selections: []BindingSelection{{
				DonationKeyID: fmt.Sprint(binding.keyID), UpstreamModelID: binding.upstream,
			}}}); err != nil {
			t.Fatal(err)
		}
	}
	models, err = environment.service.ListAvailableModels(context.Background(), routingTestNow, 10)
	if err != nil || len(models) != 2 || models[0].FullName != "[公益]alpha/second" || models[1].FullName != "[公益]provider/model" {
		t.Fatalf("available models=%+v err=%v", models, err)
	}
	if _, err := environment.service.ListAvailableModels(context.Background(), routingTestNow, 1); !errors.Is(err, ErrResourceLimit) {
		t.Fatalf("available model bound error=%v", err)
	}
	if _, err := environment.store.DB().Exec(`UPDATE site_config SET value='0' WHERE key='charity_enabled'`); err != nil {
		t.Fatal(err)
	}
	models, err = environment.service.ListAvailableModels(context.Background(), routingTestNow, 10)
	if err != nil || models == nil || len(models) != 0 {
		t.Fatalf("disabled available models=%+v err=%v", models, err)
	}
}

func TestCapabilityReleasesRowsBeforeSnapshotWithSingleConnection(t *testing.T) {
	environment := newRoutingTestEnv(t)
	environment.store.DB().SetMaxOpenConns(1)
	environment.setCapabilityGates(t, "1", "1")
	environment.seedUser(t, true, nil)
	owner := environment.seedUser(t, false, nil)
	model := environment.createModel(t, 'G')
	modelID, _ := parsePositiveID(model.ID)
	_, donationKeyID, _ := environment.seedCandidate(t, owner, 'y', "capability-model")
	batch := BindingBatch{ExpectedBindingRevision: "0", Selections: []BindingSelection{{
		DonationKeyID: fmt.Sprint(donationKeyID), UpstreamModelID: "capability-model",
	}}}
	if _, err := environment.service.AddBindingsAdmin(context.Background(), modelID,
		routingMutation(t, 'H', http.MethodPost, routeAdminBindingBatch, []int64{modelID}, map[string]any{"capability": true}), batch); err != nil {
		t.Fatalf("add capability binding: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	capability, err := environment.service.Capability(ctx, routingTestNow)
	if err != nil {
		t.Fatalf("Capability with one connection: %v", err)
	}
	if capability.State != "available" || capability.DonationIntake != "open" ||
		len(capability.Models) != 1 || capability.Models[0].ID != model.ID {
		t.Fatalf("Capability = %+v, want one available model", capability)
	}
}

func TestCapabilityDonationIntakeCombinations(t *testing.T) {
	tests := []struct {
		name       string
		charity    string
		donation   string
		wantState  string
		wantIntake string
	}{
		{name: "both closed", charity: "0", donation: "0", wantState: "feature_disabled", wantIntake: "closed"},
		{name: "charity only", charity: "1", donation: "0", wantState: "no_models", wantIntake: "closed"},
		{name: "both open", charity: "1", donation: "1", wantState: "no_models", wantIntake: "open"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			environment := newRoutingTestEnv(t)
			environment.setCapabilityGates(t, test.charity, test.donation)
			capability, err := environment.service.Capability(context.Background(), routingTestNow)
			if err != nil {
				t.Fatal(err)
			}
			if capability.State != test.wantState || capability.DonationIntake != test.wantIntake ||
				capability.Models == nil || len(capability.Models) != 0 {
				t.Fatalf("Capability = %+v", capability)
			}
		})
	}
}

func TestCapabilityRejectsMissingInvalidOrContradictoryGates(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*routingTestEnv, *testing.T)
	}{
		{name: "missing charity", mutate: func(environment *routingTestEnv, t *testing.T) {
			_, err := environment.store.DB().Exec(`DELETE FROM site_config WHERE key='charity_enabled'`)
			if err != nil {
				t.Fatal(err)
			}
		}},
		{name: "missing donation", mutate: func(environment *routingTestEnv, t *testing.T) {
			_, err := environment.store.DB().Exec(`DELETE FROM site_config WHERE key='donation_accept_enabled'`)
			if err != nil {
				t.Fatal(err)
			}
		}},
		{name: "invalid charity", mutate: func(environment *routingTestEnv, t *testing.T) {
			_, err := environment.store.DB().Exec(`UPDATE site_config SET value='2' WHERE key='charity_enabled'`)
			if err != nil {
				t.Fatal(err)
			}
		}},
		{name: "invalid donation", mutate: func(environment *routingTestEnv, t *testing.T) {
			_, err := environment.store.DB().Exec(`UPDATE site_config SET value='2' WHERE key='donation_accept_enabled'`)
			if err != nil {
				t.Fatal(err)
			}
		}},
		{name: "donation without charity", mutate: func(environment *routingTestEnv, t *testing.T) {
			environment.setCapabilityGates(t, "0", "1")
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			environment := newRoutingTestEnv(t)
			test.mutate(environment, t)
			capability, err := environment.service.Capability(context.Background(), routingTestNow)
			if !errors.Is(err, ErrInvariant) {
				t.Fatalf("Capability error = %v, want invariant", err)
			}
			if capability.State != "" || capability.Models != nil || capability.DonationIntake != "" {
				t.Fatalf("Capability on invariant failure = %+v", capability)
			}
		})
	}
}

func TestCapabilityDatabaseFailureIsNotProjectedAsClosed(t *testing.T) {
	environment := newRoutingTestEnv(t)
	if _, err := environment.store.DB().Exec(`DROP TABLE site_config`); err != nil {
		t.Fatal(err)
	}
	capability, err := environment.service.Capability(context.Background(), routingTestNow)
	if err == nil || errors.Is(err, ErrInvariant) {
		t.Fatalf("Capability error = %v, want internal database error", err)
	}
	if capability.State != "" || capability.Models != nil || capability.DonationIntake != "" {
		t.Fatalf("Capability on database failure = %+v", capability)
	}
}

func TestCapabilityModelStatesAndCandidatePrivacy(t *testing.T) {
	environment := newRoutingTestEnv(t)
	environment.setCapabilityGates(t, "0", "0")
	capability, err := environment.service.Capability(context.Background(), routingTestNow)
	if err != nil || capability.State != "feature_disabled" || capability.DonationIntake != "closed" {
		t.Fatalf("disabled Capability = %+v, %v", capability, err)
	}

	environment.setCapabilityGates(t, "1", "1")
	capability, err = environment.service.Capability(context.Background(), routingTestNow)
	if err != nil || capability.State != "no_models" || capability.DonationIntake != "open" {
		t.Fatalf("empty Capability = %+v, %v", capability, err)
	}

	environment.seedUser(t, true, nil)
	owner := environment.seedUser(t, false, nil)
	model := environment.createModel(t, 'I')
	modelID, _ := parsePositiveID(model.ID)
	capability, err = environment.service.Capability(context.Background(), routingTestNow)
	if err != nil || capability.State != "no_candidates" || capability.DonationIntake != "open" || len(capability.Models) != 0 {
		t.Fatalf("candidate-free Capability = %+v, %v", capability, err)
	}

	_, donationKeyID, _ := environment.seedCandidate(t, owner, 'z', "private-upstream")
	batch := BindingBatch{ExpectedBindingRevision: "0", Selections: []BindingSelection{{
		DonationKeyID: fmt.Sprint(donationKeyID), UpstreamModelID: "private-upstream",
	}}}
	if _, err := environment.service.AddBindingsAdmin(context.Background(), modelID,
		routingMutation(t, 'J', http.MethodPost, routeAdminBindingBatch, []int64{modelID}, map[string]any{"available": true}), batch); err != nil {
		t.Fatal(err)
	}
	capability, err = environment.service.Capability(context.Background(), routingTestNow)
	if err != nil || capability.State != "available" || capability.DonationIntake != "open" ||
		capability.ServerNow != routingTestNow || len(capability.Models) != 1 || capability.Models[0].ID != model.ID {
		t.Fatalf("available Capability = %+v, %v", capability, err)
	}
	publicModel := capability.Models[0]
	if publicModel.Pricing.Mode != "per_request" || publicModel.Pricing.UserPriceMilli == nil ||
		*publicModel.Pricing.UserPriceMilli != "3000" || publicModel.Pricing.DiscountedUserPriceMilli == nil ||
		*publicModel.Pricing.DiscountedUserPriceMilli != "2400" || publicModel.Pricing.UserPricesMilli != nil ||
		publicModel.Pricing.DiscountedUserPricesMilli != nil || !publicModel.Discount.Enabled ||
		publicModel.Discount.Percent != 80 || publicModel.Discount.StartAt == nil ||
		*publicModel.Discount.StartAt != routingTestNow-1 || publicModel.Discount.EndAt == nil ||
		*publicModel.Discount.EndAt != routingTestNow+100 {
		t.Fatalf("public request pricing = %+v, discount = %+v", publicModel.Pricing, publicModel.Discount)
	}
	bodyBytes, err := json.Marshal(capability)
	if err != nil {
		t.Fatal(err)
	}
	body := string(bodyBytes)
	for _, private := range []string{"private-upstream", "private key note", "https://z.routing.test/v1", "donor", "rolling_success", "binding"} {
		if strings.Contains(body, private) {
			t.Fatalf("Capability leaks private candidate value %q: %s", private, body)
		}
	}

	if _, err := environment.store.DB().Exec(`INSERT INTO site_config(key,value,updated_at)
VALUES('charity_token_reserve_milli','5',?)`, routingTestNow); err != nil {
		t.Fatal(err)
	}
	if _, err := environment.store.DB().Exec(`UPDATE charity_models SET pricing_mode='per_token',
uncached_user_price=1,cache_write_user_price=2000,cache_read_user_price=3001,output_user_price=0,
discount_percent=33 WHERE id=?`, modelID); err != nil {
		t.Fatal(err)
	}
	capability, err = environment.service.Capability(context.Background(), routingTestNow)
	if err != nil || len(capability.Models) != 1 {
		t.Fatalf("token Capability = %+v, %v", capability, err)
	}
	pricing := capability.Models[0].Pricing
	if pricing.Mode != "per_token" || pricing.UserPriceMilli != nil || pricing.DiscountedUserPriceMilli != nil ||
		pricing.UserPricesMilli == nil || pricing.DiscountedUserPricesMilli == nil ||
		*pricing.UserPricesMilli != (CapabilityTokenPrices{UncachedInput: "1", CacheWriteInput: "2000", CacheReadInput: "3001", Output: "0"}) ||
		*pricing.DiscountedUserPricesMilli != (CapabilityTokenPrices{UncachedInput: "1", CacheWriteInput: "660", CacheReadInput: "991", Output: "0"}) {
		t.Fatalf("public token pricing = %+v", pricing)
	}
}

func TestCapabilityFreezesIntakeBeforeCandidateSnapshots(t *testing.T) {
	environment := newRoutingTestEnv(t)
	environment.setCapabilityGates(t, "1", "1")
	environment.seedUser(t, true, nil)
	owner := environment.seedUser(t, false, nil)
	model := environment.createModel(t, 'K')
	modelID, _ := parsePositiveID(model.ID)
	_, donationKeyID, _ := environment.seedCandidate(t, owner, 'w', "freeze-model")
	batch := BindingBatch{ExpectedBindingRevision: "0", Selections: []BindingSelection{{
		DonationKeyID: fmt.Sprint(donationKeyID), UpstreamModelID: "freeze-model",
	}}}
	if _, err := environment.service.AddBindingsAdmin(context.Background(), modelID,
		routingMutation(t, 'L', http.MethodPost, routeAdminBindingBatch, []int64{modelID}, map[string]any{"freeze": true}), batch); err != nil {
		t.Fatal(err)
	}
	environment.state.dueHook = func(ctx context.Context, tx *sql.Tx, _ int64, _ int) error {
		_, err := tx.ExecContext(ctx, `UPDATE site_config SET value='0' WHERE key='donation_accept_enabled'`)
		return err
	}
	capability, err := environment.service.Capability(context.Background(), routingTestNow)
	if err != nil {
		t.Fatal(err)
	}
	if capability.State != "available" || capability.DonationIntake != "open" {
		t.Fatalf("Capability = %+v, want frozen open intake", capability)
	}
	var stored string
	if err := environment.store.DB().QueryRow(`SELECT value FROM site_config WHERE key='donation_accept_enabled'`).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != "0" {
		t.Fatalf("donation gate = %q, want candidate snapshot mutation", stored)
	}
}

func TestCapabilityHTTPJSONGolden(t *testing.T) {
	tests := []struct {
		name     string
		charity  string
		donation string
		want     string
	}{
		{name: "both closed", charity: "0", donation: "0", want: `{"state":"feature_disabled","models":[],"donation_intake":"closed","server_now":1700000000}` + "\n"},
		{name: "charity only", charity: "1", donation: "0", want: `{"state":"no_models","models":[],"donation_intake":"closed","server_now":1700000000}` + "\n"},
		{name: "both open", charity: "1", donation: "1", want: `{"state":"no_models","models":[],"donation_intake":"open","server_now":1700000000}` + "\n"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			environment := newRoutingTestEnv(t)
			environment.setCapabilityGates(t, test.charity, test.donation)
			request := httptest.NewRequest(http.MethodGet, routeCapability, nil)
			response := httptest.NewRecorder()
			(&httpAPI{service: environment.service}).capability(response, request, UserPrincipal{UserID: 1})
			if response.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
			}
			if response.Header().Get("Content-Type") != "application/json; charset=utf-8" ||
				response.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("headers = %v", response.Header())
			}
			if response.Body.String() != test.want {
				t.Fatalf("body = %q, want %q", response.Body.String(), test.want)
			}
		})
	}
}

func pointerInt64(value int64) *int64 { return &value }
func pointerBool(value bool) *bool    { return &value }
