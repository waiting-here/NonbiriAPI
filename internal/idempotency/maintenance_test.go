package idempotency_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
)

const maintenanceDecisionNow = int64(1_800_000_000)

func maintenanceHash(seed byte) []byte {
	return bytes.Repeat([]byte{seed}, 32)
}

func insertMaintenanceRecord(
	t *testing.T,
	database *sql.DB,
	scope idempotency.Scope,
	state string,
	expiresAt int64,
	seed byte,
) {
	insertMaintenanceRecordWithActor(t, database, scope, state, expiresAt, seed, seed)
}

func insertMaintenanceRecordWithActor(t *testing.T, database *sql.DB, scope idempotency.Scope, state string, expiresAt int64, seed, actorSeed byte) {
	t.Helper()
	status := 0
	body := []byte{}
	if state == "completed" {
		status = 200
		body = []byte(`{"ok":true}`)
	}
	var lookupFingerprint any
	if scope == idempotency.ScopeCredentialReport {
		lookupFingerprint = maintenanceHash(seed + 96)
	}
	_, err := database.Exec(`
INSERT INTO idempotency_records(
 scope,actor_scope_hash,key_hash,request_hash,lookup_fingerprint,
 state,http_status,response_body,created_at,expires_at
) VALUES(?,?,?,?,?,?,?,?,?,?)`,
		string(scope), maintenanceHash(actorSeed), maintenanceHash(seed+32), maintenanceHash(seed+64),
		lookupFingerprint, state, status, body, expiresAt-idempotency.ReplayWindowSeconds, expiresAt,
	)
	if err != nil {
		t.Fatalf("insert %s/%s maintenance row: %v", scope, state, err)
	}
}

func maintenanceRecordExists(t *testing.T, database *sql.DB, scope idempotency.Scope, seed byte) bool {
	t.Helper()
	var exists int
	if err := database.QueryRow(`SELECT EXISTS(
 SELECT 1 FROM idempotency_records WHERE scope=? AND key_hash=?
)`, string(scope), maintenanceHash(seed+32)).Scan(&exists); err != nil {
		t.Fatalf("read %s maintenance row: %v", scope, err)
	}
	return exists == 1
}

func maintenanceDeadline() time.Time {
	return time.Now().Add(5 * time.Second)
}

