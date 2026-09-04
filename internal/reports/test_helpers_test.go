package reports

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/connector"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/dbfixture"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

const (
	reportTestNow       int64 = 1_800_000_000
	reportTestConnector       = "openai-compatible"
	reportTestBaseURL         = "https://example.com/v1"
)

type reportTestOptions struct {
	delay           DelayFunc
	random          io.Reader
	deleteHook      *reportTestDeletionHook
	issueProjection IssueProjectionHook
}

type reportTestEnvironment struct {
	store      *db.Store
	vault      *secret.Vault
	repository *Repository
	clock      atomic.Int64
	ids        atomic.Uint64
	secrets    atomic.Uint64
	deletions  *reportTestDeletionHook
}

type reportTestDeletionCall struct {
	ownerUserID   int64
	endpointKeyID int64
	decisionNow   int64
}

type reportTestDeletionHook struct {
	mu         sync.Mutex
	calls      []reportTestDeletionCall
	failAlways bool
	failOnCall map[int]bool
	failure    error
}

func (hook *reportTestDeletionHook) deleteKey(
	ctx context.Context,
	tx *sql.Tx,
	ownerUserID int64,
	endpointKeyID int64,
	decisionNow int64,
) error {
	hook.mu.Lock()
	hook.calls = append(hook.calls, reportTestDeletionCall{
		ownerUserID: ownerUserID, endpointKeyID: endpointKeyID, decisionNow: decisionNow,
	})
	callNumber := len(hook.calls)
	shouldFail := hook.failAlways || hook.failOnCall != nil && hook.failOnCall[callNumber]
	hook.mu.Unlock()
	if shouldFail {
		if hook.failure != nil {
			return hook.failure
		}
		return errors.New("fixture endpoint-key deletion failure")
	}
	var secretRefID int64
	if err := tx.QueryRowContext(ctx, `SELECT k.secret_ref_id FROM endpoint_keys k
JOIN endpoints e ON e.id=k.endpoint_id WHERE k.id=? AND e.user_id=?`, endpointKeyID, ownerUserID).Scan(&secretRefID); err != nil {
		return fmt.Errorf("fixture read endpoint-key secret: %w", err)
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM endpoint_keys WHERE id=?`, endpointKeyID)
	if err != nil {
		return fmt.Errorf("fixture delete endpoint key: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil || changed != 1 {
		return errors.New("fixture endpoint-key deletion did not remove one row")
	}
	if _, err := tx.ExecContext(ctx, `UPDATE endpoint_key_secrets SET orphaned_at=?
WHERE id=? AND orphaned_at IS NULL`, decisionNow, secretRefID); err != nil {
		return fmt.Errorf("fixture orphan endpoint-key secret: %w", err)
	}
	return nil
}

func (hook *reportTestDeletionHook) callCount() int {
	hook.mu.Lock()
	defer hook.mu.Unlock()
	return len(hook.calls)
}

type reportTestBaseURLs struct{}

type reportTestIssueProjection struct{}

func (reportTestIssueProjection) ReconcileReportReason(context.Context, *sql.Tx, int64, int64) error {
	return nil
}

func (reportTestBaseURLs) ValidateBaseURL(value string) (string, error) {
	switch value {
	case reportTestBaseURL:
		return reportTestBaseURL, nil
	case " HTTPS://EXAMPLE.COM:443//v1 ":
		return reportTestBaseURL, nil
	case "https://other.example/v1":
		return value, nil
	default:
		return "", errors.New("fixture rejected base URL")
	}
}

func newReportTestEnvironment(t *testing.T) *reportTestEnvironment {
	t.Helper()
	return newReportTestEnvironmentWith(t, reportTestOptions{})
}

func newReportTestEnvironmentWith(t *testing.T, options reportTestOptions) *reportTestEnvironment {
	t.Helper()
	master := bytes.Repeat([]byte{0x63}, secret.MasterKeyBytes)
	vault, err := secret.New(master)
	clear(master)
	if err != nil {
		t.Fatalf("secret.New: %v", err)
	}
	path := filepath.Join(t.TempDir(), "reports.sqlite")
	dbfixture.Materialize(t, path)
	store, err := db.Open(path, vault)
	if err != nil {
		_ = vault.Close()
		t.Fatalf("db.Open: %v", err)
	}
	environment := &reportTestEnvironment{store: store, vault: vault}
	environment.clock.Store(reportTestNow)
	if options.deleteHook == nil {
		options.deleteHook = &reportTestDeletionHook{}
	}
	environment.deletions = options.deleteHook
	if options.delay == nil {
		options.delay = func(ctx context.Context, _ time.Duration) error {
			return context.Cause(ctx)
		}
	}
	if options.issueProjection == nil {
		options.issueProjection = reportTestIssueProjection{}
	}
	repository, err := New(Config{
		Store: store, Connectors: connector.NewDefaultRegistry(), BaseURLs: reportTestBaseURLs{},
		KeyDeriver: vault,
		Authorizer: authz.New(authz.Options{Now: func() time.Time {
			return time.Unix(environment.clock.Load(), 0)
		}}),
		DeleteKey: options.deleteHook.deleteKey, IssueProjection: options.issueProjection, Random: options.random,
		Now:   func() time.Time { return time.Unix(environment.clock.Load(), 0) },
		Delay: options.delay, GenerateID: environment.generateID,
	})
	if err != nil {
		_ = store.Close()
		_ = vault.Close()
		t.Fatalf("reports.New: %v", err)
	}
	environment.repository = repository
	t.Cleanup(func() { _ = store.Close() })
	t.Cleanup(func() { _ = vault.Close() })
	t.Cleanup(func() { _ = repository.Close() })
	return environment
}

func (environment *reportTestEnvironment) generateID(prefix string) (string, error) {
	if prefix == "" {
		return "", errors.New("empty fixture ID prefix")
	}
	sequence := environment.ids.Add(1)
	var raw [16]byte
	binary.BigEndian.PutUint64(raw[8:], sequence)
	return prefix + base64.RawURLEncoding.EncodeToString(raw[:]), nil
}

func reportTestOpaqueID(prefix string, sequence uint64) string {
	var raw [16]byte
	binary.BigEndian.PutUint64(raw[8:], sequence)
	return prefix + base64.RawURLEncoding.EncodeToString(raw[:])
}

func reportTestKey(sequence int) string {
	return fmt.Sprintf("r-%020d", sequence)
}

func reportTestIP(sequence int) [16]byte {
	address := netip.MustParseAddr(fmt.Sprintf("2001:db8::%x", sequence+1))
	return address.As16()
}

func (environment *reportTestEnvironment) setNow(value int64) {
	environment.clock.Store(value)
}

func (environment *reportTestEnvironment) seedActor(t *testing.T, admin bool, level int) authz.Actor {
	t.Helper()
	now := environment.clock.Load()
	zero := make([]byte, 16)
	sequence := environment.ids.Add(1)
	var discord any = fmt.Sprintf("discord-%d", sequence)
	if admin {
		discord = nil
	}
	var manualLevel any
	autoLevel := 1
	if level == 5 {
		manualLevel = 5
		autoLevel = 4
	}
	result, err := environment.store.DB().Exec(`INSERT INTO users(
discord_id,username,is_admin,donation_credit_mag,level,auto_level,total_requests,
total_uncached_input_tokens,total_cache_write_input_tokens,total_cache_read_input_tokens,
total_output_tokens,total_unknown_usage_requests,revision,created_at,updated_at)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, discord, fmt.Sprintf("report-user-%d", sequence), boolInt(admin), zero,
		manualLevel, autoLevel, zero, zero, zero, zero, zero, zero, zero, now, now)
	if err != nil {
		t.Fatalf("seed report actor: %v", err)
	}
	userID, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("seed report actor id: %v", err)
	}
	token := fmt.Sprintf("report-session-%d", sequence)
	generation := fmt.Sprintf("report-generation-%d", sequence)
	if _, err := environment.store.DB().Exec(`INSERT INTO sessions(
token_hash,user_id,last_seen_at,expires_at,absolute_expires_at,created_at,cred_gen)
VALUES(?,?,?,?,?,?,?)`, token, userID, now, now+3600, now+7200, now, generation); err != nil {
		t.Fatalf("seed report session: %v", err)
	}
	kind := authz.ActorUserSession
	if admin {
		kind = authz.ActorAdminSession
	}
	return authz.Actor{Kind: kind, UserID: userID, SessionTokenHash: token, SessionGeneration: generation}
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func (environment *reportTestEnvironment) seedEndpoint(t *testing.T, userID int64, connectorType, baseURL string) int64 {
	t.Helper()
	now := environment.clock.Load()
	result, err := environment.store.DB().Exec(`INSERT INTO endpoints(
user_id,connector_type,base_url,note,enabled,revision,created_at,updated_at)
VALUES(?,?,?,'fixture endpoint',1,1,?,?)`, userID, connectorType, baseURL, now, now)
	if err != nil {
		t.Fatalf("seed endpoint: %v", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("seed endpoint id: %v", err)
	}
	return id
}

func (environment *reportTestEnvironment) seedEndpointKey(
	t *testing.T,
	endpointID int64,
	secretValue string,
	createdAt int64,
) int64 {
	t.Helper()
	tx, err := environment.store.DB().BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin endpoint-key fixture: %v", err)
	}
	id, err := environment.insertEndpointKeyTx(context.Background(), tx, endpointID, []byte(secretValue), createdAt)
	if err == nil {
		err = tx.Commit()
	} else {
		_ = tx.Rollback()
	}
	if err != nil {
		t.Fatalf("seed endpoint key: %v", err)
	}
	return id
}

