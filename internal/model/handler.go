package model

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/waiting-here/NonbiriAPI/internal/auth"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
)

// MaxBodyBytes bounds request bodies for the model routes. A body larger than
// this is rejected with payload_too_large before any JSON decoding. It is
// comfortably above any legitimate payload (provider/model <= 64 runes each,
// upstream_model_id <= 512 runes).
const MaxBodyBytes = 1 << 20

// IdentityResolver extracts the authenticated user id from a request. The
// production resolver is SessionIdentity (session-only); tests inject a fixed
// id. A request without a valid identity yields an error that the handler maps
// to unauthorized. The handler never reads identity from a header, and never
// accepts a caller-key (bearer) principal: SessionIdentity derives from
// auth.UserFromContext, which is user-session-only by construction.
type IdentityResolver func(*http.Request) (int64, error)

// SessionIdentity is the session-only identity resolver: it returns the user
// id established by a browser user-session principal and rejects everything
// else (no principal, admin session, or caller key). Management APIs mounted
// with this resolver can never be driven by the platform bearer credential.
func SessionIdentity(r *http.Request) (int64, error) {
	if r == nil {
		return 0, errors.New("model: no request")
	}
	user, ok := auth.UserFromContext(r.Context())
	if !ok || user == nil || user.ID <= 0 {
		return 0, errors.New("model: no user session")
	}
	return user.ID, nil
}

// HandlerDeps are the collaborators a Handler needs.
type HandlerDeps struct {
	Service  *Service
	Identity IdentityResolver
}

// Handler is the mountable user-station API for platform models and bindings.
// It does not mount itself; NewHandler returns an http.Handler for the
// integration rail to register under /api/models (the routes below carry the
// full path, mirroring the endpoint rail). A nil Identity denies every request
// (unauthorized), which keeps the boundary safe until the integration rail
// wires SessionIdentity.
type Handler struct {
	svc      *Service
	identity IdentityResolver
	mux      *http.ServeMux
}

// NewHandler builds the route tree.
func NewHandler(deps HandlerDeps) http.Handler {
	svc := deps.Service
	if svc == nil {
		svc = NewService(nil)
	}
	h := &Handler{
		svc:      svc,
		identity: deps.Identity,
		mux:      http.NewServeMux(),
	}
	h.mux.HandleFunc("GET /api/models", h.listModels)
	h.mux.HandleFunc("POST /api/models", h.createModel)
	h.mux.HandleFunc("GET /api/models/{id}", h.getModel)
	h.mux.HandleFunc("PATCH /api/models/{id}", h.updateModel)
	h.mux.HandleFunc("DELETE /api/models/{id}", h.deleteModel)
	h.mux.HandleFunc("GET /api/models/{id}/bindings", h.listBindings)
	h.mux.HandleFunc("POST /api/models/{id}/bindings", h.createBinding)
	h.mux.HandleFunc("PATCH /api/models/{id}/bindings/{bId}", h.updateBinding)
	h.mux.HandleFunc("DELETE /api/models/{id}/bindings/{bId}", h.deleteBinding)
	return h
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mux.ServeHTTP(w, r)
}

// authenticate resolves the caller's user id. A missing identity resolver, a
// resolver error, or a non-positive id all yield unauthorized.
func (h *Handler) authenticate(r *http.Request) (int64, bool) {
	if h.identity == nil {
		return 0, false
	}
	uid, err := h.identity(r)
	if err != nil || uid <= 0 {
		return 0, false
	}
	return uid, true
}

// --- model routes -----------------------------------------------------------

func (h *Handler) listModels(w http.ResponseWriter, r *http.Request) {
	uid, ok := h.authenticate(r)
	if !ok {
		writeErr(w, httperr.New(httperr.CodeUnauthorized, "authentication required"))
		return
	}
	models, err := h.svc.ListModels(r.Context(), uid)
	if err != nil {
		writeServiceErr(w, err)
		return
	}
	httperr.WriteJSON(w, http.StatusOK, modelListResponse(models))
}

func (h *Handler) createModel(w http.ResponseWriter, r *http.Request) {
	uid, ok := h.authenticate(r)
	if !ok {
		writeErr(w, httperr.New(httperr.CodeUnauthorized, "authentication required"))
		return
	}
	var req createModelRequest
	if derr := decodeModelRequest(r, &req); derr.Code != "" {
		writeErr(w, derr)
		return
	}
	m, err := h.svc.CreateModel(r.Context(), uid, req.Provider, req.Model, req.RouteStrategy, req.SilentRetry)
	if err != nil {
		writeServiceErr(w, err)
		return
	}
	httperr.WriteJSON(w, http.StatusCreated, modelResponse(m))
}

