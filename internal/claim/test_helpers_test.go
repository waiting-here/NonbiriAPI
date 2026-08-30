package claim

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

type testCodec struct {
	mu       sync.Mutex
	values   map[string][]byte
	opens    int
	failOpen bool
	onOpen   func()
}

func newTestCodec() *testCodec {
	return &testCodec{values: make(map[string][]byte)}
}

func (c *testCodec) OpenForGenerationTwoContext(
	ciphertext string,
	_ secret.GenerationTwoEndpointKeyContext,
) ([]byte, error) {
	c.mu.Lock()
	c.opens++
	value, exists := c.values[ciphertext]
	fail := c.failOpen
	callback := c.onOpen
	c.mu.Unlock()
	if callback != nil {
		callback()
	}
	if fail || !exists {
		return nil, errors.New("test codec rejected envelope")
	}
	return append([]byte(nil), value...), nil
}

func (c *testCodec) SealForGenerationTwoContext(
	plaintext []byte,
	_ secret.GenerationTwoEndpointKeyContext,
) (string, error) {
	if len(plaintext) == 0 {
		return "", errors.New("test codec rejected plaintext")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	envelope := "fixture-envelope-sealed-" + strconv.Itoa(len(c.values)+1)
	c.values[envelope] = append([]byte(nil), plaintext...)
	return envelope, nil
}

func (c *testCodec) add(envelope string, plaintext []byte) {
	c.mu.Lock()
	c.values[envelope] = append([]byte(nil), plaintext...)
	c.mu.Unlock()
}

func (c *testCodec) openCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.opens
}

type testAccounting struct {
	mu               sync.Mutex
	failPoint        string
	reservations     []RequestReservation
	claimReleases    []ClaimAccounting
	claimCompletions []ClaimAccounting
	requests         []RequestAccounting
}

var errTestAccounting = errors.New("injected test accounting failure")

func (a *testAccounting) failAt(point string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.failPoint == point {
		return errTestAccounting
	}
	return nil
}

func (a *testAccounting) setFailPoint(point string) {
	a.mu.Lock()
	a.failPoint = point
	a.mu.Unlock()
}

func (a *testAccounting) ReserveRequest(
	ctx context.Context,
	tx *sql.Tx,
	input RequestReservation,
	persist DomainPersistence,
) error {
	if persist == nil {
		return errors.New("missing acceptance persistence")
	}
	if _, err := testRemainingRows(tx, input.RequestID); !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("request existed before reserve: %w", err)
	}
	if err := persist(ctx, tx); err != nil {
		return err
	}
	if err := a.failAt("reserve_after_persistence"); err != nil {
		return err
	}
	remaining, err := testRemainingRows(tx, input.RequestID)
	if err != nil {
		return err
	}
	if remaining != uint64(input.FutureRows) {
		return fmt.Errorf("reservation rows = %d, want %d", remaining, input.FutureRows)
	}
	if err := testAdjustCapacity(ctx, tx, int64(input.FutureRows), 1); err != nil {
		return err
	}
	if err := testInsertLedgerRow(ctx, tx, "request_reserve", input.RequestID); err != nil {
		return err
	}
	a.mu.Lock()
	a.reservations = append(a.reservations, input)
	a.mu.Unlock()
	return nil
}

func (a *testAccounting) ReleaseUndispatched(
	ctx context.Context,
	tx *sql.Tx,
	input ClaimAccounting,
	persist DomainPersistence,
) error {
	before, err := testRemainingRows(tx, input.RequestID)
	if err != nil {
		return err
	}
	if before < 2 || persist == nil {
		return errors.New("invalid undispatched release precondition")
	}
	if err := persist(ctx, tx); err != nil {
		return err
	}
	if err := a.failAt("release_after_persistence"); err != nil {
		return err
	}
	after, err := testRemainingRows(tx, input.RequestID)
	if err != nil {
		return err
	}
	if before-after != 1 {
		return fmt.Errorf("undispatched release delta %d -> %d", before, after)
	}
	if err := testAdjustCapacity(ctx, tx, -1, 0); err != nil {
		return err
	}
	a.mu.Lock()
	a.claimReleases = append(a.claimReleases, input)
	a.mu.Unlock()
	return nil
}

