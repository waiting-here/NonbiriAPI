package routing

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/dbfixture"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

const snapshotTestNow int64 = 1_700_000_000

type snapshotTestEnvironment struct {
	store    *db.Store
	routing  *Store
	sequence uint64
}

func newSnapshotTestEnvironment(t *testing.T) *snapshotTestEnvironment {
	t.Helper()
	master := bytes.Repeat([]byte{0x71}, secret.MasterKeyBytes)
	vault, err := secret.New(master)
	clear(master)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = vault.Close() })
	path := filepath.Join(t.TempDir(), "routing.sqlite")
	dbfixture.Materialize(t, path)
	store, err := db.Open(path, vault)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	routingStore, err := New(store)
	if err != nil {
		t.Fatal(err)
	}
	return &snapshotTestEnvironment{store: store, routing: routingStore}
}

func (environment *snapshotTestEnvironment) next() uint64 {
	environment.sequence++
	return environment.sequence
}

func (environment *snapshotTestEnvironment) seedUser(t *testing.T, suffix string) int64 {
	t.Helper()
	zero := make([]byte, 16)
	result, err := environment.store.DB().Exec(`INSERT INTO users(
discord_id,username,is_admin,donation_credit_mag,total_requests,total_uncached_input_tokens,
total_cache_write_input_tokens,total_cache_read_input_tokens,total_output_tokens,total_unknown_usage_requests,
revision,lang,created_at,updated_at) VALUES(?,?,0,?,?,?,?,?,?,?,?,?,?,?)`,
		"routing-"+suffix, "routing", zero, zero, zero, zero, zero, zero, zero, zero,
		"en", snapshotTestNow, snapshotTestNow)
	if err != nil {
		t.Fatal(err)
	}
	userID, _ := result.LastInsertId()
	return userID
}

func (environment *snapshotTestEnvironment) seedModel(t *testing.T, userID int64, provider, model string) int64 {
	t.Helper()
	result, err := environment.store.DB().Exec(`INSERT INTO models(
user_id,provider,model,full_name,route_strategy,silent_retry,flatten_tool_calls,
revision,binding_revision,created_at,updated_at)
VALUES(?,?,?,?, 'ordered',0,0,1,0,?,?)`, userID, provider, model, provider+"/"+model,
		snapshotTestNow, snapshotTestNow)
	if err != nil {
		t.Fatal(err)
	}
	modelID, _ := result.LastInsertId()
	return modelID
}

func (environment *snapshotTestEnvironment) seedAvailableBinding(t *testing.T, userID, modelID int64, upstream string) int64 {
	t.Helper()
	sequence := environment.next()
	baseURL := fmt.Sprintf("https://routing-%d.example/v1", sequence)
	endpointResult, err := environment.store.DB().Exec(`INSERT INTO endpoints(
user_id,connector_type,base_url,note,enabled,revision,created_at,updated_at)
VALUES(?,'openai-compatible',?,'',1,1,?,?)`, userID, baseURL, snapshotTestNow, snapshotTestNow)
	if err != nil {
		t.Fatal(err)
	}
	endpointID, _ := endpointResult.LastInsertId()
	contextID := make([]byte, 16)
	binary.BigEndian.PutUint64(contextID[8:], sequence)
	secretResult, err := environment.store.DB().Exec(`INSERT INTO endpoint_key_secrets(
context_id,canonical_base_url,connector_type,encrypted_secret,created_at)
VALUES(?,?,'openai-compatible','test-envelope',?)`, contextID, baseURL, snapshotTestNow)
	if err != nil {
		t.Fatal(err)
	}
	secretID, _ := secretResult.LastInsertId()
	fingerprint := make([]byte, 32)
	binary.BigEndian.PutUint64(fingerprint[24:], sequence)
	keyResult, err := environment.store.DB().Exec(`INSERT INTO endpoint_keys(
endpoint_id,secret_ref_id,secret_fingerprint,display_head,display_tail,note,enabled,
force_store_false,revision,created_at,updated_at)
VALUES(?,?,?,'head','tail','',1,0,1,?,?)`, endpointID, secretID, fingerprint,
		snapshotTestNow, snapshotTestNow)
	if err != nil {
		t.Fatal(err)
	}
	keyID, _ := keyResult.LastInsertId()
	if _, err := environment.store.DB().Exec(`INSERT INTO model_discovery_evidence(
endpoint_key_id,state,revision,safe_class,safe_diag,fetched_count)
VALUES(?,'unknown',1,'none','',0)`, keyID); err != nil {
		t.Fatal(err)
	}
	if _, err := environment.store.DB().Exec(`INSERT INTO model_pair_catalog(
endpoint_key_id,normalized_model_id,automatic_supports,manual_supports,automatic_revision,pair_revision,updated_at)
VALUES(?,?,0,1,0,1,?)`, keyID, upstream, snapshotTestNow); err != nil {
		t.Fatal(err)
	}
	if _, err := environment.store.DB().Exec(`INSERT INTO model_bindings(
model_id,endpoint_key_id,upstream_model_id,ord,created_at,updated_at)
VALUES(?,?,?,?,?,?)`, modelID, keyID, upstream, int(sequence-1), snapshotTestNow, snapshotTestNow); err != nil {
		t.Fatal(err)
	}
	return keyID
}

