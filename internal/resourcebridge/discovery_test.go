package resourcebridge

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/claim"
	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

func TestDiscoverySuccessCrossesDispatchBeforeCredentialAndNetwork(t *testing.T) {
	fixture := newBridgeFixture(t)
	ownerID := fixture.seedUser(t, "success")
	key := fixture.seedEndpointKey(t, ownerID, "success", connectorcontract.TypeOpenAICompatible, "upstream-success-credential")
	var calls atomic.Int64
	discoverer := discovererFunc(func(ctx context.Context, input connectorcontract.DiscoveryInput) connectorcontract.DiscoveryResult {
		calls.Add(1)
		if ctx.Err() != nil {
			t.Fatalf("discoverer context unexpectedly canceled: %v", ctx.Err())
		}
		if input.Backend != fixture.backend {
			t.Fatal("discoverer received a different backend")
		}
		if input.Target.Type() != key.connector || input.Target.BaseURL() != key.baseURL || input.Target.UpstreamModel() != "" {
			t.Fatalf("unexpected target: type=%q base=%q model=%q", input.Target.Type(), input.Target.BaseURL(), input.Target.UpstreamModel())
		}
		var state string
		if err := fixture.store.DB().QueryRow(`SELECT state FROM dispatch_claims
WHERE endpoint_key_id=? ORDER BY rowid DESC LIMIT 1`, key.keyID).Scan(&state); err != nil {
			t.Fatalf("observe dispatch marker: %v", err)
		}
		if state != "dispatched" {
			t.Fatalf("network callback observed state %q, want dispatched", state)
		}
		plaintext, ciphertext, ok := input.Credential.Take()
		if !ok || string(plaintext) != "upstream-success-credential" || len(ciphertext) == 0 {
			clear(plaintext)
			clear(ciphertext)
			t.Fatal("discoverer did not receive the one transferable credential")
		}
		clear(plaintext)
		clear(ciphertext)
		if plaintext, ciphertext, ok := input.Credential.Take(); ok || plaintext != nil || ciphertext != nil {
			clear(plaintext)
			clear(ciphertext)
			t.Fatal("credential transferred more than once")
		}
		return connectorcontract.DiscoveryResult{
			Models: []connectorcontract.DiscoveredModel{
				{ID: "provider/model-a", Provider: "Provider"},
				{ID: "模型-b", Provider: "供应商"},
			},
			Failure: connectorcontract.DiscoveryFailureNone, UpstreamStatus: 200, ResponseReceived: true,
		}
	})
	result, err := fixture.runtime.Discover(context.Background(), fixture.discoveryInput(t, key, discoverer))
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if !result.Succeeded || result.FailureClass != "" || result.SafeDiagnostic != "" || len(result.Models) != 2 ||
		result.Models[0].UpstreamModelID != "provider/model-a" || result.Models[1].Provider != "供应商" {
		t.Fatalf("unexpected discovery result: %+v", result)
	}
	if calls.Load() != 1 {
		t.Fatalf("discoverer calls = %d, want 1", calls.Load())
	}
	facts := fixture.latestDiscoveryFacts(t)
	assertFixedDiscoveryRequest(t, facts)
	if facts.claimState != "committed" || !facts.resultKind.Valid || facts.resultKind.String != "response" ||
		!facts.upstreamStatus.Valid || facts.upstreamStatus.Int64 != 200 || !facts.usageUnknown.Valid || facts.usageUnknown.Int64 != 1 {
		t.Fatalf("unexpected attempt facts: %+v", facts)
	}
}

func TestDiscoverySuccessfulEmptySet(t *testing.T) {
	fixture := newBridgeFixture(t)
	ownerID := fixture.seedUser(t, "empty")
	key := fixture.seedEndpointKey(t, ownerID, "empty", connectorcontract.TypeAnthropicCompatible, "upstream-empty-credential")
	var calls atomic.Int64
	result, err := fixture.runtime.Discover(context.Background(), fixture.discoveryInput(t, key, discovererFunc(
		func(context.Context, connectorcontract.DiscoveryInput) connectorcontract.DiscoveryResult {
			calls.Add(1)
			return connectorcontract.DiscoveryResult{
				Models: []connectorcontract.DiscoveredModel{}, Failure: connectorcontract.DiscoveryFailureNone,
				UpstreamStatus: 204, ResponseReceived: true,
			}
		},
	)))
	if err != nil || !result.Succeeded || len(result.Models) != 0 || calls.Load() != 1 {
		t.Fatalf("empty discovery = (%+v, %v), calls=%d", result, err, calls.Load())
	}
	facts := fixture.latestDiscoveryFacts(t)
	assertFixedDiscoveryRequest(t, facts)
	if facts.resultKind.String != "response" || facts.upstreamStatus.Int64 != 204 {
		t.Fatalf("unexpected empty success facts: %+v", facts)
	}
}