func (a *testAccounting) CompleteAttempt(
	ctx context.Context,
	tx *sql.Tx,
	input ClaimAccounting,
	persist DomainPersistence,
) error {
	before, err := testRemainingRows(tx, input.RequestID)
	if err != nil {
		return err
	}
	if before < 2 || persist == nil {
		return errors.New("invalid attempt completion precondition")
	}
	if err := persist(ctx, tx); err != nil {
		return err
	}
	if err := a.failAt("attempt_after_persistence"); err != nil {
		return err
	}
	after, err := testRemainingRows(tx, input.RequestID)
	if err != nil {
		return err
	}
	if before-after != 1 {
		return fmt.Errorf("attempt completion delta %d -> %d", before, after)
	}
	ledgerDelta := int64(0)
	if input.RewardState == RewardPosted {
		if input.RewardActualMilli <= 0 || input.ReceiverUserID == nil {
			return errors.New("posted reward lacked receiver or positive value")
		}
		ledgerDelta = 1
		if err := testInsertLedgerRow(ctx, tx, "donor_reward", input.ClaimID); err != nil {
			return err
		}
	}
	if err := testAdjustCapacity(ctx, tx, -1, ledgerDelta); err != nil {
		return err
	}
	a.mu.Lock()
	a.claimCompletions = append(a.claimCompletions, input)
	a.mu.Unlock()
	return nil
}

func (a *testAccounting) CompleteRequest(
	ctx context.Context,
	tx *sql.Tx,
	input RequestAccounting,
	releaseUnused DomainPersistence,
	terminal DomainPersistence,
) error {
	remaining, err := testRemainingRows(tx, input.RequestID)
	if err != nil {
		return err
	}
	if remaining != uint64(input.RemainingRows) || remaining < 1 || terminal == nil {
		return fmt.Errorf("terminal precondition rows = %d, want %d", remaining, input.RemainingRows)
	}
	if remaining > 1 {
		if releaseUnused == nil {
			return errors.New("missing unused-row release persistence")
		}
		if err := releaseUnused(ctx, tx); err != nil {
			return err
		}
		if err := a.failAt("request_after_unused"); err != nil {
			return err
		}
		afterRelease, err := testRemainingRows(tx, input.RequestID)
		if err != nil {
			return err
		}
		if afterRelease != 1 {
			return fmt.Errorf("unused-row release left %d rows", afterRelease)
		}
		if err := testAdjustCapacity(ctx, tx, -int64(remaining-1), 0); err != nil {
			return err
		}
	} else if releaseUnused != nil {
		return errors.New("unexpected unused-row release persistence")
	}
	if err := terminal(ctx, tx); err != nil {
		return err
	}
	if err := a.failAt("request_after_terminal"); err != nil {
		return err
	}
	after, err := testRemainingRows(tx, input.RequestID)
	if err != nil {
		return err
	}
	if after != 0 {
		return fmt.Errorf("terminal rows = %d, want 0", after)
	}
	if err := testInsertLedgerRow(ctx, tx, "request_terminal", input.RequestID); err != nil {
		return err
	}
	if err := testAdjustCapacity(ctx, tx, -1, 1); err != nil {
		return err
	}
	a.mu.Lock()
	a.requests = append(a.requests, input)
	a.mu.Unlock()
	return nil
}

