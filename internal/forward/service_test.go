package forward

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/charityrouting"
	"github.com/waiting-here/NonbiriAPI/internal/claim"
	"github.com/waiting-here/NonbiriAPI/internal/connector"
	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/debug"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
	"github.com/waiting-here/NonbiriAPI/internal/maintenance"
	"github.com/waiting-here/NonbiriAPI/internal/routing"
)

func TestDryCaptureStopsBeforeSnapshotClaimAndConnector(t *testing.T) {
	capture := &fakeDebugCapture{decision: debug.CaptureDecision{Active: true, Mode: debug.ModeDry, Language: "en"}}
	fixture := newServiceFixture(t, capture)
	request := decodeChatForTest(t, `{"model":"provider/model","messages":[{"role":"user","content":"hello"}],"stream":true}`)
	recorder := httptest.NewRecorder()

	fixture.service.Chat(context.Background(), recorder, 1, request, []byte(`{"model":"provider/model"}`), "application/json", "en")

	if recorder.Code != http.StatusUnprocessableEntity || !strings.Contains(recorder.Body.String(), httperr.CodeDebugDryRunIntercepted) {
		t.Fatalf("dry response=%d %q", recorder.Code, recorder.Body.String())
	}
	if fixture.personal.preCalls != 1 || fixture.personal.snapCalls != 0 || fixture.charity.snapCalls != 0 {
		t.Fatalf("routing calls personal=(%d,%d) charity snapshot=%d", fixture.personal.preCalls, fixture.personal.snapCalls, fixture.charity.snapCalls)
	}
	if len(fixture.claims.events) != 0 || fixture.openAI.calls != 0 || fixture.charges.calls != 0 {
		t.Fatalf("dry reached execution: claims=%v connector=%d charge=%d", fixture.claims.events, fixture.openAI.calls, fixture.charges.calls)
	}
	if got := recorder.Header().Get("X-Nonbiri-Debug-Mode"); got != "dry-run" {
		t.Fatalf("X-Nonbiri-Debug-Mode=%q", got)
	}
}

func TestCharityDryCaptureUsesCandidateFreePolicyOnly(t *testing.T) {
	capture := &fakeDebugCapture{decision: debug.CaptureDecision{Active: true, Mode: debug.ModeDry, Language: "en"}}
	fixture := newServiceFixture(t, capture)
	request := decodeChatForTest(t, `{"model":"[公益]care/model","messages":[{"role":"user","content":"hello"}]}`)
	recorder := httptest.NewRecorder()

	fixture.service.Chat(context.Background(), recorder, 1, request, []byte(`{}`), "application/json", "en")

	if recorder.Code != http.StatusUnprocessableEntity || !strings.Contains(recorder.Body.String(), httperr.CodeDebugDryRunIntercepted) {
		t.Fatalf("dry response=%d %q", recorder.Code, recorder.Body.String())
	}
	if fixture.charity.preCalls != 1 || fixture.charity.snapCalls != 0 || fixture.personal.preCalls != 0 ||
		len(fixture.claims.events) != 0 || fixture.openAI.calls != 0 || fixture.charges.calls != 0 {
		t.Fatalf("charity Dry crossed candidate boundary: charity=(%d,%d) personal=%d claims=%v connector=%d charge=%d",
			fixture.charity.preCalls, fixture.charity.snapCalls, fixture.personal.preCalls,
			fixture.claims.events, fixture.openAI.calls, fixture.charges.calls)
	}
}

func TestDispatchMarkerPrecedesCredentialAndConnectorAndTerminalizes(t *testing.T) {
	fixture := newServiceFixture(t, nil)
	candidate := fixture.personal.snapshot.Candidates[0]
	credential := fixture.addDispatch(candidate)
	fixture.openAI.events = &fixture.claims.events
	fixture.openAI.results = []connectorcontract.AttemptResult{{
		Success: true, Committed: true, Failure: connectorcontract.FailureNone,
		UpstreamStatus: http.StatusOK, ClientStatus: http.StatusOK,
		Usage: connectorcontract.Usage{Present: true, UncachedInputTokens: 2, OutputTokens: 3},
	}}
	fixture.openAI.bodies = [][]byte{[]byte(`{"id":"chatcmpl_test","object":"chat.completion"}`)}
	request := decodeChatForTest(t, `{"model":"provider/model","messages":[{"role":"user","content":"hello"}]}`)
	recorder := httptest.NewRecorder()

	fixture.service.Chat(context.Background(), recorder, 1, request, []byte(`{"model":"provider/model"}`), "application/json", "en")

	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "chatcmpl_test") {
		t.Fatalf("success response=%d %q", recorder.Code, recorder.Body.String())
	}
	requireEvents(t, fixture.claims.events, "accept", "claim", "dispatch", "connector", "attempt_terminal", "request_terminal")
	if len(fixture.claims.accepts) != 1 || fixture.claims.accepts[0].CharityDecisionNow != nil {
		t.Fatalf("personal acceptance carried charity decision time: %+v", fixture.claims.accepts)
	}
	if _, _, ok := credential.Take(); ok {
		t.Fatal("credential remained usable after connector return")
	}
	if len(fixture.claims.outcomes) != 1 {
		t.Fatalf("attempt outcomes=%d", len(fixture.claims.outcomes))
	}
	outcome := fixture.claims.outcomes[0]
	if !outcome.ProtocolSuccess || !outcome.ResponseStarted || !outcome.Usage.Present {
		t.Fatalf("outcome=%+v", outcome)
	}
	terminal := fixture.claims.requestResults[0]
	if terminal.Disposition != claim.AccountingCommit || terminal.Caller.Class != claim.ResultSuccess || terminal.Caller.Status != http.StatusOK {
		t.Fatalf("request terminal=%+v", terminal)
	}
}

func TestAttemptUsesOriginScopedV3SafetyIdentifier(t *testing.T) {
	fixture := newServiceFixture(t, nil)
	candidate := fixture.personal.snapshot.Candidates[0]
	fixture.addDispatch(candidate)
	fixture.openAI.results = []connectorcontract.AttemptResult{{
		Success: true, Committed: true, Failure: connectorcontract.FailureNone,
		UpstreamStatus: http.StatusOK, ClientStatus: http.StatusOK,
	}}
	request := decodeChatForTest(t, `{"model":"provider/model","messages":[],"safety_identifier":"caller-controlled"}`)
	recorder := httptest.NewRecorder()

	fixture.service.Chat(context.Background(), recorder, 1, request, []byte(`{}`), "application/json", "en")

	if len(fixture.openAI.policies) != 1 || len(fixture.openAI.targets) != 1 {
		t.Fatalf("connector policies=%d targets=%d", len(fixture.openAI.policies), len(fixture.openAI.targets))
	}
	origin, err := canonicalOrigin(fixture.openAI.targets[0].BaseURL())
	if err != nil {
		t.Fatal(err)
	}
	want, err := fixture.safety.Generate(1, origin)
	if err != nil {
		t.Fatal(err)
	}
	if got := fixture.openAI.policies[0].SafetyIdentifier; got != want || got == "caller-controlled" || !strings.HasPrefix(got, safetyIdentifierPrefix) {
		t.Fatalf("connector safety identifier=%q want=%q", got, want)
	}
}

