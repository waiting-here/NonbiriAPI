package debug

import (
	"encoding/json"

	"github.com/waiting-here/NonbiriAPI/internal/connector"
	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
)

// NewRequestObserver creates a bounded sidecar whose callback is already
// scoped to this user's current Debug generation. The sidecar is safe to pass
// through a request context; TryObserve never waits for this callback.
func (h *Hub) NewRequestObserver(userID int64, sessionID string, generation uint64) (*connector.SafeObserver, error) {
	return h.NewRequestObserverForTrace(userID, sessionID, generation, "")
}

// NewRequestObserverForTrace admits one sidecar only after the corresponding
// logical trace has been retained.  The admission is deliberately narrower
// than the process/session memory ceilings: SafeObserver owns a bounded queue
// outside Hub accounting, so its count is tied to MaxTraces and released only
// when the worker actually drains. A failed admission is reported to the
// active data path so it can remain fail-closed in dry-safe mode; it is never
// a reason to bypass an active Debug session.
func (h *Hub) NewRequestObserverForTrace(userID int64, sessionID string, generation uint64, traceID string) (*connector.SafeObserver, error) {
	if h == nil || userID <= 0 || sessionID == "" || generation == 0 {
		return nil, ErrInvalid
	}
	h.mu.Lock()
	session := h.sessions[userID]
	valid := !h.closed && session != nil && !session.closed && session.id == sessionID &&
		session.generation == generation && session.mode == ModeLive && h.validDeadlineLocked(session)
	if valid && traceID != "" {
		_, valid = session.traces[traceID]
	}
	if !valid || session.observerActive >= MaxTraces || h.observerCount >= h.maxObservers {
		h.mu.Unlock()
		return nil, ErrCapacity
	}
	session.observerActive++
	h.observerCount++
	h.mu.Unlock()
	observer, err := connector.NewSafeObserver(MaxSubscriberQueue, h.ConnectorObserver(userID, sessionID, generation))
	if err != nil {
		h.releaseObserverLease(session)
		return nil, err
	}
	observer.SetCloseHook(func() { h.releaseObserverLease(session) })
	return observer, nil
}

func (h *Hub) releaseObserverLease(session *hubSession) {
	if h == nil || session == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if session.observerActive > 0 {
		session.observerActive--
	}
	if h.observerCount > 0 {
		h.observerCount--
	}
}

// ConnectorObserver returns the non-blocking observer adapter for one frozen
// live request.  Connector.SafeObserver invokes the returned callback on its
// sidecar worker, so redaction/projection work cannot delay the caller's
// status, body, flush, cancellation, or accounting path.  The callback only
// carries connector-neutral safe metadata; it never receives an endpoint,
// binding, origin, key, credential, or upstream body.
func (h *Hub) ConnectorObserver(userID int64, sessionID string, generation uint64) connector.ObserverHandler {
	return func(observation connector.Observation) {
		if h == nil || userID <= 0 || sessionID == "" {
			return
		}
		traceID := safeIdentifier(observation.TraceID, maxTraceIdentifierBytes)
		if traceID == "" {
			return
		}
		attemptIndex := observation.AttemptIndex
		if attemptIndex < 0 || attemptIndex > 1_000_000 {
			attemptIndex = 0
		}
		// Hub assigns the next monotonic revision at enqueue time. Observer
		// delivery is intentionally asynchronous, so an explicit revision
		// derived from attempt index could race the wrapper's final upsert.
		revision := uint64(0)
		terminal := "dispatching"
		if observation.Kind == connector.ObservationAttemptFinished {
			switch {
			case observation.Success:
				terminal = "completed"
			case observation.Failure == connectorcontract.FailureCanceled:
				terminal = "cancelled"
			case observation.Committed:
				terminal = "incomplete"
			default:
				terminal = "failed"
			}
		}
		commitStage := "before_commit"
		if observation.Committed {
			commitStage = "after_commit"
		}
		var usage any
		if observation.Kind == connector.ObservationAttemptFinished && observation.Usage.Present {
			usage = observation.Usage
		}
		effective := map[string]any{
			"connector": map[string]any{
				"state": "evaluated",
				"name":  safeIdentifier(string(observation.Connector), 64),
			},
			"store":              observerStoreProjection(observation),
			"flatten_tool_calls": observerFlattenProjection(observation),
			"safety_identifier":  observerSafetyProjection(observation),
			"attempt_index":      attemptIndex,
			"commit_stage":       commitStage,
		}
		// This is intentionally a partial projection. Do not route it through
		// buildTracePayload: that helper's ordinary-request defaults (route,
		// model, raw body) would overwrite fields from the complete wrapper
		// projection when an asynchronous retry arrives later.
		payload := map[string]any{
			"mode":          ModeLive,
			"effective":     json.RawMessage(marshalJSON(effective)),
			"attempt_index": attemptIndex,
			"commit_stage":  commitStage,
			"attempt_state": terminal,
			// An attempt projection is a replacement of the previous attempt's
			// transient result, not an append-only history. Explicit nulls clear an
			// upstream error/usage from a silent retry when the next attempt has
			// started or succeeded without those fields.
			"error": nil,
			"usage": nil,
		}
		if usage != nil {
			payload["usage"] = usage
		}
		if category := observationFailureCategory(observation); category != "" {
			payload["error"] = category
		}
		// Attempt completion is not logical request completion. Silent retries
		// can emit a finished failure before a later attempt, and the wrapper's
		// final protocol result is the sole authority allowed to make a trace
		// terminal/evictable.
		h.publishCaller(userID, sessionID, generation, Trace{ID: traceID, Revision: revision, Payload: payload, Merge: true})
	}
}

func observerStoreProjection(observation connector.Observation) map[string]any {
	// Store is a non-secret policy bit, so the caller value may be shown as a
	// bool when it was strictly decoded. An omitted/unknown value is null;
	// never guess false from mere field absence or malformed input.
	var callerValue any
	if observation.CallerStoreValueKnown {
		callerValue = observation.CallerStoreValue
	}
	effective := map[string]any{}
	switch {
	case observation.StoreForcedFalse:
		effective["state"] = "applied"
		effective["value"] = false
	case observation.CallerStoreValueKnown:
		effective["state"] = "unchanged"
		effective["value"] = observation.CallerStoreValue
	default:
		effective["state"] = "unchanged"
	}
	return map[string]any{"caller_value": callerValue, "effective": effective}
}

func observerFlattenProjection(observation connector.Observation) map[string]any {
	state := "unchanged"
	if observation.FlattenApplied {
		state = "applied"
	}
	return map[string]any{"caller_value": nil, "effective": map[string]any{"state": state}}
}

func observerSafetyProjection(observation connector.Observation) map[string]any {
	state := "unchanged"
	if observation.SafetyIdentifierApplied {
		state = "applied"
	}
	return map[string]any{
		"caller_value": nil,
		"effective":    map[string]any{"state": state, "value_hidden": true},
	}
}

func observationFailureCategory(observation connector.Observation) string {
	if observation.Kind != connector.ObservationAttemptFinished || observation.Success {
		return ""
	}
	switch observation.Failure {
	case connectorcontract.FailureUpstream:
		return "upstream"
	case connectorcontract.FailureInternal:
		return "internal"
	case connectorcontract.FailureCanceled:
		return "caller_cancelled"
	case connectorcontract.FailureSink:
		return "sink"
	default:
		return "unknown"
	}
}