func TestDiscoveryTypedFailureMatrix(t *testing.T) {
	fixture := newBridgeFixture(t)
	ownerID := fixture.seedUser(t, "failure-matrix")
	tests := []struct {
		name       string
		typed      connectorcontract.DiscoveryResult
		wantClass  resources.DiscoveryFailureClass
		wantKind   string
		wantStatus int64
	}{
		{name: "auth", typed: connectorcontract.DiscoveryResult{Failure: connectorcontract.DiscoveryFailureAuth, ResponseReceived: true, UpstreamStatus: 401, Diagnostic: "credential rejected"}, wantClass: resources.DiscoveryFailureAuth, wantKind: "response", wantStatus: 401},
		{name: "rate limit", typed: connectorcontract.DiscoveryResult{Failure: connectorcontract.DiscoveryFailureRateLimit, ResponseReceived: true, UpstreamStatus: 429, Diagnostic: "rate limited"}, wantClass: resources.DiscoveryFailureRateLimit, wantKind: "response", wantStatus: 429},
		{name: "timeout", typed: connectorcontract.DiscoveryResult{Failure: connectorcontract.DiscoveryFailureTimeout, Diagnostic: "request timed out"}, wantClass: resources.DiscoveryFailureTimeout, wantKind: "synthetic"},
		{name: "protocol on 2xx", typed: connectorcontract.DiscoveryResult{Failure: connectorcontract.DiscoveryFailureProtocol, ResponseReceived: true, UpstreamStatus: 200, Diagnostic: "invalid response shape"}, wantClass: resources.DiscoveryFailureProtocol, wantKind: "response", wantStatus: 200},
		{name: "transport", typed: connectorcontract.DiscoveryResult{Failure: connectorcontract.DiscoveryFailureTransport, Diagnostic: "transport unavailable"}, wantClass: resources.DiscoveryFailureTransport, wantKind: "synthetic"},
		{name: "interrupted", typed: connectorcontract.DiscoveryResult{Failure: connectorcontract.DiscoveryFailureInterrupted, Diagnostic: "request interrupted"}, wantClass: resources.DiscoveryFailureInterrupted, wantKind: "synthetic"},
	}
	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			key := fixture.seedEndpointKey(t, ownerID, fmt.Sprintf("failure-%d", index), connectorcontract.TypeOpenAICompatible, "failure-matrix-credential")
			var calls atomic.Int64
			result, err := fixture.runtime.Discover(context.Background(), fixture.discoveryInput(t, key, discovererFunc(
				func(context.Context, connectorcontract.DiscoveryInput) connectorcontract.DiscoveryResult {
					calls.Add(1)
					return test.typed
				},
			)))
			if err != nil || result.Succeeded || result.FailureClass != test.wantClass || result.SafeDiagnostic != test.typed.Diagnostic || calls.Load() != 1 {
				t.Fatalf("Discover = (%+v, %v), calls=%d", result, err, calls.Load())
			}
			facts := fixture.latestDiscoveryFacts(t)
			assertFixedDiscoveryRequest(t, facts)
			if facts.claimState != "committed" || !facts.resultKind.Valid || facts.resultKind.String != test.wantKind {
				t.Fatalf("unexpected failure attempt: %+v", facts)
			}
			if test.wantStatus == 0 {
				if facts.upstreamStatus.Valid {
					t.Fatalf("synthetic status = %+v, want NULL", facts.upstreamStatus)
				}
			} else if !facts.upstreamStatus.Valid || facts.upstreamStatus.Int64 != test.wantStatus {
				t.Fatalf("upstream status = %+v, want %d", facts.upstreamStatus, test.wantStatus)
			}
		})
	}
}

