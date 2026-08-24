package debug

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/auth"
	"github.com/waiting-here/NonbiriAPI/internal/connector/openai"
	"github.com/waiting-here/NonbiriAPI/internal/forward"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
)

// WrapCaller attaches the debug data plane to a CallerKey-authenticated route
// tree.  Mount it inside the existing flow-control middleware and after
// CallerKeyMiddleware so dry requests retain the normal authentication,
// concurrency, and RPM admission order.  With no active session it returns the
// underlying handler unchanged.
func (h *Hub) WrapCaller(next http.Handler) http.Handler {
	return h.WrapCallerWithIdentity(next, nil)
}

// WrapCallerWithIdentity is the test/alternate-middleware form of
// WrapCaller.  A nil resolver retains the production CallerKey principal
// check; a custom resolver must return only a server-established user id.
func (h *Hub) WrapCallerWithIdentity(next http.Handler, resolver func(*http.Request) (int64, error)) http.Handler {
	if next == nil {
		next = http.NotFoundHandler()
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if h == nil || r == nil || r.URL == nil || r.Method != http.MethodPost || r.URL.Path != "/v1/chat/completions" {
			next.ServeHTTP(w, r)
			return
		}
		userID := int64(0)
		if resolver != nil {
			resolved, resolveErr := resolver(r)
			if resolveErr == nil {
				userID = resolved
			}
		} else {
			user, ok := auth.CallerUserFromContext(r.Context())
			if ok && user != nil && user.ID > 0 && !user.IsAdmin && !user.IsBanned && user.IsActive() {
				userID = user.ID
			}
		}
		if userID <= 0 {
			next.ServeHTTP(w, r)
			return
		}
		mode, sessionID, generation, active := h.ModeForCallerContext(r.Context(), userID)
		if !active {
			next.ServeHTTP(w, r)
			return
		}
		// The ordinary ingress gate remains authoritative, but an authenticated
		// active debug session must still observe its deterministic rejection.
		// Run the exact next handler behind a buffered writer so status, headers,
		// and body remain byte-for-byte identical; this gate occurs before body
		// decoding, routing, candidate selection, and all egress/accounting work.
		if !forward.ValidateChatIngress(r) {
			h.serveIngressFailure(w, r, next, userID, sessionID, generation, mode)
			return
		}
		// Body capture is an extra bounded copy outside Hub retained bytes. A
		// lease is acquired before touching r.Body so concurrent callers cannot
		// create an unaccounted 1 MiB-per-request amplification. If an active
		// session cannot obtain the lease, both dry and live fail closed with a
		// bounded safe-capacity response; neither path delegates to a real call.
		lease, leased := h.acquireRequestCopyLease(userID, sessionID, generation)
		if !leased {
			// An identifiable active session must never turn Hub pressure into a
			// real upstream call. The safe result is a bounded dry-capacity
			// response; only the inactive-session branch above delegates to next.
			h.serveDryCapacityFailure(w, userID, sessionID, generation)
			return
		}
		defer lease.Release()
		body, tooLarge, readErr := captureBody(r)
		if readErr != nil {
			// A dry request must not fall through to a real upstream call when
			// the observer cannot read its bounded copy. Live preserves the
			// ordinary handler's behavior with the bytes that were read.
			r.Body = newReplayBody(body, readErr)
			if mode == ModeDry {
				h.serveDryReadFailure(w, r, userID, sessionID, generation, body)
				clear(body)
				return
			}
			h.serveLiveReadFailure(w, r, next, userID, sessionID, generation, body)
			clear(body)
			return
		}
		r.Body = newReplayBody(body, nil)
		if mode == ModeDry {
			h.serveDry(w, r, userID, sessionID, generation, body, tooLarge)
			clear(body)
			return
		}
		h.serveLive(w, r, next, userID, sessionID, generation, body, tooLarge)
		clear(body)
	})
}

