package claim

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/logapi"
	"github.com/waiting-here/NonbiriAPI/internal/requestattempt"
	"github.com/waiting-here/NonbiriAPI/internal/requestkind"
)

func TestPrepareAccountDeletionAfterRealLogRetention(t *testing.T) {
	f := newClaimFixture(t)
	f.useLedgerAccounting()
	userID := f.seedUser("retained-history", false)
	f.seedLedgerUser(userID, 1_000)
	key := f.seedKey(userID, "retained-history")
	request := f.acceptSelf(userID, 1)
	handle := mustDeletionClaim(t, f, request, key, 1, PurposeSelf, 0)
	dispatch, err := f.service.TakeForDispatch(context.Background(), handle)
	if err != nil {
		t.Fatal(err)
	}
	dispatch.Clear()
	if _, err := f.service.CompleteAttempt(context.Background(), handle, successfulDeletionOutcome()); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.CompleteRequest(context.Background(), CompleteRequestInput{
		RequestID: request.ID, Caller: CallerResult{Class: ResultSuccess, Status: 200},
		Disposition: AccountingCommit, ActualChargeMilli: 60,
	}); err != nil {
		t.Fatal(err)
	}
	repository, err := logapi.NewRepository(f.db, deletionCursorKeys{})
	if err != nil {
		t.Fatal(err)
	}
	decisionNow := f.clock.Load() + 30*24*60*60
	retained, err := repository.RetainLifecycleRequestLogs(context.Background(), decisionNow, 10, time.Now().Add(5*time.Second))
	if err != nil || retained.RequestLogsDeleted != 1 {
		t.Fatalf("actual log retention = %+v, %v", retained, err)
	}
	var logs, claims int
	if err := f.db.QueryRow(`SELECT (SELECT COUNT(*) FROM request_logs), (SELECT COUNT(*) FROM dispatch_claims)`).Scan(&logs, &claims); err != nil || logs != 0 || claims != 1 {
		t.Fatalf("retained logs/claims = %d/%d, %v", logs, claims, err)
	}
	tx := f.beginLedgerTx()
	defer tx.Rollback()
	if err := f.service.PrepareAccountDeletion(context.Background(), tx, userID, decisionNow); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	terminal := loadDeletionRequest(t, f.db, request.ID)
	if terminal.UserID != nil || terminal.State != RequestTerminal || terminal.AccountingDisposition != AccountingCommit {
		t.Fatalf("detached retained request = %+v", terminal)
	}
	f.requireLedgerBalanceForUser(userID, "940")
	f.requireRealLedgerState(request.ID, 0, 0, 3, 7)
}

type deletionCursorKeys struct{}

func (deletionCursorKeys) DeriveGenerationTwoSubkey([]byte) ([]byte, error) {
	return make([]byte, 32), nil
}

