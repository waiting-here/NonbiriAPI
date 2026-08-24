package debug

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/auth"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
	"github.com/waiting-here/NonbiriAPI/internal/strictjson"
)

var errMethodNotAllowed = errors.New("debug: method not allowed")

type handler struct {
	hub      *Hub
	identity func(*http.Request) (int64, string, error)
}

// NewHandler returns the fixed control-plane handler.  Mount it beneath the
// existing user-session middleware; the handler repeats the principal check so
// a caller/admin context can never become a Debug control identity.
func NewHandler(hub *Hub, configs ...HandlerConfig) http.Handler {
	var config HandlerConfig
	if len(configs) != 0 {
		config = configs[0]
	}
	identity := config.Identity
	if identity == nil {
		identity = defaultIdentity
	}
	return &handler{hub: hub, identity: identity}
}

// NewControlHandler is an explicit alias for callers that want to make the
// control-plane-only role obvious at a mount site.
func NewControlHandler(hub *Hub, config HandlerConfig) http.Handler {
	return NewHandler(hub, config)
}

func defaultIdentity(r *http.Request) (int64, string, error) {
	if r == nil {
		return 0, "", ErrUnauthorized
	}
	user, ok := auth.UserFromContext(r.Context())
	if !ok || user == nil || user.ID <= 0 || user.IsAdmin || user.IsBanned || !user.IsActive() {
		return 0, "", ErrUnauthorized
	}
	token := auth.UserSessionToken(r)
	if token == "" {
		return 0, "", ErrUnauthorized
	}
	return user.ID, db.SessionHash(token), nil
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	setNoStore(w)
	if h == nil || h.hub == nil || r == nil {
		writeDebugError(w, ErrClosed)
		return
	}
	if r.URL != nil && r.URL.RawQuery != "" {
		// The control surface is intentionally path-bound; in particular, an
		// SSE session id may never be supplied as a query parameter.
		writeDebugError(w, ErrInvalid)
		return
	}
	switch r.URL.Path {
	case "/api/debug/session":
		h.session(w, r)
	case "/api/debug/session/live-challenge":
		h.liveChallenge(w, r)
	case "/api/debug/session/mode":
		h.mode(w, r)
	case "/api/debug/events":
		h.events(w, r)
	default:
		writeDebugError(w, ErrNotFound)
	}
}

func (h *handler) identityFor(w http.ResponseWriter, r *http.Request) (int64, string, bool) {
	if h.identity == nil {
		writeDebugError(w, ErrUnauthorized)
		return 0, "", false
	}
	userID, binding, err := h.identity(r)
	if err != nil || userID <= 0 || binding == "" {
		writeDebugError(w, ErrUnauthorized)
		return 0, "", false
	}
	return userID, binding, true
}

func (h *handler) session(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		if !requireEmptyBody(w, r) {
			return
		}
		userID, binding, ok := h.identityFor(w, r)
		if !ok {
			return
		}
		metadata, found := h.hub.Metadata(userID, binding)
		if !found {
			writeJSON(w, http.StatusOK, map[string]any{"active": false})
			return
		}
		writeJSON(w, http.StatusOK, metadata)
		return
	}
	if r.Method == http.MethodPost {
		if !requireEmptyBody(w, r) {
			return
		}
		userID, binding, ok := h.identityFor(w, r)
		if !ok {
			return
		}
		metadata, err := h.hub.Start(userID, binding)
		if err != nil {
			writeDebugError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, metadata)
		return
	}
	if r.Method == http.MethodDelete {
		if !requireEmptyBody(w, r) {
			return
		}
		userID, binding, ok := h.identityFor(w, r)
		if !ok {
			return
		}
		// Binding comparison and close are one Hub operation.  A stale browser
		// request must not close a newer session that replaced its own session
		// between separate Metadata and Forget calls.
		h.hub.Stop(userID, binding)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	writeDebugError(w, errMethodNotAllowed)
}

func (h *handler) liveChallenge(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w)
		return
	}
	if !requireEmptyBody(w, r) {
		return
	}
	userID, binding, ok := h.identityFor(w, r)
	if !ok {
		return
	}
	confirmationID, err := h.hub.IssueChallenge(userID, binding)
	if err != nil {
		writeDebugError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"confirmation_id": confirmationID})
}

