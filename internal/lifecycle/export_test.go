package lifecycle

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/auth"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/dbtest"
	"github.com/waiting-here/NonbiriAPI/internal/host"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
	"github.com/waiting-here/NonbiriAPI/internal/ratelimit"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

// fakeElevation simulates the auth rail's consumed-capability boundary.
type fakeElevation struct {
	mu         sync.Mutex
	allowCount int // number of successful consumptions remaining
	consumed   int // total consume attempts
}

func (f *fakeElevation) ConsumeElevated(_ http.ResponseWriter, _ *http.Request, _ *db.User) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.consumed++
	if f.allowCount > 0 {
		f.allowCount--
		return nil
	}
	return auth.ErrElevationRequired
}

func (f *fakeElevation) ClearElevatedCookie(http.ResponseWriter, *http.Request) {}

func lifecycleTestStore(t *testing.T) *db.Store {
	t.Helper()
	key := bytes.Repeat([]byte{0x3c}, secret.MasterKeyBytes)
	vault, err := secret.New(key)
	clear(key)
	if err != nil {
		t.Fatalf("secret.New: %v", err)
	}
	dbPath := filepath.Join(t.TempDir(), "lifecycle.db")
	dbtest.EnsureOwnerOnlyParent(t, dbPath)
	st, err := db.Open(dbPath, vault)
	if err != nil {
		_ = vault.Close()
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() {
		_ = st.Close()
		_ = vault.Close()
	})
	return st
}

func newExportHandler(t *testing.T, st *db.Store, user *db.User, elevation Elevation, limiter *ratelimit.ProbeLimiter) http.Handler {
	t.Helper()
	if elevation == nil {
		elevation = &fakeElevation{allowCount: 1}
	}
	return NewHandler(HandlerDeps{
		Store: st,
		Resolve: func(*http.Request) (*db.User, error) {
			return user, nil
		},
		Elevation: elevation,
		Limiter:   limiter,
	})
}

func exportRequest(token string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "https://example.com/api/account/export", nil)
	r.RemoteAddr = "198.51.100.30:4000"
	r = r.WithContext(host.WithStation(r.Context(), host.StationUser))
	if token != "" {
		r.Header.Set("X-Elevated-Token", token)
	}
	return r
}

