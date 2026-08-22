package charityrouting

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/connector/openai"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/dbtest"
	"github.com/waiting-here/NonbiriAPI/internal/forward"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

// fakeRunner is the programmable single-attempt charity dispatch boundary.
// Each RunCharity call consumes one preloaded response in order. Writing a
// non-empty body triggers the dispatchWriter's reserved→dispatched CAS before
// the result is returned, mirroring the real adapter's commit boundary.
type fakeRunner struct {
	mu        sync.Mutex
	responses []fakeResponse
	calls     int
}

type fakeResponse struct {
	body     []byte // body bytes written before returning (triggers dispatch CAS)
	result   openai.AttemptResult
	blockCtx bool // block until ctx is canceled (cancellation test)
}

func (f *fakeRunner) RunCharity(ctx context.Context, writer http.ResponseWriter, _ forward.CharityAttemptInput) openai.AttemptResult {
	f.mu.Lock()
	idx := f.calls % len(f.responses)
	resp := f.responses[idx]
	f.calls++
	f.mu.Unlock()
	if resp.blockCtx {
		<-ctx.Done()
		return openai.AttemptResult{Failure: openai.FailureCanceled, Diagnostic: "request canceled"}
	}
	if len(resp.body) > 0 {
		// Mirror the real adapter: the commit boundary is the number of body
		// bytes actually delegated to the client (n > 0). A zero-byte sink
		// failure stays pre-commit, exactly like the OpenAI connector.
		n, _ := writer.Write(resp.body)
		resp.result.Committed = n > 0
	}
	return resp.result
}

func (f *fakeRunner) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func testSafetyFactory(t *testing.T) *forward.SafetyIdentifierFactory {
	t.Helper()
	key := bytes.Repeat([]byte{0x5c}, 32)
	f, err := forward.NewSafetyIdentifierFactory(key)
	clear(key)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.Close() })
	return f
}