func TestMaintenanceRecoveryOnlyConvergesInProgressOrExpiredRows(t *testing.T) {
	store := openStore(t)
	maintenance := idempotency.NewMaintenance(store.DB())

	insertMaintenanceRecord(t, store.DB(), idempotency.ScopeControlMutation, "completed", maintenanceDecisionNow+1, 1)
	insertMaintenanceRecord(t, store.DB(), idempotency.ScopeActivity, "accepted", maintenanceDecisionNow+1, 2)
	insertMaintenanceRecord(t, store.DB(), idempotency.ScopeDonation, "completed", maintenanceDecisionNow, 3)
	insertMaintenanceRecord(t, store.DB(), idempotency.ScopeGameRPS, "accepted", maintenanceDecisionNow-1, 4)

	result, err := maintenance.Recover(
		context.Background(), maintenanceDecisionNow, 100, maintenanceDeadline(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if result != (idempotency.MaintenanceResult{Processed: 3, More: false}) {
		t.Fatalf("recovery result = %+v", result)
	}
	if !maintenanceRecordExists(t, store.DB(), idempotency.ScopeControlMutation, 1) {
		t.Fatal("unexpired completed replay was removed")
	}
	for _, row := range []struct {
		scope idempotency.Scope
		seed  byte
	}{
		{idempotency.ScopeActivity, 2},
		{idempotency.ScopeDonation, 3},
		{idempotency.ScopeGameRPS, 4},
	} {
		if maintenanceRecordExists(t, store.DB(), row.scope, row.seed) {
			t.Fatalf("%s recovery row remained", row.scope)
		}
	}
}

func TestMaintenanceRecoveryAcceptedFirstAndStableTieOrder(t *testing.T) {
	store := openStore(t)
	maintenance := idempotency.NewMaintenance(store.DB())
	rows := []struct {
		scope  idempotency.Scope
		state  string
		expiry int64
		seed   byte
	}{
		{idempotency.ScopeActivity, "accepted", maintenanceDecisionNow + 1, 3},
		{idempotency.ScopeActivity, "accepted", maintenanceDecisionNow + 1, 2},
		{idempotency.ScopeActivity, "accepted", maintenanceDecisionNow + 1, 1},
		{idempotency.ScopeDonation, "accepted", maintenanceDecisionNow + 1, 4},
		{idempotency.ScopeDonation, "accepted", maintenanceDecisionNow - 1, 5},
		{idempotency.ScopeActivity, "completed", maintenanceDecisionNow - 10, 6},
		{idempotency.ScopeActivity, "completed", maintenanceDecisionNow, 7},
	}
	for _, row := range rows {
		actorSeed := row.seed
		if row.seed == 3 {
			actorSeed = 2
		}
		insertMaintenanceRecordWithActor(t, store.DB(), row.scope, row.state, row.expiry, row.seed, actorSeed)
	}
	// Two accepted rows share their scope, expiration and actor; key is the
	// final tie-breaker. Another row sorts earlier by actor instead.
	for index, position := range []int{4, 2, 1, 0, 3, 5, 6} {
		result, err := maintenance.Recover(context.Background(), maintenanceDecisionNow, 1, maintenanceDeadline())
		if err != nil || result != (idempotency.MaintenanceResult{Processed: 1, More: index < len(rows)-1}) {
			t.Fatalf("batch %d = (%+v,%v)", index, result, err)
		}
		row := rows[position]
		if maintenanceRecordExists(t, store.DB(), row.scope, row.seed) {
			t.Fatalf("batch %d did not remove expected %s/%d", index, row.scope, row.seed)
		}
	}
}

func TestMaintenanceRetentionUsesExactTwentyFourHourBoundaryAndPreservesReplay(t *testing.T) {
	store := openStore(t)
	maintenance := idempotency.NewMaintenance(store.DB())
	actor, digest := actorAndDigest(t, "/api/models/{id}", struct {
		Enabled bool `json:"enabled"`
	}{Enabled: true})
	createdAt := int64(100)
	expiresAt := createdAt + idempotency.ReplayWindowSeconds

	tx := beginTx(t, store)
	decision, err := idempotency.Begin(context.Background(), tx, idempotency.BeginInput{
		Scope: idempotency.ScopeControlMutation, ActorHash: actor, Key: testKey,
		RequestHash: digest, DecisionNow: createdAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := idempotency.Complete(context.Background(), tx, decision, 200, []byte(`{"revision":"1"}`)); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	result, err := maintenance.Retain(context.Background(), expiresAt-1, 100, maintenanceDeadline())
	if err != nil {
		t.Fatal(err)
	}
	if result != (idempotency.MaintenanceResult{}) {
		t.Fatalf("pre-boundary retention = %+v", result)
	}
	tx = beginTx(t, store)
	replay, err := idempotency.Begin(context.Background(), tx, idempotency.BeginInput{
		Scope: idempotency.ScopeControlMutation, ActorHash: actor, Key: testKey,
		RequestHash: digest, DecisionNow: expiresAt - 1,
	})
	if err != nil || replay.Kind != idempotency.Replay {
		t.Fatalf("pre-boundary replay = (%+v, %v)", replay, err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}

	result, err = maintenance.Retain(context.Background(), expiresAt, 100, maintenanceDeadline())
	if err != nil {
		t.Fatal(err)
	}
	if result != (idempotency.MaintenanceResult{Processed: 1}) {
		t.Fatalf("boundary retention = %+v", result)
	}
	_, changedDigest := actorAndDigest(t, "/api/models/{id}", struct {
		Enabled bool `json:"enabled"`
	}{Enabled: false})
	tx = beginTx(t, store)
	fresh, err := idempotency.Begin(context.Background(), tx, idempotency.BeginInput{
		Scope: idempotency.ScopeControlMutation, ActorHash: actor, Key: testKey,
		RequestHash: changedDigest, DecisionNow: expiresAt,
	})
	if err != nil || fresh.Kind != idempotency.Proceed {
		t.Fatalf("boundary key reuse = (%+v, %v)", fresh, err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
}

func TestMaintenanceRetentionBoundaryMinusEqualPlusOne(t *testing.T) {
	store := openStore(t)
	maintenance := idempotency.NewMaintenance(store.DB())
	insertMaintenanceRecord(t, store.DB(), idempotency.ScopeAnnouncement, "completed", maintenanceDecisionNow-1, 10)
	insertMaintenanceRecord(t, store.DB(), idempotency.ScopeActivity, "completed", maintenanceDecisionNow, 11)
	insertMaintenanceRecord(t, store.DB(), idempotency.ScopeDonation, "completed", maintenanceDecisionNow+1, 12)

	result, err := maintenance.Retain(
		context.Background(), maintenanceDecisionNow, 100, maintenanceDeadline(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if result != (idempotency.MaintenanceResult{Processed: 2}) {
		t.Fatalf("boundary result = %+v", result)
	}
	if maintenanceRecordExists(t, store.DB(), idempotency.ScopeAnnouncement, 10) ||
		maintenanceRecordExists(t, store.DB(), idempotency.ScopeActivity, 11) {
		t.Fatal("expired boundary rows remained")
	}
	if !maintenanceRecordExists(t, store.DB(), idempotency.ScopeDonation, 12) {
		t.Fatal("post-boundary row was removed")
	}
}

func TestMaintenanceCoversEveryCanonicalScope(t *testing.T) {
	scopes := []idempotency.Scope{
		idempotency.ScopeCredentialReport,
		idempotency.ScopeControlMutation,
		idempotency.ScopeOpenAIChatCompletions,
		idempotency.ScopeCharityChatCompletions,
		idempotency.ScopeModelDiscovery,
		idempotency.ScopeMaintenance,
		idempotency.ScopeAnnouncement,
		idempotency.ScopeActivity,
		idempotency.ScopeGameFishing,
		idempotency.ScopeGameLinkLink,
		idempotency.ScopeGameRPS,
		idempotency.ScopeDonation,
		idempotency.ScopeLakeNotes,
		idempotency.ScopePersonalAutomation,
	}
	t.Run("recovery", func(t *testing.T) {
		store := openStore(t)
		maintenance := idempotency.NewMaintenance(store.DB())
		for index, scope := range scopes {
			insertMaintenanceRecord(t, store.DB(), scope, "accepted", maintenanceDecisionNow+1, byte(index+1))
		}
		result, err := maintenance.Recover(
			context.Background(), maintenanceDecisionNow, 100, maintenanceDeadline(),
		)
		if err != nil {
			t.Fatal(err)
		}
		if result != (idempotency.MaintenanceResult{Processed: len(scopes)}) {
			t.Fatalf("all-scope recovery result = %+v", result)
		}
	})

	t.Run("retention", func(t *testing.T) {
		store := openStore(t)
		maintenance := idempotency.NewMaintenance(store.DB())
		for index, scope := range scopes {
			insertMaintenanceRecord(t, store.DB(), scope, "completed", maintenanceDecisionNow, byte(index+1))
		}
		result, err := maintenance.Retain(
			context.Background(), maintenanceDecisionNow, 100, maintenanceDeadline(),
		)
		if err != nil {
			t.Fatal(err)
		}
		if result != (idempotency.MaintenanceResult{Processed: len(scopes)}) {
			t.Fatalf("all-scope retention result = %+v", result)
		}
	})
}

func TestMaintenanceBatchLimitAndExactMore(t *testing.T) {
	t.Run("recovery", func(t *testing.T) {
		store := openStore(t)
		maintenance := idempotency.NewMaintenance(store.DB())
		for seed := byte(1); seed <= 3; seed++ {
			insertMaintenanceRecord(t, store.DB(), idempotency.ScopeGameLinkLink, "accepted", maintenanceDecisionNow+100, seed)
		}
		result, err := maintenance.Recover(
			context.Background(), maintenanceDecisionNow, 2, maintenanceDeadline(),
		)
		if err != nil || result != (idempotency.MaintenanceResult{Processed: 2, More: true}) {
			t.Fatalf("first recovery batch = (%+v, %v)", result, err)
		}
		result, err = maintenance.Recover(
			context.Background(), maintenanceDecisionNow, 2, maintenanceDeadline(),
		)
		if err != nil || result != (idempotency.MaintenanceResult{Processed: 1, More: false}) {
			t.Fatalf("last recovery batch = (%+v, %v)", result, err)
		}
	})

	t.Run("retention exact full batch", func(t *testing.T) {
		store := openStore(t)
		maintenance := idempotency.NewMaintenance(store.DB())
		for seed := byte(1); seed <= 2; seed++ {
			insertMaintenanceRecord(t, store.DB(), idempotency.ScopeModelDiscovery, "completed", maintenanceDecisionNow, seed)
		}
		result, err := maintenance.Retain(
			context.Background(), maintenanceDecisionNow, 2, maintenanceDeadline(),
		)
		if err != nil || result != (idempotency.MaintenanceResult{Processed: 2, More: false}) {
			t.Fatalf("exact full retention batch = (%+v, %v)", result, err)
		}
	})
}

func TestMaintenanceValidatesBoundsPropagatesDeadlineAndDatabaseErrors(t *testing.T) {
	store := openStore(t)
	maintenance := idempotency.NewMaintenance(store.DB())
	for _, testCase := range []struct {
		name     string
		ctx      context.Context
		now      int64
		limit    int
		deadline time.Time
	}{
		{name: "nil context", now: 1, limit: 1, deadline: maintenanceDeadline()},
		{name: "negative decision time", ctx: context.Background(), now: -1, limit: 1, deadline: maintenanceDeadline()},
		{name: "decision time above range", ctx: context.Background(), now: 253402300800, limit: 1, deadline: maintenanceDeadline()},
		{name: "zero limit", ctx: context.Background(), now: 1, limit: 0, deadline: maintenanceDeadline()},
		{name: "over maximum limit", ctx: context.Background(), now: 1, limit: 101, deadline: maintenanceDeadline()},
		{name: "zero deadline", ctx: context.Background(), now: 1, limit: 1},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if _, err := maintenance.Retain(testCase.ctx, testCase.now, testCase.limit, testCase.deadline); err == nil {
				t.Fatal("invalid maintenance input was accepted")
			}
		})
	}
	if result, err := maintenance.Retain(
		context.Background(), maintenanceDecisionNow, 100, maintenanceDeadline(),
	); err != nil || result != (idempotency.MaintenanceResult{}) {
		t.Fatalf("maximum limit result = (%+v, %v)", result, err)
	}

	pastDeadline := time.Now().Add(-time.Second)
	if _, err := maintenance.Recover(
		context.Background(), maintenanceDecisionNow, 1, pastDeadline,
	); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("past deadline error = %v", err)
	}
	if _, err := idempotency.NewMaintenance(nil).Retain(
		context.Background(), maintenanceDecisionNow, 1, maintenanceDeadline(),
	); err == nil {
		t.Fatal("nil database was accepted")
	}

	closedStore := openStore(t)
	closedMaintenance := idempotency.NewMaintenance(closedStore.DB())
	if err := closedStore.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := closedMaintenance.Retain(
		context.Background(), maintenanceDecisionNow, 1, maintenanceDeadline(),
	); err == nil {
		t.Fatal("closed database error was not propagated")
	}
}