func TestPrepareAccountDeletionBatchRoutesRejectionsAndReplay(t *testing.T) {
	f := newClaimFixture(t)
	userID := f.seedUser("batch-routes", false)
	otherID := f.seedUser("batch-other", false)
	seedTerminalDeletionHistory(t, f, otherID, 2, 3)
	for _, operation := range connectorcontract.AllOperations() {
		for _, charity := range []bool{false, true} {
			route := requestkind.ForOperation(operation, charity)
			var request Request
			if charity {
				request = f.acceptCharity(userID, 1, route)
			} else {
				request = f.acceptSelf(userID, 1, route)
			}
			if _, err := f.service.CompleteRequest(context.Background(), CompleteRequestInput{
				RequestID: request.ID, Caller: CallerResult{Class: ResultCancelled}, Disposition: AccountingRelease,
			}); err != nil {
				t.Fatalf("terminal %s: %v", route, err)
			}
		}
	}
	key := f.seedKey(userID, "batch-discovery")
	key.candidate.UpstreamModelID = ""
	discovery, handle, err := f.service.ClaimDiscovery(context.Background(), DiscoveryClaimInput{ActorUserID: userID, Candidate: key.candidate})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.ReleaseUndispatched(context.Background(), handle); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.CompleteRequest(context.Background(), CompleteRequestInput{
		RequestID: discovery.ID, Caller: CallerResult{Class: ResultCancelled}, Disposition: AccountingNone,
	}); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/v1/models", "/v1/chat/completions"} {
		id, err := db.GenerateOpaqueID("req_")
		if err != nil {
			t.Fatal(err)
		}
		method := "POST"
		if path == "/v1/models" {
			method = "GET"
		}
		if err := f.service.RecordRejection(context.Background(), userID, requestattempt.Fact{
			ID: id, Method: method, Path: path, Stage: "preflight", Reason: "invalid_request", Status: 400, Code: "invalid_request",
		}); err != nil {
			t.Fatal(err)
		}
	}
	tx := f.beginLedgerTx()
	defer tx.Rollback()
	if err := f.service.PrepareAccountDeletion(context.Background(), tx, userID, 2_000); err != nil {
		t.Fatal(err)
	}
	var attached, other, mismatches int
	if err := tx.QueryRow(`SELECT
(SELECT COUNT(*) FROM logical_requests WHERE user_id=?),
(SELECT COUNT(*) FROM logical_requests WHERE user_id=?),
(SELECT COUNT(*) FROM request_logs l JOIN logical_requests r ON r.id=l.logical_request_id
 WHERE l.user_id IS NOT r.user_id OR l.caller_result_class IS NOT r.caller_result_class OR l.completed_at IS NOT r.terminal_at)`, userID, otherID).Scan(&attached, &other, &mismatches); err != nil || attached != 0 || other != 2 || mismatches != 0 {
		t.Fatalf("detached/other/snapshot mismatches = %d/%d/%d, %v", attached, other, mismatches, err)
	}
	first := deletionSnapshot(t, tx)
	if err := f.service.PrepareAccountDeletion(context.Background(), tx, userID, 2_001); err != nil {
		t.Fatal(err)
	}
	if next := deletionSnapshot(t, tx); next != first {
		t.Fatal("deletion replay changed terminal facts or accounting")
	}
}

func TestPrepareAccountDeletionBatchRejectsInvalidTerminalFactsBeforeWriting(t *testing.T) {
	for _, test := range []struct{ name, update string }{
		{"capacity", `UPDATE logical_requests SET ledger_rows_remaining=X'00000000000000000000000000000001' WHERE user_id=?`},
		{"large-capacity", `UPDATE logical_requests SET ledger_rows_remaining=X'01000000000000000000000000000000' WHERE user_id=?`},
		{"reserved", `UPDATE logical_requests SET accounting_state='reserved',account_reserved_milli=1 WHERE user_id=?`},
		{"unsettled", `UPDATE logical_requests SET accounting_state='none' WHERE user_id=?`},
		{"invalid-log", `UPDATE request_logs SET id=0 WHERE user_id=?`},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newClaimFixture(t)
			userID := f.seedUser("invalid-terminal", false)
			seedTerminalDeletionHistory(t, f, userID, 1, 1)
			if _, err := f.db.Exec(test.update, userID); err != nil {
				t.Fatal(err)
			}
			before := deletionSnapshot(t, f.db)
			tx := f.beginLedgerTx()
			defer tx.Rollback()
			if err := f.service.PrepareAccountDeletion(context.Background(), tx, userID, 2_000); !errors.Is(err, ErrInvariant) {
				t.Fatalf("invalid terminal error = %v", err)
			}
			if after := deletionSnapshot(t, tx); after != before {
				t.Fatal("terminal validation failure wrote data")
			}
		})
	}
}

