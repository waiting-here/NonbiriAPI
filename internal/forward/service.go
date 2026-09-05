package forward

import (
	"context"
	"errors"
	"log/slog"
	"math/big"
	"net/http"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/waiting-here/NonbiriAPI/internal/charityrouting"
	"github.com/waiting-here/NonbiriAPI/internal/claim"
	"github.com/waiting-here/NonbiriAPI/internal/connector"
	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/connector/openai"
	"github.com/waiting-here/NonbiriAPI/internal/credits"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/debug"
	"github.com/waiting-here/NonbiriAPI/internal/diagnostic"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
	"github.com/waiting-here/NonbiriAPI/internal/maintenance"
	"github.com/waiting-here/NonbiriAPI/internal/routing"
)

const (
	charityModelPrefix = "[公益]"
	maxPersonalRunes   = 129
	maxCharityRunes    = 133
	maxProviderRunes   = 64
	maxUnixSecond      = int64(253402300799)
)

// Service owns one complete Generation 2 logical request. Its read lock is
// held for the whole operation so Close waits for every credential-bearing
// attempt and no new attempt can race safety-key zeroization.
type Service struct {
	lifecycle sync.RWMutex
	closed    bool

	personal       PersonalRouter
	charity        CharityRouter
	claims         ClaimRail
	charityCharges CharityChargeCalculator
	debug          DebugCapture
	registry       *connector.Registry
	connectors     map[connectorcontract.Type]connector.Connector
	safety         *SafetyIdentifierFactory
	observer       *connector.SafeObserver
	now            func() time.Time
	timeout        time.Duration
	settlement     time.Duration
	backoff        BackoffConfig
}

type logicalAdmission struct {
	charity       bool
	modelID       int64
	fullName      string
	strategy      string
	silentRetry   bool
	flatten       bool
	reservedMilli int64
	decisionNow   int64
}

type executionPlan struct {
	logicalAdmission
	candidates []RouteCandidate
	purpose    claim.Purpose
	route      claim.RouteKind
}

type attemptRun struct {
	dispatched      bool
	result          connectorcontract.AttemptResult
	hasResult       bool
	failure         *wireFailure
	err             error
	terminalBlocked bool
}

func NewService(config Config) (*Service, error) {
	if nilInterfaceValue(config.Personal) || nilInterfaceValue(config.Charity) ||
		nilInterfaceValue(config.Claims) || nilInterfaceValue(config.CharityCharges) ||
		nilInterfaceValue(config.Debug) || config.Registry == nil || config.Safety == nil {
		return nil, ErrInvalidConfiguration
	}
	if config.ForwardTimeout < 0 || config.ForwardTimeout > DefaultForwardTimeout ||
		config.Settlement < 0 || config.Settlement > DefaultSettlementLimit ||
		config.Backoff.Base < 0 || config.Backoff.Max < 0 ||
		(config.Backoff.Max != 0 && config.Backoff.Max < config.Backoff.Base) {
		return nil, ErrInvalidConfiguration
	}
	if config.ForwardTimeout == 0 {
		config.ForwardTimeout = DefaultForwardTimeout
	}
	if config.Settlement == 0 {
		config.Settlement = DefaultSettlementLimit
	}
	if config.Backoff.Base == 0 && config.Backoff.Max == 0 {
		config.Backoff = BackoffConfig{Base: DefaultBackoffBase, Max: DefaultBackoffMax}
	}
	if config.Now == nil {
		config.Now = time.Now
	}

	instances := make(map[connectorcontract.Type]connector.Connector, len(config.Connectors))
	for _, instance := range config.Connectors {
		if nilInterfaceValue(instance) {
			return nil, ErrInvalidConfiguration
		}
		descriptor, ok := config.Registry.Descriptor(instance.Type())
		if !ok || descriptor.Capabilities != instance.Capabilities() {
			return nil, ErrInvalidConfiguration
		}
		if _, duplicate := instances[instance.Type()]; duplicate {
			return nil, ErrInvalidConfiguration
		}
		instances[instance.Type()] = instance
	}
	for _, connectorType := range config.Registry.Types() {
		if instances[connectorType] == nil {
			return nil, ErrInvalidConfiguration
		}
	}

	return &Service{
		personal: config.Personal, charity: config.Charity, claims: config.Claims,
		charityCharges: config.CharityCharges, debug: config.Debug, registry: config.Registry,
		connectors: instances, safety: config.Safety, observer: config.Observer,
		now: config.Now, timeout: config.ForwardTimeout, settlement: config.Settlement,
		backoff: config.Backoff.normalized(),
	}, nil
}

func (service *Service) Close() error {
	if service == nil {
		return nil
	}
	service.lifecycle.Lock()
	defer service.lifecycle.Unlock()
	if service.closed {
		return nil
	}
	service.closed = true
	if service.safety != nil {
		return service.safety.Close()
	}
	return nil
}

func (*Service) String() string   { return "[forward service]" }
func (*Service) GoString() string { return "[forward service]" }
func (*Service) LogValue() slog.Value {
	return slog.StringValue("[forward service]")
}

