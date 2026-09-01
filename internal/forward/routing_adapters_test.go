package forward

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/dbtest"
	"github.com/waiting-here/NonbiriAPI/internal/routing"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

const routingAdapterTestNow int64 = 1_700_000_000

type routingAdapterTestEnvironment struct {
	store   *db.Store
	adapter *PersonalRoutingAdapter
}

func newRoutingAdapterTestEnvironment(t *testing.T) *routingAdapterTestEnvironment {
	t.Helper()
	master := bytes.Repeat([]byte{0x6d}, secret.MasterKeyBytes)
	vault, err := secret.New(master)
	clear(master)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = vault.Close() })
	path := filepath.Join(t.TempDir(), "forward-routing.sqlite")
	dbtest.EnsureOwnerOnlyParent(t, path)
	store, err := db.Open(path, vault)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	routingStore, err := routing.New(store)
	if err != nil {
		t.Fatal(err)
	}
	adapter, err := NewPersonalRoutingAdapter(routingStore)
	if err != nil {
		t.Fatal(err)
	}
	return &routingAdapterTestEnvironment{store: store, adapter: adapter}
}

func (environment *routingAdapterTestEnvironment) seedBoundModel(t *testing.T) (int64, int64, string) {
	t.Helper()
	zero := make([]byte, 16)
	userResult, err := environment.store.DB().Exec(`INSERT INTO users(
discord_id,username,is_admin,donation_credit_mag,total_requests,total_uncached_input_tokens,
total_cache_write_input_tokens,total_cache_read_input_tokens,total_output_tokens,total_unknown_usage_requests,
revision,lang,created_at,updated_at) VALUES(?,?,0,?,?,?,?,?,?,?,?,?,?,?)`,
		"forward-routing-owner", "routing-owner", zero, zero, zero, zero, zero, zero, zero, zero,
		"en", routingAdapterTestNow, routingAdapterTestNow)
	if err != nil {
		t.Fatal(err)
	}
	userID, err := userResult.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	modelResult, err := environment.store.DB().Exec(`INSERT INTO models(
user_id,provider,model,full_name,route_strategy,silent_retry,flatten_tool_calls,
revision,binding_revision,created_at,updated_at)
VALUES(?,?,?,?, 'ordered',0,0,1,0,?,?)`, userID, "provider", "model", "provider/model",
		routingAdapterTestNow, routingAdapterTestNow)
	if err != nil {
		t.Fatal(err)
	}
	modelID, err := modelResult.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	baseURL := "https://decimal-id.example/v1"
	endpointResult, err := environment.store.DB().Exec(`INSERT INTO endpoints(
user_id,connector_type,base_url,note,enabled,revision,created_at,updated_at)
VALUES(?,'openai-compatible',?,'',1,1,?,?)`, userID, baseURL, routingAdapterTestNow, routingAdapterTestNow)
	if err != nil {
		t.Fatal(err)
	}
	endpointID, err := endpointResult.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	contextID := make([]byte, 16)
	binary.BigEndian.PutUint64(contextID[8:], uint64(endpointID))
	secretResult, err := environment.store.DB().Exec(`INSERT INTO endpoint_key_secrets(
context_id,canonical_base_url,connector_type,encrypted_secret,created_at)
VALUES(?,?,'openai-compatible','test-envelope',?)`, contextID, baseURL, routingAdapterTestNow)
	if err != nil {
		t.Fatal(err)
	}
	secretID, err := secretResult.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	fingerprint := make([]byte, 32)
	binary.BigEndian.PutUint64(fingerprint[24:], uint64(endpointID))
	keyResult, err := environment.store.DB().Exec(`INSERT INTO endpoint_keys(
endpoint_id,secret_ref_id,secret_fingerprint,display_head,display_tail,note,enabled,
force_store_false,revision,created_at,updated_at)
VALUES(?,?,?,'head','tail','',1,0,1,?,?)`, endpointID, secretID, fingerprint,
		routingAdapterTestNow, routingAdapterTestNow)
	if err != nil {
		t.Fatal(err)
	}
	keyID, err := keyResult.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := environment.store.DB().Exec(`INSERT INTO model_discovery_evidence(
endpoint_key_id,state,revision,safe_class,safe_diag,fetched_count)
VALUES(?,'unknown',1,'none','',0)`, keyID); err != nil {
		t.Fatal(err)
	}
	upstreamModelID := "upstream-decimal-model"
	if _, err := environment.store.DB().Exec(`INSERT INTO model_pair_catalog(
endpoint_key_id,normalized_model_id,automatic_supports,manual_supports,automatic_revision,pair_revision,updated_at)
VALUES(?,?,0,1,0,1,?)`, keyID, upstreamModelID, routingAdapterTestNow); err != nil {
		t.Fatal(err)
	}
	if _, err := environment.store.DB().Exec(`INSERT INTO model_bindings(
model_id,endpoint_key_id,upstream_model_id,ord,created_at,updated_at)
VALUES(?,?,?,?,?,?)`, modelID, keyID, upstreamModelID, 0, routingAdapterTestNow, routingAdapterTestNow); err != nil {
		t.Fatal(err)
	}
	return userID, modelID, upstreamModelID
}

