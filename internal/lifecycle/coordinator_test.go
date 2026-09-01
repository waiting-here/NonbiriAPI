package lifecycle

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/db"
)

func TestNewRequiresEveryClosedAdapterFamily(t *testing.T) {
	fixture := newLifecycleTestFixture(t, 100)
	tests := []struct {
		name   string
		mutate func(*Config)
	}{
		{"store", func(config *Config) { config.Store = nil }},
		{"user auth", func(config *Config) { config.UserAuth = nil }},
		{"admin auth", func(config *Config) { config.AdminAuth = nil }},
		{"cursor keys", func(config *Config) { config.CursorKeys = nil }},
		{"retirement", func(config *Config) { config.Retirement = nil }},
		{"ledger", func(config *Config) { config.Ledger = nil }},
		{"export", func(config *Config) { config.Export.RPS = nil }},
		{"delete", func(config *Config) { config.Delete.Reports = nil }},
		{"recovery", func(config *Config) { config.Recovery.Claims = nil }},
		{"retention", func(config *Config) { config.Retention.RequestLogs = nil }},
		{"held objects", func(config *Config) { config.HeldObjects.Donation = nil }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := fixture.config
			test.mutate(&config)
			if _, err := New(config); !errors.Is(err, ErrInvalid) {
				t.Fatalf("New error = %v, want ErrInvalid", err)
			}
		})
	}
}

func TestExportUsesOneTransactionFrozenOrderAndEmptyArrays(t *testing.T) {
	fixture := newLifecycleTestFixture(t, 100)
	finalizers := []*testFinalizer{{}, {}, {}}
	fixture.exports.fishingFinalizer = finalizers[0]
	fixture.exports.linkFinalizer = finalizers[1]
	fixture.exports.rpsFinalizer = finalizers[2]
	coordinator := mustNewLifecycleCoordinator(t, fixture.config)
	body, err := coordinator.Export(context.Background(), 7, 100)
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	wantOrder := []string{"identity", "resources", "issues", "ledger", "activities", "donations", "charity", "fishing", "linklink", "rps"}
	if !reflect.DeepEqual(fixture.exports.calls, wantOrder) {
		t.Fatalf("export order = %v, want %v", fixture.exports.calls, wantOrder)
	}
	if fixture.auth.userCalls != 1 {
		t.Fatalf("fresh user authorization calls = %d, want 1", fixture.auth.userCalls)
	}
	var document ExportDocument
	if err := json.Unmarshal(body, &document); err != nil {
		t.Fatalf("decode export: %v", err)
	}
	if document.SchemaVersion != 4 || document.GeneratedAt != 100 {
		t.Fatalf("export header = version %d at %d", document.SchemaVersion, document.GeneratedAt)
	}
	if document.Endpoints == nil || document.CatalogPairs == nil || document.Models == nil || document.Issues == nil ||
		document.CreditLedger == nil || document.WelfareClaims == nil || document.Thursday == nil || document.Donations == nil ||
		document.Fishing.Pending == nil || document.Fishing.Terminal == nil || document.LinkLink.Summaries == nil || document.RPS.Summaries == nil {
		t.Fatal("an empty export collection encoded as null")
	}
	for index, finalizer := range finalizers {
		if finalizer.commits != 1 || finalizer.aborts != 0 {
			t.Fatalf("export finalizer %d commits=%d aborts=%d", index, finalizer.commits, finalizer.aborts)
		}
	}
}

