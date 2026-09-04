package lifecycle

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func maintenanceObjectID(fill string, last byte) string {
	return "op_" + strings.Repeat(fill, 21) + string(last)
}

func legalHoldID(fill string, last byte) string {
	return "lgh_" + strings.Repeat(fill, 21) + string(last)
}

func legalHoldKey(label string) string {
	return "legal-hold-" + label + "-0000000000000000"
}

func seedMaintenanceHoldRoot(t *testing.T, database *sql.DB, adminID int64, id string, resolvedAt int64) {
	t.Helper()
	_, err := database.Exec(`
INSERT INTO maintenance_events(
 id,actor_user_id,actor_role,action,reason,created_at,resolved_at,deidentify_at,retain_until
) VALUES(?,?,'admin','disable','test',?,?,?,?)`, id, adminID, resolvedAt-1, resolvedAt,
		resolvedAt+90*24*60*60, resolvedAt+400*24*60*60)
	if err != nil {
		t.Fatalf("insert maintenance hold root: %v", err)
	}
}

func maintenanceHeldAdapter() testHeldObjectAdapter {
	return testHeldObjectAdapter{
		inspect: func(ctx context.Context, tx *sql.Tx, ref string, _ int64) (HeldObjectState, error) {
			var deadline int64
			var consumed int
			if err := tx.QueryRowContext(ctx, `SELECT retain_until,legal_hold_consumed FROM maintenance_events WHERE id=?`, ref).Scan(&deadline, &consumed); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return HeldObjectState{}, nil
				}
				return HeldObjectState{}, err
			}
			return HeldObjectState{Exists: true, OrdinaryDeadline: deadline, LegalHoldConsumed: consumed == 1}, nil
		},
		consume: func(ctx context.Context, tx *sql.Tx, ref string) error {
			result, err := tx.ExecContext(ctx, `UPDATE maintenance_events SET legal_hold_consumed=1 WHERE id=? AND legal_hold_consumed=0`, ref)
			if err != nil {
				return err
			}
			affected, err := result.RowsAffected()
			if err != nil || affected != 1 {
				return ErrConflict
			}
			return nil
		},
		read: func(ctx context.Context, tx *sql.Tx, ref string, _ int64) (bool, error) {
			var one int
			err := tx.QueryRowContext(ctx, `SELECT 1 FROM maintenance_events WHERE id=?`, ref).Scan(&one)
			if errors.Is(err, sql.ErrNoRows) {
				return false, nil
			}
			return err == nil && one == 1, err
		},
	}
}

func fixedLegalHoldIDs(ids ...string) OpaqueIDSource {
	var mu sync.Mutex
	index := 0
	return func(prefix string) (string, error) {
		mu.Lock()
		defer mu.Unlock()
		if prefix != "lgh_" || index >= len(ids) {
			return "", ErrUnavailable
		}
		id := ids[index]
		index++
		return id, nil
	}
}

func configureMaintenanceLegalHolds(config Config, ids ...string) Config {
	adapter := maintenanceHeldAdapter()
	config.HeldObjects = HeldObjectAdapters{
		MaintenanceEvent: adapter, ReportCase: adapter, AnnouncementAudit: adapter, Donation: adapter, RequestLog: adapter,
	}
	config.NewID = fixedLegalHoldIDs(ids...)
	return config
}

