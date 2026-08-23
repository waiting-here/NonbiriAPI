package forward

import (
	"context"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"strconv"
	"strings"

	"github.com/waiting-here/NonbiriAPI/internal/auth"
	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/connector/openai"
	"github.com/waiting-here/NonbiriAPI/internal/db"
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

// CharityRail is the [公益]-namespace routing exit, implemented by the charity
// routing service and wired optionally. When absent, [公益]-prefixed models
// are not addressable and the personal-only deployment shape is preserved.
// The two namespaces are disjoint by construction: the handler dispatches by
// the fixed [公益] prefix, so a personal model name can never enter the
// charity rail and a charity model can never enter the personal one.
type CharityRail interface {
	ListCallerModels(ctx context.Context) ([]db.CallerModel, error)
	Forward(ctx context.Context, writer http.ResponseWriter, userID int64, request *openai.ChatRequest) (connectorcontract.AttemptResult, error)
}

// Charity control-flow sentinels returned by CharityRail.Forward and mapped by
// the handler onto the stable envelope. They carry no request or secret
// material and are defined here (not in the charity routing package) so the
// handler can match them without an import cycle.
var (
	ErrCharityModelNotFound   = errors.New("charity: model not found")
	ErrCharityUnboundModel    = errors.New("charity: model has no usable candidate")
	ErrCharityDisabled        = errors.New("charity: charity routing is disabled")
	ErrCharitySuspended       = errors.New("charity: caller charity eligibility is suspended")
	ErrCharityKeysExhausted   = errors.New("charity: all donation keys exhausted")
	ErrCharityContentTooShort = errors.New("charity: content is too short")
	ErrAntiAbuseUnavailable   = errors.New("charity: anti-abuse policy unavailable")
)

// ContentTooShortError carries only bounded numeric policy context. It
// unwraps to ErrCharityContentTooShort so the handler can keep one stable wire
// code while still reporting actual and configured rune counts.
type ContentTooShortError struct {
	Actual  int
	Minimum int
}

func (e *ContentTooShortError) Error() string {
	if e == nil {
		return ErrCharityContentTooShort.Error()
	}
	return fmt.Sprintf("charity content has %d characters; minimum is %d", e.Actual, e.Minimum)
}
func (e *ContentTooShortError) Unwrap() error { return ErrCharityContentTooShort }

// HandlerDeps are the mountable exit handler's collaborators. The integration
// layer wraps this handler in auth.CallerKeyMiddleware before registration.
type HandlerDeps struct {
	Service  *Service
	Charity  CharityRail
	Identity IdentityResolver
}

// Handler serves only the two alpha OpenAI-compatible exit paths. It does not
// register itself in main or accept a user-session identity.
type Handler struct {
	service  *Service
	charity  CharityRail
	identity IdentityResolver
	mux      *http.ServeMux
}

func NewHandler(deps HandlerDeps) http.Handler {
	handler := &Handler{
		service:  deps.Service,
		charity:  deps.Charity,
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
	// The [公益] namespace is additive: when the charity rail is wired, its
	// projection is appended after the personal models. The two namespaces
	// are disjoint by construction (provider prefix guard + routing prefix),
	// so no deduplication is needed; the charity projection itself is empty
	// while the site switch is off.
	if h.charity != nil {
		charityModels, cerr := h.charity.ListCallerModels(request.Context())
		if cerr != nil {
			if request.Context().Err() != nil {
				return
			}
			writeError(writer, httperr.CodeInternal, "internal error", "")
			return
		}
		for _, model := range charityModels {
			if !validStoredModel(model) {
				writeError(writer, httperr.CodeInternal, "internal error", "")
				return
			}
			models.Data = append(models.Data, Model{
				ID:      model.FullName,
				Object:  "model",
				Created: model.CreatedAt,
				OwnedBy: model.Provider,
			})
		}
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

	// Namespace dispatch by the fixed [公益] prefix. A personal model name
	// never enters the charity rail; a [公益] model never enters the personal
	// one. When no charity rail is wired, [公益] models are not addressable
	// (404), preserving the personal-only deployment shape.
	if strings.HasPrefix(chatRequest.Model, db.CharityPrefix) {
		if h.charity == nil {
			writeError(writer, httperr.CodeNotFound, "model not found", "")
			return
		}
		h.charityChat(writer, request, userID, chatRequest)
		return
	}

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
	if result.Success || result.Committed || result.SinkFailed || result.Failure == connectorcontract.FailureCanceled || request.Context().Err() != nil {
		return
	}
	switch result.Failure {
	case connectorcontract.FailureUpstream:
		writeError(writer, httperr.CodeUpstream, "upstream request failed", result.Diagnostic)
	default:
		writeError(writer, httperr.CodeInternal, "internal error", "")
	}
}

// charityChat dispatches one [公益] request through the charity rail and maps
// its control-flow errors onto the stable envelope. A committed result
// (success or committed-failure) is already written to the client; only
// pre-dispatch failures reach the envelope.
func (h *Handler) charityChat(writer http.ResponseWriter, request *http.Request, userID int64, chatRequest *openai.ChatRequest) {
	result, err := h.charity.Forward(request.Context(), writer, userID, chatRequest)
	if err != nil {
		if request.Context().Err() != nil {
			return
		}
		switch {
		case errors.Is(err, ErrCharityContentTooShort), errors.Is(err, ErrAntiAbuseUnavailable), errors.Is(err, openai.ErrInvalidRequest):
			h.writeCharityPreflightError(writer, err)
		case errors.Is(err, ErrCharityModelNotFound):
			writeError(writer, httperr.CodeNotFound, "model not found", "")
		case errors.Is(err, ErrCharityUnboundModel):
			writeError(writer, httperr.CodeUnboundModel, "model has no usable binding", "")
		case errors.Is(err, ErrCharityDisabled):
			writeError(writer, httperr.CodeFeatureDisabled, "charity is disabled", "")
		case errors.Is(err, ErrCharitySuspended):
			writeError(writer, httperr.CodeCharitySuspended, "charity eligibility is suspended", "")
		case errors.Is(err, ErrCharityKeysExhausted):
			writeError(writer, httperr.CodeRateLimited, "all donation keys exhausted", "")
		case errors.Is(err, db.ErrInsufficientCredits):
			writeError(writer, httperr.CodeInsufficientCredits, "悠哉积分不足", "")
		default:
			writeError(writer, httperr.CodeInternal, "internal error", "")
		}
		return
	}
	if result.Success || result.Committed || result.SinkFailed || result.Failure == connectorcontract.FailureCanceled || request.Context().Err() != nil {
		return
	}
	switch result.Failure {
	case connectorcontract.FailureUpstream:
		writeError(writer, httperr.CodeUpstream, "upstream request failed", result.Diagnostic)
	default:
		writeError(writer, httperr.CodeInternal, "internal error", "")
	}
}

func (h *Handler) writeCharityPreflightError(writer http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrCharityContentTooShort):
		message := "charity content is too short"
		var tooShort *ContentTooShortError
		if errors.As(err, &tooShort) && tooShort != nil && tooShort.Actual >= 0 && tooShort.Minimum >= 0 {
			message = "charity content is too short: " + strconv.Itoa(tooShort.Actual) + " < " + strconv.Itoa(tooShort.Minimum)
		}
		writeError(writer, httperr.CodeContentTooShort, message, "")
	case errors.Is(err, ErrCharitySuspended):
		writeError(writer, httperr.CodeCharitySuspended, "charity eligibility is suspended", "")
	case errors.Is(err, ErrCharityDisabled):
		writeError(writer, httperr.CodeFeatureDisabled, "charity is disabled", "")
	case errors.Is(err, openai.ErrInvalidRequest):
		writeError(writer, httperr.CodeInvalidRequest, "invalid request", "")
	case errors.Is(err, ErrAntiAbuseUnavailable):
		writeError(writer, httperr.CodeServiceUnavailable, "service unavailable", "")
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