func (h *Hub) serveIngressFailure(w http.ResponseWriter, r *http.Request, next http.Handler, userID int64, sessionID string, generation uint64, mode Mode) {
	traceID, err := opaqueID("trace_")
	if err != nil {
		setNoStore(w)
		httpErrorWriter(w, httperr.CodeServiceUnavailable, "debug capacity unavailable")
		return
	}
	receivedAt := h.now().Unix()
	capture := &responseCapture{orig: w, maxBytes: MaxResponseBytes}
	var capturedWriter http.ResponseWriter = capture
	if _, ok := w.(http.Flusher); ok {
		capturedWriter = &responseCaptureFlusher{responseCapture: capture}
	}
	next.ServeHTTP(capturedWriter, r)
	status := capture.status
	if status == 0 {
		status = http.StatusOK
	}
	input := TraceInput{
		ID: traceID, RequestID: traceID, ReceivedAt: receivedAt, Mode: mode,
		Status: status, ContentType: capture.ContentType(),
		Terminal: "failed", ErrorCategory: "invalid_request",
	}
	setCapturedResponse(&input, capture)
	h.publishCaller(userID, sessionID, generation, Trace{ID: traceID, Revision: 1, Payload: buildTracePayload(input), Terminal: true})
	clear(capture.body.Bytes())
}

// NewDataHandler is an alias that reads naturally at a routing mount.
func NewDataHandler(h *Hub, next http.Handler) http.Handler {
	if h == nil {
		return next
	}
	return h.WrapCaller(next)
}

func captureBody(r *http.Request) ([]byte, bool, error) {
	if r == nil || r.Body == nil {
		return nil, false, nil
	}
	original := r.Body
	data, err := io.ReadAll(io.LimitReader(original, openai.MaxRequestBodyBytes+1))
	_ = original.Close()
	if err != nil {
		return data, false, err
	}
	return data, int64(len(data)) > openai.MaxRequestBodyBytes, nil
}

// replayBody preserves the ordinary handler's read-visible bytes and terminal
// read error after the debug tee has consumed and closed the original body.
// A live read failure therefore cannot be silently converted into a clean EOF,
// while dry mode can reject it without ever reaching the real handler.
type replayBody struct {
	data         []byte
	offset       int
	terminal     error
	terminalSeen bool
	closed       bool
}

func newReplayBody(data []byte, terminal error) io.ReadCloser {
	return &replayBody{data: data, terminal: terminal}
}

func (b *replayBody) Read(p []byte) (int, error) {
	if b == nil || b.closed {
		return 0, io.ErrClosedPipe
	}
	if b.offset < len(b.data) {
		n := copy(p, b.data[b.offset:])
		b.offset += n
		return n, nil
	}
	if b.terminal != nil && !b.terminalSeen {
		b.terminalSeen = true
		return 0, b.terminal
	}
	return 0, io.EOF
}

func (b *replayBody) Close() error {
	if b != nil {
		b.closed = true
	}
	return nil
}

func httpErrorWriter(w http.ResponseWriter, code, message string) {
	httperr.WriteError(w, httperr.New(code, message))
}

func setCapturedResponse(input *TraceInput, capture *responseCapture) {
	if input == nil || capture == nil {
		return
	}
	input.Response = capture.body.Bytes()
	input.ResponseOriginalBytes = int(capture.writtenBytes)
	input.ResponseCapturedBytes = capture.body.Len()
	input.ResponseTruncated = capture.truncated
}

func dryCaptureWriter(original http.ResponseWriter, capture *responseCapture) http.ResponseWriter {
	if original != nil {
		if _, ok := original.(http.Flusher); ok {
			return &responseCaptureFlusher{responseCapture: capture}
		}
	}
	return capture
}

