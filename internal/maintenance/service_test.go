package maintenance

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/dbtest"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

const maintenanceTestNow = int64(1_800_100_000)

type maintenancePrincipal struct {
	ID    int64
	Actor authz.Actor
}

func openMaintenanceStore(t *testing.T) *db.Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "maintenance.db")
	master := bytes.Repeat([]byte{0x51}, secret.MasterKeyBytes)
	vault, err := secret.New(master)
	clear(master)
	if err != nil {
		t.Fatalf("new vault: %v", err)
	}
	t.Cleanup(func() { _ = vault.Close() })
	dbtest.EnsureOwnerOnlyParent(t, path)
	store, err := db.Open(path, vault)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	store.DB().SetMaxOpenConns(16)
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func insertMaintenancePrincipal(t *testing.T, database *sql.DB, discord, token string, administrator, steward bool) maintenancePrincipal {
	t.Helper()
	zero := make([]byte, 16)
	revision := make([]byte, 16)
	revision[15] = 1
	adminValue := 0
	if administrator {
		adminValue = 1
	}
	var manualLevel any
	if steward {
		manualLevel = int64(5)
	}
	result, err := database.Exec(`
INSERT INTO users(
 discord_id,username,is_admin,donation_credit_mag,level,auto_level,
 total_requests,total_uncached_input_tokens,total_cache_write_input_tokens,
 total_cache_read_input_tokens,total_output_tokens,total_unknown_usage_requests,
 revision,created_at,updated_at
) VALUES(?,?,?,?,?,4,?,?,?,?,?,?,?,?,?)`,
		discord, "principal-"+discord, adminValue, zero, manualLevel,
		zero, zero, zero, zero, zero, zero, revision, maintenanceTestNow-100, maintenanceTestNow-100,
	)
	if err != nil {
		t.Fatalf("insert principal: %v", err)
	}
	userID, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	generation := "generation-" + discord
	if _, err := database.Exec(`
INSERT INTO sessions(token_hash,user_id,last_seen_at,expires_at,absolute_expires_at,created_at,cred_gen)
VALUES(?,?,?,?,?,?,?)`, token, userID, maintenanceTestNow-10, maintenanceTestNow+600, maintenanceTestNow+1200, maintenanceTestNow-100, generation); err != nil {
		t.Fatalf("insert session: %v", err)
	}
	kind := authz.ActorUserSession
	if administrator {
		kind = authz.ActorAdminSession
	}
	return maintenancePrincipal{ID: userID, Actor: authz.Actor{
		Kind: kind, UserID: userID, SessionTokenHash: token, SessionGeneration: generation,
	}}
}

func newMaintenanceService(t *testing.T, registry *Registry) (*Service, *Gate) {
	t.Helper()
	gate := NewGate()
	authorizer := authz.New(authz.Options{Now: func() time.Time { return time.Unix(maintenanceTestNow, 0) }})
	service, err := NewService(ServiceOptions{
		Authorizer: authorizer,
		Gate:       gate,
		Registry:   registry,
		Now:        func() time.Time { return time.Unix(maintenanceTestNow, 0) },
	})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	return service, gate
}

func nextOperationID(t *testing.T) string {
	t.Helper()
	id, err := db.GenerateOpaqueID("op_")
	if err != nil {
		t.Fatalf("generate operation ID: %v", err)
	}
	return id
}

func beginMaintenanceTx(t *testing.T, database *sql.DB) *sql.Tx {
	t.Helper()
	tx, err := database.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin transaction: %v", err)
	}
	return tx
}

func commitMaintenanceTransition(t *testing.T, database *sql.DB, tx *sql.Tx, transition Transition) {
	t.Helper()
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit transition: %v", err)
	}
	if err := transition.ObserveAfterCommit(context.Background(), database); err != nil {
		t.Fatalf("observe transition: %v", err)
	}
}

