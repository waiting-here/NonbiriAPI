package maintenance

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"
)

func insertLifecycleRetentionEvent(
	t *testing.T,
	database *sql.DB,
	actorUserID any,
	actorDiscordID any,
	actorRole any,
	resolvedAt *int64,
	reason string,
) string {
	t.Helper()
	eventID := nextOperationID(t)
	if resolvedAt == nil {
		if _, err := database.Exec(`INSERT INTO maintenance_events(
id,actor_user_id,actor_discord_id,actor_role,action,reason,created_at
) VALUES(?,?,?,?,'enable',?,?)`, eventID, actorUserID, actorDiscordID, actorRole, reason, time.Now().Unix()-1); err != nil {
			t.Fatal(err)
		}
		return eventID
	}
	if _, err := database.Exec(`INSERT INTO maintenance_events(
id,actor_user_id,actor_discord_id,actor_role,action,reason,created_at,
resolved_at,deidentify_at,retain_until
) VALUES(?,?,?,?,'disable',?,?,?,?,?)`,
		eventID, actorUserID, actorDiscordID, actorRole, reason, *resolvedAt-1,
		*resolvedAt, *resolvedAt+actorVisibleDays, *resolvedAt+eventRetentionDays); err != nil {
		t.Fatal(err)
	}
	return eventID
}

func requireMaintenanceActor(
	t *testing.T,
	database *sql.DB,
	eventID string,
	wantIdentity bool,
) {
	t.Helper()
	var userID sql.NullInt64
	var discordID, role sql.NullString
	if err := database.QueryRow(`SELECT actor_user_id,actor_discord_id,actor_role
FROM maintenance_events WHERE id=?`, eventID).Scan(&userID, &discordID, &role); err != nil {
		t.Fatal(err)
	}
	if wantIdentity != (userID.Valid && discordID.Valid && role.Valid) {
		t.Fatalf("maintenance actor %s = user:%+v discord:%+v role:%+v", eventID, userID, discordID, role)
	}
}

