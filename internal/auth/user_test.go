package auth

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/host"
	"github.com/waiting-here/NonbiriAPI/internal/ratelimit"
)

// authAdmissionClock is a mutable clock shared with an IPThrottle so the OAuth
// start admission tests can advance time deterministically without sleeping.
type authAdmissionClock struct {
	mu  sync.Mutex
	now time.Time
}

func newAuthAdmissionClock() *authAdmissionClock {
	return &authAdmissionClock{now: time.Unix(1_700_000_000, 0).UTC()}
}

func (c *authAdmissionClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *authAdmissionClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

// newTestUserAuthWithThrottle wires a UserAuth whose OAuth start admission
// throttle is the supplied instance. The state signer is deterministic and
// closed with the test.
func newTestUserAuthWithThrottle(t *testing.T, st *db.Store, provider DiscordProvider, gate RegistrationGateFunc, throttle *ratelimit.IPThrottle) *UserAuth {
	t.Helper()
	key := bytes.Repeat([]byte{0x92}, 32)
	state, err := NewStateManagerWithKey(key, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = state.Close() })
	service, err := NewUserAuth(UserAuthConfig{
		Store: st, Provider: provider, ClientID: "client-id", State: state,
		SiteBaseURL: "https://example.com", RegistrationGate: gate,
		OAuthStartThrottle: throttle,
	})
	if err != nil {
		t.Fatalf("NewUserAuth: %v", err)
	}
	return service
}

// startRequestFrom builds a GET /api/auth/discord/start request whose trusted
// edge ClientIP is derived from the supplied remote address.
func startRequestFrom(remoteIP string) *http.Request {
	r := stationRequest(http.MethodGet, "https://example.com/api/auth/discord/start", host.StationUser, nil)
	r.RemoteAddr = remoteIP + ":4000"
	return r
}

func elevateRequestFrom(sessionToken, remoteIP string) *http.Request {
	r := stationRequest(http.MethodPost, "https://example.com/api/auth/elevate", host.StationUser, nil)
	r.RemoteAddr = remoteIP + ":4000"
	if sessionToken != "" {
		r.AddCookie(&http.Cookie{Name: UserSessionCookieName, Value: sessionToken})
	}
	return r
}

// startStateValue extracts the OAuth state from a successful start response's
// Location header, proving the state pool actually issued a state.
func startStateValue(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	if rec.Code != http.StatusFound {
		return ""
	}
	location := rec.Header().Get("Location")
	u, err := url.Parse(location)
	if err != nil {
		return ""
	}
	values := u.Query()["state"]
	if len(values) != 1 {
		return ""
	}
	return values[0]
}

// TestOAuthStartAdmissionThrottlesSingleIPAndCannotExhaustStatePool drives one
// client past the per-IP limit and confirms only the limit worth of OAuth
// states are issued (every later attempt is 429 with Retry-After), so the
// shared fail-closed 4096 state pool can never be filled by a single attacker.
func TestOAuthStartAdmissionThrottlesSingleIPAndCannotExhaustStatePool(t *testing.T) {
	st := authTestStore(t)
	provider := &fakeDiscordProvider{}
	clock := newAuthAdmissionClock()
	throttle, err := ratelimit.NewIPThrottle(ratelimit.IPThrottleConfig{
		Limit: 3, Window: time.Minute, Penalty: 60 * time.Second,
		MaxKeys: 64, MaxHitsPerKey: 64, MaxKeyBytes: 64,
	}, ratelimit.WithClockFunc(clock.Now))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = throttle.Close() })
	service := newTestUserAuthWithThrottle(t, st, provider, func(context.Context) (RegistrationGate, error) {
		return RegistrationGate{}, nil
	}, throttle)

	issued := 0
	for i := 0; i < 2000; i++ {
		rec := httptest.NewRecorder()
		service.Start(rec, startRequestFrom("198.51.100.20"))
		switch rec.Code {
		case http.StatusFound:
			issued++
			if startStateValue(t, rec) == "" {
				t.Fatalf("attempt %d: 302 without a state", i)
			}
		case http.StatusTooManyRequests:
			if rec.Header().Get("Retry-After") == "" {
				t.Fatalf("attempt %d: 429 without Retry-After", i)
			}
		default:
			t.Fatalf("attempt %d: unexpected status %d body=%s", i, rec.Code, rec.Body.String())
		}
	}
	if issued != 3 {
		t.Fatalf("issued %d states for a single throttled IP, want 3", issued)
	}

	// A different IP is unaffected and the global pool is not exhausted: it can
	// still start. This is the inverse of the pre-admission failure mode where
	// one IP would have filled the pool and DoSed every other login.
	other := httptest.NewRecorder()
	service.Start(other, startRequestFrom("203.0.113.7"))
	if other.Code != http.StatusFound {
		t.Fatalf("different IP start status=%d body=%s", other.Code, other.Body.String())
	}
}

