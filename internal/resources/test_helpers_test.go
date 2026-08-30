package resources

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/connector"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/dbtest"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

const resourceTestNow int64 = 1_700_000_000

type resourceTestEnvironment struct {
	store      *db.Store
	vault      *secret.Vault
	repository *Repository
	authorizer *resourceTestAuthorizer
	secrets    *resourceTestSecretWriter
	deletions  *resourceTestEndpointKeyDeletionHook
	discovery  *resourceTestDiscoveryRail
	worker     *DiscoveryWorkerPool
	clock      *atomic.Int64
}

func newResourceTestEnvironment(t *testing.T) *resourceTestEnvironment {
	return newResourceTestEnvironmentWithDiscoveryPool(t, 4, 32, 30*time.Second)
}

func newResourceTestEnvironmentWithDiscoveryPool(
	t *testing.T,
	maxConcurrent, maxAdmitted int,
	timeout time.Duration,
) *resourceTestEnvironment {
	t.Helper()
	master := bytes.Repeat([]byte{0x51}, secret.MasterKeyBytes)
	vault, err := secret.New(master)
	clear(master)
	if err != nil {
		t.Fatalf("secret.New: %v", err)
	}
	t.Cleanup(func() { _ = vault.Close() })
	path := filepath.Join(t.TempDir(), "resources.sqlite")
	dbtest.EnsureOwnerOnlyParent(t, path)
	store, err := db.Open(path, vault)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	worker, err := NewDiscoveryWorkerPool(maxConcurrent, maxAdmitted, timeout)
	if err != nil {
		t.Fatalf("NewDiscoveryWorkerPool: %v", err)
	}
	t.Cleanup(worker.Close)
	authorizer := &resourceTestAuthorizer{}
	secrets := &resourceTestSecretWriter{}
	deletions := &resourceTestEndpointKeyDeletionHook{}
	discovery := &resourceTestDiscoveryRail{result: DiscoveryClaimResult{Succeeded: true, Models: []DiscoveredModel{}}}
	clock := &atomic.Int64{}
	clock.Store(resourceTestNow)
	repository, err := New(Config{
		Store: store, Connectors: connector.NewDefaultRegistry(),
		BaseURLs: resourceTestBaseURLValidator{}, Secrets: secrets,
		KeyDeletion:   deletions,
		DiscoveryRail: discovery, DiscoveryWorker: worker,
		CursorKeys: vault, FinalAuth: authorizer,
		Now: func() time.Time { return time.Unix(clock.Load(), 0) },
	})
	if err != nil {
		t.Fatalf("resources.New: %v", err)
	}
	return &resourceTestEnvironment{
		store: store, vault: vault, repository: repository,
		authorizer: authorizer, secrets: secrets, deletions: deletions, discovery: discovery,
		worker: worker,
		clock:  clock,
	}
}

func (environment *resourceTestEnvironment) seedUser(t *testing.T, discordID string) int64 {
	t.Helper()
	zero := make([]byte, 16)
	result, err := environment.store.DB().Exec(`
INSERT INTO users(
 discord_id,username,donation_credit_mag,total_requests,total_uncached_input_tokens,
 total_cache_write_input_tokens,total_cache_read_input_tokens,total_output_tokens,
 total_unknown_usage_requests,revision,created_at,updated_at
) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`,
		discordID, "resource tester", zero, zero, zero, zero, zero, zero, zero, zero,
		resourceTestNow, resourceTestNow)
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}
	userID, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("seed user id: %v", err)
	}
	if _, err := environment.store.DB().Exec(`
INSERT INTO caller_keys(user_id,generation,key_hash,display_head,display_tail,key_created_at,updated_at)
VALUES(?,0,NULL,'','',NULL,?)`, userID, resourceTestNow); err != nil {
		t.Fatalf("seed caller key: %v", err)
	}
	return userID
}

func (environment *resourceTestEnvironment) rowCount(t *testing.T, query string, args ...any) int {
	t.Helper()
	var count int
	if err := environment.store.DB().QueryRow(query, args...).Scan(&count); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	return count
}

func (environment *resourceTestEnvironment) createEndpoint(t *testing.T, userID int64, key string) Endpoint {
	return environment.createEndpointWithConnector(t, userID, key, "openai-compatible", true)
}

