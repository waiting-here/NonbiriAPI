package forward

import (
	"bytes"
	"context"
	"sync"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/claim"
	"github.com/waiting-here/NonbiriAPI/internal/connector"
	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/connector/openai"
	"github.com/waiting-here/NonbiriAPI/internal/debug"
)

type fixedSubkeyDeriver struct{ key []byte }

func (deriver fixedSubkeyDeriver) DeriveGenerationTwoSubkey(_ []byte) ([]byte, error) {
	if len(deriver.key) == 0 {
		return bytes.Repeat([]byte{0x5a}, 32), nil
	}
	return append([]byte(nil), deriver.key...), nil
}

type fakePersonalRouter struct {
	preflight PersonalPreflight
	snapshot  PersonalSnapshot
	models    []ListedModel
	preErr    error
	snapErr   error
	listErr   error
	preCalls  int
	snapCalls int
	listCalls int
}

func (router *fakePersonalRouter) Preflight(_ context.Context, _ int64, _ string) (PersonalPreflight, error) {
	router.preCalls++
	return router.preflight, router.preErr
}

func (router *fakePersonalRouter) Snapshot(_ context.Context, _ int64, _ string) (PersonalSnapshot, error) {
	router.snapCalls++
	return router.snapshot, router.snapErr
}

func (router *fakePersonalRouter) ListRoutableModels(_ context.Context, _ int64, _ int) ([]ListedModel, error) {
	router.listCalls++
	return append([]ListedModel(nil), router.models...), router.listErr
}

type fakeCharityRouter struct {
	preflight CharityPreflight
	snapshot  CharitySnapshot
	models    []ListedModel
	preErr    error
	snapErr   error
	listErr   error
	preCalls  int
	snapCalls int
	listCalls int
	preTimes  []int64
	snapTimes []int64
	snapTypes [][]connectorcontract.Type
}

func (router *fakeCharityRouter) Preflight(_ context.Context, _ int64, _ string, _ *openai.ChatRequest, now int64) (CharityPreflight, error) {
	router.preCalls++
	router.preTimes = append(router.preTimes, now)
	return router.preflight, router.preErr
}

func (router *fakeCharityRouter) Snapshot(_ context.Context, _ int64, now int64, connectorTypes []connectorcontract.Type) (CharitySnapshot, error) {
	router.snapCalls++
	router.snapTimes = append(router.snapTimes, now)
	router.snapTypes = append(router.snapTypes, append([]connectorcontract.Type(nil), connectorTypes...))
	return router.snapshot, router.snapErr
}

func (router *fakeCharityRouter) ListAvailableModels(_ context.Context, _ int64, _ int) ([]ListedModel, error) {
	router.listCalls++
	return append([]ListedModel(nil), router.models...), router.listErr
}

type fakeDebugCapture struct {
	decision debug.CaptureDecision
	err      error
	calls    int
}

func (capture *fakeDebugCapture) DecideAfterAdmission(_ context.Context, _ debug.CaptureInput) (debug.CaptureDecision, error) {
	capture.calls++
	return capture.decision, capture.err
}

type fakeChargeCalculator struct {
	charge int64
	err    error
	calls  int
}

func (calculator *fakeChargeCalculator) CalculateRequestCharge(_ context.Context, _ string, _ claim.AccountingDisposition) (int64, error) {
	calculator.calls++
	return calculator.charge, calculator.err
}

type fakeDispatchGrant struct {
	target     connectorcontract.Target
	policy     connectorcontract.AttemptPolicy
	credential *connectorcontract.ShortLivedSecret
}

func (grant *fakeDispatchGrant) Target() connectorcontract.Target { return grant.target }
func (grant *fakeDispatchGrant) Policy() connectorcontract.AttemptPolicy {
	return grant.policy
}
func (grant *fakeDispatchGrant) TakeCredential() (*connectorcontract.ShortLivedSecret, bool) {
	if grant == nil || grant.credential == nil {
		return nil, false
	}
	credential := grant.credential
	grant.credential = nil
	return credential, true
}
func (grant *fakeDispatchGrant) Clear() {
	if grant != nil && grant.credential != nil {
		grant.credential.Clear()
		grant.credential = nil
	}
}

type fakeClaimRail struct {
	mu sync.Mutex

	events         []string
	accepts        []claim.AcceptInput
	claims         []claim.ClaimInput
	outcomes       []claim.AttemptOutcome
	requestResults []claim.CompleteRequestInput
	dispatches     []DispatchGrant
	claimErrors    map[int]error
	takeErrors     map[int]error
	releaseErrors  map[int]error
	attemptErrors  map[int]error
	completeError  error
	acceptError    error
	acceptHook     func(context.Context)
	claimCall      int
	takeCall       int
	releaseCall    int
	attemptCall    int
}

func (rail *fakeClaimRail) addEvent(value string) {
	rail.mu.Lock()
	rail.events = append(rail.events, value)
	rail.mu.Unlock()
}

