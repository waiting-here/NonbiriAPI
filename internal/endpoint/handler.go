package endpoint

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"nonbiriapi/internal/db"
	"nonbiriapi/internal/httperr"
)

// MaxBodyBytes bounds request bodies for the endpoint routes. A body larger
// than this is rejected with payload_too_large before any JSON decoding or
// cryptographic work. It is comfortably above any legitimate endpoint/key
// payload (note <= 256 runes, secret <= 4096 bytes).
const MaxBodyBytes = 1 << 20

// IdentityResolver extracts the authenticated user id from a request. The
// production resolver is wired by the identity rail (H) from its session
// context; tests inject a fixed id. A request without a valid identity yields
// an error that the handler maps to unauthorized. The handler never reads
// identity from a hidden button or an untrusted header.
type IdentityResolver func(*http.Request) (int64, error)

// HandlerDeps are the collaborators a Handler needs.
type HandlerDeps struct {
	Service  *Service
	Identity IdentityResolver
}

// Handler is the mountable user-station API for Endpoint and EndpointKey CRUD.
// It does not mount itself; NewHandler returns an http.Handler for the
// integration rail to register under /api/endpoints and /api/endpoints/. The
// model-list and manual-refresh routes under .../keys/{keyId}/models are
// intentionally not mounted here; they belong to the model-fetch rail (J).
type Handler struct {
	svc      *Service
	identity IdentityResolver
	mux      *http.ServeMux
}

