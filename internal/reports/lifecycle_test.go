package reports

import (
	"bytes"
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
)

func addActiveReportHold(
	t *testing.T,
	environment *reportTestEnvironment,
	adminUserID int64,
	caseID string,
	sequence uint64,
) string {
	t.Helper()
	holdID := reportTestOpaqueID("lgh_", sequence)
	now := environment.clock.Load()
	tx, err := environment.store.DB().BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	committed := false
	defer rollbackUnlessCommitted(tx, &committed)
	if _, err := tx.Exec(`INSERT INTO legal_holds(
id,object_kind,object_ref,state,revision,basis,created_by_user_id,created_at,expires_at)
VALUES(?,'report_case',?,'active',1,'credential report evidence',?,?,?)`,
		holdID, caseID, adminUserID, now, now+31536000); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`UPDATE report_cases SET legal_hold_consumed=1 WHERE id=?`, caseID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`INSERT INTO legal_hold_audits(
hold_id_text,actor_user_id,action,reason,created_at,retain_until)
VALUES(?,?,'create','credential report evidence',?,NULL)`, holdID, adminUserID, now); err != nil {
		t.Fatal(err)
	}
	if err := commit(tx, &committed); err != nil {
		t.Fatal(err)
	}
	return holdID
}

func refreshReportTestSession(t *testing.T, environment *reportTestEnvironment, token string) {
	t.Helper()
	now := environment.clock.Load()
	if _, err := environment.store.DB().Exec(`UPDATE sessions SET last_seen_at=?,expires_at=?,absolute_expires_at=?
WHERE token_hash=?`, now, now+3600, now+7200, token); err != nil {
		t.Fatal(err)
	}
}

