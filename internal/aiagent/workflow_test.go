package aiagent

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/agentrequest"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	_ "modernc.org/sqlite"
)

type fakeDriver struct {
	mu            sync.Mutex
	inputs        []agentrequest.Input
	replies       []string
	unconsumed    []agentrequest.Request
	acked         []int64
	unconfigured  bool
	settings      *agentrequest.Settings
	finishReasons []string
	afterWait     func(int64)
}

func (d *fakeDriver) Ready(context.Context, *sql.Tx) (bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return !d.unconfigured, nil
}

func (d *fakeDriver) Settings(context.Context) (agentrequest.Settings, error) {
	if d.settings != nil {
		return *d.settings, nil
	}
	return agentrequest.Settings{ContextWindow: 8192, MaxOutputTokens: 1024}, nil
}
func (d *fakeDriver) Submit(_ context.Context, in agentrequest.Input) (agentrequest.Request, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.inputs = append(d.inputs, in)
	return agentrequest.Request{ID: int64(len(d.inputs))}, nil
}
func (d *fakeDriver) Wait(_ context.Context, id int64) (agentrequest.Result, error) {
	d.mu.Lock()
	content := `{"role":"assistant","content":"已完成说明。"}`
	if int(id) <= len(d.replies) {
		content = d.replies[id-1]
	}
	finish := "stop"
	if int(id) <= len(d.finishReasons) {
		finish = d.finishReasons[id-1]
	}
	afterWait := d.afterWait
	d.mu.Unlock()
	if afterWait != nil {
		afterWait(id)
	}
	return agentrequest.Result{RequestID: id, Message: json.RawMessage(content), FinishReason: finish}, nil
}
func (d *fakeDriver) Cancel(context.Context, int64) error { return nil }
func (d *fakeDriver) AckInTransaction(_ context.Context, _ *sql.Tx, id int64) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.acked = append(d.acked, id)
	return nil
}
func (d *fakeDriver) Unconsumed(context.Context, int) ([]agentrequest.Request, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]agentrequest.Request(nil), d.unconsumed...), nil
}
func (d *fakeDriver) Queue(context.Context, int64) (agentrequest.QueueStatus, error) {
	return agentrequest.QueueStatus{}, nil
}

type fakeOperations struct {
	prepared  []*fakePrepared
	signature string
}
type fakePrepared struct {
	operations []Operation
	finalized  []bool
}