func TestDiscoveryInvalidTypedResultsFailClosed(t *testing.T) {
	fixture := newBridgeFixture(t)
	ownerID := fixture.seedUser(t, "invalid-results")
	tooMany := make([]connectorcontract.DiscoveredModel, maxDiscoveryModels+1)
	for index := range tooMany {
		tooMany[index] = connectorcontract.DiscoveredModel{ID: fmt.Sprintf("model-%d", index)}
	}
	tests := []struct {
		name  string
		typed connectorcontract.DiscoveryResult
	}{
		{name: "zero value", typed: connectorcontract.DiscoveryResult{}},
		{name: "unknown failure", typed: connectorcontract.DiscoveryResult{Failure: connectorcontract.DiscoveryFailureKind("future")}},
		{name: "response without status", typed: connectorcontract.DiscoveryResult{Failure: connectorcontract.DiscoveryFailureProtocol, ResponseReceived: true}},
		{name: "status without response", typed: connectorcontract.DiscoveryResult{Failure: connectorcontract.DiscoveryFailureProtocol, UpstreamStatus: 500}},
		{name: "none on failure status", typed: connectorcontract.DiscoveryResult{Failure: connectorcontract.DiscoveryFailureNone, ResponseReceived: true, UpstreamStatus: 404}},
		{name: "failure with models", typed: connectorcontract.DiscoveryResult{Failure: connectorcontract.DiscoveryFailureAuth, ResponseReceived: true, UpstreamStatus: 401, Models: []connectorcontract.DiscoveredModel{{ID: "must-not-survive"}}}},
		{name: "success with diagnostic", typed: connectorcontract.DiscoveryResult{Failure: connectorcontract.DiscoveryFailureNone, ResponseReceived: true, UpstreamStatus: 200, Diagnostic: "not empty"}},
		{name: "too many models", typed: connectorcontract.DiscoveryResult{Failure: connectorcontract.DiscoveryFailureNone, ResponseReceived: true, UpstreamStatus: 200, Models: tooMany}},
		{name: "leading model whitespace", typed: connectorcontract.DiscoveryResult{Failure: connectorcontract.DiscoveryFailureNone, ResponseReceived: true, UpstreamStatus: 200, Models: []connectorcontract.DiscoveredModel{{ID: " model"}}}},
		{name: "provider trailing whitespace", typed: connectorcontract.DiscoveryResult{Failure: connectorcontract.DiscoveryFailureNone, ResponseReceived: true, UpstreamStatus: 200, Models: []connectorcontract.DiscoveredModel{{ID: "model", Provider: "provider "}}}},
		{name: "model control", typed: connectorcontract.DiscoveryResult{Failure: connectorcontract.DiscoveryFailureNone, ResponseReceived: true, UpstreamStatus: 200, Models: []connectorcontract.DiscoveredModel{{ID: "model\n"}}}},
		{name: "oversized diagnostic", typed: connectorcontract.DiscoveryResult{Failure: connectorcontract.DiscoveryFailureProtocol, Diagnostic: strings.Repeat("x", 4097)}},
		{name: "diagnostic control", typed: connectorcontract.DiscoveryResult{Failure: connectorcontract.DiscoveryFailureProtocol, Diagnostic: "unsafe\nline"}},
	}
	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			key := fixture.seedEndpointKey(t, ownerID, fmt.Sprintf("invalid-%d", index), connectorcontract.TypeOpenAICompatible, "invalid-result-credential")
			var calls atomic.Int64
			result, err := fixture.runtime.Discover(context.Background(), fixture.discoveryInput(t, key, discovererFunc(
				func(context.Context, connectorcontract.DiscoveryInput) connectorcontract.DiscoveryResult {
					calls.Add(1)
					return test.typed
				},
			)))
			if err != nil || result.Succeeded || result.FailureClass != resources.DiscoveryFailureProtocol ||
				result.SafeDiagnostic != diagnosticInvalidResult || len(result.Models) != 0 || calls.Load() != 1 {
				t.Fatalf("fail-closed result = (%+v, %v), calls=%d", result, err, calls.Load())
			}
			facts := fixture.latestDiscoveryFacts(t)
			assertFixedDiscoveryRequest(t, facts)
			if facts.claimState != "committed" {
				t.Fatalf("invalid typed result claim = %+v", facts)
			}
		})
	}
}

