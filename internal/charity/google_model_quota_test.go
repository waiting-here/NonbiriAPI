package charity

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/claim"
	"github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/upstreamerror"
)

const syntheticGoogleURL = "https://generativelanguage.googleapis.com/v1beta/openai"

func (e *backoffEnv) googleInput(t *testing.T, model int64, upstream string) claim.ClaimInput {
	t.Helper()
	request := e.request(t, model)
	tx := beginTestTx(t, e.store.DB())
	a := e.authorizationFor(t, tx, request.ID, e.donationKey, upstream)
	_ = tx.Rollback()
	return claim.ClaimInput{RequestID: request.ID, ActorUserID: e.callerID, AttemptSeq: 1, Purpose: claim.PurposeCharity, DonationKeyID: e.donationKey,
		Candidate: claim.Candidate{Authorization: a, EndpointID: e.endpointID, EndpointKeyID: e.endpointKey, ConnectorType: contract.TypeOpenAICompatible, CanonicalBaseURL: syntheticGoogleURL, UpstreamModelID: upstream}}
}

func (e *backoffEnv) googleDispatch(t *testing.T, model int64, upstream string) claim.Handle {
	t.Helper()
	h, err := e.rail.Claim(t.Context(), e.googleInput(t, model, upstream))
	if err != nil {
		t.Fatal(err)
	}
	g, err := e.rail.TakeForDispatch(t.Context(), h)
	if err != nil {
		t.Fatal(err)
	}
	g.Clear()
	return h
}

func googleQuotaOutcome(t *testing.T, model string, minute bool) claim.AttemptOutcome {
	t.Helper()
	id, metric, delay := "GenerateRequestsPerDayPerProjectPerModel-FreeTier", "requests", "12075s"
	if minute {
		id, metric, delay = "GenerateContentInputTokensPerModelPerMinute-FreeTier", "input_token_count", "19s"
	}
	body := fmt.Sprintf(`{"error":{"code":429,"status":"RESOURCE_EXHAUSTED","details":[{"@type":"type.googleapis.com/google.rpc.QuotaFailure","violations":[{"quotaId":%q,"quotaMetric":"generativelanguage.googleapis.com/generate_content_free_tier_%s","quotaValue":"20","quotaDimensions":{"model":%q}}]},{"@type":"type.googleapis.com/google.rpc.RetryInfo","retryDelay":%q}]}}`, id, metric, model, delay)
	return googleBodyOutcome(t, model, body)
}

func googleBodyOutcome(t *testing.T, model, body string) claim.AttemptOutcome {
	t.Helper()
	c := upstreamerror.Context{CharityCall: true, BaseURL: syntheticGoogleURL, PrivateModel: model, ContainsSecret: func([]byte) bool { return false }}
	d := c.ReadResponse(t.Context(), &http.Response{StatusCode: 429, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(body))})
	return claim.AttemptOutcome{Kind: claim.ResultResponse, UpstreamStatus: 429, UpstreamCode: d.Code(), TemporaryBackoff: d.TemporaryBackoff(), StreakDisposition: contract.StreakUpstreamFailure, FailureOrigin: contract.OriginUpstreamResponse}
}

func TestGoogleModelQuotaAdmissionAndAliases(t *testing.T) {
	e := newBackoffEnvAt(t, syntheticGoogleURL)
	otherModel := e.seedModel(t, "other-public", "per_token")
	if _, err := e.store.DB().Exec(`UPDATE charity_binding_targets SET upstream_model_id='second-model' WHERE binding_id IN (SELECT id FROM charity_model_bindings WHERE charity_model_id=?)`, otherModel); err != nil {
		t.Fatal(err)
	}
	h := e.googleDispatch(t, e.tokenModel, "upstream-model")
	e.seedServiceStreak(t, h, 2)
	late := e.googleDispatch(t, e.tokenModel, "upstream-model")
	pending, err := e.rail.Claim(t.Context(), e.googleInput(t, e.requestModel, "upstream-model"))
	if err != nil {
		t.Fatal(err)
	}
	now := e.clock.Load()
	e.complete(t, h, googleQuotaOutcome(t, "upstream-model", false))
	e.clock.Add(1)
	e.complete(t, late, googleQuotaOutcome(t, "upstream-model", true))
	e.complete(t, h, googleQuotaOutcome(t, "upstream-model", false))
	var reason, disposition string
	var until int64
	var streak []byte
	if err := e.store.DB().QueryRow(`SELECT mc.retry_at,mc.reason,h.failure_streak,r.streak_disposition FROM donation_service_model_cooldowns mc JOIN donation_service_health h ON h.id=mc.health_id JOIN donation_usage_reservations r ON r.claim_id=? WHERE h.id=?`, h.ClaimID(), e.serviceHealthID(t, h)).Scan(&until, &reason, &streak, &disposition); err != nil {
		t.Fatal(err)
	}
	if reason != "google_daily_requests" || until != now+12075 || disposition != "neutral" {
		t.Fatal(until, reason, disposition)
	}
	assertU128(t, streak, 2, "overlapping quota failures")
	assertModelLimit := func(err error) {
		t.Helper()
		var limit *claim.ModelQuotaLimit
		if !errors.As(err, &limit) || limit.Reason != reason || limit.BindingID == 0 || limit.NativeService != contract.NativeServiceChatCompletions || !errors.Is(err, claim.ErrKeyRateLimited) {
			t.Fatal(err)
		}
	}
	_, err = e.rail.TakeForDispatch(t.Context(), pending)
	assertModelLimit(err)
	_, err = e.rail.Claim(t.Context(), e.googleInput(t, e.tokenModel, "upstream-model"))
	assertModelLimit(err)
	other := e.googleDispatch(t, otherModel, "second-model")
	e.complete(t, other, googleQuotaOutcome(t, "second-model", true))
	if err := e.store.DB().QueryRow(`SELECT retry_at,reason FROM donation_service_model_cooldowns WHERE upstream_model_id='second-model'`).Scan(&until, &reason); err != nil || until != now+20 || reason != "google_minute_input_tokens" {
		t.Fatal(until, reason, err)
	}
	e.clock.Store(now + 12075)
	g, err := e.rail.TakeForDispatch(t.Context(), pending)
	if err != nil {
		t.Fatal("expiry", err)
	}
	g.Clear()
	// Removing one alias preserves other current bindings. Removing the
	// final target discards the state and fences this in-flight attempt.
	for _, item := range []struct {
		id   int64
		want int
	}{{e.tokenModel, 1}, {e.requestModel, 0}} {
		if _, err := e.store.DB().Exec(`DELETE FROM charity_model_bindings WHERE charity_model_id=?`, item.id); err != nil {
			t.Fatal(err)
		}
		var count int
		if err := e.store.DB().QueryRow(`SELECT count(*) FROM donation_service_model_cooldowns WHERE upstream_model_id='upstream-model'`).Scan(&count); err != nil || count != item.want {
			t.Fatal(count, err)
		}
	}
	e.complete(t, pending, googleQuotaOutcome(t, "upstream-model", false))
	var count int
	if err := e.store.DB().QueryRow(`SELECT count(*) FROM donation_service_model_cooldowns WHERE upstream_model_id='upstream-model'`).Scan(&count); err != nil || count != 0 {
		t.Fatal("late receipt recreated deleted target", count, err)
	}
}

