package reports

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"testing"
)

type reportIssueProjectionObservation struct {
	ownerUserID   int64
	endpointKeyID int64
	active        bool
}

type failingReportIssueProjection struct{ err error }

func (projection failingReportIssueProjection) ReconcileReportReason(context.Context, *sql.Tx, int64, int64) error {
	return projection.err
}

type recordingReportIssueProjection struct {
	mu           sync.Mutex
	observations []reportIssueProjectionObservation
}

func (projection *recordingReportIssueProjection) ReconcileReportReason(
	ctx context.Context,
	tx *sql.Tx,
	ownerUserID, endpointKeyID int64,
) error {
	var active int
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(
 SELECT 1 FROM endpoint_key_suspensions
 WHERE endpoint_key_id=? AND reason_type='report_case'
)`, endpointKeyID).Scan(&active); err != nil {
		return err
	}
	projection.mu.Lock()
	projection.observations = append(projection.observations, reportIssueProjectionObservation{
		ownerUserID: ownerUserID, endpointKeyID: endpointKeyID, active: active == 1,
	})
	projection.mu.Unlock()
	return nil
}

func (projection *recordingReportIssueProjection) snapshot() []reportIssueProjectionObservation {
	projection.mu.Lock()
	defer projection.mu.Unlock()
	return append([]reportIssueProjectionObservation(nil), projection.observations...)
}

func TestReportIssueProjectionTracksOnlyAggregateReasonTransitions(t *testing.T) {
	projection := &recordingReportIssueProjection{}
	environment := newReportTestEnvironmentWith(t, reportTestOptions{issueProjection: projection})
	owner := environment.seedActor(t, false, 0)
	endpointID := environment.seedEndpoint(t, owner.UserID, reportTestConnector, reportTestBaseURL)
	keyID := environment.seedEndpointKey(t, endpointID, "projection-secret", reportTestNow)

	environment.accept(t, "projection-secret", "first", 1, 1, nil)
	if got := projection.snapshot(); len(got) != 1 || got[0] != (reportIssueProjectionObservation{
		ownerUserID: owner.UserID, endpointKeyID: keyID, active: true,
	}) {
		t.Fatalf("first report-reason transition = %#v", got)
	}

	// A second accepted material for the same active aggregate reason must not
	// manufacture another issue occurrence.
	environment.accept(t, "projection-secret", "second", 2, 2, nil)
	if got := projection.snapshot(); len(got) != 1 {
		t.Fatalf("unchanged active reason triggered projection = %#v", got)
	}

	firstCaseID := environment.caseIDForSecret(t, "projection-secret")
	var firstDeadline int64
	if err := environment.store.DB().QueryRow(`SELECT deadline FROM report_cases WHERE id=?`, firstCaseID).Scan(&firstDeadline); err != nil {
		t.Fatalf("read first report deadline: %v", err)
	}

	// At the exact deadline, acceptance expires the old case and creates the
	// replacement in one transaction. Aggregate true->true must remain silent.
	environment.setNow(firstDeadline)
	environment.accept(t, "projection-secret", "replacement", 3, 3, nil)
	if got := projection.snapshot(); len(got) != 1 {
		t.Fatalf("same-transaction reason replacement triggered projection = %#v", got)
	}
	var firstStatus string
	if err := environment.store.DB().QueryRow(`SELECT status FROM report_cases WHERE id=?`, firstCaseID).Scan(&firstStatus); err != nil || firstStatus != "expired" {
		t.Fatalf("first case status = %q, %v", firstStatus, err)
	}

	var replacementID string
	var replacementDeadline int64
	if err := environment.store.DB().QueryRow(`
SELECT id,deadline FROM report_cases
WHERE fingerprint=(SELECT fingerprint FROM report_cases WHERE id=?)
  AND status IN ('pending_indexing','pending_review','approved_processing')
ORDER BY created_at DESC,id DESC LIMIT 1`, firstCaseID).Scan(&replacementID, &replacementDeadline); err != nil {
		t.Fatalf("read replacement report case: %v", err)
	}
	if replacementID == firstCaseID {
		t.Fatal("deadline replacement reused the expired report case")
	}

	environment.setNow(replacementDeadline)
	environment.runUntilIdle(t, 10)
	got := projection.snapshot()
	if len(got) != 2 || got[1] != (reportIssueProjectionObservation{
		ownerUserID: owner.UserID, endpointKeyID: keyID, active: false,
	}) {
		t.Fatalf("final report-reason transition = %#v", got)
	}
	if count := environment.rowCount(t, `SELECT COUNT(*) FROM endpoint_key_suspensions WHERE endpoint_key_id=?`, keyID); count != 0 {
		t.Fatalf("terminal report reason count = %d", count)
	}
}

func TestReportIssueProjectionFailureRollsBackAcceptance(t *testing.T) {
	projectionErr := errors.New("projection unavailable")
	environment := newReportTestEnvironmentWith(t, reportTestOptions{
		issueProjection: failingReportIssueProjection{err: projectionErr},
	})
	owner := environment.seedActor(t, false, 0)
	endpointID := environment.seedEndpoint(t, owner.UserID, reportTestConnector, reportTestBaseURL)
	environment.seedEndpointKey(t, endpointID, "rollback-secret", reportTestNow)

	err := environment.repository.AcceptCredentialTheft(
		context.Background(), environment.submission("rollback-secret", "rollback", 20, 20, nil),
	)
	if !errors.Is(err, projectionErr) {
		t.Fatalf("acceptance projection failure = %v", err)
	}
	for table, query := range map[string]string{
		"case":        `SELECT COUNT(*) FROM report_cases`,
		"reason":      `SELECT COUNT(*) FROM endpoint_key_suspensions`,
		"replay":      `SELECT COUNT(*) FROM idempotency_records WHERE scope='credential_report'`,
		"rate bucket": `SELECT COUNT(*) FROM report_rate_buckets`,
	} {
		if count := environment.rowCount(t, query); count != 0 {
			t.Fatalf("%s rows after projection rollback = %d", table, count)
		}
	}
}