func TestExportRejectsCollectionAndEncodedBodyBeyondFrozenBounds(t *testing.T) {
	t.Run("collection limit plus one", func(t *testing.T) {
		fixture := newLifecycleTestFixture(t, 100)
		finalizer := &testFinalizer{}
		fixture.exports.fishingFinalizer = finalizer
		fixture.exports.endpoints = make([]EndpointExport, CollectionLimit+1)
		coordinator := mustNewLifecycleCoordinator(t, fixture.config)
		if _, err := coordinator.Export(context.Background(), 7, 100); !errors.Is(err, ErrTooLarge) {
			t.Fatalf("Export error = %v, want ErrTooLarge", err)
		}
		if finalizer.aborts != 1 || finalizer.commits != 0 {
			t.Fatalf("oversized export finalizer commits=%d aborts=%d", finalizer.commits, finalizer.aborts)
		}
	})

	t.Run("nested pending seat limit plus one", func(t *testing.T) {
		fixture := newLifecycleTestFixture(t, 100)
		fixture.exports.rps.Pending = &RPSPendingExport{Seats: make([]RPSPendingSeatExport, CollectionLimit+1)}
		coordinator := mustNewLifecycleCoordinator(t, fixture.config)
		if _, err := coordinator.Export(context.Background(), 7, 100); !errors.Is(err, ErrTooLarge) {
			t.Fatalf("Export error = %v, want ErrTooLarge", err)
		}
	})

	t.Run("exact body limit and plus one", func(t *testing.T) {
		fixture := newLifecycleTestFixture(t, 100)
		coordinator := mustNewLifecycleCoordinator(t, fixture.config)
		baseline, err := coordinator.Export(context.Background(), 7, 100)
		if err != nil {
			t.Fatalf("baseline Export: %v", err)
		}
		padding := MaxExportBytes - len(baseline)
		if padding <= 0 {
			t.Fatalf("baseline export unexpectedly uses %d bytes", len(baseline))
		}
		fixture.exports.user.Username = strings.Repeat("x", padding)
		exact, err := coordinator.Export(context.Background(), 7, 100)
		if err != nil {
			t.Fatalf("exact-limit Export: %v", err)
		}
		if len(exact) != MaxExportBytes {
			t.Fatalf("exact export length = %d, want %d", len(exact), MaxExportBytes)
		}
		fixture.exports.user.Username += "x"
		if _, err := coordinator.Export(context.Background(), 7, 100); !errors.Is(err, ErrTooLarge) {
			t.Fatalf("plus-one Export error = %v, want ErrTooLarge", err)
		}
	})
}

func TestExportAbortsEveryPreparedFinalizerWhenLaterDomainFails(t *testing.T) {
	fixture := newLifecycleTestFixture(t, 100)
	finalizers := []*testFinalizer{{}, {}, {}}
	fixture.exports.fishingFinalizer = finalizers[0]
	fixture.exports.linkFinalizer = finalizers[1]
	fixture.exports.rpsFinalizer = finalizers[2]
	fixture.exports.errAt = "rps"
	coordinator := mustNewLifecycleCoordinator(t, fixture.config)
	if _, err := coordinator.Export(context.Background(), 7, 100); err == nil {
		t.Fatal("export unexpectedly succeeded")
	}
	for index, finalizer := range finalizers {
		if finalizer.commits != 0 || finalizer.aborts != 1 {
			t.Fatalf("failed export finalizer %d commits=%d aborts=%d", index, finalizer.commits, finalizer.aborts)
		}
	}
}

func seedLifecycleUser(t *testing.T, database *sql.DB, name string, admin bool, now int64) int64 {
	t.Helper()
	adminValue := 0
	if admin {
		adminValue = 1
	}
	zero := db.EncodeU128(db.U128{})
	result, err := database.Exec(`
INSERT INTO users(
 discord_id,username,is_admin,donation_credit_mag,total_requests,total_uncached_input_tokens,
 total_cache_write_input_tokens,total_cache_read_input_tokens,total_output_tokens,
 total_unknown_usage_requests,revision,created_at,updated_at
) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`, "lifecycle-"+name, name, adminValue, zero, zero, zero, zero, zero, zero, zero, zero, now, now)
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("user id: %v", err)
	}
	return id
}

func configuredDeleteAdapters(calls *[]string, finalizers []*testFinalizer, failureAt string, updateName bool) DeleteAdapters {
	makeAdapter := func(index int, name string) DeleteAdapter {
		adapter := testDeleteAdapter{name: name, calls: calls, finalizer: finalizers[index]}
		if index == 0 && updateName {
			adapter.update = func(ctx context.Context, tx *sql.Tx, request DeleteRequest) error {
				_, err := tx.ExecContext(ctx, `UPDATE users SET username='prepared-delete' WHERE id=?`, request.UserID)
				return err
			}
		}
		if name == failureAt {
			adapter.err = errors.New("delete adapter failure")
			adapter.finalizer = nil
		}
		return adapter
	}
	return DeleteAdapters{
		AuthSessionCallerKey: makeAdapter(0, "auth"), Resources: makeAdapter(1, "resources"), ClaimLog: makeAdapter(2, "claim_log"),
		IssuesAnnouncements: makeAdapter(3, "issues"), Donations: makeAdapter(4, "donations"), Activities: makeAdapter(5, "activities"),
		Reports: makeAdapter(6, "reports"), Fishing: makeAdapter(7, "fishing"), LinkLink: makeAdapter(8, "linklink"),
		RPS: makeAdapter(9, "rps"), DebugAccountStream: makeAdapter(10, "debug"),
	}
}

