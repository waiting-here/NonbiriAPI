package debug

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/waiting-here/NonbiriAPI/internal/claim"
	"github.com/waiting-here/NonbiriAPI/internal/diagnostic"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
)

const maxSnapshotTraceBytes = MaxEventBytes - 8*1024

// CaptureInput is intentionally named for its required position in the chat
// pipeline. Callers must invoke DecideAfterAdmission only after CallerKey,
// account, flow-control, body/JSON, model, and policy admission. The value has
// no candidate, credential, claim, egress, accounting, log, or health
// dependency, making a Dry return structurally able to precede all of them.
type CaptureInput struct {
	UserID          int64
	RouteKind       RouteKind
	Model           string
	Stream          bool
	MediaType       string
	Body            []byte
	Charity         bool
	IdentityCertain bool
	Language        string
}

type CaptureDecision struct {
	Active       bool
	Mode         Mode
	ClaimPurpose claim.Purpose
	Trace        *TraceHandle
	Language     string
}

func (decision CaptureDecision) DryIntercepted() bool {
	return decision.Active && decision.Mode == ModeDry
}

// WriteDryResult writes the frozen non-stream response even when the admitted
// request asked for streaming. It does not invoke any execution dependency.
func (decision CaptureDecision) WriteDryResult(writer http.ResponseWriter) {
	WriteDryIntercepted(writer, decision.Language)
}

type TraceHandle struct {
	hub       *Hub
	userID    int64
	sessionID string
	traceID   string
}

func (handle *TraceHandle) TraceID() string {
	if handle == nil {
		return ""
	}
	return handle.traceID
}

func (handle *TraceHandle) Context() context.Context {
	if handle == nil || handle.hub == nil {
		return context.Background()
	}
	handle.hub.mu.Lock()
	defer handle.hub.mu.Unlock()
	current := handle.hub.activeByUser[handle.userID]
	if current == nil || current.id != handle.sessionID {
		ctx, cancel := context.WithCancelCause(context.Background())
		cancel(ErrSessionEnded)
		return ctx
	}
	record := current.traces[handle.traceID]
	if record == nil {
		ctx, cancel := context.WithCancelCause(context.Background())
		cancel(ErrSessionEnded)
		return ctx
	}
	return record.ctx
}

func (handle *TraceHandle) WasDispatched() bool {
	if handle == nil || handle.hub == nil {
		return false
	}
	handle.hub.mu.Lock()
	defer handle.hub.mu.Unlock()
	current := handle.hub.activeByUser[handle.userID]
	if current == nil || current.id != handle.sessionID {
		return false
	}
	record := current.traces[handle.traceID]
	return record != nil && record.dispatched
}

