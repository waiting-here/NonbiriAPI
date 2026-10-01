package charity

import (
	"context"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/db"
)

func TestNeutralOutcomeFoldAdvancesInOrderAndRollsBack(t *testing.T) {
	e := newCharityTestEnv(t)
	generation := db.U128{15: 1}
	if _, err := e.store.DB().Exec("UPDATE donation_keys SET next_claim_seq=?,failure_disable_threshold=3 WHERE id=?", u128Blob(t, 8), e.donationKey); err != nil {
		t.Fatal(err)
	}
	for _, rollback := range []bool{true, false} {
		tx := beginTestTx(t, e.store.DB())
		for seq := int64(7); seq >= 1; seq-- {
			disposition, origin, state, success := "upstream_failure", "upstream_protocol", "committed", any(0)
			switch seq {
			case 2:
				disposition, origin, state, success = "neutral", "client_cancel", "released", 0
			case 3:
				disposition, origin = "neutral", "platform"
			case 5:
				disposition, origin, success = "success", "none", 1
			case 6:
				disposition, origin = "neutral", "downstream"
			}
			_, err := tx.Exec("INSERT INTO donation_usage_reservations(claim_id,donation_key_id,streak_generation,claim_seq,price_reserved_milli,calls_reserved,tokens_reserved,price_actual_milli,reward_actual_milli,calls_actual,tokens_actual,protocol_success,usage_unknown,state,created_at,finalized_at,streak_disposition,failure_origin) VALUES(?,?,?,?,0,0,0,0,0,0,0,?,0,?,?,?,?,?)", mustOpaqueID(t, "clm_"), e.donationKey, db.EncodeU128(generation), u128Blob(t, seq), success, state, charityTestNow, charityTestNow+seq, disposition, origin)
			if err != nil {
				t.Fatal(seq, err)
			}
			if err = foldStreak(context.Background(), tx, e.donationKey, generation, charityTestNow+seq); err != nil {
				t.Fatal(seq, err)
			}
			if seq > 1 {
				var next, streak []byte
				if err = tx.QueryRow("SELECT next_fold_seq,failure_streak FROM donation_keys WHERE id=?", e.donationKey).Scan(&next, &streak); err != nil {
					t.Fatal(err)
				}
				assertU128(t, next, 1, "gap cursor")
				assertU128(t, streak, 0, "gap streak")
			}
		}
		if rollback {
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			var count int
			if err := e.store.DB().QueryRow("SELECT count(*) FROM donation_usage_reservations WHERE donation_key_id=?", e.donationKey).Scan(&count); err != nil || count != 0 {
				t.Fatal("rollback usage", count, err)
			}
		} else {
			commitTestTx(t, tx)
		}
	}
	var next, streak []byte
	var disabled bool
	var alerts int
	if err := e.store.DB().QueryRow("SELECT next_fold_seq,failure_streak,failure_disabled FROM donation_keys WHERE id=?", e.donationKey).Scan(&next, &streak, &disabled); err != nil {
		t.Fatal(err)
	}
	assertU128(t, next, 8, "terminal cursor")
	assertU128(t, streak, 1, "only upstream failures count")
	e.store.DB().QueryRow("SELECT count(*) FROM admin_alerts WHERE kind='donation_failure_disabled'").Scan(&alerts)
	if disabled || alerts != 0 {
		t.Fatal("neutral falsely disabled key", disabled, alerts)
	}
}

func TestNeutralOutcomePreservesExistingStreak(t *testing.T) {
	e := newCharityTestEnv(t)
	generation := db.U128{15: 1}
	if _, err := e.store.DB().Exec("UPDATE donation_keys SET next_claim_seq=?,failure_streak=?,failure_disable_threshold=3 WHERE id=?", u128Blob(t, 4), u128Blob(t, 2), e.donationKey); err != nil {
		t.Fatal(err)
	}
	tx := beginTestTx(t, e.store.DB())
	defer tx.Rollback()
	for i, origin := range []string{"client_cancel", "platform", "downstream"} {
		seq := int64(i + 1)
		_, err := tx.Exec("INSERT INTO donation_usage_reservations(claim_id,donation_key_id,streak_generation,claim_seq,price_reserved_milli,calls_reserved,tokens_reserved,price_actual_milli,reward_actual_milli,calls_actual,tokens_actual,protocol_success,usage_unknown,state,created_at,finalized_at,streak_disposition,failure_origin) VALUES(?,?,?,?,0,0,0,0,0,0,0,0,0,'released',?,?,'neutral',?)", mustOpaqueID(t, "clm_"), e.donationKey, db.EncodeU128(generation), u128Blob(t, seq), charityTestNow, charityTestNow+seq, origin)
		if err != nil {
			t.Fatal(err)
		}
		if err = foldStreak(context.Background(), tx, e.donationKey, generation, charityTestNow+seq); err != nil {
			t.Fatal(err)
		}
		var next, streak []byte
		var disabled bool
		if err = tx.QueryRow("SELECT next_fold_seq,failure_streak,failure_disabled FROM donation_keys WHERE id=?", e.donationKey).Scan(&next, &streak, &disabled); err != nil {
			t.Fatal(err)
		}
		assertU128(t, next, seq+1, "neutral cursor")
		assertU128(t, streak, 2, "existing streak preserved")
		if disabled {
			t.Fatal("neutral outcome disabled key")
		}
	}
}