func TestDiscoveryModelAndProviderScalarBoundariesPreserveExactText(t *testing.T) {
	fixture := newBridgeFixture(t)
	ownerID := fixture.seedUser(t, "scalar-boundaries")
	validModel := strings.Repeat("界", 512)
	validProvider := strings.Repeat("供", 128)
	key := fixture.seedEndpointKey(t, ownerID, "scalar-valid", connectorcontract.TypeOpenAICompatible, "scalar-boundary-credential")
	result, err := fixture.runtime.Discover(context.Background(), fixture.discoveryInput(t, key, discovererFunc(
		func(context.Context, connectorcontract.DiscoveryInput) connectorcontract.DiscoveryResult {
			return connectorcontract.DiscoveryResult{
				Failure: connectorcontract.DiscoveryFailureNone, ResponseReceived: true, UpstreamStatus: 200,
				Models: []connectorcontract.DiscoveredModel{
					{ID: validModel, Provider: validProvider},
					{ID: "e\u0301", Provider: ""},
				},
			}
		},
	)))
	if err != nil || !result.Succeeded || len(result.Models) != 2 ||
		result.Models[0].UpstreamModelID != validModel || result.Models[0].Provider != validProvider ||
		result.Models[1].UpstreamModelID != "e\u0301" || result.Models[1].Provider != "" {
		t.Fatalf("valid scalar boundary result = (%+v, %v)", result, err)
	}

	tests := []connectorcontract.DiscoveredModel{
		{ID: strings.Repeat("界", 513)},
		{ID: "model", Provider: strings.Repeat("供", 129)},
	}
	for index, model := range tests {
		key := fixture.seedEndpointKey(t, ownerID, fmt.Sprintf("scalar-invalid-%d", index), connectorcontract.TypeOpenAICompatible, "scalar-invalid-credential")
		result, err := fixture.runtime.Discover(context.Background(), fixture.discoveryInput(t, key, discovererFunc(
			func(context.Context, connectorcontract.DiscoveryInput) connectorcontract.DiscoveryResult {
				return connectorcontract.DiscoveryResult{
					Failure: connectorcontract.DiscoveryFailureNone, ResponseReceived: true, UpstreamStatus: 200,
					Models: []connectorcontract.DiscoveredModel{model},
				}
			},
		)))
		if err != nil || result.FailureClass != resources.DiscoveryFailureProtocol || result.SafeDiagnostic != diagnosticInvalidResult {
			t.Fatalf("invalid scalar boundary %d = (%+v, %v)", index, result, err)
		}
	}
}

func TestDiscoveryCanceledBeforeClaimCreatesNoState(t *testing.T) {
	fixture := newBridgeFixture(t)
	ownerID := fixture.seedUser(t, "preclaim-cancel")
	key := fixture.seedEndpointKey(t, ownerID, "preclaim-cancel", connectorcontract.TypeOpenAICompatible, "preclaim-cancel-credential")
	var calls atomic.Int64
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result, err := fixture.runtime.Discover(ctx, fixture.discoveryInput(t, key, discovererFunc(
		func(context.Context, connectorcontract.DiscoveryInput) connectorcontract.DiscoveryResult {
			calls.Add(1)
			return connectorcontract.DiscoveryResult{}
		},
	)))
	if !errors.Is(err, ErrInterrupted) || result.FailureClass != resources.DiscoveryFailureInterrupted || calls.Load() != 0 {
		t.Fatalf("preclaim cancellation = (%+v, %v), calls=%d", result, err, calls.Load())
	}
	var requests, claims int
	if err := fixture.store.DB().QueryRow(`SELECT
(SELECT COUNT(*) FROM logical_requests WHERE route_kind='model_discovery'),
(SELECT COUNT(*) FROM dispatch_claims WHERE purpose='discovery')`).Scan(&requests, &claims); err != nil {
		t.Fatalf("count preclaim state: %v", err)
	}
	if requests != 0 || claims != 0 {
		t.Fatalf("preclaim cancellation left requests/claims = %d/%d", requests, claims)
	}
}

