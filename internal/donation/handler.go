package donation

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/waiting-here/NonbiriAPI/internal/credits"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
)

// maxBodyBytes bounds request bodies (nested submissions may carry several
// bounded secrets; the ceiling is far above any legitimate payload).
const maxBodyBytes = 1 << 20

// IdentityResolver extracts the authenticated user id from a request.
type IdentityResolver func(*http.Request) (int64, error)

// Handler is the mountable user-station /api/donations route tree.
type Handler struct {
	svc      *Service
	identity IdentityResolver
	mux      *http.ServeMux
}

// NewHandler builds the route tree. A nil identity resolver denies everything.
func NewHandler(svc *Service, identity IdentityResolver) http.Handler {
	h := &Handler{svc: svc, identity: identity, mux: http.NewServeMux()}
	h.mux.HandleFunc("GET /api/donations", h.list)
	h.mux.HandleFunc("POST /api/donations", h.create)
	h.mux.HandleFunc("GET /api/donations/{id}", h.get)
	h.mux.HandleFunc("PATCH /api/donations/{id}", h.update)
	h.mux.HandleFunc("DELETE /api/donations/{id}", h.delete)
	return h.mux
}

func (h *Handler) authenticate(w http.ResponseWriter, r *http.Request) (int64, bool) {
	if h.identity == nil {
		writeErr(w, httperr.New(httperr.CodeUnauthorized, "authentication required"))
		return 0, false
	}
	uid, err := h.identity(r)
	if err != nil || uid <= 0 {
		writeErr(w, httperr.New(httperr.CodeUnauthorized, "authentication required"))
		return 0, false
	}
	return uid, true
}

// list handles GET /api/donations?page=&page_size=.
func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	uid, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	page, pageSize, ok := parsePageParams(w, r)
	if !ok {
		return
	}
	donations, total, err := h.svc.List(r.Context(), uid, pageSize, (page-1)*pageSize)
	if err != nil {
		writeRepoErr(w, err)
		return
	}
	httperr.WriteJSON(w, http.StatusOK, listResp{
		Data:    donationsResp(donations),
		Total:   total,
		HasMore: page*pageSize < total,
	})
}

// create handles POST /api/donations.
func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	uid, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	var req createRequest
	if derr := decodeJSON(w, r, &req); derr.Code != "" {
		writeErr(w, derr)
		return
	}
	spec := CreateSpec{UserID: uid, Description: req.Description}
	if req.Description == "" {
		writeInvalid(w)
		return
	}
	exp, ok := parseNullableExpires(w, req.ExpiresAt)
	if !ok {
		return
	}
	spec.ExpiresAt = exp

	switch {
	case req.ExistingEndpoint != nil && req.NewEndpoint == nil:
		sel := &ExistingKeySelection{
			EndpointID: req.ExistingEndpoint.EndpointID,
			KeyIDs:     req.ExistingEndpoint.KeyIDs,
			Limits:     limitsMap(req.ExistingEndpoint.Keys),
		}
		if sel.EndpointID <= 0 || len(sel.KeyIDs) == 0 {
			writeInvalid(w)
			return
		}
		spec.Existing = sel
	case req.NewEndpoint != nil && req.ExistingEndpoint == nil:
		if req.NewEndpoint.BaseURL == "" || len(req.NewEndpoint.Keys) == 0 {
			writeInvalid(w)
			return
		}
		keys := make([]NewKeyEntry, 0, len(req.NewEndpoint.Keys))
		for _, k := range req.NewEndpoint.Keys {
			if k.Secret == "" {
				writeInvalid(w)
				return
			}
			mc, rpm, ok := parseKeyLimits(k.MaxConcurrency, k.RPMLimit)
			if !ok {
				writeInvalid(w)
				return
			}
			keys = append(keys, NewKeyEntry{
				Secret: []byte(k.Secret), Note: k.Note,
				MaxConcurrency: mc, RPMLimit: rpm,
			})
		}
		enabled := true
		if req.NewEndpoint.Enabled != nil {
			enabled = *req.NewEndpoint.Enabled
		}
		spec.New = &NewEndpointDraft{
			ConnectorType: req.NewEndpoint.ConnectorType,
			BaseURL:       req.NewEndpoint.BaseURL,
			Note:          req.NewEndpoint.Note,
			Enabled:       enabled,
			Keys:          keys,
		}
	default:
		writeInvalid(w)
		return
	}

	d, err := h.svc.Create(r.Context(), spec)
	if err != nil {
		writeServiceErr(w, err)
		return
	}
	httperr.WriteJSON(w, http.StatusCreated, donationResponse(d, nil, nil))
}

