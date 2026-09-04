package linklink

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/game"
)

func TestLifecycleExportContainsOnlySafeActiveAndThirtyDaySummaries(t *testing.T) {
	fixture := newFixture(t)
	userID, binding := fixture.seedUser("export", testFunding)
	first, err := fixture.service.Start(context.Background(), StartInput{UserID: userID, Spec: game.LinkLinkSpec6x8, IdempotencyKey: fixture.key(120)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.Abandon(context.Background(), AbandonInput{
		UserID: userID, SessionBinding: binding, SessionID: first.State.SessionID, ExpectedRevision: "1", Confirmation: true,
		IdempotencyKey: fixture.key(122),
	}); err != nil {
		t.Fatal(err)
	}
	second, err := fixture.service.Start(context.Background(), StartInput{UserID: userID, Spec: game.LinkLinkSpec8x8, IdempotencyKey: fixture.key(121)})
	if err != nil {
		t.Fatal(err)
	}
	tx, err := fixture.database.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	exported, err := NewLifecycleAdapter(fixture.service).ExportUser(context.Background(), tx, userID, fixture.clock.Load())
	if err != nil {
		t.Fatal(err)
	}
	if exported.Active == nil || exported.Active.SessionID != second.State.SessionID || len(exported.Summaries) != 1 || exported.Summaries[0].SessionID != first.State.SessionID {
		t.Fatalf("export = %+v", exported)
	}
	body, _ := json.Marshal(exported)
	for _, forbidden := range []string{"board", "revision", "removed_bits", "operation_id", "request_hash", "lease_id", "idempotency"} {
		if strings.Contains(string(body), forbidden) {
			t.Fatalf("export leaked %q: %s", forbidden, body)
		}
	}
}

func TestLifecycleExportTxUsesFrozenDeadlineAndPostCommitFinalizer(t *testing.T) {
	fixture := newFixture(t)
	userID, binding := fixture.seedUser("export-deadline-finalizer", testFunding)
	started, err := fixture.service.Start(context.Background(), StartInput{
		UserID: userID, Spec: game.LinkLinkSpec6x8, IdempotencyKey: fixture.key(123),
	})
	if err != nil {
		t.Fatal(err)
	}
	fixture.clock.Store(started.State.Deadline - 1)
	leaseID := fixture.mustID("gle_")
	if _, err := fixture.service.RenewLease(context.Background(), LeaseInput{
		UserID: userID, SessionBinding: binding, SessionID: started.State.SessionID, LeaseID: leaseID,
	}); err != nil {
		t.Fatal(err)
	}
	fixture.clock.Store(-1)

	tx, err := fixture.database.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	exported, finalizer, err := fixture.service.Lifecycle().ExportTx(
		context.Background(), tx, userID, started.State.Deadline, 10,
	)
	if err != nil || finalizer == nil || exported.Active != nil || len(exported.Summaries) != 1 ||
		exported.Summaries[0].SessionID != started.State.SessionID {
		_ = tx.Rollback()
		t.Fatalf("deadline export=(%+v,%v,%v)", exported, finalizer, err)
	}
	if countInTx(t, tx, `SELECT COUNT(*) FROM game_linklink_sessions WHERE id=?`, started.State.SessionID) != 0 ||
		countInTx(t, tx, `SELECT COUNT(*) FROM game_online_leases WHERE session_id=?`, started.State.SessionID) != 0 {
		_ = tx.Rollback()
		t.Fatal("deadline export did not converge inside caller transaction")
	}
	if _, bound := fixture.service.boundLease(userID, started.State.SessionID, binding, started.State.Deadline); !bound {
		_ = tx.Rollback()
		t.Fatal("process lease changed before outer commit")
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if !finalizer.Abort() {
		t.Fatal("export finalizer abort")
	}
	if fixture.scalar(`SELECT COUNT(*) FROM game_linklink_sessions WHERE id=?`, started.State.SessionID) != 1 {
		t.Fatal("rolled-back export persisted terminal state")
	}

	tx, err = fixture.database.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	_, finalizer, err = fixture.service.Lifecycle().ExportTx(
		context.Background(), tx, userID, started.State.Deadline, 10,
	)
	if err != nil || finalizer == nil {
		_ = tx.Rollback()
		t.Fatalf("committed export prepare=(%v,%v)", finalizer, err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if !finalizer.Commit() {
		t.Fatal("export finalizer commit")
	}
	if _, bound := fixture.service.boundLease(userID, started.State.SessionID, binding, started.State.Deadline); bound {
		t.Fatal("committed export retained process lease")
	}
}

func TestLifecyclePrepareDeleteTxDoesNotAcquireSharedLimiterAgain(t *testing.T) {
	fixture := newFixture(t)
	userID, _ := fixture.seedUser("delete-shared-limiter", testFunding)
	if _, err := fixture.service.Start(context.Background(), StartInput{
		UserID: userID, Spec: game.LinkLinkSpec6x8, IdempotencyKey: fixture.key(124),
	}); err != nil {
		t.Fatal(err)
	}
	_, abortOuter, err := fixture.limiter.BeginUserDeletion(userID)
	if err != nil {
		t.Fatal(err)
	}
	defer abortOuter()
	fixture.clock.Store(-1)
	tx, err := fixture.database.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	finalizer, err := fixture.service.Lifecycle().PrepareDeleteTx(context.Background(), tx, userID, testNow+17)
	if err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if !finalizer.Abort() {
		t.Fatal("delete finalizer abort")
	}
}

func TestLifecycleDeletionSharesOuterTransactionAndLateActionCannotRevive(t *testing.T) {
	fixture := newFixture(t)
	userID, binding := fixture.seedUser("delete", testFunding)
	started, err := fixture.service.Start(context.Background(), StartInput{UserID: userID, Spec: game.LinkLinkSpec6x8, IdempotencyKey: fixture.key(130)})
	if err != nil {
		t.Fatal(err)
	}
	leaseID := fixture.mustID("gle_")
	if _, err := fixture.service.RenewLease(context.Background(), LeaseInput{UserID: userID, SessionBinding: binding, SessionID: started.State.SessionID, LeaseID: leaseID}); err != nil {
		t.Fatal(err)
	}
	adapter := NewLifecycleAdapter(fixture.service)
	tx, err := fixture.database.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	rolledBack, err := adapter.PrepareUserDeletion(context.Background(), tx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if countInTx(t, tx, `SELECT COUNT(*) FROM game_linklink_sessions WHERE user_id=?`, userID) != 0 ||
		countInTx(t, tx, `SELECT COUNT(*) FROM game_online_leases WHERE user_id=? AND substr(session_id,1,3)='ll_'`, userID) != 0 ||
		countInTx(t, tx, `SELECT COUNT(*) FROM idempotency_records WHERE scope='game_linklink'`) != 0 {
		t.Fatal("prepared deletion left LinkLink-owned material")
	}
	rollbackErr := tx.Rollback()
	aborted := rolledBack.Abort()
	if rollbackErr != nil || !aborted {
		t.Fatalf("rollback/abort = (%v,%v)", rollbackErr, aborted)
	}
	if fixture.scalar(`SELECT COUNT(*) FROM game_linklink_sessions WHERE user_id=?`, userID) != 1 {
		t.Fatal("rolled-back deletion changed active state")
	}

	tx, err = fixture.database.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	committed, err := adapter.PrepareUserDeletion(context.Background(), tx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`DELETE FROM users WHERE id=?`, userID); err != nil {
		t.Fatalf("outer user delete: %v", err)
	}
	commitErr := tx.Commit()
	finalized := committed.Commit()
	if commitErr != nil || !finalized {
		t.Fatalf("commit/finalize = (%v,%v)", commitErr, finalized)
	}
	if fixture.scalar(`SELECT COUNT(*) FROM users WHERE id=?`, userID) != 0 ||
		fixture.scalar(`SELECT COUNT(*) FROM game_linklink_sessions WHERE user_id=?`, userID) != 0 ||
		fixture.scalar(`SELECT COUNT(*) FROM game_linklink_summaries WHERE user_id=?`, userID) != 0 ||
		fixture.scalar(`SELECT COUNT(*) FROM game_online_leases WHERE user_id=?`, userID) != 0 {
		t.Fatal("committed account deletion retained private LinkLink material")
	}
	if _, err := fixture.service.Match(context.Background(), MatchInput{
		UserID: userID, SessionBinding: binding, SessionID: started.State.SessionID, ExpectedRevision: "1",
		First: Coordinate{0, 0}, Second: Coordinate{0, 1}, IdempotencyKey: fixture.key(131),
	}); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("late action after account deletion = %v", err)
	}
	if fixture.scalar(`SELECT COUNT(*) FROM game_linklink_sessions WHERE id=?`, started.State.SessionID) != 0 {
		t.Fatal("late action revived deleted session")
	}
}

func TestDomainTerminalThenAccountDeletionRemovesSummary(t *testing.T) {
	fixture := newFixture(t)
	userID, binding := fixture.seedUser("domain-first-delete", testFunding)
	started, err := fixture.service.Start(context.Background(), StartInput{UserID: userID, Spec: game.LinkLinkSpec6x8, IdempotencyKey: fixture.key(132)})
	if err != nil {
		t.Fatal(err)
	}
	definition, _ := resolveSpec(game.LinkLinkSpec6x8)
	nearComplete := validBoardWithActive(definition, map[Coordinate]byte{{Row: 0, Col: 0}: 1, {Row: 0, Col: 1}: 1})
	revision := fixture.replaceBoard(started.State.SessionID, nearComplete, definition.totalPairs()-1)
	if _, err := fixture.service.Match(context.Background(), MatchInput{
		UserID: userID, SessionBinding: binding, SessionID: started.State.SessionID, ExpectedRevision: revision.Decimal(),
		First: Coordinate{0, 0}, Second: Coordinate{0, 1}, IdempotencyKey: fixture.key(133),
	}); err != nil {
		t.Fatal(err)
	}
	if fixture.scalar(`SELECT COUNT(*) FROM game_linklink_summaries WHERE session_id=?`, started.State.SessionID) != 1 {
		t.Fatal("domain-first terminal did not create summary")
	}
	tx, err := fixture.database.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	finalizer, err := fixture.service.Lifecycle().PrepareUserDeletion(context.Background(), tx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`DELETE FROM users WHERE id=?`, userID); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil || !finalizer.Commit() {
		t.Fatalf("delete commit = %v", err)
	}
	if fixture.scalar(`SELECT COUNT(*) FROM game_linklink_summaries WHERE session_id=?`, started.State.SessionID) != 0 {
		t.Fatal("account deletion retained domain-first summary")
	}
}

func TestExportRejectsHostileSummaryScore(t *testing.T) {
	fixture := newFixture(t)
	userID, binding := fixture.seedUser("hostile-summary", testFunding)
	started, err := fixture.service.Start(context.Background(), StartInput{UserID: userID, Spec: game.LinkLinkSpec6x8, IdempotencyKey: fixture.key(134)})
	if err != nil {
		t.Fatal(err)
	}
	fixture.clock.Store(started.State.Deadline)
	if current, err := fixture.service.Read(context.Background(), ReadInput{UserID: userID, SessionBinding: binding}); err != nil || current.State != nil || current.Summary == nil {
		t.Fatalf("timeout = (%+v,%v)", current, err)
	}
	if _, err := fixture.database.Exec(`UPDATE game_linklink_summaries SET score=score+1 WHERE session_id=?`, started.State.SessionID); err != nil {
		t.Fatal(err)
	}
	if current, err := fixture.service.Read(context.Background(), ReadInput{UserID: userID, SessionBinding: binding}); current.State != nil || current.Summary != nil || !errors.Is(err, ErrInvariant) {
		t.Fatalf("hostile summary current = (%+v,%v)", current, err)
	}
	tx, err := fixture.database.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := fixture.service.Lifecycle().ExportUser(context.Background(), tx, userID, fixture.clock.Load()); !errors.Is(err, ErrInvariant) {
		t.Fatalf("hostile summary export = %v", err)
	}
}

func TestSummaryRetentionBoundaryAndActiveAggregateCatchup(t *testing.T) {
	fixture := newFixture(t)
	userID, binding := fixture.seedUser("retention", testFunding)
	started, err := fixture.service.Start(context.Background(), StartInput{UserID: userID, Spec: game.LinkLinkSpec6x8, IdempotencyKey: fixture.key(140)})
	if err != nil {
		t.Fatal(err)
	}
	summary, err := fixture.service.Abandon(context.Background(), AbandonInput{
		UserID: userID, SessionBinding: binding, SessionID: started.State.SessionID, ExpectedRevision: "1", Confirmation: true,
		IdempotencyKey: fixture.key(141),
	})
	if err != nil {
		t.Fatal(err)
	}
	adapter := NewLifecycleAdapter(fixture.service)
	beforeBoundary := summary.TerminalAt + int64(summaryWindow/time.Second) - 1
	if deleted, err := adapter.Cleanup(context.Background(), beforeBoundary); err != nil || deleted != 0 {
		t.Fatalf("cleanup before boundary = (%d,%v)", deleted, err)
	}
	if fixture.scalar(`SELECT COUNT(*) FROM game_linklink_summaries WHERE session_id=?`, summary.SessionID) != 1 {
		t.Fatal("summary removed before thirty days")
	}
	if deleted, err := adapter.Cleanup(context.Background(), beforeBoundary+1); err != nil || deleted != 1 {
		t.Fatalf("cleanup at boundary = (%d,%v)", deleted, err)
	}
	if fixture.scalar(`SELECT COUNT(*) FROM game_linklink_summaries WHERE session_id=?`, summary.SessionID) != 0 {
		t.Fatal("summary retained at exact expiry")
	}

	shortID, _ := fixture.seedUser("active-short", testFunding)
	longID, _ := fixture.seedUser("active-long", testFunding)
	short, err := fixture.service.Start(context.Background(), StartInput{UserID: shortID, Spec: game.LinkLinkSpec6x8, IdempotencyKey: fixture.key(141)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.Start(context.Background(), StartInput{UserID: longID, Spec: game.LinkLinkSpec8x8, IdempotencyKey: fixture.key(142)}); err != nil {
		t.Fatal(err)
	}
	fixture.clock.Store(short.State.Deadline)
	counts, err := fixture.service.ActiveCounts(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, count := range counts {
		got[count.Spec] = count.Count
	}
	if got[game.LinkLinkSpec6x8] != "0" || got[game.LinkLinkSpec8x8] != "1" || got[game.LinkLinkSpec10x10] != "0" {
		t.Fatalf("active counts after catchup = %v", got)
	}
}

func TestLifecycleExportLimitAndBoundedRetention(t *testing.T) {
	fixture := newFixture(t)
	userID, binding := fixture.seedUser("bounded-retention", testFunding)
	for index := 0; index < 2; index++ {
		started, err := fixture.service.Start(context.Background(), StartInput{
			UserID: userID, Spec: game.LinkLinkSpec6x8, IdempotencyKey: fixture.key(150 + index*2),
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fixture.service.Abandon(context.Background(), AbandonInput{
			UserID: userID, SessionBinding: binding, SessionID: started.State.SessionID,
			ExpectedRevision: "1", Confirmation: true, IdempotencyKey: fixture.key(151 + index*2),
		}); err != nil {
			t.Fatal(err)
		}
	}
	tx, err := fixture.database.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	_, finalizer, exportErr := fixture.service.Lifecycle().ExportTx(context.Background(), tx, userID, testNow, 1)
	_ = tx.Rollback()
	if finalizer != nil || !errors.Is(exportErr, ErrLifecycleResourceLimit) {
		t.Fatalf("limited export=(%v,%v)", finalizer, exportErr)
	}

	fixture.service.recovered.Store(true)
	boundary := testNow + int64(summaryWindow/time.Second)
	before, err := fixture.service.Lifecycle().Retain(context.Background(), boundary-1, 1, time.Now().Add(time.Second))
	if err != nil || before.Processed != 0 || before.More {
		t.Fatalf("retention before boundary=(%+v,%v)", before, err)
	}
	first, err := fixture.service.Lifecycle().Retain(context.Background(), boundary, 1, time.Now().Add(time.Second))
	if err != nil || first.Processed != 1 || !first.More {
		t.Fatalf("first bounded retention=(%+v,%v)", first, err)
	}
	second, err := fixture.service.Lifecycle().Retain(context.Background(), boundary, 1, time.Now().Add(time.Second))
	if err != nil || second.Processed != 1 || second.More {
		t.Fatalf("second bounded retention=(%+v,%v)", second, err)
	}
	if fixture.scalar(`SELECT COUNT(*) FROM game_linklink_summaries WHERE user_id=?`, userID) != 0 {
		t.Fatal("bounded retention left expired summaries")
	}
}

func countInTx(t *testing.T, tx *sql.Tx, query string, arguments ...any) int64 {
	t.Helper()
	var count int64
	if err := tx.QueryRow(query, arguments...).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}
