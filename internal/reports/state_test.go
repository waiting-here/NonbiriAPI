package reports

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/netip"
	"testing"
)

func prepareReview(t *testing.T, environment *reportTestEnvironment, secretValue string, keyCount int) (int64, []int64, string) {
	t.Helper()
	owner := environment.seedActor(t, false, 1)
	endpointID := environment.seedEndpoint(t, owner.UserID, reportTestConnector, reportTestBaseURL)
	keyIDs := make([]int64, 0, keyCount)
	for index := 0; index < keyCount; index++ {
		keyIDs = append(keyIDs, environment.seedEndpointKey(t, endpointID, secretValue, environment.clock.Load()))
	}
	environment.accept(t, secretValue, "initial material", 1000, 1, nil)
	caseID := environment.caseIDForSecret(t, secretValue)
	environment.runUntilIdle(t, 10)
	status, progress, _, _ := environment.caseState(t, caseID)
	if status != "pending_review" || progress != "complete" {
		t.Fatalf("prepared case status=%s progress=%s", status, progress)
	}
	return owner.UserID, keyIDs, caseID
}

func TestAdminReportReadsPaginationSafeIntegersAndIPDecryption(t *testing.T) {
	environment := newReportTestEnvironment(t)
	firstOwner := environment.seedActor(t, false, 1)
	secondOwner := environment.seedActor(t, false, 1)
	firstEndpoint := environment.seedEndpoint(t, firstOwner.UserID, reportTestConnector, reportTestBaseURL)
	secondEndpoint := environment.seedEndpoint(t, secondOwner.UserID, reportTestConnector, reportTestBaseURL)
	environment.seedEndpointKey(t, firstEndpoint, "read-secret", reportTestNow)
	environment.seedEndpointKey(t, secondEndpoint, "read-secret", reportTestNow)
	environment.accept(t, "read-secret", "first note", 1100, 1, nil)
	environment.setNow(reportTestNow + 1)
	environment.accept(t, "read-secret", "second note", 1101, 2, nil)
	caseID := environment.caseIDForSecret(t, "read-secret")
	environment.runUntilIdle(t, 10)
	admin := environment.seedActor(t, true, 1)

	badge, err := environment.repository.Badge(context.Background(), admin)
	if err != nil {
		t.Fatal(err)
	}
	if badge.Total != "1" || badge.ByStatus["pending_indexing"] != "0" ||
		badge.ByStatus["pending_review"] != "1" || badge.ByStatus["approved_processing"] != "0" {
		t.Fatalf("badge=%+v", badge)
	}
	page, err := environment.repository.ListCases(context.Background(), admin, "pending_review", "", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Data) != 1 || page.Data[0].ID != caseID || page.Data[0].Counts.Materials != "2" ||
		page.Data[0].Counts.Targets != "2" || page.Data[0].Counts.DistinctOwners != "2" {
		t.Fatalf("case page=%+v", page)
	}

	detail, err := environment.repository.CaseDetail(context.Background(), admin, caseID, "", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.Materials.Data) != 1 || detail.Materials.Data[0].NoteText != "first note" ||
		detail.Materials.NextCursor == nil || detail.Materials.Data[0].Reporter != nil {
		t.Fatalf("first material page=%+v", detail.Materials)
	}
	if detail.Materials.Data[0].SourceIP != netip.AddrFrom16(reportTestIP(1)).String() {
		t.Fatalf("first source IP=%q", detail.Materials.Data[0].SourceIP)
	}
	secondDetail, err := environment.repository.CaseDetail(
		context.Background(), admin, caseID, *detail.Materials.NextCursor, 1,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(secondDetail.Materials.Data) != 1 || secondDetail.Materials.Data[0].NoteText != "second note" ||
		secondDetail.Materials.NextCursor != nil {
		t.Fatalf("second material page=%+v", secondDetail.Materials)
	}
	tamperedMaterialCursor := *detail.Materials.NextCursor
	if tamperedMaterialCursor[len(tamperedMaterialCursor)-1] == 'A' {
		tamperedMaterialCursor = tamperedMaterialCursor[:len(tamperedMaterialCursor)-1] + "B"
	} else {
		tamperedMaterialCursor = tamperedMaterialCursor[:len(tamperedMaterialCursor)-1] + "A"
	}
	if _, err := environment.repository.CaseDetail(context.Background(), admin, caseID, tamperedMaterialCursor, 1); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("tampered material cursor error=%v", err)
	}

	targets, err := environment.repository.Targets(context.Background(), admin, caseID, "", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(targets.Data) != 1 || targets.NextCursor == nil || targets.Data[0].Owner == nil ||
		targets.Data[0].Owner.DiscordID == "" || targets.Data[0].Owner.DisplayName == "" ||
		targets.Data[0].EndpointKeyID == nil || targets.Data[0].KeyRef == "" {
		t.Fatalf("first target page=%+v", targets)
	}
	secondTargets, err := environment.repository.Targets(context.Background(), admin, caseID, *targets.NextCursor, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(secondTargets.Data) != 1 || secondTargets.NextCursor != nil ||
		secondTargets.Data[0].TargetSequence == targets.Data[0].TargetSequence {
		t.Fatalf("second target page=%+v", secondTargets)
	}

	if _, err := environment.store.DB().Exec(`UPDATE report_cases SET
material_count=9007199254740993,target_count=9007199254740993,distinct_owner_count=9007199254740993
WHERE id=?`, caseID); err != nil {
		t.Fatal(err)
	}
	page, err = environment.repository.ListCases(context.Background(), admin, "pending_review", "", 1)
	if err != nil {
		t.Fatal(err)
	}
	if page.Data[0].Counts.Materials != "9007199254740993" || page.Data[0].Counts.Targets != "9007199254740993" ||
		page.Data[0].Counts.DistinctOwners != "9007199254740993" {
		t.Fatalf("unsafe integers were not emitted as strings: %+v", page.Data[0].Counts)
	}

	if _, err := environment.repository.CaseDetail(context.Background(), firstOwner, caseID, "", 1); !errors.Is(err, ErrForbidden) {
		t.Fatalf("ordinary user detail error=%v", err)
	}
	steward := environment.seedActor(t, false, 5)
	if _, err := environment.repository.Targets(context.Background(), steward, caseID, "", 1); !errors.Is(err, ErrForbidden) {
		t.Fatalf("L5 targets error=%v", err)
	}
}

func TestAdminFinalAuthorizationRechecksRoleAndCredentialGeneration(t *testing.T) {
	environment := newReportTestEnvironment(t)
	_, _, caseID := prepareReview(t, environment, "admin-final-authorization", 1)
	admin := environment.seedActor(t, true, 1)
	steward := environment.seedActor(t, false, 5)
	_, _, materialVersion, targetVersion := environment.caseState(t, caseID)
	command := RejectCommand{
		ExpectedMaterialVersion: materialVersion,
		ExpectedTargetVersion:   targetVersion,
		Reason:                  "confirmed invalid report",
		IdempotencyKey:          reportTestKey(1150),
	}

	if _, err := environment.repository.Reject(context.Background(), steward, caseID, command); !errors.Is(err, ErrForbidden) {
		t.Fatalf("steward mutation error=%v", err)
	}
	if got := environment.rowCount(t, `SELECT COUNT(*) FROM report_decisions WHERE case_id=?`, caseID); got != 0 {
		t.Fatalf("steward mutation decisions=%d", got)
	}
	if got := environment.rowCount(t, `SELECT COUNT(*) FROM idempotency_records WHERE scope='control_mutation'`); got != 0 {
		t.Fatalf("steward mutation replay rows=%d", got)
	}

	result, err := environment.repository.Reject(context.Background(), admin, caseID, command)
	if err != nil || result.Status != 200 || result.Replayed {
		t.Fatalf("authorized rejection result=%+v err=%v", result, err)
	}
	if _, err := environment.store.DB().Exec(`UPDATE sessions SET cred_gen='replacement-generation' WHERE token_hash=?`, admin.SessionTokenHash); err != nil {
		t.Fatal(err)
	}
	if _, err := environment.repository.Reject(context.Background(), admin, caseID, command); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("stale-generation replay error=%v", err)
	}
	if got := environment.rowCount(t, `SELECT COUNT(*) FROM report_decisions WHERE case_id=? AND action='reject'`, caseID); got != 1 {
		t.Fatalf("stale-generation replay decisions=%d", got)
	}
	if got := environment.rowCount(t, `SELECT COUNT(*) FROM idempotency_records WHERE scope='control_mutation'`); got != 1 {
		t.Fatalf("authorization failures changed replay rows=%d", got)
	}
}

func TestApproveFreezesDecisionCapturesLateTargetsAndUsesDeletionCapability(t *testing.T) {
	environment := newReportTestEnvironment(t)
	ownerUserID, keyIDs, caseID := prepareReview(t, environment, "approve-secret", 2)
	admin := environment.seedActor(t, true, 1)
	_, _, materialVersion, targetVersion := environment.caseState(t, caseID)

	_, err := environment.repository.Approve(context.Background(), admin, caseID, ApproveCommand{
		ExpectedMaterialVersion: materialVersion, ExpectedTargetVersion: targetVersion - 1,
		Reason: "stale target version", Confirmation: true, IdempotencyKey: reportTestKey(1200),
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("stale approval error=%v", err)
	}
	if got := environment.rowCount(t, `SELECT COUNT(*) FROM idempotency_records WHERE scope='control_mutation'`); got != 0 {
		t.Fatalf("stale approval persisted %d replay rows", got)
	}

	command := ApproveCommand{
		ExpectedMaterialVersion: materialVersion, ExpectedTargetVersion: targetVersion,
		Reason: "confirmed credential theft", Confirmation: true, IdempotencyKey: reportTestKey(1201),
	}
	result, err := environment.repository.Approve(context.Background(), admin, caseID, command)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != 202 || result.Value.Status != "approved_processing" || result.Replayed {
		t.Fatalf("approval result=%+v", result)
	}
	replayed, err := environment.repository.Approve(context.Background(), admin, caseID, command)
	if err != nil {
		t.Fatal(err)
	}
	if !replayed.Replayed || replayed.Status != result.Status || !bytes.Equal(replayed.Body, result.Body) {
		t.Fatalf("approval replay=%+v original=%+v", replayed, result)
	}
	different := command
	different.Reason = "different reason"
	if _, err := environment.repository.Approve(context.Background(), admin, caseID, different); !errors.Is(err, ErrConflict) {
		t.Fatalf("different approval replay error=%v", err)
	}
	if got := environment.rowCount(t, `SELECT COUNT(*) FROM report_decisions WHERE case_id=? AND action='approve'`, caseID); got != 1 {
		t.Fatalf("approval decision count=%d", got)
	}

	endpointID := environmentEndpointID(t, environment, ownerUserID)
	tx, err := environment.store.DB().BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	lateKeyID, err := environment.insertEndpointKeyTx(context.Background(), tx, endpointID, []byte("approve-secret"), environment.clock.Load())
	if err == nil {
		err = environment.repository.ProtectNewEndpointKey(context.Background(), tx, ownerUserID, lateKeyID, environment.clock.Load())
	}
	if err == nil {
		err = tx.Commit()
	} else {
		_ = tx.Rollback()
	}
	if err != nil {
		t.Fatalf("late target capture: %v", err)
	}
	keyIDs = append(keyIDs, lateKeyID)
	if got := environment.rowCount(t, `SELECT target_count FROM report_cases WHERE id=?`, caseID); got != 3 {
		t.Fatalf("target count after late key=%d", got)
	}
	environment.accept(t, "approve-secret", "ignored during approved processing", 1202, 2, nil)
	if got := environment.rowCount(t, `SELECT material_count FROM report_cases WHERE id=?`, caseID); got != 1 {
		t.Fatalf("approved processing material count=%d", got)
	}

	environment.runUntilIdle(t, 10)
	status, progress, _, finalTargetVersion := environment.caseState(t, caseID)
	if status != "approved" || progress != "complete" || finalTargetVersion <= targetVersion {
		t.Fatalf("approved state=%s progress=%s target_version=%d", status, progress, finalTargetVersion)
	}
	if got := environment.rowCount(t, `SELECT COUNT(*) FROM endpoint_keys WHERE id IN (?,?,?)`, keyIDs[0], keyIDs[1], keyIDs[2]); got != 0 {
		t.Fatalf("approved endpoint keys remaining=%d", got)
	}
	if got := environment.rowCount(t, `SELECT COUNT(*) FROM report_targets WHERE case_id=? AND state='deleted_by_approval'`, caseID); got != 3 {
		t.Fatalf("approved target count=%d", got)
	}
	if got := environment.rowCount(t, `SELECT COUNT(*) FROM endpoint_key_suspensions WHERE report_case_id=?`, caseID); got != 0 {
		t.Fatalf("approved suspensions remaining=%d", got)
	}
	if got := environment.rowCount(t, `SELECT COUNT(*) FROM accepted_operations WHERE checkpoint=?`, caseID); got != 0 {
		t.Fatalf("approved operations remaining=%d", got)
	}
	if environment.deletions.callCount() != 3 {
		t.Fatalf("deletion capability calls=%d", environment.deletions.callCount())
	}
	if got := environment.rowCount(t, `SELECT COUNT(*) FROM endpoint_key_secrets WHERE orphaned_at IS NOT NULL`); got != 3 {
		t.Fatalf("orphaned secret rows=%d", got)
	}
}

func environmentEndpointID(t *testing.T, environment *reportTestEnvironment, ownerUserID int64) int64 {
	t.Helper()
	var endpointID int64
	if err := environment.store.DB().QueryRow(`SELECT id FROM endpoints WHERE user_id=?
ORDER BY id LIMIT 1`, ownerUserID).Scan(&endpointID); err != nil {
		t.Fatal(err)
	}
	return endpointID
}

func TestRejectReleasesOnlyReportProtectionAndPreservesKeyState(t *testing.T) {
	environment := newReportTestEnvironment(t)
	owner := environment.seedActor(t, false, 1)
	endpointID := environment.seedEndpoint(t, owner.UserID, reportTestConnector, reportTestBaseURL)
	keyID := environment.seedEndpointKey(t, endpointID, "reject-secret", reportTestNow)
	if _, err := environment.store.DB().Exec(`UPDATE endpoint_keys SET enabled=0 WHERE id=?`, keyID); err != nil {
		t.Fatal(err)
	}
	environment.accept(t, "reject-secret", "review", 1300, 1, nil)
	caseID := environment.caseIDForSecret(t, "reject-secret")
	environment.runUntilIdle(t, 10)
	admin := environment.seedActor(t, true, 1)
	_, _, materialVersion, targetVersion := environment.caseState(t, caseID)
	command := RejectCommand{
		ExpectedMaterialVersion: materialVersion, ExpectedTargetVersion: targetVersion,
		Reason: "report did not establish compromise", IdempotencyKey: reportTestKey(1301),
	}
	result, err := environment.repository.Reject(context.Background(), admin, caseID, command)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != 200 || result.Value.Status != "rejected" {
		t.Fatalf("rejection result=%+v", result)
	}
	replay, err := environment.repository.Reject(context.Background(), admin, caseID, command)
	if err != nil || !replay.Replayed || !bytes.Equal(replay.Body, result.Body) {
		t.Fatalf("rejection replay=%+v err=%v", replay, err)
	}
	var enabled int
	if err := environment.store.DB().QueryRow(`SELECT enabled FROM endpoint_keys WHERE id=?`, keyID).Scan(&enabled); err != nil {
		t.Fatal(err)
	}
	if enabled != 0 {
		t.Fatalf("rejection restored enabled=%d", enabled)
	}
	if got := environment.rowCount(t, `SELECT COUNT(*) FROM endpoint_key_suspensions WHERE endpoint_key_id=?`, keyID); got != 0 {
		t.Fatalf("report suspension remaining=%d", got)
	}
	if got := environment.rowCount(t, `SELECT COUNT(*) FROM report_targets WHERE case_id=? AND state='released'`, caseID); got != 1 {
		t.Fatalf("released target count=%d", got)
	}
}

func TestReportDeadlineCutoffAndNoTerminalCooldown(t *testing.T) {
	t.Run("material before deadline merges", func(t *testing.T) {
		environment := newReportTestEnvironment(t)
		_, _, caseID := prepareReview(t, environment, "deadline-before", 1)
		var deadline int64
		if err := environment.store.DB().QueryRow(`SELECT deadline FROM report_cases WHERE id=?`, caseID).Scan(&deadline); err != nil {
			t.Fatal(err)
		}
		environment.setNow(deadline - 1)
		environment.accept(t, "deadline-before", "new material", 1400, 2, nil)
		if got := environment.rowCount(t, `SELECT COUNT(*) FROM report_cases`); got != 1 {
			t.Fatalf("case count=%d", got)
		}
		if got := environment.rowCount(t, `SELECT material_count FROM report_cases WHERE id=?`, caseID); got != 2 {
			t.Fatalf("material count=%d", got)
		}
		var unchangedDeadline int64
		if err := environment.store.DB().QueryRow(`SELECT deadline FROM report_cases WHERE id=?`, caseID).Scan(&unchangedDeadline); err != nil {
			t.Fatal(err)
		}
		if unchangedDeadline != deadline {
			t.Fatalf("deadline refreshed from %d to %d", deadline, unchangedDeadline)
		}
	})

	t.Run("submission at deadline expires old case and creates new", func(t *testing.T) {
		environment := newReportTestEnvironment(t)
		_, _, oldCaseID := prepareReview(t, environment, "deadline-equal", 1)
		var deadline int64
		if err := environment.store.DB().QueryRow(`SELECT deadline FROM report_cases WHERE id=?`, oldCaseID).Scan(&deadline); err != nil {
			t.Fatal(err)
		}
		environment.setNow(deadline)
		environment.accept(t, "deadline-equal", "fresh case", 1410, 2, nil)
		var oldStatus string
		if err := environment.store.DB().QueryRow(`SELECT status FROM report_cases WHERE id=?`, oldCaseID).Scan(&oldStatus); err != nil {
			t.Fatal(err)
		}
		if oldStatus != "expired" {
			t.Fatalf("old status=%s", oldStatus)
		}
		if got := environment.rowCount(t, `SELECT COUNT(*) FROM report_cases`); got != 2 {
			t.Fatalf("case count=%d", got)
		}
		if got := environment.rowCount(t, `SELECT COUNT(*) FROM report_cases
WHERE status IN ('pending_indexing','pending_review','approved_processing')`); got != 1 {
			t.Fatalf("active case count=%d", got)
		}
		if got := environment.rowCount(t, `SELECT COUNT(*) FROM report_targets
WHERE case_id=? AND state='released'`, oldCaseID); got != 1 {
			t.Fatalf("old released targets=%d", got)
		}
		if got := environment.rowCount(t, `SELECT COUNT(*) FROM endpoint_key_suspensions
WHERE report_case_id<>?`, oldCaseID); got != 1 {
			t.Fatalf("new-case suspensions=%d", got)
		}
	})
}

func TestReportPendingTTLDefaultMaximumAndDecisionCutoff(t *testing.T) {
	t.Run("default and maximum", func(t *testing.T) {
		for _, testCase := range []struct {
			name string
			ttl  int64
		}{
			{name: "default", ttl: 24 * 60 * 60},
			{name: "maximum", ttl: 72 * 60 * 60},
		} {
			t.Run(testCase.name, func(t *testing.T) {
				environment := newReportTestEnvironment(t)
				if testCase.name == "maximum" {
					if _, err := environment.store.DB().Exec(`UPDATE site_config SET value=?
WHERE key='report_pending_ttl_seconds'`, testCase.ttl); err != nil {
						t.Fatal(err)
					}
				}
				owner := environment.seedActor(t, false, 1)
				endpointID := environment.seedEndpoint(t, owner.UserID, reportTestConnector, reportTestBaseURL)
				environment.seedEndpointKey(t, endpointID, "ttl-"+testCase.name, reportTestNow)
				environment.accept(t, "ttl-"+testCase.name, "", 1450, 1, nil)
				caseID := environment.caseIDForSecret(t, "ttl-"+testCase.name)
				var createdAt, deadline int64
				if err := environment.store.DB().QueryRow(`SELECT created_at,deadline FROM report_cases WHERE id=?`, caseID).Scan(
					&createdAt, &deadline,
				); err != nil {
					t.Fatal(err)
				}
				if deadline != createdAt+testCase.ttl {
					t.Fatalf("deadline=%d created=%d ttl=%d", deadline, createdAt, testCase.ttl)
				}
			})
		}
	})

	for _, action := range []string{"approve", "reject"} {
		t.Run(action+" at deadline", func(t *testing.T) {
			environment := newReportTestEnvironment(t)
			_, _, caseID := prepareReview(t, environment, "decision-cutoff-"+action, 1)
			var deadline int64
			if err := environment.store.DB().QueryRow(`SELECT deadline FROM report_cases WHERE id=?`, caseID).Scan(&deadline); err != nil {
				t.Fatal(err)
			}
			environment.setNow(deadline)
			admin := environment.seedActor(t, true, 1)
			_, _, materialVersion, targetVersion := environment.caseState(t, caseID)
			var err error
			if action == "approve" {
				_, err = environment.repository.Approve(context.Background(), admin, caseID, ApproveCommand{
					ExpectedMaterialVersion: materialVersion,
					ExpectedTargetVersion:   targetVersion,
					Reason:                  "too late approval",
					Confirmation:            true,
					IdempotencyKey:          reportTestKey(1460),
				})
			} else {
				_, err = environment.repository.Reject(context.Background(), admin, caseID, RejectCommand{
					ExpectedMaterialVersion: materialVersion,
					ExpectedTargetVersion:   targetVersion,
					Reason:                  "too late rejection",
					IdempotencyKey:          reportTestKey(1461),
				})
			}
			if !errors.Is(err, ErrConflict) {
				t.Fatalf("deadline %s error=%v", action, err)
			}
			if got := environment.rowCount(t, `SELECT COUNT(*) FROM idempotency_records
WHERE scope='control_mutation'`); got != 0 {
				t.Fatalf("deadline %s retained replay rows=%d", action, got)
			}
			environment.runUntilIdle(t, 3)
			status, progress, _, _ := environment.caseState(t, caseID)
			if status != "expired" || progress != "complete" {
				t.Fatalf("deadline %s final state=%s/%s", action, status, progress)
			}
		})
	}
}

func TestDecisionReasonValidationDoesNotMutateCase(t *testing.T) {
	environment := newReportTestEnvironment(t)
	_, _, caseID := prepareReview(t, environment, "reason-validation", 1)
	admin := environment.seedActor(t, true, 1)
	_, _, materialVersion, targetVersion := environment.caseState(t, caseID)
	invalidReasons := []string{"", "   ", "line\nfeed", string([]byte{0x7f}), string(bytes.Repeat([]byte{'x'}, maxNoteBytes+1))}
	for index, reason := range invalidReasons {
		if _, err := environment.repository.Approve(context.Background(), admin, caseID, ApproveCommand{
			ExpectedMaterialVersion: materialVersion,
			ExpectedTargetVersion:   targetVersion,
			Reason:                  reason,
			Confirmation:            true,
			IdempotencyKey:          reportTestKey(1470 + index),
		}); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("approve reason %d error=%v", index, err)
		}
		if _, err := environment.repository.Reject(context.Background(), admin, caseID, RejectCommand{
			ExpectedMaterialVersion: materialVersion,
			ExpectedTargetVersion:   targetVersion,
			Reason:                  reason,
			IdempotencyKey:          reportTestKey(1480 + index),
		}); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("reject reason %d error=%v", index, err)
		}
	}
	if _, err := environment.repository.Approve(context.Background(), admin, caseID, ApproveCommand{
		ExpectedMaterialVersion: materialVersion,
		ExpectedTargetVersion:   targetVersion,
		Reason:                  "valid but unconfirmed",
		Confirmation:            false,
		IdempotencyKey:          reportTestKey(1490),
	}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("unconfirmed approval error=%v", err)
	}
	for action, mutate := range map[string]func() error{
		"approve": func() error {
			_, err := environment.repository.Approve(context.Background(), admin, caseID, ApproveCommand{
				ExpectedMaterialVersion: materialVersion,
				ExpectedTargetVersion:   targetVersion,
				Reason:                  "valid approval reason",
				Confirmation:            true,
				IdempotencyKey:          "too-short",
			})
			return err
		},
		"reject": func() error {
			_, err := environment.repository.Reject(context.Background(), admin, caseID, RejectCommand{
				ExpectedMaterialVersion: materialVersion,
				ExpectedTargetVersion:   targetVersion,
				Reason:                  "valid rejection reason",
				IdempotencyKey:          "too-short",
			})
			return err
		},
		"resume": func() error {
			_, err := environment.repository.Resume(context.Background(), admin, caseID, ResumeCommand{
				ExpectedTargetVersion: targetVersion,
				IdempotencyKey:        "too-short",
			})
			return err
		},
	} {
		if err := mutate(); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("invalid-key %s error=%v", action, err)
		}
	}
	status, progress, currentMaterial, currentTarget := environment.caseState(t, caseID)
	if status != "pending_review" || progress != "complete" || currentMaterial != materialVersion || currentTarget != targetVersion {
		t.Fatalf("case changed after invalid reasons: %s/%s/%d/%d", status, progress, currentMaterial, currentTarget)
	}
	if got := environment.rowCount(t, `SELECT COUNT(*) FROM report_decisions WHERE case_id=?`, caseID); got != 0 {
		t.Fatalf("invalid reason decisions=%d", got)
	}
	if got := environment.rowCount(t, `SELECT COUNT(*) FROM idempotency_records WHERE scope='control_mutation'`); got != 0 {
		t.Fatalf("invalid reason replay rows=%d", got)
	}
}