// ListModels returns only currently routable personal models and currently
// available charity models. The two repositories return safe logical values;
// this layer deduplicates, stably sorts, and enforces the complete wire bound.
func (service *Service) ListModels(ctx context.Context, userID int64) (ModelList, error) {
	if service == nil || ctx == nil || userID <= 0 {
		return ModelList{}, ErrInternal
	}
	service.lifecycle.RLock()
	defer service.lifecycle.RUnlock()
	if service.closed || service.personal == nil || service.charity == nil {
		return ModelList{}, ErrClosed
	}
	now, err := service.nowUnix()
	if err != nil {
		return ModelList{}, err
	}
	personalModels, err := service.personal.ListRoutableModels(ctx, userID, MaxCallerModels)
	if err != nil {
		return ModelList{}, err
	}
	charityModels, err := service.charity.ListAvailableModels(ctx, now, MaxCallerModels)
	if err != nil {
		return ModelList{}, err
	}
	if len(personalModels)+len(charityModels) > MaxCallerModels {
		return ModelList{}, routing.ErrResourceLimit
	}

	byID := make(map[string]Model, len(personalModels)+len(charityModels))
	for _, value := range personalModels {
		if !validListedModel(value, false) {
			return ModelList{}, ErrInternal
		}
		byID[value.FullName] = Model{ID: value.FullName, Object: "model", Created: value.CreatedAt, OwnedBy: value.Provider}
	}
	for _, value := range charityModels {
		if !validListedModel(value, true) {
			return ModelList{}, ErrInternal
		}
		if _, duplicate := byID[value.FullName]; duplicate {
			return ModelList{}, ErrInternal
		}
		byID[value.FullName] = Model{ID: value.FullName, Object: "model", Created: value.CreatedAt, OwnedBy: value.Provider}
	}
	models := make([]Model, 0, len(byID))
	for _, value := range byID {
		models = append(models, value)
	}
	sort.Slice(models, func(left, right int) bool { return models[left].ID < models[right].ID })
	response := ModelList{Object: "list", Data: models}
	if !modelListWithinLimit(response) {
		return ModelList{}, routing.ErrResourceLimit
	}
	return response, nil
}

// Chat performs the complete request rail and owns all terminal wire writes.
// The caller has already passed CallerKey and process-wide flow admission.
func (service *Service) Chat(
	ctx context.Context,
	writer http.ResponseWriter,
	userID int64,
	request *openai.ChatRequest,
	body []byte,
	mediaType string,
	language string,
) {
	if service == nil || ctx == nil || writer == nil || userID <= 0 || request == nil {
		writeFailure(writer, platformFailure(httperr.CodeInternal, "internal error"))
		return
	}
	service.lifecycle.RLock()
	defer service.lifecycle.RUnlock()
	if service.closed {
		writeFailure(writer, platformFailure(httperr.CodeServiceUnavailable, "service unavailable"))
		return
	}

	parent := ctx
	bounded, cancel := context.WithTimeout(parent, service.timeout)
	defer cancel()

	admission, attemptRequest, clearAttempt, err := service.preflight(bounded, userID, request)
	if clearAttempt != nil {
		defer clearAttempt()
	}
	if err != nil {
		service.writePreAcceptanceFailure(parent, writer, nil, nil, err, admission.charity, language)
		return
	}
	routeKind := debug.RouteOpenAIChat
	if admission.charity {
		routeKind = debug.RouteCharityChat
	}
	decision, err := service.debug.DecideAfterAdmission(bounded, debug.CaptureInput{
		UserID: userID, RouteKind: routeKind, Model: request.Model, Stream: request.Stream,
		MediaType: mediaType, Body: body, Charity: admission.charity,
		IdentityCertain: true, Language: language,
	})
	if err != nil {
		service.writePreAcceptanceFailure(parent, writer, nil, nil, err, admission.charity, language)
		return
	}
	if decision.DryIntercepted() {
		decision.WriteDryResult(writer)
		return
	}

	executionContext := bounded
	var suppressor *debug.CallerSuppressor
	if decision.Active {
		if decision.Mode != debug.ModeLive || decision.Trace == nil {
			service.writePreAcceptanceFailure(parent, writer, nil, decision.Trace, ErrInternal, admission.charity, language)
			return
		}
		executionContext = decision.Trace.Context()
		suppressor, err = debug.NewCallerSuppressor(parent, writer, decision.Language)
		if err != nil {
			service.writePreAcceptanceFailure(parent, writer, nil, decision.Trace, err, admission.charity, language)
			return
		}
	}

	plan, err := service.snapshot(executionContext, userID, request.Model, attemptRequest, admission, decision)
	if err != nil {
		service.writePreAcceptanceFailure(parent, writer, suppressor, decision.Trace, err, admission.charity, language)
		return
	}
	acceptInput := claim.AcceptInput{
		UserID: userID, Route: plan.route, ModelSnapshot: plan.fullName,
		AttemptLimit: len(plan.candidates), ReservedMilli: plan.reservedMilli,
		CharityModelID: charityModelID(plan),
	}
	if plan.charity {
		decisionNow := plan.decisionNow
		acceptInput.CharityDecisionNow = &decisionNow
	}
	accepted, err := service.claims.Accept(executionContext, acceptInput)
	if err != nil {
		service.writePreAcceptanceFailure(parent, writer, suppressor, decision.Trace, err, admission.charity, language)
		return
	}

	run := service.runAttempts(parent, executionContext, writer, suppressor, decision.Trace, userID, attemptRequest, plan, accepted)
	disposition := claim.AccountingRelease
	if run.dispatched {
		disposition = claim.AccountingCommit
	}
	actualCharge := int64(0)
	settleContext, settleCancel := context.WithTimeout(context.WithoutCancel(parent), service.settlement)
	defer settleCancel()
	if plan.charity && disposition == claim.AccountingCommit {
		actualCharge, err = service.charityCharges.CalculateRequestCharge(settleContext, accepted.ID, disposition)
		if err != nil {
			run.err = err
			run.terminalBlocked = true
		}
	}

	caller, failure := service.classifyCaller(parent, executionContext, plan, run)
	if decision.Active && run.dispatched {
		caller, failure = service.completeDebugTrace(parent, decision, plan.route, run, actualCharge, caller, failure)
	} else if decision.Active {
		caller, failure = service.completeUndispatchedDebug(parent, executionContext, decision, caller, failure)
	}
	if run.err != nil && caller.Class != claim.ResultCancelled && !(decision.Active && run.dispatched) {
		caller = claim.CallerResult{Class: claim.ResultFailed, Status: http.StatusInternalServerError, ErrorCode: httperr.CodeInternal}
		value := platformFailure(httperr.CodeInternal, "internal error")
		failure = &value
	}

	if !run.terminalBlocked {
		_, completionErr := service.claims.CompleteRequest(settleContext, claim.CompleteRequestInput{
			RequestID: accepted.ID, Caller: caller, Disposition: disposition, ActualChargeMilli: actualCharge,
		})
		if completionErr != nil {
			run.err = completionErr
			run.terminalBlocked = true
		}
	}

	if decision.Active {
		service.writeDebugTerminal(parent, suppressor, decision, run, caller, failure)
		return
	}
	if parent.Err() != nil || caller.Class == claim.ResultCancelled || run.hasResult && run.result.Committed {
		return
	}
	if run.err != nil {
		writeFailure(writer, platformFailure(httperr.CodeInternal, "internal error"))
		return
	}
	if failure != nil {
		writeFailure(writer, *failure)
	}
}

