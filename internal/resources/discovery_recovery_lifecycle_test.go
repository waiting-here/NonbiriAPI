package resources

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/db"
)

func TestRecoverStaleDiscoveriesAtUsesFrozenTimeAndBoundedRoots(t *testing.T) {
	environment := newResourceTestEnvironment(t)
	userID := environment.seedUser(t, "bounded-discovery-recovery")
	decisionNow := resourceTestNow + staleDiscoverySecond + 50
	firstKey, firstOperation := seedDiscoveryRecoveryRoot(t, environment, userID, 'A', decisionNow-staleDiscoverySecond)
	secondKey, secondOperation := seedDiscoveryRecoveryRoot(t, environment, userID, 'B', decisionNow-staleDiscoverySecond-1)
	orphanOperation := seedOrphanedDiscoveryOperation(t, environment, userID, decisionNow-staleDiscoverySecond)
	environment.clock.Store(decisionNow + 10_000)
	deadline := time.Now().Add(time.Minute)

	first, err := environment.repository.RecoverStaleDiscoveriesAt(
		context.Background(), decisionNow, 2, deadline,
	)
	if err != nil {
		t.Fatalf("first recovery batch: %v", err)
	}
	if first.Processed != 2 || !first.More {
		t.Fatalf("first recovery batch = %+v", first)
	}
	assertDiscoveryRecoveryEvidence(t, environment, firstKey, decisionNow)
	assertDiscoveryRecoveryEvidence(t, environment, secondKey, decisionNow)
	assertDiscoveryRecoveryOperation(t, environment, firstOperation, "completed", decisionNow)
	assertDiscoveryRecoveryOperation(t, environment, secondOperation, "completed", decisionNow)
	assertDiscoveryRecoveryOperation(t, environment, orphanOperation, "accepted", 0)

	second, err := environment.repository.RecoverStaleDiscoveriesAt(
		context.Background(), decisionNow, 2, deadline,
	)
	if err != nil {
		t.Fatalf("second recovery batch: %v", err)
	}
	if second.Processed != 1 || second.More {
		t.Fatalf("second recovery batch = %+v", second)
	}
	assertDiscoveryRecoveryOperation(t, environment, orphanOperation, "completed", decisionNow)
	environment.discovery.mu.Lock()
	networkCalls := environment.discovery.calls
	environment.discovery.mu.Unlock()
	if networkCalls != 0 {
		t.Fatalf("recovery retried discovery network rail %d times", networkCalls)
	}
}

func TestRecoverStaleDiscoveriesAtHonorsExactBoundaryAndDeadline(t *testing.T) {
	t.Run("boundary", func(t *testing.T) {
		environment := newResourceTestEnvironment(t)
		userID := environment.seedUser(t, "discovery-boundary")
		decisionNow := resourceTestNow + staleDiscoverySecond
		keyID, operationID := seedDiscoveryRecoveryRoot(t, environment, userID, 'C', decisionNow-staleDiscoverySecond+1)
		deadline := time.Now().Add(time.Minute)

		before, err := environment.repository.RecoverStaleDiscoveriesAt(
			context.Background(), decisionNow, 100, deadline,
		)
		if err != nil || before != (DiscoveryRecoveryResult{}) {
			t.Fatalf("before boundary = %+v, %v", before, err)
		}
		assertDiscoveryRecoveryOperation(t, environment, operationID, "accepted", 0)

		atBoundary, err := environment.repository.RecoverStaleDiscoveriesAt(
			context.Background(), decisionNow+1, 100, deadline,
		)
		if err != nil || atBoundary != (DiscoveryRecoveryResult{Processed: 1}) {
			t.Fatalf("at boundary = %+v, %v", atBoundary, err)
		}
		assertDiscoveryRecoveryEvidence(t, environment, keyID, decisionNow+1)
		assertDiscoveryRecoveryOperation(t, environment, operationID, "completed", decisionNow+1)
	})

	t.Run("deadline", func(t *testing.T) {
		environment := newResourceTestEnvironment(t)
		userID := environment.seedUser(t, "discovery-deadline")
		decisionNow := resourceTestNow + staleDiscoverySecond + 1
		keyID, operationID := seedDiscoveryRecoveryRoot(t, environment, userID, 'D', resourceTestNow)
		_, err := environment.repository.RecoverStaleDiscoveriesAt(
			context.Background(), decisionNow, 1, time.Now().Add(-time.Second),
		)
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("expired budget error = %v", err)
		}
		assertDiscoveryRecoveryChecking(t, environment, keyID)
		assertDiscoveryRecoveryOperation(t, environment, operationID, "accepted", 0)
	})
}

func TestRecoverStaleDiscoveriesAtRejectsBatchAboveMaximum(t *testing.T) {
	environment := newResourceTestEnvironment(t)
	_, err := environment.repository.RecoverStaleDiscoveriesAt(
		context.Background(), resourceTestNow, maxDiscoveryRecoveryBatch+1, time.Now().Add(time.Minute),
	)
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("oversized recovery batch error = %v", err)
	}
}

