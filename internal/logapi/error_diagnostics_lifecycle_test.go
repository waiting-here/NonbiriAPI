package logapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/diagnostic"
	"github.com/waiting-here/NonbiriAPI/internal/observability"
)

func TestFailureFactsFollowAccountRetirementRetentionAndHold(t *testing.T) {
	f := newLifecycleLogDatabase(t)
	owner := seedLifecycleLogUser(t, f.store.DB(), "diagnostic-lifecycle", false)
	admin := seedLifecycleLogUser(t, f.store.DB(), "", true)
	now := int64(1_900_000_000)
	f.repo.now = func() time.Time { return time.Unix(now, 0) }
	recorder, err := observability.NewRepository(f.store.DB())
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]string, 2)
	for i := range ids {
		ids[i], err = db.GenerateOpaqueID("req_")
		if err != nil {
			t.Fatal(err)
		}
		logID := insertLifecycleRequestLog(t, f.store.DB(), ids[i], owner, string(ResultFailed), 502, "upstream", now-10, now, 10, 0)
		insertLifecycleAttempt(t, f.store.DB(), logID, now-9, now)
		facts := diagnostic.Encode(&diagnostic.Failure{Category: "network", Phase: "read_response", Code: "unexpected_eof", Message: "safe retained cause", ReceivedBytes: 54})
		if _, err = f.store.DB().Exec("INSERT INTO request_attempt_diagnostics(claim_id,request_log_id,attempt_seq,category,phase,details,completed_at) SELECT claim_id,request_log_id,attempt_seq,'network','read_response',?,? FROM request_attempts WHERE request_log_id=?", facts, now, logID); err != nil {
			t.Fatal(err)
		}
		if err = recorder.RecordDelivery(t.Context(), ids[i], diagnostic.Delivery{HTTPStatus: 200, Bytes: 12}); err != nil {
			t.Fatal(err)
		}
		if i == 1 {
			insertRequestLogLegalHold(t, f.store.DB(), logID, admin, now)
		}
	}
	tx, err := f.store.DB().BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	summary, err := f.repo.ExportLifecycleSummary(t.Context(), tx, owner, now, 100)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(summary)
	if summary.TotalLogs != "2" || summary.ErrorLogs != "2" {
		t.Fatalf("unexpected export summary: %s", encoded)
	}
	if err = f.repo.PrepareLifecycleAccountDeletion(t.Context(), tx, owner, now); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec("UPDATE logical_requests SET user_id=NULL WHERE user_id=?", owner); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec("DELETE FROM users WHERE id=?", owner); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		if _, err = f.repo.GetUser(t.Context(), owner, id, AttemptFilter{}); !errors.Is(err, ErrNotFound) {
			t.Fatal("deleted owner still reads", err)
		}
		detail, err := f.repo.GetAdmin(t.Context(), id, AttemptFilter{})
		if err != nil || detail.Request.UserID != nil || detail.Delivery == nil || detail.Attempts.Data[0].ErrorDiagnostics.Message != "safe retained cause" {
			t.Fatal("anonymization destroyed retained facts", detail, err)
		}
	}
	result, err := f.repo.RetainLifecycleRequestLogs(t.Context(), now+requestLogRetentionSeconds, 100, time.Now().Add(5*time.Second))
	if err != nil || result.RequestLogsDeleted != 1 {
		t.Fatal(result, err)
	}
	if err = recorder.RecordDelivery(t.Context(), ids[0], diagnostic.Delivery{HTTPStatus: 200, Bytes: 99}); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"request_attempt_diagnostics", "request_delivery_facts"} {
		var count int
		if err = f.store.DB().QueryRow("SELECT count(*) FROM " + table).Scan(&count); err != nil || count != 1 {
			t.Fatalf("%s: %d %v", table, count, err)
		}
	}
	// A hold prevents deletion until explicitly released.
	if _, err = f.store.DB().Exec("DELETE FROM request_logs WHERE logical_request_id=?", ids[1]); err == nil {
		t.Fatal("held root was deleted")
	}
	if _, err = f.store.DB().Exec("UPDATE legal_holds SET state='released',revision=revision+1,ended_by_user_id=?,ended_at=?,end_reason='released',retain_until=? WHERE state='active'", admin, now+1, now+1+34560000); err != nil {
		t.Fatal(err)
	}
	// Clearing the released root removes both stores; late writes cannot revive it.
	if _, err = f.store.DB().Exec("DELETE FROM request_logs WHERE logical_request_id=?", ids[1]); err != nil {
		t.Fatal(err)
	}
	if err = recorder.RecordDelivery(t.Context(), ids[1], diagnostic.Delivery{HTTPStatus: 200}); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"request_attempt_diagnostics", "request_delivery_facts"} {
		var count int
		if err = f.store.DB().QueryRow(fmt.Sprintf("SELECT count(*) FROM %s", table)).Scan(&count); err != nil || count != 0 {
			t.Fatal(table, count, err)
		}
	}
}