func TestDiscoveryFailureBeforeDispatchReleasesClaim(t *testing.T) {
	fixture := newBridgeFixture(t)
	ownerID := fixture.seedUser(t, "release")
	key := fixture.seedEndpointKey(t, ownerID, "release", connectorcontract.TypeOpenAICompatible, "release-credential")
	if _, err := fixture.store.DB().Exec(`CREATE TEMP TRIGGER resourcebridge_fail_dispatch
BEFORE UPDATE OF state ON dispatch_claims WHEN NEW.state='dispatched'
BEGIN SELECT RAISE(ABORT,'injected dispatch failure with raw marker'); END`); err != nil {
		t.Fatalf("create dispatch trigger: %v", err)
	}
	t.Cleanup(func() { _, _ = fixture.store.DB().Exec(`DROP TRIGGER IF EXISTS resourcebridge_fail_dispatch`) })
	var calls atomic.Int64
	result, err := fixture.runtime.Discover(context.Background(), fixture.discoveryInput(t, key, discovererFunc(
		func(context.Context, connectorcontract.DiscoveryInput) connectorcontract.DiscoveryResult {
			calls.Add(1)
			return connectorcontract.DiscoveryResult{}
		},
	)))
	if err != nil || result.FailureClass != resources.DiscoveryFailureProtocol || calls.Load() != 0 {
		t.Fatalf("undispatched failure = (%+v, %v), calls=%d", result, err, calls.Load())
	}
	facts := fixture.latestDiscoveryFacts(t)
	assertFixedDiscoveryRequest(t, facts)
	if facts.claimState != "released" || facts.resultKind.Valid {
		t.Fatalf("undispatched failure facts: %+v", facts)
	}
}

func TestDiscoveryClaimedCompletionFailureRecoversWithoutNetwork(t *testing.T) {
	fixture := newBridgeFixture(t)
	ownerID := fixture.seedUser(t, "claimed-recovery")
	key := fixture.seedEndpointKey(t, ownerID, "claimed-recovery", connectorcontract.TypeOpenAICompatible, "claimed-recovery-credential")
	if _, err := fixture.store.DB().Exec(`CREATE TEMP TRIGGER resourcebridge_fail_dispatch_claimed
BEFORE UPDATE OF state ON dispatch_claims WHEN NEW.state IN ('dispatched','released')
BEGIN SELECT RAISE(ABORT,'injected claimed transition failure'); END`); err != nil {
		t.Fatalf("create claimed trigger: %v", err)
	}
	var calls atomic.Int64
	result, err := fixture.runtime.Discover(context.Background(), fixture.discoveryInput(t, key, discovererFunc(
		func(context.Context, connectorcontract.DiscoveryInput) connectorcontract.DiscoveryResult {
			calls.Add(1)
			return connectorcontract.DiscoveryResult{}
		},
	)))
	if !errors.Is(err, ErrUnavailable) || result.FailureClass != resources.DiscoveryFailureProtocol || calls.Load() != 0 {
		t.Fatalf("claimed transient = (%+v, %v), calls=%d", result, err, calls.Load())
	}
	if _, err := fixture.store.DB().Exec(`DROP TRIGGER resourcebridge_fail_dispatch_claimed`); err != nil {
		t.Fatalf("drop claimed trigger: %v", err)
	}
	report, err := fixture.claims.RecoverNonterminal(context.Background(), 100)
	if err != nil {
		t.Fatalf("RecoverNonterminal: %v", err)
	}
	if report.ReleasedClaims != 1 || report.CompletedRequests != 1 || calls.Load() != 0 {
		t.Fatalf("claimed recovery = %+v, calls=%d", report, calls.Load())
	}
	facts := fixture.latestDiscoveryFacts(t)
	assertFixedDiscoveryRequest(t, facts)
	if facts.claimState != "released" || facts.resultKind.Valid {
		t.Fatalf("claimed recovery facts: %+v", facts)
	}
}