// DecideAfterAdmission freezes the session mode for this request and creates
// its bounded trace. Identity uncertainty forces Dry; a definite loss of
// authority terminates the session and returns no active capture.
func (hub *Hub) DecideAfterAdmission(ctx context.Context, input CaptureInput) (CaptureDecision, error) {
	if hub == nil || ctx == nil || input.UserID <= 0 || !input.RouteKind.valid() ||
		!utf8.ValidString(input.Model) || utf8.RuneCountInString(input.Model) > 512 ||
		(input.MediaType != "" && !safeMediaType(input.MediaType)) ||
		(input.Charity != (input.RouteKind == RouteCharityChat)) {
		return CaptureDecision{}, ErrInvalid
	}
	language := normalizeLanguage(input.Language)

	// Capture the identity binding without holding the Hub while an external
	// verifier performs its read. The same session ID is rechecked afterward.
	hub.mu.Lock()
	if hub.closed {
		hub.mu.Unlock()
		return CaptureDecision{}, ErrClosed
	}
	current := hub.activeByUser[input.UserID]
	if current == nil || current.ended {
		hub.mu.Unlock()
		return CaptureDecision{Language: language}, nil
	}
	sessionID, binding := current.id, current.identityBinding
	verifier := hub.config.verifier
	hub.mu.Unlock()

	identity := IdentityUncertain
	if input.IdentityCertain && verifier != nil {
		verified, err := verifier.VerifyDebugIdentity(ctx, input.UserID, binding)
		if err == nil {
			identity = verified
		}
	}

	hub.mu.Lock()
	defer hub.mu.Unlock()
	if hub.closed {
		return CaptureDecision{}, ErrClosed
	}
	current = hub.activeByUser[input.UserID]
	if current == nil || current.ended {
		return CaptureDecision{Language: language}, nil
	}
	// Replace creates the successor while holding this same lock. A request
	// that observed the predecessor before the external identity check must
	// never fall through to ordinary forwarding; capture it conservatively in
	// the successor's default-Dry generation.
	replacedWhileVerifying := current.id != sessionID
	if replacedWhileVerifying && identity == IdentityRevoked {
		identity = IdentityUncertain
	}
	switch identity {
	case IdentityRevoked:
		hub.endSessionLocked(current, EndAuthRevoked)
		return CaptureDecision{Language: language}, nil
	case IdentityBanned:
		hub.endSessionLocked(current, EndAccountBanned)
		return CaptureDecision{Language: language}, nil
	case IdentityDeleted:
		hub.endSessionLocked(current, EndAccountDeleted)
		return CaptureDecision{Language: language}, nil
	case IdentityActive, IdentityUncertain:
	default:
		identity = IdentityUncertain
	}
	now := hub.config.now()
	if !now.Before(current.expiresAt) {
		hub.endSessionLocked(current, EndAbsoluteExpired)
		return CaptureDecision{Language: language}, nil
	}
	mode := current.mode
	if replacedWhileVerifying || identity != IdentityActive || !input.IdentityCertain {
		mode = ModeDry
	}
	record, err := hub.beginTraceLocked(ctx, current, input)
	if err != nil {
		// Capacity failure remains a Dry execution decision. No execution
		// dependency is reachable, although the exhausted session cannot retain
		// another body/trace and the caller receives the fixed safe response.
		if errors.Is(err, ErrCapacity) {
			return CaptureDecision{Active: true, Mode: ModeDry, Language: language}, nil
		}
		return CaptureDecision{}, err
	}
	handle := &TraceHandle{hub: hub, userID: input.UserID, sessionID: current.id, traceID: record.trace.TraceID}
	decision := CaptureDecision{Active: true, Mode: mode, Trace: handle, Language: language}
	if mode == ModeLive {
		if input.RouteKind == RouteCharityChat {
			decision.ClaimPurpose = claim.PurposeCharity
		} else {
			decision.ClaimPurpose = claim.PurposeDebugLive
		}
	}
	if mode == ModeDry {
		caller := fixedCallerResult(httperr.CodeDebugDryRunIntercepted, http.StatusUnprocessableEntity, language, now.Unix())
		if err := hub.completeTraceLocked(current, record, caller); err != nil {
			return CaptureDecision{}, err
		}
	}
	return decision, nil
}

func (hub *Hub) beginTraceLocked(ctx context.Context, current *session, input CaptureInput) (*traceRecord, error) {
	if current.ended || current.inflight >= MaxSessionTraces {
		return nil, ErrCapacity
	}
	for len(current.traces) >= MaxSessionTraces {
		if !hub.evictOldestTerminalTraceLocked(current, "") {
			return nil, ErrCapacity
		}
	}
	id, err := hub.config.newOpaqueID("dbt_")
	if err != nil {
		return nil, fmt.Errorf("generate debug trace id: %w", err)
	}
	if !validOID(id, "dbt_") {
		return nil, errors.New("debug trace id generator returned a non-canonical id")
	}
	now := hub.config.now()
	body := captureOwnerBody(input.MediaType, input.Body)
	trace := DebugTrace{
		TraceID: id, Revision: "1", State: TraceCapturing,
		Request:   DebugRequest{RouteKind: input.RouteKind, Model: input.Model, Stream: input.Stream, Body: body},
		CreatedAt: now.Unix(), UpdatedAt: now.Unix(), Truncated: body.Truncated,
	}
	traceCtx, cancel := context.WithCancelCause(ctx)
	record := &traceRecord{trace: trace, ctx: traceCtx, cancel: cancel}
	if err := hub.fitTraceLocked(current, record, "", 0); err != nil {
		cancel(err)
		return nil, err
	}
	current.traces[id] = record
	current.traceOrder = append(current.traceOrder, id)
	current.traceBytes += record.wireBytes
	current.inflight++
	hub.touchLocked(current, now)
	if _, err := hub.publishLocked(current, EventTraceUpsert, cloneTrace(record.trace)); err != nil {
		delete(current.traces, id)
		current.traceOrder = current.traceOrder[:len(current.traceOrder)-1]
		current.traceBytes -= record.wireBytes
		current.inflight--
		cancel(err)
		return nil, err
	}
	return record, nil
}

