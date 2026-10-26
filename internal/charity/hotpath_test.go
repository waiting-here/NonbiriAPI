package charity

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/claim"
	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/db"
)

func TestStreakRangeStopsAtGapsAndPreservesThreshold(t *testing.T) {
	e := newCharityTestEnv(t)
	one := u128Blob(t, 1)
	generation, err := db.DecodeU128(one)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.store.DB().Exec("UPDATE donation_keys SET next_claim_seq=?,next_fold_seq=?,failure_streak=?,failure_disabled=0,failure_disable_threshold='2' WHERE id=?", u128Blob(t, 258), u128Blob(t, 254), u128Blob(t, 0), e.donationKey); err != nil {
		t.Fatal(err)
	}
	seed := func(sequence int64, state, disposition, origin string, success any) {
		t.Helper()
		tx := beginTestTx(t, e.store.DB())
		if state == "reserved" {
			_, err = tx.Exec(`INSERT INTO donation_usage_reservations(claim_id,donation_key_id,streak_generation,claim_seq,price_reserved_milli,calls_reserved,tokens_reserved,state,created_at) VALUES(?,?,?,?,0,0,0,'reserved',?)`, mustOpaqueID(t, "clm_"), e.donationKey, one, u128Blob(t, sequence), charityTestNow)
		} else {
			_, err = tx.Exec(`INSERT INTO donation_usage_reservations(claim_id,donation_key_id,streak_generation,claim_seq,price_reserved_milli,price_actual_milli,reward_actual_milli,calls_reserved,calls_actual,tokens_reserved,tokens_actual,protocol_success,usage_unknown,state,created_at,finalized_at,streak_disposition,failure_origin) VALUES(?,?,?,?,0,0,0,0,0,0,0,?,0,?,?,?, ?,?)`, mustOpaqueID(t, "clm_"), e.donationKey, one, u128Blob(t, sequence), success, state, charityTestNow, charityTestNow, disposition, origin)
		}
		if err != nil {
			tx.Rollback()
			t.Fatal(err)
		}
		commitTestTx(t, tx)
	}
	check := func(wantNext, wantStreak int64, wantDisabled, wantAlerts int) {
		t.Helper()
		tx := beginTestTx(t, e.store.DB())
		if err = foldStreak(t.Context(), tx, e.donationKey, generation, charityTestNow); err != nil {
			tx.Rollback()
			t.Fatal(err)
		}
		commitTestTx(t, tx)
		var next, streak []byte
		var disabled, alerts int
		if err = e.store.DB().QueryRow("SELECT next_fold_seq,failure_streak,failure_disabled FROM donation_keys WHERE id=?", e.donationKey).Scan(&next, &streak, &disabled); err != nil {
			t.Fatal(err)
		}
		if err = e.store.DB().QueryRow("SELECT COUNT(*) FROM admin_alerts WHERE kind='donation_failure_disabled'").Scan(&alerts); err != nil {
			t.Fatal(err)
		}
		assertU128(t, next, wantNext, "next")
		assertU128(t, streak, wantStreak, "streak")
		if disabled != wantDisabled || alerts != wantAlerts {
			t.Fatalf("disabled=%d alerts=%d", disabled, alerts)
		}
	}
	seed(255, "committed", "upstream_failure", "upstream_protocol", 0)
	seed(256, "committed", "upstream_failure", "network", 0)
	seed(257, "reserved", "", "", nil)
	check(254, 0, 0, 0) // Later terminal rows must not hide a missing sequence.
	seed(254, "committed", "success", "none", 1)
	check(257, 2, 1, 1) // BLOB ordering crosses 255; reserved 257 stops the fold.
	check(257, 2, 1, 1) // Replay does not duplicate the disable event.
	var body string
	if err = e.store.DB().QueryRow("SELECT context_json FROM admin_alerts WHERE kind='donation_failure_disabled'").Scan(&body); err != nil {
		t.Fatal(err)
	}
	if !containsFoldFact(body, "256") {
		t.Fatalf("wrong crossing point: %s", body)
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	tx := beginTestTx(t, e.store.DB())
	defer tx.Rollback()
	if err = foldStreak(cancelled, tx, e.donationKey, generation, charityTestNow); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation=%v", err)
	}
}

func containsFoldFact(body, want string) bool {
	var value struct {
		Facts []struct{ Key, Value string } `json:"occurred_facts"`
	}
	if json.Unmarshal([]byte(body), &value) != nil {
		return false
	}
	for _, fact := range value.Facts {
		if fact.Key == "fold" && fact.Value == want {
			return true
		}
	}
	return false
}

func TestChargePreviewDoesNotAcquireWriter(t *testing.T) {
	e := newCharityTestEnv(t)
	rail := e.billingRail(t)
	request := e.billingRequest(t, rail, e.tokenModel, 5, 1)
	handle := e.billingDispatch(t, rail, request.ID, 1)
	if err := rail.MarkResponseStarted(t.Context(), handle); err != nil {
		t.Fatal(err)
	}
	if _, err := rail.CompleteAttempt(t.Context(), handle, claim.AttemptOutcome{Kind: claim.ResultResponse, UpstreamStatus: 200, ProtocolSuccess: true, ResponseStarted: true, Usage: contract.Usage{Present: true, UncachedInputTokens: 2}}); err != nil {
		t.Fatal(err)
	}
	held, err := e.store.DB().Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	charge, err := e.service.CalculateRequestCharge(ctx, request.ID, claim.AccountingCommit)
	cancel()
	held.Close()
	if err != nil || charge != 7 {
		t.Fatalf("charge=%d err=%v", charge, err)
	}
	if _, err = rail.CompleteRequest(t.Context(), claim.CompleteRequestInput{RequestID: request.ID, Caller: claim.CallerResult{Class: claim.ResultSuccess, Status: 200}, Disposition: claim.AccountingCommit, ActualChargeMilli: 1}); err != nil {
		t.Fatal(err)
	}
	var actual int64
	if err = e.store.DB().QueryRow("SELECT user_charge_milli FROM charity_reservations WHERE logical_request_id=?", request.ID).Scan(&actual); err != nil || actual != charge {
		t.Fatalf("actual=%d preview=%d err=%v", actual, charge, err)
	}
	cancelled, stop := context.WithCancel(t.Context())
	stop()
	if _, err = e.service.CalculateRequestCharge(cancelled, request.ID, claim.AccountingCommit); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation=%v", err)
	}
}