func (environment *resourceTestEnvironment) createEndpointWithConnector(t *testing.T, userID int64, key, connectorType string, enabled bool) Endpoint {
	t.Helper()
	input := CreateEndpointInput{
		ConnectorType: connectorType, BaseURL: "https://example.com/v1",
		Note: "endpoint note", Enabled: enabled,
	}
	mutation := resourceTestMutation(t, key, "POST", routeEndpoints, nil, createEndpointCanonical{
		ConnectorType: input.ConnectorType, BaseURL: input.BaseURL, Note: input.Note, Enabled: input.Enabled,
	})
	result, err := environment.repository.CreateEndpoint(context.Background(), userID, mutation, input)
	if err != nil {
		t.Fatalf("CreateEndpoint: %v", err)
	}
	return result.Value
}

func (environment *resourceTestEnvironment) createEndpointKey(t *testing.T, userID, endpointID int64, key string) EndpointKey {
	t.Helper()
	input := CreateEndpointKeyInput{
		Secret: []byte("ephemeral-upstream-credential"), Note: "key note",
		Enabled: true, OwnershipConfirmed: true,
	}
	mutation := resourceTestMutation(t, key, "POST", routeEndpointKeys, []int64{endpointID}, createEndpointKeyCanonical{
		Secret: "ephemeral-upstream-credential", Note: input.Note, Enabled: input.Enabled,
		ForceStoreFalse: input.ForceStoreFalse, OwnershipConfirmed: input.OwnershipConfirmed,
	})
	result, err := environment.repository.CreateEndpointKey(context.Background(), userID, endpointID, mutation, input)
	clear(input.Secret)
	if err != nil {
		t.Fatalf("CreateEndpointKey: %v", err)
	}
	return result.Value
}

func (environment *resourceTestEnvironment) createModel(t *testing.T, userID int64, key, provider, model string) Model {
	t.Helper()
	input := CreateModelInput{Provider: provider, Model: model, RouteStrategy: "ordered"}
	mutation := resourceTestMutation(t, key, "POST", routeModels, nil, createModelCanonical{
		Provider: provider, Model: model, RouteStrategy: pointer("ordered"),
	})
	result, err := environment.repository.CreateModel(context.Background(), userID, mutation, input)
	if err != nil {
		t.Fatalf("CreateModel: %v", err)
	}
	return result.Value
}

func resourceTestMutation(t *testing.T, key, method, route string, ids []int64, body any) ControlMutation {
	t.Helper()
	canonical, err := idempotency.CanonicalJSON(body)
	if err != nil {
		t.Fatalf("CanonicalJSON: %v", err)
	}
	pathIDs := make([]string, len(ids))
	for index, id := range ids {
		pathIDs[index] = fmt.Sprintf("%d", id)
	}
	return ControlMutation{
		IdempotencyKey: key, Method: method, Route: route,
		PathIDs: pathIDs, CanonicalBody: canonical,
	}
}

func resourceTestKey(seed byte) string {
	return string(bytes.Repeat([]byte{seed}, 22))
}

func pointer[T any](value T) *T { return &value }

func resourceTestID(t *testing.T, value string) int64 {
	t.Helper()
	id, err := parseDecimalID(value)
	if err != nil {
		t.Fatalf("parse id %q: %v", value, err)
	}
	return id
}

type resourceTestAuthorizer struct {
	deny  atomic.Bool
	calls atomic.Int64
}

func (authorizer *resourceTestAuthorizer) AuthorizeUserMutation(ctx context.Context, tx *sql.Tx, userID int64) error {
	authorizer.calls.Add(1)
	if authorizer.deny.Load() {
		return ErrForbidden
	}
	var isAdmin, isBanned int
	var bannedUntil sql.NullInt64
	err := tx.QueryRowContext(ctx, `SELECT is_admin,is_banned,banned_until FROM users WHERE id=?`, userID).Scan(&isAdmin, &isBanned, &bannedUntil)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrUnauthorized
	}
	if err != nil {
		return err
	}
	if isAdmin != 0 || (isBanned != 0 && (!bannedUntil.Valid || bannedUntil.Int64 > resourceTestNow)) {
		return ErrForbidden
	}
	return nil
}

type resourceTestBaseURLValidator struct{}

func (resourceTestBaseURLValidator) ValidateBaseURL(value string) (string, error) {
	if value != "https://example.com/v1" && value != "https://other.example/v1" {
		return "", errors.New("invalid base URL")
	}
	return value, nil
}

type resourceTestSecretWriter struct {
	mu            sync.Mutex
	nextContext   uint64
	writes        int
	orphans       int
	lastPlaintext []byte
}