func captureOwnerBody(mediaType string, body []byte) DebugBody {
	if mediaType == "" {
		mediaType = "application/octet-stream"
	}
	result := DebugBody{MediaType: mediaType, ByteCount: int64(len(body))}
	if utf8.Valid(body) {
		const initialTextLimit = 384 * 1024
		content := body
		if len(content) > initialTextLimit {
			content = content[:initialTextLimit]
			for len(content) > 0 && !utf8.Valid(content) {
				content = content[:len(content)-1]
			}
			result.Truncated = true
		}
		text := string(content)
		result.Text = &text
		return result
	}
	const initialBinaryLimit = 280 * 1024
	content := body
	if len(content) > initialBinaryLimit {
		content = content[:initialBinaryLimit]
		result.Truncated = true
	}
	encoded := base64.StdEncoding.EncodeToString(content)
	result.Base64 = &encoded
	return result
}

func (hub *Hub) fitTraceLocked(current *session, record *traceRecord, replacingID string, replacedBytes int) error {
	for attempts := 0; attempts < 64; attempts++ {
		encoded, err := json.Marshal(record.trace)
		if err != nil {
			return ErrInvalid
		}
		candidateBytes := len(encoded)
		projectedBytes := current.traceBytes + candidateBytes - replacedBytes
		projectedGlobal := hub.totalBytesLocked() + int64(candidateBytes-replacedBytes)
		if candidateBytes <= MaxTraceBytes && projectedBytes <= maxSnapshotTraceBytes &&
			projectedGlobal <= MaxGlobalBytes {
			record.wireBytes = candidateBytes
			return nil
		}
		if candidateBytes > MaxTraceBytes || projectedBytes > maxSnapshotTraceBytes {
			if hub.evictOldestTerminalTraceLocked(current, replacingID) {
				continue
			}
		} else if projectedGlobal > MaxGlobalBytes && hub.evictOldestGlobalTerminalTraceLocked(current, replacingID) {
			continue
		}
		if !truncateTraceBody(&record.trace) {
			return ErrCapacity
		}
	}
	return ErrCapacity
}

func truncateTraceBody(trace *DebugTrace) bool {
	if trace == nil {
		return false
	}
	body := &trace.Request.Body
	switch {
	case body.Text != nil && len(*body.Text) > 0:
		next := len(*body.Text) * 3 / 4
		for next > 0 && !utf8.ValidString((*body.Text)[:next]) {
			next--
		}
		text := (*body.Text)[:next]
		body.Text = &text
	case body.Base64 != nil && len(*body.Base64) > 0:
		next := (len(*body.Base64) * 3 / 4) / 4 * 4
		encoded := (*body.Base64)[:next]
		body.Base64 = &encoded
	default:
		return false
	}
	body.Truncated = true
	trace.Truncated = true
	return true
}

func (hub *Hub) evictOldestTerminalTraceLocked(current *session, exclude string) bool {
	for index, traceID := range current.traceOrder {
		record := current.traces[traceID]
		if traceID == exclude || record == nil || record.trace.State != TraceTerminal {
			continue
		}
		delete(current.traces, traceID)
		current.traceBytes -= record.wireBytes
		copy(current.traceOrder[index:], current.traceOrder[index+1:])
		current.traceOrder[len(current.traceOrder)-1] = ""
		current.traceOrder = current.traceOrder[:len(current.traceOrder)-1]
		return true
	}
	return false
}

func (hub *Hub) evictOldestGlobalTerminalTraceLocked(excludeSession *session, excludeTrace string) bool {
	var selectedSession *session
	var selectedTraceID string
	var selected *traceRecord
	for _, current := range hub.activeByUser {
		for _, traceID := range current.traceOrder {
			record := current.traces[traceID]
			if record == nil || record.trace.State != TraceTerminal ||
				(current == excludeSession && traceID == excludeTrace) {
				continue
			}
			if selected == nil || record.trace.CreatedAt < selected.trace.CreatedAt ||
				(record.trace.CreatedAt == selected.trace.CreatedAt &&
					(current.id < selectedSession.id || (current.id == selectedSession.id && traceID < selectedTraceID))) {
				selectedSession, selectedTraceID, selected = current, traceID, record
			}
		}
	}
	if selected == nil {
		return false
	}
	delete(selectedSession.traces, selectedTraceID)
	selectedSession.traceBytes -= selected.wireBytes
	for index, traceID := range selectedSession.traceOrder {
		if traceID != selectedTraceID {
			continue
		}
		copy(selectedSession.traceOrder[index:], selectedSession.traceOrder[index+1:])
		selectedSession.traceOrder[len(selectedSession.traceOrder)-1] = ""
		selectedSession.traceOrder = selectedSession.traceOrder[:len(selectedSession.traceOrder)-1]
		break
	}
	return true
}