func (environment *reportTestEnvironment) insertEndpointKeyTx(
	ctx context.Context,
	tx *sql.Tx,
	endpointID int64,
	secretValue []byte,
	createdAt int64,
) (int64, error) {
	var connectorType, baseURL string
	if err := tx.QueryRowContext(ctx, `SELECT connector_type,base_url FROM endpoints WHERE id=?`, endpointID).Scan(
		&connectorType, &baseURL,
	); err != nil {
		return 0, err
	}
	fingerprint, err := environment.repository.keys.fingerprintDigest(connectorType, baseURL, secretValue)
	if err != nil {
		return 0, err
	}
	secretSequence := environment.secrets.Add(1)
	var contextID [16]byte
	binary.BigEndian.PutUint64(contextID[8:], secretSequence)
	result, err := tx.ExecContext(ctx, `INSERT INTO endpoint_key_secrets(
context_id,canonical_base_url,connector_type,encrypted_secret,created_at,orphaned_at)
VALUES(?,?,?,'fixture-envelope',?,NULL)`, contextID[:], baseURL, connectorType, createdAt)
	if err != nil {
		return 0, err
	}
	secretRefID, err := result.LastInsertId()
	if err != nil {
		return 0, err
	}
	result, err = tx.ExecContext(ctx, `INSERT INTO endpoint_keys(
endpoint_id,secret_ref_id,secret_fingerprint,display_head,display_tail,note,enabled,
force_store_false,revision,created_at,updated_at)
VALUES(?,?,?,'head','tail','fixture key',1,0,1,?,?)`, endpointID, secretRefID, fingerprint[:], createdAt, createdAt)
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

func (environment *reportTestEnvironment) submission(
	secretValue string,
	note string,
	keySequence int,
	ipSequence int,
	reporter *authz.Actor,
) PublicSubmission {
	return PublicSubmission{
		ConnectorType: reportTestConnector, CanonicalBaseURL: reportTestBaseURL,
		Secret: []byte(secretValue), Note: note, IdempotencyKey: reportTestKey(keySequence),
		SourceIP: reportTestIP(ipSequence), Reporter: reporter,
	}
}

func (environment *reportTestEnvironment) accept(
	t *testing.T,
	secretValue string,
	note string,
	keySequence int,
	ipSequence int,
	reporter *authz.Actor,
) {
	t.Helper()
	if err := environment.repository.AcceptCredentialTheft(
		context.Background(), environment.submission(secretValue, note, keySequence, ipSequence, reporter),
	); err != nil {
		t.Fatalf("AcceptCredentialTheft: %v", err)
	}
}

func (environment *reportTestEnvironment) caseIDForSecret(t *testing.T, secretValue string) string {
	t.Helper()
	fingerprint, err := environment.repository.keys.fingerprintDigest(reportTestConnector, reportTestBaseURL, []byte(secretValue))
	if err != nil {
		t.Fatalf("fingerprint report secret: %v", err)
	}
	var caseID string
	if err := environment.store.DB().QueryRow(`SELECT id FROM report_cases WHERE fingerprint=?
ORDER BY created_at DESC,id DESC LIMIT 1`, fingerprint[:]).Scan(&caseID); err != nil {
		t.Fatalf("read report case: %v", err)
	}
	return caseID
}

func (environment *reportTestEnvironment) rowCount(t *testing.T, query string, args ...any) int64 {
	t.Helper()
	var count int64
	if err := environment.store.DB().QueryRow(query, args...).Scan(&count); err != nil {
		t.Fatalf("count report fixture rows: %v", err)
	}
	return count
}

func (environment *reportTestEnvironment) caseState(t *testing.T, caseID string) (string, string, int64, int64) {
	t.Helper()
	var status, progress string
	var materialVersion, targetVersion int64
	if err := environment.store.DB().QueryRow(`SELECT status,progress_state,material_version,target_version
FROM report_cases WHERE id=?`, caseID).Scan(&status, &progress, &materialVersion, &targetVersion); err != nil {
		t.Fatalf("read report case state: %v", err)
	}
	return status, progress, materialVersion, targetVersion
}

func (environment *reportTestEnvironment) runUntilIdle(t *testing.T, maxPasses int) {
	t.Helper()
	for pass := 0; pass < maxPasses; pass++ {
		result, err := environment.repository.RunWorkerOnce(context.Background())
		if err != nil {
			t.Fatalf("RunWorkerOnce pass %d: %v", pass, err)
		}
		if !result.More {
			return
		}
	}
	t.Fatalf("report worker did not become idle within %d passes", maxPasses)
}
