package db

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerationTwoHostileStorageAndAppendOnlyGuards(t *testing.T) {
	db := openGenerationTwoDDLForTest(t)
	uid := hostileInsertUser(t, db, "storage", 0, 0)
	accountID := hostileInsertAccount(t, db, "user", uid, nil, 0, hostileBlob16(0), 0)
	opID := hostileOID("op_")
	hostileInsertOperation(t, db, opID, 1, "admin_user_adjustment", "operation", opID)
	hostileMustExec(t, db, `
INSERT INTO credit_entries(operation_id,line_no,account_id,account_kind_snapshot,delta_sign,delta_mag)
VALUES(?,0,?,'user',1,?)`, opID, accountID, hostileBlob16(1))

	for _, table := range []string{"credit_operations", "credit_entries"} {
		var withoutRowID int
		if err := db.QueryRow(`SELECT wr FROM pragma_table_list WHERE name=?`, table).Scan(&withoutRowID); err != nil {
			t.Fatalf("table-list %s: %v", table, err)
		}
		if withoutRowID != 1 {
			t.Fatalf("%s must be WITHOUT ROWID", table)
		}
	}
	hostileMustFail(t, db, `UPDATE credit_operations SET created_at=1 WHERE id=?`, opID)
	hostileMustFail(t, db, `DELETE FROM credit_operations WHERE id=?`, opID)
	hostileMustFail(t, db, `UPDATE credit_entries SET line_no=1 WHERE operation_id=? AND line_no=0`, opID)
	hostileMustFail(t, db, `DELETE FROM credit_entries WHERE operation_id=? AND line_no=0`, opID)
	var retentionColumns []string
	rows, err := db.Query(`PRAGMA index_info('idx_request_logs_retention')`)
	if err != nil {
		t.Fatalf("request-log retention index lookup: %v", err)
	}
	for rows.Next() {
		var seq, cid int
		var name string
		if err := rows.Scan(&seq, &cid, &name); err != nil {
			rows.Close()
			t.Fatalf("request-log retention index scan: %v", err)
		}
		retentionColumns = append(retentionColumns, name)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		t.Fatalf("request-log retention index rows: %v", err)
	}
	if err := rows.Close(); err != nil {
		t.Fatalf("request-log retention index close: %v", err)
	}
	if len(retentionColumns) != 2 || retentionColumns[0] != "completed_at" || retentionColumns[1] != "id" {
		t.Fatalf("request-log retention index must be (completed_at,id), got %v", retentionColumns)
	}
	for _, retention := range []struct {
		table   string
		columns []string
	}{
		{table: "game_fishing_batches", columns: []string{"settled_at"}},
		{table: "request_attempts", columns: []string{"completed_at"}},
		{table: "welfare_claims", columns: []string{"created_at"}},
	} {
		if !hostileHasIndexPrefix(t, db, retention.table, retention.columns...) {
			t.Fatalf("%s must have a dedicated retention index beginning with %v", retention.table, retention.columns)
		}
	}

	// Required owner/worker/partial indexes and immutable guards are part of
	// the schema contract, not optional query optimizations.
	for _, indexName := range []string{
		"idx_credit_operations_source",
		"idx_credit_operations_created",
		"idx_credit_entries_account",
		"idx_dispatch_claims_request",
		"idx_dispatch_claims_secret",
		"idx_dispatch_claims_due",
		"idx_report_cases_active_fingerprint",
		"idx_thursday_active_participant",
		"idx_rps_rank_profit",
		"idx_rps_rank_net",
	} {
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_schema WHERE type='index' AND name=?`, indexName).Scan(&count); err != nil {
			t.Fatalf("required index %s lookup: %v", indexName, err)
		}
		if count != 1 {
			t.Fatalf("required index %s missing", indexName)
		}
	}

	for _, triggerName := range []string{
		"endpoints_identity_immutable",
		"endpoint_key_secrets_identity_immutable",
		"caller_keys_generation_guard",
		"donation_key_membership_consistency_guard",
		"rps_gesture_envelope_guard",
		"legal_hold_consumed_guard",
	} {
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_schema WHERE type='trigger' AND name=?`, triggerName).Scan(&count); err != nil {
			t.Fatalf("required trigger %s lookup: %v", triggerName, err)
		}
		if count != 1 {
			t.Fatalf("required trigger %s missing", triggerName)
		}
	}
}