func (h *Handler) getModel(w http.ResponseWriter, r *http.Request) {
	uid, ok := h.authenticate(r)
	if !ok {
		writeErr(w, httperr.New(httperr.CodeUnauthorized, "authentication required"))
		return
	}
	id, ok := parsePathID(w, r, "id")
	if !ok {
		return
	}
	m, err := h.svc.GetModel(r.Context(), uid, id)
	if err != nil {
		writeServiceErr(w, err)
		return
	}
	httperr.WriteJSON(w, http.StatusOK, modelResponse(m))
}

func (h *Handler) updateModel(w http.ResponseWriter, r *http.Request) {
	uid, ok := h.authenticate(r)
	if !ok {
		writeErr(w, httperr.New(httperr.CodeUnauthorized, "authentication required"))
		return
	}
	id, ok := parsePathID(w, r, "id")
	if !ok {
		return
	}
	var req updateModelRequest
	if derr := decodeModelRequest(r, &req); derr.Code != "" {
		writeErr(w, derr)
		return
	}
	m, err := h.svc.UpdateModel(r.Context(), uid, id, req.Provider, req.Model, req.RouteStrategy, req.SilentRetry)
	if err != nil {
		writeServiceErr(w, err)
		return
	}
	httperr.WriteJSON(w, http.StatusOK, modelResponse(m))
}

