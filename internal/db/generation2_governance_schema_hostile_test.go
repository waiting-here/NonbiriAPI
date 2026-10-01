package db

import (
	"database/sql"
	"strings"
	"testing"
)

func TestGenerationTwoHostileAdminAlertKinds(t *testing.T) {
	db := openGenerationTwoConstraintFixture(t)
	valid := []string{
		"fetch_failed",
		"forward_error",
		"registration_rejected",
		"maintenance_enabled",
		"donation_failure_disabled",
		"issue_projection_incomplete",
		"report_retry_exhausted",
		"fishing_retry_exhausted",
		"rps_terminal_retrying",
		"worker_checkpoint_failed",
		"invariant_violation",
	}
	for _, kind := range valid {
		hostileMustExec(t, db, `INSERT INTO admin_alerts(kind,created_at) VALUES(?,0)`, kind)
	}
	hostileMustFail(t, db, `INSERT INTO admin_alerts(kind,created_at) VALUES('unknown',0)`)
	hostileMustFail(t, db, `UPDATE admin_alerts SET kind='unknown' WHERE id=1`)
}

func TestGenerationTwoHostileMaintenanceActorMatrix(t *testing.T) {
	database := openGenerationTwoConstraintFixture(t)
	adminID := hostileInsertUser(t, database, "maintenance-matrix-admin", 1, 0)
	stewardID := hostileInsertUser(t, database, "maintenance-matrix-steward", 0, 0)

	tests := []struct {
		name                        string
		actorUserID, actorDiscordID any
		actorRole                   any
		valid                       bool
	}{
		{name: "deidentified", valid: true},
		{name: "environment admin", actorUserID: adminID, actorRole: "admin", valid: true},
		{name: "steward", actorUserID: stewardID, actorDiscordID: "steward-discord", actorRole: "steward", valid: true},
		{name: "admin carries Discord", actorUserID: adminID, actorDiscordID: "admin-discord", actorRole: "admin"},
		{name: "steward missing Discord", actorUserID: stewardID, actorRole: "steward"},
		{name: "user only", actorUserID: stewardID},
		{name: "user and Discord without role", actorUserID: stewardID, actorDiscordID: "partial-discord"},
		{name: "Discord only", actorDiscordID: "orphan-discord"},
		{name: "role only", actorRole: "admin"},
		{name: "steward without user", actorDiscordID: "orphan-steward", actorRole: "steward"},
		{name: "admin without user but with Discord", actorDiscordID: "orphan-admin", actorRole: "admin"},
		{name: "all non-null unknown role", actorUserID: stewardID, actorDiscordID: "unknown-role", actorRole: "owner"},
		{name: "admin empty Discord", actorUserID: adminID, actorDiscordID: "", actorRole: "admin"},
		{name: "steward empty Discord", actorUserID: stewardID, actorDiscordID: "", actorRole: "steward"},
		{name: "steward role on admin-shaped null Discord", actorUserID: adminID, actorRole: "steward"},
		{name: "admin role on steward-shaped Discord", actorUserID: stewardID, actorDiscordID: "steward-discord", actorRole: "admin"},
	}
	markers := "ABCDEFGHIJKLMNOPQRSTUVWXYZ"
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id := hostileOIDVariant("op_", markers[i], 'Q')
			_, err := database.Exec(`
INSERT INTO maintenance_events(
 id,actor_user_id,actor_discord_id,actor_role,action,reason,created_at,
 resolved_at,deidentify_at,retain_until
) VALUES(?,?,?,?,'enable','actor matrix',100,100,7776100,34560100)`,
				id, tt.actorUserID, tt.actorDiscordID, tt.actorRole)
			if tt.valid && err != nil {
				t.Fatalf("valid maintenance actor rejected: %v", err)
			}
			if !tt.valid && err == nil {
				t.Fatal("hostile maintenance actor accepted")
			}
		})
	}
}