func testAdjustCapacity(ctx context.Context, tx *sql.Tx, reservedDelta, ledgerDelta int64) error {
	var last int64
	var reservedRaw, revisionRaw []byte
	if err := tx.QueryRowContext(ctx, `SELECT last_ledger_seq,reserved_future_rows,revision
FROM credit_capacity WHERE id=1`).Scan(&last, &reservedRaw, &revisionRaw); err != nil {
		return err
	}
	reserved, ok := decodeSmallU128(reservedRaw)
	if !ok {
		return errors.New("invalid test global reservation")
	}
	revision, ok := decodeSmallU128(revisionRaw)
	if !ok {
		return errors.New("invalid test capacity revision")
	}
	newReserved := int64(reserved) + reservedDelta
	newLast := last + ledgerDelta
	if newReserved < 0 || newLast < 0 {
		return errors.New("test capacity underflow")
	}
	result, err := tx.ExecContext(ctx, `UPDATE credit_capacity
SET last_ledger_seq=?,reserved_future_rows=?,revision=?
WHERE id=1 AND last_ledger_seq=? AND reserved_future_rows=? AND revision=?`,
		newLast, u128Small(uint64(newReserved)), u128Small(revision+1),
		last, reservedRaw, revisionRaw)
	if err != nil {
		return err
	}
	return requireOneRow(result)
}

func testInsertLedgerRow(ctx context.Context, tx *sql.Tx, kind, sourceID string) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO claim_test_ledger_rows(kind,source_id) VALUES(?,?)`, kind, sourceID)
	return err
}

func testRemainingRows(tx *sql.Tx, requestID string) (uint64, error) {
	var encoded []byte
	if err := tx.QueryRow(`SELECT ledger_rows_remaining FROM logical_requests WHERE id=?`, requestID).Scan(&encoded); err != nil {
		return 0, err
	}
	value, ok := decodeSmallU128(encoded)
	if !ok {
		return 0, errors.New("invalid test request row counter")
	}
	return value, nil
}

type testCharityConfig struct {
	receiverUserID int64
	rewardMilli    int64
	priceMilli     int64
	claimErr       error
}

type testCharity struct {
	mu                 sync.Mutex
	configs            map[int64]testCharityConfig
	acceptErr          error
	releaseErr         error
	completeAttemptErr error
	completeRequestErr error
	accepts            []CharityAcceptance
	claims             []CharityClaimInput
	releases           []CharityRelease
	attempts           []CharityAttemptInput
	completions        []CharityRequestCompletion
}

func newTestCharity() *testCharity {
	return &testCharity{configs: make(map[int64]testCharityConfig)}
}

func (c *testCharity) AcceptRequest(ctx context.Context, tx *sql.Tx, input CharityAcceptance) error {
	c.mu.Lock()
	failure := c.acceptErr
	c.mu.Unlock()
	if failure != nil {
		return failure
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO claim_test_charity_facts(kind,source_id)
VALUES('accept',?)`, input.RequestID); err != nil {
		return err
	}
	c.mu.Lock()
	c.accepts = append(c.accepts, input)
	c.mu.Unlock()
	return nil
}

func (c *testCharity) Claim(_ context.Context, _ *sql.Tx, input CharityClaimInput) (CharityReservation, error) {
	c.mu.Lock()
	config, exists := c.configs[input.DonationKeyID]
	c.claims = append(c.claims, input)
	c.mu.Unlock()
	if !exists {
		return CharityReservation{}, errors.New("test donation key was not configured")
	}
	if config.claimErr != nil {
		return CharityReservation{}, config.claimErr
	}
	return CharityReservation{
		DonationKeyID:      input.DonationKeyID,
		StreakGeneration:   1,
		FrozenPriceMilli:   config.priceMilli,
		FrozenRewardMilli:  config.rewardMilli,
		ReceiverUserID:     config.receiverUserID,
		ReservedPriceMilli: config.priceMilli,
		ReservedCalls:      1,
		ReservedTokens:     32,
	}, nil
}