// NewHandler builds the route tree. A nil Identity denies every request
// (unauthorized), which keeps the boundary safe until the identity rail wires
// its resolver.
func NewHandler(deps HandlerDeps) http.Handler {
	svc := deps.Service
	if svc == nil {
		svc = NewService(ServiceDeps{})
	}
	h := &Handler{
		svc:      svc,
		identity: deps.Identity,
		mux:      http.NewServeMux(),
	}
	h.mux.HandleFunc("GET /api/endpoints", h.listEndpoints)
	h.mux.HandleFunc("POST /api/endpoints", h.createEndpoint)
	h.mux.HandleFunc("GET /api/endpoints/{id}", h.getEndpoint)
	h.mux.HandleFunc("PATCH /api/endpoints/{id}", h.updateEndpoint)
	h.mux.HandleFunc("DELETE /api/endpoints/{id}", h.deleteEndpoint)
	h.mux.HandleFunc("GET /api/endpoints/{id}/keys", h.listKeys)
	h.mux.HandleFunc("POST /api/endpoints/{id}/keys", h.createKey)
	h.mux.HandleFunc("PATCH /api/endpoints/{id}/keys/{keyId}", h.updateKey)
	h.mux.HandleFunc("DELETE /api/endpoints/{id}/keys/{keyId}", h.deleteKey)
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

// --- Endpoint routes --------------------------------------------------------

func (h *Handler) listEndpoints(w http.ResponseWriter, r *http.Request) {
	uid, ok := h.authenticate(r)
	if !ok {
		writeErr(w, httperr.New(httperr.CodeUnauthorized, "authentication required"))
		return
	}
	eps, err := h.svc.ListEndpoints(r.Context(), uid)
	if err != nil {
		writeServiceErr(w, err)
		return
	}
	httperr.WriteJSON(w, http.StatusOK, endpointListResponse(eps))
}

func (h *Handler) createEndpoint(w http.ResponseWriter, r *http.Request) {
	uid, ok := h.authenticate(r)
	if !ok {
		writeErr(w, httperr.New(httperr.CodeUnauthorized, "authentication required"))
		return
	}
	var req createEndpointRequest
	if derr := decodeEndpointRequest(r, &req); derr.Code != "" {
		writeErr(w, derr)
		return
	}
	connectorType := ""
	if req.ConnectorType != nil {
		connectorType = *req.ConnectorType
	}
	ep, err := h.svc.CreateEndpoint(r.Context(), uid, connectorType, req.BaseURL, req.Note, req.Enabled)
	if err != nil {
		writeServiceErr(w, err)
		return
	}
	httperr.WriteJSON(w, http.StatusCreated, endpointResponse(ep))
}

func (h *Handler) getEndpoint(w http.ResponseWriter, r *http.Request) {
	uid, ok := h.authenticate(r)
	if !ok {
		writeErr(w, httperr.New(httperr.CodeUnauthorized, "authentication required"))
		return
	}
	id, ok := parsePathID(w, r, "id")
	if !ok {
		return
	}
	ep, err := h.svc.GetEndpoint(r.Context(), uid, id)
	if err != nil {
		writeServiceErr(w, err)
		return
	}
	httperr.WriteJSON(w, http.StatusOK, endpointResponse(ep))
}

func (h *Handler) updateEndpoint(w http.ResponseWriter, r *http.Request) {
	uid, ok := h.authenticate(r)
	if !ok {
		writeErr(w, httperr.New(httperr.CodeUnauthorized, "authentication required"))
		return
	}
	id, ok := parsePathID(w, r, "id")
	if !ok {
		return
	}
	var req updateEndpointRequest
	if derr := decodeEndpointRequest(r, &req); derr.Code != "" {
		writeErr(w, derr)
		return
	}
	ep, err := h.svc.UpdateEndpoint(r.Context(), uid, id, req.BaseURL, req.Note, req.Enabled, req.ConnectorType)
	if err != nil {
		writeServiceErr(w, err)
		return
	}
	httperr.WriteJSON(w, http.StatusOK, endpointResponse(ep))
}

func (h *Handler) deleteEndpoint(w http.ResponseWriter, r *http.Request) {
	uid, ok := h.authenticate(r)
	if !ok {
		writeErr(w, httperr.New(httperr.CodeUnauthorized, "authentication required"))
		return
	}
	id, ok := parsePathID(w, r, "id")
	if !ok {
		return
	}
	if err := h.svc.DeleteEndpoint(r.Context(), uid, id); err != nil {
		writeServiceErr(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}

// --- EndpointKey routes -----------------------------------------------------

func (h *Handler) listKeys(w http.ResponseWriter, r *http.Request) {
	uid, ok := h.authenticate(r)
	if !ok {
		writeErr(w, httperr.New(httperr.CodeUnauthorized, "authentication required"))
		return
	}
	endpointID, ok := parsePathID(w, r, "id")
	if !ok {
		return
	}
	keys, err := h.svc.ListEndpointKeys(r.Context(), uid, endpointID)
	if err != nil {
		writeServiceErr(w, err)
		return
	}
	httperr.WriteJSON(w, http.StatusOK, keyListResponse(keys))
}

func (h *Handler) createKey(w http.ResponseWriter, r *http.Request) {
	uid, ok := h.authenticate(r)
	if !ok {
		writeErr(w, httperr.New(httperr.CodeUnauthorized, "authentication required"))
		return
	}
	endpointID, ok := parsePathID(w, r, "id")
	if !ok {
		return
	}
	var req createKeyRequest
	if derr := decodeEndpointRequest(r, &req); derr.Code != "" {
		writeErr(w, derr)
		return
	}
	// Convert the string secret to bytes for the codec boundary; the service
	// clears this slice once sealing is done.
	key, err := h.svc.CreateEndpointKey(r.Context(), uid, endpointID, []byte(req.Secret), req.Note, req.Enabled)
	if err != nil {
		writeServiceErr(w, err)
		return
	}
	httperr.WriteJSON(w, http.StatusCreated, keyResponse(key))
}

func (h *Handler) updateKey(w http.ResponseWriter, r *http.Request) {
	uid, ok := h.authenticate(r)
	if !ok {
		writeErr(w, httperr.New(httperr.CodeUnauthorized, "authentication required"))
		return
	}
	endpointID, ok := parsePathID(w, r, "id")
	if !ok {
		return
	}
	keyID, ok := parsePathID(w, r, "keyId")
	if !ok {
		return
	}
	var req updateKeyRequest
	if derr := decodeEndpointRequest(r, &req); derr.Code != "" {
		writeErr(w, derr)
		return
	}
	key, err := h.svc.UpdateEndpointKey(r.Context(), uid, endpointID, keyID, req.Note, req.Enabled)
	if err != nil {
		writeServiceErr(w, err)
		return
	}
	httperr.WriteJSON(w, http.StatusOK, keyResponse(key))
}

func (h *Handler) deleteKey(w http.ResponseWriter, r *http.Request) {
	uid, ok := h.authenticate(r)
	if !ok {
		writeErr(w, httperr.New(httperr.CodeUnauthorized, "authentication required"))
		return
	}
	endpointID, ok := parsePathID(w, r, "id")
	if !ok {
		return
	}
	keyID, ok := parsePathID(w, r, "keyId")
	if !ok {
		return
	}
	if err := h.svc.DeleteEndpointKey(r.Context(), uid, endpointID, keyID); err != nil {
		writeServiceErr(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}

// --- request / response DTOs -----------------------------------------------

type createEndpointRequest struct {
	ConnectorType *string `json:"connector_type,omitempty"`
	BaseURL       string  `json:"base_url"`
	Note          *string `json:"note,omitempty"`
	Enabled       *bool   `json:"enabled,omitempty"`
}

type updateEndpointRequest struct {
	BaseURL *string `json:"base_url,omitempty"`
	Note    *string `json:"note,omitempty"`
	Enabled *bool   `json:"enabled,omitempty"`
	// ConnectorType is immutable after creation. A present value is rejected by
	// the service (ErrConnectorImmutable). It is decoded so presence can be
	// detected and refused, rather than silently ignored.
	ConnectorType *string `json:"connector_type,omitempty"`
}

type createKeyRequest struct {
	Secret  string  `json:"secret"`
	Note    *string `json:"note,omitempty"`
	Enabled *bool   `json:"enabled,omitempty"`
}

type updateKeyRequest struct {
	Note    *string `json:"note,omitempty"`
	Enabled *bool   `json:"enabled,omitempty"`
}

type endpointResp struct {
	ID                 int64  `json:"id"`
	ConnectorType      string `json:"connector_type"`
	BaseURL            string `json:"base_url"`
	Note               string `json:"note"`
	Enabled            bool   `json:"enabled"`
	ModelFetchFailed   bool   `json:"model_fetch_failed"`
	ModelFetchFailedAt int64  `json:"model_fetch_failed_at"`
	CreatedAt          int64  `json:"created_at"`
	UpdatedAt          int64  `json:"updated_at"`
}

type endpointKeyResp struct {
	ID          int64  `json:"id"`
	DisplayHead string `json:"display_head"`
	DisplayTail string `json:"display_tail"`
	Note        string `json:"note"`
	Enabled     bool   `json:"enabled"`
	CreatedAt   int64  `json:"created_at"`
	UpdatedAt   int64  `json:"updated_at"`
}

func endpointResponse(ep db.Endpoint) endpointResp {
	return endpointResp{
		ID:                 ep.ID,
		ConnectorType:      ep.ConnectorType,
		BaseURL:            ep.BaseURL,
		Note:               ep.Note,
		Enabled:            ep.Enabled,
		ModelFetchFailed:   ep.ModelFetchFailed,
		ModelFetchFailedAt: ep.ModelFetchFailedAt,
		CreatedAt:          ep.CreatedAt,
		UpdatedAt:          ep.UpdatedAt,
	}
}

func endpointListResponse(eps []db.Endpoint) []endpointResp {
	out := make([]endpointResp, 0, len(eps))
	for _, ep := range eps {
		out = append(out, endpointResponse(ep))
	}
	return out
}

func keyResponse(k db.EndpointKey) endpointKeyResp {
	return endpointKeyResp{
		ID:          k.ID,
		DisplayHead: k.DisplayHead,
		DisplayTail: k.DisplayTail,
		Note:        k.Note,
		Enabled:     k.Enabled,
		CreatedAt:   k.CreatedAt,
		UpdatedAt:   k.UpdatedAt,
	}
}

func keyListResponse(keys []db.EndpointKey) []endpointKeyResp {
	out := make([]endpointKeyResp, 0, len(keys))
	for _, k := range keys {
		out = append(out, keyResponse(k))
	}
	return out
}

// --- helpers ---------------------------------------------------------------

// decodeEndpointRequest bounds the body and decodes JSON into dst, rejecting
// unknown fields, over-limit bodies, and trailing JSON tokens. It returns an
// httperr.Error ready to write.
func decodeEndpointRequest(r *http.Request, dst any) httperr.Error {
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
// generic default is internal; validation/cap/not-found are mapped explicitly
// so callers receive the contracted status and code.
func writeServiceErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, db.ErrNotFound):
		writeErr(w, httperr.New(httperr.CodeNotFound, "not found"))
	case errors.Is(err, db.ErrEndpointCap):
		writeErr(w, httperr.New(httperr.CodeForbidden, "endpoint cap reached"))
	case errors.Is(err, ErrConnectorImmutable):
		writeErr(w, httperr.New(httperr.CodeInvalidRequest, "connector type cannot be changed"))
	case errors.Is(err, ErrInvalidRequest):
		writeErr(w, httperr.New(httperr.CodeInvalidRequest, "invalid request"))
	case errors.Is(err, ErrPayloadTooLarge):
		writeErr(w, httperr.New(httperr.CodePayloadTooLarge, "payload too large"))
	default:
		writeErr(w, httperr.New(httperr.CodeInternal, "internal error"))
	}
}

func writeErr(w http.ResponseWriter, e httperr.Error) {
	httperr.WriteError(w, e)
}
