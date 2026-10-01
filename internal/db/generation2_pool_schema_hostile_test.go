package db

import (
	"testing"
)

func TestGenerationTwoHostileThursdaySharedPoolAndWelfareInvariants(t *testing.T) {
	db := openGenerationTwoConstraintFixture(t)
	uid := hostileInsertUser(t, db, "thursday", 0, 0)
	periodID, currentPoolID, nextPoolID := hostileInsertThursdayFixture(t, db)

	// Shared pool identity is coupled to its kind, OID and account code.
	wrongAccount := hostileInsertAccount(t, db, "platform", nil, "platform", 0, hostileBlob16(0), 0)
	hostileMustFail(t, db, `
INSERT INTO shared_pools(id,pool_type,period_id,account_id,state,revision,created_at)
VALUES(?, 'thursday', ?, ?, 'open',1,0)`, hostileOIDVariant("pol_", 'W', 'Q'), periodID, wrongAccount)
	hostileMustFail(t, db, `
INSERT INTO shared_pools(id,pool_type,period_id,account_id,state,revision,created_at)
VALUES(?, 'invalid', NULL, ?, 'open',1,0)`, hostileOIDVariant("pol_", 'I', 'Q'), wrongAccount)
	hostileMustFail(t, db, `
INSERT INTO shared_pools(id,pool_type,period_id,account_id,state,revision,created_at)
VALUES(?, 'thursday', ?, ?, 'open',1,0)`, hostileBadOID("pol_"), periodID, wrongAccount)

	// Current period scalar/matrix checks include Beijing-weekday identity,
	// exact 24-hour close, bp sum, capacity row and pool membership.
	hostileMustFail(t, db, `UPDATE thursday_periods SET platform_bp=10000 WHERE id=?`, periodID)
	hostileMustFail(t, db, `UPDATE thursday_periods SET platform_bp=9999,welfare_bp=1,next_pool_bp=0 WHERE id=?`, periodID)
	hostileMustFail(t, db, `UPDATE thursday_periods SET per_user_limit=0 WHERE id=?`, periodID)
	hostileMustFail(t, db, `UPDATE thursday_periods SET closes_at=0 WHERE id=?`, periodID)
	hostileMustFail(t, db, `UPDATE thursday_periods SET ledger_rows_remaining=? WHERE id=?`, hostileBlob16(0), periodID)
	hostileMustFail(t, db, `UPDATE thursday_periods SET current_pool_id=? WHERE id=?`, hostileOIDVariant("pol_", 'X', 'Q'), periodID)
	hostileMustExec(t, db, `UPDATE thursday_periods SET revision=?,created_at=? WHERE id=?`, hostileInt64Max, hostileTimeMax, periodID)
	hostileMustFail(t, db, `UPDATE thursday_periods SET revision=0 WHERE id=?`, periodID)
	hostileMustFail(t, db, `UPDATE thursday_periods SET revision=? WHERE id=?`, "9223372036854775808", periodID)
	hostileMustFail(t, db, `UPDATE thursday_periods SET created_at=? WHERE id=?`, hostileTimeMax+1, periodID)
	hostileMustExec(t, db, `UPDATE shared_pools SET revision=?,created_at=? WHERE id=?`, hostileInt64Max, hostileTimeMax, currentPoolID)
	hostileMustFail(t, db, `UPDATE shared_pools SET revision=0 WHERE id=?`, currentPoolID)
	hostileMustFail(t, db, `UPDATE shared_pools SET created_at=? WHERE id=?`, hostileTimeMax+1, currentPoolID)

	// Participant lifecycle matrix: an open row reserves one terminal ledger
	// operation and cannot claim a payout/unpaid reason before settlement.
	participantID := hostileOIDVariant("thp_", 'P', 'Q')
	hostileMustExec(t, db, `
INSERT INTO thursday_participants(
 period_id,participant_ref,user_id,contribution_count,contributed_mag,eligible_at_freeze,
 payout_mag,settled,ledger_rows_remaining,created_at,updated_at
) VALUES(?,?,?, ?,?,?, ?,0,?,0,0)`, periodID, participantID, uid, hostileBlob16(1), hostileBlob16(1), 1, hostileBlob16(0), hostileBlob16(1))
	hostileMustFail(t, db, `UPDATE thursday_participants SET ledger_rows_remaining=? WHERE period_id=? AND participant_ref=?`, hostileBlob16(0), periodID, participantID)
	hostileMustFail(t, db, `UPDATE thursday_participants SET unpaid_reason='account_banned' WHERE period_id=? AND participant_ref=?`, periodID, participantID)
	hostileMustFail(t, db, `UPDATE thursday_participants SET updated_at=-1 WHERE period_id=? AND participant_ref=?`, periodID, participantID)
	// A deleted account is a settled, deidentified participant: its user FK is
	// NULL, payout is zero, and the reserved terminal row is consumed.
	hostileMustExec(t, db, `
INSERT INTO thursday_participants(
 period_id,participant_ref,user_id,contribution_count,contributed_mag,eligible_at_freeze,
 payout_mag,unpaid_reason,settled,ledger_rows_remaining,created_at,updated_at
) VALUES(?,?,NULL,?,?,?,?, 'account_deleted',1,?,0,0)`, periodID, hostileOIDVariant("thp_", 'D', 'Q'), hostileMaxU128(), hostileBlob16(1), 0, hostileBlob16(0), hostileBlob16(0))
	// A banned account remains associated for the settled fact, but is marked
	// ineligible and also receives no payout.
	hostileMustExec(t, db, `
INSERT INTO thursday_participants(
 period_id,participant_ref,user_id,contribution_count,contributed_mag,eligible_at_freeze,
 payout_mag,unpaid_reason,settled,ledger_rows_remaining,created_at,updated_at
) VALUES(?,?,?, ?,?,?, ?, 'account_banned',1,?,0,0)`, periodID, hostileOIDVariant("thp_", 'B', 'Q'), uid, hostileBlob16(1), hostileBlob16(1), 0, hostileBlob16(0), hostileBlob16(0))
	hostileMustFail(t, db, `
INSERT INTO thursday_participants(
 period_id,participant_ref,user_id,contribution_count,contributed_mag,eligible_at_freeze,
 payout_mag,unpaid_reason,settled,ledger_rows_remaining,created_at,updated_at
) VALUES(?,?,?, ?,?,?, ?, 'account_deleted',1,?,0,0)`, periodID, hostileOIDVariant("thp_", 'E', 'Q'), uid, hostileBlob16(1), hostileBlob16(1), 0, hostileBlob16(0), hostileBlob16(0))
	// Deidentification after a banned settlement preserves the ineligible
	// classification instead of rewriting it as an account deletion.
	hostileMustExec(t, db, `
INSERT INTO thursday_participants(
 period_id,participant_ref,user_id,contribution_count,contributed_mag,eligible_at_freeze,
 payout_mag,unpaid_reason,settled,ledger_rows_remaining,created_at,updated_at
) VALUES(?,?,NULL,?,?,?,?, 'account_banned',1,?,0,0)`, periodID, hostileOIDVariant("thp_", 'C', 'Q'), hostileBlob16(1), hostileBlob16(1), 0, hostileBlob16(0), hostileBlob16(0))
	hostileMustFail(t, db, `
INSERT INTO thursday_participants(
 period_id,participant_ref,user_id,contribution_count,contributed_mag,eligible_at_freeze,
 payout_mag,unpaid_reason,settled,ledger_rows_remaining,created_at,updated_at
) VALUES(?,?,?, ?,?,?, ?, 'account_banned',0,?,0,0)`, periodID, hostileOIDVariant("thp_", 'U', 'Q'), uid, hostileBlob16(1), hostileBlob16(1), 0, hostileBlob16(0), hostileBlob16(1))
	hostileMustFail(t, db, `
INSERT INTO thursday_participants(
 period_id,participant_ref,user_id,contribution_count,contributed_mag,eligible_at_freeze,
 payout_mag,unpaid_reason,settled,ledger_rows_remaining,created_at,updated_at
) VALUES(?,?,?, ?,?,?, ?, 'account_banned',1,?,0,0)`, periodID, hostileOIDVariant("thp_", 'V', 'Q'), uid, hostileBlob16(1), hostileBlob16(1), 0, hostileBlob16(1), hostileBlob16(0))
	// A pure U128 participant count accepts 2^128-1; the SM128-like fields
	// remain independent and canonical.
	hostileMustExec(t, db, `UPDATE thursday_participants SET contribution_count=? WHERE period_id=? AND participant_ref=?`, hostileMaxU128(), periodID, participantID)

	// Welfare claims must be backed by the exact operation/source mapping and
	// obey the cap and pool-before bounds.
	operationID := hostileOIDVariant("op_", 'W', 'Q')
	hostileInsertOperation(t, db, operationID, 1, "welfare_claim", "operation", operationID)
	hostileGameAwardEntry(t, db, uid, operationID)
	hostileMustExec(t, db, `
INSERT INTO welfare_claims(user_id,site_day,award_milli,operation_id,threshold_milli,cap_milli,pool_before_milli,created_at,asset_type)
VALUES(?,'1970-01-01',1,?,0,1,1,0,'game')`, uid, operationID)
	hostileMustExec(t, db, `UPDATE welfare_claims SET created_at=? WHERE operation_id=?`, hostileTimeMax, operationID)
	hostileMustFail(t, db, `UPDATE welfare_claims SET created_at=? WHERE operation_id=?`, hostileTimeMax+1, operationID)
	hostileMustFail(t, db, `
INSERT INTO welfare_claims(user_id,site_day,award_milli,operation_id,threshold_milli,cap_milli,pool_before_milli,created_at)
VALUES(?,'1970-01-01',1,?,0,1,1,0)`, uid, hostileOIDVariant("op_", 'N', 'Q'))
	hostileMustFail(t, db, `
INSERT INTO welfare_claims(user_id,site_day,award_milli,operation_id,threshold_milli,cap_milli,pool_before_milli,created_at)
VALUES(?,'1970-01-02',2,?,0,1,1,0)`, uid, operationID)
	hostileMustFail(t, db, `
INSERT INTO welfare_claims(user_id,site_day,award_milli,operation_id,threshold_milli,cap_milli,pool_before_milli,created_at)
VALUES(?,'1970-01-03',1,?,0,0,1,0)`, uid, hostileOIDVariant("op_", 'Z', 'Q'))

	// Keep both pool IDs live so an accidental delete/replace cannot silently
	// satisfy the period's current/next references.
	var poolCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM shared_pools WHERE id IN (?,?)`, currentPoolID, nextPoolID).Scan(&poolCount); err != nil {
		t.Fatal(err)
	}
	if poolCount != 2 {
		t.Fatalf("expected both Thursday pools, got %d", poolCount)
	}
}

func TestGenerationTwoHostileThursdayDeidentifiedSettlementMatrix(t *testing.T) {
	tests := []struct {
		name      string
		eligible  int
		payout    []byte
		reason    any
		settled   int
		remaining []byte
		allowed   bool
	}{
		{
			name:      "account_deleted_ineligible_zero_payout",
			eligible:  0,
			payout:    hostileBlob16(0),
			reason:    "account_deleted",
			settled:   1,
			remaining: hostileBlob16(0),
			allowed:   true,
		},
		{
			name:      "account_deleted_eligible_zero_payout",
			eligible:  1,
			payout:    hostileBlob16(0),
			reason:    "account_deleted",
			settled:   1,
			remaining: hostileBlob16(0),
			allowed:   true,
		},
		{
			name:      "account_banned_ineligible_zero_payout",
			eligible:  0,
			payout:    hostileBlob16(0),
			reason:    "account_banned",
			settled:   1,
			remaining: hostileBlob16(0),
			allowed:   true,
		},
		{
			name:      "eligible_zero_payout",
			eligible:  1,
			payout:    hostileBlob16(0),
			reason:    nil,
			settled:   1,
			remaining: hostileBlob16(0),
			allowed:   true,
		},
		{
			name:      "eligible_positive_payout",
			eligible:  1,
			payout:    hostileBlob16(1),
			reason:    nil,
			settled:   1,
			remaining: hostileBlob16(0),
			allowed:   true,
		},
		{
			name:      "eligible_maximum_payout",
			eligible:  1,
			payout:    hostileMaxU128(),
			reason:    nil,
			settled:   1,
			remaining: hostileBlob16(0),
			allowed:   true,
		},
		{
			name:      "account_banned_eligible",
			eligible:  1,
			payout:    hostileBlob16(0),
			reason:    "account_banned",
			settled:   1,
			remaining: hostileBlob16(0),
		},
		{
			name:      "null_reason_ineligible",
			eligible:  0,
			payout:    hostileBlob16(0),
			reason:    nil,
			settled:   1,
			remaining: hostileBlob16(0),
		},
		{
			name:      "account_deleted_ineligible_positive_payout",
			eligible:  0,
			payout:    hostileBlob16(1),
			reason:    "account_deleted",
			settled:   1,
			remaining: hostileBlob16(0),
		},
		{
			name:      "account_deleted_eligible_positive_payout",
			eligible:  1,
			payout:    hostileBlob16(1),
			reason:    "account_deleted",
			settled:   1,
			remaining: hostileBlob16(0),
		},
		{
			name:      "account_banned_ineligible_positive_payout",
			eligible:  0,
			payout:    hostileBlob16(1),
			reason:    "account_banned",
			settled:   1,
			remaining: hostileBlob16(0),
		},
		{
			name:      "account_banned_eligible_positive_payout",
			eligible:  1,
			payout:    hostileBlob16(1),
			reason:    "account_banned",
			settled:   1,
			remaining: hostileBlob16(0),
		},
		{
			name:      "unsettled_null_user",
			eligible:  1,
			payout:    hostileBlob16(0),
			reason:    nil,
			settled:   0,
			remaining: hostileBlob16(1),
		},
		{
			name:      "settled_remaining_nonzero",
			eligible:  1,
			payout:    hostileBlob16(0),
			reason:    nil,
			settled:   1,
			remaining: hostileBlob16(1),
		},
	}

	const insertParticipant = `
INSERT INTO thursday_participants(
 period_id,participant_ref,user_id,contribution_count,contributed_mag,eligible_at_freeze,
 payout_mag,unpaid_reason,settled,ledger_rows_remaining,created_at,updated_at
) VALUES(?,?,NULL,?,?,?,?,?,?,?,0,0)`
	const updateParticipant = `
UPDATE thursday_participants
SET user_id=NULL,eligible_at_freeze=?,payout_mag=?,unpaid_reason=?,settled=?,
    ledger_rows_remaining=?,updated_at=1
WHERE period_id=? AND participant_ref=?`

	// Each case starts from the same period; rollback isolates its rows
	// without rebuilding every table and trigger for each mutation.
	db := openGenerationTwoConstraintFixture(t)
	periodID, _, _ := hostileInsertThursdayFixture(t, db)
	beginCase := func(t *testing.T) {
		t.Helper()
		hostileMustExec(t, db, `SAVEPOINT matrix_case`)
		t.Cleanup(func() {
			hostileMustExec(t, db, `ROLLBACK TO matrix_case; RELEASE matrix_case`)
		})
	}
	for _, tt := range tests {
		tt := tt
		t.Run("insert/"+tt.name, func(t *testing.T) {
			beginCase(t)
			participantID := hostileOIDVariant("thp_", 'M', 'Q')
			args := []any{
				periodID, participantID, hostileBlob16(1), hostileBlob16(1), tt.eligible,
				tt.payout, tt.reason, tt.settled, tt.remaining,
			}
			if tt.allowed {
				hostileMustExec(t, db, insertParticipant, args...)
				return
			}
			hostileMustFail(t, db, insertParticipant, args...)
		})

		t.Run("update/"+tt.name, func(t *testing.T) {
			beginCase(t)
			uid := hostileInsertUser(t, db, "thursday-matrix", 0, 0)
			participantID := hostileOIDVariant("thp_", 'M', 'Q')
			hostileMustExec(t, db, `
INSERT INTO thursday_participants(
 period_id,participant_ref,user_id,contribution_count,contributed_mag,eligible_at_freeze,
 payout_mag,settled,ledger_rows_remaining,created_at,updated_at
) VALUES(?,?,?, ?,?,?, ?,0,?,0,0)`, periodID, participantID, uid, hostileBlob16(1), hostileBlob16(1), 1, hostileBlob16(0), hostileBlob16(1))
			args := []any{
				tt.eligible, tt.payout, tt.reason, tt.settled, tt.remaining,
				periodID, participantID,
			}
			if tt.allowed {
				hostileMustExec(t, db, updateParticipant, args...)
				return
			}
			hostileMustFail(t, db, updateParticipant, args...)
		})
	}
}