func TestDeleteAccountCommitsDatabaseBeforeRetirementAndFinalizers(t *testing.T) {
	fixture := newLifecycleTestFixture(t, 100)
	userID := seedLifecycleUser(t, fixture.store.DB(), "delete-success", false, 100)
	calls := []string{}
	finalizers := make([]*testFinalizer, 11)
	for index := range finalizers {
		finalizers[index] = &testFinalizer{}
	}
	retirement := &testRetirement{}
	ledger := &testLedgerDelete{calls: &calls, deleteUser: true}
	config := fixture.config
	config.Delete = configuredDeleteAdapters(&calls, finalizers, "", false)
	config.Retirement = testRetirementBoundary{retirement: retirement}
	config.Ledger = ledger
	config.NewID = func(prefix string) (string, error) {
		if prefix != "op_" {
			t.Fatalf("operation prefix = %q", prefix)
		}
		return "op_AAAAAAAAAAAAAAAAAAAAAA", nil
	}
	coordinator := mustNewLifecycleCoordinator(t, config)
	if err := coordinator.DeleteAccount(context.Background(), userID, 100); err != nil {
		t.Fatalf("DeleteAccount: %v", err)
	}
	wantOrder := []string{"auth", "resources", "claim_log", "issues", "donations", "activities", "reports", "fishing", "linklink", "rps", "debug", "ledger"}
	if !reflect.DeepEqual(calls, wantOrder) {
		t.Fatalf("delete order = %v, want %v", calls, wantOrder)
	}
	if ledger.operationID != "op_AAAAAAAAAAAAAAAAAAAAAA" {
		t.Fatalf("ledger operation id = %q", ledger.operationID)
	}
	var count int
	if err := fixture.store.DB().QueryRow(`SELECT count(*) FROM users WHERE id=?`, userID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("deleted user count = %d, err=%v", count, err)
	}
	if retirement.commits != 1 || retirement.aborts != 0 {
		t.Fatalf("retirement commits=%d aborts=%d", retirement.commits, retirement.aborts)
	}
	for index, finalizer := range finalizers {
		if finalizer.commits != 1 || finalizer.aborts != 0 {
			t.Fatalf("finalizer %d commits=%d aborts=%d", index, finalizer.commits, finalizer.aborts)
		}
	}
}

func TestDeleteAccountFailureRollsBackAndAbortsPreparedState(t *testing.T) {
	fixture := newLifecycleTestFixture(t, 100)
	userID := seedLifecycleUser(t, fixture.store.DB(), "delete-rollback", false, 100)
	calls := []string{}
	finalizers := make([]*testFinalizer, 11)
	for index := range finalizers {
		finalizers[index] = &testFinalizer{}
	}
	retirement := &testRetirement{}
	config := fixture.config
	config.Delete = configuredDeleteAdapters(&calls, finalizers, "reports", true)
	config.Retirement = testRetirementBoundary{retirement: retirement}
	config.Ledger = &testLedgerDelete{calls: &calls, deleteUser: true}
	coordinator := mustNewLifecycleCoordinator(t, config)
	if err := coordinator.DeleteAccount(context.Background(), userID, 100); err == nil {
		t.Fatal("DeleteAccount unexpectedly succeeded")
	}
	var username string
	if err := fixture.store.DB().QueryRow(`SELECT username FROM users WHERE id=?`, userID).Scan(&username); err != nil {
		t.Fatalf("read rolled-back user: %v", err)
	}
	if username != "delete-rollback" {
		t.Fatalf("rolled-back username = %q", username)
	}
	if retirement.commits != 0 || retirement.aborts != 1 {
		t.Fatalf("retirement commits=%d aborts=%d", retirement.commits, retirement.aborts)
	}
	for index, finalizer := range finalizers {
		if index < 6 {
			if finalizer.aborts != 1 || finalizer.commits != 0 {
				t.Fatalf("prepared finalizer %d commits=%d aborts=%d", index, finalizer.commits, finalizer.aborts)
			}
		} else if finalizer.aborts != 0 || finalizer.commits != 0 {
			t.Fatalf("unprepared finalizer %d changed", index)
		}
	}
	if strings.Contains(strings.Join(calls, ","), "ledger") {
		t.Fatalf("ledger ran after adapter failure: %v", calls)
	}
}