func (c *testCharity) ReleaseUndispatched(ctx context.Context, tx *sql.Tx, input CharityRelease) error {
	c.mu.Lock()
	failure := c.releaseErr
	c.mu.Unlock()
	if failure != nil {
		return failure
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO claim_test_charity_facts(kind,source_id)
VALUES('release',?)`, input.ClaimID); err != nil {
		return err
	}
	c.mu.Lock()
	c.releases = append(c.releases, input)
	c.mu.Unlock()
	return nil
}

func (c *testCharity) PrepareAttempt(
	_ context.Context,
	_ *sql.Tx,
	input CharityAttemptInput,
) (CharityActual, error) {
	if input.DonationKeyID == nil {
		return CharityActual{}, errors.New("test donation key disappeared")
	}
	c.mu.Lock()
	config, exists := c.configs[*input.DonationKeyID]
	c.mu.Unlock()
	if !exists {
		return CharityActual{}, errors.New("test donation key was not configured")
	}
	reward := config.rewardMilli
	if input.SuppressReward {
		reward = 0
	}
	return CharityActual{PriceMilli: config.priceMilli, RewardMilli: reward}, nil
}

func (c *testCharity) CompleteAttempt(
	ctx context.Context,
	tx *sql.Tx,
	completion CharityAttemptCompletion,
) error {
	c.mu.Lock()
	failure := c.completeAttemptErr
	c.mu.Unlock()
	if failure != nil {
		return failure
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO claim_test_charity_facts(kind,source_id)
VALUES('attempt',?)`, completion.Attempt.ClaimID); err != nil {
		return err
	}
	c.mu.Lock()
	c.attempts = append(c.attempts, completion.Attempt)
	c.mu.Unlock()
	return nil
}

