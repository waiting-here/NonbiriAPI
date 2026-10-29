package claim

import (
	"crypto/sha256"
	"reflect"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/diagnostic"
	"github.com/waiting-here/NonbiriAPI/internal/logapi"
	"github.com/waiting-here/NonbiriAPI/internal/observability"
)

type diagnosticCursorKey struct{}

func (diagnosticCursorKey) DeriveGenerationTwoSubkey(info []byte) ([]byte, error) {
	key := sha256.Sum256(append([]byte("diagnostic-reader-fixture"), info...))
	return key[:], nil
}

func TestAttemptDiagnosticsCommitRollbackReplayAndLogRead(t *testing.T) {
	f := newClaimFixture(t)
	f.clock.Store(time.Now().Unix())
	owner := f.seedUser("diagnostic-owner", false)
	key := f.seedKey(owner, "diagnostic")
	request := f.acceptSelf(owner, 1)
	handle, err := f.service.Claim(t.Context(), ClaimInput{RequestID: request.ID, ActorUserID: owner, AttemptSeq: 1, Purpose: PurposeSelf, PersonalModelID: key.modelID, Candidate: key.candidate})
	if err != nil {
		t.Fatal(err)
	}
	dispatch, err := f.service.TakeForDispatch(t.Context(), handle)
	if err != nil {
		t.Fatal(err)
	}
	dispatch.Clear()
	facts := &diagnostic.Failure{Category: "network", Phase: "read_response", Code: "unexpected_eof", Message: "Response ended before all bytes were received", UpstreamStatus: 200, HeadersReceived: true, ReceivedBytes: 120, ReceivedEvents: 3, BodyEOF: true, UsagePresent: true, Correlation: []diagnostic.Correlation{{Source: "upstream", Name: "x-request-id", Value: "upstream-call-1"}}}
	outcome := AttemptOutcome{Kind: ResultResponse, UpstreamStatus: 200, ResponseStarted: true, Usage: contract.Usage{Present: true, UncachedInputTokens: 5, OutputTokens: 2}, ErrorDiagnostics: diagnostic.Encode(facts)}
	if _, err = f.db.Exec("CREATE TEMP TRIGGER reject_diagnostic_completion BEFORE UPDATE OF state ON dispatch_claims WHEN NEW.state='committed' BEGIN SELECT RAISE(ABORT,'injected terminal failure'); END"); err != nil {
		t.Fatal(err)
	}
	if _, err = f.service.CompleteAttempt(t.Context(), handle, outcome); err == nil {
		t.Fatalf("rollback: %v", err)
	}
	var count int
	for _, table := range []string{"request_attempts", "request_attempt_diagnostics"} {
		if err = f.db.QueryRow("SELECT count(*) FROM " + table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("%s survived rollback: %d %v", table, count, err)
		}
	}
	if _, err = f.db.Exec("DROP TRIGGER reject_diagnostic_completion"); err != nil {
		t.Fatal(err)
	}
	attempt, err := f.service.CompleteAttempt(t.Context(), handle, outcome)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := f.service.CompleteAttempt(t.Context(), handle, outcome)
	if err != nil || !reflect.DeepEqual(replay, attempt) {
		t.Fatalf("replay: %+v %v", replay, err)
	}
	var saved string
	if err = f.db.QueryRow("SELECT details FROM request_attempt_diagnostics WHERE claim_id=?", handle.ClaimID()).Scan(&saved); err != nil || saved != outcome.ErrorDiagnostics {
		t.Fatalf("saved: %q %v", saved, err)
	}
	if err = f.db.QueryRow("SELECT count(*) FROM request_attempt_diagnostics").Scan(&count); err != nil || count != 1 {
		t.Fatal(count, err)
	}
	if _, err = f.service.CompleteRequest(t.Context(), CompleteRequestInput{RequestID: request.ID, Caller: CallerResult{Class: ResultFailed, Status: 502, ErrorCode: "upstream"}, Disposition: AccountingCommit}); err != nil {
		t.Fatal(err)
	}
	recorder, err := observability.NewRepository(f.db)
	if err != nil {
		t.Fatal(err)
	}
	if err = recorder.RecordDelivery(t.Context(), request.ID, diagnostic.Delivery{HTTPStatus: 200, Bytes: 47}); err != nil {
		t.Fatal(err)
	}
	reader, err := logapi.NewRepository(f.db, diagnosticCursorKey{})
	if err != nil {
		t.Fatal(err)
	}
	detail, err := reader.GetAdmin(t.Context(), request.ID, logapi.AttemptFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if detail.Delivery == nil || detail.Delivery.HTTPStatus != 200 || detail.Delivery.Bytes != 47 || detail.Delivery.Failed || detail.Request.CallerStatus == nil || *detail.Request.CallerStatus != 502 || detail.Request.CallerResultClass == nil || *detail.Request.CallerResultClass != logapi.ResultFailed {
		t.Fatalf("delivery changed terminal result: %+v", detail)
	}
	if len(detail.Attempts.Data) != 1 || !reflect.DeepEqual(detail.Attempts.Data[0].ErrorDiagnostics, facts) || detail.Attempts.Data[0].ErrorDiagnostics.ProtocolComplete {
		t.Fatalf("lost failure observations: %+v", detail.Attempts)
	}
	if _, err = f.db.Exec("DELETE FROM request_logs WHERE logical_request_id=?", request.ID); err != nil {
		t.Fatal(err)
	}
	_, _ = f.service.CompleteAttempt(t.Context(), handle, outcome)
	if err = recorder.RecordDelivery(t.Context(), request.ID, diagnostic.Delivery{HTTPStatus: 200, Bytes: 999}); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"request_logs", "request_attempts", "request_attempt_diagnostics", "request_delivery_facts"} {
		if err = f.db.QueryRow("SELECT count(*) FROM " + table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("%s resurrected after cleanup: %d %v", table, count, err)
		}
	}
}
