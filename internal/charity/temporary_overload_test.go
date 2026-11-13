package charity

import (
	"database/sql"
	"errors"
	"strconv"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/claim"
	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/upstreamerror"
)

func googleOverloadOutcome() claim.AttemptOutcome {
	c := upstreamerror.Context{CharityCall: true, GoogleAPI: true, ContainsSecret: func([]byte) bool { return false }}
	d := c.Parse([]byte(`{"error":{"code":503,"status":"UNAVAILABLE","message":"This model is currently experiencing high demand. Spikes in demand are usually temporary. Please try again later."}}`))
	return claim.AttemptOutcome{Kind: claim.ResultResponse, UpstreamStatus: 503, UpstreamCode: d.Code(), TemporaryBackoff: d.TemporaryBackoff(), FailureOrigin: contract.OriginUpstreamResponse, StreakDisposition: contract.StreakUpstreamFailure}
}

func TestGoogleOverloadCooldownPreservesStreakAndRecovers(t *testing.T) {
	e := newBackoffEnv(t)
	h, _ := e.dispatch(t)
	e.seedServiceStreak(t, h, 2)
	now := e.clock.Load()
	pending, err := e.rail.Claim(t.Context(), e.retryClaimInput(t, e.request(t, e.tokenModel).ID))
	if err != nil {
		t.Fatal(err)
	}
	outcome := googleOverloadOutcome()
	e.complete(t, h, outcome)
	e.clock.Add(10)
	e.complete(t, h, outcome)
	var streak []byte
	var until int64
	var disabled bool
	if err := e.store.DB().QueryRow("SELECT failure_streak,failure_disabled,retry_after_until FROM donation_service_health WHERE id=?", e.serviceHealthID(t, h)).Scan(&streak, &disabled, &until); err != nil {
		t.Fatal(err)
	}
	assertU128(t, streak, 2, "overload preserves previous failures")
	if disabled || until != now+60 {
		t.Fatal("wrong cooldown or disabled state", disabled, until)
	}
	if _, err := e.rail.TakeForDispatch(t.Context(), pending); !errors.Is(err, claim.ErrKeyRateLimited) {
		t.Fatal("pending dispatch ignored cooldown", err)
	}
	if _, err := e.rail.Claim(t.Context(), e.retryClaimInput(t, e.request(t, e.tokenModel).ID)); !errors.Is(err, claim.ErrKeyRateLimited) {
		t.Fatal("new dispatch ignored cooldown", err)
	}
	var disposition, origin string
	var charge, starts int
	if err := e.store.DB().QueryRow("SELECT streak_disposition,failure_origin,price_actual_milli FROM donation_usage_reservations WHERE claim_id=?", h.ClaimID()).Scan(&disposition, &origin, &charge); err != nil {
		t.Fatal(err)
	}
	if err := e.store.DB().QueryRow("SELECT count(*) FROM dispatch_response_starts WHERE claim_id=?", h.ClaimID()).Scan(&starts); err != nil {
		t.Fatal(err)
	}
	if disposition != "neutral" || origin != "upstream_response" || charge != 0 || starts != 0 {
		t.Fatal("error origin/billing changed", disposition, origin, charge, starts)
	}
	e.clock.Store(now + 60)
	grant, err := e.rail.TakeForDispatch(t.Context(), pending)
	if err != nil {
		t.Fatal("cooldown did not expire", err)
	}
	grant.Clear()
	e.complete(t, pending, claim.AttemptOutcome{Kind: claim.ResultResponse, UpstreamStatus: 200, ProtocolSuccess: true, StreakDisposition: contract.StreakSuccess, FailureOrigin: contract.OriginNone})
	if err := e.store.DB().QueryRow("SELECT failure_streak FROM donation_service_health WHERE id=?", e.serviceHealthID(t, pending)).Scan(&streak); err != nil {
		t.Fatal(err)
	}
	assertU128(t, streak, 0, "later upstream success resets streak")
}

func TestGoogleOverloadPrefersEveryValidHeaderHint(t *testing.T) {
	for _, delay := range []int64{-1, 0, 20, 120} {
		t.Run(strconv.FormatInt(delay, 10), func(t *testing.T) {
			e := newBackoffEnv(t)
			h, _ := e.dispatch(t)
			e.seedServiceStreak(t, h, 2)
			now := e.clock.Load()
			if err := e.rail.ObserveResponseHeaders(t.Context(), h, contract.ResponseObservation{HasRetryAfter: true, Status: 503, ObservedAt: now, RateLimitedUntil: now + delay}); err != nil {
				t.Fatal(err)
			}
			e.complete(t, h, googleOverloadOutcome())
			var until sql.NullInt64
			var streak []byte
			if err := e.store.DB().QueryRow("SELECT retry_after_until,failure_streak FROM donation_service_health WHERE id=?", e.serviceHealthID(t, h)).Scan(&until, &streak); err != nil {
				t.Fatal(err)
			}
			if until.Valid != (delay > 0) || until.Valid && until.Int64 != now+delay {
				t.Fatal("body fallback replaced header", until, delay)
			}
			assertU128(t, streak, 2, "header priority keeps failure neutral")
		})
	}
}

func TestOrdinary503StillIncrementsFailures(t *testing.T) {
	e := newBackoffEnv(t)
	h, _ := e.dispatch(t)
	e.seedServiceStreak(t, h, 2)
	outcome := googleOverloadOutcome()
	outcome.TemporaryBackoff = upstreamerror.TemporaryBackoff{}
	e.complete(t, h, outcome)
	var streak []byte
	var until sql.NullInt64
	if err := e.store.DB().QueryRow("SELECT failure_streak,retry_after_until FROM donation_service_health WHERE id=?", e.serviceHealthID(t, h)).Scan(&streak, &until); err != nil {
		t.Fatal(err)
	}
	assertU128(t, streak, 3, "ordinary 503 remains a failure")
	if until.Valid {
		t.Fatal("ordinary 503 gained cooldown")
	}
}