func TestUndispatchedFailureReleasesClaimAndRequestReservation(t *testing.T) {
	fixture := newServiceFixture(t, nil)
	fixture.claims.takeErrors = map[int]error{0: claim.ErrCredentialUnavailable}
	request := decodeChatForTest(t, `{"model":"provider/model","messages":[]}`)
	recorder := httptest.NewRecorder()

	fixture.service.Chat(context.Background(), recorder, 1, request, []byte(`{}`), "application/json", "en")

	if recorder.Code != http.StatusInternalServerError || !strings.Contains(recorder.Body.String(), httperr.CodeInternal) {
		t.Fatalf("failure response=%d %q", recorder.Code, recorder.Body.String())
	}
	requireEvents(t, fixture.claims.events, "accept", "claim", "dispatch", "release", "request_terminal")
	if fixture.openAI.calls != 0 || len(fixture.claims.outcomes) != 0 {
		t.Fatalf("undispatched path connector=%d outcomes=%d", fixture.openAI.calls, len(fixture.claims.outcomes))
	}
	terminal := fixture.claims.requestResults[0]
	if terminal.Disposition != claim.AccountingRelease || terminal.ActualChargeMilli != 0 {
		t.Fatalf("request terminal=%+v", terminal)
	}
}

func TestUndispatchedReleaseFailureLeavesRequestForRecovery(t *testing.T) {
	fixture := newServiceFixture(t, nil)
	fixture.claims.takeErrors = map[int]error{0: claim.ErrCredentialUnavailable}
	fixture.claims.releaseErrors = map[int]error{0: errors.New("release unavailable")}
	request := decodeChatForTest(t, `{"model":"provider/model","messages":[]}`)
	recorder := httptest.NewRecorder()

	fixture.service.Chat(context.Background(), recorder, 1, request, []byte(`{}`), "application/json", "en")

	if recorder.Code != http.StatusInternalServerError || !strings.Contains(recorder.Body.String(), httperr.CodeInternal) {
		t.Fatalf("failure response=%d %q", recorder.Code, recorder.Body.String())
	}
	requireEvents(t, fixture.claims.events, "accept", "claim", "dispatch", "release")
	if len(fixture.claims.requestResults) != 0 || len(fixture.claims.outcomes) != 0 || fixture.openAI.calls != 0 {
		t.Fatalf("failed release crossed recovery boundary: requests=%d outcomes=%d connector=%d",
			len(fixture.claims.requestResults), len(fixture.claims.outcomes), fixture.openAI.calls)
	}
}

func TestSilentRetryStaysInsideFrozenCandidates(t *testing.T) {
	fixture := newServiceFixture(t, nil)
	fixture.personal.preflight.SilentRetry = true
	fixture.personal.snapshot.PersonalPreflight.SilentRetry = true
	second := fixture.personal.snapshot.Candidates[0]
	second.EndpointID, second.EndpointKeyID, second.UpstreamModelID = 31, 32, "upstream-second"
	fixture.personal.snapshot.Candidates = append(fixture.personal.snapshot.Candidates, second)
	fixture.addDispatch(fixture.personal.snapshot.Candidates[0])
	fixture.addDispatch(second)
	fixture.openAI.events = &fixture.claims.events
	fixture.openAI.results = []connectorcontract.AttemptResult{
		{Failure: connectorcontract.FailureUpstream, UpstreamStatus: http.StatusServiceUnavailable, Diagnostic: "upstream returned HTTP 503"},
		{Success: true, Committed: true, Failure: connectorcontract.FailureNone, UpstreamStatus: http.StatusOK, ClientStatus: http.StatusOK},
	}
	fixture.openAI.bodies = [][]byte{nil, []byte(`{"id":"retry-success"}`)}
	request := decodeChatForTest(t, `{"model":"provider/model","messages":[]}`)
	recorder := httptest.NewRecorder()

	fixture.service.Chat(context.Background(), recorder, 1, request, []byte(`{}`), "application/json", "en")

	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "retry-success") {
		t.Fatalf("retry response=%d %q", recorder.Code, recorder.Body.String())
	}
	requireEvents(t, fixture.claims.events,
		"accept", "claim", "dispatch", "connector", "attempt_terminal",
		"claim", "dispatch", "connector", "attempt_terminal", "request_terminal")
	if len(fixture.claims.claims) != 2 || fixture.claims.claims[0].AttemptSeq != 1 || fixture.claims.claims[1].AttemptSeq != 2 {
		t.Fatalf("claims=%+v", fixture.claims.claims)
	}
	if fixture.claims.accepts[0].AttemptLimit != 2 {
		t.Fatalf("attempt limit=%d", fixture.claims.accepts[0].AttemptLimit)
	}
}

func TestCapabilityFilterPrecedesAcceptanceAndKeepsOnlySupportedCandidates(t *testing.T) {
	fixture := newServiceFixture(t, nil)
	anthropic := fixture.personal.snapshot.Candidates[0]
	anthropic.EndpointID, anthropic.EndpointKeyID = 51, 52
	anthropic.ConnectorType = connectorcontract.TypeAnthropicCompatible
	openAI := fixture.personal.snapshot.Candidates[0]
	openAI.EndpointID, openAI.EndpointKeyID = 53, 54
	fixture.personal.snapshot.Candidates = []RouteCandidate{anthropic, openAI}
	fixture.addDispatch(openAI)
	fixture.openAI.results = []connectorcontract.AttemptResult{{
		Success: true, Committed: true, Failure: connectorcontract.FailureNone,
		UpstreamStatus: http.StatusOK, ClientStatus: http.StatusOK,
	}}
	request := decodeChatForTest(t, `{"model":"provider/model","messages":[{"role":"user","content":"hi"}],"n":1}`)
	recorder := httptest.NewRecorder()

	fixture.service.Chat(context.Background(), recorder, 1, request, []byte(`{}`), "application/json", "en")

	if fixture.claims.accepts[0].AttemptLimit != 1 || len(fixture.claims.claims) != 1 ||
		fixture.claims.claims[0].Candidate.ConnectorType != connectorcontract.TypeOpenAICompatible ||
		fixture.openAI.calls != 1 || fixture.anthropic.calls != 0 {
		t.Fatalf("capability rail accept=%+v claims=%+v openai=%d anthropic=%d",
			fixture.claims.accepts, fixture.claims.claims, fixture.openAI.calls, fixture.anthropic.calls)
	}
}

