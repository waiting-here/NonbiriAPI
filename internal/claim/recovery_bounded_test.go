package claim

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"
)

func TestRecoverNonterminalAtUsesFrozenTimeAndBoundedClaimBatch(t *testing.T) {
	fixture := newClaimFixture(t)
	userID := fixture.seedUser("bounded-recovery", false)
	for index := 0; index < 2; index++ {
		key := fixture.seedKey(userID, "bounded-recovery-"+string(rune('a'+index)))
		key.candidate.UpstreamModelID = ""
		if _, _, err := fixture.service.ClaimDiscovery(context.Background(), DiscoveryClaimInput{
			ActorUserID: userID,
			Candidate:   key.candidate,
		}); err != nil {
			t.Fatalf("claim discovery %d: %v", index, err)
		}
	}
	orphanID := seedUnreferencedRecoverySecret(t, fixture, 91, nil)
	fixture.clock.Store(9_999)
	decisionNow := int64(1_234)
	deadline := time.Now().Add(time.Minute)

	first, err := fixture.service.RecoverNonterminalAt(context.Background(), decisionNow, 1, deadline)
	if err != nil {
		t.Fatalf("first bounded recovery: %v", err)
	}
	if first.ReleasedClaims != 1 || first.CommittedClaims != 0 || first.CompletedRequests != 0 || !first.More ||
		first.MarkedOrphans != 0 || first.DeletedOrphans != 0 {
		t.Fatalf("first bounded recovery = %+v", first)
	}
	var releasedCount int
	var releasedAt int64
	if err := fixture.db.QueryRow(`SELECT COUNT(*),COALESCE(MAX(terminal_at),0)
FROM dispatch_claims WHERE state='released'`).Scan(&releasedCount, &releasedAt); err != nil {
		t.Fatalf("read first release: %v", err)
	}
	if releasedCount != 1 || releasedAt != decisionNow {
		t.Fatalf("released count/time = %d/%d", releasedCount, releasedAt)
	}
	var orphaned sql.NullInt64
	if err := fixture.db.QueryRow(`SELECT orphaned_at FROM endpoint_key_secrets WHERE id=?`, orphanID).Scan(&orphaned); err != nil {
		t.Fatalf("read separated orphan secret: %v", err)
	}
	if orphaned.Valid {
		t.Fatalf("claim recovery touched secret slot: %+v", orphaned)
	}

	second, err := fixture.service.RecoverNonterminalAt(context.Background(), decisionNow, 100, deadline)
	if err != nil {
		t.Fatalf("second bounded recovery: %v", err)
	}
	if second.ReleasedClaims != 1 || second.CommittedClaims != 0 || second.CompletedRequests != 2 || second.More {
		t.Fatalf("second bounded recovery = %+v", second)
	}
	var terminalRequests int
	var minimumTerminal, maximumTerminal int64
	if err := fixture.db.QueryRow(`SELECT COUNT(*),MIN(terminal_at),MAX(terminal_at)
FROM logical_requests WHERE state='terminal'`).Scan(&terminalRequests, &minimumTerminal, &maximumTerminal); err != nil {
		t.Fatalf("read terminal requests: %v", err)
	}
	if terminalRequests != 2 || minimumTerminal != decisionNow || maximumTerminal != decisionNow {
		t.Fatalf("terminal requests/time = %d/%d/%d", terminalRequests, minimumTerminal, maximumTerminal)
	}
}

func TestRecoverNonterminalAtHonorsExpiredBudget(t *testing.T) {
	fixture := newClaimFixture(t)
	userID := fixture.seedUser("deadline-recovery", false)
	key := fixture.seedKey(userID, "deadline-recovery")
	key.candidate.UpstreamModelID = ""
	if _, _, err := fixture.service.ClaimDiscovery(context.Background(), DiscoveryClaimInput{
		ActorUserID: userID,
		Candidate:   key.candidate,
	}); err != nil {
		t.Fatalf("claim discovery: %v", err)
	}

	_, err := fixture.service.RecoverNonterminalAt(
		context.Background(), fixture.clock.Load(), 1, time.Now().Add(-time.Second),
	)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expired budget error = %v", err)
	}
	var state string
	if err := fixture.db.QueryRow(`SELECT state FROM dispatch_claims`).Scan(&state); err != nil || state != "claimed" {
		t.Fatalf("claim after expired budget = %q, %v", state, err)
	}
}

