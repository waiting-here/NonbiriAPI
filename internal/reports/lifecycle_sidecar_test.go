package reports

import (
	"context"
	"testing"
	"time"
)

func TestReportLifecycleRecoveryUsesBoundedFrozenPass(t *testing.T) {
	environment := newReportTestEnvironment(t)
	owner := environment.seedActor(t, false, 1)
	endpointID := environment.seedEndpoint(t, owner.UserID, reportTestConnector, reportTestBaseURL)
	environment.seedEndpointKey(t, endpointID, "lifecycle-recovery", reportTestNow)
	environment.accept(t, "lifecycle-recovery", "", 4100, 1, nil)
	caseID := environment.caseIDForSecret(t, "lifecycle-recovery")
	if _, err := environment.store.DB().Exec(`UPDATE accepted_operations SET state='running'
WHERE checkpoint=? AND kind='report_indexing' AND state='accepted'`, caseID); err != nil {
		t.Fatalf("seed running report operation: %v", err)
	}

	environment.setNow(reportTestNow + 10_000)
	result, err := environment.repository.RecoverLifecycle(
		context.Background(), reportTestNow, 1, time.Now().Add(time.Second),
	)
	if err != nil || result != (LifecycleWorkResult{Processed: 1, More: true}) {
		t.Fatalf("RecoverLifecycle: result=%+v err=%v", result, err)
	}
	var state string
	if err := environment.store.DB().QueryRow(`SELECT state FROM accepted_operations
WHERE checkpoint=? AND kind='report_indexing'`, caseID).Scan(&state); err != nil {
		t.Fatalf("read recovered report operation: %v", err)
	}
	if state != "accepted" {
		t.Fatalf("recovered operation state=%q, want accepted", state)
	}
}

func TestReportLifecycleRetentionUsesDecisionNowAndAggregateHold(t *testing.T) {
	t.Run("frozen retention time", func(t *testing.T) {
		environment := newReportTestEnvironment(t)
		_, _, caseID := prepareReview(t, environment, "lifecycle-retention", 1)
		admin := environment.seedActor(t, true, 1)
		_, _, materialVersion, targetVersion := environment.caseState(t, caseID)
		if _, err := environment.repository.Reject(context.Background(), admin, caseID, RejectCommand{
			ExpectedMaterialVersion: materialVersion,
			ExpectedTargetVersion:   targetVersion,
			Reason:                  "lifecycle retention fixture",
			IdempotencyKey:          reportTestKey(4110),
		}); err != nil {
			t.Fatalf("Reject: %v", err)
		}
		boundary := reportTestNow + caseRetentionSeconds
		environment.setNow(boundary + 100)
		if _, err := environment.repository.RetainLifecycle(
			context.Background(), boundary-1, 100, time.Now().Add(time.Second),
		); err != nil {
			t.Fatalf("RetainLifecycle before boundary: %v", err)
		}
		if got := environment.rowCount(t, `SELECT COUNT(*) FROM report_cases WHERE id=?`, caseID); got != 1 {
			t.Fatalf("case retained before frozen boundary=%d", got)
		}
		for pass := 0; pass < 4; pass++ {
			result, err := environment.repository.RetainLifecycle(
				context.Background(), boundary, 100, time.Now().Add(time.Second),
			)
			if err != nil {
				t.Fatalf("RetainLifecycle at boundary pass %d: %v", pass, err)
			}
			if !result.More {
				break
			}
		}
		if got := environment.rowCount(t, `SELECT COUNT(*) FROM report_cases WHERE id=?`, caseID); got != 0 {
			t.Fatalf("case retained at frozen boundary=%d", got)
		}
	})

	t.Run("held aggregate root and irreversible marker", func(t *testing.T) {
		environment := newReportTestEnvironment(t)
		_, _, caseID := prepareReview(t, environment, "lifecycle-held-root", 1)
		admin := environment.seedActor(t, true, 1)
		_, _, materialVersion, targetVersion := environment.caseState(t, caseID)
		if _, err := environment.repository.Reject(context.Background(), admin, caseID, RejectCommand{
			ExpectedMaterialVersion: materialVersion,
			ExpectedTargetVersion:   targetVersion,
			Reason:                  "lifecycle hold fixture",
			IdempotencyKey:          reportTestKey(4120),
		}); err != nil {
			t.Fatalf("Reject: %v", err)
		}
		tx, err := environment.store.DB().BeginTx(context.Background(), nil)
		if err != nil {
			t.Fatalf("begin held report transaction: %v", err)
		}
		defer tx.Rollback()
		state, err := environment.repository.InspectLifecycleHeldCase(
			context.Background(), tx, caseID, reportTestNow+1,
		)
		if err != nil || !state.Exists || state.LegalHoldConsumed ||
			state.OrdinaryDeadline != reportTestNow+caseRetentionSeconds {
			t.Fatalf("initial held report state=%+v err=%v", state, err)
		}
		holdID := reportTestOpaqueID("lgh_", 4120)
		if _, err := tx.Exec(`INSERT INTO legal_holds(
id,object_kind,object_ref,state,revision,basis,created_by_user_id,created_at,expires_at)
VALUES(?,'report_case',?,'active',1,'lifecycle fixture',?,?,?)`,
			holdID, caseID, admin.UserID, reportTestNow+1, reportTestNow+3600); err != nil {
			t.Fatalf("insert report legal hold: %v", err)
		}
		if err := environment.repository.ConsumeLifecycleHeldCaseMarker(
			context.Background(), tx, caseID,
		); err != nil {
			t.Fatalf("ConsumeLifecycleHeldCaseMarker: %v", err)
		}
		exists, err := environment.repository.ReadLifecycleHeldCase(
			context.Background(), tx, caseID, reportTestNow+1,
		)
		if err != nil || !exists {
			t.Fatalf("ReadLifecycleHeldCase: exists=%v err=%v", exists, err)
		}
		state, err = environment.repository.InspectLifecycleHeldCase(
			context.Background(), tx, caseID, reportTestNow+1,
		)
		if err != nil || !state.LegalHoldConsumed {
			t.Fatalf("consumed held report state=%+v err=%v", state, err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatalf("commit held report transaction: %v", err)
		}
	})
}
