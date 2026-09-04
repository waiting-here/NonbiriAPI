package resourcebridge

import (
	"context"
	"errors"
	"unicode"
	"unicode/utf8"

	"github.com/waiting-here/NonbiriAPI/internal/claim"
	"github.com/waiting-here/NonbiriAPI/internal/connector"
	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/diagnostic"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

const maxDiscoveryModels = 1000

const (
	diagnosticInvalidResult        = "model discovery returned invalid typed facts"
	diagnosticCredentialFailure    = "model discovery credential unavailable"
	diagnosticConnectorPanic       = "model discovery connector failed"
	diagnosticDiscoveryInterrupted = "model discovery interrupted"
)

type normalizedDiscovery struct {
	result  resources.DiscoveryClaimResult
	attempt claim.AttemptOutcome
}

// Discover creates one discovery claim, crosses the durable dispatch marker,
// and calls the supplied registered discoverer at most once.
func (r *Runtime) Discover(ctx context.Context, input resources.DiscoveryClaimInput) (resources.DiscoveryClaimResult, error) {
	if err := r.begin(); err != nil {
		return resources.DiscoveryClaimResult{}, err
	}
	defer r.end()
	if ctx == nil || !validDiscoveryInput(input) {
		return resources.DiscoveryClaimResult{}, ErrInvalidInput
	}
	if ctx.Err() != nil {
		return interruptedResult(), ErrInterrupted
	}

	request, handle, err := r.claims.ClaimDiscovery(ctx, claim.DiscoveryClaimInput{
		ActorUserID: input.OwnerUserID,
		Candidate: claim.Candidate{
			EndpointID:       input.EndpointID,
			EndpointKeyID:    input.EndpointKeyID,
			ConnectorType:    input.ConnectorType,
			CanonicalBaseURL: input.CanonicalBaseURL,
		},
	})
	if err != nil {
		if ctx.Err() != nil {
			return interruptedResult(), ErrInterrupted
		}
		return protocolResult(""), ErrUnavailable
	}

	dispatch, err := r.claims.TakeForDispatch(ctx, handle)
	if err != nil {
		failure := protocolResult(diagnosticCredentialFailure)
		if ctx.Err() != nil {
			failure = interruptedResult()
		}
		if terminalErr := r.finishWithoutDispatch(context.WithoutCancel(ctx), request.ID, handle, failure); terminalErr != nil {
			return failure, terminalErr
		}
		return failure, nil
	}
	defer dispatch.Clear()

	credential, ok := dispatch.TakeCredential()
	if !ok || credential == nil {
		failure := protocolResult(diagnosticCredentialFailure)
		if terminalErr := r.finishDispatched(context.WithoutCancel(ctx), request.ID, handle, normalizedFailure(failure, false, 0)); terminalErr != nil {
			return failure, terminalErr
		}
		return failure, nil
	}
	defer credential.Clear()

	typedResult, panicked := callDiscoverer(ctx, input.Discoverer, connectorcontract.DiscoveryInput{
		Backend:    r.backend,
		Target:     dispatch.Target(),
		Credential: credential,
	})
	var normalized normalizedDiscovery
	if panicked {
		normalized = normalizedFailure(protocolResult(diagnosticConnectorPanic), false, 0)
	} else {
		normalized = normalizeDiscoveryResult(ctx, typedResult)
	}
	if terminalErr := r.finishDispatched(context.WithoutCancel(ctx), request.ID, handle, normalized); terminalErr != nil {
		return normalized.result, terminalErr
	}
	return normalized.result, nil
}

func (r *Runtime) finishWithoutDispatch(ctx context.Context, requestID string, handle claim.Handle, failure resources.DiscoveryClaimResult) error {
	_, err := r.claims.ReleaseUndispatched(ctx, handle)
	switch {
	case err == nil, errors.Is(err, claim.ErrTerminal):
		return r.completeDiscoveryRequest(ctx, requestID)
	case errors.Is(err, claim.ErrAlreadyDispatched):
		return r.finishDispatched(ctx, requestID, handle, normalizedFailure(failure, false, 0))
	default:
		return ErrUnavailable
	}
}

func (r *Runtime) finishDispatched(ctx context.Context, requestID string, handle claim.Handle, normalized normalizedDiscovery) error {
	if _, err := r.claims.CompleteAttempt(ctx, handle, normalized.attempt); err != nil && !errors.Is(err, claim.ErrTerminal) {
		return ErrUnavailable
	}
	return r.completeDiscoveryRequest(ctx, requestID)
}

func (r *Runtime) completeDiscoveryRequest(ctx context.Context, requestID string) error {
	_, err := r.claims.CompleteRequest(ctx, claim.CompleteRequestInput{
		RequestID: requestID,
		Caller: claim.CallerResult{
			Class:  claim.ResultSuccess,
			Status: 202,
		},
		Disposition:       claim.AccountingNone,
		ActualChargeMilli: 0,
	})
	if err != nil {
		return ErrUnavailable
	}
	return nil
}

func callDiscoverer(ctx context.Context, discoverer connector.ModelDiscoverer, input connectorcontract.DiscoveryInput) (result connectorcontract.DiscoveryResult, panicked bool) {
	defer func() {
		if recover() != nil {
			result = connectorcontract.DiscoveryResult{}
			panicked = true
		}
	}()
	return discoverer.Discover(ctx, input), false
}

func normalizeDiscoveryResult(ctx context.Context, result connectorcontract.DiscoveryResult) normalizedDiscovery {
	factsValid := (result.ResponseReceived && result.UpstreamStatus >= 100 && result.UpstreamStatus <= 599) ||
		(!result.ResponseReceived && result.UpstreamStatus == 0)
	if ctx.Err() != nil {
		if !factsValid {
			return normalizedFailure(interruptedResult(), false, 0)
		}
		return normalizedFailure(interruptedResult(), result.ResponseReceived, result.UpstreamStatus)
	}
	if !factsValid {
		return normalizedFailure(protocolResult(diagnosticInvalidResult), false, 0)
	}
	if result.Succeeded() {
		if len(result.Models) > maxDiscoveryModels {
			return normalizedFailure(protocolResult(diagnosticInvalidResult), result.ResponseReceived, result.UpstreamStatus)
		}
		models := make([]resources.DiscoveredModel, len(result.Models))
		for index, model := range result.Models {
			if !validCatalogText(model.ID, 1, 512) || !validCatalogText(model.Provider, 0, 128) {
				return normalizedFailure(protocolResult(diagnosticInvalidResult), result.ResponseReceived, result.UpstreamStatus)
			}
			models[index] = resources.DiscoveredModel{UpstreamModelID: model.ID, Provider: model.Provider}
		}
		return normalizedDiscovery{
			result: resources.DiscoveryClaimResult{Succeeded: true, Models: models},
			attempt: claim.AttemptOutcome{
				Kind:            claim.ResultResponse,
				UpstreamStatus:  result.UpstreamStatus,
				ProtocolSuccess: true,
				ResponseStarted: true,
			},
		}
	}
	if len(result.Models) != 0 || result.Failure == connectorcontract.DiscoveryFailureNone ||
		!validTypedFailure(result.Failure) || !validConnectorDiagnostic(result.Diagnostic) {
		return normalizedFailure(protocolResult(diagnosticInvalidResult), result.ResponseReceived, result.UpstreamStatus)
	}
	diagnosticText := diagnostic.Bound(result.Diagnostic)
	failure := resources.DiscoveryClaimResult{
		FailureClass:   mapFailure(result.Failure),
		SafeDiagnostic: diagnosticText,
	}
	return normalizedFailure(failure, result.ResponseReceived, result.UpstreamStatus)
}

func normalizedFailure(result resources.DiscoveryClaimResult, responseReceived bool, status int) normalizedDiscovery {
	kind := claim.ResultSynthetic
	if responseReceived {
		kind = claim.ResultResponse
	}
	return normalizedDiscovery{
		result: result,
		attempt: claim.AttemptOutcome{
			Kind:            kind,
			UpstreamStatus:  status,
			Diagnostic:      result.SafeDiagnostic,
			ProtocolSuccess: false,
			ResponseStarted: responseReceived,
		},
	}
}

func validDiscoveryInput(input resources.DiscoveryClaimInput) bool {
	return db.ValidateOpaqueID(input.OperationID, "op_") && input.OwnerUserID > 0 &&
		input.EndpointID > 0 && input.EndpointKeyID > 0 && validConnectorType(string(input.ConnectorType)) &&
		validBaseURLText(input.CanonicalBaseURL) && !nilInterface(input.Discoverer)
}

func validTypedFailure(value connectorcontract.DiscoveryFailureKind) bool {
	switch value {
	case connectorcontract.DiscoveryFailureAuth, connectorcontract.DiscoveryFailureRateLimit,
		connectorcontract.DiscoveryFailureTimeout, connectorcontract.DiscoveryFailureProtocol,
		connectorcontract.DiscoveryFailureTransport, connectorcontract.DiscoveryFailureInterrupted:
		return true
	default:
		return false
	}
}

func mapFailure(value connectorcontract.DiscoveryFailureKind) resources.DiscoveryFailureClass {
	switch value {
	case connectorcontract.DiscoveryFailureAuth:
		return resources.DiscoveryFailureAuth
	case connectorcontract.DiscoveryFailureRateLimit:
		return resources.DiscoveryFailureRateLimit
	case connectorcontract.DiscoveryFailureTimeout:
		return resources.DiscoveryFailureTimeout
	case connectorcontract.DiscoveryFailureTransport:
		return resources.DiscoveryFailureTransport
	case connectorcontract.DiscoveryFailureInterrupted:
		return resources.DiscoveryFailureInterrupted
	default:
		return resources.DiscoveryFailureProtocol
	}
}

func validConnectorDiagnostic(value string) bool {
	if len(value) > diagnostic.MaxBytes || !utf8.ValidString(value) {
		return false
	}
	for _, runeValue := range value {
		if unicode.IsControl(runeValue) || runeValue == 0x7f {
			return false
		}
	}
	return true
}

func validCatalogText(value string, minRunes, maxRunes int) bool {
	if !utf8.ValidString(value) {
		return false
	}
	count := utf8.RuneCountInString(value)
	if count < minRunes || count > maxRunes {
		return false
	}
	if value != "" {
		first, _ := utf8.DecodeRuneInString(value)
		last, _ := utf8.DecodeLastRuneInString(value)
		if unicode.IsSpace(first) || unicode.IsSpace(last) {
			return false
		}
	}
	for _, runeValue := range value {
		if unicode.IsControl(runeValue) || runeValue == 0x7f {
			return false
		}
	}
	return true
}

func interruptedResult() resources.DiscoveryClaimResult {
	return resources.DiscoveryClaimResult{
		FailureClass:   resources.DiscoveryFailureInterrupted,
		SafeDiagnostic: diagnosticDiscoveryInterrupted,
	}
}

func protocolResult(message string) resources.DiscoveryClaimResult {
	return resources.DiscoveryClaimResult{
		FailureClass:   resources.DiscoveryFailureProtocol,
		SafeDiagnostic: diagnostic.Bound(message),
	}
}
