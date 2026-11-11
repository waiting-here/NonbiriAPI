package v1

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/db"
)

func restoreSchema47LayoutForTest(t *testing.T, database *sql.DB) {
	t.Helper()
	restoreSchema48LayoutForTest(t, database)
	hostileMustExec(t, database, "DROP INDEX idx_request_logs_operation_started")
	assertRetainedManifest(t, database, db.Schema47ManifestHash)
}

func TestLogOperationSchema47UpgradeIsExplicitAndAtomic(t *testing.T) {
	path, vault := bootstrapTestPath(t, "log-operation.sqlite"), bootstrapTestVault(t)
	store, err := db.Open(path, vault)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	owner := hostileInsertUser(t, store.DB(), "retained-log-owner", 0, 1)
	requestID := "req_" + strings.Repeat("A", 22)
	hostileMustExec(t, store.DB(), `INSERT INTO logical_requests(id,user_id,route_kind,model_snapshot,state,attempt_limit,caller_result_class,caller_status,accounting_state,settlement_destination,ledger_rows_remaining,created_at,terminal_at)
VALUES(?,?,'charity_responses','provider/retained','terminal',1,'success',200,'none','user',zeroblob(16),1,2)`, requestID, owner)
	hostileMustExec(t, store.DB(), `INSERT INTO request_logs(logical_request_id,user_id,model,route_kind,started_at,completed_at,caller_result_class,caller_status,status_code)
VALUES(?,?,'provider/retained','charity_responses',1,2,'success',200,200)`, requestID, owner)
	restoreSchema47LayoutForTest(t, store.DB())
	hostileMustExec(t, store.DB(), "UPDATE schema_state SET version=47")
	plan, err := storageSchema.plan(t.Context(), store.DB())
	if err != nil || len(plan.steps) != db.CurrentSchemaVersion-47 || plan.steps[0].from != 47 || plan.steps[0].version != 48 || plan.auditsCredentials() {
		t.Fatal("index-only upgrade plan changed", plan, err)
	}
	if !reflect.DeepEqual(plan.steps[0].impact.indexedTables, []string{"request_logs"}) {
		t.Fatal("index upgrade affected unrelated tables")
	}
	before := gatewayRows(t, store.DB(), true)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	checkSource := func() {
		t.Helper()
		if err := db.InspectContext(t.Context(), path, vault, func(_ context.Context, database *sql.DB) error {
			assertExactPublishedSource(t, database, 47, db.Schema47ManifestHash, before, true)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	if opened, err := db.Open(path, vault); err == nil {
		opened.Close()
		t.Fatal("schema47 started without explicit upgrade")
	}
	checked, err := Run(t.Context(), path, vault, Options{})
	if err != nil || checked.Status != "ready" || checked.SourceSchemaVersion != 47 || checked.TargetSchemaVersion != db.CurrentSchemaVersion {
		t.Fatal("check", checked, err)
	}
	checkSource()
	index := migrationIndex(t, 47)
	original := storageSchema.versions[index].apply
	t.Cleanup(func() { storageSchema.versions[index].apply = original })
	for _, cancelAtEnd := range []bool{false, true} {
		ctx, cancel := context.WithCancel(t.Context())
		reachedDDL := false
		storageSchema.versions[index].apply = func(ctx context.Context, tx *sql.Tx) error {
			var sqlText string
			if err := tx.QueryRowContext(ctx, "SELECT sql FROM sqlite_schema WHERE name='idx_request_logs_operation_started'").Scan(&sqlText); err != nil || !strings.Contains(sqlText, ",started_at,id,completed_at)") {
				return errors.New("covering index was not applied")
			}
			reachedDDL = true
			if cancelAtEnd {
				cancel()
				return ctx.Err()
			}
			return errors.New("injected after index replacement")
		}
		_, err := Run(ctx, path, vault, Options{Apply: true})
		cancel()
		storageSchema.versions[index].apply = original
		if err == nil || !reachedDDL {
			t.Fatalf("rollback not exercised: reached=%v err=%v", reachedDDL, err)
		}
		checkSource()
	}
	result, err := Run(t.Context(), path, vault, Options{Apply: true})
	if err != nil || result.Status != "converted" || result.SourceSchemaVersion != 47 || result.TargetSchemaVersion != db.CurrentSchemaVersion {
		t.Fatal("apply", result, err)
	}
	store, err = db.Open(path, vault)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	after := gatewayRows(t, store.DB(), true)
	assertCreditOperationAssetsProjectionForTest(t, store.DB())
	delete(after, "credit_operation_assets")
	if !reflect.DeepEqual(before, after) {
		t.Fatal("index upgrade changed retained rows")
	}
	assertRetainedManifest(t, store.DB(), db.PinnedGenerationTwoManifestHash)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	again, err := Run(t.Context(), path, vault, Options{Apply: true})
	if err != nil || again.Status != "current" {
		t.Fatal("repeated upgrade", again, err)
	}
}