// TestOAuthStartAdmissionIsolatesByClientIP confirms the throttle counts per
// trusted-edge ClientIP: each IP gets its own window.
func TestOAuthStartAdmissionIsolatesByClientIP(t *testing.T) {
	st := authTestStore(t)
	provider := &fakeDiscordProvider{}
	clock := newAuthAdmissionClock()
	throttle, err := ratelimit.NewIPThrottle(ratelimit.IPThrottleConfig{
		Limit: 2, Window: time.Minute, Penalty: 60 * time.Second,
		MaxKeys: 64, MaxHitsPerKey: 64, MaxKeyBytes: 64,
	}, ratelimit.WithClockFunc(clock.Now))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = throttle.Close() })
	service := newTestUserAuthWithThrottle(t, st, provider, func(context.Context) (RegistrationGate, error) {
		return RegistrationGate{}, nil
	}, throttle)

	for _, ip := range []string{"198.51.100.20", "203.0.113.7", "192.0.2.44"} {
		for i := 0; i < 2; i++ {
			rec := httptest.NewRecorder()
			service.Start(rec, startRequestFrom(ip))
			if rec.Code != http.StatusFound {
				t.Fatalf("ip %s attempt %d status=%d body=%s", ip, i, rec.Code, rec.Body.String())
			}
		}
		rec := httptest.NewRecorder()
		service.Start(rec, startRequestFrom(ip))
		if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") == "" {
			t.Fatalf("ip %s over-limit status=%d retry=%q body=%s", ip, rec.Code, rec.Header().Get("Retry-After"), rec.Body.String())
		}
	}
}

// TestOAuthStartAdmissionDisabledPassesThrough confirms Limit == 0 admits every
// caller (the configured reverse-proxy limit remains the outer boundary).
func TestOAuthStartAdmissionDisabledPassesThrough(t *testing.T) {
	st := authTestStore(t)
	provider := &fakeDiscordProvider{}
	clock := newAuthAdmissionClock()
	throttle, err := ratelimit.NewIPThrottle(ratelimit.IPThrottleConfig{
		Limit: 0, Window: time.Minute, Penalty: 60 * time.Second,
		MaxKeys: 64, MaxHitsPerKey: 64, MaxKeyBytes: 64,
	}, ratelimit.WithClockFunc(clock.Now))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = throttle.Close() })
	service := newTestUserAuthWithThrottle(t, st, provider, func(context.Context) (RegistrationGate, error) {
		return RegistrationGate{}, nil
	}, throttle)

	for i := 0; i < 100; i++ {
		rec := httptest.NewRecorder()
		service.Start(rec, startRequestFrom("198.51.100.20"))
		if rec.Code != http.StatusFound {
			t.Fatalf("disabled attempt %d status=%d body=%s", i, rec.Code, rec.Body.String())
		}
	}
}

