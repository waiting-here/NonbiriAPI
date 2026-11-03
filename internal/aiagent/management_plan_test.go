package aiagent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
)

type managementTestOperations struct {
	*fakeOperations
	execute func(context.Context) (json.RawMessage, error)
	calls   atomic.Int32
}

func (o *managementTestOperations) Tools(ctx context.Context, a Actor, automatic bool) ([]Tool, error) {
	tools, err := o.fakeOperations.Tools(ctx, a, automatic)
	if !automatic {
		tools = append(tools, Tool{Name: "propose_management_operation", ChatOnly: true, Parameters: json.RawMessage(`{"type":"object","properties":{}}`)})
	}
	return tools, err
}
func (o *managementTestOperations) PreviewManagement(context.Context, Actor, Operation) (OperationPreview, error) {
	return OperationPreview{Title: "Synthetic management change"}, nil
}
func (o *managementTestOperations) ExecuteManagement(ctx context.Context, _ Actor, _ Operation, _ string) (json.RawMessage, error) {
	o.calls.Add(1)
	if o.execute != nil {
		return o.execute(ctx)
	}
	return json.RawMessage(`{"status_code":200,"body":{"ok":true}}`), nil
}
func managementPlanFixture(t *testing.T) (fixture, *managementTestOperations, Job, Plan) {
	t.Helper()
	f := newFixture(t)
	ops := &managementTestOperations{fakeOperations: f.ops}
	f.s.config.Operations = ops
	f.driver.replies = []string{`{"role":"assistant","tool_calls":[{"id":"management1","type":"function","function":{"name":"propose_management_operation","arguments":"{\"route\":\"PATCH /admin/api/site-config/{key}\"}"}}]}`}
	job := f.send(t, "Change a setting", "mg")
	if err := f.s.run(context.Background(), job.ID); err != nil {
		t.Fatal(err)
	}
	chat, err := f.s.Conversation(context.Background(), f.actor)
	if err != nil || len(chat.Plans) != 1 {
		t.Fatal("missing plan", chat, err)
	}
	return f, ops, job, chat.Plans[0]
}

func TestManagementPlanAtMostOnceAndCancellation(t *testing.T) {
	for _, cancel := range []bool{false, true} {
		t.Run(map[bool]string{false: "complete", true: "cancel"}[cancel], func(t *testing.T) {
			f, ops, job, plan := managementPlanFixture(t)
			started, release := make(chan struct{}), make(chan struct{})
			ops.execute = func(ctx context.Context) (json.RawMessage, error) {
				close(started)
				select {
				case <-release:
					return json.RawMessage(`{"status_code":200}`), nil
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			}
			done := make(chan error, 1)
			go func() { _, err := f.s.Decide(context.Background(), f.actor, plan.ID, true, ""); done <- err }()
			<-started
			replay, err := f.s.Decide(context.Background(), f.actor, plan.ID, true, "again")
			if err != nil || replay.State != "approved" || !strings.Contains(string(replay.Result), "executing") || ops.calls.Load() != 1 {
				t.Fatal("duplicate execution", replay, err, ops.calls.Load())
			}
			if cancel {
				if err = f.s.Cancel(context.Background(), f.actor, job.ID); err != nil {
					t.Fatal(err)
				}
			} else {
				close(release)
			}
			if err = <-done; err != nil {
				t.Fatal(err)
			}
			chat, err := f.s.Conversation(context.Background(), f.actor)
			if err != nil {
				t.Fatal(err)
			}
			if cancel && (chat.Job.State != "cancelled" || !strings.Contains(string(chat.Plans[0].Result), "unknown")) {
				t.Fatal("cancel resumed job or invented outcome", chat)
			}
			if ops.calls.Load() != 1 {
				t.Fatal("duplicate executor")
			}
		})
	}
}

func TestManagementPlanRecoveryAndRevokedAuthority(t *testing.T) {
	f, ops, job, plan := managementPlanFixture(t)
	*f.allow = false
	if _, err := f.s.Decide(context.Background(), f.actor, plan.ID, true, ""); !errors.Is(err, ErrForbidden) || ops.calls.Load() != 0 {
		t.Fatal("revoked approval executed", err)
	}
	*f.allow = true
	if _, err := f.db.Exec(`UPDATE ai_agent_plans SET state='approved',result='{"status":"executing"}' WHERE id=?`, plan.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`UPDATE ai_agent_jobs SET state='running' WHERE id=?`, job.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.s.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	after, err := f.s.Decide(context.Background(), f.actor, plan.ID, true, "retry after restart")
	if err != nil || after.State != "failed" || !strings.Contains(string(after.Result), "unknown") || ops.calls.Load() != 0 {
		t.Fatal("recovery repeated an uncertain operation", after, err)
	}
}

func TestManagementPlanCannotMixAtomicBatchOrBypassToolSwitch(t *testing.T) {
	f, _, _, _ := managementPlanFixture(t)
	if _, err := f.s.previewOperations(context.Background(), f.actor, []Operation{{Kind: "management.api", Input: json.RawMessage(`{}`)}, {Kind: "memory.edit", Input: json.RawMessage(`{}`)}}); err == nil {
		t.Fatal("mixed transaction plan admitted")
	}
	var call toolCall
	if err := json.Unmarshal([]byte(`{"id":"spoof","type":"function","function":{"name":"propose_operations","arguments":"{\"operations\":[{\"kind\":\"management.api\",\"input\":{}}]}"}}`), &call); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := collectToolWrites([]toolCall{call}, map[string]bool{"propose_operations": true}); !errors.Is(err, ErrForbidden) {
		t.Fatal("disabled management tool bypassed", err)
	}
}

func TestManagementHandlerPanicIsUncertainAndNeverReplayed(t *testing.T) {
	f, ops, _, plan := managementPlanFixture(t)
	ops.execute = func(context.Context) (json.RawMessage, error) { panic("synthetic interruption") }
	result, err := f.s.Decide(t.Context(), f.actor, plan.ID, true, "")
	if err != nil || result.State != "failed" || !strings.Contains(string(result.Result), "unknown") {
		t.Fatal(result, err)
	}
	if _, err = f.s.Decide(t.Context(), f.actor, plan.ID, true, ""); err != nil || ops.calls.Load() != 1 {
		t.Fatal("uncertain operation repeated", err)
	}
}