func seedDiscoveryRecoveryRoot(
	t *testing.T,
	environment *resourceTestEnvironment,
	userID int64,
	discriminator rune,
	startedAt int64,
) (int64, string) {
	t.Helper()
	endpoint := environment.createEndpoint(t, userID, resourceTestKey(byte(discriminator)))
	endpointID := resourceTestID(t, endpoint.ID)
	key := environment.createEndpointKey(t, userID, endpointID, resourceTestKey(byte(discriminator+10)))
	keyID := resourceTestID(t, key.ID)
	operationID, err := db.GenerateOpaqueID("op_")
	if err != nil {
		t.Fatalf("generate recovery operation id: %v", err)
	}
	operationHash := make([]byte, 32)
	operationHash[0] = byte(discriminator)
	payloadHash := make([]byte, 32)
	payloadHash[0] = byte(discriminator + 1)
	checkpoint := key.ID + ":2"
	tx, err := environment.store.DB().BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin recovery seed: %v", err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE model_discovery_evidence
SET state='checking',revision=2,operation_hash=?,safe_class='none',safe_diag='',started_at=?,completed_at=NULL,fetched_count=0
WHERE endpoint_key_id=?`, operationHash, startedAt, keyID); err != nil {
		t.Fatalf("seed checking evidence: %v", err)
	}
	if _, err := tx.Exec(`INSERT INTO accepted_operations(
id,kind,actor_user_id,actor_role,payload_hash,state,checkpoint,last_error_class,created_at,terminal_at)
VALUES(?,'model_discovery',?,'user',?,'accepted',?,NULL,?,NULL)`,
		operationID, userID, payloadHash, checkpoint, startedAt); err != nil {
		t.Fatalf("seed accepted discovery operation: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit recovery seed: %v", err)
	}
	return keyID, operationID
}

func seedOrphanedDiscoveryOperation(
	t *testing.T,
	environment *resourceTestEnvironment,
	userID int64,
	createdAt int64,
) string {
	t.Helper()
	operationID, err := db.GenerateOpaqueID("op_")
	if err != nil {
		t.Fatalf("generate orphan operation id: %v", err)
	}
	payloadHash := make([]byte, 32)
	payloadHash[0] = 0xff
	if _, err := environment.store.DB().Exec(`INSERT INTO accepted_operations(
id,kind,actor_user_id,actor_role,payload_hash,state,checkpoint,last_error_class,created_at,terminal_at)
VALUES(?,'model_discovery',?,'user',?,'accepted','missing:1',NULL,?,NULL)`,
		operationID, userID, payloadHash, createdAt); err != nil {
		t.Fatalf("seed orphan discovery operation: %v", err)
	}
	return operationID
}

func assertDiscoveryRecoveryEvidence(
	t *testing.T,
	environment *resourceTestEnvironment,
	keyID int64,
	wantCompletedAt int64,
) {
	t.Helper()
	var state, safeClass, safeDiag string
	var completedAt int64
	if err := environment.store.DB().QueryRow(`SELECT state,safe_class,safe_diag,completed_at
FROM model_discovery_evidence WHERE endpoint_key_id=?`, keyID).Scan(
		&state, &safeClass, &safeDiag, &completedAt,
	); err != nil {
		t.Fatalf("read recovered evidence: %v", err)
	}
	if state != "failed" || safeClass != "interrupted" || safeDiag != "" || completedAt != wantCompletedAt {
		t.Fatalf("recovered evidence = %q/%q/%q/%d", state, safeClass, safeDiag, completedAt)
	}
}

func assertDiscoveryRecoveryChecking(
	t *testing.T,
	environment *resourceTestEnvironment,
	keyID int64,
) {
	t.Helper()
	var state string
	if err := environment.store.DB().QueryRow(`SELECT state FROM model_discovery_evidence
WHERE endpoint_key_id=?`, keyID).Scan(&state); err != nil || state != "checking" {
		t.Fatalf("evidence after rejected recovery = %q, %v", state, err)
	}
}

func assertDiscoveryRecoveryOperation(
	t *testing.T,
	environment *resourceTestEnvironment,
	operationID string,
	wantState string,
	wantTerminalAt int64,
) {
	t.Helper()
	var state string
	var terminalAt *int64
	if err := environment.store.DB().QueryRow(`SELECT state,terminal_at FROM accepted_operations
WHERE id=?`, operationID).Scan(&state, &terminalAt); err != nil {
		t.Fatalf("read recovery operation: %v", err)
	}
	if state != wantState {
		t.Fatalf("operation %s state=%q, want %q", operationID, state, wantState)
	}
	if wantTerminalAt == 0 {
		if terminalAt != nil {
			t.Fatalf("operation %s terminal_at=%v, want NULL", operationID, terminalAt)
		}
		return
	}
	if terminalAt == nil || *terminalAt != wantTerminalAt {
		t.Fatalf("operation %s terminal_at=%v, want %d", operationID, terminalAt, wantTerminalAt)
	}
}