func (h *Hub) serveDry(w http.ResponseWriter, r *http.Request, userID int64, sessionID string, generation uint64, body []byte, tooLarge bool) {
	setNoStore(w)
	traceID, err := opaqueID("trace_")
	if err != nil {
		writeDebugError(w, ErrClosed)
		return
	}
	receivedAt := h.now().Unix()
	request, decodeErr := openai.DecodeChatRequest(bytes.NewReader(body), openai.MaxRequestBodyBytes)
	if tooLarge {
		decodeErr = openai.ErrPayloadTooLarge
	}
	if decodeErr != nil {
		capture := &responseCapture{orig: w, maxBytes: MaxResponseBytes}
		input := TraceInput{
			ID: traceID, RequestID: traceID, ReceivedAt: receivedAt, Mode: ModeDry, RawRequest: body,
			Terminal: "failed", ErrorCategory: dryErrorCategory(decodeErr), Truncated: tooLarge,
		}
		if errors.Is(decodeErr, openai.ErrPayloadTooLarge) {
			httpErrorWriter(capture, httperr.CodePayloadTooLarge, "request body too large")
		} else {
			httpErrorWriter(capture, httperr.CodeInvalidRequest, "invalid request")
		}
		input.Status, input.ContentType = capture.status, capture.ContentType()
		setCapturedResponse(&input, capture)
		h.publishCaller(userID, sessionID, generation, Trace{ID: traceID, Revision: 1, Payload: buildTracePayload(input), Terminal: true})
		clear(capture.body.Bytes())
		return
	}
	defer request.Clear()
	validator := h.validator
	if strings.HasPrefix(request.Model, "[公益]") {
		validator = h.charityValidator
	}
	if validator == nil {
		h.publishDryFailure(w, r, userID, sessionID, generation, traceID, request.Model, body, errors.New("dry validator unavailable"))
		return
	}
	validation, err := validator(r.Context(), userID, request)
	if err != nil {
		capture := &responseCapture{orig: w, maxBytes: MaxResponseBytes}
		code, message := h.MapDryRunError(err)
		httpErrorWriter(capture, code, message)
		input := TraceInput{
			ID: traceID, RequestID: traceID, ReceivedAt: receivedAt, Mode: ModeDry, Model: request.Model,
			Personal: validation.Personal, Charity: validation.Charity, RawRequest: body,
			Terminal: "failed", ErrorCategory: dryErrorCategory(err),
		}
		input.Status, input.ContentType = capture.status, capture.ContentType()
		setCapturedResponse(&input, capture)
		h.publishCaller(userID, sessionID, generation, Trace{ID: traceID, Revision: 1, Payload: buildTracePayload(input), Terminal: true})
		clear(capture.body.Bytes())
		return
	}
	if validation.Model == "" {
		validation.Model = request.Model
	}
	effective := map[string]any{
		"connector": map[string]any{"state": "not_selected_not_evaluated"},
		"key":       map[string]any{"state": "not_selected_not_evaluated"},
		"origin":    map[string]any{"state": "not_selected_not_evaluated"},
		"store":     map[string]any{"state": "not_selected_not_evaluated"},
	}
	for key, value := range validation.Effective {
		if forbiddenDryEffectiveKey(key) {
			continue
		}
		effective[key] = value
	}
	if validation.FlattenApplied {
		effective["flatten_tool_calls"] = map[string]any{"state": "applied"}
	}
	base := TraceInput{
		ID: traceID, RequestID: traceID, ReceivedAt: receivedAt, Mode: ModeDry, Model: validation.Model,
		Personal: validation.Personal, Charity: validation.Charity, RawRequest: body,
		Effective: marshalJSON(effective), Terminal: "validated",
		DebugModeHeader: "dry-run",
	}
	h.publishCaller(userID, sessionID, generation, Trace{ID: traceID, Revision: 1, Payload: buildTracePayload(base)})
	capture := &responseCapture{orig: w, maxBytes: MaxResponseBytes}
	dryWriter := dryCaptureWriter(w, capture)
	if request.Stream {
		if err := writeDryStream(r.Context(), dryWriter, request.Model); err != nil {
			base.Status, base.ContentType = capture.status, capture.ContentType()
			setCapturedResponse(&base, capture)
			base.Terminal = "incomplete"
			base.Incomplete = true
			base.ErrorCategory = "sink"
			if errors.Is(err, context.Canceled) {
				base.Terminal = "cancelled"
				base.ErrorCategory = "caller_cancelled"
			}
			h.publishCaller(userID, sessionID, generation, Trace{ID: traceID, Revision: 2, Payload: buildTracePayload(base), Terminal: true})
			clear(capture.body.Bytes())
			return
		}
	} else {
		if err := writeDryJSON(r.Context(), dryWriter, request.Model); err != nil {
			base.Status, base.ContentType = capture.status, capture.ContentType()
			setCapturedResponse(&base, capture)
			base.Terminal = "incomplete"
			base.Incomplete = true
			base.ErrorCategory = "sink"
			if errors.Is(err, context.Canceled) {
				base.Terminal = "cancelled"
				base.ErrorCategory = "caller_cancelled"
			}
			h.publishCaller(userID, sessionID, generation, Trace{ID: traceID, Revision: 2, Payload: buildTracePayload(base), Terminal: true})
			clear(capture.body.Bytes())
			return
		}
	}
	base.Status, base.ContentType = capture.status, capture.ContentType()
	setCapturedResponse(&base, capture)
	base.Terminal = "dry_completed"
	h.publishCaller(userID, sessionID, generation, Trace{ID: traceID, Revision: 2, Payload: buildTracePayload(base), Terminal: true})
	clear(capture.body.Bytes())
}