func (service *Service) preflight(ctx context.Context, userID int64, request *openai.ChatRequest) (logicalAdmission, *openai.ChatRequest, func(), error) {
	if strings.HasPrefix(request.Model, charityModelPrefix) {
		now, err := service.nowUnix()
		if err != nil {
			return logicalAdmission{charity: true}, request, nil, err
		}
		value, err := service.charity.Preflight(ctx, userID, request.Model, request, now)
		if err != nil {
			return logicalAdmission{charity: true}, request, nil, err
		}
		admission := logicalAdmission{
			charity: true, modelID: value.ModelID, fullName: value.FullName, strategy: "ordered",
			silentRetry: true, flatten: value.FlattenToolCalls, reservedMilli: value.ReservedMilli,
			decisionNow: now,
		}
		return prepareModelPolicy(request, admission)
	}
	value, err := service.personal.Preflight(ctx, userID, request.Model)
	if err != nil {
		return logicalAdmission{}, request, nil, err
	}
	admission := logicalAdmission{
		modelID: value.ModelID, fullName: value.FullName, strategy: value.RouteStrategy,
		silentRetry: value.SilentRetry, flatten: value.FlattenToolCalls,
	}
	return prepareModelPolicy(request, admission)
}

func prepareModelPolicy(request *openai.ChatRequest, admission logicalAdmission) (logicalAdmission, *openai.ChatRequest, func(), error) {
	if !validAdmission(admission) {
		return admission, request, nil, ErrInternal
	}
	if !admission.flatten {
		return admission, request, nil, nil
	}
	transformed, err := request.ReverseFlatten()
	if err != nil || transformed == nil {
		return admission, request, nil, openai.ErrInvalidRequest
	}
	return admission, transformed, transformed.Clear, nil
}

func (service *Service) snapshot(
	ctx context.Context,
	userID int64,
	identifier string,
	request *openai.ChatRequest,
	admission logicalAdmission,
	decision debug.CaptureDecision,
) (executionPlan, error) {
	plan := executionPlan{logicalAdmission: admission}
	if admission.charity {
		connectorTypes := service.supportedCharityConnectorTypes(request, admission.flatten)
		if len(connectorTypes) == 0 {
			return executionPlan{}, openai.ErrInvalidRequest
		}
		value, err := service.charity.Snapshot(ctx, admission.modelID, admission.decisionNow, connectorTypes)
		if err != nil {
			return executionPlan{}, err
		}
		if !sameCharitySnapshot(admission, value) {
			return executionPlan{}, ErrInternal
		}
		plan.candidates = append([]RouteCandidate(nil), value.Candidates...)
		plan.route = claim.RouteCharityChat
		plan.purpose = claim.PurposeCharity
	} else {
		value, err := service.personal.Snapshot(ctx, userID, identifier)
		if err != nil {
			return executionPlan{}, err
		}
		if !samePersonalSnapshot(admission, value, userID) {
			return executionPlan{}, ErrInternal
		}
		plan.candidates = append([]RouteCandidate(nil), value.Candidates...)
		plan.route = claim.RouteOpenAIChat
		plan.purpose = claim.PurposeSelf
		if decision.Active {
			plan.purpose = claim.PurposeDebugLive
		}
	}
	if decision.Active && decision.ClaimPurpose != plan.purpose {
		return executionPlan{}, ErrInternal
	}

	capable := make([]RouteCandidate, 0, len(plan.candidates))
	capabilityByType := make(map[connectorcontract.Type]bool, len(service.connectors))
	for _, candidate := range plan.candidates {
		if !service.validCandidate(candidate, admission.charity) {
			return executionPlan{}, ErrInternal
		}
		if admission.flatten && candidate.ConnectorType != connectorcontract.TypeOpenAICompatible {
			return executionPlan{}, ErrInternal
		}
		supported, evaluated := capabilityByType[candidate.ConnectorType]
		if !evaluated {
			supported = service.registry.SupportsRequest(candidate.ConnectorType, request)
			capabilityByType[candidate.ConnectorType] = supported
		}
		if supported {
			candidate.Policy.FlattenToolCalls = admission.flatten
			capable = append(capable, candidate)
		}
	}
	clear(plan.candidates)
	if len(capable) == 0 {
		return executionPlan{}, openai.ErrInvalidRequest
	}
	ordered, err := orderCandidates(plan.strategy, capable)
	clear(capable)
	if err != nil {
		return executionPlan{}, err
	}
	plan.candidates = ordered
	return plan, nil
}

