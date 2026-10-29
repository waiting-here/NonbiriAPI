package codexauth

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/dbfixture"
	"github.com/waiting-here/NonbiriAPI/internal/managementlog"
)

type auditFixture struct {
	*fixture
	store *db.Store
}

func newAuditFixture(t *testing.T) *auditFixture {
	t.Helper()
	f := newFixture(t)
	path := filepath.Join(t.TempDir(), "audit.sqlite")
	dbfixture.Materialize(t, path, f.v)
	store, err := db.Open(path, f.v)
	if err != nil {
		t.Fatal(err)
	}
	store.SetManagementCapture(managementlog.CaptureEventID)
	f.m.config.DB, f.m.config.ReadDB = store.DB(), store.ReadDB()
	t.Cleanup(func() {
		_ = f.m.Close()
		_ = store.Close()
	})
	return &auditFixture{fixture: f, store: store}
}

func auditContext(action string) (context.Context, string) {
	ctx := managementlog.WithOperation(context.Background(), managementlog.Metadata{
		Source: managementlog.SourceWeb, ExecutorKind: "human",
		Actor:  managementlog.Subject{ID: "1", Name: "Synthetic administrator", Role: "admin"},
		Domain: "ai", Action: action, Summary: "Codex connection",
	})
	metadata, _ := managementlog.MetadataFromContext(ctx)
	return ctx, metadata.EventID
}

func auditOperation(t *testing.T, f *auditFixture, event string, want managementlog.Outcome, attempt string) []managementlog.Item {
	t.Helper()
	var outcome, kind, id, actor string
	var operationID int64
	if err := f.store.ReadDB().QueryRow("SELECT id,outcome,correlation_kind,correlation_id,actor_name FROM management_operations WHERE event_id=?", event).Scan(&operationID, &outcome, &kind, &id, &actor); err != nil {
		t.Fatal(err)
	}
	if outcome != string(want) || actor != "Synthetic administrator" || attempt != "" && (kind != "codex_login" || id != attempt) {
		t.Fatalf("operation: outcome=%q kind=%q attempt=%q actor=%q", outcome, kind, id, actor)
	}
	rows, err := f.store.ReadDB().Query("SELECT action,target_kind,target_id,target_name,outcome,code,changes,details FROM management_operation_items WHERE operation_id=? ORDER BY ordinal", operationID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var items []managementlog.Item
	for rows.Next() {
		var item managementlog.Item
		var changes, details string
		if err = rows.Scan(&item.Action, &item.TargetKind, &item.TargetID, &item.TargetName, &item.Outcome, &item.Code, &changes, &details); err != nil {
			t.Fatal(err)
		}
		if json.Unmarshal([]byte(changes), &item.Changes) != nil || json.Unmarshal([]byte(details), &item.Details) != nil {
			t.Fatal("invalid saved audit details")
		}
		items = append(items, item)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	return items
}

func assertAuditClean(t *testing.T, f *auditFixture) {
	t.Helper()
	for _, table := range []string{"management_capture_context", "management_capture_rows", "management_capture_totals"} {
		var count int
		if err := f.store.ReadDB().QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("capture leftovers in %s: %d %v", table, count, err)
		}
	}
	forbidden := []string{"private-device-id", "ABCD-EFGH", "one-time-code", "verifier", "refresh-original", "operator@example.invalid", "workspace", "access_token", "refresh_token", "id_token", "account_id", "account_label", "encrypted_credentials", "upstream-private-description", jwt(map[string]any{"exp": f.now.Load() + 3600, "jti": "original"}), jwt(map[string]any{"sub": "subject", "email": "operator@example.invalid", "https://api.openai.com/auth": map[string]string{"chatgpt_account_id": "workspace"}})}
	var envelope string
	if err := f.store.ReadDB().QueryRow("SELECT encrypted_credentials FROM agent_codex_session WHERE id=1").Scan(&envelope); err == nil && envelope != "" {
		forbidden = append(forbidden, envelope)
	}
	for _, table := range []string{"management_operations", "management_operation_items", "management_operation_rollups", "management_operation_terms"} {
		rows, err := f.store.ReadDB().Query("SELECT * FROM " + table)
		if err != nil {
			t.Fatal(err)
		}
		columns, _ := rows.Columns()
		for rows.Next() {
			values := make([]any, len(columns))
			ptrs := make([]any, len(columns))
			for i := range values {
				ptrs[i] = &values[i]
			}
			if err = rows.Scan(ptrs...); err != nil {
				t.Fatal(err)
			}
			raw, _ := json.Marshal(values)
			for _, secret := range forbidden {
				if strings.Contains(string(raw), secret) {
					t.Fatalf("audit %s retained prohibited value %q", table, secret)
				}
			}
		}
		if err = rows.Err(); err != nil {
			t.Fatal(err)
		}
		rows.Close()
	}
}

func holdAuditPoll(f *auditFixture) func() {
	release := make(chan struct{})
	f.poll = func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
			_, _ = w.Write([]byte("{\"authorization_code\":\"one-time-code\",\"code_verifier\":\"verifier\"}"))
		case <-r.Context().Done():
		}
	}
	return sync.OnceFunc(func() { close(release) })
}