func TestSelfUpstream4xxPreservesStatusAndSafeDiagnostic(t *testing.T) {
	fixture := newServiceFixture(t, nil)
	fixture.addDispatch(fixture.personal.snapshot.Candidates[0])
	fixture.openAI.results = []connectorcontract.AttemptResult{{
		Failure: connectorcontract.FailureUpstream, UpstreamStatus: http.StatusTeapot,
		Diagnostic: "upstream returned HTTP 418",
	}}
	request := decodeChatForTest(t, `{"model":"provider/model","messages":[]}`)
	recorder := httptest.NewRecorder()

	fixture.service.Chat(context.Background(), recorder, 1, request, []byte(`{}`), "application/json", "en")

	if recorder.Code != http.StatusTeapot {
		t.Fatalf("status=%d body=%q", recorder.Code, recorder.Body.String())
	}
	var envelope httperr.Envelope
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if envelope.Error.Code != httperr.CodeUpstream || envelope.Error.Source != httperr.SourceUpstream || envelope.Error.Diag != "upstream returned HTTP 418" {
		t.Fatalf("error=%+v", envelope.Error)
	}
	terminal := fixture.claims.requestResults[0]
	if terminal.Caller.Status != http.StatusTeapot || terminal.Caller.ErrorCode != httperr.CodeUpstream {
		t.Fatalf("terminal=%+v", terminal)
	}
}

func TestCharityUsesCharityPurposeAndSafe502AfterDispatch(t *testing.T) {
	fixture := newServiceFixture(t, nil)
	fixture.charges.charge = 7
	fixture.addDispatch(fixture.charity.snapshot.Candidates[0])
	fixture.openAI.results = []connectorcontract.AttemptResult{{
		Failure: connectorcontract.FailureUpstream, UpstreamStatus: http.StatusNotFound,
		Diagnostic: "upstream returned HTTP 404",
	}}
	request := decodeChatForTest(t, `{"model":"[公益]care/model","messages":[{"role":"user","content":"enough"}]}`)
	recorder := httptest.NewRecorder()

	fixture.service.Chat(context.Background(), recorder, 1, request, []byte(`{}`), "application/json", "en")

	if recorder.Code != http.StatusBadGateway {
		t.Fatalf("status=%d body=%q", recorder.Code, recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), "HTTP 404") || strings.Contains(recorder.Body.String(), "diag") {
		t.Fatalf("charity leaked diagnostic: %q", recorder.Body.String())
	}
	if fixture.claims.accepts[0].Route != claim.RouteCharityChat || fixture.claims.accepts[0].CharityModelID != 8 ||
		fixture.claims.claims[0].Purpose != claim.PurposeCharity || fixture.claims.claims[0].DonationKeyID != 23 {
		t.Fatalf("charity rail accept=%+v claim=%+v", fixture.claims.accepts[0], fixture.claims.claims[0])
	}
	terminal := fixture.claims.requestResults[0]
	if terminal.Disposition != claim.AccountingCommit || terminal.ActualChargeMilli != 7 || terminal.Caller.Status != http.StatusBadGateway {
		t.Fatalf("terminal=%+v", terminal)
	}
}

func TestCommittedFailedStreamGetsNoSecondEnvelopeAndUsageUnknown(t *testing.T) {
	fixture := newServiceFixture(t, nil)
	fixture.addDispatch(fixture.personal.snapshot.Candidates[0])
	fixture.openAI.results = []connectorcontract.AttemptResult{{
		Committed: true, Failure: connectorcontract.FailureUpstream, UpstreamStatus: http.StatusOK,
		ClientStatus: http.StatusOK, Diagnostic: "upstream stream ended before terminal marker",
	}}
	partial := "data: {\"id\":\"partial\"}\n\n"
	fixture.openAI.bodies = [][]byte{[]byte(partial)}
	request := decodeChatForTest(t, `{"model":"provider/model","messages":[],"stream":true}`)
	recorder := httptest.NewRecorder()

	fixture.service.Chat(context.Background(), recorder, 1, request, []byte(`{}`), "application/json", "en")

	if recorder.Body.String() != partial || strings.Contains(recorder.Body.String(), `"error"`) || strings.Contains(recorder.Body.String(), "[DONE]") {
		t.Fatalf("committed stream body=%q", recorder.Body.String())
	}
	outcome := fixture.claims.outcomes[0]
	if outcome.ProtocolSuccess || !outcome.ResponseStarted || outcome.Usage.Present {
		t.Fatalf("outcome=%+v", outcome)
	}
	terminal := fixture.claims.requestResults[0]
	if terminal.Caller.Class != claim.ResultFailed || terminal.Caller.Status != http.StatusBadGateway || terminal.Caller.ErrorCode != httperr.CodeUpstream {
		t.Fatalf("terminal=%+v", terminal)
	}
}

func TestCommittedFailureNeverRetriesFrozenSecondCandidate(t *testing.T) {
	fixture := newServiceFixture(t, nil)
	fixture.personal.preflight.SilentRetry = true
	fixture.personal.snapshot.PersonalPreflight.SilentRetry = true
	second := fixture.personal.snapshot.Candidates[0]
	second.EndpointID, second.EndpointKeyID, second.UpstreamModelID = 71, 72, "upstream-second"
	fixture.personal.snapshot.Candidates = append(fixture.personal.snapshot.Candidates, second)
	fixture.addDispatch(fixture.personal.snapshot.Candidates[0])
	fixture.addDispatch(second)
	fixture.openAI.results = []connectorcontract.AttemptResult{
		{Committed: true, Failure: connectorcontract.FailureUpstream, UpstreamStatus: http.StatusOK, ClientStatus: http.StatusOK,
			Diagnostic: "upstream stream ended before terminal marker"},
		{Success: true, Committed: true, Failure: connectorcontract.FailureNone, UpstreamStatus: http.StatusOK, ClientStatus: http.StatusOK},
	}
	fixture.openAI.bodies = [][]byte{[]byte("data: partial\n\n"), []byte("must-not-run")}
	request := decodeChatForTest(t, `{"model":"provider/model","messages":[],"stream":true}`)
	recorder := httptest.NewRecorder()

	fixture.service.Chat(context.Background(), recorder, 1, request, []byte(`{}`), "application/json", "en")

	if fixture.openAI.calls != 1 || len(fixture.claims.claims) != 1 || strings.Contains(recorder.Body.String(), "must-not-run") {
		t.Fatalf("committed failure retried: connector=%d claims=%d body=%q", fixture.openAI.calls, len(fixture.claims.claims), recorder.Body.String())
	}
}