func forbiddenDryEffectiveKey(key string) bool {
	switch normalizeRedactionKey(key) {
	case "connector", "key", "origin", "base_url", "store", "endpoint", "endpoint_id", "endpoint_key",
		"endpoint_key_id", "binding", "binding_id", "candidate", "candidates", "safety_identifier",
		"upstream", "upstream_model_id", "physical_id", "donation_id", "donation_key_id":
		return true
	default:
		return false
	}
}

func (h *Hub) serveDryReadFailure(w http.ResponseWriter, r *http.Request, userID int64, sessionID string, generation uint64, body []byte) {
	setNoStore(w)
	if r != nil && r.Body != nil {
		defer r.Body.Close()
	}
	traceID, err := opaqueID("trace_")
	if err != nil {
		writeDebugError(w, ErrClosed)
		return
	}
	receivedAt := h.now().Unix()
	capture := &responseCapture{orig: w, maxBytes: MaxResponseBytes}
	httpErrorWriter(capture, httperr.CodeInvalidRequest, "invalid request")
	input := TraceInput{ID: traceID, RequestID: traceID, ReceivedAt: receivedAt, Mode: ModeDry, RawRequest: body, Terminal: "failed", ErrorCategory: "invalid_request", Incomplete: true,
		Status: capture.status, ContentType: capture.ContentType()}
	setCapturedResponse(&input, capture)
	h.publishCaller(userID, sessionID, generation, Trace{ID: traceID, Revision: 1, Payload: buildTracePayload(input), Terminal: true})
	clear(capture.body.Bytes())
}

func (h *Hub) serveDryCapacityFailure(w http.ResponseWriter, userID int64, sessionID string, generation uint64) {
	setNoStore(w)
	traceID, err := opaqueID("trace_")
	if err != nil {
		writeDebugError(w, ErrCapacity)
		return
	}
	receivedAt := h.now().Unix()
	capture := &responseCapture{orig: w, maxBytes: MaxResponseBytes}
	httpErrorWriter(capture, httperr.CodeServiceUnavailable, "debug capacity unavailable")
	input := TraceInput{ID: traceID, RequestID: traceID, ReceivedAt: receivedAt, Mode: ModeDry, Terminal: "failed", ErrorCategory: "capacity",
		Status: capture.status, ContentType: capture.ContentType(), Incomplete: true}
	setCapturedResponse(&input, capture)
	h.publishCaller(userID, sessionID, generation, Trace{ID: traceID, Revision: 1, Payload: buildTracePayload(input), Terminal: true})
	clear(capture.body.Bytes())
}