func (h *handler) mode(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		writeMethodNotAllowed(w)
		return
	}
	body, ok := readControlBody(w, r, false)
	if !ok {
		return
	}
	if err := strictjson.ValidateObject(body); err != nil {
		writeDebugError(w, ErrInvalid)
		return
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		writeDebugError(w, ErrInvalid)
		return
	}
	for key := range fields {
		if key != "mode" && key != "confirmation_id" {
			writeDebugError(w, ErrInvalid)
			return
		}
	}
	modeRaw, found := fields["mode"]
	if !found {
		writeDebugError(w, ErrInvalid)
		return
	}
	var mode Mode
	if err := json.Unmarshal(modeRaw, &mode); err != nil || (mode != ModeDry && mode != ModeLive) {
		writeDebugError(w, ErrInvalid)
		return
	}
	confirmation := ""
	rawConfirmation, hasConfirmation := fields["confirmation_id"]
	if mode == ModeDry {
		// The frozen dry body is exactly {"mode":"dry"}; even null or an
		// empty confirmation member is an invalid shape, not an omitted value.
		if hasConfirmation {
			writeDebugError(w, ErrInvalid)
			return
		}
	} else {
		// Live mode must carry one non-empty string confirmation.  In
		// particular, JSON null must not collapse into Go's empty string and
		// accidentally become an omitted field.
		if !hasConfirmation || json.Unmarshal(rawConfirmation, &confirmation) != nil || confirmation == "" || len(confirmation) > MaxControlBodyBytes {
			writeDebugError(w, ErrInvalid)
			return
		}
	}
	userID, binding, ok := h.identityFor(w, r)
	if !ok {
		return
	}
	metadata, err := h.hub.SetMode(userID, binding, mode, confirmation)
	if err != nil {
		writeDebugError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, metadata)
}

func (h *handler) events(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w)
		return
	}
	// SSE is still a fixed control request: an empty body is required and the
	// same 1 KiB hard gate applies before authentication/subscription work.
	if !requireEmptyBody(w, r) {
		return
	}
	if !acceptsEventStream(r) {
		writeDebugError(w, ErrInvalid)
		return
	}
	lastID, hasLastID, ok := lastEventID(r)
	if !ok {
		writeDebugError(w, ErrInvalid)
		return
	}
	userID, binding, ok := h.identityFor(w, r)
	if !ok {
		return
	}
	if _, ok := w.(http.Flusher); !ok {
		writeDebugError(w, ErrNoStream)
		return
	}
	subscription, err := h.hub.Subscribe(userID, binding, lastID, hasLastID)
	if err != nil {
		writeDebugError(w, err)
		return
	}
	defer subscription.Close()
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	initialController := http.NewResponseController(w)
	if err := setWriteDeadline(initialController); err != nil {
		writeDebugError(w, err)
		return
	}
	w.WriteHeader(http.StatusOK)
	if err := initialController.Flush(); err != nil {
		clearWriteDeadline(initialController)
		return
	}
	clearWriteDeadline(initialController)
	flusher := w.(http.Flusher)
	ticker := time.NewTicker(HeartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case envelope, open := <-subscription.Events():
			if !open {
				return
			}
			err := writeEvent(w, flusher, envelope)
			envelope.Release()
			if err != nil {
				return
			}
		case <-ticker.C:
			// Heartbeats do not refresh idle.  Re-run the identity callback so
			// logout/ban/session deletion fails closed while a stream is open.
			currentUser, currentBinding, identityErr := h.identity(r)
			if identityErr != nil || currentUser != userID || currentBinding != binding {
				h.hub.ForgetUserReason(userID, EndSessionInvalid)
				return
			}
			if active, _ := h.hub.RevalidateBinding(r.Context(), userID, binding); !active {
				return
			}
			if err := writeHeartbeat(w, flusher); err != nil {
				return
			}
		case <-r.Context().Done():
			return
		}
	}
}

func writeEvent(w http.ResponseWriter, flusher http.Flusher, envelope EventEnvelope) error {
	controller := http.NewResponseController(w)
	if err := setWriteDeadline(controller); err != nil {
		return err
	}
	defer clearWriteDeadline(controller)
	data, err := json.Marshal(envelope)
	if err != nil || len(data) > MaxEventBytes {
		return ErrTooLarge
	}
	if err := writeSSEString(w, "id: "+strconv.FormatUint(envelope.Seq, 10)+"\n"); err != nil {
		return err
	}
	if err := writeSSEString(w, "event: "+string(envelope.Type)+"\n"); err != nil {
		return err
	}
	if err := writeSSEString(w, "data: "); err != nil {
		return err
	}
	if n, err := w.Write(data); err != nil {
		return err
	} else if n != len(data) {
		return io.ErrShortWrite
	}
	if err := writeSSEString(w, "\n\n"); err != nil {
		return err
	}
	_ = flusher
	return controller.Flush()
}

