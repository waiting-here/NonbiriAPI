package forward

import (
	"errors"
	"mime"
	"net/http"
	"strings"

	"github.com/waiting-here/NonbiriAPI/internal/auth"
	"github.com/waiting-here/NonbiriAPI/internal/connector/openai"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
)

// IdentityResolver extracts only a CallerKey-established user id. Production
// uses CallerIdentity; tests may inject a fixed server-side identity.
type IdentityResolver func(*http.Request) (int64, error)

// CallerIdentity rejects browser/admin sessions and accepts only the principal
// installed by auth.CallerKeyMiddleware.
func CallerIdentity(request *http.Request) (int64, error) {
	if request == nil {
		return 0, errors.New("forward: request is required")
	}
	user, ok := auth.CallerUserFromContext(request.Context())
	if !ok || user == nil || user.ID <= 0 || user.IsAdmin || user.IsBanned {
		return 0, errors.New("forward: caller key identity is required")
	}
	return user.ID, nil
}

// HandlerDeps are the mountable exit handler's collaborators. The integration
// layer wraps this handler in auth.CallerKeyMiddleware before registration.
type HandlerDeps struct {
	Service  *Service
	Identity IdentityResolver
}

// Handler serves only the two alpha OpenAI-compatible exit paths. It does not
// register itself in main or accept a user-session identity.
type Handler struct {
	service  *Service
	identity IdentityResolver
	mux      *http.ServeMux
}

func NewHandler(deps HandlerDeps) http.Handler {
	handler := &Handler{
		service:  deps.Service,
		identity: deps.Identity,
		mux:      http.NewServeMux(),
	}
	handler.mux.HandleFunc("/v1/models", handler.models)
	handler.mux.HandleFunc("/v1/chat/completions", handler.chatCompletions)
	return handler
}

func (h *Handler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	h.mux.ServeHTTP(writer, request)
}

func (h *Handler) models(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		writeError(writer, httperr.CodeMethodNotAllowed, "method not allowed", "")
		return
	}
	userID, ok := h.authenticate(request)
	if !ok {
		writeError(writer, httperr.CodeUnauthorized, "authentication required", "")
		return
	}
	if h.service == nil {
		writeError(writer, httperr.CodeInternal, "internal error", "")
		return
	}
	models, err := h.service.ListModels(request.Context(), userID)
	if err != nil {
		if request.Context().Err() != nil {
			return
		}
		writeError(writer, httperr.CodeInternal, "internal error", "")
		return
	}
	httperr.WriteJSON(writer, http.StatusOK, models)
}

func (h *Handler) chatCompletions(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		writeError(writer, httperr.CodeMethodNotAllowed, "method not allowed", "")
		return
	}
	userID, ok := h.authenticate(request)
	if !ok {
		writeError(writer, httperr.CodeUnauthorized, "authentication required", "")
		return
	}
	if h.service == nil {
		writeError(writer, httperr.CodeInternal, "internal error", "")
		return
	}
	if !validRequestContentType(request) || request.Header.Get("Content-Encoding") != "" {
		writeError(writer, httperr.CodeInvalidRequest, "invalid request", "")
		return
	}
	chatRequest, err := openai.DecodeChatRequest(request.Body, openai.MaxRequestBodyBytes)
	if err != nil {
		if request.Context().Err() != nil {
			return
		}
		if errors.Is(err, openai.ErrPayloadTooLarge) {
			writeError(writer, httperr.CodePayloadTooLarge, "request body too large", "")
		} else {
			writeError(writer, httperr.CodeInvalidRequest, "invalid request", "")
		}
		return
	}
	defer chatRequest.Clear()

	result, err := h.service.Forward(request.Context(), writer, userID, chatRequest)
	if err != nil {
		if request.Context().Err() != nil {
			return
		}
		switch {
		case errors.Is(err, ErrModelNotFound):
			writeError(writer, httperr.CodeNotFound, "model not found", "")
		case errors.Is(err, ErrUnboundModel):
			writeError(writer, httperr.CodeUnboundModel, "model has no usable binding", "")
		default:
			writeError(writer, httperr.CodeInternal, "internal error", "")
		}
		return
	}
	if result.Success || result.Committed || result.SinkFailed || result.Failure == openai.FailureCanceled || request.Context().Err() != nil {
		return
	}
	switch result.Failure {
	case openai.FailureUpstream:
		writeError(writer, httperr.CodeUpstream, "upstream request failed", result.Diagnostic)
	default:
		writeError(writer, httperr.CodeInternal, "internal error", "")
	}
}

func (h *Handler) authenticate(request *http.Request) (int64, bool) {
	if h == nil || h.identity == nil {
		return 0, false
	}
	userID, err := h.identity(request)
	return userID, err == nil && userID > 0
}

func validRequestContentType(request *http.Request) bool {
	if request == nil {
		return false
	}
	values := request.Header.Values("Content-Type")
	if len(values) == 0 {
		return true
	}
	if len(values) != 1 {
		return false
	}
	mediaType, params, err := mime.ParseMediaType(values[0])
	if err != nil || !strings.EqualFold(mediaType, "application/json") {
		return false
	}
	for key, value := range params {
		if !strings.EqualFold(key, "charset") || !strings.EqualFold(value, "utf-8") {
			return false
		}
	}
	return true
}

func writeError(writer http.ResponseWriter, code, message, diagnostic string) {
	err := httperr.New(code, message)
	if diagnostic != "" {
		err = err.WithDiag(diagnostic)
	}
	httperr.WriteError(writer, err)
}