type resourceTestEndpointKeyDeletionCall struct {
	ownerUserID int64
	keyIDs      []int64
	decisionNow int64
}

type resourceTestEndpointKeyDeletionHook struct {
	mu    sync.Mutex
	calls []resourceTestEndpointKeyDeletionCall
	run   func(context.Context, *sql.Tx, int64, []int64, int64) error
}

func (hook *resourceTestEndpointKeyDeletionHook) PrepareEndpointKeyDeletion(
	ctx context.Context,
	tx *sql.Tx,
	ownerUserID int64,
	keyIDs []int64,
	decisionNow int64,
) error {
	call := resourceTestEndpointKeyDeletionCall{
		ownerUserID: ownerUserID,
		keyIDs:      append([]int64(nil), keyIDs...),
		decisionNow: decisionNow,
	}
	hook.mu.Lock()
	hook.calls = append(hook.calls, call)
	run := hook.run
	hook.mu.Unlock()
	if run != nil {
		return run(ctx, tx, ownerUserID, append([]int64(nil), keyIDs...), decisionNow)
	}
	return nil
}

func (writer *resourceTestSecretWriter) WriteEndpointSecret(ctx context.Context, tx *sql.Tx, input SecretWriteInput) (StoredSecret, error) {
	if len(input.Plaintext) == 0 || input.CanonicalBaseURL == "" || input.ConnectorType == "" {
		return StoredSecret{}, errors.New("invalid secret write")
	}
	writer.mu.Lock()
	writer.nextContext++
	sequence := writer.nextContext
	writer.writes++
	clear(writer.lastPlaintext)
	writer.lastPlaintext = append(writer.lastPlaintext[:0], input.Plaintext...)
	writer.mu.Unlock()
	contextID := make([]byte, 16)
	binary.BigEndian.PutUint64(contextID[8:], sequence)
	result, err := tx.ExecContext(ctx, `
INSERT INTO endpoint_key_secrets(context_id,canonical_base_url,connector_type,encrypted_secret,created_at,orphaned_at)
VALUES(?,?,?,'test-only-envelope',?,NULL)`, contextID, input.CanonicalBaseURL, input.ConnectorType, input.CreatedAt)
	clear(contextID)
	if err != nil {
		return StoredSecret{}, err
	}
	refID, err := result.LastInsertId()
	if err != nil {
		return StoredSecret{}, err
	}
	fingerprint := sha256.Sum256(input.Plaintext)
	return StoredSecret{RefID: refID, Fingerprint: fingerprint, DisplayHead: "head", DisplayTail: "tail"}, nil
}

func (writer *resourceTestSecretWriter) MarkEndpointSecretOrphaned(ctx context.Context, tx *sql.Tx, refID, at int64) error {
	result, err := tx.ExecContext(ctx, `UPDATE endpoint_key_secrets SET orphaned_at=? WHERE id=? AND orphaned_at IS NULL`, at, refID)
	if err != nil {
		return err
	}
	updated, err := result.RowsAffected()
	if err != nil || updated != 1 {
		return errors.New("secret orphan CAS failed")
	}
	writer.mu.Lock()
	writer.orphans++
	writer.mu.Unlock()
	return nil
}

type resourceTestDiscoveryRail struct {
	mu       sync.Mutex
	result   DiscoveryClaimResult
	err      error
	calls    int
	inputs   []DiscoveryClaimInput
	started  chan struct{}
	release  <-chan struct{}
	finished chan error
}

func (rail *resourceTestDiscoveryRail) Discover(ctx context.Context, input DiscoveryClaimInput) (DiscoveryClaimResult, error) {
	rail.mu.Lock()
	rail.calls++
	rail.inputs = append(rail.inputs, input)
	result := rail.result
	result.Models = append([]DiscoveredModel(nil), rail.result.Models...)
	err := rail.err
	started := rail.started
	release := rail.release
	finished := rail.finished
	rail.mu.Unlock()
	if started != nil {
		select {
		case started <- struct{}{}:
		default:
		}
	}
	if release != nil {
		var waitErr error
		select {
		case <-release:
		case <-ctx.Done():
			waitErr = ctx.Err()
		}
		if finished != nil {
			select {
			case finished <- waitErr:
			default:
			}
		}
		if waitErr != nil {
			return DiscoveryClaimResult{}, waitErr
		}
	}
	return result, err
}
