package issues

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/dbfixture"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

const issueTestNow int64 = 1_700_000_000

type issueTestEnvironment struct {
	store      *db.Store
	vault      *secret.Vault
	repository *Repository
	service    *Service
	validation *issueValidationAuthority
	clock      *atomic.Int64
	sequence   atomic.Uint64
}

func newIssueTestEnvironment(t *testing.T) *issueTestEnvironment {
	t.Helper()
	master := bytes.Repeat([]byte{0x42}, secret.MasterKeyBytes)
	vault, err := secret.New(master)
	clear(master)
	if err != nil {
		t.Fatalf("secret.New: %v", err)
	}
	t.Cleanup(func() { _ = vault.Close() })
	path := filepath.Join(t.TempDir(), "issues.sqlite")
	dbfixture.Materialize(t, path)
	store, err := db.Open(path, vault)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	clock := &atomic.Int64{}
	clock.Store(issueTestNow)
	validation := newIssueValidationAuthority()
	repository, err := NewRepository(Config{
		Store: store, CursorKeys: vault, ResourceValidation: validation,
		Now: func() time.Time { return time.Unix(clock.Load(), 0) },
	})
	if err != nil {
		t.Fatalf("NewRepository: %v", err)
	}
	service, err := NewService(repository)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return &issueTestEnvironment{
		store: store, vault: vault, repository: repository, service: service,
		validation: validation, clock: clock,
	}
}

func (environment *issueTestEnvironment) seedUser(t *testing.T, discordID string) int64 {
	t.Helper()
	zero := make([]byte, 16)
	result, err := environment.store.DB().Exec(`
INSERT INTO users(
 discord_id,username,is_admin,donation_credit_mag,total_requests,total_uncached_input_tokens,
 total_cache_write_input_tokens,total_cache_read_input_tokens,total_output_tokens,total_unknown_usage_requests,
 revision,lang,created_at,updated_at
) VALUES(?,?,0,?,?,?,?,?,?,?,?,?,?,?)`,
		discordID, "issue tester", zero, zero, zero, zero, zero, zero, zero, zero,
		"en", issueTestNow, issueTestNow)
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("seed user id: %v", err)
	}
	return id
}