func (p *fakePrepared) Apply(ctx context.Context, tx *sql.Tx) (json.RawMessage, error) {
	for _, op := range p.operations {
		if op.Kind == "fail" {
			return nil, errors.New("target changed")
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO test_mutations(value) VALUES(?)`, op.TargetID); err != nil {
			return nil, err
		}
	}
	return json.RawMessage(`{"done":true}`), nil
}
func (p *fakePrepared) Finalize(done bool) { p.finalized = append(p.finalized, done) }
func (p *fakePrepared) ApplyOperation(ctx context.Context, tx *sql.Tx, index int) (json.RawMessage, error) {
	one := &fakePrepared{operations: []Operation{p.operations[index]}}
	return one.Apply(ctx, tx)
}
func (o *fakeOperations) Tools(context.Context, Actor, bool) ([]Tool, error) {
	return []Tool{{Name: "donations", Parameters: json.RawMessage(`{"type":"object","properties":{}}`)}, {Name: "propose_operations", ChatOnly: true, Parameters: json.RawMessage(`{"type":"object","properties":{}}`)}}, nil
}
func (o *fakeOperations) Read(context.Context, Actor, string, json.RawMessage) (json.RawMessage, error) {
	return json.RawMessage(`{"items":[]}`), nil
}
func (o *fakeOperations) Preview(_ context.Context, _ Actor, ops []Operation) ([]OperationPreview, error) {
	out := []OperationPreview{}
	for _, op := range ops {
		out = append(out, OperationPreview{Title: op.Kind})
	}
	return out, nil
}
func (o *fakeOperations) Prepare(_ context.Context, _ Actor, ops []Operation) (PreparedBatch, error) {
	p := &fakePrepared{operations: ops}
	o.prepared = append(o.prepared, p)
	return p, nil
}
func (o *fakeOperations) Intelligence(context.Context, int64) (json.RawMessage, string, error) {
	return json.RawMessage(`{"donation":"virtual"}`), o.signature, nil
}
func (o *fakeOperations) ReviewOperations(_ context.Context, _ int64, signature string, r ReviewResult) ([]Operation, error) {
	if signature != o.signature {
		return nil, ErrConflict
	}
	if r.Decision == "manual" {
		return nil, nil
	}
	return []Operation{{Kind: "review", TargetID: signature, Input: json.RawMessage(`{}`)}}, nil
}

type fixture struct {
	s      *Service
	db     *sql.DB
	driver *fakeDriver
	ops    *fakeOperations
	actor  Actor
	allow  *bool
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	database, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "agent.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	database.SetMaxOpenConns(1)
	t.Cleanup(func() { database.Close() })
	if _, err = database.Exec(`PRAGMA foreign_keys=ON; CREATE TABLE donations(id INTEGER PRIMARY KEY); INSERT INTO donations(id) VALUES(10)`); err != nil {
		t.Fatal(err)
	}
	schema, err := os.ReadFile(filepath.Join("..", "db", "agent_workflow.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = database.Exec(string(schema)); err != nil {
		t.Fatal(err)
	}
	if _, err = database.Exec(db.AgentPersonalMemorySQL); err != nil {
		t.Fatal(err)
	}
	if _, err = database.Exec(`CREATE TABLE test_mutations(value TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	driver := &fakeDriver{}
	ops := &fakeOperations{signature: "v1"}
	allow := true
	service, err := New(Config{DB: database, Driver: driver, Operations: ops, Now: func() time.Time { return time.Unix(1800000000, 0) }, Authorize: func(_ context.Context, _ *sql.Tx, a Actor, p string) error {
		if !allow {
			return ErrForbidden
		}
		if (p == "ai.config" || p == "ai.archives" || p == "ai.auto") && a.Role != "admin" {
			return ErrForbidden
		}
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { service.Close() })
	settings := defaultSettings()
	settings.ChatEnabled = true
	raw, _ := json.Marshal(settings)
	if _, err = database.Exec(`INSERT INTO ai_agent_settings(id,revision,mode,config_json,owner_id,owner_label,updated_at) VALUES(1,0,'off',?,1,'test',0)`, string(raw)); err != nil {
		t.Fatal(err)
	}
	return fixture{s: service, db: database, driver: driver, ops: ops, actor: Actor{ID: 1, Role: "admin", Label: "测试管理员"}, allow: &allow}
}
func (f fixture) send(t *testing.T, content, key string) Job {
	t.Helper()
	c, err := f.s.Conversation(context.Background(), f.actor)
	if err != nil {
		t.Fatal(err)
	}
	j, err := f.s.Send(context.Background(), f.actor, SendInput{Content: content, ExpectedGeneration: c.Generation, RequestKey: strings.Repeat(key, 20)})
	if err != nil {
		t.Fatal(err)
	}
	return j
}
func (f fixture) plan(t *testing.T, ops []Operation) (Job, int64) {
	t.Helper()
	j := f.send(t, "请执行所列操作", "p")
	if _, err := f.db.Exec(`UPDATE ai_agent_jobs SET state='running' WHERE id=?`, j.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`INSERT INTO ai_agent_messages(conversation_id,generation,role,content,tool_calls,created_at) VALUES(?,?,'assistant','请确认操作','[{"id":"call_1","type":"function","function":{"name":"propose_operations","arguments":"{}"}}]',?)`, j.ConversationID, j.Generation, f.s.now().UnixMilli()); err != nil {
		t.Fatal(err)
	}
	if err := f.s.propose(context.Background(), j, f.actor, ops, []string{"call_1"}); err != nil {
		t.Fatal(err)
	}
	var id int64
	if err := f.db.QueryRow(`SELECT id FROM ai_agent_plans WHERE job_id=?`, j.ID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return j, id
}
func count(t *testing.T, database *sql.DB, table string) int {
	t.Helper()
	var n int
	if err := database.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestHumanPlanRollsBackWholeOperationAndRetainsFeedback(t *testing.T) {
	f := newFixture(t)
	_, id := f.plan(t, []Operation{{Kind: "write", TargetID: "first", Input: json.RawMessage(`{}`)}, {Kind: "fail", TargetID: "second", Input: json.RawMessage(`{}`)}})
	p, err := f.s.Decide(context.Background(), f.actor, id, true, "已核对")
	if err != nil {
		t.Fatal(err)
	}
	if p.State != "failed" || count(t, f.db, "test_mutations") != 0 {
		t.Fatalf("partial execution: %+v", p)
	}
	if len(f.ops.prepared) != 1 || fmt.Sprint(f.ops.prepared[0].finalized) != "[false]" {
		t.Fatal("rollback finalizer", f.ops.prepared)
	}
	var content string
	if err = f.db.QueryRow(`SELECT content FROM ai_agent_messages WHERE role='tool' AND tool_call_id='call_1'`).Scan(&content); err != nil || !strings.Contains(content, "target changed") {
		t.Fatal(content, err)
	}
}

func TestHumanPlanReceiptPreventsDuplicateExecution(t *testing.T) {
	f := newFixture(t)
	_, id := f.plan(t, []Operation{{Kind: "write", TargetID: "one", Input: json.RawMessage(`{}`)}})
	for i := 0; i < 2; i++ {
		p, err := f.s.Decide(context.Background(), f.actor, id, true, "")
		if err != nil || p.State != "approved" {
			t.Fatal(p, err)
		}
	}
	if count(t, f.db, "test_mutations") != 1 || len(f.ops.prepared) != 1 || fmt.Sprint(f.ops.prepared[0].finalized) != "[true]" {
		t.Fatal("duplicate execution or finalizer")
	}
}

func TestPlanConfirmationRechecksPermission(t *testing.T) {
	f := newFixture(t)
	_, id := f.plan(t, []Operation{{Kind: "write", TargetID: "one", Input: json.RawMessage(`{}`)}})
	*f.allow = false
	if _, err := f.s.Decide(context.Background(), f.actor, id, true, ""); !errors.Is(err, ErrForbidden) {
		t.Fatal(err)
	}
	if count(t, f.db, "test_mutations") != 0 {
		t.Fatal("revoked actor mutated site")
	}
}

func TestConversationEditingAndClearPreserveArchive(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	first := f.send(t, "第一条", "a")
	if err := f.s.run(ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	second := f.send(t, "第二条", "b")
	if err := f.s.run(ctx, second.ID); err != nil {
		t.Fatal(err)
	}
	c, err := f.s.Conversation(ctx, f.actor)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Messages) != 4 {
		t.Fatal(c.Messages)
	}
	actor := f.actor
	actor.Role = "level5"
	if _, err = f.s.Edit(ctx, actor, c.Messages[0].ID, SendInput{Content: "不能改旧消息", ExpectedGeneration: c.Generation, RequestKey: "edit-old-user-0001"}); !errors.Is(err, ErrForbidden) {
		t.Fatal(err)
	}
	j, err := f.s.Edit(ctx, actor, c.Messages[2].ID, SendInput{Content: "修改最新用户消息", ExpectedGeneration: c.Generation, RequestKey: "edit-latest-user-0001"})
	if err != nil {
		t.Fatal(err)
	}
	if err = f.s.run(ctx, j.ID); err != nil {
		t.Fatal(err)
	}
	c, err = f.s.Conversation(ctx, f.actor)
	if err != nil || len(c.Messages) != 4 {
		t.Fatal(c, err)
	}
	if count(t, f.db, "ai_agent_messages") != 6 {
		t.Fatal("edit destroyed archived suffix")
	}
	if err = f.s.Clear(ctx, f.actor, c.Generation); err != nil {
		t.Fatal(err)
	}
	c, err = f.s.Conversation(ctx, f.actor)
	if err != nil || len(c.Messages) != 0 || count(t, f.db, "ai_agent_messages") != 6 {
		t.Fatal(c, err)
	}
	archive, err := f.s.Messages(ctx, f.actor, f.actor.ID, 0, true)
	if err != nil || len(archive) != 6 {
		t.Fatal(len(archive), err)
	}
}

func TestEditedSummaryIsInvalidated(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	j := f.send(t, "将被总结", "a")
	if err := f.s.run(ctx, j.ID); err != nil {
		t.Fatal(err)
	}
	c, _ := f.s.Conversation(ctx, f.actor)
	if _, err := f.db.Exec(`UPDATE ai_agent_conversations SET summary='旧摘要',summary_through_id=? WHERE id=?`, c.Messages[0].ID, c.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.Edit(ctx, f.actor, c.Messages[0].ID, SendInput{Content: "事实已更新", ExpectedGeneration: c.Generation, RequestKey: "edit-admin-user-0001"}); err != nil {
		t.Fatal(err)
	}
	c, err := f.s.Conversation(ctx, f.actor)
	if err != nil || c.Summary != "" {
		t.Fatal(c, err)
	}
}

func TestAutomaticReviewOnlyUsesEnabledSubmissionEvents(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	emit := func() {
		t.Helper()
		tx, err := f.db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		if err = f.s.PendingSubmission(ctx, tx, 10); err != nil {
			tx.Rollback()
			t.Fatal(err)
		}
		if err = tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	emit()
	if count(t, f.db, "ai_agent_review_events") != 0 {
		t.Fatal("disabled event queued")
	}
	settings := defaultSettings()
	settings.Mode = "preview"
	if _, err := f.s.PutSettings(ctx, f.actor, SettingsUpdate{Settings: settings}); err != nil {
		t.Fatal(err)
	}
	if err := f.s.collectReviewEvents(ctx); err != nil {
		t.Fatal(err)
	}
	if count(t, f.db, "ai_agent_jobs") != 0 {
		t.Fatal("enable scanned backlog")
	}
	emit()
	emit()
	if count(t, f.db, "ai_agent_review_events") != 1 {
		t.Fatal("submission versions not coalesced")
	}
	if err := f.s.collectReviewEvents(ctx); err != nil {
		t.Fatal(err)
	}
	if count(t, f.db, "ai_agent_jobs") != 1 {
		t.Fatal("enabled submission not queued")
	}
}

func TestPreviewReviewNeverBecomesAutomaticByModeSwitch(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	settings := defaultSettings()
	settings.Mode = "auto"
	if _, err := f.s.PutSettings(ctx, f.actor, SettingsUpdate{Settings: settings}); err != nil {
		t.Fatal(err)
	}
	r, err := f.db.Exec(`INSERT INTO ai_agent_jobs(kind,actor_id,actor_role,actor_label,donation_id,revision_signature,mode,state,created_at,updated_at) VALUES('auto_review',1,'admin','测试',10,'v1','preview','running',0,0)`)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := r.LastInsertId()
	job, actor, err := f.s.loadJob(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.s.finishReview(ctx, job, actor, ReviewResult{Decision: "reject", InternalReason: "资料不足", UserNote: "请补充说明"}); err != nil {
		t.Fatal(err)
	}
	if count(t, f.db, "test_mutations") != 0 || len(f.ops.prepared) != 0 {
		t.Fatal("preview applied after auto mode enabled")
	}
}

func TestMemoryChangeAndBusinessActionShareTransaction(t *testing.T) {
	f := newFixture(t)
	input, _ := json.Marshal(memoryEdit{Kind: "chat", Content: "不能单独提交", ExpectedRevision: 0})
	_, id := f.plan(t, []Operation{{Kind: "memory.edit", TargetID: "chat", Input: input}, {Kind: "fail", Input: json.RawMessage(`{}`)}})
	if _, err := f.s.Decide(context.Background(), f.actor, id, true, ""); err != nil {
		t.Fatal(err)
	}
	if count(t, f.db, "ai_agent_memories") != 0 || count(t, f.db, "ai_agent_memory_history") != 0 {
		t.Fatal("memory escaped failed plan")
	}
}

func TestCompactionKeepsToolCallResultPairAndOriginals(t *testing.T) {
	calls := json.RawMessage(`[{"id":"t1","type":"function","function":{"name":"donations","arguments":"{}"}}]`)
	history := []Message{{Role: "user", Content: strings.Repeat("old", 100)}, {Role: "assistant", ToolCalls: calls}, {Role: "tool", ToolCallID: "t1", Content: "result"}, {Role: "assistant", Content: "answer"}}
	cut := compactCut(history, 60)
	if cut == 2 {
		t.Fatal("split tool call/result")
	}
	f := newFixture(t)
	ctx := context.Background()
	j := f.send(t, "保留目标与结果", "c")
	if err := f.s.run(ctx, j.ID); err != nil {
		t.Fatal(err)
	}
	j = f.send(t, "/compact 保留资源编号", "d")
	before := count(t, f.db, "ai_agent_messages")
	if err := f.s.run(ctx, j.ID); err != nil {
		t.Fatal(err)
	}
	c, err := f.s.Conversation(ctx, f.actor)
	if err != nil || c.Summary == "" || count(t, f.db, "ai_agent_messages") != before {
		t.Fatal(c, err)
	}
	if got := f.driver.inputs[len(f.driver.inputs)-1].Kind; got != "compaction" {
		t.Fatal(got)
	}
}
