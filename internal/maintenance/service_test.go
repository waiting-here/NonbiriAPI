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
	var discordValue any = discord
	if discord == "" {
		discordValue = nil
	}
	result, err := database.Exec(`
INSERT INTO users(
 discord_id,username,is_admin,donation_credit_mag,level,auto_level,
 total_requests,total_uncached_input_tokens,total_cache_write_input_tokens,
 total_cache_read_input_tokens,total_output_tokens,total_unknown_usage_requests,
 revision,created_at,updated_at
) VALUES(?,?,?,?,?,4,?,?,?,?,?,?,?,?,?)`,
		discordValue, "principal-"+token, adminValue, zero, manualLevel,
		zero, zero, zero, zero, zero, zero, revision, maintenanceTestNow-100, maintenanceTestNow-100,
	)
	if err != nil {
		t.Fatalf("insert principal: %v", err)
	}
	userID, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	generation := "generation-" + token
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

func TestChatAcceptanceUsesCallerTransactionAndIgnoresProcessProjection(t *testing.T) {
	store := openMaintenanceStore(t)
	service, gate := newMaintenanceService(t, NewRegistry())
	if gate.Ready() {
		t.Fatal("test gate unexpectedly initialized")
	}
	tx := beginMaintenanceTx(t, store.DB())
	if err := service.AuthorizeChatAcceptance(context.Background(), tx, 7, maintenanceTestNow); !errors.Is(err, ErrMaintenanceOn) {
		_ = tx.Rollback()
		t.Fatalf("fresh maintenance acceptance error = %v, want enabled", err)
	}
	if _, err := tx.Exec(`UPDATE maintenance_state SET enabled=0,revision=revision+1,changed_at=? WHERE id=1`, maintenanceTestNow); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if _, err := tx.Exec(`UPDATE site_config SET value='0',updated_at=? WHERE key='maintenance_mode'`, maintenanceTestNow); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := service.AuthorizeChatAcceptance(context.Background(), tx, 7, maintenanceTestNow); err != nil {
		_ = tx.Rollback()
		t.Fatalf("transaction-local open acceptance: %v", err)
	}
	_ = tx.Rollback()
}

