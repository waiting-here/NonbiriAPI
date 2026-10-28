package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/agentplan"
	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/dbfixture"
	"github.com/waiting-here/NonbiriAPI/internal/managementlog"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

func agentHistoryFixture(t *testing.T) (*agentOperations, *sql.DB, agentplan.Actor, agentplan.Actor) {
	t.Helper()
	vault, err := secret.New(bytes.Repeat([]byte{0x35}, secret.MasterKeyBytes))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { vault.Close() })
	path := filepath.Join(t.TempDir(), "history.db")
	dbfixture.Materialize(t, path, vault)
	store, err := db.Open(path, vault)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	database := store.DB()
	database.SetMaxOpenConns(1)
	create := func(admin bool) agentplan.Actor {
		t.Helper()
		name, role, level := "history-steward", "level6", 6
		if admin {
			name, role, level = "history-admin", "admin", 1
		}
		zero := make([]byte, 16)
		revision := make([]byte, 16)
		revision[15] = 1
		row, err := database.Exec(`INSERT INTO users(discord_id,username,is_admin,is_banned,donation_credit_mag,level,auto_level,total_requests,total_uncached_input_tokens,total_cache_write_input_tokens,total_cache_read_input_tokens,total_output_tokens,total_unknown_usage_requests,revision,created_at,updated_at) VALUES(?,?,?,0,?,?,1,?,?,?,?,?,?,?,0,0)`, name, name, admin, zero, level, zero, zero, zero, zero, zero, zero, revision)
		if err != nil {
			t.Fatal(err)
		}
		id, _ := row.LastInsertId()
		_, err = database.Exec(`INSERT INTO sessions(token_hash,user_id,last_seen_at,expires_at,absolute_expires_at,created_at,cred_gen) VALUES(?,?,0,253402300799,253402300799,0,'g1')`, name, id)
		if err != nil {
			t.Fatal(err)
		}
		return agentplan.Actor{ID: id, Role: role, Label: name, SessionHash: name, SessionGeneration: "g1"}
	}
	admin, steward := create(true), create(false)
	_, err = database.Exec("UPDATE site_config SET value='0' WHERE key='mfa_required_level_6'")
	if err != nil {
		t.Fatal(err)
	}
	o := &agentOperations{read: database, authority: agentAuthority{authorizer: authz.New(authz.Options{})}, history: managementlog.New(database, database)}
	return o, database, admin, steward
}
func addAgentHistoryRecord(t *testing.T, o *agentOperations, actor agentplan.Actor, name string) int64 {
	t.Helper()
	ctx := context.Background()
	tx, err := o.read.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	id, err := managementlog.RecordTx(ctx, tx, managementlog.Record{Metadata: managementlog.Metadata{EventID: name, Source: managementlog.SourceWeb, ExecutorKind: "human", Actor: managementlog.Subject{ID: fmt.Sprint(actor.ID)}, Domain: "users", Action: "update", Summary: "Update"}, Result: managementlog.Result{Outcome: managementlog.Succeeded}})
	if err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return id
}
func TestAgentHistoryToolsEnforceAdministratorAtEveryStage(t *testing.T) {
	o, database, admin, steward := agentHistoryFixture(t)
	ctx := context.Background()
	recordID := addAgentHistoryRecord(t, o, admin, "one")
	for _, args := range []string{`{"action":"list"}`, fmt.Sprintf(`{"action":"detail","id":"%d"}`, recordID), fmt.Sprintf(`{"action":"items","id":"%d"}`, recordID)} {
		if _, err := o.Read(ctx, admin, "management_history", json.RawMessage(args)); err != nil {
			t.Fatal(err)
		}
	}
	var count int
	if err := database.QueryRow("SELECT COUNT(*) FROM management_operations").Scan(&count); err != nil || count != 1 {
		t.Fatal("reads wrote history", count, err)
	}
	prepared, err := o.history.PrepareCleanup(ctx, admin.ID, managementlog.CleanupRequest{All: true})
	if err != nil {
		t.Fatal(err)
	}
	input, _ := json.Marshal(prepared)
	ops := []agentplan.Operation{{Kind: "management.cleanup", Input: input}}
	tools, err := o.Tools(ctx, admin, false)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(tools)
	if !strings.Contains(string(raw), "management_history") || !strings.Contains(string(raw), "management.cleanup") {
		t.Fatal(string(raw))
	}
	tools, err = o.Tools(ctx, steward, false)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = json.Marshal(tools)
	if strings.Contains(string(raw), "management_history") || strings.Contains(string(raw), "management.cleanup") {
		t.Fatal("steward offered administrator tools")
	}
	forged := steward
	forged.Role = "admin"
	automatic := admin
	automatic.Automatic = true
	for _, actor := range []agentplan.Actor{steward, forged, automatic} {
		if _, err = o.Read(ctx, actor, "management_history", json.RawMessage(`{"action":"list"}`)); err == nil {
			t.Fatal("unauthorized read", actor)
		}
		if _, err = o.Preview(ctx, actor, ops); err == nil {
			t.Fatal("unauthorized preview", actor)
		}
		if _, err = o.Prepare(ctx, actor, ops); err == nil {
			t.Fatal("unauthorized prepare", actor)
		}
	}
	batch, err := o.Prepare(ctx, admin, ops)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = database.Exec("UPDATE users SET is_banned=1 WHERE id=?", admin.ID); err != nil {
		t.Fatal(err)
	}
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = batch.ApplyOperation(ctx, tx, 0)
	_ = tx.Rollback()
	batch.Finalize(false)
	if err == nil {
		t.Fatal("revoked administrator applied cleanup")
	}
}
func TestAgentHistoryCleanupUsesOnePlanAndFrozenCutoff(t *testing.T) {
	o, database, admin, _ := agentHistoryFixture(t)
	ctx := context.Background()
	first := addAgentHistoryRecord(t, o, admin, "first")
	frozen, err := o.history.PrepareCleanup(ctx, admin.ID, managementlog.CleanupRequest{All: true})
	if err != nil {
		t.Fatal(err)
	}
	later := addAgentHistoryRecord(t, o, admin, "later")
	input, _ := json.Marshal(frozen)
	ops := []agentplan.Operation{{Kind: "management.cleanup", Input: input}}
	previews, err := o.Preview(ctx, admin, ops)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(previews[0].Before, `"matching_operations": "1"`) {
		t.Fatal(previews)
	}
	emptyInput, _ := json.Marshal(managementlog.PreparedCleanup{All: true, CutoffMaxID: 0})
	empty, err := o.Preview(ctx, admin, []agentplan.Operation{{Kind: "management.cleanup", Input: emptyInput}})
	if err != nil || !strings.Contains(empty[0].Before, `"matching_operations": "0"`) {
		t.Fatal(empty, err)
	}
	batch, err := o.Prepare(ctx, admin, ops)
	if err != nil {
		t.Fatal(err)
	}
	ctx = managementlog.WithOperation(ctx, managementlog.Metadata{Source: managementlog.SourceAIManual, ExecutorKind: "ai", Actor: managementlog.Subject{ID: "ai", Role: "ai"}, Authorizer: managementlog.Subject{ID: fmt.Sprint(admin.ID), Name: admin.Label, Role: "admin"}, AuthorizationRef: "ai_plan/1/job/1", Domain: "ai", Action: "plan.execute", CorrelationKind: "ai_plan", CorrelationID: "1", Summary: "Execute plan"})
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := batch.ApplyOperation(ctx, tx, 0)
	if err != nil {
		tx.Rollback()
		batch.Finalize(false)
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	batch.Finalize(true)
	var job managementlog.CleanupJob
	if err = json.Unmarshal(receipt, &job); err != nil {
		t.Fatal(err)
	}
	if job.CutoffMaxID != first || job.State != "pending" {
		t.Fatal(job)
	}
	for i := 0; i < 10; i++ {
		worked, err := o.history.Step(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if !worked {
			break
		}
	}
	status, err := o.history.CleanupStatus(context.Background(), admin.ID, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if status.State != "completed" || status.DeletedOperations != 1 {
		t.Fatal(status)
	}
	if _, err = o.history.Detail(context.Background(), admin.ID, later); err != nil {
		t.Fatal("new record deleted", err)
	}
	history, err := o.history.Query(context.Background(), admin.ID, managementlog.Query{Filter: managementlog.Filter{Source: managementlog.SourceAIManual}, View: "operations"})
	if err != nil || len(history.Operations) == 0 {
		t.Fatal(history, err)
	}
	for _, r := range history.Operations {
		if r.Authorizer.ID != fmt.Sprint(admin.ID) || r.AuthorizationRef != "ai_plan/1/job/1" {
			t.Fatal(r)
		}
	}
	raw, err := o.Read(context.Background(), admin, "management_history", json.RawMessage(fmt.Sprintf(`{"action":"cleanup_status","id":"%d"}`, job.ID)))
	if err != nil || !strings.Contains(string(raw), "completed") {
		t.Fatal(string(raw), err)
	}
}
