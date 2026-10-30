package codexauth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/diagnostic"
)

func TestQuotaPayloadOptionalBuckets(t *testing.T) {
	cases := []struct {
		body    string
		count   int
		partial bool
	}{
		{`{"plan_type":"business","rate_limit":{"primary_window":{"used_percent":27,"limit_window_seconds":7200,"reset_at":1800003600}},"credits":{"has_credits":true,"unlimited":false,"balance":"12.25"},"additional_rate_limits":[{"metered_feature":"review","limit_name":"Review","rate_limit":{"secondary_window":{"used_percent":105,"limit_window_seconds":86400,"reset_at":1800020000}}},42]}`, 2, true},
		{`{"rate_limit":null,"credits":null,"plan_type":"enterprise"}`, 1, false},
		{`{"rateLimits":{"primary":{"usedPercent":90}},"rateLimitsByLimitId":{"codex":{"primary":{"usedPercent":10,"windowDurationMins":15,"resetsAt":1800000000}},"other":{"primary":null},"bad":false}}`, 2, true},
		{`{"rate_limit":{"primary_window":{"used_percent":null,"limit_window_seconds":"wrong"},"future_window":{"used_percent":-5}}}`, 1, false},
	}
	for _, tc := range cases {
		data, err := decodeQuota([]byte(tc.body))
		if err != nil || len(data.Buckets) != tc.count || data.Partial != tc.partial {
			t.Fatalf("unexpected quota: %+v %v", data, err)
		}
	}
	data, _ := decodeQuota([]byte(cases[2].body))
	if len(data.Buckets[0].Windows) != 1 || *data.Buckets[0].Windows[0].UsedPercent != 10 {
		t.Fatal("single bucket was double-counted", data)
	}
	data, _ = decodeQuota([]byte(cases[1].body))
	if data.Buckets[0].Credits != nil || len(data.Buckets[0].Windows) != 0 {
		t.Fatal("missing quota was fabricated")
	}
	for _, raw := range []string{`[]`, `{"error":"no"}`, `{`} {
		if _, err := decodeQuota([]byte(raw)); err == nil {
			t.Fatal("invalid quota accepted")
		}
	}
}
func quotaConnected(t *testing.T, f *fixture) Status {
	t.Helper()
	if _, err := f.m.Begin(t.Context(), 0); err != nil {
		t.Fatal(err)
	}
	return waitStatus(t, f, "succeeded")
}
func TestQuotaConnectionGenerationDistinguishesReplacement(t *testing.T) {
	f := newFixture(t)
	if err := f.m.ValidateGeneration(t.Context(), 0); !errors.Is(err, ErrDisconnected) {
		t.Fatal("disconnected generation", err)
	}
	status := quotaConnected(t, f)
	if err := f.m.ValidateGeneration(t.Context(), status.Revision); err != nil {
		t.Fatal(err)
	}
	if err := f.m.ValidateGeneration(t.Context(), status.Revision-1); !errors.Is(err, ErrConflict) {
		t.Fatal("replaced generation", err)
	}
}
func TestQuotaCacheStaleAndRecovery(t *testing.T) {
	f := newFixture(t)
	var calls atomic.Int64
	var mode atomic.Int64
	f.usageFn = func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != "GET" || r.Header.Get("ChatGPT-Account-ID") != "workspace" || r.Header.Get("Authorization") == "" || r.Header.Get("x-openai-codex-luna-reserve") != "" {
			t.Error("incorrect quota identity or Reserve opt-in")
		}
		switch mode.Load() {
		case 1:
			w.Header().Set("Retry-After", "12")
			w.WriteHeader(429)
			w.Write([]byte(`{"error":{"code":"rate_limit_exceeded","message":"try later"}}`))
			return
		case 2:
			if f.refreshes.Load() == 0 {
				w.WriteHeader(401)
				return
			}
		}
		w.Write([]byte(`{"plan_type":"business","rate_limit":{"primary_window":{"used_percent":25,"limit_window_seconds":600}}}`))
	}
	status := quotaConnected(t, f)
	first, err := f.m.Quota(t.Context(), false)
	if err != nil || first.Data == nil || first.Revision != status.Revision {
		t.Fatal(first, err)
	}
	if _, err = f.m.Quota(t.Context(), false); err != nil || calls.Load() != 1 {
		t.Fatal("cache missed", err, calls.Load())
	}
	mode.Store(1)
	stale, err := f.m.Quota(t.Context(), true)
	if err != nil || !stale.Stale || stale.Data == nil || stale.Error == nil || stale.Error.SiteStatus != 503 || stale.Error.Details.RetryAfterSeconds == nil || *stale.Error.Details.RetryAfterSeconds != 12 {
		t.Fatal(stale, err)
	}
	if f.refreshes.Load() != 0 {
		t.Fatal("429 refreshed credentials")
	}
	mode.Store(2)
	f.refreshFn = func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		if json.NewDecoder(r.Body).Decode(&body) != nil || body["refresh_reason"] != "unauthorized_recovery" {
			t.Error("incorrect recovery reason")
		}
		f.tokens(w, "recovered", "refresh-rotated")
	}
	fresh, err := f.m.Quota(t.Context(), true)
	if err != nil || fresh.Stale || fresh.Data == nil || f.refreshes.Load() != 1 {
		t.Fatal(fresh, err, f.refreshes.Load())
	}
}
func TestQuotaSharedFlightCancelAndReconnect(t *testing.T) {
	f := newFixture(t)
	entered := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int64
	f.usageFn = func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			close(entered)
			<-release
		}
		w.Write([]byte(`{"plan_type":"plus"}`))
	}
	status := quotaConnected(t, f)
	var group sync.WaitGroup
	group.Add(2)
	errorsOut := make(chan error, 2)
	for range 2 {
		go func() { defer group.Done(); _, err := f.m.Quota(context.Background(), true); errorsOut <- err }()
	}
	<-entered
	deadline := time.Now().Add(time.Second)
	for {
		f.m.mu.Lock()
		waiters := f.m.quotaFlight.waiters
		f.m.mu.Unlock()
		if waiters == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("reads were not merged")
		}
		time.Sleep(time.Millisecond)
	}
	if _, err := f.m.Disconnect(t.Context(), status.Revision); err != nil {
		t.Fatal(err)
	}
	close(release)
	group.Wait()
	close(errorsOut)
	for err := range errorsOut {
		if !errors.Is(err, ErrConflict) && !errors.Is(err, ErrDisconnected) {
			t.Fatal("late quota accepted", err)
		}
	}
	if calls.Load() != 1 {
		t.Fatal("requests not merged")
	}
	next, _ := f.m.Status(t.Context())
	if _, err := f.m.Begin(t.Context(), next.Revision); err != nil {
		t.Fatal(err)
	}
	next = waitStatus(t, f, "succeeded")
	result, err := f.m.Quota(t.Context(), false)
	if err != nil || result.Revision != next.Revision || result.Stale {
		t.Fatal("old quota replaced new connection", result, err)
	}
}
func TestQuotaFailurePrivacyAndPermission(t *testing.T) {
	f := newFixture(t)
	quotaConnected(t, f)
	f.usageFn = func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Request-ID", strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
		w.Header().Set("CF-Ray", "safe-trace")
		w.WriteHeader(403)
		json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": "forbidden", "message": "Bearer " + strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ") + " <script>private</script>"}})
	}
	out, err := f.m.Quota(t.Context(), true)
	if err != nil || out.Error == nil || out.Error.SiteStatus != 502 {
		t.Fatal(out, err)
	}
	raw, _ := json.Marshal(out)
	if strings.Contains(string(raw), ".fixture") || strings.Contains(string(raw), "refresh-original") {
		t.Fatal("credential leaked")
	}
	if out.Error.Details.Code == "upstream_account_restricted" {
		t.Fatal("403 alone inferred restriction")
	}
	f.denied.Store(true)
	if _, err = f.m.Quota(t.Context(), false); err == nil {
		t.Fatal("unauthorized cache read")
	}
}
func TestOAuthFailureClassification(t *testing.T) {
	for _, tc := range []struct {
		status   int
		code     string
		terminal bool
	}{{401, "invalid_client", false}, {400, "invalid_grant", true}, {403, "access_denied", false}, {429, "slow_down", false}} {
		err := &OAuthError{Status: tc.status, Code: tc.code}
		if err.Terminal() != tc.terminal {
			t.Fatal("incorrect reconnect instruction", tc)
		}
	}
	f := newFixture(t)
	quotaConnected(t, f)
	f.now.Add(3600)
	f.refreshFn = func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		w.Write([]byte(`{"error":"private-refresh-secret"}`))
	}
	_, _, _, err := f.m.AccessTokenSnapshot(t.Context())
	var op *diagnostic.OperationError
	if !errors.As(err, &op) || op.Operation != "token_refresh" || op.Details.UpstreamStatus != 500 || op.SiteStatus != 502 {
		t.Fatal("refresh diagnostics lost", err)
	}
	status, _ := f.m.Status(t.Context())
	if !status.Connected {
		t.Fatal("transient failure disconnected account")
	}
}