func TestPrepareAccountDeletionBatchRejectsActiveClaimsAndMissingActiveLogs(t *testing.T) {
	for _, terminalParent := range []bool{false, true} {
		for _, dispatched := range []bool{false, true} {
			t.Run(fmt.Sprintf("terminal=%t/dispatched=%t", terminalParent, dispatched), func(t *testing.T) {
				f := newClaimFixture(t)
				userID := f.seedUser("active-invalid", false)
				key := f.seedKey(userID, "active-invalid")
				request := f.acceptSelf(userID, 1)
				handle := mustDeletionClaim(t, f, request, key, 1, PurposeSelf, 0)
				if dispatched {
					dispatch, err := f.service.TakeForDispatch(context.Background(), handle)
					if err != nil {
						t.Fatal(err)
					}
					dispatch.Clear()
				}
				want := ErrNotFound
				if terminalParent {
					_, err := f.db.Exec(`UPDATE logical_requests SET state='terminal',caller_result_class='cancelled',
terminal_at=1000,accounting_state='released',account_reserved_milli=0,ledger_rows_remaining=zeroblob(16) WHERE id=?`, request.ID)
					if err != nil {
						t.Fatal(err)
					}
					want = ErrInvariant
				}
				if _, err := f.db.Exec(`DELETE FROM request_logs WHERE logical_request_id=?`, request.ID); err != nil {
					t.Fatal(err)
				}
				before := deletionSnapshot(t, f.db)
				tx := f.beginLedgerTx()
				defer tx.Rollback()
				if err := f.service.PrepareAccountDeletion(context.Background(), tx, userID, 2_000); !errors.Is(err, want) {
					t.Fatalf("active claim deletion = %v, want %v", err, want)
				}
				if after := deletionSnapshot(t, tx); after != before {
					t.Fatal("active claim rejection wrote data")
				}
			})
		}
	}
}