// TestOAuthElevateAdmissionSharesThrottleWithLogin confirms elevation start is
// admission-controlled by the same throttle instance and the same ClientIP key
// as login start, and that admission runs only after the session check (an
// unauthenticated elevate attempt gets 401 and never reaches the state pool).
func TestOAuthElevateAdmissionSharesThrottleWithLogin(t *testing.T) {
	st := authTestStore(t)
	provider := &fakeDiscordProvider{
		login: DiscordLogin{Identity: DiscordIdentity{ID: "discord-elev", Username: "alice"}},
	}
	clock := newAuthAdmissionClock()
	throttle, err := ratelimit.NewIPThrottle(ratelimit.IPThrottleConfig{
		Limit: 3, Window: time.Minute, Penalty: 60 * time.Second,
		MaxKeys: 64, MaxHitsPerKey: 64, MaxKeyBytes: 64,
	}, ratelimit.WithClockFunc(clock.Now))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = throttle.Close() })
	service := newTestUserAuthWithThrottle(t, st, provider, func(context.Context) (RegistrationGate, error) {
		return RegistrationGate{}, nil
	}, throttle)

	// An elevate without a session is rejected before admission: the throttle
	// counter must not be consumed, and no state is issued.
	unauth := httptest.NewRecorder()
	service.Elevate(unauth, elevateRequestFrom("", "198.51.100.20"))
	if unauth.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated elevate status=%d body=%s", unauth.Code, unauth.Body.String())
	}

	user, err := st.CreateDiscordUser("discord-elev", "alice", "")
	if err != nil {
		t.Fatal(err)
	}
	sessionToken, _, err := st.CreateUserSession(user.ID)
	if err != nil {
		t.Fatal(err)
	}

	// Burn the per-IP allowance with three login starts (same ClientIP). The
	// throttle is shared across flows, so the fourth call from the same IP — an
	// authenticated elevate — must be 429, not a fresh state.
	for i := 0; i < 3; i++ {
		rec := httptest.NewRecorder()
		service.Start(rec, startRequestFrom("198.51.100.20"))
		if rec.Code != http.StatusFound {
			t.Fatalf("login start %d status=%d body=%s", i, rec.Code, rec.Body.String())
		}
	}
	throttled := httptest.NewRecorder()
	service.Elevate(throttled, elevateRequestFrom(sessionToken, "198.51.100.20"))
	if throttled.Code != http.StatusTooManyRequests || throttled.Header().Get("Retry-After") == "" {
		t.Fatalf("shared-throttle elevate status=%d retry=%q body=%s", throttled.Code, throttled.Header().Get("Retry-After"), throttled.Body.String())
	}

	// A different IP can still start an elevation.
	other := httptest.NewRecorder()
	service.Elevate(other, elevateRequestFrom(sessionToken, "203.0.113.7"))
	if other.Code != http.StatusOK {
		t.Fatalf("different IP elevate status=%d body=%s", other.Code, other.Body.String())
	}

	// After the penalty expires, the same IP can elevate again.
	clock.Advance(61 * time.Second)
	after := httptest.NewRecorder()
	service.Elevate(after, elevateRequestFrom(sessionToken, "198.51.100.20"))
	if after.Code != http.StatusOK {
		t.Fatalf("elevate after penalty status=%d body=%s", after.Code, after.Body.String())
	}
}