// get handles GET /api/donations/{id}.
func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	uid, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	id, ok := parsePathID(w, r)
	if !ok {
		return
	}
	d, keys, reviews, err := h.svc.Get(r.Context(), uid, id)
	if err != nil {
		writeRepoErr(w, err)
		return
	}
	httperr.WriteJSON(w, http.StatusOK, donationResponse(d, keys, reviews))
}

// update handles PATCH /api/donations/{id}: pending-only edits by the owner.
func (h *Handler) update(w http.ResponseWriter, r *http.Request) {
	uid, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	id, ok := parsePathID(w, r)
	if !ok {
		return
	}
	var req updateRequest
	if derr := decodeJSON(w, r, &req); derr.Code != "" {
		writeErr(w, derr)
		return
	}
	if req.Description == nil && req.ExpiresAt == nil && req.Keys == nil {
		writeInvalid(w)
		return
	}
	var descPtr *string
	if req.Description != nil {
		descVal := *req.Description
		descPtr = &descVal
	}
	expVal, expPresent, expOK := parseExpiresUpdate(req.ExpiresAt)
	if !expOK {
		writeInvalid(w)
		return
	}
	var expOuter **int64
	if expPresent {
		expOuter = &expVal
	}
	var keyIDsPtr *[]int64
	var limits []db.KeyLimitSpec
	if req.Keys != nil {
		keyIDsPtr = &req.Keys.KeyIDs
		for _, l := range req.Keys.Limits {
			mc, rpm, ok := parseKeyLimits(l.MaxConcurrency, l.RPMLimit)
			if !ok {
				writeInvalid(w)
				return
			}
			limits = append(limits, db.KeyLimitSpec{EndpointKeyID: l.EndpointKeyID, MaxConcurrency: mc, RPMLimit: rpm})
		}
	}
	d, err := h.svc.UpdatePending(r.Context(), uid, id, descPtr, expOuter, keyIDsPtr, limits)
	if err != nil {
		writeServiceErr(w, err)
		return
	}
	httperr.WriteJSON(w, http.StatusOK, donationResponse(d, nil, nil))
}