func TestPrepareAccountDeletionBatchRollsBackAfterActiveAccountingFailure(t *testing.T) {
	f := newClaimFixture(t)
	userID := f.seedUser("late-failure", false)
	seedTerminalDeletionHistory(t, f, userID, 3, 3)
	f.acceptSelf(userID, 2)
	f.accounting.setFailPoint("request_after_terminal")
	before := deletionSnapshot(t, f.db)
	tx := f.beginLedgerTx()
	defer tx.Rollback()
	if err := f.service.PrepareAccountDeletion(context.Background(), tx, userID, 2_000); !errors.Is(err, errTestAccounting) {
		t.Fatalf("late failure = %v", err)
	}
	var detached int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM logical_requests WHERE user_id IS NULL`).Scan(&detached); err != nil || detached != 3 {
		t.Fatalf("batch before late rollback = %d, %v", detached, err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if after := deletionSnapshot(t, f.db); after != before {
		t.Fatal("late failure rollback changed requests, logs, claims, or accounting")
	}
}

func TestPrepareAccountDeletionBatchLinearizesWithLateCompletion(t *testing.T) {
	f := newClaimFixture(t)
	f.useLedgerAccounting()
	userID := f.seedUser("late-concurrent", false)
	f.seedLedgerUser(userID, 1_000)
	seedTerminalDeletionHistory(t, f, userID, 3, 1)
	key := f.seedKey(userID, "late-concurrent")
	request := f.acceptSelf(userID, 1)
	handle := mustDeletionClaim(t, f, request, key, 1, PurposeSelf, 0)
	dispatch, err := f.service.TakeForDispatch(context.Background(), handle)
	if err != nil {
		t.Fatal(err)
	}
	dispatch.Clear()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	tx := f.beginLedgerTx()
	defer tx.Rollback()
	started, done := make(chan struct{}), make(chan error, 1)
	go func() {
		close(started)
		_, err := f.service.CompleteAttempt(ctx, handle, successfulDeletionOutcome())
		if err == nil {
			_, err = f.service.CompleteRequest(ctx, CompleteRequestInput{
				RequestID: request.ID, Caller: CallerResult{Class: ResultSuccess, Status: 200},
				Disposition: AccountingCommit, ActualChargeMilli: 60,
			})
		}
		done <- err
	}()
	<-started
	if err := f.service.PrepareAccountDeletion(ctx, tx, userID, 2_000); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	terminal := loadDeletionRequest(t, f.db, request.ID)
	if terminal.UserID != nil || terminal.Destination != DestinationExternal || terminal.State != RequestTerminal {
		t.Fatalf("late concurrent result = %+v", terminal)
	}
	f.requireLedgerBalanceForUser(userID, "900")
	f.requireRealLedgerState(request.ID, 0, 0, 3, 7)
}

func TestPrepareAccountDeletionBatchQueryPlans(t *testing.T) {
	f := newClaimFixture(t)
	for name, query := range map[string]string{
		"requests": terminalDeletionRequestsSQL, "claims": terminalDeletionClaimsSQL, "detach": detachTerminalRequestsSQL,
	} {
		rows, err := f.db.Query("EXPLAIN QUERY PLAN "+query, 1)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var details []string
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
				t.Fatal(err)
			}
			details = append(details, detail)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		rows.Close()
		plan := strings.Join(details, "; ")
		t.Logf("%s: %s", name, plan)
		if !strings.Contains(plan, "idx_logical_requests_user") || strings.Contains(plan, "SCAN ") || strings.Contains(plan, "TEMP B-TREE") {
			t.Fatalf("deletion must use bounded user and relationship lookups: %s", plan)
		}
	}
}

func seedTerminalDeletionHistory(t testing.TB, f *claimFixture, userID int64, count, claims int) {
	t.Helper()
	tx, err := f.db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	requestStatement, err := tx.Prepare(`INSERT INTO logical_requests
(id,user_id,route_kind,state,attempt_limit,caller_result_class,accounting_state,settlement_destination,ledger_rows_remaining,created_at,terminal_at)
VALUES(?,?,'openai_chat_completions','terminal',?,'cancelled','released','user',zeroblob(16),1000,1000)`)
	if err != nil {
		t.Fatal(err)
	}
	defer requestStatement.Close()
	logStatement, err := tx.Prepare(`INSERT INTO request_logs(logical_request_id,user_id,caller_result_class,started_at,completed_at)
VALUES(?,?,'cancelled',1000,1000)`)
	if err != nil {
		t.Fatal(err)
	}
	defer logStatement.Close()
	claimStatement, err := tx.Prepare(`INSERT INTO dispatch_claims
(id,logical_request_id,attempt_seq,purpose,claim_now,state,terminal_at,streak_disposition,failure_origin)
VALUES(?,?,?,'self',1000,'released',1000,'neutral','platform')`)
	if err != nil {
		t.Fatal(err)
	}
	defer claimStatement.Close()
	for index := 0; index < count; index++ {
		id := fmt.Sprintf("req_%010d%011dA", userID, index)
		if _, err := requestStatement.Exec(id, userID, max(1, claims)); err != nil {
			t.Fatal(err)
		}
		if _, err := logStatement.Exec(id, userID); err != nil {
			t.Fatal(err)
		}
		for seq := 1; seq <= claims; seq++ {
			claimID := fmt.Sprintf("clm_%010d%08d%03dA", userID, index, seq)
			if _, err := claimStatement.Exec(claimID, id, seq); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

type deletionQuerier interface {
	Query(string, ...any) (*sql.Rows, error)
}

func deletionSnapshot(t testing.TB, query deletionQuerier) string {
	t.Helper()
	return deletionSnapshotWithOperationIDs(t, query, nil)
}

func deletionSnapshotWithOperationIDs(t testing.TB, query deletionQuerier, operationIDs map[string]int64) string {
	t.Helper()
	digest := sha256.New()
	for _, table := range []string{"logical_requests", "dispatch_claims", "request_logs", "request_attempts", "charity_reservations", "endpoint_key_secrets", "credit_capacity", "credit_accounts", "credit_operations", "credit_entries", "claim_test_ledger_rows"} {
		statement := "SELECT * FROM " + table + " ORDER BY 1,2"
		if operationIDs != nil {
			switch table {
			case "credit_operations":
				statement = "SELECT * FROM credit_operations ORDER BY ledger_seq"
			case "credit_entries":
				statement = `SELECT e.* FROM credit_entries e LEFT JOIN credit_operations o ON o.id=e.operation_id ORDER BY o.ledger_seq,e.line_no`
			}
		}
		rows, err := query.Query(statement)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		columns, err := rows.Columns()
		if err != nil {
			t.Fatal(err)
		}
		values := make([]any, len(columns))
		pointers := make([]any, len(columns))
		for index := range values {
			pointers[index] = &values[index]
		}
		fmt.Fprintln(digest, table)
		for rows.Next() {
			if err := rows.Scan(pointers...); err != nil {
				t.Fatal(err)
			}
			if operationIDs != nil && (table == "credit_operations" || table == "credit_entries") {
				id, valid := values[0].(string)
				seq, exists := operationIDs[id]
				if !valid || !exists {
					t.Fatalf("%s contains an unmapped operation identity", table)
				}
				values[0] = seq
			}
			fmt.Fprintf(digest, "%#v\n", values)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		rows.Close()
	}
	return fmt.Sprintf("%x", digest.Sum(nil))
}