func (rail *fakeClaimRail) Accept(ctx context.Context, input claim.AcceptInput) (claim.Request, error) {
	rail.mu.Lock()
	rail.events = append(rail.events, "accept")
	rail.accepts = append(rail.accepts, input)
	err := rail.acceptError
	hook := rail.acceptHook
	rail.mu.Unlock()
	if hook != nil {
		hook(ctx)
	}
	if err != nil {
		return claim.Request{}, err
	}
	return claim.Request{ID: "req_AAAAAAAAAAAAAAAAAAAAAA"}, nil
}

func (rail *fakeClaimRail) Claim(_ context.Context, input claim.ClaimInput) (claim.Handle, error) {
	rail.mu.Lock()
	index := rail.claimCall
	rail.claimCall++
	rail.events = append(rail.events, "claim")
	rail.claims = append(rail.claims, input)
	err := rail.claimErrors[index]
	rail.mu.Unlock()
	return claim.Handle{}, err
}

func (rail *fakeClaimRail) TakeForDispatch(_ context.Context, _ claim.Handle) (DispatchGrant, error) {
	rail.mu.Lock()
	index := rail.takeCall
	rail.takeCall++
	rail.events = append(rail.events, "dispatch")
	err := rail.takeErrors[index]
	var grant DispatchGrant
	if index < len(rail.dispatches) {
		grant = rail.dispatches[index]
	}
	rail.mu.Unlock()
	return grant, err
}

func (rail *fakeClaimRail) ReleaseUndispatched(_ context.Context, _ claim.Handle) (claim.Attempt, error) {
	rail.mu.Lock()
	index := rail.releaseCall
	rail.releaseCall++
	rail.events = append(rail.events, "release")
	err := rail.releaseErrors[index]
	rail.mu.Unlock()
	return claim.Attempt{}, err
}

func (rail *fakeClaimRail) MarkResponseStarted(context.Context, claim.Handle) error {
	return nil
}

func (rail *fakeClaimRail) CompleteAttempt(_ context.Context, _ claim.Handle, outcome claim.AttemptOutcome) (claim.Attempt, error) {
	rail.mu.Lock()
	index := rail.attemptCall
	rail.attemptCall++
	rail.events = append(rail.events, "attempt_terminal")
	rail.outcomes = append(rail.outcomes, outcome)
	err := rail.attemptErrors[index]
	rail.mu.Unlock()
	return claim.Attempt{}, err
}

func (rail *fakeClaimRail) CompleteRequest(_ context.Context, input claim.CompleteRequestInput) (claim.Request, error) {
	rail.mu.Lock()
	rail.events = append(rail.events, "request_terminal")
	rail.requestResults = append(rail.requestResults, input)
	err := rail.completeError
	rail.mu.Unlock()
	return claim.Request{}, err
}

type fakeConnector struct {
	mu sync.Mutex

	connectorType connectorcontract.Type
	capabilities  connectorcontract.CapabilitySet
	results       []connectorcontract.AttemptResult
	bodies        [][]byte
	events        *[]string
	calls         int
	credentials   []*connectorcontract.ShortLivedSecret
	policies      []connectorcontract.AttemptPolicy
	targets       []connectorcontract.Target
	consume       bool
	attempt       func(context.Context, connector.AttemptInput) connectorcontract.AttemptResult
}

func (instance *fakeConnector) Type() connectorcontract.Type { return instance.connectorType }
func (instance *fakeConnector) Capabilities() connectorcontract.CapabilitySet {
	return instance.capabilities
}
func (instance *fakeConnector) Attempt(ctx context.Context, input connector.AttemptInput) connectorcontract.AttemptResult {
	instance.mu.Lock()
	index := instance.calls
	instance.calls++
	instance.credentials = append(instance.credentials, input.Credential)
	instance.policies = append(instance.policies, input.Policy)
	instance.targets = append(instance.targets, input.Target)
	if instance.events != nil {
		*instance.events = append(*instance.events, "connector")
	}
	var body []byte
	if index < len(instance.bodies) {
		body = append([]byte(nil), instance.bodies[index]...)
	}
	result := connectorcontract.AttemptResult{Failure: connectorcontract.FailureInternal}
	if index < len(instance.results) {
		result = instance.results[index]
	}
	consume := instance.consume
	attempt := instance.attempt
	instance.mu.Unlock()
	if consume && input.Credential != nil {
		plaintext, ciphertext, _ := input.Credential.Take()
		clear(plaintext)
		clear(ciphertext)
	}
	if len(body) != 0 {
		_, _ = input.Sink.Write(body)
	}
	if attempt != nil {
		return attempt(ctx, input)
	}
	return result
}

