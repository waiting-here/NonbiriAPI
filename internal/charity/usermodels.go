package charity

import (
	"net/http"
	"strconv"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
)

// MaxUserModelsLimit bounds the user-station price table; the whole enabled
// set is far smaller, so the ceiling is a fail-closed guard, not a page size.
const MaxUserModelsLimit = 256

// UserModelsDeps wires the user-station price-table handler.
type UserModelsDeps struct {
	Store *db.Store
}

// NewUserModelsHandler serves GET /api/charity/models (frozen §6.3): the
// enabled charity models with original/current discounted user prices,
// independent donor-reward prices, discount windows, last-100 protocol
// success counts, and server-resolved availability. A disabled site returns
// an empty list rather than an error so the shared response shape stays
// stable. No donated-key id or base URL exists on this projection by
// construction.
func NewUserModelsHandler(deps UserModelsDeps) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeErr := func(status int, e httperr.Error) {
			httperr.WriteJSON(w, status, httperr.Envelope{Error: e})
		}
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			writeErr(http.StatusMethodNotAllowed, httperr.New(httperr.CodeInvalidRequest, "method not allowed"))
			return
		}
		if r.URL.Path != "/api/charity/models" {
			writeErr(http.StatusNotFound, httperr.New(httperr.CodeNotFound, "not found"))
			return
		}
		models, err := deps.Store.ListUserCharityModels(r.Context(), time.Now().Unix(), MaxUserModelsLimit)
		if err != nil {
			writeErr(http.StatusInternalServerError, httperr.New(httperr.CodeInternal, "internal error"))
			return
		}
		type entry struct {
			ID                 string         `json:"id"`
			Provider           string         `json:"provider"`
			Model              string         `json:"model"`
			FullName           string         `json:"full_name"`
			Enabled            bool           `json:"enabled"`
			FlattenToolCalls   bool           `json:"flatten_tool_calls"`
			PricingMode        string         `json:"pricing_mode"`
			Prices             map[string]any `json:"prices"`
			Discount           map[string]any `json:"discount"`
			SuccessSamples     int            `json:"success_samples"`
			SuccessCount       int            `json:"success_count"`
			Available          bool           `json:"available"`
			AvailabilityReason string         `json:"availability_reason"`
		}
		data := make([]entry, 0, len(models))
		for _, m := range models {
			prices := map[string]any{
				"request_user_price_milli":       strconv.FormatInt(m.RequestUserPrice, 10),
				"request_donor_reward_milli":     strconv.FormatInt(m.RequestDonorReward, 10),
				"uncached_user_price_milli":      strconv.FormatInt(m.UncachedUserPrice, 10),
				"cache_write_user_price_milli":   strconv.FormatInt(m.CacheWriteUserPrice, 10),
				"cache_read_user_price_milli":    strconv.FormatInt(m.CacheReadUserPrice, 10),
				"output_user_price_milli":        strconv.FormatInt(m.OutputUserPrice, 10),
				"uncached_donor_reward_milli":    strconv.FormatInt(m.UncachedDonorReward, 10),
				"cache_write_donor_reward_milli": strconv.FormatInt(m.CacheWriteDonorReward, 10),
				"cache_read_donor_reward_milli":  strconv.FormatInt(m.CacheReadDonorReward, 10),
				"output_donor_reward_milli":      strconv.FormatInt(m.OutputDonorReward, 10),
				// Effective display prices under the discount valid at read
				// time; billing recomputes server-side at reservation time.
				"current_request_user_price_milli":     strconv.FormatInt(m.CurrentRequestUserPrice, 10),
				"current_uncached_user_price_milli":    strconv.FormatInt(m.CurrentUncachedUserPrice, 10),
				"current_cache_write_user_price_milli": strconv.FormatInt(m.CurrentCacheWriteUserPrice, 10),
				"current_cache_read_user_price_milli":  strconv.FormatInt(m.CurrentCacheReadUserPrice, 10),
				"current_output_user_price_milli":      strconv.FormatInt(m.CurrentOutputUserPrice, 10),
			}
			discount := map[string]any{
				"percent": m.DiscountPercent,
				"enabled": m.DiscountEnabled,
			}
			if m.DiscountStartAt != nil {
				discount["start_at"] = *m.DiscountStartAt
			}
			if m.DiscountEndAt != nil {
				discount["end_at"] = *m.DiscountEndAt
			}
			reason := "ok"
			if !m.Available {
				reason = "no_candidate"
			}
			data = append(data, entry{
				ID:                 strconv.FormatInt(m.ID, 10),
				Provider:           m.Provider,
				Model:              m.Model,
				FullName:           m.FullName,
				Enabled:            true,
				FlattenToolCalls:   m.FlattenToolCalls,
				PricingMode:        m.PricingMode,
				Prices:             prices,
				Discount:           discount,
				SuccessSamples:     m.SuccessSamples,
				SuccessCount:       m.SuccessCount,
				Available:          m.Available,
				AvailabilityReason: reason,
			})
		}
		w.Header().Set("Cache-Control", "no-store")
		httperr.WriteJSON(w, http.StatusOK, map[string]any{"data": data})
	})
}
