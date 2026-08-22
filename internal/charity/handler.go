// Package charity serves the charity model management rail (frozen §J.2/
// §J.2.5/§J.5, implementation contract §2.7/§6.3/§6.4): CRUD over charity
// models, their limited-time discounts, and bindings to donated keys.
//
// The same handler tree mounts under the administrator station
// (/admin/api/charity-models...) and the level-5 steward prefix
// (/api/steward/charity-models...): both share one service layer while each
// mounting frame resolves its own identity (admin session or live level>=5).
//
// Boundary rules:
//
//   - full_name always carries the fixed '[公益]' prefix; a provider that
//     itself starts with the prefix is rejected so the namespace can never be
//     double-prefixed or spoofed;
//   - exactly one price table is interpretable per model (per-request vs
//     per-token); all prices are canonical decimal milli-credit strings on the
//     wire and int64 in storage — no float ever participates;
//   - enabling a per-token model fails closed while
//     charity_token_reserve_milli is not configured;
//   - binding writes re-verify the full candidate predicate in SQL.
package charity

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/credits"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
)

const (
	maxBodyBytes       = 1 << 20
	maxProviderRunes   = 128
	maxModelRunes      = 256
	maxUpstreamIDRunes = 256
)

// Service orchestrates charity models over db.Store.
type Service struct {
	store *db.Store
	now   func() int64
}

// NewService builds the service.
func NewService(store *db.Store) *Service {
	return &Service{store: store, now: func() int64 { return time.Now().Unix() }}
}

// Sentinel errors mapped at the handler boundary.
var (
	ErrInvalidRequest = errors.New("charity: invalid request")
)

// Create validates and creates one charity model. actorUserID may be 0.
func (s *Service) Create(ctx context.Context, m db.CharityModel, actorUserID int64) (db.CharityModel, error) {
	if err := validateNames(m.Provider, m.Model); err != nil {
		return db.CharityModel{}, err
	}
	if err := validatePrices(&m); err != nil {
		return db.CharityModel{}, err
	}
	out, err := s.store.CreateCharityModel(ctx, m, actorUserID, s.now())
	if err != nil {
		return db.CharityModel{}, mapRepoError(err)
	}
	return out, nil
}

// Update applies one partial update.
func (s *Service) Update(ctx context.Context, id int64, upd db.CharityModelUpdate) (db.CharityModel, error) {
	if upd.Provider != nil {
		if err := validateName(*upd.Provider, maxProviderRunes); err != nil {
			return db.CharityModel{}, err
		}
		if err := validatePrefixRule(*upd.Provider); err != nil {
			return db.CharityModel{}, err
		}
	}
	if upd.Model != nil {
		if err := validateName(*upd.Model, maxModelRunes); err != nil {
			return db.CharityModel{}, err
		}
	}
	out, err := s.store.UpdateCharityModel(ctx, id, upd, s.now())
	if err != nil {
		return db.CharityModel{}, mapRepoError(err)
	}
	return out, nil
}

// Get returns one charity model with its rolling success counters.
func (s *Service) Get(ctx context.Context, id int64) (db.CharityModel, db.CharitySuccessRate, error) {
	m, err := s.store.GetCharityModel(ctx, id)
	if err != nil {
		return db.CharityModel{}, db.CharitySuccessRate{}, mapRepoError(err)
	}
	rate, err := s.store.GetCharitySuccessRate(ctx, id)
	if err != nil {
		return db.CharityModel{}, db.CharitySuccessRate{}, mapRepoError(err)
	}
	return m, rate, nil
}

// List returns one page of charity models (enabledOnly filters to routable).
func (s *Service) List(ctx context.Context, enabledOnly bool, limit, offset int) ([]db.CharityModel, int, error) {
	models, total, err := s.store.ListCharityModels(ctx, enabledOnly, limit, offset)
	if err != nil {
		return nil, 0, mapRepoError(err)
	}
	return models, total, nil
}

// Delete removes one charity model (bindings/stats cascade).
func (s *Service) Delete(ctx context.Context, id int64) error {
	if err := s.store.DeleteCharityModel(ctx, id); err != nil {
		return mapRepoError(err)
	}
	return nil
}