func TestCallerCancellationCancelsAttemptSettlesAndClearsCredential(t *testing.T) {
	for _, liveDebug := range []bool{false, true} {
		name := "ordinary"
		var capture DebugCapture
		if liveDebug {
			name = "debug live"
			hub, err := debug.NewHub(activeIdentityVerifier{})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = hub.Close() })
			metadata, _, err := hub.Start(1, "browser-binding")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := hub.ChangeMode(1, metadata.Revision, debug.ModeLive, true); err != nil {
				t.Fatal(err)
			}
			capture = hub
		}
		t.Run(name, func(t *testing.T) {
			fixture := newServiceFixture(t, capture)
			credential := fixture.addDispatch(fixture.personal.snapshot.Candidates[0])
			entered := make(chan struct{})
			fixture.openAI.attempt = func(ctx context.Context, _ connector.AttemptInput) connectorcontract.AttemptResult {
				close(entered)
				<-ctx.Done()
				return connectorcontract.AttemptResult{Failure: connectorcontract.FailureCanceled, Diagnostic: "request canceled"}
			}
			request := decodeChatForTest(t, `{"model":"provider/model","messages":[],"stream":true}`)
			recorder := httptest.NewRecorder()
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan struct{})
			go func() {
				defer close(done)
				fixture.service.Chat(ctx, recorder, 1, request, []byte(`{}`), "application/json", "en")
			}()
			select {
			case <-entered:
			case <-time.After(2 * time.Second):
				t.Fatal("connector was not entered")
			}
			cancel()
			select {
			case <-done:
			case <-time.After(2 * time.Second):
				t.Fatal("cancelled request did not finish")
			}
			if recorder.Body.Len() != 0 {
				t.Fatalf("cancelled caller received body %q", recorder.Body.String())
			}
			if len(fixture.claims.outcomes) != 1 || len(fixture.claims.requestResults) != 1 {
				t.Fatalf("terminal records outcomes=%d requests=%d", len(fixture.claims.outcomes), len(fixture.claims.requestResults))
			}
			terminal := fixture.claims.requestResults[0]
			if terminal.Caller.Class != claim.ResultCancelled || terminal.Disposition != claim.AccountingCommit {
				t.Fatalf("cancel terminal=%+v", terminal)
			}
			if _, _, ok := credential.Take(); ok {
				t.Fatal("cancelled attempt retained credential material")
			}
		})
	}
}

func TestAttemptTerminalizationFailureLeavesRequestForRecovery(t *testing.T) {
	fixture := newServiceFixture(t, nil)
	credential := fixture.addDispatch(fixture.personal.snapshot.Candidates[0])
	fixture.claims.attemptErrors = map[int]error{0: errors.New("attempt terminal unavailable")}
	fixture.openAI.results = []connectorcontract.AttemptResult{{
		Failure: connectorcontract.FailureUpstream, UpstreamStatus: http.StatusBadGateway,
		Diagnostic: "upstream returned HTTP 502",
	}}
	request := decodeChatForTest(t, `{"model":"provider/model","messages":[]}`)
	recorder := httptest.NewRecorder()

	fixture.service.Chat(context.Background(), recorder, 1, request, []byte(`{}`), "application/json", "en")

	if recorder.Code != http.StatusInternalServerError || len(fixture.claims.requestResults) != 0 {
		t.Fatalf("terminalization failure response=%d requests=%+v body=%q", recorder.Code, fixture.claims.requestResults, recorder.Body.String())
	}
	if _, _, ok := credential.Take(); ok {
		t.Fatal("terminalization failure retained credential material")
	}
}

func TestInvalidConnectorSuccessCannotProveProtocolCompletion(t *testing.T) {
	fixture := newServiceFixture(t, nil)
	fixture.addDispatch(fixture.personal.snapshot.Candidates[0])
	fixture.openAI.results = []connectorcontract.AttemptResult{{
		Success: true, Committed: false, Failure: connectorcontract.FailureNone,
		UpstreamStatus: http.StatusOK, ClientStatus: http.StatusOK,
	}}
	request := decodeChatForTest(t, `{"model":"provider/model","messages":[]}`)
	recorder := httptest.NewRecorder()

	fixture.service.Chat(context.Background(), recorder, 1, request, []byte(`{}`), "application/json", "en")

	if recorder.Code != http.StatusInternalServerError || !strings.Contains(recorder.Body.String(), httperr.CodeInternal) {
		t.Fatalf("invalid success response=%d %q", recorder.Code, recorder.Body.String())
	}
	if len(fixture.claims.outcomes) != 1 || fixture.claims.outcomes[0].Kind != claim.ResultSynthetic ||
		fixture.claims.outcomes[0].ProtocolSuccess || fixture.claims.outcomes[0].ResponseStarted {
		t.Fatalf("invalid success outcome=%+v", fixture.claims.outcomes)
	}
	terminal := fixture.claims.requestResults[0]
	if terminal.Disposition != claim.AccountingCommit || terminal.Caller.Class != claim.ResultFailed ||
		terminal.Caller.ErrorCode != httperr.CodeInternal {
		t.Fatalf("invalid success terminal=%+v", terminal)
	}
}

func TestSafetyFailureStopsBeforeClaimAndReleasesAcceptedRequest(t *testing.T) {
	fixture := newServiceFixture(t, nil)
	if err := fixture.safety.Close(); err != nil {
		t.Fatal(err)
	}
	request := decodeChatForTest(t, `{"model":"provider/model","messages":[]}`)
	recorder := httptest.NewRecorder()

	fixture.service.Chat(context.Background(), recorder, 1, request, []byte(`{}`), "application/json", "en")

	if recorder.Code != http.StatusInternalServerError || !strings.Contains(recorder.Body.String(), httperr.CodeInternal) {
		t.Fatalf("safety failure response=%d %q", recorder.Code, recorder.Body.String())
	}
	requireEvents(t, fixture.claims.events, "accept", "request_terminal")
	if len(fixture.claims.claims) != 0 || fixture.openAI.calls != 0 ||
		fixture.claims.requestResults[0].Disposition != claim.AccountingRelease {
		t.Fatalf("safety failure crossed dispatch boundary: claims=%d connector=%d terminal=%+v",
			len(fixture.claims.claims), fixture.openAI.calls, fixture.claims.requestResults)
	}
}

