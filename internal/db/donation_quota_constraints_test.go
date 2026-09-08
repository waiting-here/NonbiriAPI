package db

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
)

func quotaConstraintFixture(t *testing.T) (*sql.DB, string) {
	t.Helper()
	database := openGenerationTwoDDLForTest(t)
	uid := hostileInsertUser(t, database, "quota-user", 0, 0)
	endpoint := hostileInsertEndpoint(t, database, uid, "https://fixture.example/v1")
	secret := hostileInsertSecret(t, database, "https://fixture.example/v1", 0)
	key := hostileInsertEndpointKey(t, database, endpoint, secret)
	donation := hostileInsertDonation(t, database, uid)
	donationKey := hostileInsertDonationKey(t, database, donation, key)
	rule := hostileOID("qlr_")
	hostileMustExec(t, database, `INSERT INTO donation_quota_rules(id,donation_key_id) VALUES(?,?)`, rule, donationKey)
	hostileMustExec(t, database, `INSERT INTO donation_quota_epochs(rule_id,epoch,mode,interval,alignment,time_zone,metric,limit_mag,effective_at,pending_reserved) VALUES(?,1,'reset','day','first_success','UTC','calls',?,1000,?)`, rule, hostileBlob16(10), hostileBlob16(0))
	hostileMustExec(t, database, `UPDATE donation_quota_rules SET current_epoch=1,display_order=0 WHERE id=?`, rule)
	return database, rule
}

func TestQuotaSameSecondAndPreUnixBoundaries(t *testing.T) {
	t.Run("same-second-retirement", func(t *testing.T) {
		database, rule := quotaConstraintFixture(t)
		hostileMustExec(t, database, `UPDATE donation_quota_epochs SET retired_at=1000 WHERE rule_id=?`, rule)
		hostileMustFail(t, database, `UPDATE donation_quota_epochs SET retired_at=999 WHERE rule_id=?`, rule)
	})
	t.Run("sliding-left-boundary", func(t *testing.T) {
		database, rule := quotaConstraintFixture(t)
		hostileMustExec(t, database, `UPDATE donation_quota_epochs SET mode='sliding',alignment=NULL,window_left=-85400,window_at=1000,window_used=?,window_reserved=? WHERE rule_id=?`, hostileBlob16(0), hostileBlob16(0), rule)
		hostileMustFail(t, database, `UPDATE donation_quota_epochs SET window_left=window_at`)
	})
	t.Run("deferred-current-period", func(t *testing.T) {
		database, rule := quotaConstraintFixture(t)
		tx, err := database.BeginTx(context.Background(), nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		if _, err := tx.Exec(`UPDATE donation_quota_epochs SET current_period_start=-3600 WHERE rule_id=?`, rule); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(`INSERT INTO donation_quota_periods(rule_id,epoch,start_at,end_at,used_mag,reserved_mag) VALUES(?,1,-3600,82800,?,?)`, rule, hostileBlob16(0), hostileBlob16(0)); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		hostileMustFail(t, database, `DELETE FROM donation_quota_periods`)
		hostileMustExec(t, database, `UPDATE donation_quota_epochs SET current_period_start=NULL`)
		hostileMustExec(t, database, `DELETE FROM donation_quota_periods`)
	})
}

func TestQuotaRejectsInvalidStateCombinations(t *testing.T) {
	for _, tc := range []struct{ name, query string }{
		{"reset-null-alignment", `UPDATE donation_quota_epochs SET alignment=NULL`},
		{"calendar-week-null-start", `UPDATE donation_quota_epochs SET alignment='calendar',interval='week',week_starts_on=NULL`},
		{"calendar-five-hours", `UPDATE donation_quota_epochs SET alignment='calendar',interval='5h'`},
		{"nonweekly-week-start", `UPDATE donation_quota_epochs SET week_starts_on=1`},
		{"sliding-half-cache", `UPDATE donation_quota_epochs SET mode='sliding',alignment=NULL,window_left=0`},
		{"dangling-current-period", `UPDATE donation_quota_epochs SET current_period_start=1234`},
		{"fractional-period-start", `INSERT INTO donation_quota_periods(rule_id,epoch,start_at,end_at,used_mag,reserved_mag) SELECT rule_id,epoch,1000.5,2000,limit_mag,pending_reserved FROM donation_quota_epochs`},
		{"fractional-epoch", `INSERT INTO donation_quota_epochs(rule_id,epoch,mode,interval,alignment,time_zone,metric,limit_mag,effective_at,pending_reserved) SELECT rule_id,1.5,mode,interval,alignment,time_zone,metric,limit_mag,effective_at,pending_reserved FROM donation_quota_epochs`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			database, _ := quotaConstraintFixture(t)
			hostileMustFail(t, database, tc.query)
		})
	}
	for day := 1; day <= 7; day++ {
		t.Run(fmt.Sprintf("calendar-week-%d", day), func(t *testing.T) {
			database, _ := quotaConstraintFixture(t)
			hostileMustExec(t, database, `UPDATE donation_quota_epochs SET alignment='calendar',interval='week',week_starts_on=?`, day)
		})
	}
}