// ListBindings returns the bindings of one charity model.
func (s *Service) ListBindings(ctx context.Context, modelID int64) ([]db.CharityModelBinding, error) {
	bindings, err := s.store.ListCharityBindings(ctx, modelID)
	if err != nil {
		return nil, mapRepoError(err)
	}
	return bindings, nil
}

// CreateBinding binds one donated key + upstream model to a charity model.
func (s *Service) CreateBinding(ctx context.Context, modelID, donationKeyID int64, upstreamModelID string, ord int64) (db.CharityModelBinding, error) {
	if donationKeyID <= 0 {
		return db.CharityModelBinding{}, ErrInvalidRequest
	}
	if err := validateUpstreamID(upstreamModelID); err != nil {
		return db.CharityModelBinding{}, err
	}
	binding, err := s.store.CreateCharityBinding(ctx, modelID, donationKeyID, upstreamModelID, ord, s.now())
	if err != nil {
		return db.CharityModelBinding{}, mapRepoError(err)
	}
	return binding, nil
}

// UpdateBinding updates ord/upstream_model_id of one binding.
func (s *Service) UpdateBinding(ctx context.Context, modelID, bindingID int64, ord *int64, upstreamModelID *string) (db.CharityModelBinding, error) {
	if upstreamModelID != nil {
		if err := validateUpstreamID(*upstreamModelID); err != nil {
			return db.CharityModelBinding{}, err
		}
	}
	binding, err := s.store.UpdateCharityBinding(ctx, modelID, bindingID, ord, upstreamModelID, s.now())
	if err != nil {
		return db.CharityModelBinding{}, mapRepoError(err)
	}
	return binding, nil
}

// DeleteBinding removes one binding.
func (s *Service) DeleteBinding(ctx context.Context, modelID, bindingID int64) error {
	if err := s.store.DeleteCharityBinding(ctx, modelID, bindingID); err != nil {
		return mapRepoError(err)
	}
	return nil
}

// validateName bounds provider/model text and rejects control characters and
// leading/trailing whitespace ambiguity ('/' is allowed inside both fields).
func validateName(value string, maxRunes int) error {
	if value == "" {
		return fmt.Errorf("%w: name is required", ErrInvalidRequest)
	}
	if len([]rune(value)) > maxRunes {
		return fmt.Errorf("%w: name too long", ErrInvalidRequest)
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("%w: name contains control characters", ErrInvalidRequest)
		}
	}
	return nil
}

// validatePrefixRule rejects a provider that already starts with the charity
// prefix: the prefix is added exactly once by the platform.
func validatePrefixRule(provider string) error {
	if len(provider) >= len(db.CharityPrefix) && provider[:len(db.CharityPrefix)] == db.CharityPrefix {
		return fmt.Errorf("%w: provider must not start with %q", ErrInvalidRequest, db.CharityPrefix)
	}
	return nil
}

func validateNames(provider, model string) error {
	if err := validateName(provider, maxProviderRunes); err != nil {
		return err
	}
	if err := validateName(model, maxModelRunes); err != nil {
		return err
	}
	return validatePrefixRule(provider)
}

func validateUpstreamID(v string) error {
	if v == "" {
		return fmt.Errorf("%w: upstream_model_id is required", ErrInvalidRequest)
	}
	if len([]rune(v)) > maxUpstreamIDRunes {
		return fmt.Errorf("%w: upstream_model_id too long", ErrInvalidRequest)
	}
	for _, r := range v {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("%w: upstream_model_id contains control characters", ErrInvalidRequest)
		}
	}
	return nil
}

// validatePrices enforces non-negative prices before persistence; zeroing of
// the non-current mode's table happens inside the repository.
func validatePrices(m *db.CharityModel) error {
	prices := []int64{
		m.RequestUserPrice, m.RequestDonorReward,
		m.UncachedUserPrice, m.CacheWriteUserPrice, m.CacheReadUserPrice, m.OutputUserPrice,
		m.UncachedDonorReward, m.CacheWriteDonorReward, m.CacheReadDonorReward, m.OutputDonorReward,
	}
	for _, p := range prices {
		if p < 0 {
			return fmt.Errorf("%w: negative price", ErrInvalidRequest)
		}
	}
	return nil
}

func mapRepoError(err error) error {
	switch {
	case errors.Is(err, db.ErrNotFound), errors.Is(err, db.ErrConflict),
		errors.Is(err, db.ErrCharityTokenReserveMissing), errors.Is(err, db.ErrInvalidValue):
		return err
	default:
		return fmt.Errorf("charity: repository error: %w", err)
	}
}