func (service *Service) supportedCharityConnectorTypes(request *openai.ChatRequest, flatten bool) []connectorcontract.Type {
	if service == nil || service.registry == nil || request == nil {
		return nil
	}
	connectorTypes := make([]connectorcontract.Type, 0, len(service.connectors))
	for connectorType, instance := range service.connectors {
		if instance == nil || flatten && connectorType != connectorcontract.TypeOpenAICompatible {
			continue
		}
		if service.registry.SupportsRequest(connectorType, request) {
			connectorTypes = append(connectorTypes, connectorType)
		}
	}
	sort.Slice(connectorTypes, func(left, right int) bool { return connectorTypes[left] < connectorTypes[right] })
	return connectorTypes
}

func (service *Service) runAttempts(
	parent context.Context,
	executionContext context.Context,
	writer http.ResponseWriter,
	suppressor *debug.CallerSuppressor,
	trace *debug.TraceHandle,
	userID int64,
	request *openai.ChatRequest,
	plan executionPlan,
	accepted claim.Request,
) attemptRun {
	var run attemptRun
	for index, candidate := range plan.candidates {
		if executionContext.Err() != nil {
			break
		}
		origin, err := canonicalOrigin(candidate.CanonicalBaseURL)
		if err != nil {
			run.err = err
			break
		}
		safety, err := service.safety.Generate(userID, origin)
		origin = ""
		if err != nil {
			run.err = err
			break
		}

		handle, err := service.claims.Claim(executionContext, claim.ClaimInput{
			RequestID: accepted.ID, ActorUserID: userID, AttemptSeq: index + 1, Purpose: plan.purpose,
			Candidate: claim.Candidate{
				EndpointID: candidate.EndpointID, EndpointKeyID: candidate.EndpointKeyID,
				ConnectorType: candidate.ConnectorType, CanonicalBaseURL: candidate.CanonicalBaseURL,
				UpstreamModelID: candidate.UpstreamModelID, Policy: candidate.Policy,
			},
			DonationKeyID: candidate.DonationKeyID,
		})
		if err != nil {
			if errors.Is(err, claim.ErrNotFound) {
				continue
			}
			run.err = err
			break
		}

		dispatch, err := service.claims.TakeForDispatch(executionContext, handle)
		if err != nil {
			run.handleDispatchFailure(parent, service, handle, err)
			break
		}
		if dispatch == nil {
			run.handleDispatchFailure(parent, service, handle, claim.ErrCredentialUnavailable)
			break
		}
		run.dispatched = true
		attemptRequest := request.CloneForAttempt()
		if attemptRequest == nil {
			dispatch.Clear()
			run.completeSynthetic(parent, service, handle, "request snapshot unavailable")
			break
		}
		policy := dispatch.Policy()
		policy.SafetyIdentifier = safety
		if suppressor != nil {
			if err := suppressor.MarkDispatched(); err != nil {
				attemptRequest.Clear()
				dispatch.Clear()
				run.completeSynthetic(parent, service, handle, "debug capture canceled")
				break
			}
			if trace == nil || trace.MarkDispatched() != nil {
				attemptRequest.Clear()
				dispatch.Clear()
				run.completeSynthetic(parent, service, handle, "debug capture canceled")
				break
			}
		}
		credential, ok := dispatch.TakeCredential()
		dispatch.Clear()
		if !ok || credential == nil {
			attemptRequest.Clear()
			if credential != nil {
				credential.Clear()
			}
			run.completeSynthetic(parent, service, handle, "credential unavailable")
			break
		}
		sink := writer
		if suppressor != nil {
			sink = suppressor.UpstreamWriter()
		}
		protocolConnector := service.connectors[candidate.ConnectorType]
		if protocolConnector == nil {
			credential.Clear()
			attemptRequest.Clear()
			run.completeSynthetic(parent, service, handle, "connector unavailable")
			break
		}
		result := protocolConnector.Attempt(executionContext, connector.AttemptInput{
			Target: dispatch.Target(), Credential: credential, Ingress: attemptRequest,
			Policy: policy, Sink: sink, Observer: service.observer,
			TraceID: traceID(trace, accepted.ID), AttemptIndex: index,
		})
		credential.Clear()
		attemptRequest.Clear()
		result.Diagnostic = diagnostic.Bound(result.Diagnostic)
		if executionContext.Err() != nil && !result.Success && !result.Committed {
			result = endedExecutionResult(parent, executionContext)
		}
		if !validAttemptResult(result) {
			result = connectorcontract.AttemptResult{Failure: connectorcontract.FailureInternal, Diagnostic: "invalid connector result"}
		}
		run.result, run.hasResult = result, true
		settleContext, cancel := context.WithTimeout(context.WithoutCancel(parent), service.settlement)
		_, completionErr := service.claims.CompleteAttempt(settleContext, handle, attemptOutcome(result))
		cancel()
		if completionErr != nil {
			run.err = completionErr
			run.terminalBlocked = true
			break
		}
		if result.Success || !retryable(result, plan.silentRetry) || index == len(plan.candidates)-1 {
			break
		}
		if !service.backoff.wait(executionContext, index) {
			run.result = endedExecutionResult(parent, executionContext)
			run.hasResult = true
			break
		}
	}
	if !run.dispatched && run.err == nil {
		if executionContext.Err() != nil {
			if errors.Is(executionContext.Err(), context.DeadlineExceeded) {
				value := platformFailure(httperr.CodeServiceUnavailable, "service unavailable")
				run.failure = &value
			}
		} else {
			value := platformFailure(httperr.CodeUnboundModel, "model has no usable binding")
			if plan.charity {
				value = platformFailure(httperr.CodeRateLimited, "charity candidates are temporarily unavailable")
			}
			run.failure = &value
		}
	}
	return run
}