func TestQuotaAggregateModeCannotDrift(t *testing.T) {
	for _, sliding := range []bool{false, true} {
		t.Run(fmt.Sprintf("sliding-%v", sliding), func(t *testing.T) {
			database, rule := quotaConstraintFixture(t)
			period := `INSERT INTO donation_quota_periods(rule_id,epoch,start_at,end_at,used_mag,reserved_mag) VALUES(?,1,1000,2000,?,?)`
			bucket := `INSERT INTO donation_quota_buckets(rule_id,epoch,success_at,used_mag,reserved_mag) VALUES(?,1,1000,?,?)`
			if sliding {
				hostileMustExec(t, database, `UPDATE donation_quota_epochs SET mode='sliding',alignment=NULL`)
				hostileMustFail(t, database, period, rule, hostileBlob16(0), hostileBlob16(0))
				hostileMustExec(t, database, bucket, rule, hostileBlob16(0), hostileBlob16(0))
				hostileMustFail(t, database, `UPDATE donation_quota_buckets SET success_at=1000.5`)
				hostileMustFail(t, database, `UPDATE donation_quota_epochs SET mode='reset',alignment='first_success'`)
			} else {
				hostileMustFail(t, database, bucket, rule, hostileBlob16(0), hostileBlob16(0))
				hostileMustExec(t, database, period, rule, hostileBlob16(0), hostileBlob16(0))
				hostileMustFail(t, database, `UPDATE donation_quota_epochs SET mode='sliding',alignment=NULL`)
			}
		})
	}
}

func TestQuotaReceiptPeriodReferenceIsRetainedUntilRelease(t *testing.T) {
	database, rule := quotaConstraintFixture(t)
	var userID, keyID, secretID, donationKeyID int64
	if err := database.QueryRow(`SELECT d.user_id,k.endpoint_key_id,e.secret_ref_id,k.id
FROM donation_keys k JOIN donations d ON d.id=k.donation_id JOIN endpoint_keys e ON e.id=k.endpoint_key_id`).Scan(&userID, &keyID, &secretID, &donationKeyID); err != nil {
		t.Fatal(err)
	}
	reqID, claimID := hostileOID("req_"), hostileOID("clm_")
	hostileInsertLogicalRequest(t, database, reqID, userID, "charity_chat_completions", 1)
	hostileMustExec(t, database, `INSERT INTO dispatch_claims(id,logical_request_id,attempt_seq,purpose,endpoint_key_id,secret_ref_id,donation_key_id,claim_now,state,donor_reward_state)
VALUES(?,?,1,'charity',?,?,?,1000,'claimed','pending')`, claimID, reqID, keyID, secretID, donationKeyID)
	receiptSQL := `INSERT INTO donation_quota_receipts(claim_id,rule_id,epoch,state,reserved_mag,remaining_reserved_mag,success_at,period_start,capacity_state)
VALUES(?,?,1,'started',?,?,1000,1000,'attached')`
	hostileMustFail(t, database, receiptSQL, claimID, rule, hostileBlob16(1), hostileBlob16(1))
	hostileMustExec(t, database, `INSERT INTO donation_quota_periods(rule_id,epoch,start_at,end_at,used_mag,reserved_mag) VALUES(?,1,1000,2000,?,?)`, rule, hostileBlob16(0), hostileBlob16(1))
	hostileMustExec(t, database, receiptSQL, claimID, rule, hostileBlob16(1), hostileBlob16(1))
	hostileMustFail(t, database, `DELETE FROM donation_quota_periods`)
	hostileMustExec(t, database, `DELETE FROM donation_quota_receipts`)
	hostileMustExec(t, database, `DELETE FROM donation_quota_periods`)
}

func TestHandlingProcessingActorMatchesState(t *testing.T) {
	database := openGenerationTwoDDLForTest(t)
	uid := hostileInsertUser(t, database, "processing-actor", 0, 0)
	donation := hostileInsertDonation(t, database, nil)
	for _, state := range []string{"legacy", "pending", "closed"} {
		closedAt, reason := any(nil), ""
		if state == "closed" {
			closedAt, reason = int64(0), "withdrawn"
		}
		hostileMustFail(t, database, `INSERT INTO donation_handling(donation_id,state,revision,processed_by_user_id,closed_at,closed_reason,created_at,updated_at) VALUES(?,?,1,?,?,?,0,0)`, donation, state, uid, closedAt, reason)
	}
	hostileMustExec(t, database, `INSERT INTO donation_handling(donation_id,state,revision,processed_at,processed_by_user_id,processed_by_role,created_at,updated_at) VALUES(?,'processed',1,0,?,'level5',0,0)`, donation, uid)
	hostileMustExec(t, database, `DELETE FROM users WHERE id=?`, uid)
	var actor any
	var state, role string
	if err := database.QueryRow(`SELECT state,processed_by_role,processed_by_user_id FROM donation_handling`).Scan(&state, &role, &actor); err != nil {
		t.Fatal(err)
	}
	if state != "processed" || role != "level5" || actor != nil {
		t.Fatal("processing history did not survive actor deletion safely")
	}
}

func TestPublicModelDescriptionTextBoundary(t *testing.T) {
	database := openGenerationTwoDDLForTest(t)
	hostileMustExec(t, database, `INSERT INTO charity_models(provider,model,full_name,enabled,pricing_mode,created_at,updated_at) VALUES('fixture','model','[公益]fixture/model',0,'per_request',0,0)`)
	hostileMustExec(t, database, `INSERT INTO charity_model_access(model_id) SELECT id FROM charity_models`)
	for _, value := range []string{"", "<b>literal</b>\n\ttext", strings.Repeat("界", 1024), strings.Repeat("🙂", 1024)} {
		hostileMustExec(t, database, `UPDATE charity_model_access SET public_description=?`, value)
	}
	for c := rune(0); c <= 159; c++ {
		if c == '\t' || c == '\n' || (c >= 32 && c < 127) {
			continue
		}
		t.Run(fmt.Sprintf("control-%d", c), func(t *testing.T) {
			hostileMustFail(t, database, `UPDATE charity_model_access SET public_description=?`, "before"+string(c)+"after")
		})
	}
	hostileMustFail(t, database, `UPDATE charity_model_access SET public_description=?`, strings.Repeat("a", 1025))
	hostileMustFail(t, database, `UPDATE charity_model_access SET public_description=?`, []byte("not-text"))
}
