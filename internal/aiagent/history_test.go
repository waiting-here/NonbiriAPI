package aiagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/managementlog"
)

func historyFixture(t *testing.T) fixture {
	t.Helper()
	f := newFixture(t)
	_, err := f.db.Exec(`CREATE TABLE users(id INTEGER PRIMARY KEY, username TEXT, is_admin INTEGER, is_banned INTEGER DEFAULT 0, level INTEGER, auto_level INTEGER DEFAULT 1);
 INSERT INTO users(id,username,is_admin,level) VALUES(1,'Stored administrator',1,1),(2,'Stored steward',0,6);` + db.ManagementOperationsSQL)
	if err != nil {
		t.Fatal(err)
	}
	f.s.config.History = managementlog.New(f.db, f.db)
	return f
}
func historyRecords(t *testing.T, f fixture) []managementlog.Operation {
	t.Helper()
	page, err := f.s.config.History.Query(context.Background(), 1, managementlog.Query{View: "operations"})
	if err != nil {
		t.Fatal(err)
	}
	return page.Operations
}
func TestHistoryManualPlanAttributionReplayAndRejection(t *testing.T) {
	for _, approve := range []bool{true, false} {
		for _, steward := range []bool{false, true} {
			t.Run(fmt.Sprintf("approve=%v/steward=%v", approve, steward), func(t *testing.T) {
				f := historyFixture(t)
				if steward {
					f.actor = Actor{ID: 2, Role: "level6", Label: "Stale label"}
				}
				_, id := f.plan(t, []Operation{{Kind: "write", TargetID: "target", Input: json.RawMessage(`{"note":"private text"}`)}})
				ctx := managementlog.WithOperation(context.Background(), managementlog.Metadata{Source: managementlog.SourceWeb, ExecutorKind: "human", Actor: managementlog.Subject{ID: fmt.Sprint(f.actor.ID)}, Domain: "ai", Action: "POST", Summary: "outer"})
				plan, err := f.s.Decide(ctx, f.actor, id, approve, "private feedback")
				if err != nil {
					t.Fatal(err)
				}
				expected := "rejected"
				if approve {
					expected = "approved"
				}
				if plan.State != expected {
					t.Fatal(plan)
				}
				tx, err := f.db.BeginTx(ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				if err = managementlog.Commit(ctx, tx); err != nil {
					t.Fatal(err)
				}
				if _, err = f.s.Decide(context.Background(), f.actor, id, approve, "replay"); err != nil {
					t.Fatal(err)
				}
				records := historyRecords(t, f)
				if len(records) != 1 {
					t.Fatal(records)
				}
				r := records[0]
				if r.CorrelationKind != "ai_plan" || r.CorrelationID != fmt.Sprint(id) {
					t.Fatal(r)
				}
				subjectName := "Stored administrator"
				if steward {
					subjectName = "Stored steward"
				}
				if approve {
					if r.Source != managementlog.SourceAIManual || r.ExecutorKind != "ai" || r.Authorizer.Name != subjectName || r.Authorizer.ID != fmt.Sprint(f.actor.ID) || !strings.Contains(r.AuthorizationRef, "/job/") || r.Outcome != managementlog.Succeeded {
						t.Fatal(r)
					}
				} else if r.Source != managementlog.SourceWeb || r.Authorizer.ID != "" || r.AuthorizationRef != "" || r.Actor.Name != subjectName || r.Outcome != managementlog.Rejected {
					t.Fatal(r)
				}
				items, err := f.s.config.History.Items(ctx, 1, r.ID, 1, 100)
				if err != nil {
					t.Fatal(err)
				}
				encoded, _ := json.Marshal(items)
				if strings.Contains(string(encoded), "private") {
					t.Fatal(string(encoded))
				}
			})
		}
	}
}
func TestHistoryRollbackCannotClaimSuccessfulSteps(t *testing.T) {
	f := historyFixture(t)
	input, _ := json.Marshal(memoryEdit{Kind: "chat", Content: "private memory", ExpectedRevision: 0})
	_, id := f.plan(t, []Operation{{Kind: "memory.edit", Input: input}, {Kind: "write", TargetID: "first", Input: json.RawMessage(`{}`)}, {Kind: "fail", TargetID: "last", Input: json.RawMessage(`{}`)}})
	plan, err := f.s.Decide(context.Background(), f.actor, id, true, "")
	if err != nil || plan.State != "failed" {
		t.Fatal(plan, err)
	}
	if count(t, f.db, "test_mutations") != 0 || count(t, f.db, "ai_agent_memories") != 0 {
		t.Fatal("partial write survived")
	}
	r := historyRecords(t, f)
	if len(r) != 1 || r[0].Outcome != managementlog.Failed || r[0].Succeeded != 0 || r[0].Failed != 3 {
		t.Fatal(r)
	}
	items, err := f.s.config.History.Items(context.Background(), 1, r[0].ID, 1, 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range items.Items {
		if item.Outcome != managementlog.Failed {
			t.Fatal(item)
		}
	}
}
func TestHistoryFailurePreventsBusinessCommit(t *testing.T) {
	f := historyFixture(t)
	_, id := f.plan(t, []Operation{{Kind: "write", TargetID: "one", Input: json.RawMessage(`{}`)}})
	if _, err := f.db.Exec(`CREATE TRIGGER reject_history BEFORE INSERT ON management_operations BEGIN SELECT RAISE(ABORT,'history unavailable'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.Decide(context.Background(), f.actor, id, true, ""); err == nil {
		t.Fatal("missing audit committed")
	}
	if count(t, f.db, "test_mutations") != 0 {
		t.Fatal("business write escaped audit failure")
	}
	var state string
	if err := f.db.QueryRow("SELECT state FROM ai_agent_plans WHERE id=?", id).Scan(&state); err != nil || state != "pending" {
		t.Fatal(state, err)
	}
}
func TestHistorySettingsAndMemoriesContainNoValues(t *testing.T) {
	f := historyFixture(t)
	ctx := context.Background()
	settings := defaultSettings()
	settings.ChatEnabled = true
	settings.ToolDescriptions["donations"] = "custom private instructions"
	delete(settings.Tools, "management_history")
	if _, err := f.s.PutSettings(ctx, f.actor, SettingsUpdate{Settings: settings}); err != nil {
		t.Fatal(err)
	}
	saved, err := f.s.Settings(ctx, f.actor)
	if err != nil || !saved.Tools["management_history"] || saved.ToolDescriptions["donations"] != "custom private instructions" {
		t.Fatal(saved, err)
	}
	if _, err = f.s.PutMemory(ctx, f.actor, "chat", "private memory", 0); err != nil {
		t.Fatal(err)
	}
	steward := Actor{ID: 2, Role: "level6", Label: "steward"}
	if _, err = f.s.PutPersonalMemory(ctx, steward, 0, "private personal", 0); err != nil {
		t.Fatal(err)
	}
	if _, err = f.s.PutPersonalMemory(ctx, steward, 1, "forbidden", 0); !errors.Is(err, ErrForbidden) {
		t.Fatal(err)
	}
	records := historyRecords(t, f)
	if len(records) != 3 {
		t.Fatal(records)
	}
	for _, r := range records {
		if r.Source != managementlog.SourceWeb || r.ExecutorKind != "human" || r.Outcome != managementlog.Succeeded {
			t.Fatal(r)
		}
		items, err := f.s.config.History.Items(ctx, 1, r.ID, 1, 100)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(items)
		if strings.Contains(string(raw), "private") || strings.Contains(string(raw), "instructions") {
			t.Fatal(string(raw))
		}
	}
}

type historyReviewOperations struct {
	*fakeOperations
	fail bool
}

func (o historyReviewOperations) ReviewOperations(ctx context.Context, id int64, sig string, r ReviewResult) ([]Operation, error) {
	if o.fail {
		return []Operation{{Kind: "write", TargetID: "first", Input: json.RawMessage(`{}`)}, {Kind: "fail", TargetID: "second", Input: json.RawMessage(`{}`)}}, nil
	}
	return o.fakeOperations.ReviewOperations(ctx, id, sig, r)
}
func TestHistoryAutoReviewUsesCurrentConfigurationAndOnlyExecution(t *testing.T) {
	for _, mode := range []string{"auto", "approve", "preview", "manual", "failed", "invalid", "revoked"} {
		t.Run(mode, func(t *testing.T) {
			f := historyFixture(t)
			ctx := context.Background()
			if _, err := f.db.Exec("UPDATE ai_agent_settings SET mode='auto',revision=7,owner_label='stale'"); err != nil {
				t.Fatal(err)
			}
			jobMode := "auto"
			decision := "reject"
			signature := "v1"
			if mode == "preview" {
				jobMode = "preview"
			}
			if mode == "manual" {
				decision = "manual"
			}
			if mode == "invalid" {
				signature = "old"
			}
			if mode == "failed" {
				f.s.config.Operations = historyReviewOperations{fakeOperations: f.ops, fail: true}
			}
			row, err := f.db.Exec(`INSERT INTO ai_agent_jobs(kind,actor_id,actor_role,actor_label,donation_id,revision_signature,mode,state,created_at,updated_at) VALUES('auto_review',1,'admin','stale',10,?,?,'running',0,0)`, signature, jobMode)
			if err != nil {
				t.Fatal(err)
			}
			id, _ := row.LastInsertId()
			job, actor, err := f.s.loadJob(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "revoked" {
				*f.allow = false
			}
			outcome := ReviewResult{Decision: decision, InternalReason: "private reason", UserNote: "private note"}
			if mode == "approve" {
				outcome.Decision = "approve"
				outcome.Bindings = []ReviewBinding{{}}
			}
			err = f.s.finishReview(ctx, job, actor, outcome)
			if mode == "revoked" {
				if !errors.Is(err, ErrForbidden) {
					t.Fatal(err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			records := historyRecords(t, f)
			if mode == "manual" || mode == "preview" {
				if len(records) != 0 {
					t.Fatal(records)
				}
				return
			}
			if len(records) != 1 {
				t.Fatal(records)
			}
			r := records[0]
			expected := managementlog.Succeeded
			if mode == "failed" {
				expected = managementlog.Failed
			}
			if mode == "invalid" || mode == "revoked" {
				expected = managementlog.Rejected
			}
			if r.Source != managementlog.SourceAIAuto || r.Authorizer.Name != "Stored administrator" || r.AuthorizationRef != "ai_config/7" || r.CorrelationID != fmt.Sprint(id) || r.Outcome != expected {
				t.Fatal(r)
			}
			if mode == "failed" && count(t, f.db, "test_mutations") != 0 {
				t.Fatal("failed automatic transaction committed")
			}
			if mode == "revoked" {
				return
			}
			if err = f.s.finishReview(ctx, job, actor, outcome); err == nil {
				t.Fatal("finished job executed twice")
			}
			if len(historyRecords(t, f)) != 1 {
				t.Fatal("duplicate automatic history")
			}
		})
	}
}
func TestHistoryCleanupProposalFreezesServerCutoff(t *testing.T) {
	f := historyFixture(t)
	ctx := context.Background()
	if _, err := f.s.PutMemory(ctx, f.actor, "chat", "one", 0); err != nil {
		t.Fatal(err)
	}
	_, id := f.plan(t, []Operation{{Kind: "management.cleanup", Input: json.RawMessage(`{"all":true}`)}})
	var raw string
	if err := f.db.QueryRow("SELECT operations FROM ai_agent_plans WHERE id=?", id).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var ops []Operation
	if err := json.Unmarshal([]byte(raw), &ops); err != nil {
		t.Fatal(err)
	}
	var prepared managementlog.PreparedCleanup
	if err := json.Unmarshal(ops[0].Input, &prepared); err != nil {
		t.Fatal(err)
	}
	if prepared.CutoffMaxID != 1 || !prepared.All {
		t.Fatal(prepared)
	}
	if _, err := f.s.PutMemory(ctx, f.actor, "chat", "later", 1); err != nil {
		t.Fatal(err)
	}
	if prepared.CutoffMaxID != 1 {
		t.Fatal("cutoff changed")
	}
	_, err := f.s.freezeHistoryOperations(ctx, f.actor, []Operation{{Kind: "management.cleanup", Input: json.RawMessage(`{"all":true,"cutoff_max_id":"999"}`)}})
	if !errors.Is(err, ErrInvalid) {
		t.Fatal("model supplied cutoff accepted", err)
	}
	_, err = f.s.freezeHistoryOperations(ctx, Actor{ID: 2, Role: "level6"}, []Operation{{Kind: "management.cleanup", Input: json.RawMessage(`{"all":true}`)}})
	if !errors.Is(err, ErrForbidden) {
		t.Fatal(err)
	}
}

func TestHistoryReceiptsCaptureSkipsAndCreatedTargets(t *testing.T) {
	for _, row := range []struct {
		added string
		want  managementlog.Outcome
	}{{"0", managementlog.NoChange}, {"2", managementlog.Partial}} {
		item := managementlog.Item{Outcome: managementlog.Succeeded}
		historyReceipt(&item, Operation{Kind: "charity.binding.apply_preset"}, json.RawMessage(fmt.Sprintf(`{"added_count":"%s","skipped_count":"3"}`, row.added)))
		if item.Outcome != row.want || item.Code != "already_bound_or_missing" {
			t.Fatal(item)
		}
	}
	item := managementlog.Item{}
	historyReceipt(&item, Operation{Kind: "charity.model.create"}, json.RawMessage(`{"id":"32","public_description":"private body"}`))
	if item.TargetID != "32" || item.TargetKind != "charity_model" || item.TargetName != "" {
		t.Fatal(item)
	}
}
