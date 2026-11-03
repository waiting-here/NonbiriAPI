package charity

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/claim"
	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/donation"
)

type rateReleaseRegistrar struct{ *http.ServeMux }

func (r rateReleaseRegistrar) RegisterAdminRoute(method, pattern string, handler http.Handler) error {
	r.Handle(method+" "+pattern, handler)
	return nil
}

func TestServiceRateLimitReleaseFencesLateHeadersAndBackoff(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		for _, headers := range []bool{false, true} {
			t.Run(fmt.Sprintf("legacy=%t/headers=%t", legacy, headers), func(t *testing.T) {
				e := newBackoffEnv(t)
				// An expired legacy episode grants a real probe with legacy receipt scope.
				if legacy {
					if _, err := e.store.DB().Exec("INSERT INTO donation_key_backoffs(donation_key_id,generation,episode,period,source,stage,retry_at,updated_at) VALUES(?,3,1,'day','builtin',1,?,?)", e.donationKey, e.clock.Load()-1, e.clock.Load()); err != nil {
						t.Fatal(err)
					}
				}
				old, _, authorization := connectionDispatch(t, e, claim.RouteCharityChat)
				if _, err := e.store.DB().Exec(`INSERT INTO users(discord_id,username,is_admin,donation_credit_mag,total_requests,total_uncached_input_tokens,total_cache_write_input_tokens,total_cache_read_input_tokens,total_output_tokens,total_unknown_usage_requests,revision,created_at,updated_at) VALUES(NULL,'release manager',1,zeroblob(16),zeroblob(16),zeroblob(16),zeroblob(16),zeroblob(16),zeroblob(16),zeroblob(16),zeroblob(16),?,?)`, charityTestNow, charityTestNow); err != nil {
					t.Fatal(err)
				}
				if _, err := e.store.DB().Exec("INSERT INTO donation_handling(donation_id,state,revision,created_at,updated_at) VALUES(?,'pending',1,?,?)", e.donationID, charityTestNow, charityTestNow); err != nil {
					t.Fatal(err)
				}
				service, err := donation.New(donation.Config{Store: e.store, RoleAuth: forceReviewAuthority{}, Now: func() time.Time { return time.Unix(e.clock.Load(), 0) }})
				if err != nil {
					t.Fatal(err)
				}
				mux := http.NewServeMux()
				if err := donation.RegisterAdminRoutes(rateReleaseRegistrar{mux}, service); err != nil {
					t.Fatal(err)
				}
				var revision int64
				if err := e.store.DB().QueryRow("SELECT revision FROM donation_uses WHERE id=?", authorization.UseID).Scan(&revision); err != nil {
					t.Fatal(err)
				}
				path := fmt.Sprintf("/admin/api/donations/%d/uses/%d/rate-limit-release", e.donationID, authorization.UseID)
				request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(fmt.Sprintf(`{"expected_revision":"%d"}`, revision)))
				request.Header.Set("Idempotency-Key", strings.Repeat("U", 22))
				response := httptest.NewRecorder()
				mux.ServeHTTP(response, request)
				if response.Code != 200 {
					t.Fatalf("release: %d %s", response.Code, response.Body)
				}
				if headers {
					err := e.rail.ObserveResponseHeaders(t.Context(), old, contract.ResponseObservation{HasRetryAfter: true, Status: 429, ObservedAt: e.clock.Load(), RateLimitedUntil: e.clock.Load() + 600})
					if err != nil {
						t.Fatal(err)
					}
				}
				e.complete(t, old, quotaOutcome("day"))
				var waits int
				query := `SELECT (SELECT count(*) FROM donation_service_health WHERE retry_after_until IS NOT NULL OR backoff_retry_at IS NOT NULL OR trial_claim_id IS NOT NULL)+(SELECT count(*) FROM donation_key_cooldowns)+(SELECT count(*) FROM donation_key_backoffs WHERE retry_at IS NOT NULL OR trial_claim_id IS NOT NULL)`
				if err := e.store.DB().QueryRow(query).Scan(&waits); err != nil || waits != 0 {
					t.Fatal("late result restored wait", waits, err)
				}
				fresh, _, _ := connectionDispatch(t, e, claim.RouteCharityChat)
				e.complete(t, fresh, quotaOutcome("day"))
				if e.status(t) == nil {
					t.Fatal("fresh result failed to establish new wait")
				}
			})
		}
	}
}