// TestOAuthStartAdmissionCapacityFailsClosed503 confirms a full bounded entry
// store is 503 fail-closed rather than evicting a live identity.
func TestOAuthStartAdmissionCapacityFailsClosed503(t *testing.T) {
	st := authTestStore(t)
	provider := &fakeDiscordProvider{}
	clock := newAuthAdmissionClock()
	throttle, err := ratelimit.NewIPThrottle(ratelimit.IPThrottleConfig{
		Limit: 4, Window: time.Minute, Penalty: 60 * time.Second,
		MaxKeys: 2, MaxHitsPerKey: 64, MaxKeyBytes: 64,
	}, ratelimit.WithClockFunc(clock.Now))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = throttle.Close() })
	service := newTestUserAuthWithThrottle(t, st, provider, func(context.Context) (RegistrationGate, error) {
		return RegistrationGate{}, nil
	}, throttle)

	for _, ip := range []string{"198.51.100.20", "203.0.113.7"} {
		rec := httptest.NewRecorder()
		service.Start(rec, startRequestFrom(ip))
		if rec.Code != http.StatusFound {
			t.Fatalf("fill %s status=%d body=%s", ip, rec.Code, rec.Body.String())
		}
	}
	over := httptest.NewRecorder()
	service.Start(over, startRequestFrom("192.0.2.44"))
	if over.Code != http.StatusServiceUnavailable {
		t.Fatalf("capacity status=%d body=%s; want 503 fail-closed, not evict", over.Code, over.Body.String())
	}
	if over.Header().Get("Retry-After") != "" {
		t.Fatalf("capacity 503 must not carry Retry-After: %q", over.Header().Get("Retry-After"))
	}
	// The two live identities are unaffected by the third IP's 503.
	for _, ip := range []string{"198.51.100.20", "203.0.113.7"} {
		rec := httptest.NewRecorder()
		service.Start(rec, startRequestFrom(ip))
		if rec.Code != http.StatusFound {
			t.Fatalf("live ip %s after capacity status=%d body=%s", ip, rec.Code, rec.Body.String())
		}
	}
}

// TestOAuthStartAdmissionReconfigureLiveApplies confirms a runtime reconfigure
// (the site-config live-apply path) changes the active limit and penalty for
// the next OAuth start without restarting the process.
func TestOAuthStartAdmissionReconfigureLiveApplies(t *testing.T) {
	st := authTestStore(t)
	provider := &fakeDiscordProvider{}
	clock := newAuthAdmissionClock()
	throttle, err := ratelimit.NewIPThrottle(ratelimit.IPThrottleConfig{
		Limit: 3, Window: time.Minute, Penalty: 60 * time.Second,
		MaxKeys: 64, MaxHitsPerKey: 64, MaxKeyBytes: 64,
	}, ratelimit.WithClockFunc(clock.Now))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = throttle.Close() })
	service := newTestUserAuthWithThrottle(t, st, provider, func(context.Context) (RegistrationGate, error) {
		return RegistrationGate{}, nil
	}, throttle)

	for i := 0; i < 3; i++ {
		rec := httptest.NewRecorder()
		service.Start(rec, startRequestFrom("198.51.100.20"))
		if rec.Code != http.StatusFound {
			t.Fatalf("fill %d status=%d body=%s", i, rec.Code, rec.Body.String())
		}
	}
	penalized := httptest.NewRecorder()
	service.Start(penalized, startRequestFrom("198.51.100.20"))
	if penalized.Code != http.StatusTooManyRequests {
		t.Fatalf("pre-reconfigure penalty status=%d body=%s", penalized.Code, penalized.Body.String())
	}

	// Live-apply a higher limit (mirrors PATCH /admin/api/site-config/{key}).
	// The in-flight penalty keeps its original 60s block until it expires
	// naturally; a config update never lifts an active block.
	if err := throttle.Reconfigure(ratelimit.IPThrottleConfig{
		Limit: 6, Window: time.Minute, Penalty: 30 * time.Second,
	}); err != nil {
		t.Fatalf("reconfigure: %v", err)
	}
	clock.Advance(61 * time.Second)
	issued := 0
	for i := 0; i < 6; i++ {
		rec := httptest.NewRecorder()
		service.Start(rec, startRequestFrom("198.51.100.20"))
		if rec.Code != http.StatusFound {
			t.Fatalf("post-reconfigure attempt %d status=%d body=%s", i, rec.Code, rec.Body.String())
		}
		issued++
	}
	over := httptest.NewRecorder()
	service.Start(over, startRequestFrom("198.51.100.20"))
	if over.Code != http.StatusTooManyRequests || over.Header().Get("Retry-After") != "30" {
		t.Fatalf("new penalty status=%d retry=%q; want 429 Retry-After=30", over.Code, over.Header().Get("Retry-After"))
	}
	if issued != 6 {
		t.Fatalf("issued %d after reconfigure, want 6", issued)
	}

	// Live-apply Limit == 0 disables admission entirely.
	if err := throttle.Reconfigure(ratelimit.IPThrottleConfig{Limit: 0}); err != nil {
		t.Fatalf("reconfigure disabled: %v", err)
	}
	clock.Advance(31 * time.Second)
	for i := 0; i < 50; i++ {
		rec := httptest.NewRecorder()
		service.Start(rec, startRequestFrom("198.51.100.20"))
		if rec.Code != http.StatusFound {
			t.Fatalf("disabled attempt %d status=%d body=%s", i, rec.Code, rec.Body.String())
		}
	}
}