func TestPersonalRoutingAdapterDecimalModelIDCompletesChat(t *testing.T) {
	environment := newRoutingAdapterTestEnvironment(t)
	userID, modelID, upstreamModelID := environment.seedBoundModel(t)
	identifier := strconv.FormatInt(modelID, 10)

	preflight, err := environment.adapter.Preflight(context.Background(), userID, identifier)
	if err != nil {
		t.Fatalf("decimal preflight: %v", err)
	}
	if preflight.ModelID != modelID || preflight.FullName != "provider/model" || preflight.OwnerUserID != userID {
		t.Fatalf("decimal preflight=%+v", preflight)
	}
	snapshot, err := environment.adapter.Snapshot(context.Background(), userID, identifier)
	if err != nil {
		t.Fatalf("decimal snapshot: %v", err)
	}
	if snapshot.ModelID != modelID || snapshot.FullName != "provider/model" || len(snapshot.Candidates) != 1 {
		t.Fatalf("decimal snapshot=%+v", snapshot)
	}
	candidate := snapshot.Candidates[0]
	if candidate.UpstreamModelID != upstreamModelID || candidate.ConnectorType != connectorcontract.TypeOpenAICompatible {
		t.Fatalf("decimal candidate=%+v", candidate)
	}

	fixture := newServiceFixture(t, nil)
	fixture.service.personal = environment.adapter
	fixture.addDispatch(candidate)
	fixture.openAI.results = []connectorcontract.AttemptResult{{
		Success: true, Committed: true, Failure: connectorcontract.FailureNone,
		UpstreamStatus: http.StatusOK, ClientStatus: http.StatusOK,
	}}
	fixture.openAI.bodies = [][]byte{[]byte(`{"id":"decimal-model-success"}`)}
	request := decodeChatForTest(t, fmt.Sprintf(`{"model":%q,"messages":[]}`, identifier))
	recorder := httptest.NewRecorder()

	fixture.service.Chat(context.Background(), recorder, userID, request, []byte(`{}`), "application/json", "en")

	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "decimal-model-success") {
		t.Fatalf("decimal Chat response=%d %q", recorder.Code, recorder.Body.String())
	}
	if len(fixture.claims.accepts) != 1 || fixture.claims.accepts[0].ModelSnapshot != "provider/model" {
		t.Fatalf("decimal Chat accept=%+v", fixture.claims.accepts)
	}
	if len(fixture.claims.claims) != 1 || fixture.claims.claims[0].Candidate.EndpointKeyID != candidate.EndpointKeyID ||
		fixture.claims.claims[0].Candidate.UpstreamModelID != upstreamModelID {
		t.Fatalf("decimal Chat claim=%+v", fixture.claims.claims)
	}
	if fixture.openAI.calls != 1 || len(fixture.openAI.targets) != 1 || fixture.openAI.targets[0].UpstreamModel() != upstreamModelID {
		t.Fatalf("decimal Chat targets=%+v calls=%d", fixture.openAI.targets, fixture.openAI.calls)
	}
}