// --- HTTP surface -----------------------------------------------------------

// IdentityResolver extracts the acting manager identity (opaque user id).
type IdentityResolver func(*http.Request) (int64, error)

// Handler is the mountable charity-model management route tree for one path
// prefix ("/admin/api" or "/api/steward").
type Handler struct {
	svc      *Service
	identity IdentityResolver
	prefix   string
	mux      *http.ServeMux
}

// NewHandler builds the route tree under the given prefix.
func NewHandler(prefix string, svc *Service, identity IdentityResolver) *Handler {
	h := &Handler{svc: svc, identity: identity, prefix: prefix, mux: http.NewServeMux()}
	reg := func(method, suffix string, sub func(w http.ResponseWriter, r *http.Request, actor int64)) {
		h.mux.HandleFunc(method+" "+prefix+suffix, func(w http.ResponseWriter, r *http.Request) {
			actor, ok := h.authenticate(w, r)
			if !ok {
				return
			}
			sub(w, r, actor)
		})
	}
	reg("GET", "/charity-models", h.listFor)
	reg("POST", "/charity-models", h.createFor)
	reg("GET", "/charity-models/{id}", h.getFor)
	reg("PATCH", "/charity-models/{id}", h.updateFor)
	reg("DELETE", "/charity-models/{id}", h.deleteFor)
	reg("GET", "/charity-models/{id}/bindings", h.listBindingsFor)
	reg("POST", "/charity-models/{id}/bindings", h.createBindingFor)
	reg("PATCH", "/charity-models/{id}/bindings/{bindingId}", h.updateBindingFor)
	reg("DELETE", "/charity-models/{id}/bindings/{bindingId}", h.deleteBindingFor)
	return h
}

// --- externally mountable sub-handlers --------------------------------------
//
// Frames that resolve their own principals (the steward gate hands sub-
// handlers a ready user id) wire these directly instead of mounting the whole
// tree.

// ListSub serves GET {prefix}/charity-models.
func (h *Handler) ListSub(w http.ResponseWriter, r *http.Request, actor int64) {
	h.listFor(w, r, actor)
}

// CreateSub serves POST {prefix}/charity-models.
func (h *Handler) CreateSub(w http.ResponseWriter, r *http.Request, actor int64) {
	h.createFor(w, r, actor)
}

// GetSub serves GET {prefix}/charity-models/{id}.
func (h *Handler) GetSub(w http.ResponseWriter, r *http.Request, actor int64) { h.getFor(w, r, actor) }

// UpdateSub serves PATCH {prefix}/charity-models/{id}.
func (h *Handler) UpdateSub(w http.ResponseWriter, r *http.Request, actor int64) {
	h.updateFor(w, r, actor)
}

// DeleteSub serves DELETE {prefix}/charity-models/{id}.
func (h *Handler) DeleteSub(w http.ResponseWriter, r *http.Request, actor int64) {
	h.deleteFor(w, r, actor)
}

// ListBindingsSub serves GET {prefix}/charity-models/{id}/bindings.
func (h *Handler) ListBindingsSub(w http.ResponseWriter, r *http.Request, actor int64) {
	h.listBindingsFor(w, r, actor)
}

// CreateBindingSub serves POST {prefix}/charity-models/{id}/bindings.
func (h *Handler) CreateBindingSub(w http.ResponseWriter, r *http.Request, actor int64) {
	h.createBindingFor(w, r, actor)
}

// UpdateBindingSub serves PATCH .../bindings/{bindingId}.
func (h *Handler) UpdateBindingSub(w http.ResponseWriter, r *http.Request, actor int64) {
	h.updateBindingFor(w, r, actor)
}

// DeleteBindingSub serves DELETE .../bindings/{bindingId}.
func (h *Handler) DeleteBindingSub(w http.ResponseWriter, r *http.Request, actor int64) {
	h.deleteBindingFor(w, r, actor)
}

func (h *Handler) authenticate(w http.ResponseWriter, r *http.Request) (int64, bool) {
	if h.identity == nil {
		writeErr(w, httperr.New(httperr.CodeUnauthorized, "authentication required"))
		return 0, false
	}
	id, err := h.identity(r)
	if err != nil || id <= 0 {
		writeErr(w, httperr.New(httperr.CodeForbidden, "manager authorization required"))
		return 0, false
	}
	return id, true
}

