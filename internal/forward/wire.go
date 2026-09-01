package forward

import (
	"encoding/json"
	"net/http"

	"github.com/waiting-here/NonbiriAPI/internal/httperr"
)

type wireFailure struct {
	code            string
	message         string
	status          int
	diagnostic      string
	upstreamContext bool
}

func platformFailure(code, message string) wireFailure {
	return wireFailure{code: code, message: message, status: statusForCode(code)}
}

func upstreamWireFailure(status int, message, diagnostic string, exposeContext bool) wireFailure {
	if status < http.StatusBadRequest || status > 499 && status != http.StatusBadGateway && status != http.StatusGatewayTimeout {
		status = http.StatusBadGateway
	}
	return wireFailure{
		code: httperr.CodeUpstream, message: message, status: status,
		diagnostic: diagnostic, upstreamContext: exposeContext,
	}
}

func writeFailure(writer http.ResponseWriter, failure wireFailure) {
	if writer == nil {
		return
	}
	value := httperr.New(failure.code, failure.message)
	if failure.upstreamContext && failure.diagnostic != "" {
		value = value.WithDiag(failure.diagnostic)
	}
	if failure.code == httperr.CodeUpstream && failure.upstreamContext {
		httperr.WriteUpstreamError(writer, value, failure.status)
		return
	}
	httperr.WriteError(writer, value)
}

func statusForCode(code string) int {
	switch code {
	case httperr.CodeInvalidRequest, httperr.CodeContentTooShort:
		return http.StatusBadRequest
	case httperr.CodeUnauthorized:
		return http.StatusUnauthorized
	case httperr.CodeForbidden, httperr.CodeElevationRequired, httperr.CodeInsufficientCredits,
		httperr.CodeFeatureDisabled, httperr.CodeCharitySuspended, httperr.CodeCheckinCapReached:
		return http.StatusForbidden
	case httperr.CodeNotFound:
		return http.StatusNotFound
	case httperr.CodeConflict, httperr.CodeAlreadyCheckedIn, httperr.CodeDebugLiveCancelled:
		return http.StatusConflict
	case httperr.CodeMethodNotAllowed:
		return http.StatusMethodNotAllowed
	case httperr.CodeRateLimited:
		return http.StatusTooManyRequests
	case httperr.CodePayloadTooLarge:
		return http.StatusRequestEntityTooLarge
	case httperr.CodeUpstream:
		return http.StatusBadGateway
	case httperr.CodeUnboundModel, httperr.CodeMaintenance, httperr.CodeServiceUnavailable:
		return http.StatusServiceUnavailable
	case httperr.CodeResourceLimitExceeded, httperr.CodeDebugDryRunIntercepted, httperr.CodeDebugLiveResultCaptured:
		return http.StatusUnprocessableEntity
	case httperr.CodeResourceLocked:
		return http.StatusLocked
	default:
		return http.StatusInternalServerError
	}
}

func modelListWithinLimit(value ModelList) bool {
	encoded, err := json.Marshal(value)
	return err == nil && len(encoded) <= MaxModelListBytes
}

func writeModelList(writer http.ResponseWriter, value ModelList) {
	if writer == nil {
		return
	}
	encoded, err := json.Marshal(value)
	if err != nil || len(encoded) > MaxModelListBytes {
		writeFailure(writer, platformFailure(httperr.CodeResourceLimitExceeded, "resource limit exceeded"))
		return
	}
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-store")
	writer.WriteHeader(http.StatusOK)
	_, _ = writer.Write(encoded)
}