// TestOAuthStartAdmissionStableErrorShape confirms the 429 and 503 admission
// responses use the stable error envelope (rate_limited / service_unavailable)
// and never echo the client IP or any internal detail.
func TestOAuthStartAdmissionStableErrorShape(t *testing.T) {
	st := authTestStore(t)
	provider := &fakeDiscordProvider{}
	clock := newAuthAdmissionClock()
	throttle, err := ratelimit.NewIPThrottle(ratelimit.IPThrottleConfig{
		Limit: 1, Window: time.Minute, Penalty: 60 * time.Second,
		MaxKeys: 1, MaxHitsPerKey: 64, MaxKeyBytes: 64,
	}, ratelimit.WithClockFunc(clock.Now))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = throttle.Close() })
	service := newTestUserAuthWithThrottle(t, st, provider, func(context.Context) (RegistrationGate, error) {
		return RegistrationGate{}, nil
	}, throttle)

	ok := httptest.NewRecorder()
	service.Start(ok, startRequestFrom("198.51.100.20"))
	if ok.Code != http.StatusFound {
		t.Fatalf("first start status=%d body=%s", ok.Code, ok.Body.String())
	}
	rateLimited := httptest.NewRecorder()
	service.Start(rateLimited, startRequestFrom("198.51.100.20"))
	if rateLimited.Code != http.StatusTooManyRequests {
		t.Fatalf("rate-limited status=%d body=%s", rateLimited.Code, rateLimited.Body.String())
	}
	if !strings.Contains(rateLimited.Body.String(), `"code":"rate_limited"`) {
		t.Fatalf("rate-limited body missing stable code: %s", rateLimited.Body.String())
	}
	if strings.Contains(rateLimited.Body.String(), "198.51.100.20") {
		t.Fatalf("rate-limited body echoed client IP: %s", rateLimited.Body.String())
	}

	// A third distinct IP fills the bounded entry store (MaxKeys == 1 already
	// holds the first IP) and gets 503 service_unavailable, fail-closed.
	capacity := httptest.NewRecorder()
	service.Start(capacity, startRequestFrom("203.0.113.7"))
	if capacity.Code != http.StatusServiceUnavailable {
		t.Fatalf("capacity status=%d body=%s", capacity.Code, capacity.Body.String())
	}
	if !strings.Contains(capacity.Body.String(), `"code":"service_unavailable"`) {
		t.Fatalf("capacity body missing stable code: %s", capacity.Body.String())
	}
}

// TestOAuthStartAdmissionNilThrottlePasses confirms a UserAuth constructed
// without a throttle behaves exactly as before (no admission, no 429 path),
// so the feature is opt-in at the integration rail.
func TestOAuthStartAdmissionNilThrottlePasses(t *testing.T) {
	st := authTestStore(t)
	provider := &fakeDiscordProvider{}
	service := newTestUserAuth(t, st, provider, func(context.Context) (RegistrationGate, error) {
		return RegistrationGate{}, nil
	})
	for i := 0; i < 100; i++ {
		rec := httptest.NewRecorder()
		service.Start(rec, startRequestFrom("198.51.100.20"))
		if rec.Code != http.StatusFound {
			t.Fatalf("nil-throttle attempt %d status=%d body=%s", i, rec.Code, rec.Body.String())
		}
	}
}