func TestGenerationTwoHostileMaintenanceActorDeidentificationDeadline(t *testing.T) {
	database := openGenerationTwoConstraintFixture(t)
	adminID := hostileInsertUser(t, database, "maintenance-deidentify-admin", 1, 0)
	stewardID := hostileInsertUser(t, database, "maintenance-deidentify-steward", 0, 0)

	var now int64
	if err := database.QueryRow(`SELECT unixepoch()`).Scan(&now); err != nil {
		t.Fatalf("read SQLite clock: %v", err)
	}
	dueResolvedAt := now - 7776001
	futureResolvedAt := now - 7776000 + 3600

	insertResolved := func(t *testing.T, id string, actorUserID int64, actorDiscordID any, actorRole string, resolvedAt int64) {
		t.Helper()
		hostileMustExec(t, database, `
INSERT INTO maintenance_events(
 id,actor_user_id,actor_discord_id,actor_role,action,reason,created_at,
 resolved_at,deidentify_at,retain_until
) VALUES(?,?,?,?,'disable','lifecycle',?,?,?,?)`,
			id, actorUserID, actorDiscordID, actorRole, resolvedAt,
			resolvedAt, resolvedAt+7776000, resolvedAt+34560000)
	}
	assertActorCleared := func(t *testing.T, id string) {
		t.Helper()
		var actorUserID, actorDiscordID, actorRole any
		if err := database.QueryRow(`
SELECT actor_user_id,actor_discord_id,actor_role
FROM maintenance_events WHERE id=?`, id).Scan(&actorUserID, &actorDiscordID, &actorRole); err != nil {
			t.Fatalf("read maintenance actor %s: %v", id, err)
		}
		if actorUserID != nil || actorDiscordID != nil || actorRole != nil {
			t.Fatalf("maintenance actor %s was not fully cleared: user=%v Discord=%v role=%v", id, actorUserID, actorDiscordID, actorRole)
		}
	}

	for _, tt := range []struct {
		name           string
		id             string
		actorUserID    int64
		actorDiscordID any
		actorRole      string
	}{
		{name: "administrator", id: hostileOIDVariant("op_", 'A', 'Q'), actorUserID: adminID, actorRole: "admin"},
		{name: "steward", id: hostileOIDVariant("op_", 'S', 'Q'), actorUserID: stewardID, actorDiscordID: "steward-discord", actorRole: "steward"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			insertResolved(t, tt.id, tt.actorUserID, tt.actorDiscordID, tt.actorRole, dueResolvedAt)
			hostileMustExec(t, database, `
UPDATE maintenance_events
SET actor_user_id=NULL,actor_discord_id=NULL,actor_role=NULL
WHERE id=?`, tt.id)
			assertActorCleared(t, tt.id)
		})
	}

	insertBoundaryEvent := func(t *testing.T, offset int64) string {
		t.Helper()
		id, err := GenerateOpaqueID("op_")
		if err != nil {
			t.Fatalf("generate boundary event ID: %v", err)
		}
		hostileMustExec(t, database, `
INSERT INTO maintenance_events(
 id,actor_user_id,actor_discord_id,actor_role,action,reason,created_at,
 resolved_at,deidentify_at,retain_until
) VALUES(?,?,NULL,'admin','disable','boundary',
 unixepoch()-7776000+?,unixepoch()-7776000+?,unixepoch()+?,unixepoch()+26784000+?)`,
			id, adminID, offset, offset, offset, offset)
		return id
	}

	// SQLite keeps no-argument date/time functions stable within one sqlite3_step.
	// The WHERE predicate therefore proves the trigger observes the exact same
	// equality boundary. A second rollover between INSERT and UPDATE only yields
	// zero affected rows, so retry with a fresh event instead of accepting a
	// timing-dependent result.
	const boundaryAttempts = 32
	matchedEquality := false
	for attempt := 0; attempt < boundaryAttempts; attempt++ {
		id := insertBoundaryEvent(t, 0)
		result, err := database.Exec(`
UPDATE maintenance_events
SET actor_user_id=NULL,actor_discord_id=NULL,actor_role=NULL
WHERE id=? AND unixepoch()=deidentify_at`, id)
		if err != nil {
			t.Fatalf("actor clear at exact deidentify deadline was rejected: %v", err)
		}
		rows, err := result.RowsAffected()
		if err != nil {
			t.Fatalf("observe exact-deadline actor clear: %v", err)
		}
		if rows == 1 {
			assertActorCleared(t, id)
			matchedEquality = true
			break
		}
		if rows != 0 {
			t.Fatalf("exact-deadline actor clear changed %d rows", rows)
		}
		hostileMustExec(t, database, `DELETE FROM maintenance_events WHERE id=?`, id)
	}
	if !matchedEquality {
		t.Fatal("could not exercise unixepoch()=deidentify_at boundary")
	}

	// Use the same statement-stable clock to prove the strict -1 second side of
	// the boundary. A rollover before this UPDATE is likewise a zero-row retry.
	matchedBeforeDeadline := false
	for attempt := 0; attempt < boundaryAttempts; attempt++ {
		id := insertBoundaryEvent(t, 1)
		result, err := database.Exec(`
UPDATE maintenance_events
SET actor_user_id=NULL,actor_discord_id=NULL,actor_role=NULL
WHERE id=? AND unixepoch()=deidentify_at-1`, id)
		if err != nil {
			if !strings.Contains(err.Error(), "maintenance event is append-only") {
				t.Fatalf("actor clear one second before deadline failed unexpectedly: %v", err)
			}
			var actorUserID int64
			var actorDiscordID sql.NullString
			var actorRole string
			if scanErr := database.QueryRow(`
SELECT actor_user_id,actor_discord_id,actor_role
FROM maintenance_events WHERE id=?`, id).Scan(&actorUserID, &actorDiscordID, &actorRole); scanErr != nil {
				t.Fatalf("read one-second-before actor after rejection: %v", scanErr)
			}
			if actorUserID != adminID || actorDiscordID.Valid || actorRole != "admin" {
				t.Fatalf("one-second-before rejection changed actor: user=%d Discord=%v role=%q", actorUserID, actorDiscordID, actorRole)
			}
			matchedBeforeDeadline = true
			break
		}
		rows, rowsErr := result.RowsAffected()
		if rowsErr != nil {
			t.Fatalf("observe one-second-before actor clear: %v", rowsErr)
		}
		if rows == 1 {
			t.Fatal("actor clear one second before deidentify deadline was accepted")
		}
		if rows != 0 {
			t.Fatalf("one-second-before actor clear changed %d rows", rows)
		}
		hostileMustExec(t, database, `DELETE FROM maintenance_events WHERE id=?`, id)
	}
	if !matchedBeforeDeadline {
		t.Fatal("could not exercise unixepoch()=deidentify_at-1 boundary")
	}

	futureID := hostileOIDVariant("op_", 'F', 'Q')
	insertResolved(t, futureID, stewardID, "future-steward", "steward", futureResolvedAt)
	hostileMustFail(t, database, `
UPDATE maintenance_events
SET actor_user_id=NULL,actor_discord_id=NULL,actor_role=NULL
WHERE id=?`, futureID)

	immutableID := hostileOIDVariant("op_", 'I', 'Q')
	insertResolved(t, immutableID, adminID, nil, "admin", dueResolvedAt)
	hostileMustFail(t, database, `
UPDATE maintenance_events
SET actor_user_id=NULL,actor_discord_id=NULL,actor_role=NULL,reason='rewritten'
WHERE id=?`, immutableID)

	heldID := hostileOIDVariant("op_", 'H', 'Q')
	insertResolved(t, heldID, stewardID, "held-steward", "steward", dueResolvedAt)
	holdID := hostileOIDVariant("lgh_", 'H', 'Q')
	hostileMustExec(t, database, `
INSERT INTO legal_holds(
 id,object_kind,object_ref,state,revision,basis,created_by_user_id,created_at,expires_at
) VALUES(?,'maintenance_event',?,'active',1,'lifecycle hold',?,?,?)`,
		holdID, heldID, adminID, now-10, now+3600)
	hostileMustExec(t, database, `UPDATE maintenance_events SET legal_hold_consumed=1 WHERE id=?`, heldID)
	hostileMustExec(t, database, `
UPDATE maintenance_events
SET actor_user_id=NULL,actor_discord_id=NULL,actor_role=NULL
WHERE id=?`, heldID)
	assertActorCleared(t, heldID)
	var marker int
	var holdState string
	if err := database.QueryRow(`SELECT legal_hold_consumed FROM maintenance_events WHERE id=?`, heldID).Scan(&marker); err != nil {
		t.Fatalf("read held maintenance marker: %v", err)
	}
	if err := database.QueryRow(`SELECT state FROM legal_holds WHERE id=?`, holdID).Scan(&holdState); err != nil {
		t.Fatalf("read active legal hold: %v", err)
	}
	if marker != 1 || holdState != "active" {
		t.Fatalf("actor clear changed legal hold facts: marker=%d state=%q", marker, holdState)
	}
}

