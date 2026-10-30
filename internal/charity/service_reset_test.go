package charity

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/agentplan"
	"github.com/waiting-here/NonbiriAPI/internal/claim"
	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/donation"
)

// Exercise the real key-reset entry while an older attempt is still dispatched.
func TestServiceFailureResetIsolatesLateAttemptAndPreservesWaits(t *testing.T) {
	e := newBackoffEnv(t)
	old, _, authorization := connectionDispatch(t, e, claim.RouteCharityChat)
	adminResult, err := e.store.DB().Exec(`INSERT INTO users(discord_id,username,is_admin,donation_credit_mag,total_requests,total_uncached_input_tokens,total_cache_write_input_tokens,total_cache_read_input_tokens,total_output_tokens,total_unknown_usage_requests,revision,created_at,updated_at) VALUES(NULL,'reset manager',1,zeroblob(16),zeroblob(16),zeroblob(16),zeroblob(16),zeroblob(16),zeroblob(16),zeroblob(16),zeroblob(16),?,?)`, charityTestNow, charityTestNow)
	if err != nil {
		t.Fatal(err)
	}
	adminID, err := adminResult.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.store.DB().Exec(`INSERT INTO donation_handling(donation_id,state,revision,created_at,updated_at) VALUES(?,'pending',1,?,?) ON CONFLICT(donation_id) DO NOTHING`, e.donationID, charityTestNow, charityTestNow); err != nil {
		t.Fatal(err)
	}
	now := e.clock.Load()
	if _, err := e.store.DB().Exec(`UPDATE donation_service_health SET failure_streak=nbi_u128(10),failure_disabled=1,retry_after_until=?,backoff_period='day',backoff_source='builtin',backoff_stage=1,backoff_retry_at=? WHERE use_id=?`, now+60, now+120, authorization.UseID); err != nil {
		t.Fatal(err)
	}
	service, err := donation.New(donation.Config{Store: e.store, Now: func() time.Time { return time.Unix(e.clock.Load(), 0) }})
	if err != nil {
		t.Fatal(err)
	}
	var revision int64
	if err = e.store.DB().QueryRow("SELECT revision FROM donations WHERE id=?", e.donationID).Scan(&revision); err != nil {
		t.Fatal(err)
	}
	tx := beginTestTx(t, e.store.DB())
	_, err = service.AgentApply(t.Context(), tx, agentplan.Actor{ID: adminID, Role: "admin"}, agentplan.Operation{Kind: "donation.key.reset", TargetID: fmt.Sprint(e.donationID), Input: json.RawMessage(fmt.Sprintf(`{"key_id":"%d","expected_revision":"%d"}`, e.donationKey, revision))}, func(ctx context.Context, tx *sql.Tx, actor agentplan.Actor, _ string) error {
		return (forceReviewAuthority{}).AuthorizeAdminMutation(ctx, tx, actor.ID)
	})
	if err != nil {
		t.Fatal(err)
	}
	commitTestTx(t, tx)
	e.complete(t, old, claim.AttemptOutcome{Kind: claim.ResultResponse, UpstreamStatus: 503, StreakDisposition: contract.StreakUpstreamFailure, FailureOrigin: contract.OriginUpstreamResponse})
	var count, generation []byte
	var disabled bool
	var retry, backoff int64
	if err = e.store.DB().QueryRow("SELECT failure_streak,streak_generation,failure_disabled,retry_after_until,backoff_retry_at FROM donation_service_health WHERE use_id=?", authorization.UseID).Scan(&count, &generation, &disabled, &retry, &backoff); err != nil {
		t.Fatal(err)
	}
	assertU128(t, count, 0, "late pre-reset failure")
	assertU128(t, generation, 2, "service reset generation")
	if disabled || retry != now+60 || backoff != now+120 {
		t.Fatalf("reset/waits: %t %d %d", disabled, retry, backoff)
	}
	// Once the preserved waiting period ends, a new generation can claim normally.
	e.clock.Add(121)
	fresh, _, _ := connectionDispatch(t, e, claim.RouteCharityChat)
	e.complete(t, fresh, claim.AttemptOutcome{Kind: claim.ResultResponse, UpstreamStatus: 503, StreakDisposition: contract.StreakUpstreamFailure, FailureOrigin: contract.OriginUpstreamResponse})
	if err = e.store.DB().QueryRow("SELECT failure_streak FROM donation_service_health WHERE use_id=?", authorization.UseID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	assertU128(t, count, 1, "new generation failure")
}
