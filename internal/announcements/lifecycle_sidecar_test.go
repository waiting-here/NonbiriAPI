package announcements

import (
	"context"
	"database/sql"
	"strconv"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/db"
)

func TestAnnouncementLifecycleAuditRetentionUsesFrozenTimeAndOneBudget(t *testing.T) {
	environment := newAnnouncementTestEnvironment(t)
	adminID := environment.seedUser(t, "", "en", true)
	decisionNow := announcementTestNow
	actorAuditAnnouncement, err := db.GenerateOpaqueID("ann_")
	if err != nil {
		t.Fatalf("GenerateOpaqueID actor audit: %v", err)
	}
	actorCreatedAt := decisionNow - announcementAuditActorTTL
	actorResult, err := environment.store.DB().Exec(`
INSERT INTO announcement_audits(
 announcement_id_text,actor_user_id,action,from_revision,to_revision,reason,created_at,actor_deidentify_at
) VALUES(?,?,'create',0,1,'',?,?)`,
		actorAuditAnnouncement, adminID, actorCreatedAt, decisionNow)
	if err != nil {
		t.Fatalf("insert actor-deidentification audit: %v", err)
	}
	actorAuditID, err := actorResult.LastInsertId()
	if err != nil {
		t.Fatalf("actor audit id: %v", err)
	}

	oldAuditAnnouncement, err := db.GenerateOpaqueID("ann_")
	if err != nil {
		t.Fatalf("GenerateOpaqueID old audit: %v", err)
	}
	oldCreatedAt := decisionNow - announcementAuditTTL
	oldResult, err := environment.store.DB().Exec(`
INSERT INTO announcement_audits(
 announcement_id_text,actor_user_id,action,from_revision,to_revision,reason,created_at,actor_deidentify_at
) VALUES(?,NULL,'delete',1,2,'retained fact',?,?)`,
		oldAuditAnnouncement, oldCreatedAt, oldCreatedAt+announcementAuditActorTTL)
	if err != nil {
		t.Fatalf("insert retained audit: %v", err)
	}
	oldAuditID, err := oldResult.LastInsertId()
	if err != nil {
		t.Fatalf("old audit id: %v", err)
	}

	environment.clock.Store(decisionNow + announcementAuditTTL)
	before, err := environment.repository.RetainLifecycleAudits(
		context.Background(), decisionNow-1, 1, time.Now().Add(time.Second),
	)
	if err != nil || before != (LifecycleAuditRetentionResult{}) {
		t.Fatalf("retention before frozen boundary: result=%+v err=%v", before, err)
	}
	var actor sql.NullInt64
	if err := environment.store.DB().QueryRow(`SELECT actor_user_id FROM announcement_audits WHERE id=?`, actorAuditID).Scan(&actor); err != nil {
		t.Fatalf("read actor audit before boundary: %v", err)
	}
	if !actor.Valid || actor.Int64 != adminID {
		t.Fatalf("repository clock leaked into frozen decision: actor=%+v", actor)
	}

	deidentified, err := environment.repository.RetainLifecycleAudits(
		context.Background(), decisionNow, 1, time.Now().Add(time.Second),
	)
	if err != nil || deidentified != (LifecycleAuditRetentionResult{
		ActorsDeidentified: 1, Processed: 1, More: true,
	}) {
		t.Fatalf("actor deidentification pass: result=%+v err=%v", deidentified, err)
	}
	if err := environment.store.DB().QueryRow(`SELECT actor_user_id FROM announcement_audits WHERE id=?`, actorAuditID).Scan(&actor); err != nil {
		t.Fatalf("read deidentified actor audit: %v", err)
	}
	if actor.Valid {
		t.Fatalf("actor identity survived exact boundary: %+v", actor)
	}

	deleted, err := environment.repository.RetainLifecycleAudits(
		context.Background(), decisionNow, 1, time.Now().Add(time.Second),
	)
	if err != nil || deleted != (LifecycleAuditRetentionResult{
		AuditsDeleted: 1, Processed: 1, More: true,
	}) {
		t.Fatalf("audit fact deletion pass: result=%+v err=%v", deleted, err)
	}
	var count int
	if err := environment.store.DB().QueryRow(`SELECT count(*) FROM announcement_audits WHERE id=?`, oldAuditID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("retained audit fact count=%d err=%v", count, err)
	}
	if err := environment.store.DB().QueryRow(`SELECT count(*) FROM announcement_audits WHERE id=?`, actorAuditID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("newer deidentified audit count=%d err=%v", count, err)
	}
}

func TestAnnouncementLifecycleHeldAuditDoesNotRequireContent(t *testing.T) {
	environment := newAnnouncementTestEnvironment(t)
	adminID := environment.seedUser(t, "", "en", true)
	announcementID, err := db.GenerateOpaqueID("ann_")
	if err != nil {
		t.Fatalf("GenerateOpaqueID announcement audit: %v", err)
	}
	result, err := environment.store.DB().Exec(`
INSERT INTO announcement_audits(
 announcement_id_text,actor_user_id,action,from_revision,to_revision,reason,created_at,actor_deidentify_at
) VALUES(?,?,'create',0,1,'audit only',?,?)`,
		announcementID, adminID, announcementTestNow, announcementTestNow+announcementAuditActorTTL)
	if err != nil {
		t.Fatalf("insert content-free announcement audit: %v", err)
	}
	auditID, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("announcement audit id: %v", err)
	}
	tx, err := environment.store.DB().BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin announcement hold transaction: %v", err)
	}
	defer tx.Rollback()
	state, err := environment.repository.InspectLifecycleHeldAudit(
		context.Background(), tx, auditID, announcementTestNow+1,
	)
	if err != nil || !state.Exists || state.LegalHoldConsumed ||
		state.OrdinaryDeadline != announcementTestNow+announcementAuditTTL {
		t.Fatalf("initial announcement audit state=%+v err=%v", state, err)
	}
	holdID, err := db.GenerateOpaqueID("lgh_")
	if err != nil {
		t.Fatalf("GenerateOpaqueID legal hold: %v", err)
	}
	if _, err := tx.Exec(`INSERT INTO legal_holds(
id,object_kind,object_ref,state,revision,basis,created_by_user_id,created_at,expires_at)
VALUES(?,'announcement_audit',?,'active',1,'audit fixture',?,?,?)`,
		holdID, strconv.FormatInt(auditID, 10), adminID, announcementTestNow+1, announcementTestNow+3600); err != nil {
		t.Fatalf("insert announcement audit hold: %v", err)
	}
	if err := environment.repository.ConsumeLifecycleHeldAuditMarker(
		context.Background(), tx, auditID,
	); err != nil {
		t.Fatalf("ConsumeLifecycleHeldAuditMarker: %v", err)
	}
	exists, err := environment.repository.ReadLifecycleHeldAudit(
		context.Background(), tx, auditID, announcementTestNow+1,
	)
	if err != nil || !exists {
		t.Fatalf("ReadLifecycleHeldAudit without announcement content: exists=%v err=%v", exists, err)
	}
	state, err = environment.repository.InspectLifecycleHeldAudit(
		context.Background(), tx, auditID, announcementTestNow+1,
	)
	if err != nil || !state.LegalHoldConsumed {
		t.Fatalf("consumed announcement audit state=%+v err=%v", state, err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit announcement hold transaction: %v", err)
	}
}
