package charity

import (
	"context"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/db"
)

func TestFailureFoldUsesLatestThresholdWithoutNewGeneration(t *testing.T) {
	e := newCharityTestEnv(t)
	if _, err := e.store.DB().Exec("UPDATE donation_keys SET next_claim_seq=? WHERE id=?", u128Blob(t, 9), e.donationKey); err != nil {
		t.Fatal(err)
	}
	generation := db.U128{15: 1}
	var wantCount int64
	for i, c := range []struct {
		threshold         int64
		success, disabled bool
	}{
		{3, false, false}, {3, false, false}, {0, false, false}, {4, false, true},
		{10, false, false}, {0, false, false}, {0, true, false}, {1, false, true},
	} {
		tx := beginTestTx(t, e.store.DB())
		// Policy save preserves the count and changes only the threshold/error flag.
		if _, err := tx.Exec("UPDATE donation_keys SET failure_disable_threshold=?,failure_disabled=0 WHERE id=?", c.threshold, e.donationKey); err != nil {
			t.Fatal(err)
		}
		success := 0
		if c.success {
			success = 1
			wantCount = 0
		} else {
			wantCount++
		}
		sequence := int64(i + 1)
		if _, err := tx.Exec(`INSERT INTO donation_usage_reservations(claim_id,donation_key_id,streak_generation,claim_seq,price_reserved_milli,price_actual_milli,reward_actual_milli,calls_reserved,calls_actual,tokens_reserved,tokens_actual,protocol_success,usage_unknown,state,created_at,finalized_at) VALUES(?,?,?,?,0,0,0,0,0,0,0,?,0,'committed',?,?)`, mustOpaqueID(t, "clm_"), e.donationKey, db.EncodeU128(generation), u128Blob(t, sequence), success, charityTestNow, charityTestNow+sequence); err != nil {
			t.Fatal(err)
		}
		if err := foldStreak(context.Background(), tx, e.donationKey, generation, charityTestNow+sequence); err != nil {
			t.Fatal(err)
		}
		var count, gen []byte
		var disabled bool
		if err := tx.QueryRow("SELECT failure_streak,streak_generation,failure_disabled FROM donation_keys WHERE id=?", e.donationKey).Scan(&count, &gen, &disabled); err != nil {
			t.Fatal(err)
		}
		assertU128(t, count, wantCount, "count")
		assertU128(t, gen, 1, "generation")
		if disabled != c.disabled {
			t.Fatal(sequence, disabled)
		}
		commitTestTx(t, tx)
	}
	var alerts int
	e.store.DB().QueryRow("SELECT count(*) FROM admin_alerts WHERE kind='donation_failure_disabled'").Scan(&alerts)
	if alerts != 2 {
		t.Fatal("subsequent disable transition did not alert", alerts)
	}
}