func TestDeadlineAfterAcceptanceBeforeFirstClaimIsNotReportedAsUnbound(t *testing.T) {
	fixture := newServiceFixture(t, nil)
	fixture.service.timeout = 5 * time.Millisecond
	fixture.claims.acceptHook = func(ctx context.Context) { <-ctx.Done() }
	request := decodeChatForTest(t, `{"model":"provider/model","messages":[]}`)
	recorder := httptest.NewRecorder()

	fixture.service.Chat(context.Background(), recorder, 1, request, []byte(`{}`), "application/json", "en")

	if recorder.Code != http.StatusServiceUnavailable || !strings.Contains(recorder.Body.String(), httperr.CodeServiceUnavailable) ||
		strings.Contains(recorder.Body.String(), httperr.CodeUnboundModel) {
		t.Fatalf("deadline response=%d %q", recorder.Code, recorder.Body.String())
	}
	requireEvents(t, fixture.claims.events, "accept", "request_terminal")
	if fixture.claims.requestResults[0].Disposition != claim.AccountingRelease || fixture.openAI.calls != 0 {
		t.Fatalf("deadline terminal=%+v connector=%d", fixture.claims.requestResults, fixture.openAI.calls)
	}
}

func TestServiceCloseWaitsForInFlightAttemptAndRejectsNewWork(t *testing.T) {
	fixture := newServiceFixture(t, nil)
	credential := fixture.addDispatch(fixture.personal.snapshot.Candidates[0])
	entered := make(chan struct{})
	release := make(chan struct{})
	fixture.openAI.attempt = func(_ context.Context, input connector.AttemptInput) connectorcontract.AttemptResult {
		close(entered)
		<-release
		_, _ = input.Sink.Write([]byte(`{"id":"closed-after-attempt"}`))
		return connectorcontract.AttemptResult{
			Success: true, Committed: true, Failure: connectorcontract.FailureNone,
			UpstreamStatus: http.StatusOK, ClientStatus: http.StatusOK,
		}
	}
	request := decodeChatForTest(t, `{"model":"provider/model","messages":[]}`)
	recorder := httptest.NewRecorder()
	chatDone := make(chan struct{})
	go func() {
		defer close(chatDone)
		fixture.service.Chat(context.Background(), recorder, 1, request, []byte(`{}`), "application/json", "en")
	}()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("connector was not entered")
	}
	closeDone := make(chan error, 1)
	go func() { closeDone <- fixture.service.Close() }()
	select {
	case err := <-closeDone:
		t.Fatalf("Close returned while attempt was active: %v", err)
	case <-time.After(25 * time.Millisecond):
	}
	close(release)
	select {
	case <-chatDone:
	case <-time.After(2 * time.Second):
		t.Fatal("in-flight request did not finish")
	}
	if err := <-closeDone; err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, _, ok := credential.Take(); ok {
		t.Fatal("closed service retained attempt credential")
	}
	if _, err := fixture.service.ListModels(context.Background(), 1); !errors.Is(err, ErrClosed) {
		t.Fatalf("post-close ListModels error=%v", err)
	}
	if value, err := fixture.safety.Generate(1, testSafetyOrigin); value != "" || !errors.Is(err, errSafetyIdentifierClosed) {
		t.Fatalf("post-close safety value=%q err=%v", value, err)
	}
}

func TestPersonalAdmissionRejectsPersistedNameBeyond129Runes(t *testing.T) {
	fixture := newServiceFixture(t, nil)
	tooLong := strings.Repeat("界", 130)
	fixture.personal.preflight.FullName = tooLong
	fixture.personal.snapshot.PersonalPreflight.FullName = tooLong
	request := decodeChatForTest(t, `{"model":"`+tooLong+`","messages":[]}`)
	recorder := httptest.NewRecorder()

	fixture.service.Chat(context.Background(), recorder, 1, request, []byte(`{}`), "application/json", "en")

	if recorder.Code != http.StatusInternalServerError || len(fixture.claims.events) != 0 || fixture.personal.snapCalls != 0 {
		t.Fatalf("persisted-name invariant response=%d claims=%v snapshots=%d body=%q",
			recorder.Code, fixture.claims.events, fixture.personal.snapCalls, recorder.Body.String())
	}
}

func TestRoutineFormattingRedactsPhysicalCandidateFacts(t *testing.T) {
	candidate := RouteCandidate{
		EndpointID: 71, EndpointKeyID: 72, DonationKeyID: 73,
		ConnectorType:    connectorcontract.TypeOpenAICompatible,
		CanonicalBaseURL: "https://private-target.example/v1", UpstreamModelID: "private-upstream-model",
	}
	formatted := fmt.Sprintf("%v %+v %#v", candidate, candidate, candidate)
	for _, forbidden := range []string{"71", "72", "73", "private-target", "private-upstream-model"} {
		if strings.Contains(formatted, forbidden) {
			t.Fatalf("routine formatting exposed %q: %q", forbidden, formatted)
		}
	}
}
func TestDebugUpstreamResultProjectsCharityWithoutChangingSelf(t *testing.T) {
	result := connectorcontract.AttemptResult{
		Failure: connectorcontract.FailureUpstream, UpstreamStatus: http.StatusTeapot,
		Diagnostic: "owner-safe connector diagnostic",
		Usage: connectorcontract.Usage{
			Present: true, UncachedInputTokens: 3, CacheWriteInputTokens: 2,
			CacheReadInputTokens: 1, OutputTokens: 4,
		},
	}
	self := debugUpstreamResult(result, claim.RouteOpenAIChat, 17, 4_000)
	if self.ResultKind != debug.ResultResponse || self.StatusCode == nil ||
		*self.StatusCode != http.StatusTeapot || self.Diag == nil ||
		*self.Diag != result.Diagnostic {
		t.Fatalf("self projection changed: %+v", self)
	}

	charity := debugUpstreamResult(result, claim.RouteCharityChat, 17, 4_000)
	if charity.ResultKind != debug.ResultSynthetic || charity.StatusCode == nil ||
		*charity.StatusCode != http.StatusBadGateway || charity.UpstreamCode != nil || charity.Diag != nil {
		t.Fatalf("charity failure projection = %+v", charity)
	}
	if charity.Usage.UncachedInputTokens != "3" || charity.Usage.CacheWriteInputTokens != "2" ||
		charity.Usage.CacheReadInputTokens != "1" || charity.Usage.OutputTokens != "4" ||
		charity.Usage.TotalTokens != "10" || charity.Usage.UsageUnknown ||
		charity.Usage.Charge != "17" || charity.CompletedAt != 4_000 {
		t.Fatalf("charity usage projection = %+v", charity)
	}

	success := connectorcontract.AttemptResult{
		Success: true, Committed: true, Failure: connectorcontract.FailureNone,
		UpstreamStatus: 299, Diagnostic: "owner-safe success diagnostic",
		Usage: result.Usage,
	}
	charitySuccess := debugUpstreamResult(success, claim.RouteCharityChat, 19, 4_001)
	if charitySuccess.ResultKind != debug.ResultSynthetic || charitySuccess.StatusCode == nil ||
		*charitySuccess.StatusCode != http.StatusOK ||
		charitySuccess.UpstreamCode != nil || charitySuccess.Diag != nil {
		t.Fatalf("charity success projection = %+v", charitySuccess)
	}
}

