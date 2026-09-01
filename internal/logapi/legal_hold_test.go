package logapi

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/db"
)

func insertRequestLogLegalHold(
	t *testing.T,
	database *sql.DB,
	requestLogID, adminUserID, createdAt int64,
) {
	t.Helper()
	holdID, err := db.GenerateOpaqueID("lgh_")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO legal_holds(
id,object_kind,object_ref,state,revision,basis,created_by_user_id,created_at,expires_at
) VALUES(?,'request_log',?,'active',1,'request log hold',?,?,?)`,
		holdID, fmt.Sprintf("%d", requestLogID), adminUserID, createdAt, createdAt+3600); err != nil {
		t.Fatalf("insert request-log legal hold: %v", err)
	}
}

func TestHeldRequestLogDeadlineMarkerAggregateAndDeidentification(t *testing.T) {
	fixture := newLifecycleLogDatabase(t)
	userID := seedLifecycleLogUser(t, fixture.store.DB(), "held-log-owner", false)
	adminID := seedLifecycleLogUser(t, fixture.store.DB(), "", true)
	completedAt := int64(1_700_000_100)
	terminalRequestID, err := db.GenerateOpaqueID("req_")
	if err != nil {
		t.Fatal(err)
	}
	pendingRequestID, err := db.GenerateOpaqueID("req_")
	if err != nil {
		t.Fatal(err)
	}
	terminalLogID := insertLifecycleRequestLog(t, fixture.store.DB(), terminalRequestID, userID,
		string(ResultSuccess), 200, nil, completedAt-10, completedAt, 10, 0)
	pendingLogID := insertLifecycleRequestLog(t, fixture.store.DB(), pendingRequestID, userID,
		nil, nil, nil, completedAt+1, nil, 0, 0)
	insertLifecycleAttempt(t, fixture.store.DB(), terminalLogID, completedAt-9, completedAt-8)
	deadline := completedAt + requestLogRetentionSeconds
	insertRequestLogLegalHold(t, fixture.store.DB(), terminalLogID, adminID, deadline-10)

	tx, err := fixture.store.DB().BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	ref := fmt.Sprintf("%d", terminalLogID)
	for _, delta := range []int64{-1, 0, 1} {
		decisionNow := deadline + delta
		state, err := fixture.repo.InspectForCreate(context.Background(), tx, ref, decisionNow)
		if err != nil {
			_ = tx.Rollback()
			t.Fatalf("inspect request log at deadline%+d: %v", delta, err)
		}
		if !state.Exists || state.OrdinaryDeadline != deadline || state.LegalHoldConsumed {
			_ = tx.Rollback()
			t.Fatalf("request-log state at deadline%+d = %+v", delta, state)
		}
		summary, err := fixture.repo.ExportLifecycleSummary(context.Background(), tx, userID, decisionNow, 10)
		if err != nil {
			_ = tx.Rollback()
			t.Fatalf("ordinary request-log export at deadline%+d: %v", delta, err)
		}
		want := "1"
		if delta < 0 {
			want = "2"
		}
		if summary.TotalLogs != want {
			_ = tx.Rollback()
			t.Fatalf("ordinary request-log count at deadline%+d = %s, want %s", delta, summary.TotalLogs, want)
		}
	}
	pendingState, err := fixture.repo.InspectForCreate(
		context.Background(), tx, fmt.Sprintf("%d", pendingLogID), deadline,
	)
	if err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if !pendingState.Exists || pendingState.OrdinaryDeadline != maxUnixSecond {
		_ = tx.Rollback()
		t.Fatalf("pending request-log state = %+v", pendingState)
	}
	if err := fixture.repo.ConsumeMarker(context.Background(), tx, ref); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := fixture.repo.ConsumeMarker(context.Background(), tx, ref); !errors.Is(err, ErrConflict) {
		_ = tx.Rollback()
		t.Fatalf("second request-log marker consumption = %v, want conflict", err)
	}
	if _, err := tx.Exec(`DELETE FROM request_attempts WHERE request_log_id=?`, terminalLogID); err == nil {
		_ = tx.Rollback()
		t.Fatal("active request-log hold allowed attempt deletion")
	}
	if err := fixture.repo.PrepareLifecycleAccountDeletion(context.Background(), tx, userID, deadline); err != nil {
		_ = tx.Rollback()
		t.Fatalf("prepare held request-log deletion: %v", err)
	}
	if _, err := tx.Exec(`UPDATE logical_requests SET user_id=NULL WHERE id=?`, terminalRequestID); err != nil {
		_ = tx.Rollback()
		t.Fatalf("deidentify held request log: %v", err)
	}
	exists, err := fixture.repo.ReadHeld(context.Background(), tx, ref, deadline)
	if err != nil || !exists {
		_ = tx.Rollback()
		t.Fatalf("read deidentified held request log = %v, %v", exists, err)
	}
	exists, err = fixture.repo.ReadHeld(context.Background(), tx, "9223372036854775807", deadline)
	if err != nil || exists {
		_ = tx.Rollback()
		t.Fatalf("read missing held request log = %v, %v", exists, err)
	}
	var projectedUser sql.NullInt64
	var roots, attempts, pendingRoots, consumed int
	if err := tx.QueryRow(`SELECT user_id,legal_hold_consumed FROM request_logs WHERE id=?`, terminalLogID).Scan(
		&projectedUser, &consumed,
	); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.QueryRow(`SELECT COUNT(*) FROM request_logs WHERE id=?`, terminalLogID).Scan(&roots); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.QueryRow(`SELECT COUNT(*) FROM request_attempts WHERE request_log_id=?`, terminalLogID).Scan(&attempts); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.QueryRow(`SELECT COUNT(*) FROM request_logs WHERE id=?`, pendingLogID).Scan(&pendingRoots); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if projectedUser.Valid || consumed != 1 || roots != 1 || attempts != 1 || pendingRoots != 0 {
		_ = tx.Rollback()
		t.Fatalf("held log user=%+v consumed=%d roots=%d attempts=%d pending=%d",
			projectedUser, consumed, roots, attempts, pendingRoots)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}
