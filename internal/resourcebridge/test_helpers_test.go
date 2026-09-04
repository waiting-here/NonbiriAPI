package resourcebridge

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/backend"
	"github.com/waiting-here/NonbiriAPI/internal/claim"
	"github.com/waiting-here/NonbiriAPI/internal/connector"
	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/dbfixture"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

const bridgeTestNow = int64(1_700_000_000)

type bridgeFixture struct {
	store   *db.Store
	vault   *trackingVault
	claims  *claim.Service
	backend *testBackend
	runtime *Runtime
	clock   atomic.Int64
}

type seededKey struct {
	ownerID    int64
	endpointID int64
	keyID      int64
	secretID   int64
	connector  connectorcontract.Type
	baseURL    string
}

func newBridgeFixture(t testing.TB) *bridgeFixture {
	t.Helper()
	return newBridgeFixtureWithRandom(t, nil)
}

func newBridgeFixtureWithRandom(t testing.TB, random io.Reader) *bridgeFixture {
	t.Helper()
	master := bytes.Repeat([]byte{0x51}, secret.MasterKeyBytes)
	inner, err := secret.New(master)
	clear(master)
	if err != nil {
		t.Fatalf("secret.New: %v", err)
	}
	t.Cleanup(func() { _ = inner.Close() })
	vault := &trackingVault{inner: inner}
	path := filepath.Join(t.TempDir(), "resourcebridge.sqlite")
	dbfixture.Materialize(t, path)
	store, err := db.Open(path, vault)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	fixture := &bridgeFixture{store: store, vault: vault, backend: &testBackend{}}
	fixture.clock.Store(bridgeTestNow)
	claims, err := claim.New(claim.Dependencies{
		DB:      store.DB(),
		Secrets: vault,
		Now:     func() time.Time { return time.Unix(fixture.clock.Load(), 0) },
	})
	if err != nil {
		t.Fatalf("claim.New: %v", err)
	}
	fixture.claims = claims
	runtime, err := New(Config{
		Store: store, Vault: vault, Claims: claims, Backend: fixture.backend,
		Now: func() time.Time { return time.Unix(fixture.clock.Load(), 0) }, Random: random,
	})
	if err != nil {
		t.Fatalf("resourcebridge.New: %v", err)
	}
	fixture.runtime = runtime
	t.Cleanup(func() { _ = runtime.Close() })
	return fixture
}