func TestGenerationTwoHostileAnnouncementAuditRevisionMatrix(t *testing.T) {
	db := openGenerationTwoConstraintFixture(t)
	adminID := hostileInsertUser(t, db, "announcement-revisions", 1, 0)
	hostileInsertAnnouncementAudit(t, db, adminID, 0)

	insertAudit := func(id, action string, from, to int64) {
		t.Helper()
		auditID := hostileNextPK64(t, db, "announcement_audits")
		hostileMustExec(t, db, `
INSERT INTO announcement_audits(
 id,announcement_id_text,actor_user_id,action,from_revision,to_revision,reason,
 created_at,actor_deidentify_at
) VALUES(?,?,?,?,?,?,'hostile',0,7776000)`, auditID, id, adminID, action, from, to)
	}
	insertAudit(hostileOIDVariant("ann_", 'E', 'Q'), "edit", 1, 2)
	insertAudit(hostileOIDVariant("ann_", 'P', 'Q'), "publish", 2, 3)
	hostileMustFail(t, db, `
INSERT INTO announcement_audits(
 announcement_id_text,actor_user_id,action,from_revision,to_revision,reason,
 created_at,actor_deidentify_at
) VALUES(? ,? ,'edit',0,1,'hostile',0,7776000)`, hostileOIDVariant("ann_", 'Z', 'Q'), adminID)
	hostileMustFail(t, db, `
INSERT INTO announcement_audits(
 announcement_id_text,actor_user_id,action,from_revision,to_revision,reason,
 created_at,actor_deidentify_at
) VALUES(? ,? ,'publish',2,2,'hostile',0,7776000)`, hostileOIDVariant("ann_", 'Y', 'Q'), adminID)
	hostileMustFail(t, db, `
INSERT INTO announcement_audits(
 announcement_id_text,actor_user_id,action,from_revision,to_revision,reason,
 created_at,actor_deidentify_at
) VALUES(? ,? ,'withdraw',3,2,'hostile',0,7776000)`, hostileOIDVariant("ann_", 'X', 'Q'), adminID)
}