// delete handles DELETE /api/donations/{id}.
func (h *Handler) delete(w http.ResponseWriter, r *http.Request) {
	uid, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	id, ok := parsePathID(w, r)
	if !ok {
		return
	}
	if len(r.URL.Query()) != 0 {
		writeInvalid(w)
		return
	}
	if err := h.svc.Delete(r.Context(), uid, id); err != nil {
		writeServiceErr(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}

// --- wire shapes ------------------------------------------------------------

type createRequest struct {
	Description      string          `json:"description"`
	ExpiresAt        json.RawMessage `json:"expires_at,omitempty"`
	ExistingEndpoint *existingSelReq `json:"existing_endpoint,omitempty"`
	NewEndpoint      *newEndpointReq `json:"new_endpoint,omitempty"`
}

type existingSelReq struct {
	EndpointID int64      `json:"endpoint_id"`
	KeyIDs     []int64    `json:"key_ids"`
	Keys       []limitReq `json:"keys,omitempty"` // per-key charity limits for the selection
}

type limitReq struct {
	EndpointKeyID  int64 `json:"endpoint_key_id"`
	MaxConcurrency *int  `json:"max_concurrency,omitempty"`
	RPMLimit       *int  `json:"rpm_limit,omitempty"`
}

type newEndpointReq struct {
	ConnectorType string      `json:"connector_type,omitempty"`
	BaseURL       string      `json:"base_url"`
	Note          string      `json:"note,omitempty"`
	Enabled       *bool       `json:"enabled,omitempty"`
	Keys          []newKeyReq `json:"keys"`
}

type newKeyReq struct {
	Secret         string `json:"secret"`
	Note           string `json:"note,omitempty"`
	MaxConcurrency *int   `json:"max_concurrency,omitempty"`
	RPMLimit       *int   `json:"rpm_limit,omitempty"`
}

type updateRequest struct {
	Description *string         `json:"description,omitempty"`
	ExpiresAt   json.RawMessage `json:"expires_at,omitempty"`
	Keys        *keysReplaceReq `json:"keys,omitempty"`
}

type keysReplaceReq struct {
	KeyIDs []int64    `json:"key_ids"`
	Limits []limitReq `json:"limits,omitempty"`
}

type donationKeyResp struct {
	ID                   int64  `json:"id"`
	EndpointKeyID        *int64 `json:"endpoint_key_id"`
	DisplayHead          string `json:"display_head"`
	DisplayTail          string `json:"display_tail"`
	MaxConcurrency       int64  `json:"max_concurrency"`
	RPMLimit             int64  `json:"rpm_limit"`
	CreditsUsageCapMilli string `json:"credits_usage_cap_milli"`
	CreditsUsedMilli     string `json:"credits_used_milli"`
	CreditsReservedMilli string `json:"credits_reserved_milli"`
	Enabled              bool   `json:"enabled"`
	CreatedAt            int64  `json:"created_at"`
	UpdatedAt            int64  `json:"updated_at"`
}

type donationReviewResp struct {
	ID             int64  `json:"id"`
	ReviewerUserID *int64 `json:"reviewer_user_id"`
	ReviewerRole   string `json:"reviewer_role"`
	Action         string `json:"action"`
	Note           string `json:"note"`
	CreatedAt      int64  `json:"created_at"`
}

type donationResp struct {
	ID              int64                `json:"id"`
	UserID          int64                `json:"user_id"`
	EndpointID      *int64               `json:"endpoint_id"`
	EndpointBaseURL string               `json:"endpoint_base_url"`
	Status          string               `json:"status"`
	Enabled         bool                 `json:"enabled"`
	Description     string               `json:"description"`
	ReviewNote      string               `json:"review_note"`
	ExpiresAt       *int64               `json:"expires_at"`
	ReviewedAt      *int64               `json:"reviewed_at"`
	CreatedAt       int64                `json:"created_at"`
	UpdatedAt       int64                `json:"updated_at"`
	Keys            []donationKeyResp    `json:"keys"`
	Reviews         []donationReviewResp `json:"reviews,omitempty"`
}

type listResp struct {
	Data    []donationResp `json:"data"`
	Total   int            `json:"total"`
	HasMore bool           `json:"has_more"`
}

func donationsResp(ds []db.Donation) []donationResp {
	out := make([]donationResp, 0, len(ds))
	for _, d := range ds {
		out = append(out, donationResponse(d, nil, nil))
	}
	return out
}

// donationResponse projects one donation. Keys/reviews are included only when
// the caller fetched them; the projection never contains secret or ciphertext
// fields because none exist on the repository types.
func donationResponse(d db.Donation, keys []db.DonationKey, reviews []db.DonationReview) donationResp {
	resp := donationResp{
		ID: d.ID, UserID: d.UserID, EndpointID: d.EndpointID,
		EndpointBaseURL: d.EndpointBaseURL, Status: d.Status, Enabled: d.Enabled,
		Description: d.Description, ReviewNote: d.ReviewNote,
		ExpiresAt: d.ExpiresAt, ReviewedAt: d.ReviewedAt,
		CreatedAt: d.CreatedAt, UpdatedAt: d.UpdatedAt,
		Keys: make([]donationKeyResp, 0, len(keys)),
	}
	for _, k := range keys {
		resp.Keys = append(resp.Keys, donationKeyResp{
			ID: k.ID, EndpointKeyID: k.EndpointKeyID,
			DisplayHead: k.DisplayHead, DisplayTail: k.DisplayTail,
			MaxConcurrency: k.MaxConcurrency, RPMLimit: k.RPMLimit,
			CreditsUsageCapMilli: credits.FormatAmount(k.CreditsUsageCap),
			CreditsUsedMilli:     credits.FormatAmount(k.CreditsUsed),
			CreditsReservedMilli: credits.FormatAmount(k.CreditsReserved),
			Enabled:              k.Enabled,
			CreatedAt:            k.CreatedAt, UpdatedAt: k.UpdatedAt,
		})
	}
	for _, rv := range reviews {
		resp.Reviews = append(resp.Reviews, donationReviewResp{
			ID: rv.ID, ReviewerUserID: rv.ReviewerUserID, ReviewerRole: rv.ReviewerRole,
			Action: rv.Action, Note: rv.Note, CreatedAt: rv.CreatedAt,
		})
	}
	return resp
}

// --- helpers ----------------------------------------------------------------

// limitsMap converts the optional per-key limit entries of a selection.
func limitsMap(entries []limitReq) map[int64]db.KeyLimitSpec {
	if len(entries) == 0 {
		return nil
	}
	out := make(map[int64]db.KeyLimitSpec, len(entries))
	for _, e := range entries {
		mc := int64(0)
		if e.MaxConcurrency != nil {
			mc = int64(*e.MaxConcurrency)
		}
		rpm := int64(0)
		if e.RPMLimit != nil {
			rpm = int64(*e.RPMLimit)
		}
		out[e.EndpointKeyID] = db.KeyLimitSpec{EndpointKeyID: e.EndpointKeyID, MaxConcurrency: mc, RPMLimit: rpm}
	}
	return out
}

// parseKeyLimits applies the non-negative bounds to donor-supplied counts.
func parseKeyLimits(maxConcurrency, rpmLimit *int) (int64, int64, bool) {
	mc := int64(0)
	if maxConcurrency != nil {
		if *maxConcurrency < 0 || int64(*maxConcurrency) > db.MaxDonationKeyConcurrency {
			return 0, 0, false
		}
		mc = int64(*maxConcurrency)
	}
	rpm := int64(0)
	if rpmLimit != nil {
		if *rpmLimit < 0 || int64(*rpmLimit) > db.MaxDonationKeyRPM {
			return 0, 0, false
		}
		rpm = int64(*rpmLimit)
	}
	return mc, rpm, true
}

// parseNullableExpires decodes the create-path tri-state expires_at field:
// absent or explicit null means "never expires" (nil); otherwise a positive
// unix-seconds integer is required.
func parseNullableExpires(w http.ResponseWriter, raw json.RawMessage) (*int64, bool) {
	if raw == nil || string(raw) == "null" {
		return nil, true
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var n json.Number
	if err := dec.Decode(&n); err != nil {
		writeInvalid(w)
		return nil, false
	}
	v, err := n.Int64()
	if err != nil || v <= 0 {
		writeInvalid(w)
		return nil, false
	}
	return &v, true
}

// parseExpiresUpdate decodes the PATCH-path tri-state: absent (unchanged,
// ok=false), explicit null (clear to never-expires, value=nil) or a positive
// integer.
func parseExpiresUpdate(raw json.RawMessage) (*int64, bool, bool) {
	if raw == nil {
		return nil, false, true
	}
	if string(raw) == "null" {
		return nil, true, true
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var n json.Number
	if err := dec.Decode(&n); err != nil {
		return nil, false, false
	}
	v, err := n.Int64()
	if err != nil || v <= 0 {
		return nil, false, false
	}
	return &v, true, true
}

// parsePageParams validates strict single-valued page/page_size queries
// (defaults 1/20, clamped [1,100]; unknown params are invalid_request).
func parsePageParams(w http.ResponseWriter, r *http.Request) (int, int, bool) {
	q := r.URL.Query()
	page, pageSize := 1, 20
	if raw := q["page"]; len(raw) > 1 {
		writeInvalid(w)
		return 0, 0, false
	} else if len(raw) == 1 {
		n, err := strconv.Atoi(raw[0])
		if err != nil || n < 1 || len(raw[0]) == 0 || raw[0] != strconv.Itoa(n) {
			writeInvalid(w)
			return 0, 0, false
		}
		page = n
	}
	if raw := q["page_size"]; len(raw) > 1 {
		writeInvalid(w)
		return 0, 0, false
	} else if len(raw) == 1 {
		n, err := strconv.Atoi(raw[0])
		if err != nil || n < 1 || n > 100 || raw[0] != strconv.Itoa(n) {
			writeInvalid(w)
			return 0, 0, false
		}
		pageSize = n
	}
	for key := range q {
		switch key {
		case "page", "page_size":
		default:
			writeInvalid(w)
			return 0, 0, false
		}
	}
	return page, pageSize, true
}

func parsePathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	raw := r.PathValue("id")
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		writeErr(w, httperr.New(httperr.CodeNotFound, "not found"))
		return 0, false
	}
	return id, true
}

// decodeJSON bounds the body and rejects unknown/trailing content.
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) httperr.Error {
	if r == nil || r.Body == nil {
		return httperr.New(httperr.CodeInvalidRequest, "request body is required")
	}
	r.Body = http.MaxBytesReader(nil, r.Body, maxBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			return httperr.New(httperr.CodePayloadTooLarge, "request body too large")
		}
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return httperr.New(httperr.CodeInvalidRequest, "request body is required")
		}
		return httperr.New(httperr.CodeInvalidRequest, "malformed request body")
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			return httperr.New(httperr.CodeInvalidRequest, "unexpected trailing content")
		}
		return httperr.New(httperr.CodeInvalidRequest, "malformed request body")
	}
	return httperr.Error{}
}

