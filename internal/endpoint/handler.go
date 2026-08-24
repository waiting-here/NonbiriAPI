package endpoint

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
	"github.com/waiting-here/NonbiriAPI/internal/strictjson"
)

// MaxBodyBytes bounds request bodies for the endpoint routes. A body larger
// than this is rejected with payload_too_large before any JSON decoding or
// cryptographic work. It is comfortably above any legitimate endpoint/key
// payload (note <= 256 runes, secret <= 4096 bytes).
const MaxBodyBytes = 1 << 20

const (
	maxEndpointJSONDepth  = strictjson.MaxDepth
	maxEndpointJSONFields = strictjson.MaxFields
)

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
	ep, err := h.svc.GetEndpoint(r.Context(), uid, endpointID)
	if err != nil {
		writeServiceErr(w, err)
		return
	}
	keys, err := h.svc.ListEndpointKeys(r.Context(), uid, endpointID)
	if err != nil {
		writeServiceErr(w, err)
		return
	}
	httperr.WriteJSON(w, http.StatusOK, keyListResponseForConnector(keys, ep.ConnectorType))
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
	ep, err := h.svc.GetEndpoint(r.Context(), uid, endpointID)
	if err != nil {
		writeServiceErr(w, err)
		return
	}
	var req createKeyRequest
	if derr := decodeEndpointRequest(r, &req); derr.Code != "" {
		writeErr(w, derr)
		return
	}
	// Transfer the decoded secret into a clearable slice and drop the request
	// field before entering the transactional persistence boundary.
	plaintext := []byte(req.Secret)
	req.Secret = ""
	defer clear(plaintext)
	var key db.EndpointKey
	if req.ForceStoreFalse.Present {
		value := req.ForceStoreFalse.Value
		key, err = h.svc.CreateEndpointKeyWithPolicy(r.Context(), uid, endpointID, plaintext, req.Note, req.Enabled, &value)
	} else {
		key, err = h.svc.CreateEndpointKey(r.Context(), uid, endpointID, plaintext, req.Note, req.Enabled)
	}
	if err != nil {
		writeServiceErr(w, err)
		return
	}
	httperr.WriteJSON(w, http.StatusCreated, keyResponseForConnector(key, ep.ConnectorType))
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
	ep, err := h.svc.GetEndpoint(r.Context(), uid, endpointID)
	if err != nil {
		writeServiceErr(w, err)
		return
	}
	var req updateKeyRequest
	if derr := decodeEndpointRequest(r, &req); derr.Code != "" {
		writeErr(w, derr)
		return
	}
	if req.Note == nil && req.Enabled == nil && !req.ForceStoreFalse.Present {
		// PATCH is a partial update, but an empty object is never a useful
		// operation.  Reject it before the repository can turn it into a
		// successful no-op response.
		writeErr(w, httperr.New(httperr.CodeInvalidRequest, "patch must contain a field"))
		return
	}
	var key db.EndpointKey
	if req.ForceStoreFalse.Present {
		value := req.ForceStoreFalse.Value
		key, err = h.svc.UpdateEndpointKeyWithPolicy(r.Context(), uid, endpointID, keyID, req.Note, req.Enabled, &value)
	} else {
		key, err = h.svc.UpdateEndpointKey(r.Context(), uid, endpointID, keyID, req.Note, req.Enabled)
	}
	if err != nil {
		writeServiceErr(w, err)
		return
	}
	httperr.WriteJSON(w, http.StatusOK, keyResponseForConnector(key, ep.ConnectorType))
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
	Secret          string             `json:"secret"`
	Note            *string            `json:"note,omitempty"`
	Enabled         *bool              `json:"enabled,omitempty"`
	ForceStoreFalse strictOptionalBool `json:"force_store_false,omitempty"`
}

type updateKeyRequest struct {
	Note            *string            `json:"note,omitempty"`
	Enabled         *bool              `json:"enabled,omitempty"`
	ForceStoreFalse strictOptionalBool `json:"force_store_false,omitempty"`
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
	ID              int64  `json:"id"`
	DisplayHead     string `json:"display_head"`
	DisplayTail     string `json:"display_tail"`
	Note            string `json:"note"`
	Enabled         bool   `json:"enabled"`
	ForceStoreFalse *bool  `json:"force_store_false,omitempty"`
	CreatedAt       int64  `json:"created_at"`
	UpdatedAt       int64  `json:"updated_at"`
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
	return keyResponseForConnector(k, string(ConnectorOpenAICompatible))
}