func TestProtectNewEndpointKeyDeadlineOrdering(t *testing.T) {
	t.Run("before deadline joins union", func(t *testing.T) {
		environment := newReportTestEnvironment(t)
		ownerUserID, _, caseID := prepareReview(t, environment, "late-before", 1)
		var deadline int64
		if err := environment.store.DB().QueryRow(`SELECT deadline FROM report_cases WHERE id=?`, caseID).Scan(&deadline); err != nil {
			t.Fatal(err)
		}
		endpointID := environmentEndpointID(t, environment, ownerUserID)
		tx, err := environment.store.DB().BeginTx(context.Background(), nil)
		if err != nil {
			t.Fatal(err)
		}
		lateKeyID, err := environment.insertEndpointKeyTx(context.Background(), tx,
			endpointID, []byte("late-before"), deadline-1)
		if err == nil {
			err = environment.repository.ProtectNewEndpointKey(context.Background(), tx, ownerUserID, lateKeyID, deadline-1)
		}
		if err == nil {
			err = tx.Commit()
		} else {
			_ = tx.Rollback()
		}
		if err != nil {
			t.Fatal(err)
		}
		if got := environment.rowCount(t, `SELECT target_count FROM report_cases WHERE id=?`, caseID); got != 2 {
			t.Fatalf("target count=%d", got)
		}
		if got := environment.rowCount(t, `SELECT COUNT(*) FROM endpoint_key_suspensions WHERE endpoint_key_id=?`, lateKeyID); got != 1 {
			t.Fatalf("late-key suspension=%d", got)
		}
	})

	t.Run("at deadline expires without absorbing new key", func(t *testing.T) {
		environment := newReportTestEnvironment(t)
		ownerUserID, _, caseID := prepareReview(t, environment, "late-equal", 1)
		var deadline int64
		if err := environment.store.DB().QueryRow(`SELECT deadline FROM report_cases WHERE id=?`, caseID).Scan(&deadline); err != nil {
			t.Fatal(err)
		}
		endpointID := environmentEndpointID(t, environment, ownerUserID)
		tx, err := environment.store.DB().BeginTx(context.Background(), nil)
		if err != nil {
			t.Fatal(err)
		}
		lateKeyID, err := environment.insertEndpointKeyTx(context.Background(), tx,
			endpointID, []byte("late-equal"), deadline)
		if err == nil {
			err = environment.repository.ProtectNewEndpointKey(context.Background(), tx, ownerUserID, lateKeyID, deadline)
		}
		if err == nil {
			err = tx.Commit()
		} else {
			_ = tx.Rollback()
		}
		if err != nil {
			t.Fatal(err)
		}
		status, _, _, _ := environment.caseState(t, caseID)
		if status != "expired" {
			t.Fatalf("case status=%s", status)
		}
		if got := environment.rowCount(t, `SELECT COUNT(*) FROM report_targets
WHERE case_id=? AND endpoint_key_id=?`, caseID, lateKeyID); got != 0 {
			t.Fatalf("deadline key absorbed into old case: %d", got)
		}
		if got := environment.rowCount(t, `SELECT COUNT(*) FROM endpoint_key_suspensions WHERE endpoint_key_id=?`, lateKeyID); got != 0 {
			t.Fatalf("deadline key suspended=%d", got)
		}
		environment.runUntilIdle(t, 10)
		if got := environment.rowCount(t, `SELECT COUNT(*) FROM report_targets
WHERE case_id=? AND endpoint_key_id=?`, caseID, lateKeyID); got != 0 {
			t.Fatalf("deadline key absorbed by expiry worker: %d", got)
		}
	})
}