func TestGoogleModelHeaderScopeAndRecovery(t *testing.T) {
	e := newBackoffEnvAt(t, syntheticGoogleURL)
	now := e.clock.Load()
	first := e.googleDispatch(t, e.tokenModel, "upstream-model")
	second := e.googleDispatch(t, e.tokenModel, "upstream-model")
	results := make(chan error, 2)
	for i, h := range []claim.Handle{first, second} {
		go func() {
			results <- e.rail.ObserveResponseHeaders(t.Context(), h, contract.ResponseObservation{HasRetryAfter: true, Status: 429, ObservedAt: now, RateLimitedUntil: now + int64(10+i*20)})
		}()
	}
	for range 2 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	check := func(wantModel, wantPending, wantShared int64) {
		t.Helper()
		var model, pending, shared int64
		if err := e.store.DB().QueryRow(`SELECT COALESCE((SELECT MAX(retry_at) FROM donation_service_model_cooldowns),0),COALESCE((SELECT MAX(rate_limited_until) FROM dispatch_retry_after_observations WHERE pending_health_id IS NOT NULL),0),COALESCE((SELECT MAX(retry_after_until) FROM donation_service_health),0)`).Scan(&model, &pending, &shared); err != nil || model != wantModel || pending != wantPending || shared != wantShared {
			t.Fatal(model, pending, shared, err)
		}
	}
	check(0, now+30, 0)
	noDelay := googleBodyOutcome(t, "upstream-model", `{"error":{"code":429,"status":"RESOURCE_EXHAUSTED","details":[{"@type":"type.googleapis.com/google.rpc.QuotaFailure","violations":[{"quotaId":"GenerateRequestsPerDayPerProjectPerModel-FreeTier","quotaMetric":"generativelanguage.googleapis.com/generate_content_free_tier_requests","quotaValue":"20"}]}]}}`)
	e.complete(t, first, noDelay)
	check(now+10, now+30, 0)
	if _, err := e.rail.RecoverNonterminal(t.Context(), 100); err != nil {
		t.Fatal(err)
	}
	check(now+10, 0, now+30)
	e.clock.Store(now + 30)
	h := e.googleDispatch(t, e.tokenModel, "upstream-model")
	if err := e.rail.ObserveResponseHeaders(t.Context(), h, contract.ResponseObservation{HasRetryAfter: true, Status: 429, ObservedAt: now + 30, RateLimitedUntil: now + 30}); err != nil {
		t.Fatal(err)
	}
	e.complete(t, h, googleQuotaOutcome(t, "upstream-model", false))
	check(now+10, 0, now+30)
}

func TestGoogleModelQuotaLateReceiptsAfterChanges(t *testing.T) {
	for _, change := range []string{"release", "rebind", "credential"} {
		t.Run(change, func(t *testing.T) {
			e := newBackoffEnvAt(t, syntheticGoogleURL)
			h := e.googleDispatch(t, e.tokenModel, "upstream-model")
			a := h.Authorization()
			switch change {
			case "release":
				if _, err := e.store.DB().Exec("UPDATE donation_service_health SET backoff_generation=backoff_generation+1 WHERE id=?", e.serviceHealthID(t, h)); err != nil {
					t.Fatal(err)
				}
			case "rebind":
				if _, err := e.store.DB().Exec("UPDATE charity_binding_targets SET revision=revision+1 WHERE id=?", a.TargetID); err != nil {
					t.Fatal(err)
				}
			case "credential":
				if _, err := e.store.DB().Exec("UPDATE credential_versions SET enabled=0 WHERE id=?", a.CredentialVersionID); err != nil {
					t.Fatal(err)
				}
			}
			e.complete(t, h, googleQuotaOutcome(t, "upstream-model", false))
			var count int
			if err := e.store.DB().QueryRow("SELECT count(*) FROM donation_service_model_cooldowns").Scan(&count); err != nil || count != 0 {
				t.Fatal("late quota updated changed target", count, err)
			}
		})
	}
}