func TestDebugLiveSuppressesAllUpstreamBytesAndUsesDebugPurpose(t *testing.T) {
	hub, err := debug.NewHub(activeIdentityVerifier{})
	if err != nil {
		t.Fatalf("NewHub: %v", err)
	}
	t.Cleanup(func() { _ = hub.Close() })
	metadata, _, err := hub.Start(1, "browser-binding")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, err := hub.ChangeMode(1, metadata.Revision, debug.ModeLive, true); err != nil {
		t.Fatalf("ChangeMode: %v", err)
	}
	fixture := newServiceFixture(t, hub)
	fixture.addDispatch(fixture.personal.snapshot.Candidates[0])
	fixture.openAI.results = []connectorcontract.AttemptResult{{
		Success: true, Committed: true, Failure: connectorcontract.FailureNone,
		UpstreamStatus: http.StatusOK, ClientStatus: http.StatusOK,
		Usage: connectorcontract.Usage{Present: true, UncachedInputTokens: 1, OutputTokens: 2},
	}}
	fixture.openAI.bodies = [][]byte{[]byte("RAW_UPSTREAM_SECRET_BODY")}
	request := decodeChatForTest(t, `{"model":"provider/model","messages":[]}`)
	recorder := httptest.NewRecorder()

	fixture.service.Chat(context.Background(), recorder, 1, request, []byte(`{"model":"provider/model"}`), "application/json", "en")

	if recorder.Code != http.StatusUnprocessableEntity || !strings.Contains(recorder.Body.String(), httperr.CodeDebugLiveResultCaptured) {
		t.Fatalf("live response=%d %q", recorder.Code, recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), "RAW_UPSTREAM") {
		t.Fatalf("raw upstream reached caller: %q", recorder.Body.String())
	}
	if fixture.claims.claims[0].Purpose != claim.PurposeDebugLive {
		t.Fatalf("purpose=%q", fixture.claims.claims[0].Purpose)
	}
	terminal := fixture.claims.requestResults[0]
	if terminal.Caller.ErrorCode != httperr.CodeDebugLiveResultCaptured || terminal.Caller.Status != http.StatusUnprocessableEntity {
		t.Fatalf("terminal=%+v", terminal)
	}
}

func TestCharityDebugLiveKeepsCharityPurposeAndAccounting(t *testing.T) {
	hub, err := debug.NewHub(activeIdentityVerifier{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = hub.Close() })
	metadata, _, err := hub.Start(1, "browser-binding")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := hub.ChangeMode(1, metadata.Revision, debug.ModeLive, true); err != nil {
		t.Fatal(err)
	}
	fixture := newServiceFixture(t, hub)
	fixture.charges.charge = 6
	fixture.addDispatch(fixture.charity.snapshot.Candidates[0])
	fixture.openAI.results = []connectorcontract.AttemptResult{{
		Failure: connectorcontract.FailureUpstream, UpstreamStatus: http.StatusTeapot,
		Diagnostic: "HOSTILE_REAL_STATUS_AND_DIAGNOSTIC",
		Usage:      connectorcontract.Usage{Present: true, UncachedInputTokens: 2, OutputTokens: 1},
	}}
	fixture.openAI.bodies = [][]byte{[]byte("CHARITY_RAW_UPSTREAM")}
	request := decodeChatForTest(t, `{"model":"[公益]care/model","messages":[{"role":"user","content":"hello"}]}`)
	recorder := httptest.NewRecorder()

	fixture.service.Chat(context.Background(), recorder, 1, request, []byte(`{}`), "application/json", "en")

	if recorder.Code != http.StatusUnprocessableEntity || !strings.Contains(recorder.Body.String(), httperr.CodeDebugLiveResultCaptured) ||
		strings.Contains(recorder.Body.String(), "CHARITY_RAW_UPSTREAM") {
		t.Fatalf("charity live response=%d %q", recorder.Code, recorder.Body.String())
	}
	if len(fixture.claims.claims) != 1 || fixture.claims.claims[0].Purpose != claim.PurposeCharity {
		t.Fatalf("charity live claim=%+v", fixture.claims.claims)
	}
	terminal := fixture.claims.requestResults[0]
	if terminal.Disposition != claim.AccountingCommit || terminal.ActualChargeMilli != 6 ||
		terminal.Caller.ErrorCode != httperr.CodeDebugLiveResultCaptured {
		t.Fatalf("charity live terminal=%+v", terminal)
	}
	subscription, err := hub.Subscribe(context.Background(), 1, "browser-binding", "")
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	t.Cleanup(func() { _ = subscription.Close() })
	snapshotContext, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	event, err := subscription.Next(snapshotContext)
	if err != nil {
		t.Fatalf("snapshot Next: %v", err)
	}
	if event.Kind != debug.EventSnapshot {
		t.Fatalf("snapshot event kind = %q", event.Kind)
	}
	var snapshot debug.SnapshotData
	if err := json.Unmarshal(event.Data, &snapshot); err != nil {
		t.Fatalf("decode snapshot: %v", err)
	}
	if len(snapshot.Traces) != 1 || snapshot.Traces[0].Request.RouteKind != debug.RouteCharityChat {
		t.Fatalf("charity debug traces = %+v", snapshot.Traces)
	}
	upstream := snapshot.Traces[0].UpstreamResult
	if upstream == nil || upstream.ResultKind != debug.ResultSynthetic || upstream.StatusCode == nil ||
		*upstream.StatusCode != http.StatusBadGateway || upstream.UpstreamCode != nil || upstream.Diag != nil ||
		upstream.Usage.TotalTokens != "3" || upstream.Usage.Charge != "6" {
		t.Fatalf("charity debug upstream projection = %+v", upstream)
	}
	if encoded := string(event.Data); strings.Contains(encoded, "HOSTILE_REAL_STATUS_AND_DIAGNOSTIC") {
		t.Fatalf("charity debug wire leaked real upstream facts: %s", encoded)
	}
}