func (h *Hub) serveLiveReadFailure(w http.ResponseWriter, r *http.Request, next http.Handler, userID int64, sessionID string, generation uint64, body []byte) {
	traceID, err := opaqueID("trace_")
	if err != nil {
		setNoStore(w)
		httpErrorWriter(w, httperr.CodeServiceUnavailable, "debug capacity unavailable")
		return
	}
	receivedAt := h.now().Unix()
	// The body has already been bounded and replayed with its terminal read
	// error. Use the same direct tee as a normal live request: buffering here
	// would change first-byte/Flush timing and could turn a streaming response
	// into an unbounded post-handler copy.
	capture := &responseCapture{orig: w, maxBytes: MaxResponseBytes}
	if r != nil && r.Body != nil {
		defer r.Body.Close()
	}
	var capturedWriter http.ResponseWriter = capture
	if _, ok := w.(http.Flusher); ok {
		capturedWriter = &responseCaptureFlusher{responseCapture: capture}
	}
	next.ServeHTTP(capturedWriter, r)
	status := capture.status
	if status == 0 {
		status = http.StatusOK
	}
	input := TraceInput{
		ID: traceID, RequestID: traceID, ReceivedAt: receivedAt, Mode: ModeLive, RawRequest: body,
		Status: status, ContentType: capture.ContentType(),
		Terminal: "failed", ErrorCategory: "invalid_request", Incomplete: true,
	}
	setCapturedResponse(&input, capture)
	h.publishCaller(userID, sessionID, generation, Trace{ID: traceID, Revision: 1, Payload: buildTracePayload(input), Terminal: true})
	clear(capture.body.Bytes())
}

func (h *Hub) publishDryFailure(w http.ResponseWriter, r *http.Request, userID int64, sessionID string, generation uint64, traceID, model string, body []byte, err error) {
	receivedAt := h.now().Unix()
	capture := &responseCapture{orig: w, maxBytes: MaxResponseBytes}
	code, message := h.MapDryRunError(err)
	httpErrorWriter(capture, code, message)
	input := TraceInput{ID: traceID, RequestID: traceID, ReceivedAt: receivedAt, Mode: ModeDry, Model: model, RawRequest: body, Terminal: "failed", ErrorCategory: "internal",
		Status: capture.status, ContentType: capture.ContentType()}
	setCapturedResponse(&input, capture)
	h.publishCaller(userID, sessionID, generation, Trace{ID: traceID, Revision: 1, Payload: buildTracePayload(input), Terminal: true})
	clear(capture.body.Bytes())
}