func (run *attemptRun) handleDispatchFailure(parent context.Context, service *Service, handle claim.Handle, dispatchErr error) {
	settleContext, cancel := context.WithTimeout(context.WithoutCancel(parent), service.settlement)
	defer cancel()
	_, releaseErr := service.claims.ReleaseUndispatched(settleContext, handle)
	if releaseErr == nil {
		run.err = dispatchErr
		return
	}
	if errors.Is(releaseErr, claim.ErrAlreadyDispatched) {
		run.dispatched = true
		result := connectorcontract.AttemptResult{Failure: connectorcontract.FailureInternal, Diagnostic: "credential unavailable"}
		run.result, run.hasResult = result, true
		_, completionErr := service.claims.CompleteAttempt(settleContext, handle, attemptOutcome(result))
		if completionErr != nil {
			run.err = completionErr
			run.terminalBlocked = true
		} else {
			run.err = dispatchErr
		}
		return
	}
	run.err = releaseErr
	run.terminalBlocked = true
}

func (run *attemptRun) completeSynthetic(parent context.Context, service *Service, handle claim.Handle, diagnosticText string) {
	result := connectorcontract.AttemptResult{Failure: connectorcontract.FailureInternal, Diagnostic: diagnosticText}
	run.result, run.hasResult, run.dispatched = result, true, true
	settleContext, cancel := context.WithTimeout(context.WithoutCancel(parent), service.settlement)
	defer cancel()
	_, run.err = service.claims.CompleteAttempt(settleContext, handle, attemptOutcome(result))
	run.terminalBlocked = run.err != nil
}

func (service *Service) classifyCaller(parent, executionContext context.Context, plan executionPlan, run attemptRun) (claim.CallerResult, *wireFailure) {
	if parent.Err() != nil || executionCancelled(parent, executionContext) {
		return claim.CallerResult{Class: claim.ResultCancelled}, nil
	}
	if run.failure != nil {
		return callerFromFailure(*run.failure), run.failure
	}
	if run.err != nil && !run.hasResult {
		value := platformFailure(httperr.CodeInternal, "internal error")
		return callerFromFailure(value), &value
	}
	if !run.hasResult {
		value := platformFailure(httperr.CodeUnboundModel, "model has no usable binding")
		return callerFromFailure(value), &value
	}
	if run.result.Success {
		return claim.CallerResult{Class: claim.ResultSuccess, Status: http.StatusOK}, nil
	}
	if run.result.Failure == connectorcontract.FailureCanceled || run.result.Failure == connectorcontract.FailureSink {
		return claim.CallerResult{Class: claim.ResultCancelled}, nil
	}
	value := failureForAttempt(run.result, plan.charity)
	return callerFromFailure(value), &value
}

func (service *Service) completeDebugTrace(
	parent context.Context,
	decision debug.CaptureDecision,
	route claim.RouteKind,
	run attemptRun,
	actualCharge int64,
	caller claim.CallerResult,
	failure *wireFailure,
) (claim.CallerResult, *wireFailure) {
	if decision.Trace == nil {
		value := platformFailure(httperr.CodeInternal, "internal error")
		return callerFromFailure(value), &value
	}
	if parent != nil && parent.Err() != nil {
		_ = decision.Trace.CompleteCancelled(decision.Language)
		return claim.CallerResult{Class: claim.ResultCancelled}, nil
	}
	if executionCancelled(parent, decision.Trace.Context()) {
		_ = decision.Trace.CompleteCancelled(decision.Language)
		value := platformFailure(httperr.CodeDebugLiveCancelled, "Debug live request was cancelled")
		return callerFromFailure(value), &value
	}
	if run.hasResult {
		projected := debugUpstreamResult(run.result, route, actualCharge, service.mustNowUnix())
		if err := decision.Trace.RecordUpstream(projected); err != nil {
			value := platformFailure(httperr.CodeDebugLiveCancelled, "Debug live request was cancelled")
			return callerFromFailure(value), &value
		}
	}
	if err := decision.Trace.CompleteLiveCaptured(decision.Language); err != nil {
		value := platformFailure(httperr.CodeDebugLiveCancelled, "Debug live request was cancelled")
		return callerFromFailure(value), &value
	}
	value := platformFailure(httperr.CodeDebugLiveResultCaptured, "The upstream response was captured by the Debug page")
	return callerFromFailure(value), &value
}