func TestDebugStopAndReplaceCancelDispatchedForwardWithStable409(t *testing.T) {
	tests := []struct {
		name   string
		cancel func(*debug.Hub, string) error
	}{
		{
			name: "stop",
			cancel: func(hub *debug.Hub, revision string) error {
				return hub.Stop(1, revision, true)
			},
		},
		{
			name: "replace",
			cancel: func(hub *debug.Hub, revision string) error {
				_, err := hub.Replace(1, "browser-binding", revision, true)
				return err
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			hub, err := debug.NewHub(activeIdentityVerifier{})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = hub.Close() })
			metadata, _, err := hub.Start(1, "browser-binding")
			if err != nil {
				t.Fatal(err)
			}
			live, err := hub.ChangeMode(1, metadata.Revision, debug.ModeLive, true)
			if err != nil {
				t.Fatal(err)
			}
			fixture := newServiceFixture(t, hub)
			credential := fixture.addDispatch(fixture.personal.snapshot.Candidates[0])
			entered := make(chan struct{})
			fixture.openAI.attempt = func(ctx context.Context, _ connector.AttemptInput) connectorcontract.AttemptResult {
				close(entered)
				<-ctx.Done()
				return connectorcontract.AttemptResult{Failure: connectorcontract.FailureCanceled, Diagnostic: "request canceled"}
			}
			request := decodeChatForTest(t, `{"model":"provider/model","messages":[],"stream":false}`)
			recorder := httptest.NewRecorder()
			done := make(chan struct{})
			go func() {
				defer close(done)
				fixture.service.Chat(context.Background(), recorder, 1, request, []byte(`{}`), "application/json", "en")
			}()
			select {
			case <-entered:
			case <-time.After(2 * time.Second):
				t.Fatal("connector was not entered")
			}
			if err := test.cancel(hub, live.Revision); err != nil {
				t.Fatalf("cancel live session: %v", err)
			}
			select {
			case <-done:
			case <-time.After(2 * time.Second):
				t.Fatal("cancelled debug forward did not finish")
			}

			if recorder.Code != http.StatusConflict || !strings.Contains(recorder.Body.String(), httperr.CodeDebugLiveCancelled) {
				t.Fatalf("cancel response=%d %q", recorder.Code, recorder.Body.String())
			}
			if _, _, ok := credential.Take(); ok {
				t.Fatal("cancelled debug forward retained credential")
			}
			if len(fixture.claims.requestResults) != 1 {
				t.Fatalf("request results=%+v", fixture.claims.requestResults)
			}
			terminal := fixture.claims.requestResults[0]
			if terminal.Disposition != claim.AccountingCommit || terminal.Caller.ErrorCode != httperr.CodeDebugLiveCancelled {
				t.Fatalf("cancel terminal=%+v", terminal)
			}
		})
	}
}

func TestCandidateRevocationBeforeClaimDoesNotExpandSnapshot(t *testing.T) {
	fixture := newServiceFixture(t, nil)
	second := fixture.personal.snapshot.Candidates[0]
	second.EndpointID, second.EndpointKeyID = 41, 42
	fixture.personal.snapshot.Candidates = append(fixture.personal.snapshot.Candidates, second)
	fixture.claims.claimErrors = map[int]error{0: claim.ErrNotFound, 1: claim.ErrNotFound}
	request := decodeChatForTest(t, `{"model":"provider/model","messages":[]}`)
	recorder := httptest.NewRecorder()

	fixture.service.Chat(context.Background(), recorder, 1, request, []byte(`{}`), "application/json", "en")

	if recorder.Code != http.StatusServiceUnavailable || !strings.Contains(recorder.Body.String(), httperr.CodeUnboundModel) {
		t.Fatalf("response=%d %q", recorder.Code, recorder.Body.String())
	}
	if len(fixture.claims.claims) != 2 || fixture.personal.snapCalls != 1 || fixture.openAI.calls != 0 {
		t.Fatalf("claims=%d snapshots=%d connector=%d", len(fixture.claims.claims), fixture.personal.snapCalls, fixture.openAI.calls)
	}
	if fixture.claims.requestResults[0].Disposition != claim.AccountingRelease {
		t.Fatalf("terminal=%+v", fixture.claims.requestResults[0])
	}
}

func TestChargeFailureLeavesAcceptedRequestForRecovery(t *testing.T) {
	fixture := newServiceFixture(t, nil)
	fixture.addDispatch(fixture.charity.snapshot.Candidates[0])
	fixture.charges.err = errors.New("charge unavailable")
	fixture.openAI.results = []connectorcontract.AttemptResult{{
		Failure: connectorcontract.FailureUpstream, UpstreamStatus: http.StatusBadGateway,
	}}
	request := decodeChatForTest(t, `{"model":"[公益]care/model","messages":[]}`)
	recorder := httptest.NewRecorder()

	fixture.service.Chat(context.Background(), recorder, 1, request, []byte(`{}`), "application/json", "en")

	if len(fixture.claims.requestResults) != 0 {
		t.Fatalf("request was incorrectly settled with unknown charge: %+v", fixture.claims.requestResults)
	}
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d body=%q", recorder.Code, recorder.Body.String())
	}
}

func TestCharityPreflightAndSnapshotShareOneDecisionTime(t *testing.T) {
	fixture := newServiceFixture(t, nil)
	fixture.addDispatch(fixture.charity.snapshot.Candidates[0])
	var clockCalls int
	fixture.service.now = func() time.Time {
		clockCalls++
		return time.Unix(2_000+int64(clockCalls), 0)
	}
	fixture.openAI.results = []connectorcontract.AttemptResult{{
		Success: true, Committed: true, Failure: connectorcontract.FailureNone,
		UpstreamStatus: http.StatusOK, ClientStatus: http.StatusOK,
	}}
	request := decodeChatForTest(t, `{"model":"[公益]care/model","messages":[],"n":1}`)
	recorder := httptest.NewRecorder()

	fixture.service.Chat(context.Background(), recorder, 1, request, []byte(`{}`), "application/json", "en")

	if len(fixture.charity.preTimes) != 1 || len(fixture.charity.snapTimes) != 1 ||
		fixture.charity.preTimes[0] != fixture.charity.snapTimes[0] {
		t.Fatalf("charity decision times preflight=%v snapshot=%v", fixture.charity.preTimes, fixture.charity.snapTimes)
	}
	if len(fixture.charity.snapTypes) != 1 || len(fixture.charity.snapTypes[0]) != 1 ||
		fixture.charity.snapTypes[0][0] != connectorcontract.TypeOpenAICompatible {
		t.Fatalf("charity snapshot connector capabilities=%v, want OpenAI only", fixture.charity.snapTypes)
	}
	if len(fixture.claims.accepts) != 1 || fixture.claims.accepts[0].CharityDecisionNow == nil ||
		*fixture.claims.accepts[0].CharityDecisionNow != fixture.charity.preTimes[0] {
		t.Fatalf("charity acceptance decision time=%+v, want %d", fixture.claims.accepts, fixture.charity.preTimes[0])
	}
	if clockCalls != 1 {
		t.Fatalf("forward sampled the request clock %d times, want 1", clockCalls)
	}
}