func TestMaintainOrphanSecretsAtUsesExactOneHourBoundaryAndLimit(t *testing.T) {
	fixture := newClaimFixture(t)
	decisionNow := int64(5_000)
	dueAt := decisionNow - int64(OrphanSecretTTL.Seconds())
	dueID := seedUnreferencedRecoverySecret(t, fixture, 101, &dueAt)
	notDueAt := dueAt + 1
	notDueID := seedUnreferencedRecoverySecret(t, fixture, 102, &notDueAt)
	unmarkedID := seedUnreferencedRecoverySecret(t, fixture, 103, nil)
	fixture.clock.Store(99_999)
	deadline := time.Now().Add(time.Minute)

	first, err := fixture.service.MaintainOrphanSecretsAt(context.Background(), decisionNow, 1, deadline)
	if err != nil {
		t.Fatalf("first secret batch: %v", err)
	}
	if first.Deleted != 1 || first.Marked != 0 || !first.More {
		t.Fatalf("first secret batch = %+v", first)
	}
	assertRecoverySecretExists(t, fixture, dueID, false)
	assertRecoverySecretExists(t, fixture, notDueID, true)

	second, err := fixture.service.MaintainOrphanSecretsAt(context.Background(), decisionNow, 1, deadline)
	if err != nil {
		t.Fatalf("second secret batch: %v", err)
	}
	if second.Deleted != 0 || second.Marked != 1 || second.More {
		t.Fatalf("second secret batch = %+v", second)
	}
	var markedAt sql.NullInt64
	if err := fixture.db.QueryRow(`SELECT orphaned_at FROM endpoint_key_secrets WHERE id=?`, unmarkedID).Scan(&markedAt); err != nil {
		t.Fatalf("read new orphan marker: %v", err)
	}
	if !markedAt.Valid || markedAt.Int64 != decisionNow {
		t.Fatalf("new orphan marker = %+v", markedAt)
	}

	boundaryPlusOne, err := fixture.service.MaintainOrphanSecretsAt(context.Background(), decisionNow+1, 100, deadline)
	if err != nil {
		t.Fatalf("boundary plus one: %v", err)
	}
	if boundaryPlusOne.Deleted != 1 || boundaryPlusOne.Marked != 0 || boundaryPlusOne.More {
		t.Fatalf("boundary plus one = %+v", boundaryPlusOne)
	}
	assertRecoverySecretExists(t, fixture, notDueID, false)
	assertRecoverySecretExists(t, fixture, unmarkedID, true)

	markedBoundary, err := fixture.service.MaintainOrphanSecretsAt(
		context.Background(), decisionNow+int64(OrphanSecretTTL.Seconds()), 100, deadline,
	)
	if err != nil {
		t.Fatalf("marked boundary: %v", err)
	}
	if markedBoundary.Deleted != 1 || markedBoundary.Marked != 0 || markedBoundary.More {
		t.Fatalf("marked boundary = %+v", markedBoundary)
	}
	assertRecoverySecretExists(t, fixture, unmarkedID, false)
}

func TestMaintainOrphanSecretsAtHonorsExpiredBudget(t *testing.T) {
	fixture := newClaimFixture(t)
	secretID := seedUnreferencedRecoverySecret(t, fixture, 111, nil)
	_, err := fixture.service.MaintainOrphanSecretsAt(
		context.Background(), fixture.clock.Load(), 1, time.Now().Add(-time.Second),
	)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expired budget error = %v", err)
	}
	var marker sql.NullInt64
	if err := fixture.db.QueryRow(`SELECT orphaned_at FROM endpoint_key_secrets WHERE id=?`, secretID).Scan(&marker); err != nil {
		t.Fatalf("read marker after expired budget: %v", err)
	}
	if marker.Valid {
		t.Fatalf("expired budget marked secret: %+v", marker)
	}
}

func TestBoundedRecoverySeamsRejectBatchAboveMaximum(t *testing.T) {
	fixture := newClaimFixture(t)
	deadline := time.Now().Add(time.Minute)
	if _, err := fixture.service.RecoverNonterminalAt(
		context.Background(), fixture.clock.Load(), MaxRecoveryBatch+1, deadline,
	); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("claim oversized batch error = %v", err)
	}
	if _, err := fixture.service.MaintainOrphanSecretsAt(
		context.Background(), fixture.clock.Load(), MaxRecoveryBatch+1, deadline,
	); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("secret oversized batch error = %v", err)
	}
}

func seedUnreferencedRecoverySecret(
	t *testing.T,
	fixture *claimFixture,
	discriminator byte,
	orphanedAt *int64,
) int64 {
	t.Helper()
	contextID := make([]byte, 16)
	contextID[0] = discriminator
	result, err := fixture.db.Exec(`INSERT INTO endpoint_key_secrets(
context_id,canonical_base_url,connector_type,encrypted_secret,created_at,orphaned_at)
VALUES(?,'https://orphan.example.test/v1','openai-compatible',?,0,?)`,
		contextID, "recovery-envelope-"+string(rune(discriminator)), orphanedAt)
	if err != nil {
		t.Fatalf("seed unreferenced secret: %v", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("read unreferenced secret id: %v", err)
	}
	return id
}

func assertRecoverySecretExists(t *testing.T, fixture *claimFixture, secretID int64, want bool) {
	t.Helper()
	var exists int
	if err := fixture.db.QueryRow(`SELECT EXISTS(
SELECT 1 FROM endpoint_key_secrets WHERE id=?)`, secretID).Scan(&exists); err != nil {
		t.Fatalf("read secret %d existence: %v", secretID, err)
	}
	if (exists != 0) != want {
		t.Fatalf("secret %d exists=%d, want %v", secretID, exists, want)
	}
}