func (h *Hub) serveLive(w http.ResponseWriter, r *http.Request, next http.Handler, userID int64, sessionID string, generation uint64, body []byte, tooLarge bool) {
	traceID, err := opaqueID("trace_")
	if err != nil {
		h.serveDryCapacityFailure(w, userID, sessionID, generation)
		return
	}
	receivedAt := h.now().Unix()
	// Forward preserves its independent accounting AttemptID while carrying
	// this logical id into the Connector SafeObserver path. All retries for
	// the request therefore upsert the same trace with Hub-assigned revisions.
	protocolState := &forward.ProtocolState{}
	r = r.WithContext(forward.WithProtocolState(forward.WithObserverTraceID(r.Context(), traceID), protocolState))
	request, decodeErr := openai.DecodeChatRequest(bytes.NewReader(body), openai.MaxRequestBodyBytes)
	if tooLarge {
		decodeErr = openai.ErrPayloadTooLarge
	}
	if request != nil {
		defer request.Clear()
	}
	model := ""
	if request != nil {
		model = request.Model
	}
	personalRoute := model != "" && !strings.HasPrefix(model, "[公益]")
	charityRoute := strings.HasPrefix(model, "[公益]")
	initial := TraceInput{ID: traceID, RequestID: traceID, ReceivedAt: receivedAt, Mode: ModeLive, Model: model, Personal: personalRoute, Charity: charityRoute, RawRequest: body, Terminal: "received", Truncated: tooLarge}
	if decodeErr != nil {
		initial.Terminal = "failed"
		initial.ErrorCategory = dryErrorCategory(decodeErr)
	}
	// Admission is the boundary for all debug-copy allocation. If the initial
	// projection cannot be retained (for example, all 32 traces are active or
	// the session budget is exhausted), an identifiable active session fails
	// closed with a bounded dry-capacity response; it is never forwarded to a
	// real upstream without its retained debug copy.
	if !h.publishCaller(userID, sessionID, generation, Trace{ID: traceID, Revision: 1, Payload: buildTracePayload(initial)}) {
		// Initial projection admission is the boundary for real execution. If
		// the active session cannot retain it, fail closed with no connector,
		// Vault, DNS, egress, reservation, or usage path.
		h.serveDryCapacityFailure(w, userID, sessionID, generation)
		return
	}
	if request != nil {
		observer, observerErr := h.NewRequestObserverForTrace(userID, sessionID, generation, traceID)
		if observerErr != nil {
			// A live sidecar is part of the bounded active-session admission.
			// Do not silently execute a real request after its observer lease is
			// unavailable; return the safe dry-capacity result instead.
			h.serveLiveCapacityFailure(w, userID, sessionID, generation, traceID, model, body)
			return
		}
		if observer != nil {
			r = r.WithContext(forward.WithObserver(r.Context(), observer))
			// Observer shutdown must never delay caller completion or accounting
			// release. Accepted bounded events drain on a sidecar goroutine.
			defer func() {
				go func() {
					_ = observer.Close()
					if dropped := observer.Dropped(); dropped != 0 {
						h.publishObserverDrop(userID, sessionID, generation, dropped)
					}
				}()
			}()
		}
	}
	capture := &responseCapture{orig: w, maxBytes: MaxResponseBytes}
	defer func() {
		status := capture.status
		if status == 0 {
			status = http.StatusOK
		}
		terminal := "incomplete"
		if r.Context().Err() != nil {
			terminal = "cancelled"
		} else if status >= http.StatusBadRequest {
			terminal = "failed"
		} else if capture.writeErr != nil {
			terminal = "incomplete"
		} else if protocolState.Completed() {
			terminal = "completed"
		} else if capture.truncated {
			terminal = "incomplete"
		}
		final := TraceInput{
			ID: traceID, RequestID: traceID, ReceivedAt: receivedAt, Mode: ModeLive, Model: model, Personal: personalRoute, Charity: charityRoute, RawRequest: body,
			Status: status, ContentType: capture.ContentType(),
			Terminal: terminal, Truncated: tooLarge || capture.truncated, Incomplete: terminal == "incomplete",
			ErrorCategory: func() string {
				if capture.writeErr != nil {
					return "sink"
				}
				if decodeErr != nil {
					return dryErrorCategory(decodeErr)
				}
				return ""
			}(),
		}
		setCapturedResponse(&final, capture)
		final.Truncated = final.Truncated || final.ResponseTruncated
		h.publishCaller(userID, sessionID, generation, Trace{ID: traceID, Payload: buildTracePayload(final), Merge: true, Terminal: true})
		clear(capture.body.Bytes())
		clear(body)
	}()
	// The original request is restored before entering the real path.  The
	// capture wrapper only tees caller-visible bytes and never touches context,
	// headers, status, flush, termination, or the accounting result.
	r.Body = io.NopCloser(bytes.NewReader(body))
	// Preserve the optional Flusher capability exactly.  A wrapper that always
	// implements Flush would make a non-streaming underlying writer appear
	// stream-capable and change the real handler's branch/flush behavior.
	var capturedWriter http.ResponseWriter = capture
	if _, ok := w.(http.Flusher); ok {
		capturedWriter = &responseCaptureFlusher{responseCapture: capture}
	}
	next.ServeHTTP(capturedWriter, r)
}