func TestDiscoveryCancellationAfterDispatchCompletesConservatively(t *testing.T) {
	fixture := newBridgeFixture(t)
	ownerID := fixture.seedUser(t, "postdispatch-cancel")
	key := fixture.seedEndpointKey(t, ownerID, "postdispatch-cancel", connectorcontract.TypeOpenAICompatible, "postdispatch-cancel-credential")
	openStarted := make(chan struct{}, 1)
	openRelease := make(chan struct{})
	fixture.vault.mu.Lock()
	fixture.vault.openStarted = openStarted
	fixture.vault.openRelease = openRelease
	fixture.vault.mu.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var calls atomic.Int64
	type response struct {
		result resources.DiscoveryClaimResult
		err    error
	}
	done := make(chan response, 1)
	go func() {
		result, err := fixture.runtime.Discover(ctx, fixture.discoveryInput(t, key, discovererFunc(
			func(callbackContext context.Context, _ connectorcontract.DiscoveryInput) connectorcontract.DiscoveryResult {
				calls.Add(1)
				if callbackContext.Err() == nil {
					return connectorcontract.DiscoveryResult{Failure: connectorcontract.DiscoveryFailureProtocol, Diagnostic: "context was not canceled"}
				}
				return connectorcontract.DiscoveryResult{Failure: connectorcontract.DiscoveryFailureNone, ResponseReceived: true, UpstreamStatus: 200}
			},
		)))
		done <- response{result: result, err: err}
	}()
	select {
	case <-openStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("credential open did not start after dispatch")
	}
	var state string
	if err := fixture.store.DB().QueryRow(`SELECT state FROM dispatch_claims WHERE purpose='discovery'`).Scan(&state); err != nil {
		t.Fatalf("read dispatch state: %v", err)
	}
	if state != "dispatched" {
		t.Fatalf("state before cancellation = %q, want dispatched", state)
	}
	cancel()
	close(openRelease)
	select {
	case completed := <-done:
		if completed.err != nil || completed.result.FailureClass != resources.DiscoveryFailureInterrupted || calls.Load() != 1 {
			t.Fatalf("postdispatch cancellation = (%+v, %v), calls=%d", completed.result, completed.err, calls.Load())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("postdispatch cancellation did not finish")
	}
	facts := fixture.latestDiscoveryFacts(t)
	assertFixedDiscoveryRequest(t, facts)
	if facts.claimState != "committed" || facts.resultKind.String != "response" || facts.upstreamStatus.Int64 != 200 ||
		!facts.diagnostic.Valid || facts.diagnostic.String != diagnosticDiscoveryInterrupted {
		t.Fatalf("postdispatch cancellation facts: %+v", facts)
	}
}

func TestDiscoveryCredentialUnavailableAndConnectorPanic(t *testing.T) {
	t.Run("credential unavailable", func(t *testing.T) {
		fixture := newBridgeFixture(t)
		ownerID := fixture.seedUser(t, "credential-failure")
		key := fixture.seedEndpointKey(t, ownerID, "credential-failure", connectorcontract.TypeOpenAICompatible, "credential-failure-secret")
		fixture.vault.setOpenFailure(true, "raw ciphertext and credential failure marker")
		var calls atomic.Int64
		result, err := fixture.runtime.Discover(context.Background(), fixture.discoveryInput(t, key, discovererFunc(
			func(context.Context, connectorcontract.DiscoveryInput) connectorcontract.DiscoveryResult {
				calls.Add(1)
				return connectorcontract.DiscoveryResult{}
			},
		)))
		if err != nil || result.FailureClass != resources.DiscoveryFailureProtocol ||
			result.SafeDiagnostic != diagnosticCredentialFailure || calls.Load() != 0 {
			t.Fatalf("credential failure = (%+v, %v), calls=%d", result, err, calls.Load())
		}
		facts := fixture.latestDiscoveryFacts(t)
		assertFixedDiscoveryRequest(t, facts)
		if facts.claimState != "committed" || facts.resultKind.String != "synthetic" || facts.upstreamStatus.Valid {
			t.Fatalf("credential failure facts: %+v", facts)
		}
	})

	t.Run("connector panic", func(t *testing.T) {
		fixture := newBridgeFixture(t)
		ownerID := fixture.seedUser(t, "panic")
		key := fixture.seedEndpointKey(t, ownerID, "panic", connectorcontract.TypeOpenAICompatible, "panic-secret-marker")
		var calls atomic.Int64
		result, err := fixture.runtime.Discover(context.Background(), fixture.discoveryInput(t, key, discovererFunc(
			func(context.Context, connectorcontract.DiscoveryInput) connectorcontract.DiscoveryResult {
				calls.Add(1)
				panic("raw secret/base/model panic marker")
			},
		)))
		if err != nil || result.FailureClass != resources.DiscoveryFailureProtocol ||
			result.SafeDiagnostic != diagnosticConnectorPanic || calls.Load() != 1 {
			t.Fatalf("panic result = (%+v, %v), calls=%d", result, err, calls.Load())
		}
		facts := fixture.latestDiscoveryFacts(t)
		assertFixedDiscoveryRequest(t, facts)
		if facts.claimState != "committed" || facts.resultKind.String != "synthetic" {
			t.Fatalf("panic facts: %+v", facts)
		}
	})
}

func TestDiscoveryAttemptCompletionFailureRecoversWithoutSecondNetwork(t *testing.T) {
	fixture := newBridgeFixture(t)
	ownerID := fixture.seedUser(t, "attempt-recovery")
	key := fixture.seedEndpointKey(t, ownerID, "attempt-recovery", connectorcontract.TypeOpenAICompatible, "attempt-recovery-credential")
	if _, err := fixture.store.DB().Exec(`CREATE TEMP TRIGGER resourcebridge_fail_attempt
BEFORE INSERT ON request_attempts
BEGIN SELECT RAISE(ABORT,'injected attempt completion failure'); END`); err != nil {
		t.Fatalf("create attempt trigger: %v", err)
	}
	var calls atomic.Int64
	result, err := fixture.runtime.Discover(context.Background(), fixture.discoveryInput(t, key, discovererFunc(
		func(context.Context, connectorcontract.DiscoveryInput) connectorcontract.DiscoveryResult {
			calls.Add(1)
			return connectorcontract.DiscoveryResult{Failure: connectorcontract.DiscoveryFailureNone, ResponseReceived: true, UpstreamStatus: 200}
		},
	)))
	if !errors.Is(err, ErrUnavailable) || !result.Succeeded || calls.Load() != 1 {
		t.Fatalf("attempt transient result = (%+v, %v), calls=%d", result, err, calls.Load())
	}
	if _, err := fixture.store.DB().Exec(`DROP TRIGGER resourcebridge_fail_attempt`); err != nil {
		t.Fatalf("drop attempt trigger: %v", err)
	}
	report, err := fixture.claims.RecoverNonterminal(context.Background(), 100)
	if err != nil {
		t.Fatalf("RecoverNonterminal: %v", err)
	}
	if report.CommittedClaims != 1 || report.CompletedRequests != 1 || calls.Load() != 1 {
		t.Fatalf("recovery report = %+v, calls=%d", report, calls.Load())
	}
	facts := fixture.latestDiscoveryFacts(t)
	assertFixedDiscoveryRequest(t, facts)
	if facts.claimState != "committed" || facts.resultKind.String != "synthetic" || facts.upstreamStatus.Int64 != 502 {
		t.Fatalf("recovered attempt facts: %+v", facts)
	}
}

func TestDiscoveryRequestCompletionFailureRecoversAndPreservesAttempt(t *testing.T) {
	fixture := newBridgeFixture(t)
	ownerID := fixture.seedUser(t, "request-recovery")
	key := fixture.seedEndpointKey(t, ownerID, "request-recovery", connectorcontract.TypeOpenAICompatible, "request-recovery-credential")
	if _, err := fixture.store.DB().Exec(`CREATE TEMP TRIGGER resourcebridge_fail_request
BEFORE UPDATE OF state ON logical_requests WHEN NEW.state='terminal'
BEGIN SELECT RAISE(ABORT,'injected request completion failure'); END`); err != nil {
		t.Fatalf("create request trigger: %v", err)
	}
	var calls atomic.Int64
	result, err := fixture.runtime.Discover(context.Background(), fixture.discoveryInput(t, key, discovererFunc(
		func(context.Context, connectorcontract.DiscoveryInput) connectorcontract.DiscoveryResult {
			calls.Add(1)
			return connectorcontract.DiscoveryResult{Failure: connectorcontract.DiscoveryFailureNone, ResponseReceived: true, UpstreamStatus: 206}
		},
	)))
	if !errors.Is(err, ErrUnavailable) || !result.Succeeded || calls.Load() != 1 {
		t.Fatalf("request transient result = (%+v, %v), calls=%d", result, err, calls.Load())
	}
	if _, err := fixture.store.DB().Exec(`DROP TRIGGER resourcebridge_fail_request`); err != nil {
		t.Fatalf("drop request trigger: %v", err)
	}
	report, err := fixture.claims.RecoverNonterminal(context.Background(), 100)
	if err != nil {
		t.Fatalf("RecoverNonterminal: %v", err)
	}
	if report.CommittedClaims != 0 || report.CompletedRequests != 1 || calls.Load() != 1 {
		t.Fatalf("recovery report = %+v, calls=%d", report, calls.Load())
	}
	facts := fixture.latestDiscoveryFacts(t)
	assertFixedDiscoveryRequest(t, facts)
	if facts.claimState != "committed" || facts.resultKind.String != "response" || facts.upstreamStatus.Int64 != 206 {
		t.Fatalf("recovered request facts: %+v", facts)
	}
	second, err := fixture.claims.RecoverNonterminal(context.Background(), 100)
	if err != nil {
		t.Fatalf("second RecoverNonterminal: %v", err)
	}
	if second.ReleasedClaims != 0 || second.CommittedClaims != 0 || second.CompletedRequests != 0 || calls.Load() != 1 {
		t.Fatalf("second recovery = %+v, calls=%d", second, calls.Load())
	}
}

func TestDiscoveryTerminalHelpersAreIdempotent(t *testing.T) {
	t.Run("dispatched", func(t *testing.T) {
		fixture := newBridgeFixture(t)
		ownerID := fixture.seedUser(t, "idempotent-dispatched")
		key := fixture.seedEndpointKey(t, ownerID, "idempotent-dispatched", connectorcontract.TypeOpenAICompatible, "idempotent-dispatched-credential")
		request, handle, err := fixture.claims.ClaimDiscovery(context.Background(), claim.DiscoveryClaimInput{
			ActorUserID: ownerID,
			Candidate:   claim.Candidate{EndpointID: key.endpointID, EndpointKeyID: key.keyID, ConnectorType: key.connector, CanonicalBaseURL: key.baseURL},
		})
		if err != nil {
			t.Fatalf("ClaimDiscovery: %v", err)
		}
		dispatch, err := fixture.claims.TakeForDispatch(context.Background(), handle)
		if err != nil {
			t.Fatalf("TakeForDispatch: %v", err)
		}
		dispatch.Clear()
		normalized := normalizeDiscoveryResult(context.Background(), connectorcontract.DiscoveryResult{
			Failure: connectorcontract.DiscoveryFailureNone, ResponseReceived: true, UpstreamStatus: 200,
		})
		for attempt := 0; attempt < 2; attempt++ {
			if err := fixture.runtime.finishDispatched(context.Background(), request.ID, handle, normalized); err != nil {
				t.Fatalf("finishDispatched %d: %v", attempt, err)
			}
		}
		var count int
		if err := fixture.store.DB().QueryRow(`SELECT COUNT(*) FROM request_attempts`).Scan(&count); err != nil {
			t.Fatalf("count attempts: %v", err)
		}
		if count != 1 {
			t.Fatalf("idempotent attempt count = %d, want 1", count)
		}
		assertFixedDiscoveryRequest(t, fixture.latestDiscoveryFacts(t))
	})

	t.Run("undispatched", func(t *testing.T) {
		fixture := newBridgeFixture(t)
		ownerID := fixture.seedUser(t, "idempotent-undispatched")
		key := fixture.seedEndpointKey(t, ownerID, "idempotent-undispatched", connectorcontract.TypeOpenAICompatible, "idempotent-undispatched-credential")
		request, handle, err := fixture.claims.ClaimDiscovery(context.Background(), claim.DiscoveryClaimInput{
			ActorUserID: ownerID,
			Candidate:   claim.Candidate{EndpointID: key.endpointID, EndpointKeyID: key.keyID, ConnectorType: key.connector, CanonicalBaseURL: key.baseURL},
		})
		if err != nil {
			t.Fatalf("ClaimDiscovery: %v", err)
		}
		failure := protocolResult("idempotent undispatched failure")
		for attempt := 0; attempt < 2; attempt++ {
			if err := fixture.runtime.finishWithoutDispatch(context.Background(), request.ID, handle, failure); err != nil {
				t.Fatalf("finishWithoutDispatch %d: %v", attempt, err)
			}
		}
		facts := fixture.latestDiscoveryFacts(t)
		assertFixedDiscoveryRequest(t, facts)
		if facts.claimState != "released" || facts.resultKind.Valid {
			t.Fatalf("idempotent undispatched facts: %+v", facts)
		}
	})
}