type serviceFixture struct {
	service   *Service
	personal  *fakePersonalRouter
	charity   *fakeCharityRouter
	claims    *fakeClaimRail
	debug     DebugCapture
	charges   *fakeChargeCalculator
	openAI    *fakeConnector
	anthropic *fakeConnector
	safety    *SafetyIdentifierFactory
}

func newServiceFixture(t *testing.T, capture DebugCapture) *serviceFixture {
	t.Helper()
	if capture == nil {
		capture = &fakeDebugCapture{}
	}
	personalCandidate := RouteCandidate{
		EndpointID: 11, EndpointKeyID: 12, ConnectorType: connectorcontract.TypeOpenAICompatible,
		CanonicalBaseURL: "https://upstream.example/v1", UpstreamModelID: "upstream-model",
	}
	personal := &fakePersonalRouter{
		preflight: PersonalPreflight{
			ModelID: 7, OwnerUserID: 1, Provider: "provider", Model: "model", FullName: "provider/model",
			RouteStrategy: "ordered", Revision: 1, BindingRevision: 1,
		},
	}
	personal.snapshot = PersonalSnapshot{PersonalPreflight: personal.preflight, Candidates: []RouteCandidate{personalCandidate}}
	charityCandidate := personalCandidate
	charityCandidate.EndpointID, charityCandidate.EndpointKeyID, charityCandidate.DonationKeyID = 21, 22, 23
	charity := &fakeCharityRouter{
		preflight: CharityPreflight{
			ModelID: 8, Provider: "care", Model: "model", FullName: "[公益]care/model", ReservedMilli: 10,
		},
	}
	charity.snapshot = CharitySnapshot{CharityPreflight: charity.preflight, Candidates: []RouteCandidate{charityCandidate}}
	claims := &fakeClaimRail{}
	charges := &fakeChargeCalculator{charge: 10}
	registry := connector.NewDefaultRegistry()
	openAIDescriptor, _ := registry.Descriptor(connectorcontract.TypeOpenAICompatible)
	anthropicDescriptor, _ := registry.Descriptor(connectorcontract.TypeAnthropicCompatible)
	openAI := &fakeConnector{connectorType: connectorcontract.TypeOpenAICompatible, capabilities: openAIDescriptor.Capabilities, consume: true}
	anthropic := &fakeConnector{connectorType: connectorcontract.TypeAnthropicCompatible, capabilities: anthropicDescriptor.Capabilities, consume: true}
	safety, err := NewSafetyIdentifierFactory(fixedSubkeyDeriver{})
	if err != nil {
		t.Fatalf("NewSafetyIdentifierFactory: %v", err)
	}
	service, err := NewService(Config{
		Personal: personal, Charity: charity, Claims: claims, CharityCharges: charges,
		Debug: capture, Registry: registry, Connectors: []connector.Connector{openAI, anthropic}, Safety: safety,
		Now:            func() time.Time { return time.Unix(2_000, 0) },
		ForwardTimeout: time.Minute, Settlement: time.Second,
		Backoff: BackoffConfig{Max: time.Nanosecond},
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	t.Cleanup(func() { _ = service.Close() })
	return &serviceFixture{
		service: service, personal: personal, charity: charity, claims: claims, debug: capture,
		charges: charges, openAI: openAI, anthropic: anthropic, safety: safety,
	}
}

func (fixture *serviceFixture) addDispatch(candidate RouteCandidate) *connectorcontract.ShortLivedSecret {
	credential := connectorcontract.NewShortLivedSecret([]byte("secret-value"), []byte("ciphertext-value"))
	fixture.claims.dispatches = append(fixture.claims.dispatches, &fakeDispatchGrant{
		target: connectorcontract.NewTarget(candidate.ConnectorType, candidate.CanonicalBaseURL, candidate.UpstreamModelID),
		policy: candidate.Policy, credential: credential,
	})
	return credential
}

func decodeChatForTest(t *testing.T, body string) *openai.ChatRequest {
	t.Helper()
	request, err := openai.DecodeChatRequest(bytes.NewBufferString(body), openai.MaxRequestBodyBytes)
	if err != nil {
		t.Fatalf("DecodeChatRequest: %v", err)
	}
	t.Cleanup(request.Clear)
	return request
}

func requireEvents(t *testing.T, got []string, want ...string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("events=%v, want %v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("events=%v, want %v", got, want)
		}
	}
}

type activeIdentityVerifier struct{}

func (activeIdentityVerifier) VerifyDebugIdentity(context.Context, int64, string) (debug.IdentityState, error) {
	return debug.IdentityActive, nil
}

var (
	_ PersonalRouter             = (*fakePersonalRouter)(nil)
	_ CharityRouter              = (*fakeCharityRouter)(nil)
	_ DebugCapture               = (*fakeDebugCapture)(nil)
	_ ClaimRail                  = (*fakeClaimRail)(nil)
	_ connector.Connector        = (*fakeConnector)(nil)
	_ GenerationTwoSubkeyDeriver = fixedSubkeyDeriver{}
)