func (f *bridgeFixture) seedUser(t testing.TB, label string) int64 {
	t.Helper()
	zero := make([]byte, 16)
	result, err := f.store.DB().Exec(`INSERT INTO users(
discord_id,username,donation_credit_mag,total_requests,total_uncached_input_tokens,
total_cache_write_input_tokens,total_cache_read_input_tokens,total_output_tokens,
total_unknown_usage_requests,revision,created_at,updated_at)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, "bridge-"+label, label, zero, zero, zero, zero, zero, zero, zero, zero,
		f.clock.Load(), f.clock.Load())
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}
	userID, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("seed user id: %v", err)
	}
	return userID
}

func (f *bridgeFixture) seedEndpointKey(t testing.TB, ownerID int64, label string, connectorType connectorcontract.Type, plaintext string) seededKey {
	t.Helper()
	baseURL := "https://" + label + ".example.test/v1"
	result, err := f.store.DB().Exec(`INSERT INTO endpoints(
user_id,connector_type,base_url,note,enabled,revision,created_at,updated_at)
VALUES(?,?,?,'',1,1,?,?)`, ownerID, connectorType, baseURL, f.clock.Load(), f.clock.Load())
	if err != nil {
		t.Fatalf("seed endpoint: %v", err)
	}
	endpointID, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("seed endpoint id: %v", err)
	}
	tx, err := f.store.DB().BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin key seed: %v", err)
	}
	defer tx.Rollback()
	credential := []byte(plaintext)
	stored, err := f.runtime.WriteEndpointSecret(context.Background(), tx, resources.SecretWriteInput{
		CanonicalBaseURL: baseURL,
		ConnectorType:    string(connectorType),
		Plaintext:        credential,
		CreatedAt:        f.clock.Load(),
	})
	if err != nil {
		t.Fatalf("WriteEndpointSecret: %v", err)
	}
	if !allZero(credential) {
		t.Fatal("WriteEndpointSecret retained caller plaintext")
	}
	result, err = tx.ExecContext(context.Background(), `INSERT INTO endpoint_keys(
endpoint_id,secret_ref_id,secret_fingerprint,display_head,display_tail,note,
enabled,force_store_false,revision,created_at,updated_at)
VALUES(?,?,?,?,?,'',1,0,1,?,?)`, endpointID, stored.RefID, stored.Fingerprint[:], stored.DisplayHead, stored.DisplayTail,
		f.clock.Load(), f.clock.Load())
	if err != nil {
		t.Fatalf("seed endpoint key: %v", err)
	}
	keyID, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("seed endpoint key id: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit key seed: %v", err)
	}
	return seededKey{
		ownerID: ownerID, endpointID: endpointID, keyID: keyID, secretID: stored.RefID,
		connector: connectorType, baseURL: baseURL,
	}
}

func (f *bridgeFixture) discoveryInput(t testing.TB, key seededKey, discoverer connector.ModelDiscoverer) resources.DiscoveryClaimInput {
	t.Helper()
	operationID, err := db.GenerateOpaqueID("op_")
	if err != nil {
		t.Fatalf("GenerateOpaqueID: %v", err)
	}
	return resources.DiscoveryClaimInput{
		OperationID: operationID, OwnerUserID: key.ownerID, EndpointID: key.endpointID,
		EndpointKeyID: key.keyID, ConnectorType: key.connector,
		CanonicalBaseURL: key.baseURL, Discoverer: discoverer,
	}
}

func (f *bridgeFixture) writeUnreferencedSecret(t testing.TB, plaintext string, commit bool) resources.StoredSecret {
	t.Helper()
	tx, err := f.store.DB().BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin secret write: %v", err)
	}
	defer tx.Rollback()
	buffer := []byte(plaintext)
	stored, err := f.runtime.WriteEndpointSecret(context.Background(), tx, resources.SecretWriteInput{
		CanonicalBaseURL: "https://secret.example.test/v1",
		ConnectorType:    string(connectorcontract.TypeOpenAICompatible),
		Plaintext:        buffer,
		CreatedAt:        f.clock.Load(),
	})
	if err != nil {
		t.Fatalf("WriteEndpointSecret: %v", err)
	}
	if !allZero(buffer) {
		t.Fatal("plaintext buffer was not cleared")
	}
	if commit {
		if err := tx.Commit(); err != nil {
			t.Fatalf("commit secret: %v", err)
		}
	}
	return stored
}

type discoveryFacts struct {
	requestID       string
	requestState    string
	callerClass     string
	callerStatus    int
	accountingState string
	reservedMilli   int64
	claimState      string
	purpose         string
	resultKind      sql.NullString
	upstreamStatus  sql.NullInt64
	diagnostic      sql.NullString
	usageUnknown    sql.NullInt64
}

func (f *bridgeFixture) latestDiscoveryFacts(t testing.TB) discoveryFacts {
	t.Helper()
	var facts discoveryFacts
	err := f.store.DB().QueryRow(`SELECT
r.id,r.state,COALESCE(r.caller_result_class,''),COALESCE(r.caller_status,0),
r.accounting_state,r.account_reserved_milli,c.state,c.purpose,
a.result_kind,a.upstream_status,a.diag,a.usage_unknown
FROM logical_requests r
JOIN dispatch_claims c ON c.logical_request_id=r.id
JOIN request_logs l ON l.logical_request_id=r.id
LEFT JOIN request_attempts a ON a.request_log_id=l.id
WHERE r.route_kind='model_discovery'
ORDER BY r.rowid DESC LIMIT 1`).Scan(
		&facts.requestID, &facts.requestState, &facts.callerClass, &facts.callerStatus,
		&facts.accountingState, &facts.reservedMilli, &facts.claimState, &facts.purpose,
		&facts.resultKind, &facts.upstreamStatus, &facts.diagnostic, &facts.usageUnknown,
	)
	if err != nil {
		t.Fatalf("read discovery facts: %v", err)
	}
	return facts
}

func assertFixedDiscoveryRequest(t testing.TB, facts discoveryFacts) {
	t.Helper()
	if facts.requestState != "terminal" || facts.callerClass != "success" || facts.callerStatus != 202 ||
		facts.accountingState != "none" || facts.reservedMilli != 0 || facts.purpose != "discovery" {
		t.Fatalf("unexpected request projection: %+v", facts)
	}
}

type testBackend struct {
	mu        sync.Mutex
	opens     int
	openErr   error
	rewriteTo string
}

func (b *testBackend) Open(baseURL string) (backend.EndpointClient, error) {
	b.mu.Lock()
	b.opens++
	err := b.openErr
	rewrite := b.rewriteTo
	b.mu.Unlock()
	if err != nil {
		return nil, err
	}
	if rewrite != "" {
		baseURL = rewrite
	}
	return testEndpointClient{baseURL: baseURL}, nil
}

func (*testBackend) MaxResponseBytes() int64 { return 1 << 20 }

func (b *testBackend) setOpenError(err error) {
	b.mu.Lock()
	b.openErr = err
	b.mu.Unlock()
}

func (b *testBackend) openCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.opens
}

type testEndpointClient struct{ baseURL string }

func (c testEndpointClient) BaseURL() string { return c.baseURL }
func (testEndpointClient) Do(*http.Request) (*http.Response, error) {
	panic("resource bridge must not perform HTTP directly")
}

type trackingVault struct {
	inner *secret.Vault

	mu           sync.Mutex
	deriveCalls  int
	deriveInfo   []byte
	derivedAlias []byte
	failDerive   bool
	deriveLength int
	failSeal     bool
	failOpen     bool
	errorText    string
	openStarted  chan<- struct{}
	openRelease  <-chan struct{}
}

func (v *trackingVault) DeriveGenerationTwoSubkey(info []byte) ([]byte, error) {
	v.mu.Lock()
	v.deriveCalls++
	clear(v.deriveInfo)
	v.deriveInfo = append(v.deriveInfo[:0], info...)
	fail := v.failDerive
	length := v.deriveLength
	errorText := v.errorText
	v.mu.Unlock()
	if fail {
		return nil, errors.New(errorText)
	}
	derived, err := v.inner.DeriveGenerationTwoSubkey(info)
	if err != nil {
		return nil, err
	}
	if length > 0 {
		resized := make([]byte, length)
		copy(resized, derived)
		clear(derived)
		derived = resized
	}
	v.mu.Lock()
	v.derivedAlias = derived
	v.mu.Unlock()
	return derived, nil
}

func (v *trackingVault) SealForGenerationTwoContext(plaintext []byte, credentialContext secret.GenerationTwoEndpointKeyContext) (string, error) {
	v.mu.Lock()
	fail := v.failSeal
	errorText := v.errorText
	v.mu.Unlock()
	if fail {
		return "", errors.New(errorText)
	}
	return v.inner.SealForGenerationTwoContext(plaintext, credentialContext)
}

func (v *trackingVault) OpenForGenerationTwoContext(ciphertext string, credentialContext secret.GenerationTwoEndpointKeyContext) ([]byte, error) {
	v.mu.Lock()
	fail := v.failOpen
	errorText := v.errorText
	started := v.openStarted
	release := v.openRelease
	v.mu.Unlock()
	if started != nil {
		select {
		case started <- struct{}{}:
		default:
		}
	}
	if release != nil {
		<-release
	}
	if fail {
		return nil, errors.New(errorText)
	}
	return v.inner.OpenForGenerationTwoContext(ciphertext, credentialContext)
}

func (v *trackingVault) setOpenFailure(fail bool, errorText string) {
	v.mu.Lock()
	v.failOpen = fail
	v.errorText = errorText
	v.mu.Unlock()
}

func (v *trackingVault) derivationSnapshot() (int, []byte, []byte) {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.deriveCalls, append([]byte(nil), v.derivedAlias...), append([]byte(nil), v.deriveInfo...)
}

type discovererFunc func(context.Context, connectorcontract.DiscoveryInput) connectorcontract.DiscoveryResult

func (function discovererFunc) Discover(ctx context.Context, input connectorcontract.DiscoveryInput) connectorcontract.DiscoveryResult {
	return function(ctx, input)
}

func allZero(value []byte) bool {
	for _, item := range value {
		if item != 0 {
			return false
		}
	}
	return true
}
