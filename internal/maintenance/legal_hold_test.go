package maintenance

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/db"
)

func insertMaintenanceHeldEvent(
	t *testing.T,
	database *sql.DB,
	actorUserID int64,
	resolvedAt *int64,
) string {
	t.Helper()
	eventID := nextOperationID(t)
	if resolvedAt == nil {
		if _, err := database.Exec(`INSERT INTO maintenance_events(
id,actor_user_id,actor_discord_id,actor_role,action,reason,created_at
) VALUES(?,?,NULL,'admin','enable','held maintenance event',?)`, eventID, actorUserID, int64(1_700_000_000)); err != nil {
			t.Fatalf("insert unresolved maintenance event: %v", err)
		}
		return eventID
	}
	if _, err := database.Exec(`INSERT INTO maintenance_events(
id,actor_user_id,actor_discord_id,actor_role,action,reason,created_at,
resolved_at,deidentify_at,retain_until
) VALUES(?,?,NULL,'admin','disable','held maintenance event',?,?,?,?)`,
		eventID, actorUserID, *resolvedAt-1, *resolvedAt,
		*resolvedAt+actorVisibleDays, *resolvedAt+eventRetentionDays); err != nil {
		t.Fatalf("insert resolved maintenance event: %v", err)
	}
	return eventID
}

func insertMaintenanceLegalHold(
	t *testing.T,
	database *sql.DB,
	eventID string,
	adminUserID, createdAt int64,
) {
	t.Helper()
	holdID, err := db.GenerateOpaqueID("lgh_")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO legal_holds(
id,object_kind,object_ref,state,revision,basis,created_by_user_id,created_at,expires_at
) VALUES(?,'maintenance_event',?,'active',1,'maintenance hold',?,?,?)`,
		holdID, eventID, adminUserID, createdAt, createdAt+3600); err != nil {
		t.Fatalf("insert maintenance legal hold: %v", err)
	}
}

func TestHeldMaintenanceEventDeadlineBoundaries(t *testing.T) {
	store := openMaintenanceStore(t)
	service, _ := newMaintenanceService(t, NewRegistry())
	admin := insertMaintenancePrincipal(t, store.DB(), "", "held-deadline-admin", true, false)
	resolvedAt := int64(1_700_000_000)
	resolvedID := insertMaintenanceHeldEvent(t, store.DB(), admin.ID, &resolvedAt)
	unresolvedID := insertMaintenanceHeldEvent(t, store.DB(), admin.ID, nil)
	deadline := resolvedAt + eventRetentionDays

	tx := beginMaintenanceTx(t, store.DB())
	defer tx.Rollback()
	for _, delta := range []int64{-1, 0, 1} {
		decisionNow := deadline + delta
		state, err := service.InspectForCreate(context.Background(), tx, resolvedID, decisionNow)
		if err != nil {
			t.Fatalf("inspect resolved event at deadline%+d: %v", delta, err)
		}
		if !state.Exists || state.OrdinaryDeadline != deadline || state.LegalHoldConsumed {
			t.Fatalf("resolved state at deadline%+d = %+v", delta, state)
		}
		if got, want := decisionNow < state.OrdinaryDeadline, delta < 0; got != want {
			t.Fatalf("resolved eligibility at deadline%+d = %v, want %v", delta, got, want)
		}
	}
	state, err := service.InspectForCreate(context.Background(), tx, unresolvedID, resolvedAt)
	if err != nil {
		t.Fatal(err)
	}
	if !state.Exists || state.OrdinaryDeadline != maxUnixSecond || state.LegalHoldConsumed {
		t.Fatalf("unresolved state = %+v", state)
	}
}

func TestHeldMaintenanceEventMarkerReadAndDeidentification(t *testing.T) {
	store := openMaintenanceStore(t)
	service, _ := newMaintenanceService(t, NewRegistry())
	admin := insertMaintenancePrincipal(t, store.DB(), "", "held-marker-admin", true, false)
	resolvedAt := int64(1_600_000_000)
	eventID := insertMaintenanceHeldEvent(t, store.DB(), admin.ID, &resolvedAt)
	insertMaintenanceLegalHold(t, store.DB(), eventID, admin.ID, resolvedAt+1)

	tx := beginMaintenanceTx(t, store.DB())
	if err := service.ConsumeMarker(context.Background(), tx, eventID); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := service.ConsumeMarker(context.Background(), tx, eventID); !errors.Is(err, ErrConflict) {
		_ = tx.Rollback()
		t.Fatalf("second marker consumption = %v, want conflict", err)
	}
	if _, err := tx.ExecContext(context.Background(), `DELETE FROM maintenance_events WHERE id=?`, eventID); err == nil {
		_ = tx.Rollback()
		t.Fatal("active hold allowed maintenance event deletion")
	}
	if _, err := tx.ExecContext(context.Background(), `UPDATE maintenance_events
SET actor_user_id=NULL,actor_discord_id=NULL,actor_role=NULL WHERE id=?`, eventID); err != nil {
		_ = tx.Rollback()
		t.Fatalf("deidentify held maintenance event: %v", err)
	}
	exists, err := service.ReadHeld(context.Background(), tx, eventID, resolvedAt+2)
	if err != nil || !exists {
		_ = tx.Rollback()
		t.Fatalf("read deidentified held event = %v, %v", exists, err)
	}
	missingID := nextOperationID(t)
	exists, err = service.ReadHeld(context.Background(), tx, missingID, resolvedAt+2)
	if err != nil || exists {
		_ = tx.Rollback()
		t.Fatalf("read missing held event = %v, %v", exists, err)
	}
	var actorUserID sql.NullInt64
	var actorRole sql.NullString
	var consumed int
	if err := tx.QueryRow(`SELECT actor_user_id,actor_role,legal_hold_consumed
FROM maintenance_events WHERE id=?`, eventID).Scan(&actorUserID, &actorRole, &consumed); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if actorUserID.Valid || actorRole.Valid || consumed != 1 {
		_ = tx.Rollback()
		t.Fatalf("deidentified event actor=%+v role=%+v consumed=%d", actorUserID, actorRole, consumed)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}