func TestAuditDeviceLoginSuccessAndDisconnectSnapshots(t *testing.T) {
	f := newAuditFixture(t)
	release := holdAuditPoll(f)
	defer release()
	ctx, event := auditContext("codex.login")
	pending, err := f.m.Begin(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	items := auditOperation(t, f, event, managementlog.Accepted, pending.Login.ID)
	if len(items) != 1 || items[0].Outcome != managementlog.Accepted {
		t.Fatalf("accepted request was not recorded first: %+v", items)
	}
	release()
	connected := waitStatus(t, f.fixture, "succeeded")
	items = auditOperation(t, f, event, managementlog.Succeeded, pending.Login.ID)
	if len(items) != 2 || items[1].Outcome != managementlog.Succeeded || items[1].Details.State != "created" {
		t.Fatalf("login completion missing committed snapshot: %+v", items)
	}
	assertConnectedChange(t, items[1], nil, "1")
	assertAuditClean(t, f)
	// Re-recording the completed HTTP request cannot restore its accepted state.
	tx, err := f.store.DB().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = managementlog.Commit(ctx, tx); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	auditOperation(t, f, event, managementlog.Succeeded, pending.Login.ID)
	logout, logoutEvent := auditContext("codex.logout")
	if _, err = f.m.Disconnect(logout, connected.Revision); err != nil {
		t.Fatal(err)
	}
	items = auditOperation(t, f, logoutEvent, managementlog.Succeeded, "")
	if len(items) != 1 || items[0].Details.State != "updated" {
		t.Fatalf("disconnect snapshot missing: %+v", items)
	}
	before := "1"
	assertConnectedChange(t, items[0], &before, "0")
	if ready, _, err := f.m.Snapshot(t.Context(), f.store.ReadDB()); err != nil || ready {
		t.Fatalf("disconnect retained credentials: %v", err)
	}
	assertAuditClean(t, f)
}

func assertConnectedChange(t *testing.T, item managementlog.Item, before *string, after string) {
	t.Helper()
	found := false
	for _, change := range item.Changes {
		if change.Field != "connected" && change.Field != "revision" && change.Field != "expires_at" {
			t.Fatalf("unexpected Codex audit field: %s", change.Field)
		}
		if change.Field == "connected" {
			found = change.After != nil && *change.After == after && ((before == nil && change.Before == nil) || before != nil && change.Before != nil && *change.Before == *before)
		}
	}
	if !found {
		t.Fatalf("connected change missing: %+v", item.Changes)
	}
}

func TestAuditDeviceLoginTerminalOutcomes(t *testing.T) {
	for _, tc := range []struct {
		name, state, code string
		outcome           managementlog.Outcome
	}{
		{"rejected", "failed", "access_denied", managementlog.Failed},
		{"expired", "failed", "expired_token", managementlog.Failed},
		{"cancelled", "cancelled", "login_cancelled", managementlog.NoChange},
		{"disconnected", "", "login_cancelled", managementlog.NoChange},
		{"closed", "", "login_interrupted", managementlog.Unknown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newAuditFixture(t)
			release := holdAuditPoll(f)
			defer release()
			if tc.name == "rejected" || tc.name == "expired" {
				f.poll = func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(http.StatusBadRequest)
					_ = json.NewEncoder(w).Encode(map[string]string{"error": tc.code, "error_description": "upstream-private-description"})
				}
			}
			ctx, event := auditContext("codex.login")
			pending, err := f.m.Begin(ctx, 0)
			if err != nil {
				t.Fatal(err)
			}
			switch tc.name {
			case "cancelled":
				_, err = f.m.Cancel(context.Background(), pending.Login.ID)
			case "disconnected":
				logout, _ := auditContext("codex.logout")
				_, err = f.m.Disconnect(logout, 0)
			case "closed":
				err = f.m.Close()
			default:
				waitStatus(t, f.fixture, tc.state)
			}
			if err != nil {
				t.Fatal(err)
			}
			items := auditOperation(t, f, event, tc.outcome, pending.Login.ID)
			if len(items) != 2 || items[1].Code != tc.code || items[1].Outcome != tc.outcome || len(items[1].Changes) != 0 {
				t.Fatalf("terminal audit: %+v", items)
			}
			if ready, _, err := f.m.Snapshot(t.Context(), f.store.ReadDB()); err != nil || ready {
				t.Fatalf("terminal login retained credentials: %v", err)
			}
			assertAuditClean(t, f)
		})
	}
}