func (c *testCharity) CompleteRequest(
	ctx context.Context,
	tx *sql.Tx,
	input CharityRequestCompletion,
) error {
	c.mu.Lock()
	failure := c.completeRequestErr
	c.mu.Unlock()
	if failure != nil {
		return failure
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO claim_test_charity_facts(kind,source_id)
VALUES('request',?)`, input.RequestID); err != nil {
		return err
	}
	c.mu.Lock()
	c.completions = append(c.completions, input)
	c.mu.Unlock()
	return nil
}

type claimFixture struct {
	t          *testing.T
	store      *db.Store
	db         *sql.DB
	codec      *testCodec
	accounting *testAccounting
	charity    *testCharity
	service    *Service
	clock      atomic.Int64
	nextKey    int64
}

type testKey struct {
	endpointID int64
	keyID      int64
	secretID   int64
	candidate  Candidate
}

func newClaimFixture(t *testing.T) *claimFixture {
	t.Helper()
	codec := newTestCodec()
	store, err := db.Open(filepath.Join(t.TempDir(), "claim.sqlite"), codec)
	if err != nil {
		t.Fatalf("open Generation 2 fixture: %v", err)
	}
	if _, err := store.DB().Exec(`CREATE TEMP TABLE claim_test_ledger_rows(
kind TEXT NOT NULL,source_id TEXT NOT NULL,PRIMARY KEY(kind,source_id)) WITHOUT ROWID`); err != nil {
		t.Fatalf("create test ledger evidence: %v", err)
	}
	if _, err := store.DB().Exec(`CREATE TEMP TABLE claim_test_charity_facts(
kind TEXT NOT NULL,source_id TEXT NOT NULL,PRIMARY KEY(kind,source_id)) WITHOUT ROWID`); err != nil {
		t.Fatalf("create test charity evidence: %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("close Generation 2 fixture: %v", err)
		}
	})
	fixture := &claimFixture{
		t:          t,
		store:      store,
		db:         store.DB(),
		codec:      codec,
		accounting: &testAccounting{},
		charity:    newTestCharity(),
	}
	fixture.clock.Store(1_000)
	service, err := New(Dependencies{
		DB:         fixture.db,
		Secrets:    codec,
		Accounting: fixture.accounting,
		Charity:    fixture.charity,
		Now: func() time.Time {
			return time.Unix(fixture.clock.Load(), 0)
		},
	})
	if err != nil {
		t.Fatalf("new claim service: %v", err)
	}
	fixture.service = service
	return fixture
}

func (f *claimFixture) seedUser(label string, admin bool) int64 {
	f.t.Helper()
	zero := make([]byte, 16)
	result, err := f.db.Exec(`INSERT INTO users(
discord_id,username,is_admin,donation_credit_mag,total_requests,total_uncached_input_tokens,
total_cache_write_input_tokens,total_cache_read_input_tokens,total_output_tokens,
total_unknown_usage_requests,revision,created_at,updated_at)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`, "fixture-"+label, label, boolInt(admin), zero, zero, zero,
		zero, zero, zero, zero, zero, f.clock.Load(), f.clock.Load())
	if err != nil {
		f.t.Fatalf("seed user %q: %v", label, err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		f.t.Fatalf("read user id: %v", err)
	}
	return id
}

func (f *claimFixture) seedKey(ownerUserID int64, label string) testKey {
	f.t.Helper()
	f.nextKey++
	baseURL := "https://" + label + ".example.test/v1"
	result, err := f.db.Exec(`INSERT INTO endpoints(
user_id,connector_type,base_url,note,enabled,revision,created_at,updated_at)
VALUES(?,'openai-compatible',?,'',1,1,?,?)`, ownerUserID, baseURL, f.clock.Load(), f.clock.Load())
	if err != nil {
		f.t.Fatalf("seed endpoint: %v", err)
	}
	endpointID, err := result.LastInsertId()
	if err != nil {
		f.t.Fatalf("read endpoint id: %v", err)
	}
	contextID := make([]byte, 16)
	contextID[8] = byte(f.nextKey >> 8)
	contextID[9] = byte(f.nextKey)
	envelope := "fixture-envelope-" + strconv.FormatInt(f.nextKey, 10)
	f.codec.add(envelope, []byte("fixture-credential-"+label))
	result, err = f.db.Exec(`INSERT INTO endpoint_key_secrets(
context_id,canonical_base_url,connector_type,encrypted_secret,created_at)
VALUES(?,?,'openai-compatible',?,?)`, contextID, baseURL, envelope, f.clock.Load())
	if err != nil {
		f.t.Fatalf("seed endpoint secret: %v", err)
	}
	secretID, err := result.LastInsertId()
	if err != nil {
		f.t.Fatalf("read endpoint secret id: %v", err)
	}
	fingerprint := make([]byte, 32)
	fingerprint[0] = byte(f.nextKey)
	result, err = f.db.Exec(`INSERT INTO endpoint_keys(
endpoint_id,secret_ref_id,secret_fingerprint,display_head,display_tail,note,
enabled,force_store_false,revision,created_at,updated_at)
VALUES(?,?,?,'head','tail','',1,0,1,?,?)`, endpointID, secretID, fingerprint,
		f.clock.Load(), f.clock.Load())
	if err != nil {
		f.t.Fatalf("seed endpoint key: %v", err)
	}
	keyID, err := result.LastInsertId()
	if err != nil {
		f.t.Fatalf("read endpoint key id: %v", err)
	}
	return testKey{
		endpointID: endpointID,
		keyID:      keyID,
		secretID:   secretID,
		candidate: Candidate{
			EndpointID:       endpointID,
			EndpointKeyID:    keyID,
			ConnectorType:    connectorcontract.TypeOpenAICompatible,
			CanonicalBaseURL: baseURL,
			UpstreamModelID:  "provider/" + label,
		},
	}
}

func (f *claimFixture) seedDonationKey(ownerUserID int64, key testKey, label string, reward int64) int64 {
	f.t.Helper()
	result, err := f.db.Exec(`INSERT INTO donations(
user_id,status,revision,description,review_note,reviewed_by_role,expires_at,reviewed_at,
created_at,updated_at)
VALUES(?,'approved',1,'','','admin',?,?,?,?)`, ownerUserID, f.clock.Load()+86_400,
		f.clock.Load(), f.clock.Load(), f.clock.Load())
	if err != nil {
		f.t.Fatalf("seed donation %q: %v", label, err)
	}
	donationID, err := result.LastInsertId()
	if err != nil {
		f.t.Fatalf("read donation id: %v", err)
	}
	zero := make([]byte, 16)
	one := make([]byte, 16)
	one[15] = 1
	result, err = f.db.Exec(`INSERT INTO donation_keys(
donation_id,endpoint_key_id,display_head,display_tail,canonical_base_url,connector_type,
price_used_mag,price_reserved_mag,calls_used,calls_reserved,tokens_used,tokens_reserved,
failure_streak,streak_generation,next_claim_seq,next_fold_seq,created_at,updated_at)
VALUES(?,?,'head','tail',?,'openai-compatible',?,?,?,?,?,?,?,?,?,?,?,?)`,
		donationID, key.keyID, key.candidate.CanonicalBaseURL,
		zero, zero, zero, zero, zero, zero, zero, one, one, one, f.clock.Load(), f.clock.Load())
	if err != nil {
		f.t.Fatalf("seed donation key %q: %v", label, err)
	}
	donationKeyID, err := result.LastInsertId()
	if err != nil {
		f.t.Fatalf("read donation key id: %v", err)
	}
	f.charity.mu.Lock()
	f.charity.configs[donationKeyID] = testCharityConfig{
		receiverUserID: ownerUserID,
		rewardMilli:    reward,
		priceMilli:     7,
	}
	f.charity.mu.Unlock()
	return donationKeyID
}

func (f *claimFixture) seedSuspension(keyID int64) {
	f.t.Helper()
	caseID, err := db.GenerateOpaqueID("rpc_")
	if err != nil {
		f.t.Fatalf("generate report case id: %v", err)
	}
	fingerprint := make([]byte, 32)
	fingerprint[0] = 1
	_, err = f.db.Exec(`INSERT INTO report_cases(
id,fingerprint,connector_type,canonical_base_url,status,progress_state,material_version,
target_version,deadline,material_count,target_count,distinct_owner_count,created_at)
VALUES(?,?,'openai-compatible','https://reported.example.test/v1','pending_review','complete',
1,1,?,0,0,0,?)`, caseID, fingerprint, f.clock.Load()+1_000, f.clock.Load())
	if err != nil {
		f.t.Fatalf("seed report case: %v", err)
	}
	if _, err := f.db.Exec(`INSERT INTO endpoint_key_suspensions(
endpoint_key_id,reason_type,report_case_id,created_at)
VALUES(?,'report_case',?,?)`, keyID, caseID, f.clock.Load()); err != nil {
		f.t.Fatalf("seed endpoint key suspension: %v", err)
	}
}

func (f *claimFixture) acceptSelf(userID int64, attemptLimit int) Request {
	f.t.Helper()
	request, err := f.service.Accept(context.Background(), AcceptInput{
		UserID:        userID,
		Route:         RouteOpenAIChat,
		ModelSnapshot: "public/model",
		AttemptLimit:  attemptLimit,
		ReservedMilli: 100,
	})
	if err != nil {
		f.t.Fatalf("accept self request: %v", err)
	}
	return request
}

func (f *claimFixture) acceptCharity(userID int64, attemptLimit int) Request {
	f.t.Helper()
	request, err := f.service.Accept(context.Background(), AcceptInput{
		UserID:         userID,
		Route:          RouteCharityChat,
		ModelSnapshot:  "[公益]provider/model",
		AttemptLimit:   attemptLimit,
		ReservedMilli:  200,
		CharityModelID: 1,
	})
	if err != nil {
		f.t.Fatalf("accept charity request: %v", err)
	}
	return request
}

func (f *claimFixture) requireCapacity(requestID string, requestRows, globalRows uint64, ledgerRows int64) {
	f.t.Helper()
	var requestRaw, globalRaw []byte
	var last, evidence int64
	if err := f.db.QueryRow(`SELECT ledger_rows_remaining FROM logical_requests WHERE id=?`, requestID).Scan(&requestRaw); err != nil {
		f.t.Fatalf("read request capacity: %v", err)
	}
	if err := f.db.QueryRow(`SELECT last_ledger_seq,reserved_future_rows FROM credit_capacity WHERE id=1`).Scan(&last, &globalRaw); err != nil {
		f.t.Fatalf("read global capacity: %v", err)
	}
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM claim_test_ledger_rows`).Scan(&evidence); err != nil {
		f.t.Fatalf("read test ledger evidence: %v", err)
	}
	requestValue, requestOK := decodeSmallU128(requestRaw)
	globalValue, globalOK := decodeSmallU128(globalRaw)
	if !requestOK || !globalOK || requestValue != requestRows || globalValue != globalRows ||
		last != ledgerRows || evidence != ledgerRows {
		f.t.Fatalf("capacity request=%d/%t global=%d/%t ledger=%d evidence=%d; want %d %d %d",
			requestValue, requestOK, globalValue, globalOK, last, evidence,
			requestRows, globalRows, ledgerRows)
	}
}