func (h *Hub) serveLiveCapacityFailure(w http.ResponseWriter, userID int64, sessionID string, generation uint64, traceID, model string, body []byte) {
	setNoStore(w)
	capture := &responseCapture{orig: w, maxBytes: MaxResponseBytes}
	httpErrorWriter(capture, httperr.CodeServiceUnavailable, "debug capacity unavailable")
	input := TraceInput{
		ID: traceID, RequestID: traceID, ReceivedAt: h.now().Unix(), Mode: ModeDry,
		Model: model, RawRequest: body, Terminal: "failed", ErrorCategory: "capacity", Incomplete: true,
		Status: capture.status, ContentType: capture.ContentType(),
	}
	setCapturedResponse(&input, capture)
	// The initial live projection is already authoritative. Merge the bounded
	// local capacity failure into that same logical trace so no nonterminal
	// received record remains to consume the 32-trace active cap.
	h.publishCaller(userID, sessionID, generation, Trace{ID: traceID, Payload: buildTracePayload(input), Merge: true, Terminal: true})
	clear(capture.body.Bytes())
}

func (h *Hub) publishCaller(userID int64, sessionID string, generation uint64, trace Trace) bool {
	if h == nil || userID <= 0 || sessionID == "" {
		return false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	session := h.sessions[userID]
	if h.closed || session == nil || session.closed || session.id != sessionID || session.generation != generation || !h.validDeadlineLocked(session) {
		return false
	}
	h.touchLocked(session)
	return h.publishTraceLocked(session, trace)
}

func writeDryJSON(ctx context.Context, w http.ResponseWriter, model string) error {
	if err := contextDone(ctx); err != nil {
		return err
	}
	id, err := opaqueID("debug-dry-")
	if err != nil {
		return err
	}
	response := map[string]any{
		"id": id, "object": "chat.completion", "created": time.Now().Unix(), "model": model,
		"choices": []any{map[string]any{
			"index":         0,
			"message":       map[string]any{"role": "assistant", "content": "NonbiriAPI debug dry run: no request was sent upstream."},
			"finish_reason": "stop",
		}},
	}
	data, _ := json.Marshal(response)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Nonbiri-Debug-Mode", "dry-run")
	w.WriteHeader(http.StatusOK)
	if err := contextDone(ctx); err != nil {
		return err
	}
	return writeDryBytes(w, data)
}

func writeDryStream(ctx context.Context, w http.ResponseWriter, model string) error {
	if err := contextDone(ctx); err != nil {
		return err
	}
	id, err := opaqueID("debug-dry-")
	if err != nil {
		return err
	}
	created := time.Now().Unix()
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("X-Nonbiri-Debug-Mode", "dry-run")
	w.WriteHeader(http.StatusOK)
	flusher, _ := w.(http.Flusher)
	write := func(chunk map[string]any) error {
		if err := contextDone(ctx); err != nil {
			return err
		}
		data, _ := json.Marshal(chunk)
		if err := writeDryString(w, "data: "+string(data)+"\n\n"); err != nil {
			return err
		}
		if flusher != nil {
			flusher.Flush()
		}
		return nil
	}
	if err := write(map[string]any{
		"id": id, "object": "chat.completion.chunk", "created": created, "model": model,
		"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"role": "assistant"}, "finish_reason": nil}},
	}); err != nil {
		return err
	}
	if err := write(map[string]any{
		"id": id, "object": "chat.completion.chunk", "created": created, "model": model,
		"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"content": "NonbiriAPI debug dry run: no request was sent upstream."}, "finish_reason": nil}},
	}); err != nil {
		return err
	}
	if err := write(map[string]any{
		"id": id, "object": "chat.completion.chunk", "created": created, "model": model,
		"choices": []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": "stop"}},
	}); err != nil {
		return err
	}
	if err := contextDone(ctx); err != nil {
		return err
	}
	err = writeDryString(w, "data: [DONE]\n\n")
	if err == nil && flusher != nil {
		flusher.Flush()
	}
	return err
}

func writeDryBytes(w io.Writer, data []byte) error {
	if w == nil {
		return io.ErrClosedPipe
	}
	n, err := w.Write(data)
	if err != nil {
		return err
	}
	if n != len(data) {
		return io.ErrShortWrite
	}
	return nil
}

func writeDryString(w io.Writer, data string) error {
	return writeDryBytes(w, []byte(data))
}

func contextDone(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}

func dryErrorCategory(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, openai.ErrPayloadTooLarge) {
		return "payload_too_large"
	}
	if errors.Is(err, context.Canceled) {
		return "caller_cancelled"
	}
	return "invalid_request"
}

