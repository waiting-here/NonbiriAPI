package forward

import (
	"context"
	"errors"
	"net/http"

	"github.com/waiting-here/NonbiriAPI/internal/charityrouting"
	"github.com/waiting-here/NonbiriAPI/internal/claim"
	"github.com/waiting-here/NonbiriAPI/internal/connector"
	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/debug"
	"github.com/waiting-here/NonbiriAPI/internal/diagnostic"
	"github.com/waiting-here/NonbiriAPI/internal/donationquota"
	"github.com/waiting-here/NonbiriAPI/internal/egress"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
	"github.com/waiting-here/NonbiriAPI/internal/smartbackoff"
)

type attemptRun struct {
	dispatched      bool
	responseStarted bool
	result          connectorcontract.AttemptResult
	hasResult       bool
	failure         *wireFailure
	err             error
	terminalBlocked bool
}

func (service *Service) runAttempts(
	parent context.Context,
	executionContext context.Context,
	writer http.ResponseWriter,
	suppressor *debug.CallerSuppressor,
	trace *debug.TraceHandle,
	userID int64,
	request *validatedRequest,
	plan executionPlan,
	accepted claim.Request,
) attemptRun {
	var run attemptRun
	keyLimited := false
	remaining := append([]RouteCandidate(nil), plan.candidates...)
	balancedPlan := plan.charity && plan.charityStrategy == charityrouting.RouteCacheBalanced ||
		!plan.charity && plan.strategy == "cache_balanced"
	safetyByBaseURL := make(map[string]string)
	if balancedPlan {
		for _, candidate := range plan.candidates {
			if _, present := safetyByBaseURL[candidate.CanonicalBaseURL]; present {
				continue
			}
			origin, err := canonicalOrigin(candidate.CanonicalBaseURL)
			if err != nil {
				return attemptRun{err: err}
			}
			safety, err := service.safety.Generate(userID, origin)
			if err != nil {
				return attemptRun{err: err}
			}
			safetyByBaseURL[candidate.CanonicalBaseURL] = safety
		}
	}
	for index := range plan.candidates {
		if executionContext.Err() != nil {
			break
		}
		balanced := balancedPlan
		if balanced && len(remaining) == 0 {
			break
		}
		candidate := plan.candidates[index]
		safety := safetyByBaseURL[candidate.CanonicalBaseURL]
		if !balanced {
			origin, err := canonicalOrigin(candidate.CanonicalBaseURL)
			if err != nil {
				run.err = err
				break
			}
			safety, err = service.safety.Generate(userID, origin)
			if err != nil {
				run.err = err
				break
			}
		}
		claimInput := claim.ClaimInput{
			RequestID: accepted.ID, ActorUserID: userID, AttemptSeq: index + 1, Purpose: plan.purpose,
			Candidate: claim.Candidate{
				EndpointID: candidate.EndpointID, EndpointKeyID: candidate.EndpointKeyID,
				ConnectorType: candidate.ConnectorType, CanonicalBaseURL: candidate.CanonicalBaseURL,
				UpstreamModelID: candidate.UpstreamModelID, Policy: candidate.Policy,
			},
			DonationKeyID: candidate.DonationKeyID,
		}
		if plan.charity {
			claimInput.OutputTokenFloor = outputFloor(candidate)
		} else {
			claimInput.PersonalModelID = plan.modelID
		}
		if balanced {
			claimInput.Candidate = claim.Candidate{}
			claimInput.DonationKeyID = 0
			for _, choice := range remaining {
				var floor int64
				if plan.charity {
					floor = outputFloor(choice)
				}
				claimInput.BalancedCandidates = append(claimInput.BalancedCandidates, claim.BalancedCandidate{
					Candidate: claim.Candidate{EndpointID: choice.EndpointID, EndpointKeyID: choice.EndpointKeyID,
						ConnectorType: choice.ConnectorType, CanonicalBaseURL: choice.CanonicalBaseURL,
						UpstreamModelID: choice.UpstreamModelID, Policy: choice.Policy},
					DonationKeyID:    choice.DonationKeyID,
					OutputTokenFloor: floor,
				})
			}
		}
		handle, err := service.claims.Claim(executionContext, claimInput)
		if err != nil {
			if plan.charity && errors.Is(err, claim.ErrForbidden) || errors.Is(err, claim.ErrModelUnavailable) {
				if plan.charity || !run.dispatched {
					value := failureForError(err, plan.charity)
					run.failure = &value
				}
				break
			}
			if errors.Is(err, claim.ErrKeyRateLimited) || errors.Is(err, donationquota.ErrLimited) {
				keyLimited = true
				if balanced {
					break
				}
				continue
			}
			if errors.Is(err, claim.ErrNotFound) {
				if balanced {
					break
				}
				continue
			}
			if errors.Is(err, donationquota.ErrCapacity) || errors.Is(err, claim.ErrRoutingBusy) {
				if !run.dispatched {
					value := failureForError(err, plan.charity)
					run.failure = &value
				}
				break
			}
			run.err = err
			break
		}
		if balanced {
			selected := -1
			for position, choice := range remaining {
				if choice.EndpointKeyID == handle.EndpointKeyID() && choice.DonationKeyID == handle.DonationKeyID() &&
					choice.UpstreamModelID == handle.Target().UpstreamModel() {
					selected = position
					candidate = choice
					break
				}
			}
			if selected < 0 {
				run.handleDispatchFailure(parent, service, handle, claim.ErrInvariant)
				break
			}
			remaining = append(remaining[:selected], remaining[selected+1:]...)
			safety = safetyByBaseURL[candidate.CanonicalBaseURL]
		}

		dispatch, err := service.claims.TakeForDispatch(executionContext, handle)
		if err != nil {
			released := run.handleDispatchFailure(parent, service, handle, err)
			if released && (errors.Is(err, donationquota.ErrLimited) || errors.Is(err, claim.ErrKeyRateLimited) || (plan.charity && errors.Is(err, claim.ErrNotFound))) {
				keyLimited = keyLimited || errors.Is(err, donationquota.ErrLimited) || errors.Is(err, claim.ErrKeyRateLimited)
				run.err = nil
				continue
			}
			break
		}
		if dispatch == nil {
			run.handleDispatchFailure(parent, service, handle, claim.ErrCredentialUnavailable)
			break
		}
		run.dispatched = true
		prepared := candidate.prepared
		attemptRequest := prepared.source(request).CloneForAttempt()
		if attemptRequest == nil {
			dispatch.Clear()
			run.completeUndeliveredSynthetic(parent, service, handle, "request snapshot unavailable")
			break
		}
		backoffTrial := false
		if info, ok := dispatch.(interface {
			IsBackoffTrial() bool
		}); ok {
			backoffTrial = info.IsBackoffTrial()
		}
		policy := dispatch.Policy()
		policy.GatewayModel = candidate.Policy.GatewayModel
		policy.SafetyIdentifier = safety
		clearAdaptation := prepared.applyPolicy(&policy)
		if request.chat == nil {
			policy.ForceStoreFalse, policy.FlattenToolCalls = false, false
		}
		if suppressor != nil {
			if err := suppressor.MarkDispatched(); err != nil {
				clearAdaptation()
				attemptRequest.Clear()
				dispatch.Clear()
				run.completeUndeliveredSynthetic(parent, service, handle, "debug capture canceled")
				break
			}
			if trace == nil || trace.MarkDispatched() != nil {
				clearAdaptation()
				attemptRequest.Clear()
				dispatch.Clear()
				run.completeUndeliveredSynthetic(parent, service, handle, "debug capture canceled")
				break
			}
		}
		credential, ok := dispatch.TakeCredential()
		dispatch.Clear()
		if !ok || credential == nil {
			clearAdaptation()
			attemptRequest.Clear()
			if credential != nil {
				credential.Clear()
			}
			run.completeUndeliveredSynthetic(parent, service, handle, "credential unavailable")
			break
		}
		sink := writer
		if suppressor != nil {
			sink = suppressor.UpstreamWriter()
		}
		callerSink := sink
		var converted *transportResponse
		if request.chat != nil && attemptRequest.Stream != request.Stream {
			converted = newTransportResponse(attemptRequest.Stream)
			sink = converted
		}
		var responseStart *responseStartWriter
		if plan.charity || request.chat == nil {
			sink, responseStart = checkpointResponseWriter(sink, func() error {
				checkpointContext, cancel := context.WithTimeout(context.WithoutCancel(parent), service.settlement)
				defer cancel()
				return service.claims.MarkResponseStarted(checkpointContext, handle, responseStart.upstreamStatus)
			})
			responseStart.acceptedStreams = plan.charity
			responseStart.backoffTrial = backoffTrial
			responseStart.now = service.now
			responseStart.observe = func(observation connectorcontract.ResponseObservation) error {
				checkpointContext, cancel := context.WithTimeout(context.WithoutCancel(parent), service.settlement)
				defer cancel()
				return service.claims.ObserveResponseHeaders(checkpointContext, handle, observation)
			}
		}
		protocolConnector := service.connectors[candidate.ConnectorType]
		if protocolConnector == nil {
			clearAdaptation()
			credential.Clear()
			attemptRequest.Clear()
			run.completeUndeliveredSynthetic(parent, service, handle, "connector unavailable")
			break
		}
		attemptContext := egress.WithCloudflareProxy(executionContext, plan.cloudflareProxyEnabled)
		attemptContext = smartbackoff.WithCharityCall(attemptContext, plan.charity)
		var backoffRules []smartbackoff.Rule
		if info, ok := dispatch.(interface{ BackoffRules() []smartbackoff.Rule }); ok {
			backoffRules = info.BackoffRules()
		}
		attemptContext = smartbackoff.WithRules(attemptContext, backoffRules)
		if service.errorScope != nil {
			attemptContext = service.errorScope(attemptContext, accepted.ID, index+1)
		}
		result := protocolConnector.Attempt(attemptContext, connector.AttemptInput{
			Operation: attemptRequest.operation,
			Target:    dispatch.Target(), Credential: credential, Ingress: attemptRequest.chat, Embedding: attemptRequest.embedding, Image: attemptRequest.image, Decision: attemptRequest.decision,
			Policy: policy, Sink: sink, Observer: service.observer,
			TraceID: traceID(trace, accepted.ID), AttemptIndex: index,
		})
		clearAdaptation()
		credential.Clear()
		attemptRequest.Clear()
		if converted != nil {
			if validAttemptResult(result) {
				result = converted.complete(executionContext, callerSink, callerIncludesUsage(request.chat), result)
			}
			converted.clear()
		}
		result.Diagnostic = diagnostic.Bound(result.Diagnostic)
		if responseStart != nil && responseStart.started {
			run.responseStarted = true
		}
		if executionContext.Err() != nil && !result.Success && !result.Committed && !run.responseStarted && !connectorcontract.ValidOutcome(result.StreakDisposition, result.FailureOrigin) {
			result = endedExecutionResult(parent, executionContext)
		}
		if !validAttemptResult(result) {
			result = connectorcontract.AttemptResult{Failure: connectorcontract.FailureInternal, Diagnostic: "invalid connector result"}
		}
		run.result, run.hasResult = result, true
		settleContext, cancel := context.WithTimeout(context.WithoutCancel(attemptContext), service.settlement)
		outcome := attemptOutcome(result)
		acceptedByCharityUpstream := plan.charity && result.UpstreamStatus == http.StatusOK
		if plan.charity && responseStart != nil && responseStart.retryAfterObserved && !outcome.ProtocolSuccess {
			outcome.StreakDisposition = connectorcontract.StreakNeutral
		}
		if acceptedByCharityUpstream {
			// Key availability follows HTTP acceptance. Protocol completion and
			// the durable billing checkpoint retain their own facts.
			outcome.StreakDisposition = connectorcontract.StreakSuccess
		}
		if responseStart != nil && (responseStart.started || responseStart.upstreamStatus == http.StatusOK) {
			outcome.ResponseStarted = true
		}
		_, completionErr := service.claims.CompleteAttempt(settleContext, handle, outcome)
		cancel()
		if completionErr != nil {
			run.err = completionErr
			run.terminalBlocked = true
			break
		}
		if acceptedByCharityUpstream || run.responseStarted || result.Success || !retryable(result, plan.silentRetry) || index == len(plan.candidates)-1 {
			break
		}
		if !service.backoff.wait(executionContext, index) {
			run.result = endedExecutionResult(parent, executionContext)
			run.hasResult = true
			break
		}
	}
	if !run.dispatched && run.err == nil && run.failure == nil {
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
			if keyLimited {
				value = platformFailure(httperr.CodeRateLimited, "model connections are temporarily at capacity")
			}
			run.failure = &value
		}
	}
	return run
}