func TestLegalHoldCreateReplayReleaseAndEndedReadRollup(t *testing.T) {
	fixture := newLifecycleTestFixture(t, 100)
	adminID := seedLifecycleUser(t, fixture.store.DB(), "hold-admin", true, 100)
	rootID := maintenanceObjectID("A", 'A')
	holdID := legalHoldID("A", 'A')
	seedMaintenanceHoldRoot(t, fixture.store.DB(), adminID, rootID, 100)
	config := configureMaintenanceLegalHolds(fixture.config, holdID)
	coordinator := mustNewLifecycleCoordinator(t, config)
	create := LegalHoldCreate{
		AdminID: adminID, ObjectKind: HeldMaintenanceEvent, ObjectRef: rootID, Basis: "preserve evidence",
		ExpiresAt: 200, Confirmation: true, IdempotencyKey: legalHoldKey("create"), DecisionNow: 100,
	}
	first, err := coordinator.CreateLegalHold(context.Background(), create)
	if err != nil {
		t.Fatalf("CreateLegalHold: %v", err)
	}
	if first.Status != 201 || first.Replayed || first.Value.ID != holdID || first.Value.State != "active" || first.Value.Revision != "1" {
		t.Fatalf("create result = %+v", first)
	}
	replay, err := coordinator.CreateLegalHold(context.Background(), create)
	if err != nil {
		t.Fatalf("CreateLegalHold replay: %v", err)
	}
	if !replay.Replayed || replay.Status != first.Status || !reflect.DeepEqual(replay.Body, first.Body) || !reflect.DeepEqual(replay.Value, first.Value) {
		t.Fatalf("create replay mismatch: first=%+v replay=%+v", first, replay)
	}
	if fixture.auth.freshCalls != 2 {
		t.Fatalf("fresh authorization calls after replay = %d, want 2", fixture.auth.freshCalls)
	}

	create.Basis = "different body"
	if _, err := coordinator.CreateLegalHold(context.Background(), create); !errors.Is(err, ErrConflict) {
		t.Fatalf("same-key different-body error = %v, want ErrConflict", err)
	}
	if _, err := coordinator.ReleaseLegalHold(context.Background(), LegalHoldRelease{
		AdminID: adminID, HoldID: holdID, ExpectedRevision: "1", Reason: "release",
		Confirmation: true, IdempotencyKey: legalHoldKey("create"), DecisionNow: 120,
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("cross-route key error = %v, want ErrConflict", err)
	}

	active, err := coordinator.GetLegalHold(context.Background(), adminID, holdID, 120)
	if err != nil || active.State != "active" {
		t.Fatalf("active detail = %+v, err=%v", active, err)
	}
	release := LegalHoldRelease{
		AdminID: adminID, HoldID: holdID, ExpectedRevision: "1", Reason: "no longer needed",
		Confirmation: true, IdempotencyKey: legalHoldKey("release"), DecisionNow: 150,
	}
	released, err := coordinator.ReleaseLegalHold(context.Background(), release)
	if err != nil {
		t.Fatalf("ReleaseLegalHold: %v", err)
	}
	if released.Value.State != "released" || released.Value.Revision != "2" || released.Value.EndedAt == nil || *released.Value.EndedAt != 150 {
		t.Fatalf("released detail = %+v", released.Value)
	}
	releaseReplay, err := coordinator.ReleaseLegalHold(context.Background(), release)
	if err != nil || !releaseReplay.Replayed || !reflect.DeepEqual(releaseReplay.Body, released.Body) {
		t.Fatalf("release replay = %+v, err=%v", releaseReplay, err)
	}
	ended, err := coordinator.GetLegalHold(context.Background(), adminID, holdID, 151)
	if err != nil || ended.State != "released" {
		t.Fatalf("ended detail = %+v, err=%v", ended, err)
	}
	retainUntil := int64(150 + 400*24*60*60)
	var readCount, persistedRetain int64
	if err := fixture.store.DB().QueryRow(`
SELECT read_count,retain_until FROM legal_hold_read_audits
WHERE hold_id_text=? AND admin_user_id=? AND read_kind='metadata'`, holdID, adminID).Scan(&readCount, &persistedRetain); err != nil {
		t.Fatalf("read audit: %v", err)
	}
	if readCount != 2 || persistedRetain != retainUntil {
		t.Fatalf("read audit count=%d retain=%d", readCount, persistedRetain)
	}
	if _, err := coordinator.GetLegalHold(context.Background(), adminID, holdID, retainUntil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("hard-cutoff detail error = %v, want ErrNotFound", err)
	}
	if result, err := coordinator.retainEndedHolds(context.Background(), retainUntil, WorkerBatchLimit, time.Now().Add(WorkerBudget)); err != nil || result.Processed != 1 {
		t.Fatalf("retainEndedHolds result=%+v err=%v", result, err)
	}
	var marker int
	if err := fixture.store.DB().QueryRow(`SELECT legal_hold_consumed FROM maintenance_events WHERE id=?`, rootID).Scan(&marker); err != nil || marker != 1 {
		t.Fatalf("root marker=%d err=%v", marker, err)
	}
	create.Basis = "cannot reopen"
	create.IdempotencyKey = legalHoldKey("reopen")
	create.DecisionNow = retainUntil
	create.ExpiresAt = retainUntil + 1
	if _, err := coordinator.CreateLegalHold(context.Background(), create); !errors.Is(err, ErrNotFound) {
		t.Fatalf("past-ordinary-deadline reopen error = %v, want ErrNotFound", err)
	}
}

func TestLegalHoldExpiryAndHeldObjectAuthorization(t *testing.T) {
	fixture := newLifecycleTestFixture(t, 100)
	adminID := seedLifecycleUser(t, fixture.store.DB(), "expiry-admin", true, 100)
	rootID := maintenanceObjectID("B", 'Q')
	holdID := legalHoldID("B", 'Q')
	seedMaintenanceHoldRoot(t, fixture.store.DB(), adminID, rootID, 100)
	coordinator := mustNewLifecycleCoordinator(t, configureMaintenanceLegalHolds(fixture.config, holdID))
	_, err := coordinator.CreateLegalHold(context.Background(), LegalHoldCreate{
		AdminID: adminID, ObjectKind: HeldMaintenanceEvent, ObjectRef: rootID, Basis: "expiry evidence",
		ExpiresAt: 200, Confirmation: true, IdempotencyKey: legalHoldKey("expiry"), DecisionNow: 100,
	})
	if err != nil {
		t.Fatalf("CreateLegalHold: %v", err)
	}
	tx, err := fixture.store.DB().BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	allowed, err := coordinator.AuthorizeHeldObjectRead(context.Background(), tx, adminID, HeldMaintenanceEvent, rootID, 199)
	if err != nil || !allowed {
		_ = tx.Rollback()
		t.Fatalf("active held read allowed=%v err=%v", allowed, err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	tx, err = fixture.store.DB().BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	allowed, err = coordinator.AuthorizeHeldObjectRead(context.Background(), tx, adminID, HeldMaintenanceEvent, rootID, 200)
	if err != nil || allowed {
		_ = tx.Rollback()
		t.Fatalf("expired held read allowed=%v err=%v", allowed, err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	detail, err := coordinator.GetLegalHold(context.Background(), adminID, holdID, 201)
	if err != nil || detail.State != "expired" || detail.EndedAt == nil || *detail.EndedAt != 200 {
		t.Fatalf("expired detail=%+v err=%v", detail, err)
	}
	if _, err := coordinator.ReleaseLegalHold(context.Background(), LegalHoldRelease{
		AdminID: adminID, HoldID: holdID, ExpectedRevision: "2", Reason: "too late", Confirmation: true,
		IdempotencyKey: legalHoldKey("late-release"), DecisionNow: 201,
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("late release error=%v, want ErrConflict", err)
	}
	var action string
	var createdAt int64
	if err := fixture.store.DB().QueryRow(`SELECT action,created_at FROM legal_hold_audits WHERE hold_id_text=? AND action='expire'`, holdID).Scan(&action, &createdAt); err != nil {
		t.Fatalf("expiry audit: %v", err)
	}
	if action != "expire" || createdAt != 200 {
		t.Fatalf("expiry audit action=%q created=%d", action, createdAt)
	}
}

func TestLegalHoldListCursorBindsFiltersAndActor(t *testing.T) {
	fixture := newLifecycleTestFixture(t, 100)
	adminID := seedLifecycleUser(t, fixture.store.DB(), "list-admin", true, 100)
	otherAdminID := seedLifecycleUser(t, fixture.store.DB(), "list-other", false, 100)
	roots := []string{maintenanceObjectID("C", 'g'), maintenanceObjectID("D", 'w'), maintenanceObjectID("E", 'A')}
	holds := []string{legalHoldID("C", 'g'), legalHoldID("D", 'w'), legalHoldID("E", 'A')}
	for _, root := range roots {
		seedMaintenanceHoldRoot(t, fixture.store.DB(), adminID, root, 100)
	}
	coordinator := mustNewLifecycleCoordinator(t, configureMaintenanceLegalHolds(fixture.config, holds...))
	for index := range roots {
		_, err := coordinator.CreateLegalHold(context.Background(), LegalHoldCreate{
			AdminID: adminID, ObjectKind: HeldMaintenanceEvent, ObjectRef: roots[index], Basis: "list evidence",
			ExpiresAt: 500, Confirmation: true, IdempotencyKey: legalHoldKey("list" + string(rune('A'+index))), DecisionNow: int64(100 + index),
		})
		if err != nil {
			t.Fatalf("create hold %d: %v", index, err)
		}
	}
	first, err := coordinator.ListLegalHolds(context.Background(), LegalHoldListFilter{AdminID: adminID, Limit: 2, DecisionNow: 103})
	if err != nil || len(first.Data) != 2 || first.NextCursor == nil {
		t.Fatalf("first page=%+v err=%v", first, err)
	}
	if first.Data[0].ID != holds[2] || first.Data[1].ID != holds[1] {
		t.Fatalf("first page order=%v", []string{first.Data[0].ID, first.Data[1].ID})
	}
	second, err := coordinator.ListLegalHolds(context.Background(), LegalHoldListFilter{
		AdminID: adminID, Limit: 2, Cursor: *first.NextCursor, DecisionNow: 103,
	})
	if err != nil || len(second.Data) != 1 || second.Data[0].ID != holds[0] || second.NextCursor != nil {
		t.Fatalf("second page=%+v err=%v", second, err)
	}
	tampered := *first.NextCursor
	if tampered[len(tampered)-1] == 'A' {
		tampered = tampered[:len(tampered)-1] + "B"
	} else {
		tampered = tampered[:len(tampered)-1] + "A"
	}
	if _, err := coordinator.ListLegalHolds(context.Background(), LegalHoldListFilter{
		AdminID: adminID, Limit: 2, Cursor: tampered, DecisionNow: 103,
	}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("tampered cursor error=%v", err)
	}
	if _, err := coordinator.ListLegalHolds(context.Background(), LegalHoldListFilter{
		AdminID: otherAdminID, Limit: 2, Cursor: *first.NextCursor, DecisionNow: 103,
	}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("cross-admin cursor error=%v", err)
	}
	if _, err := coordinator.ListLegalHolds(context.Background(), LegalHoldListFilter{
		AdminID: adminID, Kind: HeldMaintenanceEvent, Limit: 2, Cursor: *first.NextCursor, DecisionNow: 103,
	}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("cross-filter cursor error=%v", err)
	}
}

func TestLegalHoldWorkerHonorsExpiredTransactionBudget(t *testing.T) {
	fixture := newLifecycleTestFixture(t, 100)
	coordinator := mustNewLifecycleCoordinator(t, fixture.config)
	expiredDeadline := time.Now().Add(-time.Second)
	if _, err := coordinator.expireDueHolds(context.Background(), 100, WorkerBatchLimit, expiredDeadline); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expireDueHolds error = %v, want context deadline", err)
	}
	if _, err := coordinator.retainEndedHolds(context.Background(), 100, WorkerBatchLimit, expiredDeadline); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("retainEndedHolds error = %v, want context deadline", err)
	}
}
