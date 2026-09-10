package db

import (
	"context"
	"testing"
)

func TestBrowseExtensionPreservesPopulatedRecurringAndPresentationState(t *testing.T) {
	for _, hash := range []string{preBrowseManifestHash, preQuotaCleanupManifestHash, preStewardHoldReadManifestHash, preHourlyQuotaManifestHash} {
		t.Run(hash, func(t *testing.T) { testIndexExtensionPreservesPopulatedState(t, hash) })
	}
}

func testIndexExtensionPreservesPopulatedState(t *testing.T, hash string) {
	t.Helper()
	path, vault := bootstrapTestPath(t, "browse-retained.sqlite"), bootstrapTestVault(t)
	store, err := Open(path, vault)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	seedRetainedBusinessData(t, store, vault)
	makeRetainedSource(t, store.DB(), hash)
	database := store.DB()
	hostileMustExec(t, database, `UPDATE donation_handling SET state='processed',revision=7,processed_at=205,processed_by_role='admin',processed_by_user_id=(SELECT id FROM users WHERE is_admin=1),updated_at=205`)
	hostileMustExec(t, database, `UPDATE charity_model_access SET allowed_level_mask=21,public_description='Retained plain text <b>model</b>'`)
	rule := hostileOID("qlr_")
	hostileMustExec(t, database, `INSERT INTO donation_quota_rules(id,donation_key_id) SELECT ?,id FROM donation_keys`, rule)
	hostileMustExec(t, database, `INSERT INTO donation_quota_epochs(rule_id,epoch,mode,interval,alignment,time_zone,metric,limit_mag,effective_at,retired_at,last_observed_at,pending_reserved)
VALUES(?,1,'reset','day','calendar','Asia/Kolkata','calls',?,101,200,200,?)`, rule, hostileBlob16(10), hostileBlob16(0))
	hostileMustExec(t, database, `INSERT INTO donation_quota_periods(rule_id,epoch,start_at,end_at,used_mag,reserved_mag) VALUES(?,1,-19800,66600,?,?)`, rule, hostileBlob16(13), hostileBlob16(0))
	hostileMustExec(t, database, `UPDATE donation_quota_epochs SET current_period_start=-19800 WHERE rule_id=? AND epoch=1`, rule)
	hostileMustExec(t, database, `INSERT INTO donation_quota_receipts(claim_id,rule_id,epoch,state,actual_mag,success_at,period_start,capacity_state)
SELECT id,?,1,'settled',?,105,-19800,'attached' FROM dispatch_claims`, rule, hostileBlob16(13))
	hostileMustExec(t, database, `INSERT INTO donation_quota_epochs(rule_id,epoch,mode,interval,alignment,time_zone,metric,limit_mag,effective_at,last_observed_at,window_left,window_at,window_used,window_reserved,pending_reserved)
VALUES(?,2,'sliding','month',NULL,'America/New_York','tokens',?,200,300,-2678100,300,?,?,?)`, rule, hostileBlob16(100), hostileBlob16(7), hostileBlob16(0), hostileBlob16(0))
	hostileMustExec(t, database, `INSERT INTO donation_quota_buckets(rule_id,epoch,success_at,used_mag,reserved_mag) VALUES(?,2,201,?,?)`, rule, hostileBlob16(7), hostileBlob16(0))
	hostileMustExec(t, database, `UPDATE donation_quota_rules SET current_epoch=2,display_order=0 WHERE id=?`, rule)
	hostileMustExec(t, database, `UPDATE donation_quota_capacity SET rows_used=2,rows_held=0 WHERE id=1`)
	hostileMustExec(t, database, `INSERT INTO game_rps_presentation(session_id,pool_tie_count) SELECT id,? FROM game_rps_sessions`, hostileBlob16(7))
	hostileMustExec(t, database, `INSERT INTO game_rps_pending_presentation(user_id,own_buy_in,own_cash_out,quick_seat0_gesture,quick_seat1_gesture,quick_seat2_gesture)
SELECT user_id,?,?,'rock','paper','scissors' FROM game_rps_pending_results`, hostileBlob16(15), hostileBlob16(25))
	hostileMustExec(t, database, `INSERT INTO game_rps_summary_presentation(session_id,seat_no,own_buy_in,own_cash_out)
SELECT session_id,seat_no,?,? FROM game_rps_summary_seats`, hostileBlob16(15), hostileBlob16(25))
	if err := foreignKeyCheck(context.Background(), database); err != nil {
		t.Fatal(err)
	}
	before := retainedTableImages(t, database, nil)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 2; attempt++ {
		store, err = Open(path, vault)
		if err != nil {
			t.Fatal(err)
		}
		defer store.Close()
		assertRetainedManifest(t, store.DB(), PinnedGenerationTwoManifestHash)
		assertRetainedImages(t, store.DB(), before)
		if err := foreignKeyCheck(context.Background(), store.DB()); err != nil {
			t.Fatal(err)
		}
		if err := store.Close(); err != nil {
			t.Fatal(err)
		}
	}
}