func requireMaintenanceEventCount(t *testing.T, database *sql.DB, eventID string, want int) {
	t.Helper()
	var count int
	if err := database.QueryRow(`SELECT COUNT(*) FROM maintenance_events WHERE id=?`, eventID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != want {
		t.Fatalf("maintenance event %s count=%d, want %d", eventID, count, want)
	}
}

func TestLifecycleMaintenanceRetentionBoundariesHoldAndUnresolved(t *testing.T) {
	store := openMaintenanceStore(t)
	owner, err := NewLifecycleRetention(store.DB())
	if err != nil {
		t.Fatal(err)
	}
	steward := insertMaintenancePrincipal(t, store.DB(), "steward-retention", "retention-steward", false, true)
	admin := insertMaintenancePrincipal(t, store.DB(), "", "retention-admin", true, false)
	boundary := time.Now().Unix() - 5
	deadline := time.Now().Add(5 * time.Second)

	actorResolvedAt := boundary - actorVisibleDays
	actorEventID := insertLifecycleRetentionEvent(
		t, store.DB(), steward.ID, "steward-retention", "steward", &actorResolvedAt, "actor boundary",
	)
	before, err := owner.RetainEvents(context.Background(), boundary-1, 100, deadline)
	if err != nil || before != (LifecycleRetentionResult{}) {
		t.Fatalf("maintenance actor before boundary = %+v, %v", before, err)
	}
	requireMaintenanceActor(t, store.DB(), actorEventID, true)
	atBoundary, err := owner.RetainEvents(context.Background(), boundary, 100, deadline)
	if err != nil || atBoundary != (LifecycleRetentionResult{
		ActorsDeidentified: 1, Processed: 1,
	}) {
		t.Fatalf("maintenance actor at boundary = %+v, %v", atBoundary, err)
	}
	requireMaintenanceActor(t, store.DB(), actorEventID, false)
	afterActor, err := owner.RetainEvents(context.Background(), boundary+1, 100, deadline)
	if err != nil || afterActor != (LifecycleRetentionResult{}) {
		t.Fatalf("maintenance actor after boundary = %+v, %v", afterActor, err)
	}

	factResolvedAt := boundary - eventRetentionDays
	factEventID := insertLifecycleRetentionEvent(
		t, store.DB(), nil, nil, nil, &factResolvedAt, "fact boundary",
	)
	beforeFact, err := owner.RetainEvents(context.Background(), boundary-1, 100, deadline)
	if err != nil || beforeFact != (LifecycleRetentionResult{}) {
		t.Fatalf("maintenance fact before boundary = %+v, %v", beforeFact, err)
	}
	requireMaintenanceEventCount(t, store.DB(), factEventID, 1)
	atFact, err := owner.RetainEvents(context.Background(), boundary, 100, deadline)
	if err != nil || atFact != (LifecycleRetentionResult{EventsDeleted: 1, Processed: 1}) {
		t.Fatalf("maintenance fact at boundary = %+v, %v", atFact, err)
	}
	requireMaintenanceEventCount(t, store.DB(), factEventID, 0)

	afterFactID := insertLifecycleRetentionEvent(
		t, store.DB(), nil, nil, nil, &factResolvedAt, "fact after boundary",
	)
	afterFact, err := owner.RetainEvents(context.Background(), boundary+1, 100, deadline)
	if err != nil || afterFact != (LifecycleRetentionResult{EventsDeleted: 1, Processed: 1}) {
		t.Fatalf("maintenance fact after boundary = %+v, %v", afterFact, err)
	}
	requireMaintenanceEventCount(t, store.DB(), afterFactID, 0)

	heldResolvedAt := boundary - eventRetentionDays - 1
	heldID := insertLifecycleRetentionEvent(
		t, store.DB(), steward.ID, "steward-retention", "steward", &heldResolvedAt, "held fact",
	)
	insertMaintenanceLegalHold(t, store.DB(), heldID, admin.ID, boundary-100)
	if _, err := store.DB().Exec(`UPDATE maintenance_events SET legal_hold_consumed=1 WHERE id=?`, heldID); err != nil {
		t.Fatal(err)
	}
	unresolvedID := insertLifecycleRetentionEvent(
		t, store.DB(), steward.ID, "steward-retention", "steward", nil, "unresolved fact",
	)
	heldResult, err := owner.RetainEvents(context.Background(), boundary+1, 100, deadline)
	if err != nil || heldResult != (LifecycleRetentionResult{
		ActorsDeidentified: 1, Processed: 1,
	}) {
		t.Fatalf("held maintenance retention = %+v, %v", heldResult, err)
	}
	requireMaintenanceEventCount(t, store.DB(), heldID, 1)
	requireMaintenanceActor(t, store.DB(), heldID, false)
	requireMaintenanceEventCount(t, store.DB(), unresolvedID, 1)
	requireMaintenanceActor(t, store.DB(), unresolvedID, true)
}

func TestLifecycleMaintenanceRetentionLimitMoreDeadlineAndRollback(t *testing.T) {
	store := openMaintenanceStore(t)
	owner, err := NewLifecycleRetention(store.DB())
	if err != nil {
		t.Fatal(err)
	}
	steward := insertMaintenancePrincipal(t, store.DB(), "steward-bounded", "bounded-steward", false, true)
	decisionNow := time.Now().Unix() - 5
	deadline := time.Now().Add(5 * time.Second)

	for index := 0; index < 3; index++ {
		resolvedAt := decisionNow - actorVisibleDays - 1
		insertLifecycleRetentionEvent(
			t, store.DB(), steward.ID, "steward-bounded", "steward", &resolvedAt, "bounded actor",
		)
	}
	firstActors, err := owner.RetainEvents(context.Background(), decisionNow, 2, deadline)
	if err != nil || firstActors != (LifecycleRetentionResult{
		ActorsDeidentified: 2, Processed: 2, More: true,
	}) {
		t.Fatalf("first bounded maintenance actors = %+v, %v", firstActors, err)
	}
	secondActors, err := owner.RetainEvents(context.Background(), decisionNow, 2, deadline)
	if err != nil || secondActors != (LifecycleRetentionResult{
		ActorsDeidentified: 1, Processed: 1,
	}) {
		t.Fatalf("second bounded maintenance actors = %+v, %v", secondActors, err)
	}

	for index := 0; index < 3; index++ {
		resolvedAt := decisionNow - eventRetentionDays - 1
		insertLifecycleRetentionEvent(t, store.DB(), nil, nil, nil, &resolvedAt, "bounded fact")
	}
	firstFacts, err := owner.RetainEvents(context.Background(), decisionNow, 2, deadline)
	if err != nil || firstFacts != (LifecycleRetentionResult{
		EventsDeleted: 2, Processed: 2, More: true,
	}) {
		t.Fatalf("first bounded maintenance facts = %+v, %v", firstFacts, err)
	}
	secondFacts, err := owner.RetainEvents(context.Background(), decisionNow, 2, deadline)
	if err != nil || secondFacts != (LifecycleRetentionResult{
		EventsDeleted: 1, Processed: 1,
	}) {
		t.Fatalf("second bounded maintenance facts = %+v, %v", secondFacts, err)
	}

	rollbackResolvedAt := decisionNow - actorVisibleDays - 1
	rollbackFirst := insertLifecycleRetentionEvent(
		t, store.DB(), steward.ID, "steward-bounded", "steward", &rollbackResolvedAt, "rollback-ok",
	)
	rollbackSecond := insertLifecycleRetentionEvent(
		t, store.DB(), steward.ID, "steward-bounded", "steward", &rollbackResolvedAt, "rollback-fail",
	)
	expired, err := owner.RetainEvents(
		context.Background(), decisionNow, 2, time.Now().Add(-time.Second),
	)
	if !errors.Is(err, context.DeadlineExceeded) || expired != (LifecycleRetentionResult{}) {
		t.Fatalf("expired maintenance budget = %+v, %v", expired, err)
	}
	requireMaintenanceActor(t, store.DB(), rollbackFirst, true)
	requireMaintenanceActor(t, store.DB(), rollbackSecond, true)

	if _, err := store.DB().Exec(`CREATE TRIGGER test_maintenance_retention_rollback
BEFORE UPDATE OF actor_user_id ON maintenance_events WHEN OLD.reason='rollback-fail'
BEGIN SELECT RAISE(ABORT,'maintenance retention rollback'); END`); err != nil {
		t.Fatal(err)
	}
	rolledBack, err := owner.RetainEvents(context.Background(), decisionNow, 2, deadline)
	if err == nil || rolledBack != (LifecycleRetentionResult{}) {
		t.Fatalf("failed maintenance retention = %+v, %v", rolledBack, err)
	}
	requireMaintenanceActor(t, store.DB(), rollbackFirst, true)
	requireMaintenanceActor(t, store.DB(), rollbackSecond, true)
}
