package charity

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/claim"
	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
	"github.com/waiting-here/NonbiriAPI/internal/smartbackoff"
)

type backoffEnv struct {
	*charityTestEnv
	rail  *claim.Service
	clock atomic.Int64
}

func newBackoffEnv(t *testing.T) *backoffEnv {
	t.Helper()
	e := &backoffEnv{charityTestEnv: newCharityTestEnv(t)}
	e.clock.Store(charityTestNow + 30)
	e.billingRail(t)
	vault, err := secret.New(bytes.Repeat([]byte{charityTestMasterKeyByte}, secret.MasterKeyBytes))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = vault.Close() })
	e.rail, err = claim.New(claim.Dependencies{DB: e.store.DB(), Secrets: vault, Charity: e.service, Accounting: claim.NewLedgerAccounting(), Acceptance: billingAcceptance{}, Now: func() time.Time { return time.Unix(e.clock.Load(), 0) }})
	if err != nil {
		t.Fatal(err)
	}
	return e
}
func (e *backoffEnv) request(t *testing.T, model int64) claim.Request {
	t.Helper()
	now := e.clock.Load()
	reserve := int64(5)
	if model == e.requestModel {
		reserve = 3000
		if now < charityTestNow+1000 {
			reserve = 2400
		}
	}
	value, err := e.rail.Accept(t.Context(), claim.AcceptInput{UserID: e.callerID, Route: claim.RouteCharityChat, ModelSnapshot: "[公益]provider/model", AttemptLimit: 1, ReservedMilli: reserve, CharityModelID: model, CharityDecisionNow: &now})
	if err != nil {
		t.Fatal(err)
	}
	return value
}
func (e *backoffEnv) dispatch(t *testing.T) (claim.Handle, *claim.Dispatch) {
	t.Helper()
	r := e.request(t, e.tokenModel)
	h, err := e.rail.Claim(t.Context(), e.retryClaimInput(t, r.ID))
	if err != nil {
		t.Fatal(err)
	}
	grant, err := e.rail.TakeForDispatch(t.Context(), h)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(grant.Clear)
	return h, grant
}
func quotaOutcome(period string) claim.AttemptOutcome {
	return claim.AttemptOutcome{Kind: claim.ResultResponse, UpstreamStatus: 429, StreakDisposition: contract.StreakUpstreamFailure, FailureOrigin: contract.OriginUpstreamResponse, Backoff: smartbackoff.Classification{Period: period, Source: "builtin"}}
}
func (e *backoffEnv) complete(t *testing.T, h claim.Handle, outcome claim.AttemptOutcome) {
	t.Helper()
	if _, err := e.rail.CompleteAttempt(t.Context(), h, outcome); err != nil {
		t.Fatal(err)
	}
}
func (e *backoffEnv) status(t *testing.T) *smartbackoff.Status {
	t.Helper()
	return e.serviceStatus(t, e.donationKey)
}
func (e *backoffEnv) serviceStatus(t *testing.T, memberID int64) *smartbackoff.Status {
	t.Helper()
	var period, source sql.NullString
	var retry sql.NullInt64
	var probing bool
	err := e.store.DB().QueryRow(`SELECT h.backoff_period,h.backoff_source,h.backoff_retry_at,h.trial_claim_id IS NOT NULL FROM donation_service_health h JOIN donation_uses u ON u.id=h.use_id JOIN donation_use_revisions r ON r.id=u.current_approved_revision_id JOIN endpoint_key_grant_revisions g ON g.id=r.grant_revision_id WHERE u.donation_key_id=? AND h.credential_version_id=g.credential_version_id ORDER BY u.native_service_bit LIMIT 1`, memberID).Scan(&period, &source, &retry, &probing)
	if errors.Is(err, sql.ErrNoRows) {
		projection, legacyErr := smartbackoff.Read(t.Context(), e.store.DB(), memberID, e.clock.Load())
		if legacyErr != nil {
			t.Fatal(legacyErr)
		}
		return projection.Backoff
	}
	if err != nil {
		t.Fatal(err)
	}
	var until *int64
	if retry.Valid {
		until = &retry.Int64
	}
	current := smartbackoff.DecodeProjection(period.String, source.String, until, probing, e.clock.Load()).Backoff
	if current != nil {
		return current
	}
	projection, err := smartbackoff.Read(t.Context(), e.store.DB(), memberID, e.clock.Load())
	if err != nil {
		t.Fatal(err)
	}
	return projection.Backoff
}
func (e *backoffEnv) wantWaiting(t *testing.T, delay int64) {
	t.Helper()
	got := e.status(t)
	if got == nil || got.State != "waiting" || got.RetryAt != e.clock.Load()+delay {
		t.Fatalf("waiting %+v at %d want delay %d", got, e.clock.Load(), delay)
	}
}
func (e *backoffEnv) ready(t *testing.T) {
	t.Helper()
	s := e.status(t)
	if s == nil {
		t.Fatal("no episode")
	}
	e.clock.Store(s.RetryAt)
}
func TestSmartBackoffNeutralProgressionAndHeaderPrecedence(t *testing.T) {
	e := newBackoffEnv(t)
	if _, err := e.store.DB().Exec("UPDATE donation_keys SET failure_streak=? WHERE id=?", u128Blob(t, 2), e.donationKey); err != nil {
		t.Fatal(err)
	}
	for index, delay := range []int64{900, 1800, 3600, 3600} {
		period := []string{"unknown", "five_hour", "unknown", "five_hour"}[index]
		h, g := e.dispatch(t)
		if (delay != 900) != g.IsBackoffTrial() {
			t.Fatal("trial marker", delay)
		}
		e.complete(t, h, quotaOutcome(period))
		e.wantWaiting(t, delay)
		// Replay uses the original outcome and cannot change the next trial time.
		e.complete(t, h, quotaOutcome(period))
		e.wantWaiting(t, delay)
		var streak []byte
		if err := e.store.DB().QueryRow("SELECT failure_streak FROM donation_keys WHERE id=?", e.donationKey).Scan(&streak); err != nil {
			t.Fatal(err)
		}
		assertU128(t, streak, 2, "quota neutral")
		e.ready(t)
	}
	h, _ := e.dispatch(t)
	if err := e.rail.ObserveResponseHeaders(t.Context(), h, contract.ResponseObservation{HasRetryAfter: true, Status: 429, ObservedAt: e.clock.Load(), RateLimitedUntil: e.clock.Load()}); err != nil {
		t.Fatal(err)
	}
	e.complete(t, h, quotaOutcome("month"))
	got := e.status(t)
	if got == nil || got.State != "ready" || got.Reason != "five_hour_quota" {
		t.Fatal("valid expired header invoked fallback", got)
	}
}
func TestSmartBackoffOnlyOneTrialAcrossModelsAndBeforeDispatchCancel(t *testing.T) {
	e := newBackoffEnv(t)
	h, _ := e.dispatch(t)
	e.complete(t, h, quotaOutcome("week"))
	e.wantWaiting(t, 21600)
	e.ready(t)
	// Cancelling a claim before dispatch must not consume the trial.
	pending, err := e.rail.Claim(t.Context(), e.retryClaimInput(t, e.request(t, e.tokenModel).ID))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.rail.ReleaseUndispatched(t.Context(), pending); err != nil {
		t.Fatal(err)
	}
	if e.status(t).State != "ready" {
		t.Fatal("undispatched cancellation acquired trial")
	}
	var handles []claim.Handle
	for _, model := range []int64{e.tokenModel, e.requestModel} {
		req := e.request(t, model)
		handle, err := e.rail.Claim(t.Context(), e.retryClaimInput(t, req.ID))
		if err != nil {
			t.Fatal(err)
		}
		handles = append(handles, handle)
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, handle := range handles {
		wg.Add(1)
		go func() {
			defer wg.Done()
			grant, err := e.rail.TakeForDispatch(context.Background(), handle)
			if grant != nil {
				grant.Clear()
			}
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	success, blocked := 0, 0
	for err := range results {
		if err == nil {
			success++
		} else if errors.Is(err, claim.ErrKeyRateLimited) {
			blocked++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || blocked != 1 || e.status(t).State != "probing" {
		t.Fatal(success, blocked, e.status(t))
	}
}
func TestSmartBackoffTrialFailuresKeepStageAndRecoveryReleasesLease(t *testing.T) {
	for _, kind := range []string{"network", "cancel", "recovery"} {
		t.Run(kind, func(t *testing.T) {
			e := newBackoffEnv(t)
			h, _ := e.dispatch(t)
			e.complete(t, h, quotaOutcome("five_hour"))
			e.ready(t)
			trial, _ := e.dispatch(t)
			switch kind {
			case "network":
				e.complete(t, trial, claim.AttemptOutcome{Kind: claim.ResultSynthetic, StreakDisposition: contract.StreakUpstreamFailure, FailureOrigin: contract.OriginNetwork})
			case "cancel":
				e.complete(t, trial, claim.AttemptOutcome{Kind: claim.ResultSynthetic, StreakDisposition: contract.StreakNeutral, FailureOrigin: contract.OriginClientCancel})
			case "recovery":
				if _, err := e.rail.RecoverNonterminal(t.Context(), 100); err != nil {
					t.Fatal(err)
				}
			}
			e.wantWaiting(t, 900)
			var streak []byte
			if err := e.store.DB().QueryRow("SELECT failure_streak FROM donation_service_health WHERE id=?", e.serviceHealthID(t, trial)).Scan(&streak); err != nil {
				t.Fatal(err)
			}
			want := int64(0)
			if kind == "network" {
				want = 1
			}
			assertU128(t, streak, want, "trial failure")
		})
	}
}
func TestSmartBackoffAcceptedHeadersRecoverBeforeCompletionWithoutEarlyNonstreamBilling(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(map[bool]string{false: "nonstream", true: "stream"}[stream], func(t *testing.T) {
			e := newBackoffEnv(t)
			h, _ := e.dispatch(t)
			e.complete(t, h, quotaOutcome("five_hour"))
			e.ready(t)
			trial, _ := e.dispatch(t)
			if err := e.rail.ObserveResponseHeaders(t.Context(), trial, contract.ResponseObservation{Status: 200, Stream: stream, ObservedAt: e.clock.Load()}); err != nil {
				t.Fatal(err)
			}
			if s := e.status(t); s != nil {
				t.Fatal("accepted trial still unavailable", s)
			}
			var starts, headers int
			if err := e.store.DB().QueryRow("SELECT count(*) FROM dispatch_response_starts WHERE claim_id=?", trial.ClaimID()).Scan(&starts); err != nil {
				t.Fatal(err)
			}
			if err := e.store.DB().QueryRow("SELECT count(*) FROM dispatch_retry_after_observations WHERE claim_id=?", trial.ClaimID()).Scan(&headers); err != nil {
				t.Fatal(err)
			}
			wantStarts := 0
			if stream {
				wantStarts = 1
			}
			if starts != wantStarts || headers != 0 {
				t.Fatal("fabricated billing/header", starts, headers)
			}
			newer, _ := e.dispatch(t)
			e.complete(t, newer, quotaOutcome("month"))
			e.wantWaiting(t, 43200)
			before := *e.status(t)
			// Recovery preserves the accepted fact but cannot clear the subsequent episode.
			if _, err := e.rail.RecoverNonterminal(t.Context(), 100); err != nil {
				t.Fatal(err)
			}
			if got := e.status(t); got == nil || *got != before {
				t.Fatal("late recovery changed newer episode", got)
			}
			var disposition string
			var price int64
			if err := e.store.DB().QueryRow("SELECT streak_disposition,price_actual_milli FROM donation_usage_reservations WHERE claim_id=?", trial.ClaimID()).Scan(&disposition, &price); err != nil {
				t.Fatal(err)
			}
			if disposition != "success" || (!stream && price != 0) {
				t.Fatal("accepted recovery accounting", disposition, price)
			}
		})
	}
}
func TestSmartBackoffLateResultsCannotChangeNewEpisode(t *testing.T) {
	e := newBackoffEnv(t)
	old, _ := e.dispatch(t)
	first, _ := e.dispatch(t)
	e.complete(t, first, quotaOutcome("five_hour"))
	before := *e.status(t)
	e.complete(t, old, quotaOutcome("month"))
	if got := e.status(t); got == nil || *got != before {
		t.Fatal("older nontrial advanced episode", got)
	}
	e.ready(t)
	oldTrial, _ := e.dispatch(t)
	if err := e.rail.ObserveResponseHeaders(t.Context(), oldTrial, contract.ResponseObservation{Status: 200, ObservedAt: e.clock.Load()}); err != nil {
		t.Fatal(err)
	}
	newer, _ := e.dispatch(t)
	e.complete(t, newer, quotaOutcome("day"))
	before = *e.status(t)
	e.complete(t, oldTrial, quotaOutcome("month"))
	if got := e.status(t); got == nil || *got != before {
		t.Fatal("accepted old trial changed newer episode", got)
	}
	var disposition string
	if err := e.store.DB().QueryRow("SELECT streak_disposition FROM donation_usage_reservations WHERE claim_id=?", oldTrial.ClaimID()).Scan(&disposition); err != nil || disposition != "success" {
		t.Fatal("initial acceptance lost success", disposition, err)
	}

}
func TestSmartBackoffExplicitResetAndEndedKeyCannotRevive(t *testing.T) {
	e := newBackoffEnv(t)
	h, _ := e.dispatch(t)
	outcome := quotaOutcome("week")
	outcome.Backoff.ResetAt = e.clock.Load() + 12345
	e.complete(t, h, outcome)
	e.wantWaiting(t, 12345)
	e.ready(t)
	trial, _ := e.dispatch(t)
	if _, err := e.store.DB().Exec("UPDATE donation_keys SET enabled=0,ended_at=?,ended_reason=?,report_match_until=? WHERE id=?", e.clock.Load(), "member_removed", e.clock.Load()+7776000, e.donationKey); err != nil {
		t.Fatal(err)
	}
	before := *e.status(t)
	e.complete(t, trial, quotaOutcome("month"))
	if got := e.status(t); got == nil || *got != before {
		t.Fatal("ended key mutated", got)
	}
}

func TestAutomaticBackoffExpiredHeadersSuppressFirstEpisode(t *testing.T) {
	for _, offset := range []int64{0, -60} {
		t.Run(fmt.Sprint(offset), func(t *testing.T) {
			e := newBackoffEnv(t)
			if _, err := e.store.DB().Exec("UPDATE donation_keys SET failure_streak=? WHERE id=?", u128Blob(t, 2), e.donationKey); err != nil {
				t.Fatal(err)
			}
			h, _ := e.dispatch(t)
			if err := e.rail.ObserveResponseHeaders(t.Context(), h, contract.ResponseObservation{HasRetryAfter: true, Status: 429, ObservedAt: e.clock.Load(), RateLimitedUntil: e.clock.Load() + offset}); err != nil {
				t.Fatal(err)
			}
			e.complete(t, h, quotaOutcome("month"))
			if e.status(t) != nil {
				t.Fatal("valid expired header fell back to body")
			}
			var streak []byte
			if err := e.store.DB().QueryRow("SELECT failure_streak FROM donation_keys WHERE id=?", e.donationKey).Scan(&streak); err != nil {
				t.Fatal(err)
			}
			assertU128(t, streak, 2, "expired header keeps streak")
		})
	}
}