func (handle *TraceHandle) MarkDispatched() error {
	return handle.withTrace(func(hub *Hub, current *session, record *traceRecord) error {
		if record.trace.State == TraceTerminal {
			return ErrTraceTerminal
		}
		record.dispatched = true
		hub.touchLocked(current, hub.config.now())
		return nil
	})
}

// RecordUpstream accepts only the structured safe projection. The type has no
// fields through which raw upstream headers/body/error text can be passed.
func (handle *TraceHandle) RecordUpstream(result DebugUpstreamResult) error {
	if !result.valid() {
		return ErrInvalid
	}
	return handle.withTrace(func(hub *Hub, current *session, record *traceRecord) error {
		if record.trace.State == TraceTerminal {
			return ErrTraceTerminal
		}
		if !record.dispatched {
			return ErrConflict
		}
		if result.CompletedAt < record.trace.CreatedAt {
			result.CompletedAt = record.trace.CreatedAt
		}
		result.UpstreamCode = cloneString(result.UpstreamCode)
		if result.Diag != nil {
			bounded := diagnostic.Bound(*result.Diag)
			if !safeDiagnostic(bounded) {
				return ErrInvalid
			}
			result.Diag = stringPointer(bounded)
		}
		previous := cloneTrace(record.trace)
		oldBytes := record.wireBytes
		record.trace.UpstreamResult = &result
		return hub.updateTraceLocked(current, record, previous, oldBytes)
	})
}

// CompleteCaller terminates a trace with a caller-safe result. Callers must
// never build this value from raw upstream text; upstream-origin fields are
// already absent from the type.
func (handle *TraceHandle) CompleteCaller(result DebugCallerResult) error {
	if !result.valid() || result.Source != SourcePlatform {
		return ErrInvalid
	}
	return handle.withTrace(func(hub *Hub, current *session, record *traceRecord) error {
		if record.dispatched {
			return ErrConflict
		}
		return hub.completeTraceLocked(current, record, result)
	})
}

func (handle *TraceHandle) CompleteLiveCaptured(language string) error {
	now := time.Now().Unix()
	if handle != nil && handle.hub != nil {
		now = handle.hub.config.now().Unix()
	}
	result := fixedCallerResult(
		httperr.CodeDebugLiveResultCaptured, http.StatusUnprocessableEntity, normalizeLanguage(language), now,
	)
	return handle.withTrace(func(hub *Hub, current *session, record *traceRecord) error {
		if !record.dispatched {
			return ErrConflict
		}
		return hub.completeTraceLocked(current, record, result)
	})
}

func (handle *TraceHandle) CompleteCancelled(language string) error {
	now := time.Now().Unix()
	if handle != nil && handle.hub != nil {
		now = handle.hub.config.now().Unix()
	}
	result := DebugCallerResult{
		HTTPStatus: 499, Source: SourcePlatform,
		Message: wirePlatformMessage(cancelledMessage(normalizeLanguage(language))), CompletedAt: now,
	}
	return handle.withTrace(func(hub *Hub, current *session, record *traceRecord) error {
		return hub.completeTraceLocked(current, record, result)
	})
}

func (handle *TraceHandle) withTrace(operation func(*Hub, *session, *traceRecord) error) error {
	if handle == nil || handle.hub == nil || operation == nil {
		return ErrInvalid
	}
	hub := handle.hub
	hub.mu.Lock()
	defer hub.mu.Unlock()
	if hub.closed {
		return ErrClosed
	}
	current := hub.activeByUser[handle.userID]
	if current == nil || current.ended || current.id != handle.sessionID {
		return ErrSessionEnded
	}
	record := current.traces[handle.traceID]
	if record == nil {
		return ErrSessionEnded
	}
	return operation(hub, current, record)
}

func (hub *Hub) updateTraceLocked(current *session, record *traceRecord, previous DebugTrace, oldBytes int) error {
	revision, ok := parseRevision(record.trace.Revision)
	if !ok || revision == ^uint64(0) {
		return ErrCapacity
	}
	record.trace.Revision = fmt.Sprintf("%d", revision+1)
	now := hub.config.now().Unix()
	if now < record.trace.CreatedAt {
		now = record.trace.CreatedAt
	}
	record.trace.UpdatedAt = now
	if err := hub.fitTraceLocked(current, record, record.trace.TraceID, oldBytes); err != nil {
		record.trace = previous
		record.wireBytes = oldBytes
		return err
	}
	current.traceBytes += record.wireBytes - oldBytes
	hub.touchLocked(current, hub.config.now())
	if _, err := hub.publishLocked(current, EventTraceUpsert, cloneTrace(record.trace)); err != nil {
		current.traceBytes -= record.wireBytes - oldBytes
		record.trace = previous
		record.wireBytes = oldBytes
		return err
	}
	return nil
}