func TestChatAcceptanceRejectsMirrorMismatchAndInvalidInputs(t *testing.T) {
	store := openMaintenanceStore(t)
	service, _ := newMaintenanceService(t, NewRegistry())
	tx := beginMaintenanceTx(t, store.DB())
	if _, err := tx.Exec(`UPDATE site_config SET value='0',updated_at=? WHERE key='maintenance_mode'`, maintenanceTestNow); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := service.AuthorizeChatAcceptance(context.Background(), tx, 1, maintenanceTestNow); !errors.Is(err, ErrInvariant) {
		_ = tx.Rollback()
		t.Fatalf("mirror mismatch error = %v, want invariant", err)
	}
	_ = tx.Rollback()

	if err := service.AuthorizeChatAcceptance(context.Background(), nil, 1, maintenanceTestNow); !errors.Is(err, ErrInvalidMutation) {
		t.Fatalf("nil transaction error = %v", err)
	}
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

type maintenanceFactsQueryer interface {
	QueryRow(query string, args ...any) *sql.Row
}

type maintenanceMutationFacts struct {
	Enabled                 int
	Revision                int64
	ChangedAt               int64
	CurrentEventID          sql.NullString
	ConfigValue             string
	ConfigUpdatedAt         int64
	ConfigRevision          int64
	ConfigRevisionUpdatedAt int64
	EventCount              int
	ResolvedEventCount      int
	AcceptedOperationCount  int
	AlertCount              int
	ResolvedAlertCount      int
}

func readMaintenanceMutationFacts(t *testing.T, q maintenanceFactsQueryer) maintenanceMutationFacts {
	t.Helper()
	var facts maintenanceMutationFacts
	if err := q.QueryRow(`SELECT enabled,revision,changed_at,current_event_id FROM maintenance_state WHERE id=1`).Scan(
		&facts.Enabled, &facts.Revision, &facts.ChangedAt, &facts.CurrentEventID,
	); err != nil {
		t.Fatalf("read maintenance state facts: %v", err)
	}
	if err := q.QueryRow(`SELECT value,updated_at FROM site_config WHERE key='maintenance_mode'`).Scan(
		&facts.ConfigValue, &facts.ConfigUpdatedAt,
	); err != nil {
		t.Fatalf("read maintenance config facts: %v", err)
	}
	if err := q.QueryRow(`SELECT revision,updated_at FROM config_revisions WHERE domain='site'`).Scan(
		&facts.ConfigRevision, &facts.ConfigRevisionUpdatedAt,
	); err != nil {
		t.Fatalf("read maintenance config revision facts: %v", err)
	}
	if err := q.QueryRow(`SELECT COUNT(*),COALESCE(SUM(resolved_at IS NOT NULL),0) FROM maintenance_events`).Scan(
		&facts.EventCount, &facts.ResolvedEventCount,
	); err != nil {
		t.Fatalf("read maintenance event facts: %v", err)
	}
	if err := q.QueryRow(`SELECT COUNT(*) FROM accepted_operations WHERE kind='maintenance_enable'`).Scan(
		&facts.AcceptedOperationCount,
	); err != nil {
		t.Fatalf("read maintenance operation facts: %v", err)
	}
	if err := q.QueryRow(`SELECT COUNT(*),COALESCE(SUM(resolved),0) FROM admin_alerts WHERE kind='maintenance_enabled'`).Scan(
		&facts.AlertCount, &facts.ResolvedAlertCount,
	); err != nil {
		t.Fatalf("read maintenance alert facts: %v", err)
	}
	return facts
}

func TestMaintenanceAdministratorUsesNullDiscordActorSnapshot(t *testing.T) {
	store := openMaintenanceStore(t)
	admin := insertMaintenancePrincipal(t, store.DB(), "", "admin-null-discord", true, false)
	service, gate := newMaintenanceService(t, NewRegistry())
	if _, err := service.PrepareListener(context.Background(), store.DB()); err != nil {
		t.Fatalf("prepare listener: %v", err)
	}

	var adminDiscord sql.NullString
	if err := store.DB().QueryRow(`SELECT discord_id FROM users WHERE id=?`, admin.ID).Scan(&adminDiscord); err != nil {
		t.Fatalf("read administrator identity: %v", err)
	}
	if adminDiscord.Valid {
		t.Fatalf("administrator fixture has Discord identity %q", adminDiscord.String)
	}

	disableID := nextOperationID(t)
	tx := beginMaintenanceTx(t, store.DB())
	disable, err := service.DisableTx(context.Background(), tx, admin.Actor, DisableCommand{
		ExpectedRevision: 1, OperationID: disableID, Reason: "fresh configuration reviewed",
	})
	if err != nil {
		t.Fatalf("disable fresh maintenance: %v", err)
	}
	commitMaintenanceTransition(t, store.DB(), tx, disable)
	if gate.Enabled() {
		t.Fatal("administrator disable did not open the gate")
	}

	var (
		disableActorID      int64
		disableActorDiscord sql.NullString
		disableActorRole    string
		disableAction       string
	)
	if err := store.DB().QueryRow(`
SELECT actor_user_id,actor_discord_id,actor_role,action
FROM maintenance_events WHERE id=?`, disableID).Scan(
		&disableActorID, &disableActorDiscord, &disableActorRole, &disableAction,
	); err != nil {
		t.Fatalf("read administrator disable event: %v", err)
	}
	if disableActorID != admin.ID || disableActorDiscord.Valid || disableActorRole != "admin" || disableAction != "disable" {
		t.Fatalf("administrator disable actor=(%d,%v,%q,%q)", disableActorID, disableActorDiscord, disableActorRole, disableAction)
	}

	enableID := nextOperationID(t)
	payloadHash := sha256.Sum256([]byte("administrator maintenance enable"))
	tx = beginMaintenanceTx(t, store.DB())
	enable, err := service.EnableTx(context.Background(), tx, admin.Actor, EnableCommand{
		ExpectedRevision: 2,
		OperationID:      enableID,
		PayloadHash:      payloadHash,
		Reason:           "administrator safety check",
		Confirmed:        true,
	})
	if err != nil {
		t.Fatalf("administrator enable: %v", err)
	}
	commitMaintenanceTransition(t, store.DB(), tx, enable)
	if !gate.Enabled() {
		t.Fatal("administrator enable did not close the gate")
	}

	var (
		enableActorID      int64
		enableActorDiscord sql.NullString
		enableActorRole    string
		enableAction       string
		operationActorID   int64
		operationRole      string
		operationState     string
		storedPayloadHash  []byte
	)
	if err := store.DB().QueryRow(`
SELECT actor_user_id,actor_discord_id,actor_role,action
FROM maintenance_events WHERE id=?`, enableID).Scan(
		&enableActorID, &enableActorDiscord, &enableActorRole, &enableAction,
	); err != nil {
		t.Fatalf("read administrator enable event: %v", err)
	}
	if enableActorID != admin.ID || enableActorDiscord.Valid || enableActorRole != "admin" || enableAction != "enable" {
		t.Fatalf("administrator enable actor=(%d,%v,%q,%q)", enableActorID, enableActorDiscord, enableActorRole, enableAction)
	}
	if err := store.DB().QueryRow(`
SELECT actor_user_id,actor_role,state,payload_hash
FROM accepted_operations WHERE id=?`, enableID).Scan(
		&operationActorID, &operationRole, &operationState, &storedPayloadHash,
	); err != nil {
		t.Fatalf("read administrator accepted operation: %v", err)
	}
	if operationActorID != admin.ID || operationRole != "admin" || operationState != "completed" || !bytes.Equal(storedPayloadHash, payloadHash[:]) {
		t.Fatalf("administrator operation=(%d,%q,%q,%x)", operationActorID, operationRole, operationState, storedPayloadHash)
	}

	var (
		stateEnabled, alertResolved int
		stateRevision               int64
		stateEventID                sql.NullString
		configValue                 string
		configRevision              int64
		alertRef                    sql.NullString
		alertSubject                sql.NullInt64
	)
	if err := store.DB().QueryRow(`SELECT enabled,revision,current_event_id FROM maintenance_state WHERE id=1`).Scan(
		&stateEnabled, &stateRevision, &stateEventID,
	); err != nil {
		t.Fatalf("read administrator maintenance state: %v", err)
	}
	if err := store.DB().QueryRow(`SELECT value FROM site_config WHERE key='maintenance_mode'`).Scan(&configValue); err != nil {
		t.Fatalf("read administrator maintenance mirror: %v", err)
	}
	if err := store.DB().QueryRow(`SELECT revision FROM config_revisions WHERE domain='site'`).Scan(&configRevision); err != nil {
		t.Fatalf("read administrator maintenance config revision: %v", err)
	}
	if err := store.DB().QueryRow(`
SELECT ref,subject_user_id,resolved FROM admin_alerts
WHERE kind='maintenance_enabled' AND ref=?`, enableID).Scan(&alertRef, &alertSubject, &alertResolved); err != nil {
		t.Fatalf("read administrator primary alert: %v", err)
	}
	if stateEnabled != 1 || stateRevision != 3 || !stateEventID.Valid || stateEventID.String != enableID ||
		configValue != "1" || configRevision != 3 || !alertRef.Valid || alertRef.String != enableID ||
		!alertSubject.Valid || alertSubject.Int64 != admin.ID || alertResolved != 0 {
		t.Fatalf("administrator atomic facts state=(%d,%d,%v) config=(%q,%d) alert=(%v,%v,%d)",
			stateEnabled, stateRevision, stateEventID, configValue, configRevision, alertRef, alertSubject, alertResolved)
	}
}

func TestMaintenanceTransitionAtomicityUniqueAlertAndRestart(t *testing.T) {
	store := openMaintenanceStore(t)
	admin := insertMaintenancePrincipal(t, store.DB(), "", "admin-session", true, false)
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
		eventActorID                                                       int64
		eventDiscord                                                       sql.NullString
		storedHash                                                         []byte
	)
	if err := store.DB().QueryRow(`
SELECT action,actor_user_id,actor_discord_id,actor_role,resolved_at
FROM maintenance_events WHERE id=?`, enableID).Scan(&eventAction, &eventActorID, &eventDiscord, &eventRole, &resolved); err != nil {
		t.Fatal(err)
	}
	if eventAction != "enable" || eventActorID != steward.ID || !eventDiscord.Valid || eventDiscord.String != "9102" || eventRole != "steward" || resolved.Valid {
		t.Fatalf("enable event=(%q,%d,%v,%q,%v)", eventAction, eventActorID, eventDiscord, eventRole, resolved)
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

	beforeRejectedDisable := readMaintenanceMutationFacts(t, store.DB())
	tx = beginMaintenanceTx(t, store.DB())
	_, err = service.DisableTx(context.Background(), tx, steward.Actor, DisableCommand{
		ExpectedRevision: 3, OperationID: nextOperationID(t), Reason: "not authorized",
	})
	if !errors.Is(err, authz.ErrForbidden) {
		t.Fatalf("steward disable error=%v", err)
	}
	if after := readMaintenanceMutationFacts(t, tx); after != beforeRejectedDisable {
		t.Fatalf("steward disable wrote maintenance facts: before=%+v after=%+v", beforeRejectedDisable, after)
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
	var (
		finalActorID      int64
		finalActorDiscord sql.NullString
		finalActorRole    string
		finalResolvedAt   int64
		finalDeidentifyAt int64
		finalRetainUntil  int64
	)
	if err := store.DB().QueryRow(`
SELECT actor_user_id,actor_discord_id,actor_role,resolved_at,deidentify_at,retain_until
FROM maintenance_events WHERE id=?`, finalDisableID).Scan(
		&finalActorID, &finalActorDiscord, &finalActorRole, &finalResolvedAt, &finalDeidentifyAt, &finalRetainUntil,
	); err != nil {
		t.Fatalf("read administrator disable after steward enable: %v", err)
	}
	if finalActorID != admin.ID || finalActorDiscord.Valid || finalActorRole != "admin" ||
		finalResolvedAt != maintenanceTestNow || finalDeidentifyAt != maintenanceTestNow+actorVisibleDays || finalRetainUntil != maintenanceTestNow+eventRetentionDays {
		t.Fatalf("administrator disable actor/retention=(%d,%v,%q,%d,%d,%d)",
			finalActorID, finalActorDiscord, finalActorRole, finalResolvedAt, finalDeidentifyAt, finalRetainUntil)
	}
}

func TestMaintenanceRejectsInvalidAdministratorActorsWithoutWrites(t *testing.T) {
	tests := []struct {
		name  string
		actor func(t *testing.T, database *sql.DB, admin, user, steward maintenancePrincipal) authz.Actor
		want  error
	}{
		{
			name: "unknown session",
			actor: func(_ *testing.T, _ *sql.DB, admin, _, _ maintenancePrincipal) authz.Actor {
				actor := admin.Actor
				actor.SessionTokenHash = "missing-admin-session"
				return actor
			},
			want: authz.ErrUnauthorized,
		},
		{
			name: "expired session",
			actor: func(t *testing.T, database *sql.DB, admin, _, _ maintenancePrincipal) authz.Actor {
				t.Helper()
				if _, err := database.Exec(`UPDATE sessions SET expires_at=? WHERE token_hash=?`, maintenanceTestNow, admin.Actor.SessionTokenHash); err != nil {
					t.Fatalf("expire administrator session: %v", err)
				}
				return admin.Actor
			},
			want: authz.ErrUnauthorized,
		},
		{
			name: "credential generation mismatch",
			actor: func(_ *testing.T, _ *sql.DB, admin, _, _ maintenancePrincipal) authz.Actor {
				actor := admin.Actor
				actor.SessionGeneration = "mismatched-generation"
				return actor
			},
			want: authz.ErrUnauthorized,
		},
		{
			name: "ordinary user",
			actor: func(_ *testing.T, _ *sql.DB, _, user, _ maintenancePrincipal) authz.Actor {
				return user.Actor
			},
			want: authz.ErrForbidden,
		},
		{
			name: "steward role",
			actor: func(_ *testing.T, _ *sql.DB, _, _, steward maintenancePrincipal) authz.Actor {
				return steward.Actor
			},
			want: authz.ErrForbidden,
		},
		{
			name: "administrator through user actor kind",
			actor: func(_ *testing.T, _ *sql.DB, admin, _, _ maintenancePrincipal) authz.Actor {
				actor := admin.Actor
				actor.Kind = authz.ActorUserSession
				return actor
			},
			want: authz.ErrForbidden,
		},
		{
			name: "invalid actor kind",
			actor: func(_ *testing.T, _ *sql.DB, admin, _, _ maintenancePrincipal) authz.Actor {
				actor := admin.Actor
				actor.Kind = 0
				return actor
			},
			want: authz.ErrUnauthorized,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := openMaintenanceStore(t)
			admin := insertMaintenancePrincipal(t, store.DB(), "", "admin-rejected", true, false)
			user := insertMaintenancePrincipal(t, store.DB(), "9401", "user-rejected", false, false)
			steward := insertMaintenancePrincipal(t, store.DB(), "9402", "steward-rejected", false, true)
			service, _ := newMaintenanceService(t, NewRegistry())
			if _, err := service.PrepareListener(context.Background(), store.DB()); err != nil {
				t.Fatalf("prepare listener: %v", err)
			}
			actor := tt.actor(t, store.DB(), admin, user, steward)
			before := readMaintenanceMutationFacts(t, store.DB())
			tx := beginMaintenanceTx(t, store.DB())
			_, err := service.DisableTx(context.Background(), tx, actor, DisableCommand{
				ExpectedRevision: 1, OperationID: nextOperationID(t), Reason: "must be rejected",
			})
			if !errors.Is(err, tt.want) {
				t.Fatalf("DisableTx error=%v, want %v", err, tt.want)
			}
			if after := readMaintenanceMutationFacts(t, tx); after != before {
				t.Fatalf("rejected administrator actor wrote maintenance facts: before=%+v after=%+v", before, after)
			}
			if err := tx.Rollback(); err != nil {
				t.Fatalf("rollback rejected administrator mutation: %v", err)
			}
			if after := readMaintenanceMutationFacts(t, store.DB()); after != before {
				t.Fatalf("rejected administrator actor committed maintenance facts: before=%+v after=%+v", before, after)
			}
		})
	}
}

func TestMaintenanceRejectsStewardWithoutDiscordWithoutWrites(t *testing.T) {
	store := openMaintenanceStore(t)
	admin := insertMaintenancePrincipal(t, store.DB(), "", "admin-steward-null", true, false)
	steward := insertMaintenancePrincipal(t, store.DB(), "", "steward-null-discord", false, true)
	service, _ := newMaintenanceService(t, NewRegistry())
	if _, err := service.PrepareListener(context.Background(), store.DB()); err != nil {
		t.Fatalf("prepare listener: %v", err)
	}

	tx := beginMaintenanceTx(t, store.DB())
	disable, err := service.DisableTx(context.Background(), tx, admin.Actor, DisableCommand{
		ExpectedRevision: 1, OperationID: nextOperationID(t), Reason: "prepare steward rejection",
	})
	if err != nil {
		t.Fatalf("prepare disabled state: %v", err)
	}
	commitMaintenanceTransition(t, store.DB(), tx, disable)

	before := readMaintenanceMutationFacts(t, store.DB())
	tx = beginMaintenanceTx(t, store.DB())
	_, err = service.EnableTx(context.Background(), tx, steward.Actor, EnableCommand{
		ExpectedRevision: 2,
		OperationID:      nextOperationID(t),
		PayloadHash:      sha256.Sum256([]byte("steward without Discord")),
		Reason:           "must be rejected",
		Confirmed:        true,
	})
	if !errors.Is(err, authz.ErrForbidden) {
		t.Fatalf("steward without Discord error=%v", err)
	}
	if after := readMaintenanceMutationFacts(t, tx); after != before {
		t.Fatalf("steward without Discord wrote maintenance facts: before=%+v after=%+v", before, after)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatalf("rollback steward without Discord: %v", err)
	}
	if after := readMaintenanceMutationFacts(t, store.DB()); after != before {
		t.Fatalf("steward without Discord committed maintenance facts: before=%+v after=%+v", before, after)
	}
}

func TestMaintenanceRollbackCannotBeObservedAndFinalAuthWins(t *testing.T) {
	store := openMaintenanceStore(t)
	admin := insertMaintenancePrincipal(t, store.DB(), "", "admin-rollback", true, false)
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
	admin := insertMaintenancePrincipal(t, store.DB(), "", "admin-continuation", true, false)
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
