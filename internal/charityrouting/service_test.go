package charityrouting

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

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

type routingDonationState struct{ dueCalls atomic.Int64 }

func (*routingDonationState) MaterializeExpiryTx(context.Context, *sql.Tx, int64, int64) (bool, error) {
	return false, nil
}
func (state *routingDonationState) MaterializeDueExpiriesTx(context.Context, *sql.Tx, int64, int) error {
	state.dueCalls.Add(1)
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
	t.Helper()
	now := environment.clock.Load()
	baseURL := fmt.Sprintf("https://%c.routing.test/v1", suffix)
	result, err := environment.store.DB().Exec(`INSERT INTO endpoints(user_id,connector_type,base_url,note,enabled,revision,created_at,updated_at)
VALUES(?,'openai-compatible',?,'private',1,1,?,?)`, ownerID, baseURL, now, now)
	if err != nil {
		t.Fatal(err)
	}
	endpointID, _ := result.LastInsertId()
	contextID, fingerprint := make([]byte, 16), make([]byte, 32)
	contextID[15], fingerprint[31] = suffix, suffix
	result, err = environment.store.DB().Exec(`INSERT INTO endpoint_key_secrets(
context_id,canonical_base_url,connector_type,encrypted_secret,created_at) VALUES(?,?,'openai-compatible','envelope',?)`, contextID, baseURL, now)
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
failure_streak,streak_generation,next_claim_seq,next_fold_seq,enabled,token_reserve,safe_note,created_at,updated_at)
VALUES(?,?,?,?,?,'openai-compatible',?,?,?,?,?,?,?,?,?,?,1,10,'safe label',?,?)`, donationID, endpointKeyID,
		"head", "tail", baseURL, zero, zero, zero, zero, zero, zero, zero, one, one, one, now, now)
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
	snapshot, err := environment.service.Snapshot(context.Background(), modelID, routingTestNow)
	if err != nil || len(snapshot.Candidates()) != 1 || snapshot.Candidates()[0].DonationKeyID != donationKeyID || snapshot.ReservedMilli != 2400 {
		t.Fatalf("snapshot = %+v candidates=%+v err=%v", snapshot, snapshot.Candidates(), err)
	}
	zero := db.EncodeU128(db.U128{})
	if _, err := environment.store.DB().Exec(`UPDATE donation_keys SET call_limit_mag=? WHERE id=?`, zero, donationKeyID); err != nil {
		t.Fatal(err)
	}
	if _, err := environment.service.Snapshot(context.Background(), modelID, routingTestNow); !errors.Is(err, ErrUnavailable) {
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

func TestCapabilityReleasesRowsBeforeSnapshotWithSingleConnection(t *testing.T) {
	environment := newRoutingTestEnv(t)
	environment.store.DB().SetMaxOpenConns(1)
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
	if capability.State != "available" || len(capability.Models) != 1 || capability.Models[0].ID != model.ID {
		t.Fatalf("Capability = %+v, want one available model", capability)
	}
}

func pointerInt64(value int64) *int64 { return &value }
func pointerBool(value bool) *bool    { return &value }