func (environment *issueTestEnvironment) seedEndpoint(t *testing.T, userID int64) int64 {
	t.Helper()
	result, err := environment.store.DB().Exec(`
INSERT INTO endpoints(user_id,connector_type,base_url,note,enabled,revision,created_at,updated_at)
VALUES(?,'openai-compatible',?,'',1,1,?,?)`, userID,
		fmt.Sprintf("https://endpoint-%d.example/v1", environment.sequence.Add(1)), issueTestNow, issueTestNow)
	if err != nil {
		t.Fatalf("seed endpoint: %v", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("seed endpoint id: %v", err)
	}
	return id
}

func (environment *issueTestEnvironment) seedEndpointKey(t *testing.T, endpointID int64) int64 {
	t.Helper()
	sequence := environment.sequence.Add(1)
	contextID := make([]byte, 16)
	binary.BigEndian.PutUint64(contextID[8:], sequence)
	secretResult, err := environment.store.DB().Exec(`
INSERT INTO endpoint_key_secrets(context_id,canonical_base_url,connector_type,encrypted_secret,created_at)
VALUES(?,'https://upstream.example/v1','openai-compatible','test-envelope',?)`, contextID, issueTestNow)
	if err != nil {
		t.Fatalf("seed endpoint key secret: %v", err)
	}
	secretID, err := secretResult.LastInsertId()
	if err != nil {
		t.Fatalf("seed endpoint key secret id: %v", err)
	}
	fingerprint := make([]byte, 32)
	binary.BigEndian.PutUint64(fingerprint[24:], sequence)
	keyResult, err := environment.store.DB().Exec(`
INSERT INTO endpoint_keys(
 endpoint_id,secret_ref_id,secret_fingerprint,display_head,display_tail,note,enabled,force_store_false,revision,created_at,updated_at
) VALUES(?,?,?,'sk-test','tail','',1,0,1,?,?)`, endpointID, secretID, fingerprint, issueTestNow, issueTestNow)
	if err != nil {
		t.Fatalf("seed endpoint key: %v", err)
	}
	keyID, err := keyResult.LastInsertId()
	if err != nil {
		t.Fatalf("seed endpoint key id: %v", err)
	}
	if _, err := environment.store.DB().Exec(`
INSERT INTO model_discovery_evidence(endpoint_key_id,state,revision,safe_class,safe_diag,fetched_count)
VALUES(?,'unknown',1,'none','',0)`, keyID); err != nil {
		t.Fatalf("seed discovery evidence: %v", err)
	}
	return keyID
}

func (environment *issueTestEnvironment) seedModel(t *testing.T, userID int64, suffix string) int64 {
	t.Helper()
	model := "model-" + suffix
	result, err := environment.store.DB().Exec(`
INSERT INTO models(
 user_id,provider,model,full_name,route_strategy,silent_retry,flatten_tool_calls,revision,binding_revision,created_at,updated_at
) VALUES(?,'provider',?,'provider/'||?,'ordered',0,0,1,0,?,?)`, userID, model, model, issueTestNow, issueTestNow)
	if err != nil {
		t.Fatalf("seed model: %v", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("seed model id: %v", err)
	}
	return id
}

func (environment *issueTestEnvironment) setDiscovery(t *testing.T, keyID int64, state, safeClass string, completedAt int64) {
	t.Helper()
	if state == "succeeded" {
		safeClass = "none"
	}
	result, err := environment.store.DB().Exec(`
UPDATE model_discovery_evidence
SET state=?,revision=revision+1,operation_hash=NULL,safe_class=?,safe_diag='',started_at=NULL,completed_at=?,fetched_count=0
WHERE endpoint_key_id=?`, state, safeClass, completedAt, keyID)
	if err != nil {
		t.Fatalf("set discovery: %v", err)
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		t.Fatalf("set discovery changed=%d", changed)
	}
}

func (environment *issueTestEnvironment) makeBindingAvailable(t *testing.T, modelID, keyID int64, upstream string) {
	t.Helper()
	if _, err := environment.store.DB().Exec(`
INSERT INTO model_pair_catalog(
 endpoint_key_id,normalized_model_id,automatic_supports,manual_supports,automatic_revision,pair_revision,updated_at
) VALUES(?,?,0,1,0,1,?)`, keyID, upstream, issueTestNow); err != nil {
		t.Fatalf("seed pair catalog: %v", err)
	}
	if _, err := environment.store.DB().Exec(`
INSERT INTO model_bindings(model_id,endpoint_key_id,upstream_model_id,ord,created_at,updated_at)
VALUES(?,?,?,0,?,?)`, modelID, keyID, upstream, issueTestNow, issueTestNow); err != nil {
		t.Fatalf("seed binding: %v", err)
	}
}

func (environment *issueTestEnvironment) insertReportReason(t *testing.T, keyID int64) string {
	t.Helper()
	sequence := environment.sequence.Add(1)
	caseID := issueOpaqueID("rpc_", sequence)
	fingerprint := make([]byte, 32)
	binary.BigEndian.PutUint64(fingerprint[24:], sequence)
	if _, err := environment.store.DB().Exec(`
INSERT INTO report_cases(
 id,fingerprint,connector_type,canonical_base_url,status,progress_state,material_version,target_version,
 deadline,material_count,target_count,distinct_owner_count,created_at
) VALUES(?,?,'openai-compatible','https://upstream.example/v1','pending_review','complete',1,1,?,0,0,0,?)`,
		caseID, fingerprint, environment.clock.Load()+3600, environment.clock.Load()); err != nil {
		t.Fatalf("seed report case: %v", err)
	}
	if _, err := environment.store.DB().Exec(`
INSERT INTO endpoint_key_suspensions(endpoint_key_id,reason_type,report_case_id,created_at)
VALUES(?,'report_case',?,?)`, keyID, caseID, environment.clock.Load()); err != nil {
		t.Fatalf("seed report reason: %v", err)
	}
	return caseID
}

func issueOpaqueID(prefix string, value uint64) string {
	raw := make([]byte, 16)
	binary.BigEndian.PutUint64(raw[8:], value)
	return prefix + base64.RawURLEncoding.EncodeToString(raw)
}

type issueValidationKey struct {
	userID     int64
	kind       ResourceKind
	resourceID int64
	root       RootCause
}

type issueValidationAuthority struct {
	mu      sync.Mutex
	states  map[issueValidationKey]ResourceValidationState
	targets map[int64][]ResourceValidationTarget
}

func newIssueValidationAuthority() *issueValidationAuthority {
	return &issueValidationAuthority{
		states:  make(map[issueValidationKey]ResourceValidationState),
		targets: make(map[int64][]ResourceValidationTarget),
	}
}

func (authority *issueValidationAuthority) set(userID int64, kind ResourceKind, resourceID int64, root RootCause, state ResourceValidationState) {
	authority.mu.Lock()
	defer authority.mu.Unlock()
	key := issueValidationKey{userID: userID, kind: kind, resourceID: resourceID, root: root}
	authority.states[key] = state
	target := ResourceValidationTarget{ResourceKind: kind, ResourceID: resourceID, RootCause: root}
	targets := authority.targets[userID]
	found := false
	for _, current := range targets {
		if current == target {
			found = true
			break
		}
	}
	if !found {
		targets = append(targets, target)
		sort.Slice(targets, func(i, j int) bool {
			if targets[i].ResourceKind != targets[j].ResourceKind {
				return targets[i].ResourceKind < targets[j].ResourceKind
			}
			if targets[i].ResourceID != targets[j].ResourceID {
				return targets[i].ResourceID < targets[j].ResourceID
			}
			return targets[i].RootCause < targets[j].RootCause
		})
		authority.targets[userID] = targets
	}
}

func (authority *issueValidationAuthority) remove(userID int64, kind ResourceKind, resourceID int64, root RootCause) {
	authority.mu.Lock()
	defer authority.mu.Unlock()
	delete(authority.states, issueValidationKey{userID: userID, kind: kind, resourceID: resourceID, root: root})
	target := ResourceValidationTarget{ResourceKind: kind, ResourceID: resourceID, RootCause: root}
	items := authority.targets[userID]
	filtered := items[:0]
	for _, item := range items {
		if item != target {
			filtered = append(filtered, item)
		}
	}
	authority.targets[userID] = filtered
}

func (authority *issueValidationAuthority) Current(_ context.Context, _ *sql.Tx, userID int64, kind ResourceKind, resourceID int64, root RootCause) (ResourceValidationState, error) {
	authority.mu.Lock()
	defer authority.mu.Unlock()
	return authority.states[issueValidationKey{userID: userID, kind: kind, resourceID: resourceID, root: root}], nil
}

func (authority *issueValidationAuthority) Scan(_ context.Context, _ *sql.Tx, userID int64, cursor string, limit int) (ResourceValidationBatch, error) {
	authority.mu.Lock()
	defer authority.mu.Unlock()
	if limit < 1 {
		return ResourceValidationBatch{}, fmt.Errorf("invalid limit")
	}
	start := 0
	if cursor != "" {
		parsed, err := strconv.Atoi(cursor)
		if err != nil || parsed < 0 || strconv.Itoa(parsed) != cursor {
			return ResourceValidationBatch{}, fmt.Errorf("invalid cursor")
		}
		start = parsed
	}
	targets := authority.targets[userID]
	if start > len(targets) {
		return ResourceValidationBatch{}, fmt.Errorf("cursor beyond target set")
	}
	end := start + limit
	if end > len(targets) {
		end = len(targets)
	}
	items := append([]ResourceValidationTarget(nil), targets[start:end]...)
	if end == len(targets) {
		return ResourceValidationBatch{Items: items, Done: true}, nil
	}
	return ResourceValidationBatch{Items: items, NextCursor: strconv.Itoa(end)}, nil
}