func (service *Service) completeUndispatchedDebug(
	parent, executionContext context.Context,
	decision debug.CaptureDecision,
	caller claim.CallerResult,
	failure *wireFailure,
) (claim.CallerResult, *wireFailure) {
	if parent != nil && parent.Err() != nil {
		return claim.CallerResult{Class: claim.ResultCancelled}, nil
	}
	if executionContext != nil && executionContext.Err() != nil && !errors.Is(executionContext.Err(), context.DeadlineExceeded) {
		value := platformFailure(httperr.CodeDebugLiveCancelled, "Debug live request was cancelled")
		return callerFromFailure(value), &value
	}
	if failure == nil || failure.code == httperr.CodeUpstream {
		value := platformFailure(httperr.CodeServiceUnavailable, "service unavailable")
		failure = &value
		caller = callerFromFailure(value)
	}
	if decision.Trace == nil {
		value := platformFailure(httperr.CodeInternal, "internal error")
		return callerFromFailure(value), &value
	}
	if err := decision.Trace.CompleteCaller(debugCallerResult(*failure, service.mustNowUnix())); err != nil {
		value := platformFailure(httperr.CodeDebugLiveCancelled, "Debug live request was cancelled")
		return callerFromFailure(value), &value
	}
	return caller, failure
}

func (service *Service) writeDebugTerminal(parent context.Context, suppressor *debug.CallerSuppressor, decision debug.CaptureDecision, run attemptRun, caller claim.CallerResult, failure *wireFailure) {
	if parent.Err() != nil || suppressor == nil {
		return
	}
	if !run.dispatched {
		_ = suppressor.WritePlatformBeforeDispatch(func(writer http.ResponseWriter) {
			if caller.ErrorCode == httperr.CodeDebugLiveCancelled {
				debug.WriteLiveCancelled(writer, decision.Language, false)
				return
			}
			if failure == nil {
				writeFailure(writer, platformFailure(httperr.CodeInternal, "internal error"))
				return
			}
			writeFailure(writer, *failure)
		})
		return
	}
	if run.terminalBlocked {
		// A persisted dispatch still has to converge through recovery. The live
		// caller remains on the fixed capture wire and never receives internals.
		if caller.ErrorCode == httperr.CodeDebugLiveCancelled {
			_ = suppressor.WriteCancelled()
		} else {
			_ = suppressor.WriteCaptured()
		}
		return
	}
	if caller.ErrorCode == httperr.CodeDebugLiveCancelled {
		_ = suppressor.WriteCancelled()
		return
	}
	_ = suppressor.WriteCaptured()
}

func (service *Service) writePreAcceptanceFailure(
	parent context.Context,
	writer http.ResponseWriter,
	suppressor *debug.CallerSuppressor,
	trace *debug.TraceHandle,
	err error,
	charity bool,
	language string,
) {
	if parent != nil && parent.Err() != nil {
		return
	}
	if trace != nil && executionCancelled(parent, trace.Context()) {
		debug.WriteLiveCancelled(writer, language, false)
		return
	}
	failure := failureForError(err, charity)
	var rejected *charityrouting.ContentTooShortError
	if errors.As(err, &rejected) && db.ValidateOpaqueID(rejected.RequestID, "req_") {
		writer.Header().Set("X-Request-ID", rejected.RequestID)
	}
	if trace != nil {
		result := debugCallerResult(failure, service.mustNowUnix())
		if completeErr := trace.CompleteCaller(result); completeErr != nil {
			debug.WriteLiveCancelled(writer, language, false)
			return
		}
	}
	if suppressor != nil {
		_ = suppressor.WritePlatformBeforeDispatch(func(target http.ResponseWriter) { writeFailure(target, failure) })
		return
	}
	writeFailure(writer, failure)
}

func (service *Service) validCandidate(candidate RouteCandidate, charity bool) bool {
	if candidate.EndpointID <= 0 || candidate.EndpointKeyID <= 0 ||
		(charity != (candidate.DonationKeyID > 0)) ||
		!validBoundedText(candidate.CanonicalBaseURL, 4096, 4096) ||
		!validBoundedText(candidate.UpstreamModelID, 512, 4096) {
		return false
	}
	descriptor, ok := service.registry.Descriptor(candidate.ConnectorType)
	return ok && service.connectors[candidate.ConnectorType] != nil && descriptor.Capabilities == service.connectors[candidate.ConnectorType].Capabilities()
}

func (service *Service) nowUnix() (int64, error) {
	if service == nil || service.now == nil {
		return 0, ErrInternal
	}
	value := service.now().Unix()
	if value < 0 || value > maxUnixSecond {
		return 0, ErrInternal
	}
	return value, nil
}

func (service *Service) mustNowUnix() int64 {
	value, err := service.nowUnix()
	if err != nil {
		return 0
	}
	return value
}