func TestAcceptLedgerErrorsKeepStableWireMappingsThroughWrapping(t *testing.T) {
	tests := []struct {
		name      string
		charity   bool
		acceptErr error
		wantHTTP  int
		wantCode  string
	}{
		{
			name:    "concurrent charity balance loss",
			charity: true,
			acceptErr: fmt.Errorf("claim reserve request: %w",
				fmt.Errorf("ledger apply: %w", ledger.ErrInsufficientBalance)),
			wantHTTP: http.StatusForbidden,
			wantCode: httperr.CodeInsufficientCredits,
		},
		{
			name:      "acceptance capacity exhausted",
			acceptErr: fmt.Errorf("claim reserve request: %w", ledger.ErrCapacityExhausted),
			wantHTTP:  http.StatusServiceUnavailable,
			wantCode:  httperr.CodeServiceUnavailable,
		},
		{
			name:      "database busy retryable",
			acceptErr: fmt.Errorf("claim reserve request: %w", fmt.Errorf("begin immediate: %w", ledger.ErrRetryable)),
			wantHTTP:  http.StatusServiceUnavailable,
			wantCode:  httperr.CodeServiceUnavailable,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newServiceFixture(t, nil)
			fixture.claims.acceptError = test.acceptErr
			model := "provider/model"
			if test.charity {
				model = "[公益]care/model"
			}
			request := decodeChatForTest(t, `{"model":"`+model+`","messages":[]}`)
			recorder := httptest.NewRecorder()

			fixture.service.Chat(context.Background(), recorder, 1, request, []byte(`{}`), "application/json", "en")

			if recorder.Code != test.wantHTTP || !strings.Contains(recorder.Body.String(), test.wantCode) {
				t.Fatalf("response=%d %q", recorder.Code, recorder.Body.String())
			}
			if len(fixture.claims.events) != 1 || fixture.claims.events[0] != "accept" || fixture.openAI.calls != 0 {
				t.Fatalf("acceptance failure crossed dispatch boundary: events=%v connector=%d", fixture.claims.events, fixture.openAI.calls)
			}
		})
	}
}

func TestCharityPreflightRevocationKeepsUnauthorizedMappingThroughWrapping(t *testing.T) {
	fixture := newServiceFixture(t, nil)
	fixture.charity.preErr = fmt.Errorf("preflight identity recheck: %w", charityrouting.ErrUnauthorized)
	request := decodeChatForTest(t, `{"model":"[公益]care/model","messages":[]}`)
	recorder := httptest.NewRecorder()

	fixture.service.Chat(context.Background(), recorder, 1, request, []byte(`{}`), "application/json", "en")

	if recorder.Code != http.StatusUnauthorized || !strings.Contains(recorder.Body.String(), httperr.CodeUnauthorized) {
		t.Fatalf("response=%d %q", recorder.Code, recorder.Body.String())
	}
	if len(fixture.claims.events) != 0 || fixture.openAI.calls != 0 {
		t.Fatalf("revoked caller crossed acceptance boundary: events=%v connector=%d", fixture.claims.events, fixture.openAI.calls)
	}
}

func TestPreAcceptanceErrorClosedSet(t *testing.T) {
	tests := []struct {
		name        string
		charity     bool
		err         error
		wantHTTP    int
		wantCode    string
		wantText    string
		acceptErr   bool
		snapshotErr bool
	}{
		{name: "model not found", err: routing.ErrNotFound, wantHTTP: http.StatusNotFound, wantCode: httperr.CodeNotFound},
		{name: "ambiguous model", err: routing.ErrAmbiguousIdentity, wantHTTP: http.StatusBadRequest, wantCode: httperr.CodeInvalidRequest},
		{name: "unbound model", err: routing.ErrUnbound, wantHTTP: http.StatusServiceUnavailable, wantCode: httperr.CodeUnboundModel},
		{name: "routing resource limit", err: routing.ErrResourceLimit, wantHTTP: http.StatusUnprocessableEntity, wantCode: httperr.CodeResourceLimitExceeded},
		{name: "charity disabled", charity: true, err: charityrouting.ErrFeatureDisabled, wantHTTP: http.StatusForbidden, wantCode: httperr.CodeFeatureDisabled},
		{name: "charity suspended", charity: true, err: charityrouting.ErrCharitySuspended, wantHTTP: http.StatusForbidden, wantCode: httperr.CodeCharitySuspended},
		{name: "charity credits", charity: true, err: charityrouting.ErrInsufficientCredits, wantHTTP: http.StatusForbidden, wantCode: httperr.CodeInsufficientCredits},
		{
			name: "charity content counts", charity: true,
			err:      fmt.Errorf("policy: %w", &charityrouting.ContentTooShortError{Actual: 2, Minimum: 5}),
			wantHTTP: http.StatusBadRequest, wantCode: httperr.CodeContentTooShort, wantText: "2 < 5",
		},
		{name: "charity unavailable", charity: true, err: charityrouting.ErrUnavailable, wantHTTP: http.StatusServiceUnavailable, wantCode: httperr.CodeUnboundModel},
		{
			name: "charity ordering entropy unavailable", charity: true,
			err:      fmt.Errorf("snapshot: %w", charityrouting.ErrEntropyUnavailable),
			wantHTTP: http.StatusServiceUnavailable, wantCode: httperr.CodeServiceUnavailable, snapshotErr: true,
		},
		{
			name: "maintenance final transaction", err: fmt.Errorf("accept gate: %w", maintenance.ErrMaintenanceOn),
			wantHTTP: http.StatusServiceUnavailable, wantCode: httperr.CodeMaintenance, acceptErr: true,
		},
		{
			name: "claim dependency", err: fmt.Errorf("accept: %w", claim.ErrDependencyUnavailable),
			wantHTTP: http.StatusServiceUnavailable, wantCode: httperr.CodeServiceUnavailable, acceptErr: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newServiceFixture(t, nil)
			model := "provider/model"
			if test.charity {
				model = "[公益]care/model"
				if test.snapshotErr {
					fixture.charity.snapErr = test.err
				} else {
					fixture.charity.preErr = test.err
				}
			} else if test.acceptErr {
				fixture.claims.acceptError = test.err
			} else {
				fixture.personal.preErr = test.err
			}
			request := decodeChatForTest(t, `{"model":"`+model+`","messages":[]}`)
			recorder := httptest.NewRecorder()

			fixture.service.Chat(context.Background(), recorder, 1, request, []byte(`{}`), "application/json", "en")

			if recorder.Code != test.wantHTTP || !strings.Contains(recorder.Body.String(), test.wantCode) {
				t.Fatalf("response=%d %q", recorder.Code, recorder.Body.String())
			}
			if test.wantText != "" {
				var envelope httperr.Envelope
				if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil ||
					!strings.Contains(envelope.Error.Message, test.wantText) {
					t.Fatalf("response message=%q decode=%v", envelope.Error.Message, err)
				}
			}
			if fixture.openAI.calls != 0 {
				t.Fatalf("pre-acceptance error reached connector: %d", fixture.openAI.calls)
			}
		})
	}
}
