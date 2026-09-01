package charity

import (
	"context"
	"testing"
)

func TestExportConsumerTxAggregateCutoffAndCleanupRollback(t *testing.T) {
	environment := newCharityTestEnv(t)
	requestID := environment.accept(t, environment.requestModel, 2400, 1)
	terminalAt := charityTestNow + 30
	reward := u128Blob(t, 1250)

	tx := beginTestTx(t, environment.store.DB())
	if _, err := tx.Exec(`UPDATE charity_reservations SET state='committed',
original_charge_milli=2500,user_charge_milli=2000,donor_reward_total_mag=?,
usage_uncached_input_tokens=11,cache_write_input_tokens=12,cache_read_input_tokens=13,
usage_output_tokens=14,usage_unknown=1,finalized_at=?,updated_at=?
WHERE logical_request_id=?`, reward, terminalAt, terminalAt, requestID); err != nil {
		t.Fatal(err)
	}
	inside, err := environment.service.ExportConsumerTx(context.Background(), tx, environment.callerID, terminalAt+1, 10)
	if err != nil {
		t.Fatal(err)
	}
	want := ConsumerExport{
		RequestCount: "1", OriginalCharge: "2.5", Charged: "2", Saved: "0.5", DonorReward: "1.25",
		UncachedInput: "11", CacheWriteInput: "12", CacheReadInput: "13", Output: "14", UsageUnknownCount: "1",
	}
	if inside != want {
		t.Fatalf("same-tx aggregate = %+v, want %+v", inside, want)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}

	tx = beginTestTx(t, environment.store.DB())
	afterRollback, err := environment.service.ExportConsumerTx(context.Background(), tx, environment.callerID, terminalAt+1, 10)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if afterRollback != (ConsumerExport{
		RequestCount: "1", OriginalCharge: "0", Charged: "0", Saved: "0", DonorReward: "0",
		UncachedInput: "0", CacheWriteInput: "0", CacheReadInput: "0", Output: "0", UsageUnknownCount: "0",
	}) {
		t.Fatalf("rolled-back aggregate = %+v", afterRollback)
	}

	tx = beginTestTx(t, environment.store.DB())
	if _, err := tx.Exec(`UPDATE charity_reservations SET state='committed',
original_charge_milli=2500,user_charge_milli=2000,donor_reward_total_mag=?,
usage_uncached_input_tokens=11,cache_write_input_tokens=12,cache_read_input_tokens=13,
usage_output_tokens=14,usage_unknown=1,finalized_at=?,updated_at=?
WHERE logical_request_id=?`, reward, terminalAt, terminalAt, requestID); err != nil {
		t.Fatal(err)
	}
	commitTestTx(t, tx)

	decisionNow := terminalAt + terminalRetention
	tx = beginTestTx(t, environment.store.DB())
	cutoff, err := environment.service.ExportConsumerTx(context.Background(), tx, environment.callerID, decisionNow, 10)
	if err != nil {
		t.Fatal(err)
	}
	if cutoff.RequestCount != "0" || cutoff.OriginalCharge != "0" || cutoff.DonorReward != "0" {
		t.Fatalf("ordinary cutoff aggregate = %+v", cutoff)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}

	tx = beginTestTx(t, environment.store.DB())
	cleaned, err := environment.service.CleanupTx(context.Background(), tx, decisionNow, 10)
	if err != nil || cleaned != 1 {
		t.Fatalf("CleanupTx = %d, %v", cleaned, err)
	}
	var insideCount int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM charity_reservations WHERE logical_request_id=?`, requestID).Scan(&insideCount); err != nil || insideCount != 0 {
		t.Fatalf("inside cleanup count = %d, %v", insideCount, err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	var outsideCount int
	if err := environment.store.DB().QueryRow(`SELECT COUNT(*) FROM charity_reservations WHERE logical_request_id=?`, requestID).Scan(&outsideCount); err != nil || outsideCount != 1 {
		t.Fatalf("rolled-back cleanup count = %d, %v", outsideCount, err)
	}
}