func (h *Handler) deleteModel(w http.ResponseWriter, r *http.Request) {
	uid, ok := h.authenticate(r)
	if !ok {
		writeErr(w, httperr.New(httperr.CodeUnauthorized, "authentication required"))
		return
	}
	id, ok := parsePathID(w, r, "id")
	if !ok {
		return
	}
	if err := h.svc.DeleteModel(r.Context(), uid, id); err != nil {
		writeServiceErr(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}

// --- binding routes ---------------------------------------------------------

func (h *Handler) listBindings(w http.ResponseWriter, r *http.Request) {
	uid, ok := h.authenticate(r)
	if !ok {
		writeErr(w, httperr.New(httperr.CodeUnauthorized, "authentication required"))
		return
	}
	modelID, ok := parsePathID(w, r, "id")
	if !ok {
		return
	}
	bindings, err := h.svc.ListBindings(r.Context(), uid, modelID)
	if err != nil {
		writeServiceErr(w, err)
		return
	}
	httperr.WriteJSON(w, http.StatusOK, bindingListResponse(bindings))
}

func (h *Handler) createBinding(w http.ResponseWriter, r *http.Request) {
	uid, ok := h.authenticate(r)
	if !ok {
		writeErr(w, httperr.New(httperr.CodeUnauthorized, "authentication required"))
		return
	}
	modelID, ok := parsePathID(w, r, "id")
	if !ok {
		return
	}
	var req createBindingRequest
	if derr := decodeModelRequest(r, &req); derr.Code != "" {
		writeErr(w, derr)
		return
	}
	b, err := h.svc.CreateBinding(r.Context(), uid, modelID, req.EndpointKeyID, req.UpstreamModelID, req.Ord)
	if err != nil {
		writeServiceErr(w, err)
		return
	}
	httperr.WriteJSON(w, http.StatusCreated, bindingResponse(b))
}

func (h *Handler) updateBinding(w http.ResponseWriter, r *http.Request) {
	uid, ok := h.authenticate(r)
	if !ok {
		writeErr(w, httperr.New(httperr.CodeUnauthorized, "authentication required"))
		return
	}
	modelID, ok := parsePathID(w, r, "id")
	if !ok {
		return
	}
	bindingID, ok := parsePathID(w, r, "bId")
	if !ok {
		return
	}
	var req updateBindingRequest
	if derr := decodeModelRequest(r, &req); derr.Code != "" {
		writeErr(w, derr)
		return
	}
	b, err := h.svc.UpdateBinding(r.Context(), uid, modelID, bindingID, req.Ord, req.UpstreamModelID)
	if err != nil {
		writeServiceErr(w, err)
		return
	}
	httperr.WriteJSON(w, http.StatusOK, bindingResponse(b))
}

func (h *Handler) deleteBinding(w http.ResponseWriter, r *http.Request) {
	uid, ok := h.authenticate(r)
	if !ok {
		writeErr(w, httperr.New(httperr.CodeUnauthorized, "authentication required"))
		return
	}
	modelID, ok := parsePathID(w, r, "id")
	if !ok {
		return
	}
	bindingID, ok := parsePathID(w, r, "bId")
	if !ok {
		return
	}
	if err := h.svc.DeleteBinding(r.Context(), uid, modelID, bindingID); err != nil {
		writeServiceErr(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}

// --- request / response DTOs -----------------------------------------------

type createModelRequest struct {
	Provider      string  `json:"provider"`
	Model         string  `json:"model"`
	RouteStrategy *string `json:"route_strategy,omitempty"`
	SilentRetry   *bool   `json:"silent_retry,omitempty"`
}

type updateModelRequest struct {
	Provider      *string `json:"provider,omitempty"`
	Model         *string `json:"model,omitempty"`
	RouteStrategy *string `json:"route_strategy,omitempty"`
	SilentRetry   *bool   `json:"silent_retry,omitempty"`
}

type createBindingRequest struct {
	EndpointKeyID   int64  `json:"endpoint_key_id"`
	UpstreamModelID string `json:"upstream_model_id"`
	Ord             *int64 `json:"ord,omitempty"`
}

type updateBindingRequest struct {
	Ord             *int64  `json:"ord,omitempty"`
	UpstreamModelID *string `json:"upstream_model_id,omitempty"`
}

type modelResp struct {
	ID            int64  `json:"id"`
	Provider      string `json:"provider"`
	Model         string `json:"model"`
	FullName      string `json:"full_name"`
	RouteStrategy string `json:"route_strategy"`
	SilentRetry   bool   `json:"silent_retry"`
	BindingCount  int    `json:"binding_count"`
	CreatedAt     int64  `json:"created_at"`
	UpdatedAt     int64  `json:"updated_at"`
}

type bindingResp struct {
	ID              int64  `json:"id"`
	EndpointKeyID   int64  `json:"endpoint_key_id"`
	UpstreamModelID string `json:"upstream_model_id"`
	Ord             int64  `json:"ord"`
}

func modelResponse(m db.Model) modelResp {
	return modelResp{
		ID: m.ID, Provider: m.Provider, Model: m.Model, FullName: m.FullName,
		RouteStrategy: m.RouteStrategy, SilentRetry: m.SilentRetry,
		BindingCount: m.BindingCount,
		CreatedAt:    m.CreatedAt, UpdatedAt: m.UpdatedAt,
	}
}

func modelListResponse(models []db.Model) []modelResp {
	out := make([]modelResp, 0, len(models))
	for _, m := range models {
		out = append(out, modelResponse(m))
	}
	return out
}

func bindingResponse(b db.ModelBinding) bindingResp {
	return bindingResp{
		ID: b.ID, EndpointKeyID: b.EndpointKeyID,
		UpstreamModelID: b.UpstreamModelID, Ord: b.Ord,
	}
}

func bindingListResponse(bindings []db.ModelBinding) []bindingResp {
	out := make([]bindingResp, 0, len(bindings))
	for _, b := range bindings {
		out = append(out, bindingResponse(b))
	}
	return out
}

// --- helpers ---------------------------------------------------------------

// decodeModelRequest bounds the body and decodes JSON into dst, rejecting
// unknown fields, over-limit bodies, and trailing JSON tokens.
func decodeModelRequest(r *http.Request, dst any) httperr.Error {
	if r == nil || r.Body == nil {
		return httperr.New(httperr.CodeInvalidRequest, "request body is required")
	}
	r.Body = http.MaxBytesReader(nil, r.Body, MaxBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return decodeErrToHTTP(err)
	}
	// Decoder.More only describes the current array/object context; it is not
	// a top-level trailing-token check. Decode one more value and require EOF.
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			return httperr.New(httperr.CodeInvalidRequest, "unexpected trailing content")
		}
		return decodeErrToHTTP(err)
	}
	return httperr.Error{}
}

// decodeErrToHTTP maps JSON / body-limit errors to the stable envelope.
func decodeErrToHTTP(err error) httperr.Error {
	var maxBytesErr *http.MaxBytesError
	if errors.As(err, &maxBytesErr) {
		return httperr.New(httperr.CodePayloadTooLarge, "request body too large")
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return httperr.New(httperr.CodeInvalidRequest, "request body is required")
	}
	return httperr.New(httperr.CodeInvalidRequest, "malformed request body")
}

// parsePathID reads a positive int64 path parameter and writes an error
// envelope on failure.
func parsePathID(w http.ResponseWriter, r *http.Request, name string) (int64, bool) {
	raw := r.PathValue(name)
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		writeErr(w, httperr.New(httperr.CodeInvalidRequest, "invalid id"))
		return 0, false
	}
	return id, true
}

// writeServiceErr maps a service/repository error to the stable envelope. The
// generic default is internal; validation/conflict/not-found are mapped
// explicitly so callers receive the contracted status and code.
func writeServiceErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, db.ErrNotFound):
		writeErr(w, httperr.New(httperr.CodeNotFound, "not found"))
	case errors.Is(err, db.ErrConflict):
		writeErr(w, httperr.New(httperr.CodeConflict, "conflict"))
	case errors.Is(err, ErrInvalidRequest):
		writeErr(w, httperr.New(httperr.CodeInvalidRequest, "invalid request"))
	default:
		writeErr(w, httperr.New(httperr.CodeInternal, "internal error"))
	}
}

func writeErr(w http.ResponseWriter, e httperr.Error) {
	httperr.WriteError(w, e)
}