func (f *claimFixture) charityFactCount(kind, sourceID string) int {
	f.t.Helper()
	var count int
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM claim_test_charity_facts
WHERE kind=? AND source_id=?`, kind, sourceID).Scan(&count); err != nil {
		f.t.Fatalf("count test charity facts: %v", err)
	}
	return count
}

func (f *claimFixture) requireCallerMirror(requestID string, caller CallerResult, completedAt int64) {
	f.t.Helper()
	var class, callerError sql.NullString
	var callerStatus, completed sql.NullInt64
	var legacyStatus int
	var legacyError string
	if err := f.db.QueryRow(`SELECT caller_result_class,caller_status,caller_error_code,
status_code,error_code,completed_at FROM request_logs WHERE logical_request_id=?`, requestID).Scan(
		&class, &callerStatus, &callerError, &legacyStatus, &legacyError, &completed); err != nil {
		f.t.Fatalf("read request log caller mirror: %v", err)
	}
	statusMatches := caller.Status == 0 && !callerStatus.Valid ||
		caller.Status != 0 && callerStatus.Valid && callerStatus.Int64 == int64(caller.Status)
	errorMatches := caller.ErrorCode == "" && !callerError.Valid ||
		caller.ErrorCode != "" && callerError.Valid && callerError.String == caller.ErrorCode
	if !class.Valid || class.String != string(caller.Class) || !statusMatches || !errorMatches ||
		legacyStatus != caller.Status || legacyError != caller.ErrorCode ||
		!completed.Valid || completed.Int64 != completedAt {
		f.t.Fatalf("request log mirror class=%+v status=%+v error=%+v legacy=%d/%q completed=%+v; want %+v at %d",
			class, callerStatus, callerError, legacyStatus, legacyError, completed, caller, completedAt)
	}
}

func (f *claimFixture) requireCallerMirrorOpen(requestID string) {
	f.t.Helper()
	var class, status, callerError, completed any
	var legacyStatus int
	var legacyError string
	if err := f.db.QueryRow(`SELECT caller_result_class,caller_status,caller_error_code,
status_code,error_code,completed_at FROM request_logs WHERE logical_request_id=?`, requestID).Scan(
		&class, &status, &callerError, &legacyStatus, &legacyError, &completed); err != nil {
		f.t.Fatalf("read open request log caller mirror: %v", err)
	}
	if class != nil || status != nil || callerError != nil || completed != nil || legacyStatus != 0 || legacyError != "" {
		f.t.Fatalf("open request log had terminal mirror class=%v status=%v error=%v legacy=%d/%q completed=%v",
			class, status, callerError, legacyStatus, legacyError, completed)
	}
}

func requireErrorIs(t *testing.T, err, target error) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Fatalf("error = %v, want %v", err, target)
	}
}