func (h *Handler) listFor(w http.ResponseWriter, r *http.Request, _ int64) {
	page, pageSize, ok := parsePageParams(w, r)
	if !ok {
		return
	}
	enabledOnly := false
	q := r.URL.Query()
	if raw := q["enabled"]; len(raw) == 1 {
		switch raw[0] {
		case "true":
			enabledOnly = true
		case "false", "":
		default:
			writeInvalid(w)
			return
		}
	} else if len(raw) > 1 {
		writeInvalid(w)
		return
	}
	models, total, err := h.svc.List(r.Context(), enabledOnly, pageSize, (page-1)*pageSize)
	if err != nil {
		writeErr(w, httperr.New(httperr.CodeInternal, "internal error"))
		return
	}
	resp := charityListResp{Data: make([]charityModelResp, 0, len(models)), Total: total, HasMore: page*pageSize < total}
	for _, m := range models {
		rate, _ := h.svc.store.GetCharitySuccessRate(r.Context(), m.ID)
		resp.Data = append(resp.Data, charityResponse(m, rate))
	}
	httperr.WriteJSON(w, http.StatusOK, resp)
}

func (h *Handler) createFor(w http.ResponseWriter, r *http.Request, actor int64) {
	var req charityWriteReq
	if derr := decodeJSON(w, r, &req); derr.Code != "" {
		writeErr(w, derr)
		return
	}
	m, ok := req.toModel(w)
	if !ok {
		return
	}
	if req.Enabled != nil {
		m.Enabled = *req.Enabled
	}
	out, err := h.svc.Create(r.Context(), m, actor)
	if err != nil {
		writeServiceErr(w, err)
		return
	}
	rate, _ := h.svc.store.GetCharitySuccessRate(r.Context(), out.ID)
	httperr.WriteJSON(w, http.StatusCreated, charityResponse(out, rate))
}

func (h *Handler) getFor(w http.ResponseWriter, r *http.Request, _ int64) {
	id, ok := parsePathID(w, r)
	if !ok {
		return
	}
	m, rate, err := h.svc.Get(r.Context(), id)
	if err != nil {
		writeServiceErr(w, err)
		return
	}
	httperr.WriteJSON(w, http.StatusOK, charityResponse(m, rate))
}

func (h *Handler) updateFor(w http.ResponseWriter, r *http.Request, _ int64) {
	id, ok := parsePathID(w, r)
	if !ok {
		return
	}
	var req charityPatchReq
	if derr := decodeJSON(w, r, &req); derr.Code != "" {
		writeErr(w, derr)
		return
	}
	upd, ok := req.toUpdate(w)
	if !ok {
		return
	}
	out, err := h.svc.Update(r.Context(), id, upd)
	if err != nil {
		writeServiceErr(w, err)
		return
	}
	rate, _ := h.svc.store.GetCharitySuccessRate(r.Context(), out.ID)
	httperr.WriteJSON(w, http.StatusOK, charityResponse(out, rate))
}

