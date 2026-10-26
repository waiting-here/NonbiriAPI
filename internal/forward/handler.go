package forward

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"

	"github.com/waiting-here/NonbiriAPI/internal/charityrouting"
	"github.com/waiting-here/NonbiriAPI/internal/connector/openai"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
	"github.com/waiting-here/NonbiriAPI/internal/requestattempt"
	"github.com/waiting-here/NonbiriAPI/internal/requestkind"
	"github.com/waiting-here/NonbiriAPI/internal/routing"
)

func NewHandler(service *Service) *Handler { return &Handler{service: service} }

func (handler *Handler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Cache-Control", "no-store")
	if request == nil || request.URL == nil {
		writeFailure(writer, platformFailure(httperr.CodeNotFound, "not found"))
		return
	}
	if failure := exactIngressFailure(request.Method, request.URL.Path, request.URL.EscapedPath()); failure != nil {
		writeFailure(writer, *failure)
		return
	}
	userID, err := CallerIdentity(request)
	if err != nil {
		writeFailure(writer, platformFailure(httperr.CodeUnauthorized, "authentication required"))
		return
	}
	if request.URL.RawQuery != "" || request.URL.ForceQuery {
		requestattempt.Stage(request.Context(), "preflight", "")
		failure := rejectionFailure(httperr.CodeInvalidRequest, "invalid request", "query", "query parameters are not supported")
		requestattempt.Detail(request.Context(), failure.detail)
		writeFailure(writer, failure)
		return
	}
	if handler == nil || handler.service == nil {
		writeFailure(writer, platformFailure(httperr.CodeServiceUnavailable, "service unavailable"))
		return
	}
	switch request.URL.Path {
	case "/v1/models":
		handler.models(writer, request, userID)
	default:
		if strings.HasPrefix(request.URL.Path, "/v1/models/") {
			handler.models(writer, request, userID)
			return
		}
		requestattempt.Stage(request.Context(), "preflight", "")
		handler.chat(writer, request, userID)
	}
}

func (handler *Handler) models(writer http.ResponseWriter, request *http.Request, userID int64) {
	requestattempt.Stage(request.Context(), "preflight", "")
	if hasRequestBody(request) {
		failure := rejectionFailure(httperr.CodeInvalidRequest, "invalid request", "body", "GET model discovery does not accept a request body")
		requestattempt.Detail(request.Context(), failure.detail)
		writeFailure(writer, failure)
		return
	}
	models, err := handler.service.ListModels(request.Context(), userID)
	if err != nil {
		if request.Context().Err() == nil {
			if errors.Is(err, routing.ErrResourceLimit) || errors.Is(err, charityrouting.ErrResourceLimit) {
				writeFailure(writer, platformFailure(httperr.CodeResourceLimitExceeded, "resource limit exceeded"))
			} else {
				writeFailure(writer, platformFailure(httperr.CodeInternal, "internal error"))
			}
		}
		return
	}
	if strings.HasPrefix(request.URL.Path, "/v1/models/") {
		name := strings.TrimPrefix(request.URL.Path, "/v1/models/")
		for _, model := range models.Data {
			if model.ID == name {
				writer.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(writer).Encode(model)
				return
			}
		}
		writeFailure(writer, platformFailure(httperr.CodeNotFound, "model not found"))
		return
	}
	writeModelList(writer, models)
}

func (handler *Handler) chat(writer http.ResponseWriter, request *http.Request, userID int64) {
	p, prepared := request.Context().Value(preparedIngressKey{}).(*preparedIngress)
	if !prepared {
		p = prepareIngress(request)
		defer p.clear()
	}
	if request.Context().Err() != nil {
		return
	}
	if p.failure != nil {
		requestattempt.Detail(request.Context(), p.failure.detail)
		writeFailure(writer, *p.failure)
		return
	}
	decoded, filtered, charity, err := handler.service.decodeEnvelope(request.Context(), userID, p.body, p.envelope, requestkind.OperationForPath(request.URL.Path), p.multipart)

	if err != nil {
		if request.Context().Err() == nil {
			handler.service.writePreAcceptanceFailure(request.Context(), writer, nil, nil, err, charity, request.Header.Get("Accept-Language"))
		}
		return
	}
	defer decoded.Clear()
	defer clear(filtered)
	requestattempt.Model(request.Context(), decoded.Model)
	if decoded.inference != nil {
		decoded.inference.SetOptionHeaders(request.Header)
	}
	handler.service.execute(request.Context(), writer, userID, decoded, filtered, p.mediaType, request.Header.Get("Accept-Language"), request.Header)
}

func validateChatMedia(request *http.Request) (string, bool) {
	if request == nil || len(request.Header.Values("Content-Encoding")) != 0 {
		return "", false
	}
	values := request.Header.Values("Content-Type")
	if len(values) == 0 {
		return "application/json", true
	}
	if len(values) != 1 {
		return "", false
	}
	mediaType, parameters, err := mime.ParseMediaType(values[0])
	if err == nil && mediaType == "multipart/form-data" && openai.SupportsInferenceMultipart(requestkind.OperationForPath(request.URL.Path)) && parameters["boundary"] != "" {
		return values[0], true
	}
	if err != nil || !strings.EqualFold(mediaType, "application/json") {
		return "", false
	}
	for key, value := range parameters {
		if !strings.EqualFold(key, "charset") || !strings.EqualFold(value, "utf-8") {
			return "", false
		}
	}
	return "application/json", true
}

func readBoundedBody(reader io.Reader, limit int64) ([]byte, error) {
	if reader == nil || limit < 1 {
		return nil, openai.ErrInvalidRequest
	}
	body, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		clear(body)
		return nil, openai.ErrInvalidRequest
	}
	if int64(len(body)) > limit {
		clear(body)
		return nil, openai.ErrPayloadTooLarge
	}
	return body, nil
}

func hasRequestBody(request *http.Request) bool {
	return request != nil && (request.ContentLength != 0 || len(request.TransferEncoding) != 0)
}