func (hub *Hub) completeTraceLocked(current *session, record *traceRecord, result DebugCallerResult) error {
	if record.trace.State == TraceTerminal {
		return ErrTraceTerminal
	}
	if current.inflight <= 0 {
		return ErrConflict
	}
	if result.CompletedAt < record.trace.CreatedAt {
		result.CompletedAt = record.trace.CreatedAt
	}
	previous := cloneTrace(record.trace)
	oldBytes := record.wireBytes
	record.trace.CallerResult = &result
	record.trace.State = TraceTerminal
	// The terminal trace and the session-level inflight count are one
	// observable state transition. In particular, slow-consumer recovery can
	// publish a snapshot while updateTraceLocked is notifying subscribers, so
	// decrement before that publish and restore it if the update cannot commit.
	current.inflight--
	if err := hub.updateTraceLocked(current, record, previous, oldBytes); err != nil {
		current.inflight++
		return err
	}
	if record.cancel != nil {
		record.cancel(ErrTraceTerminal)
	}
	return nil
}

func fixedCallerResult(code string, status int, language string, completedAt int64) DebugCallerResult {
	message := dryMessage(language)
	if code == httperr.CodeDebugLiveResultCaptured {
		message = liveCapturedMessage(language)
	} else if code == httperr.CodeDebugLiveCancelled {
		message = liveCancelledMessage(language)
	}
	return DebugCallerResult{
		HTTPStatus: status, ErrorCode: stringPointer(code), Source: SourcePlatform,
		Message: wirePlatformMessage(message), CompletedAt: completedAt,
	}
}

func normalizeLanguage(language string) string {
	if strings.EqualFold(language, "zh") || strings.HasPrefix(strings.ToLower(language), "zh-") {
		return "zh"
	}
	return "en"
}

func dryMessage(language string) string {
	if normalizeLanguage(language) == "zh" {
		return "调试 Dry run 已拦截本次请求；请求已在调试页面捕获，未发送到上游。"
	}
	return "Debug dry run intercepted this request. It was captured on the Debug page and was not sent upstream."
}

func liveCapturedMessage(language string) string {
	if normalizeLanguage(language) == "zh" {
		return "上游响应已被调试页捕获。"
	}
	return "The upstream response was captured by the Debug page."
}

func liveCancelledMessage(language string) string {
	if normalizeLanguage(language) == "zh" {
		return "调试 Live 请求已取消。"
	}
	return "The Debug live request was cancelled."
}

func cancelledMessage(language string) string {
	if normalizeLanguage(language) == "zh" {
		return "调用方已取消请求。"
	}
	return "The caller cancelled the request."
}

func wirePlatformMessage(message string) string {
	return "[NonbiriAPI] " + strings.ReplaceAll(message, "[NonbiriAPI] ", "")
}

func WriteDryIntercepted(writer http.ResponseWriter, language string) {
	writer.Header().Set("X-Nonbiri-Debug-Mode", "dry-run")
	httperr.WriteError(writer, httperr.New(httperr.CodeDebugDryRunIntercepted, dryMessage(language)))
}

func WriteLiveCaptured(writer http.ResponseWriter, language string) {
	writer.Header().Set("X-Nonbiri-Debug-Mode", "live-captured")
	httperr.WriteError(writer, httperr.New(httperr.CodeDebugLiveResultCaptured, liveCapturedMessage(language)))
}

// WriteLiveCancelled writes the frozen 409 while HTTP is writable, or the
// bounded safe terminal frame after a stream commit. It never writes upstream
// status, headers, body, or diagnostic material.
func WriteLiveCancelled(writer http.ResponseWriter, language string, streamCommitted bool) {
	errorValue := httperr.New(httperr.CodeDebugLiveCancelled, liveCancelledMessage(language))
	if streamCommitted {
		_, _ = writer.Write(httperr.SSEErrorFrame(errorValue))
		return
	}
	writer.Header().Set("X-Nonbiri-Debug-Mode", "live-cancelled")
	httperr.WriteError(writer, errorValue)
}