func TestGenerationTwoHostileLegalHoldMarkersDeletesAndAudits(t *testing.T) {
	db := openGenerationTwoConstraintFixture(t)
	adminID := hostileInsertUser(t, db, "legal-admin", 1, 0)
	userID := hostileInsertUser(t, db, "legal-user", 0, 0)
	// announcement_audits has an INTEGER identity independent of the other
	// aggregate tables; this first row is the legal-hold root exercised below.
	hostileInsertAnnouncementAudit(t, db, adminID, 0)

	maintenanceID := hostileOIDVariant("op_", 'M', 'Q')
	hostileInsertMaintenanceEvent(t, db, maintenanceID, adminID)
	// A disable event closes the open enable in the same transaction and gives
	// both rows the one immutable de-identification/retention schedule.
	disableMaintenanceID := hostileOIDVariant("op_", 'C', 'Q')
	hostileMustExec(t, db, `BEGIN`)
	hostileMustExec(t, db, `
INSERT INTO maintenance_events(
 id,actor_user_id,actor_discord_id,actor_role,action,reason,created_at,
 resolved_at,deidentify_at,retain_until
) VALUES(?,?,NULL,'admin','disable','hostile',100,100,7776100,34560100)`, disableMaintenanceID, adminID)
	hostileMustExec(t, db, `
UPDATE maintenance_events
SET resolved_at=100,deidentify_at=7776100,retain_until=34560100
WHERE id=? AND action='enable'`, maintenanceID)
	hostileMustExec(t, db, `COMMIT`)
	var enableResolved, disableResolved, enableDeidentify, disableDeidentify, enableRetain, disableRetain int64
	if err := db.QueryRow(`SELECT resolved_at,deidentify_at,retain_until FROM maintenance_events WHERE id=?`, maintenanceID).Scan(&enableResolved, &enableDeidentify, &enableRetain); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT resolved_at,deidentify_at,retain_until FROM maintenance_events WHERE id=?`, disableMaintenanceID).Scan(&disableResolved, &disableDeidentify, &disableRetain); err != nil {
		t.Fatal(err)
	}
	if enableResolved != 100 || disableResolved != 100 || enableDeidentify != 7776100 || disableDeidentify != 7776100 || enableRetain != 34560100 || disableRetain != 34560100 {
		t.Fatalf("maintenance close did not atomically close both events: enable=(%d,%d,%d) disable=(%d,%d,%d)", enableResolved, enableDeidentify, enableRetain, disableResolved, disableDeidentify, disableRetain)
	}

	// Account deletion must clear all three maintenance actor identity fields
	// together; FK SET NULL on only actor_user_id would violate the row check.
	maintenanceActor := hostileInsertUser(t, db, "maintenance-actor-delete", 0, 0)
	maintenanceActorEvent := hostileOIDVariant("op_", 'D', 'Q')
	hostileMustExec(t, db, `
INSERT INTO maintenance_events(
 id,actor_user_id,actor_discord_id,actor_role,action,reason,created_at
) VALUES(?,?,?,'steward','enable','hostile',0)`, maintenanceActorEvent, maintenanceActor, "hostile-steward")
	hostileMustExec(t, db, `DELETE FROM users WHERE id=?`, maintenanceActor)
	var clearedActor, clearedDiscord, clearedRole any
	if err := db.QueryRow(`SELECT actor_user_id,actor_discord_id,actor_role FROM maintenance_events WHERE id=?`, maintenanceActorEvent).Scan(&clearedActor, &clearedDiscord, &clearedRole); err != nil {
		t.Fatal(err)
	}
	if clearedActor != nil || clearedDiscord != nil || clearedRole != nil {
		t.Fatalf("maintenance actor identity was not fully deidentified: user=%v discord=%v role=%v", clearedActor, clearedDiscord, clearedRole)
	}

	reportID := hostileOIDVariant("rpc_", 'R', 'Q')
	hostileInsertReportCase(t, db, reportID, hostileBlob32(1), "pending_review", "complete", 0)
	materialID := hostileInsertReportMaterial(t, db, reportID)
	targetID := hostileOIDVariant("rpt_", 'R', 'Q')
	hostileInsertReportTarget(t, db, reportID, targetID)
	decisionID := hostileInsertReportDecision(t, db, reportID, adminID)

	legalDonationEndpointID := hostileInsertEndpoint(t, db, userID, "https://legal-hold-donation.example/v1")
	legalDonationSecretID := hostileInsertSecret(t, db, "https://legal-hold-donation.example/v1", 0)
	legalDonationEndpointKeyID := hostileInsertEndpointKey(t, db, legalDonationEndpointID, legalDonationSecretID)
	donationID := hostileInsertDonation(t, db, userID)
	donationKeyID := hostileInsertDonationKey(t, db, donationID, legalDonationEndpointKeyID)
	reviewID := hostileInsertDonationReview(t, db, donationID, adminID)

	// Append-only review/decision rows permit only the FK-driven actor NULL
	// mutation.  Any other direct field rewrite remains forbidden.
	reviewActor := hostileInsertUser(t, db, "review-actor-null", 0, 0)
	reviewActorReviewID := hostileInsertDonationReview(t, db, donationID, reviewActor)
	hostileMustExec(t, db, `UPDATE donation_reviews SET reviewer_user_id=NULL WHERE id=?`, reviewActorReviewID)
	hostileMustFail(t, db, `UPDATE donation_reviews SET note='rewritten' WHERE id=?`, reviewActorReviewID)
	reviewDeleteActor := hostileInsertUser(t, db, "review-actor-delete", 0, 0)
	reviewDeleteID := hostileInsertDonationReview(t, db, donationID, reviewDeleteActor)
	hostileMustExec(t, db, `DELETE FROM users WHERE id=?`, reviewDeleteActor)
	var reviewerAfterDelete any
	if err := db.QueryRow(`SELECT reviewer_user_id FROM donation_reviews WHERE id=?`, reviewDeleteID).Scan(&reviewerAfterDelete); err != nil {
		t.Fatal(err)
	}
	if reviewerAfterDelete != nil {
		t.Fatalf("donation review actor FK did not deidentify: %v", reviewerAfterDelete)
	}

	decisionActor := hostileInsertUser(t, db, "decision-actor-null", 0, 0)
	decisionActorDecisionID := hostileInsertReportDecision(t, db, reportID, decisionActor)
	hostileMustExec(t, db, `UPDATE report_decisions SET actor_user_id=NULL WHERE id=?`, decisionActorDecisionID)
	hostileMustFail(t, db, `UPDATE report_decisions SET reason='rewritten' WHERE id=?`, decisionActorDecisionID)
	decisionDeleteActor := hostileInsertUser(t, db, "decision-actor-delete", 0, 0)
	decisionDeleteID := hostileInsertReportDecision(t, db, reportID, decisionDeleteActor)
	hostileMustExec(t, db, `DELETE FROM users WHERE id=?`, decisionDeleteActor)
	var decisionActorAfterDelete any
	if err := db.QueryRow(`SELECT actor_user_id FROM report_decisions WHERE id=?`, decisionDeleteID).Scan(&decisionActorAfterDelete); err != nil {
		t.Fatal(err)
	}
	if decisionActorAfterDelete != nil {
		t.Fatalf("report decision actor FK did not deidentify: %v", decisionActorAfterDelete)
	}

	requestID := hostileOIDVariant("req_", 'L', 'Q')
	hostileInsertLogicalRequest(t, db, requestID, userID, "openai_chat_completions", 1)
	requestLogID := hostileInsertRequestLog(t, db, requestID, userID, "openai_chat_completions")
	attemptID := hostileOIDVariant("clm_", 'A', 'Q')
	hostileInsertAttempt(t, db, attemptID, requestLogID, 1)

	// Announcement actor deletion is allowed through the FK path, while an
	// early manual NULL update is not.  A due 90-day de-identification update
	// is allowed and uses the same narrow mutation.
	earlyAnnouncementAt := hostileTimeMax - 7776000 - 100
	earlyAnnouncementID := hostileInsertAnnouncementAudit(t, db, adminID, earlyAnnouncementAt)
	hostileMustFail(t, db, `UPDATE announcement_audits SET actor_user_id=NULL WHERE id=?`, earlyAnnouncementID)
	futureAnnouncementActor := hostileInsertUser(t, db, "announcement-actor-delete", 0, 0)
	futureAnnouncementID := hostileInsertAnnouncementAudit(t, db, futureAnnouncementActor, earlyAnnouncementAt)
	hostileMustExec(t, db, `DELETE FROM users WHERE id=?`, futureAnnouncementActor)
	var announcementActorAfterDelete any
	if err := db.QueryRow(`SELECT actor_user_id FROM announcement_audits WHERE id=?`, futureAnnouncementID).Scan(&announcementActorAfterDelete); err != nil {
		t.Fatal(err)
	}
	if announcementActorAfterDelete != nil {
		t.Fatalf("announcement audit actor FK did not deidentify: %v", announcementActorAfterDelete)
	}
	dueAnnouncementID := hostileInsertAnnouncementAudit(t, db, adminID, 0)
	hostileMustExec(t, db, `UPDATE announcement_audits SET actor_user_id=NULL WHERE id=?`, dueAnnouncementID)
	var dueAnnouncementActor any
	if err := db.QueryRow(`SELECT actor_user_id FROM announcement_audits WHERE id=?`, dueAnnouncementID).Scan(&dueAnnouncementActor); err != nil {
		t.Fatal(err)
	}
	if dueAnnouncementActor != nil {
		t.Fatalf("due announcement audit actor was not deidentified: %v", dueAnnouncementActor)
	}

	// The five legal-hold roots cannot be inserted already marked.  A marker
	// is an irreversible fact produced only by the matching active-hold CAS.
	hostileMustFail(t, db, `
INSERT INTO maintenance_events(
 id,actor_user_id,actor_discord_id,actor_role,action,reason,created_at,legal_hold_consumed
) VALUES(?,?,NULL,'admin','enable','hostile',0,1)`, hostileOIDVariant("op_", 'I', 'Q'), adminID)
	hostileMustFail(t, db, `
INSERT INTO report_cases(
 id,fingerprint,connector_type,canonical_base_url,status,progress_state,
 material_version,target_version,deadline,cursor_source,cursor_id,material_count,target_count,
 distinct_owner_count,processed_target_count,deleted_target_count,released_target_count,
 retry_attempt_count,created_at,terminal_at,legal_hold_consumed
) VALUES(?,?,'openai-compatible','https://upstream.example/v1','pending_review','complete',
 1,1,1000,NULL,NULL,0,0,0,0,0,0,0,0,NULL,1)`, hostileOIDVariant("rpc_", 'I', 'Q'), hostileBlob32(11))
	hostileMustFail(t, db, `
INSERT INTO announcement_audits(
 announcement_id_text,actor_user_id,action,from_revision,to_revision,reason,
 created_at,actor_deidentify_at,legal_hold_consumed
) VALUES(?,?,'create',0,1,'hostile',0,7776000,1)`, hostileOIDVariant("ann_", 'I', 'Q'), adminID)
	hostileMustFail(t, db, `
INSERT INTO donations(
 user_id,status,revision,description,review_note,created_at,updated_at,legal_hold_consumed
) VALUES(?,'pending',1,'','',0,0,1)`, userID)
	markerRequestID := hostileOIDVariant("req_", 'I', 'Q')
	hostileInsertLogicalRequest(t, db, markerRequestID, userID, "openai_chat_completions", 1)
	hostileMustFail(t, db, `
INSERT INTO request_logs(
 logical_request_id,user_id,model,upstream_model_id,route_kind,endpoint_base_url,
 started_at,legal_hold_consumed
) VALUES(?,?,'model','upstream','openai_chat_completions','https://upstream.example/v1',0,1)`, markerRequestID, userID)

	hostileMustFail(t, db, `
INSERT INTO legal_holds(id,object_kind,object_ref,state,revision,basis,created_by_user_id,created_at,expires_at)
VALUES(?, 'report_case','not-a-report-id','active',1,'hostile',?,100,200)`, hostileOIDVariant("lgh_", 'X', 'Q'), adminID)
	hostileMustFail(t, db, `
INSERT INTO legal_holds(id,object_kind,object_ref,state,revision,basis,created_by_user_id,created_at,expires_at)
VALUES(?, 'maintenance_event',?,'active',1,'hostile',?,100,200)`, hostileOIDVariant("lgh_", 'Y', 'Q'), maintenanceID, userID)
	hostileMustFail(t, db, `
INSERT INTO legal_holds(id,object_kind,object_ref,state,revision,basis,created_by_user_id,created_at,expires_at)
VALUES(?, 'maintenance_event',?,'active',1,'hostile',?,100,31536101)`, hostileOIDVariant("lgh_", 'Z', 'Q'), maintenanceID, adminID)
	hostileMustFail(t, db, `
INSERT INTO legal_holds(id,object_kind,object_ref,state,revision,basis,created_by_user_id,created_at,expires_at)
VALUES(?, 'maintenance_event',?,'released',1,'hostile',?,100,200)`, hostileOIDVariant("lgh_", 'V', 'Q'), maintenanceID, adminID)

	rootCases := []struct {
		name, kind, ref, holdID, markerUpdate, deleteRoot string
		children                                          []string
	}{
		{
			"maintenance-event", "maintenance_event", maintenanceID, hostileOIDVariant("lgh_", 'M', 'Q'),
			`UPDATE maintenance_events SET legal_hold_consumed=1 WHERE id=?`,
			`DELETE FROM maintenance_events WHERE id=?`, nil,
		},
		{
			"report-case", "report_case", reportID, hostileOIDVariant("lgh_", 'R', 'Q'),
			`UPDATE report_cases SET legal_hold_consumed=1 WHERE id=?`,
			`DELETE FROM report_cases WHERE id=?`, []string{
				`DELETE FROM report_materials WHERE id=?`,
				`DELETE FROM report_targets WHERE id=?`,
				`DELETE FROM report_decisions WHERE id=?`,
			},
		},
		{
			"announcement-audit", "announcement_audit", "1", hostileOIDVariant("lgh_", 'A', 'Q'),
			`UPDATE announcement_audits SET legal_hold_consumed=1 WHERE id=1`,
			`DELETE FROM announcement_audits WHERE id=1`, nil,
		},
		{
			"donation", "donation", fmt.Sprintf("%d", donationID), hostileOIDVariant("lgh_", 'D', 'Q'),
			`UPDATE donations SET legal_hold_consumed=1 WHERE id=?`,
			`DELETE FROM donations WHERE id=?`, []string{
				`DELETE FROM donation_keys WHERE id=?`,
				`DELETE FROM donation_reviews WHERE id=?`,
			},
		},
		{
			"request-log", "request_log", fmt.Sprintf("%d", requestLogID), hostileOIDVariant("lgh_", 'Q', 'Q'),
			`UPDATE request_logs SET legal_hold_consumed=1 WHERE id=?`,
			`DELETE FROM request_logs WHERE id=?`, []string{
				`DELETE FROM request_attempts WHERE claim_id=?`,
			},
		},
	}

	for _, tc := range rootCases {
		t.Run(tc.name, func(t *testing.T) {
			hostileInsertLegalHold(t, db, tc.holdID, tc.kind, tc.ref, adminID)
			switch tc.kind {
			case "maintenance_event":
				hostileMustExec(t, db, tc.markerUpdate, tc.ref)
			case "report_case":
				hostileMustExec(t, db, tc.markerUpdate, tc.ref)
			case "donation":
				hostileMustExec(t, db, tc.markerUpdate, donationID)
			case "request_log":
				hostileMustExec(t, db, tc.markerUpdate, requestLogID)
			case "announcement_audit":
				hostileMustExec(t, db, tc.markerUpdate)
			}
			// A legal-hold marker is monotonic and can only be set in the same
			// transaction as an active matching hold.
			switch tc.kind {
			case "maintenance_event":
				hostileMustFail(t, db, `UPDATE maintenance_events SET legal_hold_consumed=0 WHERE id=?`, tc.ref)
			case "report_case":
				hostileMustFail(t, db, `UPDATE report_cases SET legal_hold_consumed=0 WHERE id=?`, tc.ref)
			case "donation":
				hostileMustFail(t, db, `UPDATE donations SET legal_hold_consumed=0 WHERE id=?`, donationID)
			case "request_log":
				hostileMustFail(t, db, `UPDATE request_logs SET legal_hold_consumed=0 WHERE id=?`, requestLogID)
			case "announcement_audit":
				hostileMustFail(t, db, `UPDATE announcement_audits SET legal_hold_consumed=0 WHERE id=1`)
			}
			for _, childDelete := range tc.children {
				switch childDelete {
				case `DELETE FROM report_materials WHERE id=?`:
					hostileMustFail(t, db, childDelete, materialID)
				case `DELETE FROM report_targets WHERE id=?`:
					hostileMustFail(t, db, childDelete, targetID)
				case `DELETE FROM report_decisions WHERE id=?`:
					hostileMustFail(t, db, childDelete, decisionID)
				case `DELETE FROM donation_keys WHERE id=?`:
					hostileMustFail(t, db, childDelete, donationKeyID)
				case `DELETE FROM donation_reviews WHERE id=?`:
					hostileMustFail(t, db, childDelete, reviewID)
				case `DELETE FROM request_attempts WHERE claim_id=?`:
					hostileMustFail(t, db, childDelete, attemptID)
				}
			}
			switch tc.kind {
			case "maintenance_event", "report_case":
				hostileMustFail(t, db, tc.deleteRoot, tc.ref)
			case "donation":
				hostileMustFail(t, db, tc.deleteRoot, donationID)
			case "request_log":
				hostileMustFail(t, db, tc.deleteRoot, requestLogID)
			case "announcement_audit":
				hostileMustFail(t, db, tc.deleteRoot)
			}
		})
	}

	// No root may carry a marker without an active matching hold.
	unheldMaintenanceID := hostileOIDVariant("op_", 'U', 'Q')
	hostileInsertMaintenanceEvent(t, db, unheldMaintenanceID, adminID)
	hostileMustFail(t, db, `UPDATE maintenance_events SET legal_hold_consumed=1 WHERE id=?`, unheldMaintenanceID)
	hostileMustFail(t, db, `DELETE FROM legal_holds WHERE id=?`, rootCases[0].holdID)

	// Audit rows are append-only except for one narrow retention update after
	// the hold ends.  Arbitrary edits and second retention changes are hostile.
	auditHoldID := rootCases[1].holdID
	hostileMustFail(t, db, `UPDATE legal_holds SET object_kind='donation' WHERE id=?`, auditHoldID)
	hostileMustFail(t, db, `UPDATE legal_holds SET object_ref=? WHERE id=?`, hostileOIDVariant("rpc_", 'Z', 'Q'), auditHoldID)
	hostileMustFail(t, db, `UPDATE legal_holds SET expires_at=201 WHERE id=?`, auditHoldID)
	hostileMustFail(t, db, `UPDATE legal_holds SET basis='rewritten' WHERE id=?`, auditHoldID)
	hostileMustFail(t, db, `UPDATE legal_holds SET created_by_user_id=? WHERE id=?`, userID, auditHoldID)
	hostileMustFail(t, db, `UPDATE legal_holds SET revision=2 WHERE id=?`, auditHoldID)
	retainUntil := int64(150 + 34560000)
	hostileMustFail(t, db, `
UPDATE legal_holds
SET state='released',revision=1,ended_by_user_id=?,ended_at=150,end_reason='released',retain_until=?
WHERE id=?`, adminID, retainUntil, auditHoldID)
	hostileMustFail(t, db, `
 UPDATE legal_holds
 SET state='released',revision=3,ended_by_user_id=?,ended_at=150,end_reason='released',retain_until=?
 WHERE id=?`, adminID, retainUntil, auditHoldID)
	createAuditID := hostileNextPK64(t, db, "legal_hold_audits")
	hostileMustExec(t, db, `
	INSERT INTO legal_hold_audits(id,hold_id_text,actor_user_id,action,created_at)
	VALUES(?,?,?,'create',100)`, createAuditID, auditHoldID, adminID)
	hostileMustFail(t, db, `UPDATE legal_hold_audits SET reason='changed' WHERE hold_id_text=? AND action='create'`, auditHoldID)
	hostileMustExec(t, db, `
UPDATE legal_holds SET state='released',revision=2,ended_by_user_id=?,ended_at=150,end_reason='released',retain_until=? WHERE id=?`, adminID, retainUntil, auditHoldID)
	hostileMustFail(t, db, `
UPDATE legal_holds
SET state='active',revision=3,ended_by_user_id=NULL,ended_at=NULL,end_reason=NULL,retain_until=NULL
WHERE id=?`, auditHoldID)
	hostileMustFail(t, db, `
UPDATE legal_holds
SET state='expired',revision=3,ended_by_user_id=NULL,ended_at=200,end_reason='expired',retain_until=34560200
WHERE id=?`, auditHoldID)
	hostileMustExec(t, db, `UPDATE legal_hold_audits SET retain_until=? WHERE hold_id_text=? AND action='create'`, retainUntil, auditHoldID)
	hostileMustFail(t, db, `UPDATE legal_hold_audits SET retain_until=? WHERE hold_id_text=? AND action='create'`, retainUntil+1, auditHoldID)
	releaseAuditID := hostileNextPK64(t, db, "legal_hold_audits")
	hostileMustExec(t, db, `
	INSERT INTO legal_hold_audits(id,hold_id_text,actor_user_id,action,reason,created_at,retain_until)
	VALUES(?,?,?,'release','released',150,?)`, releaseAuditID, auditHoldID, adminID, retainUntil)
	hostileMustFail(t, db, `
INSERT INTO legal_hold_audits(hold_id_text,actor_user_id,action,reason,created_at,retain_until)
VALUES(?,?,'release','released',150,?)`, auditHoldID, adminID, retainUntil)

	readHoldID := rootCases[0].holdID
	hostileMustExec(t, db, `
INSERT INTO legal_hold_read_audits(hold_id_text,admin_user_id,read_kind,first_read_at,last_read_at,read_count)
VALUES(?,?, 'metadata',100,100,1)`, readHoldID, adminID)
	hostileMustExec(t, db, `UPDATE legal_hold_read_audits SET last_read_at=101,read_count=2 WHERE hold_id_text=? AND admin_user_id=? AND read_kind='metadata'`, readHoldID, adminID)
	hostileMustFail(t, db, `UPDATE legal_hold_read_audits SET last_read_at=102,read_count=2 WHERE hold_id_text=? AND admin_user_id=? AND read_kind='metadata'`, readHoldID, adminID)
	hostileMustFail(t, db, `UPDATE legal_hold_read_audits SET last_read_at=103,read_count=4 WHERE hold_id_text=? AND admin_user_id=? AND read_kind='metadata'`, readHoldID, adminID)
	hostileMustFail(t, db, `UPDATE legal_hold_read_audits SET last_read_at=102,read_count=1 WHERE hold_id_text=? AND admin_user_id=? AND read_kind='metadata'`, readHoldID, adminID)
	hostileMustFail(t, db, `UPDATE legal_hold_read_audits SET first_read_at=101,last_read_at=102,read_count=3 WHERE hold_id_text=? AND admin_user_id=? AND read_kind='metadata'`, readHoldID, adminID)
	hostileMustFail(t, db, `UPDATE legal_hold_read_audits SET last_read_at=100,read_count=3 WHERE hold_id_text=? AND admin_user_id=? AND read_kind='metadata'`, readHoldID, adminID)
	hostileMustFail(t, db, `UPDATE legal_hold_read_audits SET last_read_at=99 WHERE hold_id_text=? AND admin_user_id=? AND read_kind='metadata'`, readHoldID, adminID)
	hostileMustFail(t, db, `UPDATE legal_hold_read_audits SET retain_until=1 WHERE hold_id_text=? AND admin_user_id=? AND read_kind='metadata'`, readHoldID, adminID)
	readRetainUntil := int64(150 + 34560000)
	hostileMustExec(t, db, `
UPDATE legal_holds SET state='released',revision=2,ended_by_user_id=?,ended_at=150,end_reason='released',retain_until=?
WHERE id=?`, adminID, readRetainUntil, readHoldID)
	hostileMustExec(t, db, `UPDATE legal_hold_read_audits SET retain_until=? WHERE hold_id_text=? AND admin_user_id=? AND read_kind='metadata'`, readRetainUntil, readHoldID, adminID)
	hostileMustFail(t, db, `UPDATE legal_hold_read_audits SET retain_until=? WHERE hold_id_text=? AND admin_user_id=? AND read_kind='metadata'`, readRetainUntil+1, readHoldID, adminID)
	hostileMustExec(t, db, `UPDATE legal_hold_read_audits SET last_read_at=102,read_count=3 WHERE hold_id_text=? AND admin_user_id=? AND read_kind='metadata'`, readHoldID, adminID)
	hostileMustFail(t, db, `UPDATE legal_hold_read_audits SET last_read_at=103,read_count=3 WHERE hold_id_text=? AND admin_user_id=? AND read_kind='metadata'`, readHoldID, adminID)
	hostileMustFail(t, db, `UPDATE legal_hold_read_audits SET last_read_at=104,read_count=5 WHERE hold_id_text=? AND admin_user_id=? AND read_kind='metadata'`, readHoldID, adminID)
	hostileMustFail(t, db, `UPDATE legal_hold_read_audits SET retain_until=?,last_read_at=103,read_count=4 WHERE hold_id_text=? AND admin_user_id=? AND read_kind='metadata'`, readRetainUntil+1, readHoldID, adminID)
	var firstRead, lastRead, readCount int64
	var readRetain sql.NullInt64
	if err := db.QueryRow(`SELECT first_read_at,last_read_at,read_count,retain_until FROM legal_hold_read_audits WHERE hold_id_text=? AND admin_user_id=? AND read_kind='metadata'`, readHoldID, adminID).Scan(&firstRead, &lastRead, &readCount, &readRetain); err != nil {
		t.Fatal(err)
	}
	if firstRead != 100 || lastRead != 102 || readCount != 3 || !readRetain.Valid || readRetain.Int64 != readRetainUntil {
		t.Fatalf("ended-hold read audit rollup mismatch: first=%d last=%d count=%d retain=%v", firstRead, lastRead, readCount, readRetain)
	}

	// Cleanup may remove ended hold metadata, but the root's irreversible
	// marker survives and blocks a second hold for the same object.
	hostileMustExec(t, db, `DELETE FROM legal_hold_audits WHERE hold_id_text=?`, auditHoldID)
	hostileMustExec(t, db, `DELETE FROM legal_hold_read_audits WHERE hold_id_text=?`, auditHoldID)
	hostileMustExec(t, db, `DELETE FROM legal_holds WHERE id=?`, auditHoldID)
	var reportMarker int
	if err := db.QueryRow(`SELECT legal_hold_consumed FROM report_cases WHERE id=?`, reportID).Scan(&reportMarker); err != nil {
		t.Fatal(err)
	}
	if reportMarker != 1 {
		t.Fatalf("ended hold cleanup cleared report root marker: %d", reportMarker)
	}
	hostileMustFail(t, db, `
INSERT INTO legal_holds(id,object_kind,object_ref,state,revision,basis,created_by_user_id,created_at,expires_at)
VALUES(?,'report_case',?,'active',1,'reopened',?,100,200)`, hostileOIDVariant("lgh_", 'S', 'Q'), reportID, adminID)
}

func TestGenerationTwoHostileManifestDynamicRPSAccountIdentity(t *testing.T) {
	// Fresh bootstrap has two pool accounts, both canonical zero SM128 values.
	// The stricter fresh validator also has to tolerate a valid historical RPS
	// platform account after its queue/session root has been cleaned up.
	var image []byte
	{
		freshPath := filepath.Join(privateDBDir(t), "manifest-rps-fresh.sqlite")
		store := openTestStore(t, freshPath)
		db := store.DB()
		if err := validateGenerationTwoFreshSeedManifest(context.Background(), db); err != nil {
			t.Fatalf("fresh seed manifest rejected canonical pools: %v", err)
		}
		rows, err := db.Query(`
SELECT p.pool_type,a.kind,a.code,a.balance_sign,hex(a.balance_mag)
FROM shared_pools p JOIN credit_accounts a ON a.id=p.account_id
ORDER BY p.pool_type`)
		if err != nil {
			t.Fatal(err)
		}
		poolCount := 0
		for rows.Next() {
			var poolType, kind, code, balanceHex string
			var sign int
			if err := rows.Scan(&poolType, &kind, &code, &sign, &balanceHex); err != nil {
				t.Fatal(err)
			}
			poolCount++
			if kind != "pool" || sign != 0 || balanceHex != strings.Repeat("0", 32) ||
				code == "" || !strings.HasPrefix(code, "pool:") {
				t.Fatalf("fresh pool account is not canonical zero: type=%q kind=%q code=%q sign=%d mag=%q", poolType, kind, code, sign, balanceHex)
			}
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
		if poolCount != 2 {
			t.Fatalf("fresh seed expected two pool accounts, got %d", poolCount)
		}
		var busy, logFrames, checkpointed int
		if err := db.QueryRow(`PRAGMA wal_checkpoint(TRUNCATE)`).Scan(&busy, &logFrames, &checkpointed); err != nil {
			t.Fatalf("checkpoint fresh manifest fixture: %v", err)
		}
		if busy != 0 || logFrames < 0 || checkpointed < 0 || checkpointed > logFrames {
			t.Fatalf("fresh manifest fixture checkpoint returned (%d,%d,%d)", busy, logFrames, checkpointed)
		}
		if err := store.Close(); err != nil {
			t.Fatalf("close fresh manifest fixture: %v", err)
		}
		image, err = os.ReadFile(freshPath)
		if err != nil {
			t.Fatalf("read fresh manifest fixture: %v", err)
		}
	}
	{
		store := openTestStore(t, copyPrivateSQLiteTestImage(t, image))
		db := store.DB()
		validQueueID := hostileOIDVariant("rpsq_", 'H', 'Q')
		validQueueCode := "rps-queue:" + validQueueID
		hostileMustExec(t, db, `
INSERT INTO credit_accounts(id,kind,user_id,code,balance_sign,balance_mag,created_at,updated_at)
VALUES(?,?,?,?,0,?,0,0)`, hostileNextPK64(t, db, "credit_accounts"), "platform", nil, validQueueCode, hostileBlob16(0))
		if err := validateGenerationTwoSeedManifest(context.Background(), db); err != nil {
			t.Fatalf("valid historical dynamic RPS account without live root rejected: %v", err)
		}
	}
	for _, tc := range []struct {
		name  string
		query string
		args  []any
	}{
		{
			name:  "welfare-sign",
			query: `UPDATE credit_accounts SET balance_sign=1 WHERE id=(SELECT account_id FROM shared_pools WHERE pool_type='welfare')`,
		},
		{
			name:  "thursday-magnitude",
			query: `UPDATE credit_accounts SET balance_mag=? WHERE id=(SELECT account_id FROM shared_pools WHERE pool_type='thursday')`,
			args:  []any{hostileBlob16(1)},
		},
	} {
		tc := tc
		t.Run("fresh-corruption-"+tc.name, func(t *testing.T) {
			store := openTestStore(t, copyPrivateSQLiteTestImage(t, image))
			db := store.DB()
			hostileMustExec(t, db, `PRAGMA ignore_check_constraints=ON`)
			hostileMustExec(t, db, tc.query, tc.args...)
			if err := validateGenerationTwoFreshSeedManifest(context.Background(), db); err == nil {
				t.Fatalf("fresh manifest accepted corrupted %s pool account", tc.name)
			}
		})
	}

	// Each malformed identity gets a fresh database.  A malformed row must be
	// the only corruption under test; retaining an earlier bad row would make
	// later iterations unable to prove their own rejection.
	malformed := []string{
		"rps-queue:rpsq_A", // short body
		"rps-queue:rpsq_" + strings.Repeat("A", 21) + "B", // bad tail
		"rps-queue:rpsq_" + strings.Repeat("A", 21) + "!", // bad charset
		"rps-queue:rpsq_" + strings.Repeat("A", 22) + "Q", // overlong body
		"rps-session:rps_A", // short body
		"rps-session:rps_" + strings.Repeat("A", 21) + "B", // bad tail
		"rps-session:rps_" + strings.Repeat("A", 21) + "!", // bad charset
		"rps-session:rps_" + strings.Repeat("A", 22) + "Q", // overlong body
		"rps-queue:rpsx_" + strings.Repeat("A", 21) + "Q",  // wrong prefix
	}
	for i, code := range malformed {
		t.Run(fmt.Sprintf("malformed-%02d", i), func(t *testing.T) {
			store := openTestStore(t, copyPrivateSQLiteTestImage(t, image))
			db := store.DB()
			var dynamicCount int
			if err := db.QueryRow(`SELECT COUNT(*) FROM credit_accounts WHERE kind='platform' AND (code LIKE 'rps-queue:%' OR code LIKE 'rps-session:%')`).Scan(&dynamicCount); err != nil {
				t.Fatal(err)
			}
			if dynamicCount != 0 {
				t.Fatalf("manifest fixture copy contains %d prior dynamic accounts", dynamicCount)
			}
			hostileMustExec(t, db, `PRAGMA ignore_check_constraints=ON`)
			id := hostileNextPK64(t, db, "credit_accounts")
			hostileMustExec(t, db, `
		INSERT INTO credit_accounts(id,kind,user_id,code,balance_sign,balance_mag,created_at,updated_at)
		VALUES(?, 'platform',NULL,?,0,?,0,0)`, id, code, hostileBlob16(0))
			if err := validateGenerationTwoSeedManifest(context.Background(), db); err == nil {
				t.Fatalf("manifest accepted malformed dynamic RPS code %q", code)
			}
		})
	}
}
