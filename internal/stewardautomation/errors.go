package stewardautomation

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/charityrouting"
	"github.com/waiting-here/NonbiriAPI/internal/donation"
	"github.com/waiting-here/NonbiriAPI/internal/donationquota"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

type stepError struct {
	step  string
	cause error
}

func (e *stepError) Error() string        { return "automation step failed" }
func (e *stepError) Unwrap() error        { return e.cause }
func atStep(step string, err error) error { return &stepError{step, err} }

func safeError(err error) (string, string) {
	code, message := httperr.CodeInternal, "operation failed; no diagnostic details are exposed"
	switch {
	case errors.Is(err, errInvalid), errors.Is(err, resources.ErrInvalidRequest), errors.Is(err, donation.ErrInvalidRequest), errors.Is(err, charityrouting.ErrInvalidRequest), errors.Is(err, donationquota.ErrInvalid):
		code, message = httperr.CodeInvalidRequest, "invalid input or incompatible settings"
	case errors.Is(err, authz.ErrUnauthorized), errors.Is(err, resources.ErrUnauthorized), errors.Is(err, donation.ErrUnauthorized), errors.Is(err, charityrouting.ErrUnauthorized):
		code, message = httperr.CodeUnauthorized, "CallerKey is no longer authorized"
	case errors.Is(err, authz.ErrForbidden), errors.Is(err, resources.ErrForbidden), errors.Is(err, donation.ErrForbidden), errors.Is(err, charityrouting.ErrForbidden):
		code, message = httperr.CodeForbidden, "an active steward account is required"
	case errors.Is(err, resources.ErrNotFound), errors.Is(err, donation.ErrNotFound), errors.Is(err, charityrouting.ErrNotFound):
		code, message = httperr.CodeNotFound, "resource is missing or unavailable to this steward"
	case errors.Is(err, resources.ErrConflict), errors.Is(err, donation.ErrConflict), errors.Is(err, charityrouting.ErrConflict), errors.Is(err, idempotency.ErrConflict), errors.Is(err, idempotency.ErrInProgress), errors.Is(err, donationquota.ErrConflict):
		code, message = httperr.CodeConflict, "resource changed, or the idempotency key was reused with different input"
	case errors.Is(err, resources.ErrResourceLimit), errors.Is(err, charityrouting.ErrResourceLimit), errors.Is(err, donationquota.ErrCapacity):
		code, message = httperr.CodeResourceLimitExceeded, "configured resource limit reached"
	case errors.Is(err, resources.ErrResourceLocked):
		code, message = httperr.CodeResourceLocked, "resource is locked for review"
	case errors.Is(err, donation.ErrFeatureDisabled), errors.Is(err, charityrouting.ErrFeatureDisabled):
		code, message = httperr.CodeFeatureDisabled, "donation intake is closed"
	case errors.Is(err, errDiscovery):
		code, message = "discovery_failed", "the current upstream model discovery did not succeed"
		var failure *discoveryFailureError
		if errors.As(err, &failure) {
			switch failure.class {
			case "auth":
				message = "upstream rejected the key during model discovery"
			case "rate_limit":
				message = "upstream rate limited model discovery"
			case "timeout":
				message = "upstream model discovery timed out"
			case "protocol":
				message = "upstream returned an invalid model list"
			case "transport":
				message = "upstream model discovery failed at the network boundary"
			case "interrupted":
				message = "model discovery was interrupted"
			}
		}
	case errors.Is(err, errModelMissing):
		code, message = "model_not_found", "the current upstream model list does not contain the requested model"
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		code, message = "incomplete", "operation did not complete before cancellation or timeout; retry this item"
	case errors.Is(err, resources.ErrUnavailable), errors.Is(err, donation.ErrUnavailable), errors.Is(err, charityrouting.ErrUnavailable):
		code, message = httperr.CodeServiceUnavailable, "service is temporarily unavailable"
	}
	var step *stepError
	if errors.As(err, &step) {
		message = step.step + ": " + message
	}
	return code, message
}

func writeError(w http.ResponseWriter, err error) {
	code, message := safeError(err)
	if code == "incomplete" {
		httperrValue := httperr.New(httperr.CodeServiceUnavailable, message)
		httperrValue.Source = httperr.SourcePlatform
		// The timeout status applies only to this synchronous control operation.
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusGatewayTimeout)
		_ = json.NewEncoder(w).Encode(httperr.Envelope{Error: httperrValue})
		return
	}
	httperr.WriteError(w, httperr.New(code, message))
}