func TestAuditDeviceLoginWriteFailureRollsBackBeforeFailureRecord(t *testing.T) {
	f := newAuditFixture(t)
	// The business write has already occurred when this trigger rejects the
	// success audit. The failure audit must use a fresh, available transaction.
	if _, err := f.store.DB().Exec("CREATE TRIGGER reject_codex_success BEFORE INSERT ON management_operation_items WHEN NEW.action='connect' AND NEW.outcome='succeeded' BEGIN SELECT RAISE(ABORT,'synthetic audit write failure'); END"); err != nil {
		t.Fatal(err)
	}
	ctx, event := auditContext("codex.login")
	pending, err := f.m.Begin(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { f.m.workers.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(7 * time.Second):
		t.Fatal("failed login transaction did not finish")
	}
	status, err := f.m.Status(t.Context())
	if err != nil || status.Login == nil || status.Login.State != "failed" {
		t.Fatalf("failed audit status: %+v %v", status, err)
	}
	items := auditOperation(t, f, event, managementlog.Failed, pending.Login.ID)
	if len(items) != 2 || len(items[1].Changes) != 0 {
		t.Fatalf("rolled back business change was recorded: %+v", items)
	}
	var count int
	if err = f.store.ReadDB().QueryRow("SELECT COUNT(*) FROM agent_codex_session").Scan(&count); err != nil || count != 0 {
		t.Fatalf("credential mutation escaped failed audit: %d %v", count, err)
	}
	assertAuditClean(t, f)
}

func TestAuditDeviceLoginAcceptanceFailureDoesNotCreatePendingLogin(t *testing.T) {
	f := newAuditFixture(t)
	if _, err := f.store.DB().Exec("CREATE TRIGGER reject_codex_accept BEFORE INSERT ON management_operations BEGIN SELECT RAISE(ABORT,'synthetic acceptance failure'); END"); err != nil {
		t.Fatal(err)
	}
	ctx, _ := auditContext("codex.login")
	if _, err := f.m.Begin(ctx, 0); err == nil {
		t.Fatal("login started without recording its accepted operation")
	}
	status, err := f.m.Status(t.Context())
	if err != nil || status.Login != nil || status.Connected {
		t.Fatalf("rejected acceptance left pending state: %+v %v", status, err)
	}
	assertAuditClean(t, f)
}

func TestAuditDeviceLoginInitialRequestFailureClosesAccepted(t *testing.T) {
	f := newAuditFixture(t)
	f.m.client.issuer += "/unrecognized"
	ctx, event := auditContext("codex.login")
	if _, err := f.m.Begin(ctx, 0); err == nil {
		t.Fatal("unexpected device-code response was accepted")
	}
	meta, _ := managementlog.MetadataFromContext(ctx)
	items := auditOperation(t, f, event, managementlog.Failed, meta.CorrelationID)
	if len(items) != 2 || items[1].Code != "device_login_unavailable" {
		t.Fatalf("initial authorization failure audit: %+v", items)
	}
	status, err := f.m.Status(t.Context())
	if err != nil || status.Login == nil || status.Login.State != "failed" || status.Login.UserCode != "" {
		t.Fatalf("initial failure retained pending credentials: %+v %v", status, err)
	}
	assertAuditClean(t, f)
}