func TestDetachUserForDeletionPreservesCaseAndPurgesIdentityUnderLegalHold(t *testing.T) {
	environment := newReportTestEnvironment(t)
	owner := environment.seedActor(t, false, 1)
	endpointID := environment.seedEndpoint(t, owner.UserID, reportTestConnector, reportTestBaseURL)
	environment.seedEndpointKey(t, endpointID, "delete-user-secret", reportTestNow)
	environment.accept(t, "delete-user-secret", "reported by the owner", 3000, 1, &owner)
	caseID := environment.caseIDForSecret(t, "delete-user-secret")
	environment.runUntilIdle(t, 3)
	admin := environment.seedActor(t, true, 1)
	addActiveReportHold(t, environment, admin.UserID, caseID, 3000)

	badgeBefore, err := environment.repository.Badge(context.Background(), admin)
	if err != nil {
		t.Fatal(err)
	}
	var deadline, materialVersion, targetVersion int64
	if err := environment.store.DB().QueryRow(`SELECT deadline,material_version,target_version
FROM report_cases WHERE id=?`, caseID).Scan(&deadline, &materialVersion, &targetVersion); err != nil {
		t.Fatal(err)
	}
	rateHash, err := environment.repository.keys.rateDigest("account", []byte(strconv.FormatInt(owner.UserID, 10)))
	if err != nil {
		t.Fatal(err)
	}
	if got := environment.rowCount(t, `SELECT COUNT(*) FROM report_rate_buckets
WHERE scope='account' AND scope_hash=?`, rateHash[:]); got != 1 {
		t.Fatalf("account rate rows before deletion=%d", got)
	}

	tx, err := environment.store.DB().BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := environment.repository.DetachUserForDeletion(context.Background(), tx, owner.UserID, environment.clock.Load()); err == nil {
		_, err = tx.Exec(`DELETE FROM users WHERE id=?`, owner.UserID)
	} else {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err == nil {
		err = tx.Commit()
	} else {
		_ = tx.Rollback()
	}
	if err != nil {
		t.Fatal(err)
	}

	var status string
	var newDeadline, newMaterialVersion, newTargetVersion int64
	if err := environment.store.DB().QueryRow(`SELECT status,deadline,material_version,target_version
FROM report_cases WHERE id=?`, caseID).Scan(&status, &newDeadline, &newMaterialVersion, &newTargetVersion); err != nil {
		t.Fatal(err)
	}
	if status != "pending_review" || newDeadline != deadline || newMaterialVersion != materialVersion+1 || newTargetVersion != targetVersion+1 {
		t.Fatalf("detached case status=%s deadline=%d material=%d target=%d",
			status, newDeadline, newMaterialVersion, newTargetVersion)
	}
	if got := environment.rowCount(t, `SELECT COUNT(*) FROM report_materials
WHERE case_id=? AND (reporter_user_id IS NOT NULL OR reporter_discord_id IS NOT NULL)`, caseID); got != 0 {
		t.Fatalf("identified materials=%d", got)
	}
	if got := environment.rowCount(t, `SELECT COUNT(*) FROM report_targets
WHERE case_id=? AND state='deleted_by_account' AND endpoint_key_id IS NULL
 AND owner_user_id IS NULL AND owner_discord_id IS NULL AND owner_display_name IS NULL`, caseID); got != 1 {
		t.Fatalf("deidentified account targets=%d", got)
	}
	if got := environment.rowCount(t, `SELECT COUNT(*) FROM report_rate_buckets
WHERE scope='account' AND scope_hash=?`, rateHash[:]); got != 0 {
		t.Fatalf("account rate rows after deletion=%d", got)
	}
	badgeAfter, err := environment.repository.Badge(context.Background(), admin)
	if err != nil {
		t.Fatal(err)
	}
	if badgeAfter.Total != badgeBefore.Total || badgeAfter.ByStatus["pending_review"] != badgeBefore.ByStatus["pending_review"] {
		t.Fatalf("badge changed before=%+v after=%+v", badgeBefore, badgeAfter)
	}

	result, err := environment.repository.Approve(context.Background(), admin, caseID, ApproveCommand{
		ExpectedMaterialVersion: newMaterialVersion,
		ExpectedTargetVersion:   newTargetVersion,
		Reason:                  "case remains actionable after account deletion",
		Confirmation:            true,
		IdempotencyKey:          reportTestKey(3001),
	})
	if err != nil || result.Status != 202 {
		t.Fatalf("approve detached case result=%+v err=%v", result, err)
	}
	environment.runUntilIdle(t, 3)
	status, progress, _, _ := environment.caseState(t, caseID)
	if status != "approved" || progress != "complete" {
		t.Fatalf("detached approved case=%s/%s", status, progress)
	}
	if environment.deletions.callCount() != 0 {
		t.Fatalf("deletion hook called for deleted account target=%d", environment.deletions.callCount())
	}
}

func TestApprovalAndAccountDeletionLinearizeInBothOrders(t *testing.T) {
	deleteOwner := func(t *testing.T, environment *reportTestEnvironment, ownerUserID int64) {
		t.Helper()
		tx, err := environment.store.DB().BeginTx(context.Background(), nil)
		if err != nil {
			t.Fatal(err)
		}
		if err = environment.repository.DetachUserForDeletion(
			context.Background(), tx, ownerUserID, environment.clock.Load(),
		); err == nil {
			_, err = tx.Exec(`DELETE FROM users WHERE id=?`, ownerUserID)
		}
		if err == nil {
			err = tx.Commit()
		} else {
			_ = tx.Rollback()
		}
		if err != nil {
			t.Fatal(err)
		}
	}

	t.Run("account deletion wins before approved worker", func(t *testing.T) {
		environment := newReportTestEnvironment(t)
		ownerUserID, _, caseID := prepareReview(t, environment, "approval-delete-first", 1)
		_, _ = approvePreparedCase(t, environment, caseID, 3050)
		_, _, _, approvedTargetVersion := environment.caseState(t, caseID)

		deleteOwner(t, environment, ownerUserID)
		status, progress, _, detachedTargetVersion := environment.caseState(t, caseID)
		if status != "approved_processing" || progress != "in_progress" || detachedTargetVersion != approvedTargetVersion+1 {
			t.Fatalf("detached approval state=%s/%s target_version=%d", status, progress, detachedTargetVersion)
		}
		if got := environment.rowCount(t, `SELECT COUNT(*) FROM report_targets
WHERE case_id=? AND state='deleted_by_account' AND endpoint_key_id IS NULL
 AND owner_user_id IS NULL AND owner_discord_id IS NULL AND owner_display_name IS NULL`, caseID); got != 1 {
			t.Fatalf("account-deleted approval targets=%d", got)
		}
		environment.runUntilIdle(t, 3)
		status, progress, _, _ = environment.caseState(t, caseID)
		if status != "approved" || progress != "complete" {
			t.Fatalf("account-deleted approval completion=%s/%s", status, progress)
		}
		if environment.deletions.callCount() != 0 {
			t.Fatalf("approved worker deleted an account-owned target twice: %d", environment.deletions.callCount())
		}
	})

	t.Run("approved worker wins before account deletion", func(t *testing.T) {
		environment := newReportTestEnvironment(t)
		ownerUserID, _, caseID := prepareReview(t, environment, "approval-worker-first", 1)
		_, _ = approvePreparedCase(t, environment, caseID, 3060)
		environment.runUntilIdle(t, 3)
		status, progress, _, targetVersion := environment.caseState(t, caseID)
		if status != "approved" || progress != "complete" {
			t.Fatalf("worker-first approval=%s/%s", status, progress)
		}

		deleteOwner(t, environment, ownerUserID)
		status, progress, _, detachedTargetVersion := environment.caseState(t, caseID)
		if status != "approved" || progress != "complete" || detachedTargetVersion != targetVersion {
			t.Fatalf("post-approval deletion state=%s/%s target_version=%d", status, progress, detachedTargetVersion)
		}
		if got := environment.rowCount(t, `SELECT COUNT(*) FROM report_targets
WHERE case_id=? AND state='deleted_by_approval' AND endpoint_key_id IS NULL
 AND owner_user_id IS NULL AND owner_discord_id IS NULL AND owner_display_name IS NULL`, caseID); got != 1 {
			t.Fatalf("deidentified approved targets=%d", got)
		}
		if environment.deletions.callCount() != 1 {
			t.Fatalf("worker-first deletion capability calls=%d", environment.deletions.callCount())
		}
	})
}

func TestDetachAtDeadlineFinishesExpiredPendingIndexingWithoutPostDeadlineCapture(t *testing.T) {
	environment := newReportTestEnvironment(t)
	owner := environment.seedActor(t, false, 1)
	endpointID := environment.seedEndpoint(t, owner.UserID, reportTestConnector, reportTestBaseURL)
	environment.seedEndpointKey(t, endpointID, "delete-expiry-secret", reportTestNow)
	environment.accept(t, "delete-expiry-secret", "", 3100, 1, &owner)
	caseID := environment.caseIDForSecret(t, "delete-expiry-secret")
	var deadline int64
	if err := environment.store.DB().QueryRow(`SELECT deadline FROM report_cases WHERE id=?`, caseID).Scan(&deadline); err != nil {
		t.Fatal(err)
	}
	atDeadlineKeyID := environment.seedEndpointKey(t, endpointID, "delete-expiry-secret", deadline)
	afterDeadlineKeyID := environment.seedEndpointKey(t, endpointID, "delete-expiry-secret", deadline+1)
	environment.setNow(deadline)
	tx, err := environment.store.DB().BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := environment.repository.DetachUserForDeletion(context.Background(), tx, owner.UserID, deadline); err == nil {
		_, err = tx.Exec(`DELETE FROM users WHERE id=?`, owner.UserID)
	} else {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err == nil {
		err = tx.Commit()
	} else {
		_ = tx.Rollback()
	}
	if err != nil {
		t.Fatal(err)
	}
	status, progress, _, _ := environment.caseState(t, caseID)
	if status != "expired" || progress != "in_progress" {
		t.Fatalf("expiry deletion state=%s/%s", status, progress)
	}
	if got := environment.rowCount(t, `SELECT COUNT(*) FROM report_targets WHERE case_id=?`, caseID); got != 1 {
		t.Fatalf("captured target count=%d", got)
	}
	if got := environment.rowCount(t, `SELECT COUNT(*) FROM report_targets
WHERE case_id=? AND state='released' AND endpoint_key_id IS NULL AND owner_user_id IS NULL`, caseID); got != 1 {
		t.Fatalf("released deidentified target count=%d", got)
	}
	if got := environment.rowCount(t, `SELECT COUNT(*) FROM report_targets
WHERE case_id=? AND endpoint_key_id IN (?,?)`, caseID, atDeadlineKeyID, afterDeadlineKeyID); got != 0 {
		t.Fatalf("deadline-or-later account targets absorbed=%d", got)
	}
	environment.runUntilIdle(t, 3)
	status, progress, _, _ = environment.caseState(t, caseID)
	if status != "expired" || progress != "complete" {
		t.Fatalf("completed expiry deletion state=%s/%s", status, progress)
	}
}

func TestReportRetentionBoundariesAndActiveCaseSafety(t *testing.T) {
	t.Run("terminal deleted exactly at ninety days", func(t *testing.T) {
		environment := newReportTestEnvironment(t)
		_, _, caseID := prepareReview(t, environment, "retention-terminal", 1)
		admin := environment.seedActor(t, true, 1)
		_, _, materialVersion, targetVersion := environment.caseState(t, caseID)
		if _, err := environment.repository.Reject(context.Background(), admin, caseID, RejectCommand{
			ExpectedMaterialVersion: materialVersion,
			ExpectedTargetVersion:   targetVersion,
			Reason:                  "fixture rejection",
			IdempotencyKey:          reportTestKey(3200),
		}); err != nil {
			t.Fatal(err)
		}
		environment.setNow(reportTestNow + caseRetentionSeconds - 1)
		if err := environment.repository.cleanupReportData(context.Background(), environment.clock.Load()); err != nil {
			t.Fatal(err)
		}
		if got := environment.rowCount(t, `SELECT COUNT(*) FROM report_cases WHERE id=?`, caseID); got != 1 {
			t.Fatalf("case before retention boundary=%d", got)
		}
		environment.setNow(reportTestNow + caseRetentionSeconds)
		if err := environment.repository.cleanupReportData(context.Background(), environment.clock.Load()); err != nil {
			t.Fatal(err)
		}
		if got := environment.rowCount(t, `SELECT COUNT(*) FROM report_cases WHERE id=?`, caseID); got != 0 {
			t.Fatalf("case at retention boundary=%d", got)
		}
	})

	t.Run("pending cases are never age cleaned", func(t *testing.T) {
		environment := newReportTestEnvironment(t)
		owner := environment.seedActor(t, false, 1)
		endpointID := environment.seedEndpoint(t, owner.UserID, reportTestConnector, reportTestBaseURL)
		environment.seedEndpointKey(t, endpointID, "retention-pending", reportTestNow)
		environment.accept(t, "retention-pending", "", 3210, 1, nil)
		caseID := environment.caseIDForSecret(t, "retention-pending")
		environment.setNow(reportTestNow + caseRetentionSeconds)
		if err := environment.repository.cleanupReportData(context.Background(), environment.clock.Load()); err != nil {
			t.Fatal(err)
		}
		if got := environment.rowCount(t, `SELECT COUNT(*) FROM report_cases WHERE id=?`, caseID); got != 1 {
			t.Fatalf("pending case cleaned=%d", got)
		}
		admin := environment.seedActor(t, true, 1)
		badge, err := environment.repository.Badge(context.Background(), admin)
		if err != nil {
			t.Fatal(err)
		}
		page, err := environment.repository.ListCases(context.Background(), admin, "pending_indexing", "", 10)
		if err != nil {
			t.Fatal(err)
		}
		if badge.ByStatus["pending_indexing"] != "1" || len(page.Data) != 1 || page.Data[0].ID != caseID {
			t.Fatalf("old pending badge=%+v page=%+v", badge, page)
		}
	})

	t.Run("approved processing survives then deletes after completion", func(t *testing.T) {
		hook := &reportTestDeletionHook{failAlways: true}
		environment := newReportTestEnvironmentWith(t, reportTestOptions{deleteHook: hook})
		_, _, caseID := prepareReview(t, environment, "retention-processing", 1)
		_, admin := approvePreparedCase(t, environment, caseID, 3220)
		environment.setNow(reportTestNow + caseRetentionSeconds)
		if _, err := environment.repository.RunWorkerOnce(context.Background()); err != nil {
			t.Fatal(err)
		}
		if got := environment.rowCount(t, `SELECT COUNT(*) FROM report_cases
WHERE id=? AND status='approved_processing'`, caseID); got != 1 {
			t.Fatalf("processing case after cleanup=%d", got)
		}
		hook.mu.Lock()
		hook.failAlways = false
		hook.mu.Unlock()
		refreshReportTestSession(t, environment, admin.SessionTokenHash)
		var targetVersion int64
		if err := environment.store.DB().QueryRow(`SELECT target_version FROM report_cases WHERE id=?`, caseID).Scan(&targetVersion); err != nil {
			t.Fatal(err)
		}
		if _, err := environment.repository.Resume(context.Background(), admin, caseID, ResumeCommand{
			ExpectedTargetVersion: targetVersion,
			IdempotencyKey:        reportTestKey(3221),
		}); err != nil {
			t.Fatal(err)
		}
		environment.runUntilIdle(t, 3)
		if got := environment.rowCount(t, `SELECT COUNT(*) FROM report_cases WHERE id=?`, caseID); got != 0 {
			t.Fatalf("completed old approval retained=%d", got)
		}
	})

	t.Run("active legal hold retains terminal case", func(t *testing.T) {
		environment := newReportTestEnvironment(t)
		_, _, caseID := prepareReview(t, environment, "retention-hold", 1)
		admin := environment.seedActor(t, true, 1)
		_, _, materialVersion, targetVersion := environment.caseState(t, caseID)
		if _, err := environment.repository.Reject(context.Background(), admin, caseID, RejectCommand{
			ExpectedMaterialVersion: materialVersion,
			ExpectedTargetVersion:   targetVersion,
			Reason:                  "fixture rejection under hold",
			IdempotencyKey:          reportTestKey(3230),
		}); err != nil {
			t.Fatal(err)
		}
		addActiveReportHold(t, environment, admin.UserID, caseID, 3230)
		environment.setNow(reportTestNow + caseRetentionSeconds)
		if err := environment.repository.cleanupReportData(context.Background(), environment.clock.Load()); err != nil {
			t.Fatal(err)
		}
		if got := environment.rowCount(t, `SELECT COUNT(*) FROM report_cases WHERE id=?`, caseID); got != 1 {
			t.Fatalf("held report case count=%d", got)
		}
	})
}

func TestTamperedSourceIPEnvelopeFailsClosedAndAlertsWithoutIP(t *testing.T) {
	environment := newReportTestEnvironment(t)
	owner := environment.seedActor(t, false, 1)
	endpointID := environment.seedEndpoint(t, owner.UserID, reportTestConnector, reportTestBaseURL)
	environment.seedEndpointKey(t, endpointID, "tamper-envelope", reportTestNow)
	environment.accept(t, "tamper-envelope", "", 3300, 1, nil)
	caseID := environment.caseIDForSecret(t, "tamper-envelope")
	environment.runUntilIdle(t, 3)
	admin := environment.seedActor(t, true, 1)
	tampered := bytes.Repeat([]byte{0xa5}, envelopeBytes)
	if _, err := environment.store.DB().Exec(`UPDATE report_materials SET source_ip_envelope=? WHERE case_id=?`, tampered, caseID); err != nil {
		t.Fatal(err)
	}
	if _, err := environment.repository.CaseDetail(context.Background(), admin, caseID, "", 10); !errors.Is(err, ErrInvariant) {
		t.Fatalf("tampered envelope error=%v", err)
	}
	var message string
	if err := environment.store.DB().QueryRow(`SELECT message FROM admin_alerts
WHERE kind='invariant_violation' AND ref=? AND resolved=0`, caseID).Scan(&message); err != nil {
		t.Fatal(err)
	}
	if message != "report_source_ip_envelope_invalid" || strings.Contains(message, "2001:db8") {
		t.Fatalf("unsafe invariant alert=%q", message)
	}
}

func TestActiveReportHoldKnownIDReadAndExpiryMaterialization(t *testing.T) {
	environment := newReportTestEnvironment(t)
	_, _, caseID := prepareReview(t, environment, "held-object-read", 1)
	admin := environment.seedActor(t, true, 1)
	_, _, materialVersion, targetVersion := environment.caseState(t, caseID)
	if _, err := environment.repository.Reject(context.Background(), admin, caseID, RejectCommand{
		ExpectedMaterialVersion: materialVersion,
		ExpectedTargetVersion:   targetVersion,
		Reason:                  "retained security evidence",
		IdempotencyKey:          reportTestKey(3400),
	}); err != nil {
		t.Fatal(err)
	}
	holdID := addActiveReportHold(t, environment, admin.UserID, caseID, 3400)
	operationID := reportTestOpaqueID("op_", 3400)
	if _, err := environment.store.DB().Exec(`INSERT INTO accepted_operations(
id,kind,actor_user_id,actor_role,payload_hash,state,checkpoint,last_error_class,created_at,terminal_at)
VALUES(?,'report_indexing',NULL,'',?,'completed',?,NULL,?,?)`,
		operationID, bytes.Repeat([]byte{0x34}, 32), caseID, reportTestNow, reportTestNow); err != nil {
		t.Fatal(err)
	}

	environment.setNow(reportTestNow + caseRetentionSeconds)
	refreshReportTestSession(t, environment, admin.SessionTokenHash)
	if err := environment.repository.cleanupReportData(context.Background(), environment.clock.Load()); err != nil {
		t.Fatal(err)
	}
	if got := environment.rowCount(t, `SELECT COUNT(*) FROM accepted_operations WHERE id=?`, operationID); got != 0 {
		t.Fatalf("legal hold retained technical operation=%d", got)
	}
	page, err := environment.repository.ListCases(context.Background(), admin, "rejected", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Data) != 0 {
		t.Fatalf("held-only case leaked into ordinary list: %+v", page)
	}
	detail, err := environment.repository.CaseDetail(context.Background(), admin, caseID, "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if detail.ID != caseID || detail.Status != "rejected" || len(detail.Materials.Data) != 1 {
		t.Fatalf("held detail=%+v", detail)
	}
	targets, err := environment.repository.Targets(context.Background(), admin, caseID, "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(targets.Data) != 1 {
		t.Fatalf("held targets=%+v", targets)
	}
	var firstRead, lastRead, readCount int64
	var retain any
	if err := environment.store.DB().QueryRow(`SELECT first_read_at,last_read_at,read_count,retain_until
FROM legal_hold_read_audits WHERE hold_id_text=? AND admin_user_id=? AND read_kind='object'`,
		holdID, admin.UserID).Scan(&firstRead, &lastRead, &readCount, &retain); err != nil {
		t.Fatal(err)
	}
	if firstRead != environment.clock.Load() || lastRead != firstRead || readCount != 2 || retain != nil {
		t.Fatalf("object read rollup first=%d last=%d count=%d retain=%v", firstRead, lastRead, readCount, retain)
	}

	var expiresAt int64
	if err := environment.store.DB().QueryRow(`SELECT expires_at FROM legal_holds WHERE id=?`, holdID).Scan(&expiresAt); err != nil {
		t.Fatal(err)
	}
	environment.setNow(expiresAt)
	refreshReportTestSession(t, environment, admin.SessionTokenHash)
	if _, err := environment.repository.CaseDetail(context.Background(), admin, caseID, "", 10); !errors.Is(err, ErrNotFound) {
		t.Fatalf("due held-only detail error=%v", err)
	}
	var holdState, endReason string
	var revision, endedAt, retainUntil int64
	if err := environment.store.DB().QueryRow(`SELECT state,revision,ended_at,end_reason,retain_until
FROM legal_holds WHERE id=?`, holdID).Scan(&holdState, &revision, &endedAt, &endReason, &retainUntil); err != nil {
		t.Fatal(err)
	}
	if holdState != "expired" || revision != 2 || endedAt != expiresAt || endReason != "expired" ||
		retainUntil != expiresAt+legalHoldRetentionSeconds {
		t.Fatalf("materialized hold state=%s revision=%d ended=%d reason=%s retain=%d",
			holdState, revision, endedAt, endReason, retainUntil)
	}
	if got := environment.rowCount(t, `SELECT COUNT(*) FROM legal_hold_audits
WHERE hold_id_text=? AND action='expire' AND actor_user_id IS NULL AND created_at=? AND retain_until=?`,
		holdID, expiresAt, retainUntil); got != 1 {
		t.Fatalf("hold expiry audits=%d", got)
	}
	var retainedRead int64
	if err := environment.store.DB().QueryRow(`SELECT retain_until FROM legal_hold_read_audits
WHERE hold_id_text=? AND admin_user_id=? AND read_kind='object'`, holdID, admin.UserID).Scan(&retainedRead); err != nil {
		t.Fatal(err)
	}
	if retainedRead != retainUntil {
		t.Fatalf("object read retain_until=%d want=%d", retainedRead, retainUntil)
	}
	if err := environment.repository.cleanupReportData(context.Background(), expiresAt); err != nil {
		t.Fatal(err)
	}
	if got := environment.rowCount(t, `SELECT COUNT(*) FROM report_cases WHERE id=?`, caseID); got != 0 {
		t.Fatalf("expired held case count=%d", got)
	}
}