func writeInvalid(w http.ResponseWriter) {
	writeErr(w, httperr.New(httperr.CodeInvalidRequest, "invalid request"))
}

// writeServiceErr maps service/repository sentinels to stable envelopes.
func writeServiceErr(w http.ResponseWriter, err error) {
	var capErr *db.CapError
	if errors.As(err, &capErr) {
		writeErr(w, httperr.New(httperr.CodeResourceLimitExceeded, "resource limit reached").
			WithResourceLimit(capErr.Resource, capErr.Limit))
		return
	}
	switch {
	case errors.Is(err, ErrFeatureDisabled):
		writeErr(w, httperr.New(httperr.CodeFeatureDisabled, "捐赠提交暂未开放"))
	case errors.Is(err, db.ErrDonationKeyClaimConflict):
		writeErr(w, httperr.New(httperr.CodeConflict, "所选密钥已被其他捐赠占用"))
	case errors.Is(err, db.ErrResourceInActiveDonation):
		writeErr(w, httperr.New(httperr.CodeConflict, "资源正被审批中或启用中的捐赠使用，无法删除"))
	case errors.Is(err, db.ErrInvalidValue), errors.Is(err, ErrInvalidRequest):
		writeInvalid(w)
	case errors.Is(err, ErrSecretTooLong):
		writeErr(w, httperr.New(httperr.CodePayloadTooLarge, "secret too long"))
	case errors.Is(err, db.ErrNotFound):
		writeErr(w, httperr.New(httperr.CodeNotFound, "not found"))
	case errors.Is(err, db.ErrConflict):
		// A refusal decided against CURRENT STORED STATE (wrong review state,
		// already deleted, lost race) is a conflict, not a malformed request.
		writeErr(w, httperr.New(httperr.CodeConflict, "当前状态不允许该操作"))
	default:
		writeErr(w, httperr.New(httperr.CodeInternal, "internal error"))
	}
}

// writeRepoErr maps bare repository sentinels (read paths).
func writeRepoErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, db.ErrDonationKeyClaimConflict):
		writeErr(w, httperr.New(httperr.CodeConflict, "所选密钥已被其他捐赠占用"))
	case errors.Is(err, db.ErrNotFound):
		writeErr(w, httperr.New(httperr.CodeNotFound, "not found"))
	case errors.Is(err, db.ErrInvalidValue):
		writeInvalid(w)
	case errors.Is(err, db.ErrConflict):
		writeErr(w, httperr.New(httperr.CodeConflict, "当前状态不允许该操作"))
	default:
		writeErr(w, httperr.New(httperr.CodeInternal, "internal error"))
	}
}

func writeErr(w http.ResponseWriter, e httperr.Error) {
	httperr.WriteError(w, e)
}