func keyResponseForConnector(k db.EndpointKey, connectorType string) endpointKeyResp {
	var forceStoreFalse *bool
	if connectorType == string(ConnectorOpenAICompatible) {
		value := k.ForceStoreFalse
		forceStoreFalse = &value
	}
	return endpointKeyResp{
		ID:              k.ID,
		DisplayHead:     k.DisplayHead,
		DisplayTail:     k.DisplayTail,
		Note:            k.Note,
		Enabled:         k.Enabled,
		ForceStoreFalse: forceStoreFalse,
		CreatedAt:       k.CreatedAt,
		UpdatedAt:       k.UpdatedAt,
	}
}

func keyListResponse(keys []db.EndpointKey) []endpointKeyResp {
	return keyListResponseForConnector(keys, string(ConnectorOpenAICompatible))
}

func keyListResponseForConnector(keys []db.EndpointKey, connectorType string) []endpointKeyResp {
	out := make([]endpointKeyResp, 0, len(keys))
	for _, k := range keys {
		out = append(out, keyResponseForConnector(k, connectorType))
	}
	return out
}

// strictOptionalBool distinguishes an omitted policy field from a JSON null
// while accepting only the JSON boolean literals. The strategy wire contract
// deliberately rejects numbers, strings, and null rather than coercing them.
type strictOptionalBool struct {
	Value   bool
	Present bool
}

func (b *strictOptionalBool) UnmarshalJSON(data []byte) error {
	switch string(bytes.TrimSpace(data)) {
	case "true":
		b.Value, b.Present = true, true
	case "false":
		b.Value, b.Present = false, true
	default:
		return errors.New("policy field must be a JSON boolean")
	}
	return nil
}

// --- helpers ---------------------------------------------------------------

// decodeEndpointRequest bounds the body and decodes JSON into dst, rejecting
// unknown fields, over-limit bodies, and trailing JSON tokens. It returns an
// httperr.Error ready to write.
func decodeEndpointRequest(r *http.Request, dst any) httperr.Error {
	if r == nil || r.Body == nil {
		return httperr.New(httperr.CodeInvalidRequest, "request body is required")
	}
	data, err := io.ReadAll(io.LimitReader(r.Body, MaxBodyBytes+1))
	if err != nil {
		clear(data)
		return httperr.New(httperr.CodeInvalidRequest, "malformed request body")
	}
	if int64(len(data)) > MaxBodyBytes {
		clear(data)
		return httperr.New(httperr.CodePayloadTooLarge, "request body too large")
	}
	defer clear(data)
	if err := rejectDuplicateJSONFields(data); err != nil {
		return httperr.New(httperr.CodeInvalidRequest, "malformed request body")
	}
	dec := json.NewDecoder(bytes.NewReader(data))
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

// rejectDuplicateJSONFields walks the JSON syntax before decoding into a Go
// struct. encoding/json otherwise silently keeps the last duplicate member,
// which would make policy PATCH semantics depend on wire ordering. The walk is
// deliberately syntax-only; DisallowUnknownFields below remains authoritative
// for the destination shape.
func rejectDuplicateJSONFields(data []byte) error {
	return strictjson.ValidateObject(data)
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
	var capErr *db.CapError
	if errors.As(err, &capErr) {
		writeErr(w, httperr.New(httperr.CodeResourceLimitExceeded, "resource limit reached").
			WithResourceLimit(capErr.Resource, capErr.Limit))
		return
	}
	switch {
	case errors.Is(err, db.ErrNotFound):
		writeErr(w, httperr.New(httperr.CodeNotFound, "not found"))
	case errors.Is(err, db.ErrConflict):
		writeErr(w, httperr.New(httperr.CodeConflict, "conflict"))
	case errors.Is(err, ErrConnectorImmutable):
		writeErr(w, httperr.New(httperr.CodeInvalidRequest, "connector type cannot be changed"))
	case errors.Is(err, db.ErrEndpointOriginConflict):
		writeErr(w, httperr.New(httperr.CodeConflict, "delete all endpoint keys before changing the endpoint origin"))
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