func seedExportFixture(t *testing.T, st *db.Store) *db.User {
	t.Helper()
	ctx := context.Background()
	user, err := st.CreateDiscordUser("discord-export", "alice", "")
	if err != nil {
		t.Fatal(err)
	}
	ep, err := st.CreateEndpoint(ctx, user.ID, "openai-compatible", "https://upstream.example/v1/", "my endpoint", true, 100)
	if err != nil {
		t.Fatal(err)
	}
	key, err := st.CreateEndpointKeyWithPolicy(ctx, user.ID, ep.ID, []byte("sk-lifecycle-export"), "head", "tail", "my key", true, true, 100, "owner")
	if err != nil {
		t.Fatal(err)
	}
	model, err := st.CreateModelWithPolicy(ctx, user.ID, "provider", "model", "ordered", false, true, 100, "owner")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.ReplaceFetchedModels(ctx, user.ID, ep.ID, key.ID, []db.FetchedModel{
		{UpstreamModelID: "upstream-model", Provider: "provider", Status: "ok"},
	}, 100); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateBinding(ctx, user.ID, model.ID, key.ID, "upstream-model", 0, 100); err != nil {
		t.Fatal(err)
	}
	if _, err := st.RegenerateCallerKey(user.ID); err != nil {
		t.Fatal(err)
	}
	usage, err := st.GetUserUsage(ctx, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if usage.TotalRequests != 0 {
		t.Fatalf("fixture usage=%#v", usage)
	}
	return user
}

func TestExportRequiresAuthenticatedUser(t *testing.T) {
	st := lifecycleTestStore(t)
	handler := NewHandler(HandlerDeps{
		Store:     st,
		Elevation: &fakeElevation{allowCount: 1},
	})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, exportRequest(""))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestExportRequiresElevatedCapability(t *testing.T) {
	st := lifecycleTestStore(t)
	user := seedExportFixture(t, st)
	handler := newExportHandler(t, st, user, &fakeElevation{allowCount: 0}, nil)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, exportRequest(""))
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), `"code":"elevated_required"`) {
		t.Fatalf("missing capability status=%d body=%s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("cache=%q", rec.Header().Get("Cache-Control"))
	}
	// A garbage token fails identically without echoing the token.
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, exportRequest("garbage-token"))
	if rec.Code != http.StatusForbidden || strings.Contains(rec.Body.String(), "garbage-token") {
		t.Fatalf("garbage token status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestExportRateLimitedPerUser(t *testing.T) {
	st := lifecycleTestStore(t)
	user := seedExportFixture(t, st)
	limiter, err := ratelimit.NewProbeLimiter(ratelimit.ProbeLimiterConfig{
		Window: time.Minute, DefaultLimit: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer limiter.Close()
	elevation := &fakeElevation{allowCount: 5}
	handler := newExportHandler(t, st, user, elevation, limiter)

	first := httptest.NewRecorder()
	handler.ServeHTTP(first, exportRequest("token-1"))
	if first.Code != http.StatusOK {
		t.Fatalf("first export status=%d body=%s", first.Code, first.Body.String())
	}
	second := httptest.NewRecorder()
	handler.ServeHTTP(second, exportRequest("token-2"))
	if second.Code != http.StatusTooManyRequests || !strings.Contains(second.Body.String(), `"code":"rate_limited"`) {
		t.Fatalf("second export status=%d body=%s", second.Code, second.Body.String())
	}
	if second.Header().Get("Retry-After") == "" {
		t.Fatalf("rate limited response lacks Retry-After")
	}
	elevation.mu.Lock()
	consumed := elevation.consumed
	elevation.mu.Unlock()
	if consumed != 1 {
		t.Fatalf("capability consumed=%d on rate-limited request, want 1 (capability must survive rate limits)", consumed)
	}
}

func TestExportPackageShapeWhitelistAndNoSecrets(t *testing.T) {
	st := lifecycleTestStore(t)
	user := seedExportFixture(t, st)
	// This test resolves the CURRENT row (not the seed-time snapshot) so the
	// level state set below is projected exactly as a live session would see
	// it.
	handler := NewHandler(HandlerDeps{
		Store: st,
		Resolve: func(*http.Request) (*db.User, error) {
			return st.GetUserByID(user.ID)
		},
		Elevation: &fakeElevation{allowCount: 1},
	})

	// The usage summary carries the four-bucket totals alongside the legacy
	// mirrors; record one known request so the projection is exercised. The
	// timezone offset is configured first so the same request also lands in
	// the user's own daily activity summary (schema v2 section).
	if err := st.SetSiteTimezoneOffsetMinutes(330); err != nil {
		t.Fatal(err)
	}
	if err := st.RecordRequest(context.Background(), db.RequestLogInput{
		AttemptID: "export-usage-attempt", UserID: user.ID, Model: "provider/model",
		StatusCode: 200, StartedAt: time.Unix(1700000000, 0).UTC(), CompletedAt: time.Unix(1700000001, 0).UTC(),
		UncachedInputTokens: 2, CacheWriteInputTokens: 3, CacheReadInputTokens: 5, OutputTokens: 7,
		Activity: &db.ActivityDelta{APIRequests: 1, UncachedInputTokens: 2, CacheWriteInputTokens: 3, CacheReadInputTokens: 5, OutputTokens: 7},
	}); err != nil {
		t.Fatal(err)
	}
	// Credit balances and ledger entries are part of the export (schema v2):
	// one adjustment gives the projection known string values.
	if _, err := st.ApplyAdminCreditAdjustment(context.Background(), db.AdminCreditAdjustment{
		TargetUserID: user.ID, ActorUserID: user.ID, OperationID: "export-ledger-1",
		Reason: "export fixture", CreditsSet: true, CreditsDelta: 12345, DonationSet: true, DonationDelta: 678,
	}); err != nil {
		t.Fatal(err)
	}
	// Level state exports as small integers: a manual override set just for
	// this assertion and the untouched automatic high-water mark. The handler
	// above resolves the current row, so no fixture refresh is needed.
	manualLevel := 2
	if _, err := st.SetUserManualLevel(user.ID, &manualLevel); err != nil {
		t.Fatalf("set manual level: %v", err)
	}
	// One check-in with a deterministic award gives the checkins section
	// (schema v2) a known row: site-local date + award string only.
	if err := st.SetSiteConfigValueWithActivity(context.Background(), db.CheckinModeKey,
		db.CheckinModeEnabled, user.ID, time.Unix(1700000000, 0).UTC()); err != nil {
		t.Fatal(err)
	}
	for key, value := range map[string]int64{
		db.CheckinAwardMinMilliKey: 1000,
		db.CheckinAwardMaxMilliKey: 1000,
	} {
		if _, err := st.DB().Exec(`INSERT INTO site_config (key, value, updated_at) VALUES (?, ?, 0)
			ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, strconv.FormatInt(value, 10)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := st.Checkin(context.Background(), user.ID, time.Unix(1700000000, 0).UTC()); err != nil {
		t.Fatalf("checkin: %v", err)
	}
	// Seed both sides of the charity export boundary: one reservation consumed
	// by this user and one reservation against this user's donated resource.
	donor, err := st.CreateDiscordUser("discord-export-donor", "donor", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range []struct {
		attempt string
		owner   int64
		donor   int64
		model   string
		reward  int64
	}{
		{attempt: "export-charity-consumer", owner: user.ID, donor: donor.ID, model: "[公益]/consumer", reward: 0},
		{attempt: "export-charity-donor", owner: donor.ID, donor: user.ID, model: "[公益]/donor", reward: 250},
	} {
		if _, err := st.DB().Exec(`INSERT INTO charity_reservations
			(user_id, donor_user_id, attempt_id, model_snapshot, state, pricing_mode,
			discount_percent, user_reserved, key_reserved, original_charge, user_charge,
			donor_reward, usage_uncached_input_tokens, usage_cache_write_input_tokens,
			usage_cache_read_input_tokens, usage_output_tokens, usage_unknown,
			created_at, finalized_at, updated_at)
			VALUES (?, ?, ?, ?, 'committed', 'per_request', 100, 1000, 1200,
				1200, 1000, ?, 2, 3, 5, 7, 0, 1700000000, 1700000001, 1700000001)`,
			row.owner, row.donor, row.attempt, row.model, row.reward); err != nil {
			t.Fatalf("seed charity reservation %s: %v", row.attempt, err)
		}
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, exportRequest("cap-token"))
	if rec.Code != http.StatusOK {
		t.Fatalf("export status=%d body=%s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("content type=%q", ct)
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("cache=%q", rec.Header().Get("Cache-Control"))
	}
	if !strings.Contains(rec.Header().Get("Content-Disposition"), "attachment") {
		t.Fatalf("disposition=%q", rec.Header().Get("Content-Disposition"))
	}

	var pkg struct {
		SchemaVersion int `json:"schema_version"`
		User          struct {
			ID                        int64  `json:"id"`
			Discord                   string `json:"discord_id"`
			Username                  string `json:"username"`
			ManualLevel               *int   `json:"manual_level"`
			AutoLevel                 int    `json:"auto_level"`
			EffectiveEndpointLimit    int    `json:"effective_endpoint_limit"`
			EffectiveRPMLimit         int    `json:"effective_rpm_limit"`
			ConcurrencyLimit          *int   `json:"concurrency_limit"`
			EffectiveConcurrencyLimit int    `json:"effective_concurrency_limit"`
		} `json:"user"`
		CreditLedger []struct {
			ID                  int64  `json:"id"`
			Kind                string `json:"kind"`
			CreditsDelta        string `json:"credits_delta"`
			DonationCreditDelta string `json:"donation_credit_delta"`
			CreditsAfter        string `json:"credits_after"`
			Reason              string `json:"reason"`
		} `json:"credit_ledger"`
		Endpoints []struct {
			ID      int64  `json:"id"`
			BaseURL string `json:"base_url"`
			Note    string `json:"note"`
			Keys    []struct {
				ID              int64  `json:"id"`
				DisplayHead     string `json:"display_head"`
				DisplayTail     string `json:"display_tail"`
				ForceStoreFalse bool   `json:"force_store_false"`
			} `json:"keys"`
		} `json:"endpoints"`
		Models []struct {
			FullName         string `json:"full_name"`
			FlattenToolCalls bool   `json:"flatten_tool_calls"`
			Bindings         []struct {
				EndpointKeyID   int64  `json:"endpoint_key_id"`
				UpstreamModelID string `json:"upstream_model_id"`
				Ord             int64  `json:"ord"`
			} `json:"bindings"`
		} `json:"models"`
		CallerKey struct {
			Display string `json:"display"`
		} `json:"caller_key"`
		Usage struct {
			TotalRequests              int64 `json:"total_requests"`
			TotalUncachedInputTokens   int64 `json:"total_uncached_input_tokens"`
			TotalCacheWriteInputTokens int64 `json:"total_cache_write_input_tokens"`
			TotalCacheReadInputTokens  int64 `json:"total_cache_read_input_tokens"`
			TotalOutputTokens          int64 `json:"total_output_tokens"`
			TotalPromptTokens          int64 `json:"total_prompt_tokens"`
			TotalCompletionTokens      int64 `json:"total_completion_tokens"`
		} `json:"usage"`
		LogSummary struct {
			TotalLogs int64 `json:"total_logs"`
		} `json:"log_summary"`
		ActivityDaily []struct {
			Day                   int64 `json:"day"`
			ProductActive         bool  `json:"product_active"`
			APIRequests           int64 `json:"api_requests"`
			UncachedInputTokens   int64 `json:"uncached_input_tokens"`
			CacheWriteInputTokens int64 `json:"cache_write_input_tokens"`
			CacheReadInputTokens  int64 `json:"cache_read_input_tokens"`
			OutputTokens          int64 `json:"output_tokens"`
			Checkins              int64 `json:"checkins"`
			ConsoleWrites         int64 `json:"console_writes"`
		} `json:"activity_daily"`
		Checkins []struct {
			Day        string `json:"day"`
			AwardMilli string `json:"award_milli"`
		} `json:"checkins"`
		CharityReservations []struct {
			Model               string `json:"model"`
			State               string `json:"state"`
			UserReservedMilli   string `json:"user_reserved_milli"`
			OriginalChargeMilli string `json:"original_charge_milli"`
			UncachedInputTokens int64  `json:"uncached_input_tokens"`
		} `json:"charity_reservations"`
		DonorRewards []struct {
			Model            string `json:"model"`
			DonorRewardMilli string `json:"donor_reward_milli"`
		} `json:"donor_rewards"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &pkg); err != nil {
		t.Fatalf("decode package: %v body=%s", err, rec.Body.String())
	}
	if pkg.SchemaVersion != 3 || pkg.User.ID != user.ID || pkg.User.Discord != "discord-export" || pkg.User.Username != "alice" {
		t.Fatalf("package header=%+v", pkg)
	}
	if pkg.User.ManualLevel == nil || *pkg.User.ManualLevel != 2 || pkg.User.AutoLevel != 1 {
		t.Fatalf("level export = (%+v, %d), want (2, 1)", pkg.User.ManualLevel, pkg.User.AutoLevel)
	}
	if pkg.User.EffectiveEndpointLimit != db.DefaultEndpointLimit || pkg.User.EffectiveRPMLimit != ratelimit.DefaultRPMPerUserLimit ||
		pkg.User.ConcurrencyLimit != nil || pkg.User.EffectiveConcurrencyLimit != db.DefaultUserConcurrencyLimit {
		t.Fatalf("limit export = %+v", pkg.User)
	}
	if len(pkg.Endpoints) != 1 || pkg.Endpoints[0].BaseURL != "https://upstream.example/v1/" || len(pkg.Endpoints[0].Keys) != 1 || pkg.Endpoints[0].Keys[0].DisplayHead != "head" || !pkg.Endpoints[0].Keys[0].ForceStoreFalse {
		t.Fatalf("endpoints=%+v", pkg.Endpoints)
	}
	if len(pkg.Models) != 1 || pkg.Models[0].FullName != "provider/model" || !pkg.Models[0].FlattenToolCalls || len(pkg.Models[0].Bindings) != 1 || pkg.Models[0].Bindings[0].UpstreamModelID != "upstream-model" {
		t.Fatalf("models=%+v", pkg.Models)
	}
	if !strings.HasPrefix(pkg.CallerKey.Display, "nbk_") {
		t.Fatalf("caller key display=%q", pkg.CallerKey.Display)
	}
	if pkg.Usage.TotalRequests != 1 || pkg.LogSummary.TotalLogs != 1 {
		t.Fatalf("usage=%+v logs=%+v", pkg.Usage, pkg.LogSummary)
	}
	// Credit ledger section: exactly the seeded entries (the adjustment and
	// the deterministic check-in award) with canonical string values and the
	// bounded reason; nothing else ever enters this section.
	if len(pkg.CreditLedger) != 2 {
		t.Fatalf("credit_ledger rows=%d, want 2", len(pkg.CreditLedger))
	}
	kinds := map[string]bool{}
	for _, entry := range pkg.CreditLedger {
		kinds[entry.Kind] = true
	}
	if !kinds["admin_adjustment"] || !kinds["checkin_award"] {
		t.Fatalf("credit_ledger kinds=%v", kinds)
	}
	entry := pkg.CreditLedger[0]
	if entry.Kind != "admin_adjustment" || entry.CreditsDelta != "12345" ||
		entry.DonationCreditDelta != "678" || entry.CreditsAfter != "12345" || entry.Reason != "export fixture" {
		t.Fatalf("credit_ledger entry=%+v", entry)
	}
	if pkg.Usage.TotalUncachedInputTokens != 2 || pkg.Usage.TotalCacheWriteInputTokens != 3 ||
		pkg.Usage.TotalCacheReadInputTokens != 5 || pkg.Usage.TotalOutputTokens != 7 ||
		pkg.Usage.TotalPromptTokens != 10 || pkg.Usage.TotalCompletionTokens != 7 {
		t.Fatalf("usage buckets=%+v", pkg.Usage)
	}
	// The user's own daily activity summary is exported (schema v2); the
	// site-wide rollup table is never part of a personal export.
	if len(pkg.ActivityDaily) != 1 {
		t.Fatalf("activity_daily=%+v", pkg.ActivityDaily)
	}
	day := pkg.ActivityDaily[0]
	if day.ProductActive != true || day.APIRequests != 1 || day.UncachedInputTokens != 2 ||
		day.CacheWriteInputTokens != 3 || day.CacheReadInputTokens != 5 || day.OutputTokens != 7 ||
		day.ConsoleWrites != 1 || day.Checkins != 1 {
		t.Fatalf("activity_daily row=%+v", day)
	}
	// The check-in history section (schema v2): the site-local calendar date
	// under the configured +330 offset and the award as a canonical decimal
	// string — no streak, no location, nothing else.
	if len(pkg.Checkins) != 1 {
		t.Fatalf("checkins=%+v", pkg.Checkins)
	}
	wantDate := time.Unix(db.SiteDayKey(1700000000, 330)+330*60, 0).UTC().Format("2006-01-02")
	if pkg.Checkins[0].Day != wantDate || pkg.Checkins[0].AwardMilli != "1000" {
		t.Fatalf("checkins row=%+v, want (%q, \"1000\")", pkg.Checkins[0], wantDate)
	}
	if len(pkg.CharityReservations) != 1 {
		t.Fatalf("charity_reservations=%+v", pkg.CharityReservations)
	}
	reservation := pkg.CharityReservations[0]
	if reservation.Model != "[公益]/consumer" || reservation.State != "committed" ||
		reservation.UserReservedMilli != "1000" || reservation.OriginalChargeMilli != "1200" ||
		reservation.UncachedInputTokens != 2 {
		t.Fatalf("charity reservation row=%+v", reservation)
	}
	if len(pkg.DonorRewards) != 1 || pkg.DonorRewards[0].Model != "[公益]/donor" || pkg.DonorRewards[0].DonorRewardMilli != "250" {
		t.Fatalf("donor_rewards=%+v", pkg.DonorRewards)
	}

	// Whitelist enforcement: no secret material anywhere in the package.
	for _, forbidden := range []string{"nbsec:", "encrypted_secret", "secret", "access_token", "token_hash", "key_hash", "Authorization"} {
		if bytes.Contains(rec.Body.Bytes(), []byte(forbidden)) {
			t.Fatalf("package leaked %q: %s", forbidden, rec.Body.String())
		}
	}
}

func TestExportConsumesCapabilityOnce(t *testing.T) {
	st := lifecycleTestStore(t)
	user := seedExportFixture(t, st)
	elevation := &fakeElevation{allowCount: 1}
	handler := newExportHandler(t, st, user, elevation, nil)

	first := httptest.NewRecorder()
	handler.ServeHTTP(first, exportRequest("cap-token"))
	if first.Code != http.StatusOK {
		t.Fatalf("first status=%d body=%s", first.Code, first.Body.String())
	}
	second := httptest.NewRecorder()
	handler.ServeHTTP(second, exportRequest("cap-token"))
	if second.Code != http.StatusForbidden {
		t.Fatalf("replayed token status=%d body=%s", second.Code, second.Body.String())
	}
}

// TestExportEndToEndWithRealElevation wires the real auth elevation boundary
// (session cookie -> Discord reauthorization -> single-use capability) to the
// export handler: one successful export, then the same token is dead.
func TestExportEndToEndWithRealElevation(t *testing.T) {
	st := lifecycleTestStore(t)
	user := seedExportFixture(t, st)

	provider := e2eDiscordProvider{}
	service, err := auth.NewUserAuth(auth.UserAuthConfig{
		Store: st, Provider: provider, ClientID: "client-id",
		SiteBaseURL: "https://example.com",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	sessionToken, _, err := st.CreateUserSession(user.ID)
	if err != nil {
		t.Fatal(err)
	}

	// Step 1: POST /api/auth/elevate with the session cookie.
	elevateReq := httptest.NewRequest(http.MethodPost, "https://example.com/api/auth/elevate", nil)
	elevateReq.RemoteAddr = "198.51.100.30:4000"
	elevateReq = elevateReq.WithContext(host.WithStation(elevateReq.Context(), host.StationUser))
	elevateReq.AddCookie(&http.Cookie{Name: auth.UserSessionCookieName, Value: sessionToken})
	elevateRec := httptest.NewRecorder()
	service.Handler().ServeHTTP(elevateRec, elevateReq)
	if elevateRec.Code != http.StatusOK {
		t.Fatalf("elevate status=%d body=%s", elevateRec.Code, elevateRec.Body.String())
	}
	var elevatePayload struct {
		AuthorizationURL string `json:"authorization_url"`
	}
	if err := json.Unmarshal(elevateRec.Body.Bytes(), &elevatePayload); err != nil {
		t.Fatal(err)
	}
	elevateURL, err := url.Parse(elevatePayload.AuthorizationURL)
	if err != nil {
		t.Fatal(err)
	}
	state := elevateURL.Query().Get("state")
	stateCookie := cookieByName(t, elevateRec, auth.OAuthStateCookieName)
	if stateCookie == nil || stateCookie.Value != state {
		t.Fatal("state cookie mismatch")
	}

	// Step 2: callback completes the reauthorization and mints the capability.
	callbackReq := httptest.NewRequest(http.MethodGet,
		"https://example.com/api/auth/discord/callback?state="+url.QueryEscape(state)+"&code=code-e2e", nil)
	callbackReq.RemoteAddr = "198.51.100.30:4000"
	callbackReq = callbackReq.WithContext(host.WithStation(callbackReq.Context(), host.StationUser))
	callbackReq.AddCookie(stateCookie)
	callbackReq.AddCookie(&http.Cookie{Name: auth.UserSessionCookieName, Value: sessionToken})
	callbackRec := httptest.NewRecorder()
	service.Handler().ServeHTTP(callbackRec, callbackReq)
	if callbackRec.Code != http.StatusFound {
		t.Fatalf("callback status=%d body=%s", callbackRec.Code, callbackRec.Body.String())
	}
	elevatedCookie := cookieByName(t, callbackRec, auth.ElevatedCookieName)
	if elevatedCookie == nil || elevatedCookie.Value == "" {
		t.Fatal("no elevated capability cookie")
	}

	// Step 3: export once with the real capability, then it is consumed.
	exportHandler := NewHandler(HandlerDeps{
		Store: st,
		Resolve: func(r *http.Request) (*db.User, error) {
			token := auth.UserSessionToken(r)
			user, err := st.AuthenticateUserSession(token)
			if err != nil || user == nil {
				return nil, auth.ErrElevationRequired
			}
			return user, nil
		},
		Elevation: service,
	})
	doExport := func(token string) int {
		req := exportRequest(token)
		req.AddCookie(&http.Cookie{Name: auth.UserSessionCookieName, Value: sessionToken})
		rec := httptest.NewRecorder()
		exportHandler.ServeHTTP(rec, req)
		return rec.Code
	}
	if code := doExport(elevatedCookie.Value); code != http.StatusOK {
		t.Fatalf("first export status=%d", code)
	}
	if code := doExport(elevatedCookie.Value); code != http.StatusForbidden {
		t.Fatalf("replayed export status=%d, want 403", code)
	}
}

// e2eDiscordProvider is a minimal provider fake for the end-to-end test: the
// re-authorized identity always matches the fixture user.
type e2eDiscordProvider struct{}

func (e2eDiscordProvider) AuthorizationURL(_ context.Context, request auth.DiscordAuthorizeRequest) (string, error) {
	return "https://discord.example/authorize?state=" + url.QueryEscape(request.State), nil
}

func (e2eDiscordProvider) Exchange(_ context.Context, _ string, _ string) (auth.DiscordLogin, error) {
	return auth.DiscordLogin{
		Identity: auth.DiscordIdentity{ID: "discord-export", Username: "alice"},
	}, nil
}

func cookieByName(t *testing.T, rec *httptest.ResponseRecorder, name string) *http.Cookie {
	t.Helper()
	for _, cookie := range rec.Result().Cookies() {
		if cookie.Name == name {
			return cookie
		}
	}
	return nil
}

// --- service-level bound tests ---------------------------------------------

func mustLimiter(t *testing.T) *ratelimit.ProbeLimiter {
	t.Helper()
	limiter, err := ratelimit.NewProbeLimiter(ratelimit.ProbeLimiterConfig{
		Window: time.Minute, DefaultLimit: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = limiter.Close() })
	return limiter
}

func TestBuildExportFailsClosedOnInvalidUser(t *testing.T) {
	st := lifecycleTestStore(t)
	svc := NewExportService(st)
	if _, err := svc.BuildExport(context.Background(), nil); !errors.Is(err, ErrExportTooLarge) {
		t.Fatalf("nil user err=%v, want ErrExportTooLarge", err)
	}
	if _, err := svc.BuildExport(context.Background(), &db.User{ID: 0}); !errors.Is(err, ErrExportTooLarge) {
		t.Fatalf("zero user err=%v, want ErrExportTooLarge", err)
	}
}

func TestBuildExportEmptyAccount(t *testing.T) {
	st := lifecycleTestStore(t)
	user, err := st.CreateDiscordUser("discord-empty", "empty", "")
	if err != nil {
		t.Fatal(err)
	}
	svc := NewExportService(st)
	payload, err := svc.BuildExport(context.Background(), user)
	if err != nil {
		t.Fatalf("build empty export: %v", err)
	}
	var pkg struct {
		Endpoints []any `json:"endpoints"`
		Models    []any `json:"models"`
		CallerKey any   `json:"caller_key"`
	}
	if err := json.Unmarshal(payload, &pkg); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if pkg.Endpoints == nil || pkg.Models == nil || pkg.CallerKey != nil {
		t.Fatalf("empty package shape: %s", payload)
	}
}

func TestBuildExportRefreshesCurrentRawAndEffectiveUserLimits(t *testing.T) {
	st := lifecycleTestStore(t)
	// Keep the create-time row as a deliberately stale session principal.
	stale, err := st.CreateDiscordUser("discord-export-limits", "limits", "")
	if err != nil {
		t.Fatal(err)
	}
	if stale.EndpointLimit != nil || stale.RPMLimit != nil || stale.ConcurrencyLimit != nil {
		t.Fatalf("fresh row unexpectedly has explicit limits: %+v", stale)
	}
	if err := st.SetSiteConfigValue("default_endpoint_limit", "4"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSiteConfigValue("default_rpm_per_user", "40"); err != nil {
		t.Fatal(err)
	}
	endpoint, rpm, concurrency := 10_000, 41, 9
	if _, err := st.UpdateUserLimits(stale.ID, db.UserLimitPatch{
		EndpointLimitSet: true, EndpointLimit: &endpoint,
		RPMLimitSet: true, RPMLimit: &rpm,
		ConcurrencyLimitSet: true, ConcurrencyLimit: &concurrency,
	}); err != nil {
		t.Fatal(err)
	}

	type exportedLimits struct {
		SchemaVersion int `json:"schema_version"`
		User          struct {
			EndpointLimit             *int `json:"endpoint_limit"`
			EffectiveEndpointLimit    int  `json:"effective_endpoint_limit"`
			RPMLimit                  *int `json:"rpm_limit"`
			EffectiveRPMLimit         int  `json:"effective_rpm_limit"`
			ConcurrencyLimit          *int `json:"concurrency_limit"`
			EffectiveConcurrencyLimit int  `json:"effective_concurrency_limit"`
		} `json:"user"`
	}
	decode := func() exportedLimits {
		t.Helper()
		payload, err := NewExportService(st).BuildExport(context.Background(), stale)
		if err != nil {
			t.Fatalf("BuildExport: %v", err)
		}
		var out exportedLimits
		if err := json.Unmarshal(payload, &out); err != nil {
			t.Fatalf("decode export: %v", err)
		}
		return out
	}

	out := decode()
	if out.SchemaVersion != 3 || out.User.EndpointLimit == nil || *out.User.EndpointLimit != endpoint ||
		out.User.EffectiveEndpointLimit != endpoint || out.User.RPMLimit == nil || *out.User.RPMLimit != rpm ||
		out.User.EffectiveRPMLimit != rpm || out.User.ConcurrencyLimit == nil || *out.User.ConcurrencyLimit != concurrency ||
		out.User.EffectiveConcurrencyLimit != concurrency {
		t.Fatalf("explicit current limits from stale principal: %+v", out)
	}

	// Clearing the current row restores nullable wire values. A later default
	// edit is projected at export generation time; the stale principal cannot
	// pin either the old explicit values or old defaults.
	if _, err := st.UpdateUserLimits(stale.ID, db.UserLimitPatch{
		EndpointLimitSet: true, EndpointLimit: nil,
		RPMLimitSet: true, RPMLimit: nil,
		ConcurrencyLimitSet: true, ConcurrencyLimit: nil,
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSiteConfigValue("default_endpoint_limit", "6"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSiteConfigValue("default_rpm_per_user", "70"); err != nil {
		t.Fatal(err)
	}
	out = decode()
	if out.User.EndpointLimit != nil || out.User.EffectiveEndpointLimit != 6 ||
		out.User.RPMLimit != nil || out.User.EffectiveRPMLimit != 70 ||
		out.User.ConcurrencyLimit != nil || out.User.EffectiveConcurrencyLimit != db.DefaultUserConcurrencyLimit {
		t.Fatalf("current nullable/default limits from stale principal: %+v", out)
	}
}

func TestHandlerMapsProjectionLimitToPayloadTooLarge(t *testing.T) {
	st := lifecycleTestStore(t)
	user := seedExportFixture(t, st)
	limitedHandler := NewHandler(HandlerDeps{
		Elevation: &fakeElevation{allowCount: 1},
		Resolve: func(*http.Request) (*db.User, error) {
			return user, nil
		},
		Limiter:  mustLimiter(t),
		Exporter: stubExporter{err: db.ErrExportLimit},
	})
	rec := httptest.NewRecorder()
	limitedHandler.ServeHTTP(rec, exportRequest("cap"))
	if rec.Code != http.StatusRequestEntityTooLarge || !strings.Contains(rec.Body.String(), `"code":"payload_too_large"`) {
		t.Fatalf("limit status=%d body=%s", rec.Code, rec.Body.String())
	}

	tooLargeHandler := NewHandler(HandlerDeps{
		Elevation: &fakeElevation{allowCount: 1},
		Resolve: func(*http.Request) (*db.User, error) {
			return user, nil
		},
		Limiter:  mustLimiter(t),
		Exporter: stubExporter{err: ErrExportTooLarge},
	})
	rec = httptest.NewRecorder()
	tooLargeHandler.ServeHTTP(rec, exportRequest("cap"))
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("size bound status=%d body=%s", rec.Code, rec.Body.String())
	}

	internalHandler := NewHandler(HandlerDeps{
		Elevation: &fakeElevation{allowCount: 1},
		Resolve: func(*http.Request) (*db.User, error) {
			return user, nil
		},
		Limiter:  mustLimiter(t),
		Exporter: stubExporter{err: errors.New("boom")},
	})
	rec = httptest.NewRecorder()
	internalHandler.ServeHTTP(rec, exportRequest("cap"))
	if rec.Code != http.StatusInternalServerError || !strings.Contains(rec.Body.String(), `"code":"internal"`) || strings.Contains(rec.Body.String(), "boom") {
		t.Fatalf("internal status=%d body=%s", rec.Code, rec.Body.String())
	}
}

type stubExporter struct {
	payload []byte
	err     error
}

func (s stubExporter) BuildExport(context.Context, *db.User) ([]byte, error) {
	if s.err != nil {
		return nil, s.err
	}
	if s.payload == nil {
		return []byte(`{}`), nil
	}
	return s.payload, nil
}

// guard: the stable code exists and maps to 403.
func TestElevationRequiredCodeIsStable(t *testing.T) {
	if httperr.CodeElevationRequired != "elevated_required" {
		t.Fatalf("code=%q", httperr.CodeElevationRequired)
	}
	rec := httptest.NewRecorder()
	httperr.WriteError(rec, httperr.New(httperr.CodeElevationRequired, "elevated capability required"))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status=%d", rec.Code)
	}
}