func openCharityTestStore(t *testing.T) *db.Store {
	t.Helper()
	master := bytes.Repeat([]byte{0x6a}, secret.MasterKeyBytes)
	vault, err := secret.New(master)
	clear(master)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = vault.Close() })
	dbPath := filepath.Join(t.TempDir(), "charity-svc.db")
	dbtest.EnsureOwnerOnlyParent(t, dbPath)
	store, err := db.Open(dbPath, vault)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func setConfig(t *testing.T, store *db.Store, key, value string) {
	t.Helper()
	if _, err := store.DB().Exec(`INSERT INTO site_config (key, value, updated_at) VALUES (?, ?, 0)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, value); err != nil {
		t.Fatalf("set config %s: %v", key, err)
	}
}

func setCredits(t *testing.T, store *db.Store, userID, amount int64) {
	t.Helper()
	if _, err := store.DB().Exec(`UPDATE users SET credits=? WHERE id=?`, amount, userID); err != nil {
		t.Fatalf("set credits: %v", err)
	}
}

// seedServiceFixture builds one routable [公益] model backed by n donation keys
// against the same endpoint, returns the service and the consumer + candidate
// donation key ids. The runner is the programmable fakeRunner.
func seedServiceFixture(t *testing.T, store *db.Store, runner *fakeRunner, nKeys int, perRequest bool, consumerCredits int64) (*Service, int64, []int64) {
	t.Helper()
	ctx := context.Background()
	setConfig(t, store, "charity_enabled", "1")
	if perRequest {
		// no token reserve needed
	} else {
		setConfig(t, store, "charity_token_reserve_milli", "1000")
	}
	donor, _, err := store.FindOrCreateDiscordUser("discord-donor", "donor", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	ep, err := store.CreateEndpoint(ctx, donor.ID, "openai-compatible", "https://charity.example.com", "", true, 1)
	if err != nil {
		t.Fatal(err)
	}
	var keyIDs []int64
	for i := 0; i < nKeys; i++ {
		k, err := store.CreateEndpointKey(ctx, donor.ID, ep.ID, []byte("sk-"+string(rune('a'+i))), "h", "t", "", true, 1)
		if err != nil {
			t.Fatal(err)
		}
		keyIDs = append(keyIDs, k.ID)
		if err := store.ReplaceFetchedModels(ctx, donor.ID, ep.ID, k.ID, []db.FetchedModel{{UpstreamModelID: "up/charity", Provider: "donor"}}, 10); err != nil {
			t.Fatal(err)
		}
	}
	in := db.CreateDonationInput{
		UserID: donor.ID, Description: "test donation", Now: 40,
		Existing: &db.ExistingEndpointKeys{EndpointID: ep.ID, KeyIDs: keyIDs},
	}
	d, err := store.CreateDonation(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ApplyDonationReview(ctx, db.ReviewDecision{
		DonationID: d.ID, Role: db.ReviewRoleAdmin, ReviewerID: donor.ID, Action: db.ReviewActionApprove, Now: 60,
	}); err != nil {
		t.Fatal(err)
	}
	var donationKeyIDs []int64
	rows, _ := store.DB().Query(`SELECT id FROM donation_keys WHERE donation_id=? ORDER BY id`, d.ID)
	defer rows.Close()
	for rows.Next() {
		var id int64
		rows.Scan(&id)
		donationKeyIDs = append(donationKeyIDs, id)
	}
	mode := db.CharityPricingPerRequest
	if !perRequest {
		mode = db.CharityPricingPerToken
	}
	m := db.CharityModel{
		Provider: "donor", Model: "charity", Enabled: true, PricingMode: mode,
		RequestUserPrice: 500, RequestDonorReward: 100,
		UncachedUserPrice: 1_000_000, CacheWriteUserPrice: 1_000_000, CacheReadUserPrice: 1_000_000, OutputUserPrice: 1_000_000,
		UncachedDonorReward: 200_000, CacheWriteDonorReward: 200_000, CacheReadDonorReward: 200_000, OutputDonorReward: 200_000,
	}
	created, err := store.CreateCharityModel(ctx, m, 0, 70)
	if err != nil {
		t.Fatal(err)
	}
	for i, dkID := range donationKeyIDs {
		if _, err := store.CreateCharityBinding(ctx, created.ID, dkID, "up/charity", int64(i), 80); err != nil {
			t.Fatal(err)
		}
	}
	consumer, _, err := store.FindOrCreateDiscordUser("discord-consumer", "consumer", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	setCredits(t, store, consumer.ID, consumerCredits)
	svc, err := NewService(ServiceConfig{
		Store: store, Runner: runner, SafetyIdentifiers: testSafetyFactory(t),
		Now: func() time.Time { return time.Unix(1000, 0) },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.Close() })
	return svc, consumer.ID, donationKeyIDs
}

func TestServiceForwardSuccessCommitsAndRewards(t *testing.T) {
	store := openCharityTestStore(t)
	runner := &fakeRunner{responses: []fakeResponse{{
		body:   []byte("data: {\"hello\"}\n\n"),
		result: openai.AttemptResult{Success: true, Usage: openai.Usage{OutputTokens: 10, Present: true}},
	}}}
	svc, consumerID, _ := seedServiceFixture(t, store, runner, 1, true, 10_000)
	// Activity needs an explicit site timezone; without one the rollup is
	// force-disabled by design.
	setConfig(t, store, "site_timezone_offset_minutes", "0")

	rec := httptest.NewRecorder()
	req := &openai.ChatRequest{}
	req.Model = "[公益]donor/charity"
	result, err := svc.Forward(context.Background(), rec, consumerID, req)
	if err != nil {
		t.Fatalf("Forward: %v", err)
	}
	if !result.Success || !result.Committed {
		t.Fatalf("result = success=%v committed=%v", result.Success, result.Committed)
	}
	// The reservation reached committed with the donor reward.
	var stateStr string
	var donorReward int64
	store.DB().QueryRow(`SELECT state, donor_reward FROM charity_reservations LIMIT 1`).Scan(&stateStr, &donorReward)
	if stateStr != "committed" {
		t.Fatalf("state = %q, want committed", stateStr)
	}
	if donorReward != 100 {
		t.Fatalf("donor_reward = %d, want 100", donorReward)
	}
	// Outcome recorded as success.
	var successCount int
	store.DB().QueryRow(`SELECT success_count FROM charity_model_stats LIMIT 1`).Scan(&successCount)
	if successCount != 1 {
		t.Fatalf("success_count = %d, want 1", successCount)
	}
	// A protocol-terminating successful charity call is one product-activity
	// event, exactly like the personal exit (frozen §F).
	var apiRequests int64
	if err := store.DB().QueryRow(`SELECT api_requests FROM user_activity_daily`).Scan(&apiRequests); err != nil {
		t.Fatalf("activity row missing after successful charity call: %v", err)
	}
	if apiRequests != 1 {
		t.Fatalf("api_requests = %d, want 1", apiRequests)
	}
}

func TestServiceForwardPreDispatchFailureAllKeys(t *testing.T) {
	store := openCharityTestStore(t)
	runner := &fakeRunner{responses: []fakeResponse{
		{result: openai.AttemptResult{Failure: openai.FailureUpstream, Diagnostic: "upstream down"}},
		{result: openai.AttemptResult{Failure: openai.FailureUpstream, Diagnostic: "upstream down"}},
	}}
	svc, consumerID, _ := seedServiceFixture(t, store, runner, 2, true, 10_000)

	rec := httptest.NewRecorder()
	req := &openai.ChatRequest{Model: "[公益]donor/charity"}
	result, err := svc.Forward(context.Background(), rec, consumerID, req)
	if err != nil {
		t.Fatalf("Forward: %v", err)
	}
	if result.Success || result.Committed {
		t.Fatalf("result should be a pre-dispatch failure")
	}
	if result.Failure != openai.FailureUpstream {
		t.Fatalf("failure = %v, want upstream", result.Failure)
	}
	// All candidates failed pre-dispatch: the reservation was released (refund).
	var state string
	store.DB().QueryRow(`SELECT state FROM charity_reservations LIMIT 1`).Scan(&state)
	if state != "released" {
		t.Fatalf("state = %q, want released (pre-dispatch all failed)", state)
	}
	var credits int64
	store.DB().QueryRow(`SELECT credits FROM users WHERE id=?`, consumerID).Scan(&credits)
	if credits != 10_000 {
		t.Fatalf("consumer credits = %d, want refunded to %d", credits, 10_000)
	}
}

func TestServiceForwardAdmissionExhaustedReturns429(t *testing.T) {
	store := openCharityTestStore(t)
	runner := &fakeRunner{responses: []fakeResponse{{result: openai.AttemptResult{Failure: openai.FailureUpstream}}}}
	svc, consumerID, keyIDs := seedServiceFixture(t, store, runner, 1, true, 10_000)
	// Cap the only key below the per-request reserve (500): admission refuses.
	if _, err := store.DB().Exec(`UPDATE donation_keys SET credits_usage_cap=100 WHERE id=?`, keyIDs[0]); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	req := &openai.ChatRequest{Model: "[公益]donor/charity"}
	_, err := svc.Forward(context.Background(), rec, consumerID, req)
	if !errors.Is(err, ErrKeysExhausted) {
		t.Fatalf("Forward = %v, want ErrKeysExhausted", err)
	}
	// No reservation was created and no runner call was made.
	var n int64
	store.DB().QueryRow(`SELECT COUNT(*) FROM charity_reservations`).Scan(&n)
	if n != 0 {
		t.Fatalf("reservations = %d, want 0", n)
	}
	if runner.callCount() != 0 {
		t.Fatalf("runner calls = %d, want 0 (admission refused before dispatch)", runner.callCount())
	}
}

func TestServiceForwardInsufficientCredits(t *testing.T) {
	store := openCharityTestStore(t)
	runner := &fakeRunner{responses: []fakeResponse{{result: openai.AttemptResult{Success: true}}}}
	svc, consumerID, _ := seedServiceFixture(t, store, runner, 1, true, 100) // 100 < 500 reserve
	rec := httptest.NewRecorder()
	req := &openai.ChatRequest{Model: "[公益]donor/charity"}
	_, err := svc.Forward(context.Background(), rec, consumerID, req)
	if !errors.Is(err, db.ErrInsufficientCredits) {
		t.Fatalf("Forward = %v, want db.ErrInsufficientCredits", err)
	}
}

func TestServiceForwardDisabledRefuses(t *testing.T) {
	store := openCharityTestStore(t)
	runner := &fakeRunner{responses: []fakeResponse{{result: openai.AttemptResult{Success: true}}}}
	svc, consumerID, _ := seedServiceFixture(t, store, runner, 1, true, 10_000)
	setConfig(t, store, "charity_enabled", "0")
	rec := httptest.NewRecorder()
	req := &openai.ChatRequest{Model: "[公益]donor/charity"}
	_, err := svc.Forward(context.Background(), rec, consumerID, req)
	if !errors.Is(err, ErrCharityDisabled) {
		t.Fatalf("Forward = %v, want ErrCharityDisabled", err)
	}
}

func TestServiceForwardSwapAcrossKeysDebitsUserOnce(t *testing.T) {
	store := openCharityTestStore(t)
	// First key fails pre-dispatch; second key succeeds. The user is debited
	// exactly once (the swap moves only the key reserve).
	runner := &fakeRunner{responses: []fakeResponse{
		{result: openai.AttemptResult{Failure: openai.FailureUpstream, Diagnostic: "key1 down"}},
		{body: []byte("ok"), result: openai.AttemptResult{Success: true, Usage: openai.Usage{Present: true, OutputTokens: 1}}},
	}}
	svc, consumerID, _ := seedServiceFixture(t, store, runner, 2, true, 10_000)
	rec := httptest.NewRecorder()
	req := &openai.ChatRequest{Model: "[公益]donor/charity"}
	result, err := svc.Forward(context.Background(), rec, consumerID, req)
	if err != nil {
		t.Fatalf("Forward: %v", err)
	}
	if !result.Success || !result.Committed {
		t.Fatalf("result should be a committed success")
	}
	if runner.callCount() != 2 {
		t.Fatalf("runner calls = %d, want 2 (retry across keys)", runner.callCount())
	}
	// Exactly one reservation row, committed; user debited once (500).
	var n int64
	store.DB().QueryRow(`SELECT COUNT(*) FROM charity_reservations`).Scan(&n)
	if n != 1 {
		t.Fatalf("reservations = %d, want 1", n)
	}
	var state string
	store.DB().QueryRow(`SELECT state FROM charity_reservations`).Scan(&state)
	if state != "committed" {
		t.Fatalf("state = %q, want committed", state)
	}
	var credits int64
	store.DB().QueryRow(`SELECT credits FROM users WHERE id=?`, consumerID).Scan(&credits)
	if credits != 10_000-500 {
		t.Fatalf("consumer credits = %d, want %d (debited once)", credits, 10_000-500)
	}
}

func TestServiceForwardCommittedFailureBadEOFSettles(t *testing.T) {
	store := openCharityTestStore(t)
	// Bytes crossed the dispatch boundary, then the upstream failed (bad EOF
	// / mid-stream error). The contract commits the actual charge; with no
	// valid usage it commits under unknown semantics.
	runner := &fakeRunner{responses: []fakeResponse{
		{body: []byte("data: partial"), result: openai.AttemptResult{Failure: openai.FailureUpstream, Diagnostic: "bad eof"}},
	}}
	svc, consumerID, _ := seedServiceFixture(t, store, runner, 1, true, 10_000)
	rec := httptest.NewRecorder()
	req := &openai.ChatRequest{Model: "[公益]donor/charity"}
	result, err := svc.Forward(context.Background(), rec, consumerID, req)
	if err != nil {
		t.Fatalf("Forward: %v", err)
	}
	if result.Committed != true {
		t.Fatalf("result should be committed (bytes crossed)")
	}
	if result.Success {
		t.Fatalf("result should not be success (bad EOF)")
	}
	var state string
	var unknown int
	store.DB().QueryRow(`SELECT state, usage_unknown FROM charity_reservations`).Scan(&state, &unknown)
	if state != "committed" || unknown != 1 {
		t.Fatalf("state=%q unknown=%d, want committed/unknown", state, unknown)
	}
	// Outcome recorded as failure (no protocol-terminating success).
	var successCount int
	store.DB().QueryRow(`SELECT success_count FROM charity_model_stats LIMIT 1`).Scan(&successCount)
	if successCount != 0 {
		t.Fatalf("success_count = %d, want 0 (bad EOF is not success)", successCount)
	}
	// A committed-but-failed call never fabricates a product-activity event.
	setConfig(t, store, "site_timezone_offset_minutes", "0")
	var activityRows int
	store.DB().QueryRow(`SELECT COUNT(*) FROM user_activity_daily`).Scan(&activityRows)
	if activityRows != 0 {
		t.Fatalf("activity rows = %d, want 0 (failed call is not API activity)", activityRows)
	}
}

func TestServiceForwardClientCancelBeforeDispatch(t *testing.T) {
	store := openCharityTestStore(t)
	runner := &fakeRunner{responses: []fakeResponse{{blockCtx: true}}}
	svc, consumerID, _ := seedServiceFixture(t, store, runner, 1, true, 10_000)
	ctx, cancel := context.WithCancel(context.Background())
	rec := httptest.NewRecorder()
	req := &openai.ChatRequest{Model: "[公益]donor/charity"}
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	_, err := svc.Forward(ctx, rec, consumerID, req)
	if err != nil {
		// A canceled pre-dispatch returns a result (not an error sentinel).
	}
	var state string
	store.DB().QueryRow(`SELECT state FROM charity_reservations`).Scan(&state)
	if state != "released" {
		t.Fatalf("state = %q, want released (client cancel before dispatch)", state)
	}
}

func TestServiceForwardNamespaceIsolation(t *testing.T) {
	store := openCharityTestStore(t)
	runner := &fakeRunner{responses: []fakeResponse{{result: openai.AttemptResult{Success: true}}}}
	svc, consumerID, _ := seedServiceFixture(t, store, runner, 1, true, 10_000)
	rec := httptest.NewRecorder()
	req := &openai.ChatRequest{Model: "personal/model"} // no [公益] prefix
	_, err := svc.Forward(context.Background(), rec, consumerID, req)
	if !errors.Is(err, ErrModelNotFound) {
		t.Fatalf("personal model = %v, want ErrModelNotFound", err)
	}
}

func TestServiceRecoverAllConvergesStalled(t *testing.T) {
	store := openCharityTestStore(t)
	runner := &fakeRunner{responses: []fakeResponse{{blockCtx: true}}}
	svc, consumerID, keyIDs := seedServiceFixture(t, store, runner, 1, true, 10_000)
	// Create a reserved reservation directly, then recover: it should release.
	cand, err := store.ResolveCharityRoute(context.Background(), "[公益]donor/charity", 1000, 16)
	if err != nil {
		t.Fatal(err)
	}
	c := cand.Candidates[0]
	res, _, err := store.CreateCharityReservation(context.Background(), db.ReserveCharityInput{
		UserID: consumerID, FullName: "[公益]donor/charity",
		BindingID: c.BindingID, DonationKeyID: c.DonationKeyID,
		AttemptID: "recover-1", BaseURL: c.BaseURL, Now: 1000,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Dispatch one, then recover: committed unknown.
	res2, _, _ := store.CreateCharityReservation(context.Background(), db.ReserveCharityInput{
		UserID: consumerID, FullName: "[公益]donor/charity",
		BindingID: c.BindingID, DonationKeyID: c.DonationKeyID,
		AttemptID: "recover-2", BaseURL: c.BaseURL, Now: 1001,
	})
	store.DispatchCharityReservation(context.Background(), res2.ID, 1000)

	svc.RecoverAll(context.Background(), true)
	r1, _ := store.GetCharityReservation(context.Background(), res.ID)
	r2, _ := store.GetCharityReservation(context.Background(), res2.ID)
	if r1.State != "released" {
		t.Fatalf("recover reserved → %q, want released", r1.State)
	}
	if r2.State != "committed" || !r2.UsageUnknown {
		t.Fatalf("recover dispatched → %q unknown=%v, want committed/unknown", r2.State, r2.UsageUnknown)
	}
	// The released row refunded its 500; the dispatched row committed unknown
	// (user keeps paying its 500 reserve). The key reserve is fully freed.
	var credits int64
	store.DB().QueryRow(`SELECT credits FROM users WHERE id=?`, consumerID).Scan(&credits)
	if credits != 9500 {
		t.Fatalf("credits after recover = %d, want 9500", credits)
	}
	var reserved int64
	store.DB().QueryRow(`SELECT credits_reserved FROM donation_keys WHERE id=?`, keyIDs[0]).Scan(&reserved)
	if reserved != 0 {
		t.Fatalf("key reserved after recover = %d, want 0", reserved)
	}
}

func TestServiceCancelUserContextsCancelsInFlight(t *testing.T) {
	store := openCharityTestStore(t)
	runner := &fakeRunner{responses: []fakeResponse{{blockCtx: true}}}
	svc, consumerID, _ := seedServiceFixture(t, store, runner, 1, true, 10_000)

	// Start an in-flight call that blocks on its context, then cancel the
	// user's contexts (the lifecycle pre-delete hook). The in-flight runner
	// must observe cancellation.
	done := make(chan openai.AttemptResult, 1)
	rec := httptest.NewRecorder()
	req := &openai.ChatRequest{Model: "[公益]donor/charity"}
	go func() {
		result, _ := svc.Forward(context.Background(), rec, consumerID, req)
		done <- result
	}()
	// Give the call time to register and block.
	time.Sleep(50 * time.Millisecond)
	svc.CancelUserContexts(consumerID)
	select {
	case result := <-done:
		if result.Failure != openai.FailureCanceled {
			t.Fatalf("result failure = %v, want canceled", result.Failure)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("in-flight call was not canceled by CancelUserContexts")
	}
	// The canceled pre-dispatch reservation was released (refund).
	var state string
	store.DB().QueryRow(`SELECT state FROM charity_reservations LIMIT 1`).Scan(&state)
	if state != "released" {
		t.Fatalf("state = %q, want released after cancel", state)
	}
}

// zeroByteResponseWriter simulates a client sink that delivers ZERO body bytes
// on the first non-empty write (an HTTP/2 stream reset, an early client
// disconnect, or a write-deadline/pressure failure) and then errors on every
// further write. It is the SEC-A2-01 regression vector: the dispatch CAS runs
// before the delegate, so without compensation the row would be stuck
// `dispatched` while the adapter reports a pre-commit failure.
var errZeroByteSink = errors.New("test: zero-byte sink failure")

type zeroByteResponseWriter struct {
	hdr http.Header
}

func (w *zeroByteResponseWriter) Header() http.Header {
	if w.hdr == nil {
		w.hdr = http.Header{}
	}
	return w.hdr
}

func (w *zeroByteResponseWriter) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	return 0, errZeroByteSink
}

func (w *zeroByteResponseWriter) WriteHeader(int) {}

// partialResponseWriter delivers a strict prefix of every non-empty write.
// It models a client sink that accepted some body bytes before returning a
// short-write error; any delivered byte must keep the reservation dispatched.
type partialResponseWriter struct {
	hdr       http.Header
	delivered int
	maxBytes  int
}

func (w *partialResponseWriter) Header() http.Header {
	if w.hdr == nil {
		w.hdr = make(http.Header)
	}
	return w.hdr
}

func (w *partialResponseWriter) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	n := w.maxBytes
	if n <= 0 || n >= len(p) {
		n = len(p) - 1
	}
	if n == 0 {
		n = 1
	}
	w.delivered += n
	return n, errors.New("test: partial write")
}

func (w *partialResponseWriter) WriteHeader(int) {}

// TestServiceForwardZeroByteSinkReleasesNotCharges: the first real body write
// delivers zero bytes (sink failure). The frozen dispatch boundary ("first
// body byte successfully submitted") was NOT crossed, so the reservation must
// be released: the user is refunded, the donation-key cap is freed, and no
// donor reward or unknown-usage commit is recorded. This is the core
// SEC-A2-01 regression — previously the row stayed `dispatched` and recovery
// later committed it as unknown-usage, charging the user for nothing.
func TestServiceForwardZeroByteSinkReleasesNotCharges(t *testing.T) {
	store := openCharityTestStore(t)
	runner := &fakeRunner{responses: []fakeResponse{{
		body:   []byte("data: {\"hello\"}\n\n"),
		result: openai.AttemptResult{Success: true, Usage: openai.Usage{OutputTokens: 10, Present: true}},
	}}}
	svc, consumerID, keyIDs := seedServiceFixture(t, store, runner, 1, true, 10_000)

	rec := &zeroByteResponseWriter{}
	req := &openai.ChatRequest{Model: "[公益]donor/charity"}
	result, err := svc.Forward(context.Background(), rec, consumerID, req)
	if err != nil {
		t.Fatalf("Forward: %v", err)
	}
	if result.Committed {
		t.Fatalf("result.Committed = true, want false (zero body bytes delivered)")
	}
	// The reservation was compensated back to `reserved` and then released.
	var state string
	store.DB().QueryRow(`SELECT state FROM charity_reservations LIMIT 1`).Scan(&state)
	if state != "released" {
		t.Fatalf("state = %q, want released (zero-byte sink must not charge)", state)
	}
	var credits int64
	store.DB().QueryRow(`SELECT credits FROM users WHERE id=?`, consumerID).Scan(&credits)
	if credits != 10_000 {
		t.Fatalf("consumer credits = %d, want refunded to 10_000", credits)
	}
	var reserved int64
	store.DB().QueryRow(`SELECT credits_reserved FROM donation_keys WHERE id=?`, keyIDs[0]).Scan(&reserved)
	if reserved != 0 {
		t.Fatalf("key credits_reserved = %d, want 0 (cap freed)", reserved)
	}
	var n int64
	store.DB().QueryRow(`SELECT COUNT(*) FROM charity_reservations WHERE state='committed'`).Scan(&n)
	if n != 0 {
		t.Fatalf("committed rows = %d, want 0", n)
	}
}

// TestServiceForwardZeroByteThenRetrySwapsAndReleases: a zero-byte sink
// failure on the first donated key is compensated back to `reserved`, so the
// swap to the next candidate succeeds (previously the stuck `dispatched` row
// made the swap fail with an internal illegal-transition error). The same
// broken client sink fails the second candidate too, so the reservation is
// ultimately released and the user refunded. The regression value is that
// the retry loop runs both candidates (runner called twice) and ends in a
// clean release instead of an internal error.
func TestServiceForwardZeroByteThenRetrySwapsAndReleases(t *testing.T) {
	store := openCharityTestStore(t)
	runner := &fakeRunner{responses: []fakeResponse{
		{body: []byte("data: partial"), result: openai.AttemptResult{Success: true, Usage: openai.Usage{Present: true, OutputTokens: 1}}}, // key1: zero-byte sink failure
		{body: []byte("ok"), result: openai.AttemptResult{Success: true, Usage: openai.Usage{Present: true, OutputTokens: 1}}},            // key2: zero-byte sink failure (same broken client)
	}}
	svc, consumerID, _ := seedServiceFixture(t, store, runner, 2, true, 10_000)

	rec := &zeroByteResponseWriter{}
	req := &openai.ChatRequest{Model: "[公益]donor/charity"}
	result, err := svc.Forward(context.Background(), rec, consumerID, req)
	if err != nil {
		t.Fatalf("Forward: %v, want nil (compensation must not surface an internal error)", err)
	}
	if result.Committed {
		t.Fatalf("result.Committed = true, want false (no bytes reached the broken client)")
	}
	if runner.callCount() != 2 {
		t.Fatalf("runner calls = %d, want 2 (zero-byte compensated, swap succeeded, retried)", runner.callCount())
	}
	var n int64
	store.DB().QueryRow(`SELECT COUNT(*) FROM charity_reservations`).Scan(&n)
	if n != 1 {
		t.Fatalf("reservations = %d, want 1", n)
	}
	var state string
	store.DB().QueryRow(`SELECT state FROM charity_reservations`).Scan(&state)
	if state != "released" {
		t.Fatalf("state = %q, want released (all candidates failed, refund)", state)
	}
	var credits int64
	store.DB().QueryRow(`SELECT credits FROM users WHERE id=?`, consumerID).Scan(&credits)
	if credits != 10_000 {
		t.Fatalf("consumer credits = %d, want refunded to 10_000", credits)
	}
}

// TestServiceForwardShortWriteStaysDispatched: a partial write (n > 0 but n <
// len) DOES cross the frozen dispatch boundary — bytes reached the client —
// so the row stays `dispatched` and is NOT compensated. The service settles it
// under the commit formula (unknown usage when the connector reports no valid
// usage), never releasing after the client already received data.
func TestServiceForwardShortWriteStaysDispatched(t *testing.T) {
	store := openCharityTestStore(t)
	runner := &fakeRunner{responses: []fakeResponse{{
		body:   []byte("data: partial"),
		result: openai.AttemptResult{Failure: openai.FailureUpstream, Diagnostic: "bad eof"},
	}}}
	svc, consumerID, _ := seedServiceFixture(t, store, runner, 1, true, 10_000)

	rec := &partialResponseWriter{maxBytes: 3}
	req := &openai.ChatRequest{Model: "[公益]donor/charity"}
	result, err := svc.Forward(context.Background(), rec, consumerID, req)
	if err != nil {
		t.Fatalf("Forward: %v", err)
	}
	if !result.Committed {
		t.Fatalf("result.Committed = false, want true (bytes were delivered)")
	}
	if rec.delivered <= 0 || rec.delivered >= len(runner.responses[0].body) {
		t.Fatalf("delivered bytes = %d, want 0 < n < %d", rec.delivered, len(runner.responses[0].body))
	}
	var state string
	var unknown int
	store.DB().QueryRow(`SELECT state, usage_unknown FROM charity_reservations`).Scan(&state, &unknown)
	if state != "committed" || unknown != 1 {
		t.Fatalf("state=%q unknown=%d, want committed/unknown (short write settles, never releases)", state, unknown)
	}
	var credits int64
	store.DB().QueryRow(`SELECT credits FROM users WHERE id=?`, consumerID).Scan(&credits)
	if credits != 10_000-500 {
		t.Fatalf("consumer credits = %d, want %d (charged the discounted reserve)", credits, 10_000-500)
	}
}