func writeHeartbeat(w http.ResponseWriter, flusher http.Flusher) error {
	controller := http.NewResponseController(w)
	if err := setWriteDeadline(controller); err != nil {
		return err
	}
	defer clearWriteDeadline(controller)
	if err := writeSSEString(w, ": heartbeat\n\n"); err != nil {
		return err
	}
	_ = flusher
	return controller.Flush()
}

func writeSSEString(w io.Writer, value string) error {
	if w == nil {
		return io.ErrClosedPipe
	}
	n, err := io.WriteString(w, value)
	if err != nil {
		return err
	}
	if n != len(value) {
		return io.ErrShortWrite
	}
	return nil
}

func setWriteDeadline(controller *http.ResponseController) error {
	if controller == nil {
		return http.ErrNotSupported
	}
	if err := controller.SetWriteDeadline(time.Now().Add(WriteDeadline)); err != nil && !errors.Is(err, http.ErrNotSupported) {
		return err
	}
	return nil
}

func clearWriteDeadline(controller *http.ResponseController) {
	if controller != nil {
		_ = controller.SetWriteDeadline(time.Time{})
	}
}

func acceptsEventStream(r *http.Request) bool {
	if r == nil {
		return false
	}
	values := r.Header.Values("Accept")
	if len(values) != 1 {
		return false
	}
	for _, item := range strings.Split(values[0], ",") {
		if strings.EqualFold(strings.TrimSpace(strings.SplitN(item, ";", 2)[0]), "text/event-stream") {
			return true
		}
	}
	return false
}

func lastEventID(r *http.Request) (uint64, bool, bool) {
	if r == nil {
		return 0, false, false
	}
	values := r.Header.Values("Last-Event-ID")
	if len(values) == 0 {
		return 0, false, true
	}
	if len(values) != 1 || strings.TrimSpace(values[0]) != values[0] || values[0] == "" {
		return 0, false, false
	}
	value, err := strconv.ParseUint(values[0], 10, 64)
	return value, true, err == nil
}

func requireEmptyBody(w http.ResponseWriter, r *http.Request) bool {
	if r == nil || r.Body == nil {
		return true
	}
	data, err := io.ReadAll(io.LimitReader(r.Body, MaxControlBodyBytes+1))
	clear(data)
	if err != nil || len(data) != 0 {
		writeDebugError(w, ErrInvalid)
		return false
	}
	return true
}

func readControlBody(w http.ResponseWriter, r *http.Request, allowEmpty bool) ([]byte, bool) {
	if r == nil || r.Body == nil {
		if allowEmpty {
			return nil, true
		}
		writeDebugError(w, ErrInvalid)
		return nil, false
	}
	data, err := io.ReadAll(io.LimitReader(r.Body, MaxControlBodyBytes+1))
	if err != nil {
		clear(data)
		writeDebugError(w, ErrInvalid)
		return nil, false
	}
	if len(data) > MaxControlBodyBytes {
		clear(data)
		writeDebugError(w, ErrTooLarge)
		return nil, false
	}
	if len(data) == 0 && !allowEmpty {
		writeDebugError(w, ErrInvalid)
		return nil, false
	}
	return data, true
}

func setNoStore(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	setNoStore(w)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeMethodNotAllowed(w http.ResponseWriter) {
	setNoStore(w)
	httperr.WriteError(w, httperr.New(httperr.CodeMethodNotAllowed, "method not allowed"))
}

func writeDebugError(w http.ResponseWriter, err error) {
	setNoStore(w)
	code := httperr.CodeInternal
	message := "debug service unavailable"
	statusErr := err
	switch {
	case errors.Is(statusErr, ErrInvalid):
		code, message = httperr.CodeInvalidRequest, "invalid request"
	case errors.Is(statusErr, ErrUnauthorized):
		code, message = httperr.CodeUnauthorized, "authentication required"
	case errors.Is(statusErr, ErrNotFound):
		code, message = httperr.CodeNotFound, "debug session not found"
	case errors.Is(statusErr, ErrCapacity):
		code, message = httperr.CodeServiceUnavailable, "debug capacity unavailable"
	case errors.Is(statusErr, ErrTooLarge):
		code, message = httperr.CodeResourceLimitExceeded, "debug resource limit exceeded"
	case errors.Is(statusErr, ErrConfirmation):
		code, message = httperr.CodeForbidden, "live confirmation required"
	case errors.Is(statusErr, ErrNoStream):
		code, message = httperr.CodeServiceUnavailable, "streaming unavailable"
	case errors.Is(statusErr, ErrClosed):
		code, message = httperr.CodeServiceUnavailable, "debug service unavailable"
	case errors.Is(statusErr, errMethodNotAllowed):
		code, message = httperr.CodeMethodNotAllowed, "method not allowed"
	}
	httperr.WriteError(w, httperr.New(code, message))
}