func TestMaintenanceTransitionAtomicityUniqueAlertAndRestart(t *testing.T) {
	store := openMaintenanceStore(t)
	admin := insertMaintenancePrincipal(t, store.DB(), "9101", "admin-session", true, false)
	steward := insertMaintenancePrincipal(t, store.DB(), "9102", "steward-session", false, true)
	service, gate := newMaintenanceService(t, NewRegistry())
	initial, err := service.PrepareListener(context.Background(), store.DB())
	if err != nil {
		t.Fatalf("prepare listener: %v", err)
	}
	if !initial.Enabled || initial.Revision != 1 || !gate.Enabled() {
		t.Fatalf("fresh state=%+v gate=%v", initial, gate.Enabled())
	}

	disableID := nextOperationID(t)
	tx := beginMaintenanceTx(t, store.DB())
	disable, err := service.DisableTx(context.Background(), tx, admin.Actor, DisableCommand{
		ExpectedRevision: 1, OperationID: disableID, Reason: "configuration reviewed",
	})
	if err != nil {
		t.Fatalf("disable fresh maintenance: %v", err)
	}
	if !gate.Enabled() {
		t.Fatal("uncommitted disable changed process gate")
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if !gate.Enabled() {
		t.Fatal("commit changed process gate before observation")
	}
	if err := disable.ObserveAfterCommit(context.Background(), store.DB()); err != nil {
		t.Fatal(err)
	}
	if gate.Enabled() {
		t.Fatal("observed disable did not open gate")
	}

	enableID := nextOperationID(t)
	payloadHash := sha256.Sum256([]byte("maintenance enable payload"))
	tx = beginMaintenanceTx(t, store.DB())
	enable, err := service.EnableTx(context.Background(), tx, steward.Actor, EnableCommand{
		ExpectedRevision: 2,
		OperationID:      enableID,
		PayloadHash:      payloadHash,
		Reason:           "urgent maintenance",
		Confirmed:        true,
	})
	if err != nil {
		t.Fatalf("enable maintenance: %v", err)
	}
	if gate.Enabled() {
		t.Fatal("uncommitted enable changed process gate")
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if gate.Enabled() {
		t.Fatal("commit changed process gate before observation")
	}

	restartService, restartGate := newMaintenanceService(t, NewRegistry())
	recovered, err := restartService.PrepareListener(context.Background(), store.DB())
	if err != nil {
		t.Fatalf("restart recovery: %v", err)
	}
	if !recovered.Enabled || recovered.Revision != 3 || recovered.CurrentEventID != enableID || !restartGate.Enabled() {
		t.Fatalf("recovered state=%+v gate=%v", recovered, restartGate.Enabled())
	}
	if err := enable.ObserveAfterCommit(context.Background(), store.DB()); err != nil {
		t.Fatal(err)
	}
	if !gate.Enabled() {
		t.Fatal("observed enable did not close gate")
	}

	var (
		eventAction, eventRole, operationKind, operationState, configValue string
		resolved                                                           sql.NullInt64
		storedHash                                                         []byte
	)
	if err := store.DB().QueryRow(`SELECT action,actor_role,resolved_at FROM maintenance_events WHERE id=?`, enableID).Scan(&eventAction, &eventRole, &resolved); err != nil {
		t.Fatal(err)
	}
	if eventAction != "enable" || eventRole != "steward" || resolved.Valid {
		t.Fatalf("enable event=(%q,%q,%v)", eventAction, eventRole, resolved)
	}
	if err := store.DB().QueryRow(`SELECT kind,state,payload_hash FROM accepted_operations WHERE id=?`, enableID).Scan(&operationKind, &operationState, &storedHash); err != nil {
		t.Fatal(err)
	}
	if operationKind != "maintenance_enable" || operationState != "completed" || !bytes.Equal(storedHash, payloadHash[:]) {
		t.Fatalf("accepted operation=(%q,%q,%x)", operationKind, operationState, storedHash)
	}
	var unresolved int
	if err := store.DB().QueryRow(`SELECT COUNT(*) FROM admin_alerts WHERE kind='maintenance_enabled' AND resolved=0`).Scan(&unresolved); err != nil || unresolved != 1 {
		t.Fatalf("unresolved alerts=(%d,%v)", unresolved, err)
	}
	if err := store.DB().QueryRow(`SELECT value FROM site_config WHERE key='maintenance_mode'`).Scan(&configValue); err != nil || configValue != "1" {
		t.Fatalf("maintenance config=(%q,%v)", configValue, err)
	}

	tx = beginMaintenanceTx(t, store.DB())
	_, err = service.EnableTx(context.Background(), tx, admin.Actor, EnableCommand{
		ExpectedRevision: 2, OperationID: nextOperationID(t), PayloadHash: payloadHash,
		Reason: "stale enable", Confirmed: true,
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("stale enable error=%v", err)
	}
	_ = tx.Rollback()
	var eventCount, alertCount int
	if err := store.DB().QueryRow(`SELECT COUNT(*) FROM maintenance_events WHERE action='enable'`).Scan(&eventCount); err != nil {
		t.Fatal(err)
	}
	if err := store.DB().QueryRow(`SELECT COUNT(*) FROM admin_alerts WHERE kind='maintenance_enabled'`).Scan(&alertCount); err != nil {
		t.Fatal(err)
	}
	if eventCount != 1 || alertCount != 1 {
		t.Fatalf("stale enable changed events/alerts=%d/%d", eventCount, alertCount)
	}

	tx = beginMaintenanceTx(t, store.DB())
	_, err = service.DisableTx(context.Background(), tx, steward.Actor, DisableCommand{
		ExpectedRevision: 3, OperationID: nextOperationID(t), Reason: "not authorized",
	})
	if !errors.Is(err, authz.ErrForbidden) {
		t.Fatalf("steward disable error=%v", err)
	}
	_ = tx.Rollback()

	finalDisableID := nextOperationID(t)
	tx = beginMaintenanceTx(t, store.DB())
	finalDisable, err := service.DisableTx(context.Background(), tx, admin.Actor, DisableCommand{
		ExpectedRevision: 3, OperationID: finalDisableID, Reason: "maintenance complete",
	})
	if err != nil {
		t.Fatal(err)
	}
	commitMaintenanceTransition(t, store.DB(), tx, finalDisable)
	if gate.Enabled() {
		t.Fatal("disable did not reopen process gate")
	}
	var resolvedAt, deidentifyAt, retainUntil int64
	if err := store.DB().QueryRow(`SELECT resolved_at,deidentify_at,retain_until FROM maintenance_events WHERE id=?`, enableID).Scan(&resolvedAt, &deidentifyAt, &retainUntil); err != nil {
		t.Fatal(err)
	}
	if resolvedAt != maintenanceTestNow || deidentifyAt != maintenanceTestNow+actorVisibleDays || retainUntil != maintenanceTestNow+eventRetentionDays {
		t.Fatalf("enable retention=(%d,%d,%d)", resolvedAt, deidentifyAt, retainUntil)
	}
	if err := store.DB().QueryRow(`SELECT COUNT(*) FROM admin_alerts WHERE kind='maintenance_enabled' AND resolved=0`).Scan(&unresolved); err != nil || unresolved != 0 {
		t.Fatalf("resolved alerts=(%d,%v)", unresolved, err)
	}
}

func TestMaintenanceRollbackCannotBeObservedAndFinalAuthWins(t *testing.T) {
	store := openMaintenanceStore(t)
	admin := insertMaintenancePrincipal(t, store.DB(), "9201", "admin-rollback", true, false)
	steward := insertMaintenancePrincipal(t, store.DB(), "9202", "steward-rollback", false, true)
	service, gate := newMaintenanceService(t, NewRegistry())
	if _, err := service.PrepareListener(context.Background(), store.DB()); err != nil {
		t.Fatal(err)
	}

	tx := beginMaintenanceTx(t, store.DB())
	disable, err := service.DisableTx(context.Background(), tx, admin.Actor, DisableCommand{
		ExpectedRevision: 1, OperationID: nextOperationID(t), Reason: "ready",
	})
	if err != nil {
		t.Fatal(err)
	}
	commitMaintenanceTransition(t, store.DB(), tx, disable)

	tx = beginMaintenanceTx(t, store.DB())
	rolledBack, err := service.EnableTx(context.Background(), tx, steward.Actor, EnableCommand{
		ExpectedRevision: 2, OperationID: nextOperationID(t), PayloadHash: sha256.Sum256([]byte("rollback")),
		Reason: "will roll back", Confirmed: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if err := rolledBack.ObserveAfterCommit(context.Background(), store.DB()); err == nil {
		t.Fatal("rolled-back transition was observable")
	}
	if gate.Enabled() {
		t.Fatal("rolled-back transition changed gate")
	}

	if _, err := store.DB().Exec(`UPDATE users SET level=4 WHERE id=?`, steward.ID); err != nil {
		t.Fatal(err)
	}
	tx = beginMaintenanceTx(t, store.DB())
	_, err = service.EnableTx(context.Background(), tx, steward.Actor, EnableCommand{
		ExpectedRevision: 2, OperationID: nextOperationID(t), PayloadHash: sha256.Sum256([]byte("demoted")),
		Reason: "demoted", Confirmed: true,
	})
	if !errors.Is(err, authz.ErrForbidden) {
		t.Fatalf("demoted final auth error=%v", err)
	}
	_ = tx.Rollback()
	var revision int64
	if err := store.DB().QueryRow(`SELECT revision FROM maintenance_state WHERE id=1`).Scan(&revision); err != nil || revision != 2 {
		t.Fatalf("revision after rejected enable=(%d,%v)", revision, err)
	}
}

func TestAuthorizeContinuationRechecksCommittedMaintenanceState(t *testing.T) {
	store := openMaintenanceStore(t)
	admin := insertMaintenancePrincipal(t, store.DB(), "9301", "admin-continuation", true, false)
	registry := NewRegistry()
	if err := registry.Register("accepted_worker", ContinuationRegistration{
		Authority: func(context.Context, *sql.Tx, ContinuationRequest) (bool, error) { return true, nil },
		Snapshot: func(context.Context, *sql.Tx, ContinuationRequest) (ContinuationSnapshot, error) {
			return ContinuationSnapshot{Revision: "1", Payload: []byte(`{"state":"accepted"}`)}, nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	service, gate := newMaintenanceService(t, registry)
	if _, err := service.PrepareListener(context.Background(), store.DB()); err != nil {
		t.Fatal(err)
	}
	request := ContinuationRequest{
		Kind: "accepted_worker", Authority: ContinuationSystem, AcceptedRef: "accepted",
		ResourceRef: "resource", Action: "continue",
	}
	tx := beginMaintenanceTx(t, store.DB())
	if _, err := service.AuthorizeContinuation(context.Background(), tx, request); err != nil {
		t.Fatalf("active continuation: %v", err)
	}
	_ = tx.Rollback()

	tx = beginMaintenanceTx(t, store.DB())
	disable, err := service.DisableTx(context.Background(), tx, admin.Actor, DisableCommand{
		ExpectedRevision: 1, OperationID: nextOperationID(t), Reason: "resume service",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if !gate.Enabled() {
		t.Fatal("test requires the pre-observation gate to remain enabled")
	}
	tx = beginMaintenanceTx(t, store.DB())
	if _, err := service.AuthorizeContinuation(context.Background(), tx, request); !errors.Is(err, ErrNotInMaintenance) {
		t.Fatalf("stale process gate continuation error=%v", err)
	}
	_ = tx.Rollback()
	if err := disable.ObserveAfterCommit(context.Background(), store.DB()); err != nil {
		t.Fatal(err)
	}
}
