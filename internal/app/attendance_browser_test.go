package app

import (
	"encoding/json"
	"io"
	"net/http"
	"time"
)

// These controls exist only in the opt-in browser test's private database.
func v136BrowserControl(w http.ResponseWriter, r *http.Request, f *imageBrowserFixture, management map[string]any) bool {
	if r.Method != http.MethodPost {
		return false
	}
	switch r.URL.Path {
	case "/v136/advance":
		var input struct {
			Seconds int64 `json:"seconds"`
		}
		if json.NewDecoder(io.LimitReader(r.Body, 1024)).Decode(&input) != nil || input.Seconds < 1 || input.Seconds > 31*86400 {
			http.Error(w, "invalid clock step", http.StatusBadRequest)
			return true
		}
		f.offset.Add(input.Seconds)
		writeImageFixtureJSON(w, map[string]bool{"ok": true})
		return true
	case "/v136/health":
		if management == nil {
			http.Error(w, "missing management fixture", http.StatusBadRequest)
			return true
		}
		key := management["resource_health"].(map[string]any)["key_id"]
		until := time.Now().Unix() + 3600
		_, err := f.store.DB().ExecContext(r.Context(), `UPDATE donation_service_health SET failure_streak=nbi_u128(10),failure_disabled=1,
retry_after_until=?,backoff_retry_at=?,backoff_period='five_hour',backoff_source='builtin',backoff_episode=1,trial_claim_id=NULL,backoff_stage=1
WHERE use_id IN (SELECT id FROM donation_uses WHERE donation_key_id=? AND native_service_bit=1)`, until, until, key)
		if err != nil {
			http.Error(w, "health fixture update failed", http.StatusInternalServerError)
			return true
		}
		writeImageFixtureJSON(w, map[string]any{"until": until})
		return true
	default:
		return false
	}
}
