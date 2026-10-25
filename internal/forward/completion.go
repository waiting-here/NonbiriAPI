package forward

import (
	"context"
	"errors"
	"net/http"

	"github.com/waiting-here/NonbiriAPI/internal/charityrouting"
	"github.com/waiting-here/NonbiriAPI/internal/claim"
	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/debug"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
	"github.com/waiting-here/NonbiriAPI/internal/requestattempt"
)

func (service *Service) classifyCaller(parent, executionContext context.Context, plan executionPlan, run attemptRun) (claim.CallerResult, *wireFailure) {
	// A client can close after receiving the complete response while settlement
	// is still running. That late disconnect cannot undo successful delivery.
	if (parent.Err() != nil || executionCancelled(parent, executionContext)) && (!run.hasResult || !run.result.Success) {
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

// writePreAcceptanceFailure finishes any Debug caller record before emitting
// the platform failure. A canceled caller gets no write; a live suppressor
// retains control of the response until the pre-dispatch outcome is settled.
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
	var rejected *charityrouting.ContentTooShortError
	recordedShort := errors.As(err, &rejected) && db.ValidateOpaqueID(rejected.RequestID, "req_")
	if trace != nil && executionCancelled(parent, trace.Context()) {
		if !recordedShort {
			debug.WriteLiveCancelled(writer, language, false)
			return
		}
		// Recording this rejection may itself ban the account and terminate its
		// debug session. Preserve the committed rejection and request identity.
		trace = nil
	}
	failure := failureForError(err, charity)
	requestattempt.Detail(parent, failure.detail)
	if recordedShort {
		writer.Header().Set("X-Request-ID", rejected.RequestID)
	}
	if trace != nil {
		result := debugCallerResult(failure, service.mustNowUnix())
		if completeErr := trace.CompleteCaller(result); completeErr != nil && !recordedShort {
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