func validAdmission(value logicalAdmission) bool {
	maxRunes := maxPersonalRunes
	if value.charity {
		maxRunes = maxCharityRunes
	}
	return value.modelID > 0 && validBoundedText(value.fullName, maxRunes, 4096) &&
		(value.strategy == "ordered" || value.strategy == "random") &&
		value.reservedMilli >= 0 && value.reservedMilli <= claim.MaxMoneyMilli &&
		(!value.charity || value.decisionNow >= 0 && value.decisionNow <= maxUnixSecond) &&
		(value.charity == strings.HasPrefix(value.fullName, charityModelPrefix))
}

func samePersonalSnapshot(preflight logicalAdmission, snapshot PersonalSnapshot, userID int64) bool {
	return !preflight.charity && snapshot.ModelID == preflight.modelID && snapshot.OwnerUserID == userID &&
		snapshot.FullName == preflight.fullName && snapshot.RouteStrategy == preflight.strategy &&
		snapshot.SilentRetry == preflight.silentRetry && snapshot.FlattenToolCalls == preflight.flatten
}

func sameCharitySnapshot(preflight logicalAdmission, snapshot CharitySnapshot) bool {
	return preflight.charity && snapshot.ModelID == preflight.modelID && snapshot.FullName == preflight.fullName &&
		snapshot.FlattenToolCalls == preflight.flatten && snapshot.ReservedMilli == preflight.reservedMilli
}

func charityModelID(plan executionPlan) int64 {
	if plan.charity {
		return plan.modelID
	}
	return 0
}

func attemptOutcome(result connectorcontract.AttemptResult) claim.AttemptOutcome {
	kind := claim.ResultSynthetic
	if result.UpstreamStatus != 0 {
		kind = claim.ResultResponse
	}
	return claim.AttemptOutcome{
		Kind: kind, UpstreamStatus: result.UpstreamStatus, Diagnostic: result.Diagnostic,
		Usage: result.Usage, ProtocolSuccess: result.Success, ResponseStarted: result.Committed,
	}
}

func endedExecutionResult(parent, executionContext context.Context) connectorcontract.AttemptResult {
	if parent != nil && parent.Err() == nil && executionContext != nil && errors.Is(executionContext.Err(), context.DeadlineExceeded) {
		return connectorcontract.AttemptResult{
			Failure: connectorcontract.FailureUpstream, ClientStatus: http.StatusGatewayTimeout,
			Diagnostic: "forward request timed out",
		}
	}
	return connectorcontract.AttemptResult{Failure: connectorcontract.FailureCanceled, Diagnostic: "request canceled"}
}

func executionCancelled(parent, executionContext context.Context) bool {
	if parent != nil && parent.Err() != nil {
		return true
	}
	if executionContext == nil || executionContext.Err() == nil {
		return false
	}
	return !errors.Is(executionContext.Err(), context.DeadlineExceeded)
}

func traceID(trace *debug.TraceHandle, fallback string) string {
	if trace != nil && trace.TraceID() != "" {
		return trace.TraceID()
	}
	return fallback
}

func debugUpstreamResult(result connectorcontract.AttemptResult, route claim.RouteKind, chargeMilli, completedAt int64) debug.DebugUpstreamResult {
	kind := debug.ResultSynthetic
	var status *int
	if result.UpstreamStatus != 0 {
		kind = debug.ResultResponse
		value := result.UpstreamStatus
		status = &value
	}
	usage := result.Usage
	total := new(big.Int)
	for _, value := range []int64{usage.UncachedInputTokens, usage.CacheWriteInputTokens, usage.CacheReadInputTokens, usage.OutputTokens} {
		total.Add(total, big.NewInt(value))
	}
	projection := debug.DebugUpstreamResult{
		ResultKind: kind, StatusCode: status,
		Usage: debug.LogUsage{
			UncachedInputTokens:   strconv.FormatInt(usage.UncachedInputTokens, 10),
			CacheWriteInputTokens: strconv.FormatInt(usage.CacheWriteInputTokens, 10),
			CacheReadInputTokens:  strconv.FormatInt(usage.CacheReadInputTokens, 10),
			OutputTokens:          strconv.FormatInt(usage.OutputTokens, 10), TotalTokens: total.String(),
			UsageUnknown: !usage.Present, Charge: credits.FormatAmount(chargeMilli),
		},
		CompletedAt: completedAt,
	}
	if route == claim.RouteCharityChat {
		status := http.StatusBadGateway
		if result.Success {
			status = http.StatusOK
		}
		projection.ResultKind = debug.ResultSynthetic
		projection.StatusCode = &status
		return projection
	}
	if result.Diagnostic != "" {
		value := diagnostic.Bound(result.Diagnostic)
		projection.Diag = &value
	}
	return projection
}

func validListedModel(value ListedModel, charity bool) bool {
	maxRunes := maxPersonalRunes
	if charity {
		maxRunes = maxCharityRunes
	}
	return value.ModelID > 0 && value.CreatedAt >= 0 &&
		validBoundedText(value.Provider, maxProviderRunes, 4096) &&
		validBoundedText(value.FullName, maxRunes, 4096) &&
		(charity == strings.HasPrefix(value.FullName, charityModelPrefix))
}