type responseCapture struct {
	orig         http.ResponseWriter
	status       int
	wroteHead    bool
	body         bytes.Buffer
	maxBytes     int
	truncated    bool
	writtenBytes int64
	writeErr     error
	contentType  string
}

func (c *responseCapture) Header() http.Header { return c.orig.Header() }

func (c *responseCapture) WriteHeader(status int) {
	if c.wroteHead {
		return
	}
	c.status = status
	c.wroteHead = true
	c.orig.WriteHeader(status)
	c.contentType = strings.TrimSpace(c.orig.Header().Get("Content-Type"))
}

func (c *responseCapture) Write(p []byte) (int, error) {
	if !c.wroteHead {
		// Let the underlying writer perform its own implicit 200 commit and
		// content sniffing. Calling WriteHeader(200) first changes the
		// caller-visible Content-Type for writers such as ResponseRecorder.
		written, err := c.orig.Write(p)
		c.status = http.StatusOK
		c.wroteHead = true
		c.contentType = strings.TrimSpace(c.orig.Header().Get("Content-Type"))
		c.captureWritten(p, written)
		if err != nil && c.writeErr == nil {
			c.writeErr = err
		}
		return written, err
	}
	written, err := c.orig.Write(p)
	c.captureWritten(p, written)
	if err != nil && c.writeErr == nil {
		c.writeErr = err
	}
	return written, err
}

func (c *responseCapture) captureWritten(p []byte, written int) {
	if written > 0 {
		if written > len(p) {
			written = len(p)
		}
		c.writtenBytes += int64(written)
		remaining := c.maxBytes - c.body.Len()
		if remaining <= 0 {
			c.truncated = true
		} else if written > remaining {
			_, _ = c.body.Write(p[:remaining])
			c.truncated = true
		} else {
			_, _ = c.body.Write(p[:written])
		}
	}
}

func (c *responseCapture) flush() {
	_ = c.flushError()
}

// FlushError lets http.ResponseController stop at the capture boundary before
// it follows Unwrap to the real writer. Unwrap remains available for the
// handler's normal capability discovery, but flushing cannot bypass commit,
// content-type snapshot, or capture bookkeeping.
func (c *responseCapture) FlushError() error {
	return c.flushError()
}

func (c *responseCapture) flushError() error {
	if !c.wroteHead {
		c.WriteHeader(http.StatusOK)
	}
	if flusher, ok := c.orig.(interface{ FlushError() error }); ok {
		return flusher.FlushError()
	}
	if flusher, ok := c.orig.(http.Flusher); ok {
		flusher.Flush()
		return nil
	}
	return http.NewResponseController(c.orig).Flush()
}

// responseCaptureFlusher is selected only when the wrapped writer already
// exposed http.Flusher, preserving the dynamic interface set seen by the
// actual forwarding handler.
type responseCaptureFlusher struct{ *responseCapture }

func (c *responseCaptureFlusher) Flush() { c.responseCapture.flush() }

func (c *responseCapture) Unwrap() http.ResponseWriter { return c.orig }

func (c *responseCapture) ContentType() string {
	if c == nil || c.orig == nil {
		return ""
	}
	if c.wroteHead {
		return c.contentType
	}
	return strings.TrimSpace(c.orig.Header().Get("Content-Type"))
}