func (run *attemptRun) handleDispatchFailure(parent context.Context, service *Service, handle claim.Handle, dispatchErr error) bool {
	settleContext, cancel := context.WithTimeout(context.WithoutCancel(parent), service.settlement)
	defer cancel()
	_, releaseErr := service.claims.ReleaseUndispatched(settleContext, handle)
	if releaseErr == nil {
		if errors.Is(dispatchErr, claim.ErrForbidden) || errors.Is(dispatchErr, claim.ErrModelUnavailable) {
			value := failureForError(dispatchErr, true)
			run.failure = &value
			return true
		}
		if errors.Is(dispatchErr, donationquota.ErrCapacity) {
			if !run.dispatched {
				value := failureForError(dispatchErr, true)
				run.failure = &value
			}
			return true
		}
		run.err = dispatchErr
		return true
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
		return false
	}
	run.err = releaseErr
	run.terminalBlocked = true
	return false
}

func (run *attemptRun) completeSynthetic(parent context.Context, service *Service, handle claim.Handle, diagnosticText string) {
	result := connectorcontract.AttemptResult{Failure: connectorcontract.FailureInternal, Diagnostic: diagnosticText}
	run.result, run.hasResult, run.dispatched = result, true, true
	settleContext, cancel := context.WithTimeout(context.WithoutCancel(parent), service.settlement)
	defer cancel()
	_, run.err = service.claims.CompleteAttempt(settleContext, handle, attemptOutcome(result))
	run.terminalBlocked = run.err != nil
}

func (run *attemptRun) completeUndeliveredSynthetic(parent context.Context, service *Service, handle claim.Handle, diagnosticText string) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), service.settlement)
	revokeErr := service.claims.RevokeUndelivered(ctx, handle)
	cancel()
	run.completeSynthetic(parent, service, handle, diagnosticText)
	if revokeErr != nil {
		run.err = errors.Join(run.err, revokeErr)
		run.terminalBlocked = true
	}
}