func (h *Handler) deleteFor(w http.ResponseWriter, r *http.Request, _ int64) {
	id, ok := parsePathID(w, r)
	if !ok {
		return
	}
	if len(r.URL.Query()) != 0 {
		writeInvalid(w)
		return
	}
	if err := h.svc.Delete(r.Context(), id); err != nil {
		writeServiceErr(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) listBindingsFor(w http.ResponseWriter, r *http.Request, _ int64) {
	id, ok := parsePathID(w, r)
	if !ok {
		return
	}
	bindings, err := h.svc.ListBindings(r.Context(), id)
	if err != nil {
		writeServiceErr(w, err)
		return
	}
	out := make([]charityBindingResp, 0, len(bindings))
	for _, b := range bindings {
		out = append(out, bindingResponse(b))
	}
	httperr.WriteJSON(w, http.StatusOK, map[string]any{"data": out})
}

func (h *Handler) createBindingFor(w http.ResponseWriter, r *http.Request, _ int64) {
	id, ok := parsePathID(w, r)
	if !ok {
		return
	}
	var req struct {
		DonationKeyID   int64  `json:"donation_key_id"`
		UpstreamModelID string `json:"upstream_model_id"`
		Ord             *int64 `json:"ord,omitempty"`
	}
	if derr := decodeJSON(w, r, &req); derr.Code != "" {
		writeErr(w, derr)
		return
	}
	ord := int64(0)
	if req.Ord != nil {
		if *req.Ord < 0 {
			writeInvalid(w)
			return
		}
		ord = *req.Ord
	}
	binding, err := h.svc.CreateBinding(r.Context(), id, req.DonationKeyID, req.UpstreamModelID, ord)
	if err != nil {
		writeServiceErr(w, err)
		return
	}
	httperr.WriteJSON(w, http.StatusCreated, bindingResponse(binding))
}

func (h *Handler) updateBindingFor(w http.ResponseWriter, r *http.Request, _ int64) {
	id, bID, ok := parseBindingPath(w, r)
	if !ok {
		return
	}
	var req struct {
		Ord             *int64  `json:"ord,omitempty"`
		UpstreamModelID *string `json:"upstream_model_id,omitempty"`
	}
	if derr := decodeJSON(w, r, &req); derr.Code != "" {
		writeErr(w, derr)
		return
	}
	if req.Ord == nil && req.UpstreamModelID == nil {
		writeInvalid(w)
		return
	}
	if req.Ord != nil && *req.Ord < 0 {
		writeInvalid(w)
		return
	}
	binding, err := h.svc.UpdateBinding(r.Context(), id, bID, req.Ord, req.UpstreamModelID)
	if err != nil {
		writeServiceErr(w, err)
		return
	}
	httperr.WriteJSON(w, http.StatusOK, bindingResponse(binding))
}

func (h *Handler) deleteBindingFor(w http.ResponseWriter, r *http.Request, _ int64) {
	id, bID, ok := parseBindingPath(w, r)
	if !ok {
		return
	}
	if len(r.URL.Query()) != 0 {
		writeInvalid(w)
		return
	}
	if err := h.svc.DeleteBinding(r.Context(), id, bID); err != nil {
		writeServiceErr(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}

// --- wire shapes ------------------------------------------------------------

type priceFieldsReq struct {
	RequestUserPrice      *string `json:"request_user_price"`
	RequestDonorReward    *string `json:"request_donor_reward"`
	UncachedUserPrice     *string `json:"uncached_user_price"`
	CacheWriteUserPrice   *string `json:"cache_write_user_price"`
	CacheReadUserPrice    *string `json:"cache_read_user_price"`
	OutputUserPrice       *string `json:"output_user_price"`
	UncachedDonorReward   *string `json:"uncached_donor_reward"`
	CacheWriteDonorReward *string `json:"cache_write_donor_reward"`
	CacheReadDonorReward  *string `json:"cache_read_donor_reward"`
	OutputDonorReward     *string `json:"output_donor_reward"`
}

type discountFieldsReq struct {
	Percent    *int            `json:"percent"`
	Enabled    *bool           `json:"enabled"`
	StartAt    json.RawMessage `json:"start_at,omitempty"`
	EndAt      json.RawMessage `json:"end_at,omitempty"`
	ClearStart bool            `json:"clear_start_at,omitempty"`
	ClearEnd   bool            `json:"clear_end_at,omitempty"`
}

type charityWriteReq struct {
	Provider    string            `json:"provider"`
	Model       string            `json:"model"`
	PricingMode string            `json:"pricing_mode"`
	Enabled     *bool             `json:"enabled,omitempty"`
	Prices      priceFieldsReq    `json:"prices"`
	Discount    discountFieldsReq `json:"discount"`
}

type charityPatchReq struct {
	Provider    *string            `json:"provider,omitempty"`
	Model       *string            `json:"model,omitempty"`
	PricingMode *string            `json:"pricing_mode,omitempty"`
	Enabled     *bool              `json:"enabled,omitempty"`
	Prices      *priceFieldsReq    `json:"prices,omitempty"`
	Discount    *discountFieldsReq `json:"discount,omitempty"`
}

// parsePrice decodes one canonical decimal milli-credit string field.
func parsePrice(raw *string) (*int64, bool) {
	if raw == nil {
		return nil, true
	}
	v, err := credits.ParseAmount(*raw)
	if err != nil || v < 0 {
		return nil, false
	}
	return &v, true
}

func (r priceFieldsReq) toUpdate(dst *db.CharityModelPrices, w http.ResponseWriter) bool {
	assign := func(raw *string, target **int64) bool {
		v, good := parsePrice(raw)
		if !good {
			return false
		}
		if raw != nil {
			*target = v
		}
		return true
	}
	if !assign(r.RequestUserPrice, &dst.RequestUserPrice) ||
		!assign(r.RequestDonorReward, &dst.RequestDonorReward) ||
		!assign(r.UncachedUserPrice, &dst.UncachedUserPrice) ||
		!assign(r.CacheWriteUserPrice, &dst.CacheWriteUserPrice) ||
		!assign(r.CacheReadUserPrice, &dst.CacheReadUserPrice) ||
		!assign(r.OutputUserPrice, &dst.OutputUserPrice) ||
		!assign(r.UncachedDonorReward, &dst.UncachedDonorReward) ||
		!assign(r.CacheWriteDonorReward, &dst.CacheWriteDonorReward) ||
		!assign(r.CacheReadDonorReward, &dst.CacheReadDonorReward) ||
		!assign(r.OutputDonorReward, &dst.OutputDonorReward) {
		writeInvalid(w)
		return false
	}
	return true
}

func (r charityWriteReq) toModel(w http.ResponseWriter) (db.CharityModel, bool) {
	m := db.CharityModel{
		Provider:    r.Provider,
		Model:       r.Model,
		PricingMode: r.PricingMode,
	}
	if r.PricingMode != db.CharityPricingPerRequest && r.PricingMode != db.CharityPricingPerToken {
		writeInvalid(w)
		return m, false
	}
	var prices db.CharityModelPrices
	if !r.Prices.toUpdate(&prices, w) {
		return m, false
	}
	if prices.RequestUserPrice != nil {
		m.RequestUserPrice = *prices.RequestUserPrice
	}
	if prices.RequestDonorReward != nil {
		m.RequestDonorReward = *prices.RequestDonorReward
	}
	if prices.UncachedUserPrice != nil {
		m.UncachedUserPrice = *prices.UncachedUserPrice
	}
	if prices.CacheWriteUserPrice != nil {
		m.CacheWriteUserPrice = *prices.CacheWriteUserPrice
	}
	if prices.CacheReadUserPrice != nil {
		m.CacheReadUserPrice = *prices.CacheReadUserPrice
	}
	if prices.OutputUserPrice != nil {
		m.OutputUserPrice = *prices.OutputUserPrice
	}
	if prices.UncachedDonorReward != nil {
		m.UncachedDonorReward = *prices.UncachedDonorReward
	}
	if prices.CacheWriteDonorReward != nil {
		m.CacheWriteDonorReward = *prices.CacheWriteDonorReward
	}
	if prices.CacheReadDonorReward != nil {
		m.CacheReadDonorReward = *prices.CacheReadDonorReward
	}
	if prices.OutputDonorReward != nil {
		m.OutputDonorReward = *prices.OutputDonorReward
	}
	startAt, endAt, ok2 := parseDiscountInterval(r.Discount, w)
	if !ok2 {
		return m, false
	}
	m.DiscountStartAt, m.DiscountEndAt = startAt, endAt
	if r.Discount.Percent != nil {
		m.DiscountPercent = *r.Discount.Percent
	}
	if r.Discount.Enabled != nil {
		m.DiscountEnabled = *r.Discount.Enabled
	}
	return m, true
}

func (r charityPatchReq) toUpdate(w http.ResponseWriter) (db.CharityModelUpdate, bool) {
	var upd db.CharityModelUpdate
	upd.Provider = r.Provider
	upd.Model = r.Model
	upd.PricingMode = r.PricingMode
	upd.Enabled = r.Enabled
	if r.Prices != nil {
		prices := &db.CharityModelPrices{}
		if !r.Prices.toUpdate(prices, w) {
			return upd, false
		}
		upd.Prices = prices
	}
	if r.Discount != nil {
		// Wire-level range validation happens HERE so a client-supplied
		// out-of-range percent is invalid_request (400), while state and
		// interval conflicts decided against stored data remain 409.
		if r.Discount.Percent != nil && (*r.Discount.Percent < 0 || *r.Discount.Percent > 100) {
			writeInvalid(w)
			return upd, false
		}
		upd.DiscountPercent = r.Discount.Percent
		upd.DiscountEnabled = r.Discount.Enabled
		upd.ClearDiscountStart = r.Discount.ClearStart
		upd.ClearDiscountEnd = r.Discount.ClearEnd
		startAt, endAt, ok2 := parseDiscountInterval(*r.Discount, w)
		if !ok2 {
			return upd, false
		}
		upd.DiscountStartAt = startAt
		upd.DiscountEndAt = endAt
	}
	return upd, true
}

// parseDiscountInterval decodes nullable epoch bounds; explicit null clears.
// The Clear* flags of the request shape are honored by the caller mapping them
// onto the update struct directly.
func parseDiscountInterval(d discountFieldsReq, w http.ResponseWriter) (*int64, *int64, bool) {
	decode := func(raw json.RawMessage) (*int64, bool, bool) {
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
	startAt, startPresent, okS := decode(d.StartAt)
	endAt, endPresent, okE := decode(d.EndAt)
	if !okS || !okE {
		writeInvalid(w)
		return nil, nil, false
	}
	var startPtr, endPtr *int64
	if startPresent {
		startPtr = startAt
	}
	if endPresent {
		endPtr = endAt
	}
	return startPtr, endPtr, true
}

type charityModelResp struct {
	ID              int64               `json:"id"`
	Provider        string              `json:"provider"`
	Model           string              `json:"model"`
	FullName        string              `json:"full_name"`
	Enabled         bool                `json:"enabled"`
	PricingMode     string              `json:"pricing_mode"`
	Prices          charityPricesResp   `json:"prices"`
	Discount        charityDiscountResp `json:"discount"`
	SuccessSamples  int                 `json:"success_samples"`
	SuccessCount    int                 `json:"success_count"`
	CreatedByUserID *int64              `json:"created_by_user_id"`
	CreatedAt       int64               `json:"created_at"`
	UpdatedAt       int64               `json:"updated_at"`
}

type charityPricesResp struct {
	RequestUserPriceMilli      string `json:"request_user_price_milli"`
	RequestDonorRewardMilli    string `json:"request_donor_reward_milli"`
	UncachedUserPriceMilli     string `json:"uncached_user_price_milli"`
	CacheWriteUserPriceMilli   string `json:"cache_write_user_price_milli"`
	CacheReadUserPriceMilli    string `json:"cache_read_user_price_milli"`
	OutputUserPriceMilli       string `json:"output_user_price_milli"`
	UncachedDonorRewardMilli   string `json:"uncached_donor_reward_milli"`
	CacheWriteDonorRewardMilli string `json:"cache_write_donor_reward_milli"`
	CacheReadDonorRewardMilli  string `json:"cache_read_donor_reward_milli"`
	OutputDonorRewardMilli     string `json:"output_donor_reward_milli"`
}

type charityDiscountResp struct {
	Percent int    `json:"percent"`
	Enabled bool   `json:"enabled"`
	StartAt *int64 `json:"start_at"`
	EndAt   *int64 `json:"end_at"`
}

type charityBindingResp struct {
	ID                 int64  `json:"id"`
	CharityModelID     int64  `json:"charity_model_id"`
	DonationKeyID      int64  `json:"donation_key_id"`
	UpstreamModelID    string `json:"upstream_model_id"`
	Ord                int64  `json:"ord"`
	CreatedAt          int64  `json:"created_at"`
	EndpointBaseURL    string `json:"endpoint_base_url"`
	KeyDisplayHead     string `json:"key_display_head"`
	KeyDisplayTail     string `json:"key_display_tail"`
	DonationKeyEnabled bool   `json:"donation_key_enabled"`
}

type charityListResp struct {
	Data    []charityModelResp `json:"data"`
	Total   int                `json:"total"`
	HasMore bool               `json:"has_more"`
}

func charityResponse(m db.CharityModel, rate db.CharitySuccessRate) charityModelResp {
	return charityModelResp{
		ID: m.ID, Provider: m.Provider, Model: m.Model, FullName: m.FullName,
		Enabled: m.Enabled, PricingMode: m.PricingMode,
		Prices: charityPricesResp{
			RequestUserPriceMilli:      credits.FormatAmount(m.RequestUserPrice),
			RequestDonorRewardMilli:    credits.FormatAmount(m.RequestDonorReward),
			UncachedUserPriceMilli:     credits.FormatAmount(m.UncachedUserPrice),
			CacheWriteUserPriceMilli:   credits.FormatAmount(m.CacheWriteUserPrice),
			CacheReadUserPriceMilli:    credits.FormatAmount(m.CacheReadUserPrice),
			OutputUserPriceMilli:       credits.FormatAmount(m.OutputUserPrice),
			UncachedDonorRewardMilli:   credits.FormatAmount(m.UncachedDonorReward),
			CacheWriteDonorRewardMilli: credits.FormatAmount(m.CacheWriteDonorReward),
			CacheReadDonorRewardMilli:  credits.FormatAmount(m.CacheReadDonorReward),
			OutputDonorRewardMilli:     credits.FormatAmount(m.OutputDonorReward),
		},
		Discount: charityDiscountResp{
			Percent: m.DiscountPercent, Enabled: m.DiscountEnabled,
			StartAt: m.DiscountStartAt, EndAt: m.DiscountEndAt,
		},
		SuccessSamples:  rate.SampleCount,
		SuccessCount:    rate.SuccessCount,
		CreatedByUserID: m.CreatedByUserID,
		CreatedAt:       m.CreatedAt, UpdatedAt: m.UpdatedAt,
	}
}

func bindingResponse(b db.CharityModelBinding) charityBindingResp {
	return charityBindingResp{
		ID: b.ID, CharityModelID: b.CharityModelID, DonationKeyID: b.DonationKeyID,
		UpstreamModelID: b.UpstreamModelID, Ord: b.Ord, CreatedAt: b.CreatedAt,
		EndpointBaseURL: b.EndpointBaseURL, KeyDisplayHead: b.KeyDisplayHead,
		KeyDisplayTail: b.KeyDisplayTail, DonationKeyEnabled: b.DonationKeyEnabled,
	}
}

// --- helpers ----------------------------------------------------------------

func parsePathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	raw := r.PathValue("id")
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		writeErr(w, httperr.New(httperr.CodeNotFound, "not found"))
		return 0, false
	}
	return id, true
}

func parseBindingPath(w http.ResponseWriter, r *http.Request) (int64, int64, bool) {
	id, ok := parsePathID(w, r)
	if !ok {
		return 0, 0, false
	}
	raw := r.PathValue("bindingId")
	bID, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || bID <= 0 {
		writeErr(w, httperr.New(httperr.CodeNotFound, "not found"))
		return 0, 0, false
	}
	return id, bID, true
}

// parsePageParams validates strict single-valued page/page_size queries.
func parsePageParams(w http.ResponseWriter, r *http.Request) (int, int, bool) {
	q := r.URL.Query()
	page, pageSize := 1, 20
	if raw := q["page"]; len(raw) > 1 {
		writeInvalid(w)
		return 0, 0, false
	} else if len(raw) == 1 {
		n, err := strconv.Atoi(raw[0])
		if err != nil || n < 1 || raw[0] != strconv.Itoa(n) {
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
		case "page", "page_size", "enabled":
		default:
			writeInvalid(w)
			return 0, 0, false
		}
	}
	return page, pageSize, true
}

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

func writeServiceErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, db.ErrCharityTokenReserveMissing):
		writeErr(w, httperr.New(httperr.CodeConflict,
			"token 计价模型启用前必须配置 charity_token_reserve_milli"))
	case errors.Is(err, db.ErrNotFound):
		writeErr(w, httperr.New(httperr.CodeNotFound, "not found"))
	case errors.Is(err, db.ErrInvalidValue):
		writeInvalid(w)
	case errors.Is(err, db.ErrConflict):
		// A state/candidate/uniqueness refusal decided by the repository
		// (duplicate routing key, disabled precondition) is a conflict, not a
		// malformed request.
		writeErr(w, httperr.New(httperr.CodeConflict, "资源状态冲突"))
	case errors.Is(err, ErrInvalidRequest):
		writeInvalid(w)
	default:
		writeErr(w, httperr.New(httperr.CodeInternal, "internal error"))
	}
}

func writeErr(w http.ResponseWriter, e httperr.Error) {
	httperr.WriteError(w, e)
}

// Handler returns the raw route tree for external mounting (the caller wraps
// it in httpmw.API and its own authentication middleware).
func (h *Handler) Handler() http.Handler { return h.mux }