func TestPendingIndexingExpiryCompletesReleasedProjection(t *testing.T) {
	environment := newReportTestEnvironment(t)
	owner := environment.seedActor(t, false, 1)
	endpointID := environment.seedEndpoint(t, owner.UserID, reportTestConnector, reportTestBaseURL)
	environment.seedEndpointKey(t, endpointID, "expiry-secret", reportTestNow)
	environment.accept(t, "expiry-secret", "", 1500, 1, nil)
	caseID := environment.caseIDForSecret(t, "expiry-secret")
	var deadline int64
	if err := environment.store.DB().QueryRow(`SELECT deadline FROM report_cases WHERE id=?`, caseID).Scan(&deadline); err != nil {
		t.Fatal(err)
	}
	environment.setNow(deadline)
	environment.runUntilIdle(t, 10)
	status, progress, _, _ := environment.caseState(t, caseID)
	if status != "expired" || progress != "complete" {
		t.Fatalf("expiry status=%s progress=%s", status, progress)
	}
	if got := environment.rowCount(t, `SELECT COUNT(*) FROM report_targets
WHERE case_id=? AND state='released'`, caseID); got != 1 {
		t.Fatalf("released target count=%d", got)
	}
	if got := environment.rowCount(t, `SELECT COUNT(*) FROM endpoint_key_suspensions WHERE report_case_id=?`, caseID); got != 0 {
		t.Fatalf("expiry suspensions=%d", got)
	}
	if got := environment.rowCount(t, `SELECT COUNT(*) FROM report_decisions WHERE case_id=? AND action='expire'`, caseID); got != 1 {
		t.Fatalf("expiry decisions=%d", got)
	}
}

func TestDecisionResponseJSONUsesStringVersions(t *testing.T) {
	response := DecisionResponse{ID: reportTestOpaqueID("rpc_", 1), Status: "approved_processing", MaterialVersion: "9007199254740993", TargetVersion: "9223372036854775807"}
	body, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(body, []byte(`"material_version":"9007199254740993"`)) ||
		!bytes.Contains(body, []byte(`"target_version":"9223372036854775807"`)) {
		t.Fatalf("decision JSON=%s", body)
	}
}