func validBoundedText(value string, maxRunes, maxBytes int) bool {
	if value == "" || !utf8.ValidString(value) || utf8.RuneCountInString(value) > maxRunes || len(value) > maxBytes {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) || r == 0x7f {
			return false
		}
	}
	first, _ := utf8.DecodeRuneInString(value)
	last, _ := utf8.DecodeLastRuneInString(value)
	return !unicode.IsSpace(first) && !unicode.IsSpace(last)
}

func nilInterfaceValue(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}

func failureForError(err error, charity bool) wireFailure {
	switch {
	case err == nil:
		return platformFailure(httperr.CodeInternal, "internal error")
	case errors.Is(err, context.Canceled):
		return platformFailure(httperr.CodeServiceUnavailable, "service unavailable")
	case errors.Is(err, context.DeadlineExceeded):
		return platformFailure(httperr.CodeServiceUnavailable, "service unavailable")
	case errors.Is(err, maintenance.ErrMaintenanceOn):
		return platformFailure(httperr.CodeMaintenance, "maintenance mode is active")
	case errors.Is(err, routing.ErrNotFound), errors.Is(err, charityrouting.ErrNotFound):
		return platformFailure(httperr.CodeNotFound, "model not found")
	case errors.Is(err, routing.ErrAmbiguousIdentity), errors.Is(err, routing.ErrInvalidIdentity),
		errors.Is(err, openai.ErrInvalidRequest), errors.Is(err, charityrouting.ErrInvalidRequest):
		return platformFailure(httperr.CodeInvalidRequest, "invalid request")
	case errors.Is(err, charityrouting.ErrEntropyUnavailable):
		return platformFailure(httperr.CodeServiceUnavailable, "service unavailable")
	case errors.Is(err, routing.ErrUnbound), errors.Is(err, charityrouting.ErrUnavailable):
		return platformFailure(httperr.CodeUnboundModel, "model has no usable binding")
	case errors.Is(err, routing.ErrResourceLimit), errors.Is(err, charityrouting.ErrResourceLimit):
		return platformFailure(httperr.CodeResourceLimitExceeded, "resource limit exceeded")
	case errors.Is(err, charityrouting.ErrFeatureDisabled):
		return platformFailure(httperr.CodeFeatureDisabled, "charity is disabled")
	case errors.Is(err, charityrouting.ErrCharitySuspended):
		return platformFailure(httperr.CodeCharitySuspended, "charity eligibility is suspended")
	case errors.Is(err, charityrouting.ErrInsufficientCredits):
		return platformFailure(httperr.CodeInsufficientCredits, "insufficient credits")
	case errors.Is(err, ledger.ErrInsufficientBalance):
		return platformFailure(httperr.CodeInsufficientCredits, "insufficient credits")
	case errors.Is(err, ledger.ErrCapacityExhausted), errors.Is(err, ledger.ErrRetryable):
		return platformFailure(httperr.CodeServiceUnavailable, "service unavailable")
	case errors.Is(err, charityrouting.ErrContentTooShort):
		message := "charity content is too short"
		var tooShort *charityrouting.ContentTooShortError
		if errors.As(err, &tooShort) && tooShort != nil && tooShort.Actual >= 0 && tooShort.Minimum >= 0 {
			message += ": " + strconv.Itoa(tooShort.Actual) + " < " + strconv.Itoa(tooShort.Minimum)
		}
		return platformFailure(httperr.CodeContentTooShort, message)
	case errors.Is(err, claim.ErrNotFound):
		return platformFailure(httperr.CodeUnauthorized, "authentication required")
	case errors.Is(err, charityrouting.ErrUnauthorized):
		return platformFailure(httperr.CodeUnauthorized, "authentication required")
	case errors.Is(err, claim.ErrDependencyUnavailable), errors.Is(err, debug.ErrClosed), errors.Is(err, debug.ErrCapacity):
		return platformFailure(httperr.CodeServiceUnavailable, "service unavailable")
	case errors.Is(err, ErrClosed):
		return platformFailure(httperr.CodeServiceUnavailable, "service unavailable")
	default:
		_ = charity
		return platformFailure(httperr.CodeInternal, "internal error")
	}
}

func failureForAttempt(result connectorcontract.AttemptResult, charity bool) wireFailure {
	if charity {
		return upstreamWireFailure(http.StatusBadGateway, "upstream request failed", "", false)
	}
	if result.Failure == connectorcontract.FailureInternal {
		return platformFailure(httperr.CodeInternal, "internal error")
	}
	status := http.StatusBadGateway
	if result.UpstreamStatus >= http.StatusBadRequest && result.UpstreamStatus <= 499 {
		status = result.UpstreamStatus
	} else if result.ClientStatus == http.StatusGatewayTimeout || isTimeoutResult(result) {
		status = http.StatusGatewayTimeout
	}
	return upstreamWireFailure(status, "upstream request failed", result.Diagnostic, true)
}

func callerFromFailure(failure wireFailure) claim.CallerResult {
	return claim.CallerResult{Class: claim.ResultFailed, Status: failure.status, ErrorCode: failure.code}
}

func debugCallerResult(failure wireFailure, completedAt int64) debug.DebugCallerResult {
	code := failure.code
	return debug.DebugCallerResult{
		HTTPStatus: failure.status, ErrorCode: &code, Source: debug.SourcePlatform,
		Message: "[NonbiriAPI] " + strings.ReplaceAll(failure.message, "[NonbiriAPI] ", ""), CompletedAt: completedAt,
	}
}