func TestLogicalPreflightIsCandidateFreeAndOwnerScoped(t *testing.T) {
	environment := newSnapshotTestEnvironment(t)
	owner := environment.seedUser(t, "owner")
	other := environment.seedUser(t, "other")
	modelID := environment.seedModel(t, owner, "provider", "model")

	byName, err := environment.routing.Preflight(context.Background(), owner, "provider/model")
	if err != nil {
		t.Fatal(err)
	}
	if byName.ModelID() != modelID || byName.OwnerUserID() != owner || byName.FullName() != "provider/model" ||
		byName.RouteStrategy() != "ordered" || byName.Revision() != 1 || byName.BindingRevision() != 0 {
		t.Fatalf("preflight=%+v", byName)
	}
	byID, err := environment.routing.Preflight(context.Background(), owner, strconv.FormatInt(modelID, 10))
	if err != nil || byID.ModelID() != modelID {
		t.Fatalf("id preflight=%+v err=%v", byID, err)
	}
	if _, err := environment.routing.Preflight(context.Background(), other, "provider/model"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-owner preflight error=%v", err)
	}
	if _, err := environment.routing.Snapshot(context.Background(), owner, Identity{FullName: "provider/model"}); !errors.Is(err, ErrUnbound) {
		t.Fatalf("candidate-free model snapshot error=%v, want unbound", err)
	}
}

func TestRoutableListIsStableBoundedAndTracksCurrentBindings(t *testing.T) {
	environment := newSnapshotTestEnvironment(t)
	owner := environment.seedUser(t, "list-owner")
	other := environment.seedUser(t, "list-other")
	zetaID := environment.seedModel(t, owner, "zeta", "model")
	alphaID := environment.seedModel(t, owner, "alpha", "model")
	environment.seedModel(t, owner, "draft", "model")
	otherID := environment.seedModel(t, other, "other", "model")
	zetaKey := environment.seedAvailableBinding(t, owner, zetaID, "upstream-zeta")
	environment.seedAvailableBinding(t, owner, alphaID, "upstream-alpha")
	environment.seedAvailableBinding(t, other, otherID, "upstream-other")

	models, err := environment.routing.ListRoutableModels(context.Background(), owner, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 2 || models[0].FullName != "alpha/model" || models[1].FullName != "zeta/model" {
		t.Fatalf("routable models=%+v", models)
	}
	if _, err := environment.routing.ListRoutableModels(context.Background(), owner, 1); !errors.Is(err, ErrResourceLimit) {
		t.Fatalf("bounded list error=%v", err)
	}
	if _, err := environment.store.DB().Exec(`UPDATE endpoint_keys SET enabled=0,revision=revision+1 WHERE id=?`, zetaKey); err != nil {
		t.Fatal(err)
	}
	models, err = environment.routing.ListRoutableModels(context.Background(), owner, 10)
	if err != nil || len(models) != 1 || models[0].FullName != "alpha/model" {
		t.Fatalf("post-revocation models=%+v err=%v", models, err)
	}
}

func TestSnapshotRejectsMoreThanOneHundredFrozenCandidates(t *testing.T) {
	environment := newSnapshotTestEnvironment(t)
	owner := environment.seedUser(t, "candidate-limit")
	modelID := environment.seedModel(t, owner, "provider", "many")
	for index := 0; index <= MaxSnapshotCandidates; index++ {
		environment.seedAvailableBinding(t, owner, modelID, fmt.Sprintf("upstream-%03d", index))
	}
	_, err := environment.routing.Snapshot(context.Background(), owner, Identity{FullName: "provider/many"})
	if !errors.Is(err, ErrResourceLimit) {
		t.Fatalf("candidate limit error=%v", err)
	}
}
