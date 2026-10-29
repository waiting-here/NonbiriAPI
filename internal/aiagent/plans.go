package aiagent

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/managementlog"
)

type memoryEdit struct {
	Kind             string `json:"kind"`
	OwnerID          int64  `json:"owner_id,string,omitempty"`
	Content          string `json:"content"`
	ExpectedRevision int64  `json:"expected_revision,string"`
}

func (s *Service) previewOperations(ctx context.Context, actor Actor, ops []Operation) ([]OperationPreview, error) {
	if len(ops) == 0 || len(ops) > 100 {
		return nil, ErrInvalid
	}
	out := make([]OperationPreview, 0, len(ops))
	for _, op := range ops {
		if op.Kind == "memory.edit" {
			var input memoryEdit
			if err := json.Unmarshal(op.Input, &input); err != nil {
				return nil, ErrInvalid
			}
			old, err := s.previewMemory(ctx, actor, input)
			if err != nil {
				return nil, err
			}
			if old.Revision != input.ExpectedRevision {
				return nil, ErrConflict
			}
			label := "对话记忆"
			if input.Kind == "auto_review" {
				label = "自动审批记忆"
			} else if input.Kind == "personal" {
				label = fmt.Sprintf("专属记忆（站内 ID %d）", old.OwnerID)
			}
			out = append(out, OperationPreview{Title: "修改" + label, Before: old.Content, After: input.Content})
		} else {
			items, err := s.config.Operations.Preview(ctx, actor, []Operation{op})
			if err != nil {
				return nil, err
			}
			if len(items) != 1 {
				return nil, ErrInvalid
			}
			out = append(out, items[0])
		}
	}
	return out, nil
}

func (s *Service) propose(ctx context.Context, job Job, actor Actor, ops []Operation, callIDs []string) error {
	ops, err := s.freezeHistoryOperations(ctx, actor, ops)
	if err != nil {
		return err
	}
	preview, err := s.previewOperations(ctx, actor, ops)
	if err != nil {
		return err
	}
	operationsJSON, err := json.Marshal(ops)
	if err != nil {
		return err
	}
	previewJSON, _ := json.Marshal(preview)
	idsJSON, _ := json.Marshal(callIDs)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = s.checkChatJob(ctx, tx, actor, job.ID, job.Generation, "running"); err != nil {
		return err
	}
	now := s.now().UnixMilli()
	if _, err = tx.ExecContext(ctx, `INSERT INTO ai_agent_plans(job_id,state,operations,preview,tool_call_ids,created_at) VALUES(?,'pending',?,?,?,?)`, job.ID, string(operationsJSON), string(previewJSON), string(idsJSON), now); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE ai_agent_jobs SET state='waiting_confirmation',updated_at=? WHERE id=?`, now, job.ID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Service) checkChatJob(ctx context.Context, tx *sql.Tx, actor Actor, id, generation int64, state string) error {
	if err := s.requireChat(ctx, tx, actor); err != nil {
		return err
	}
	var exists bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM ai_agent_jobs j JOIN ai_agent_conversations c ON c.id=j.conversation_id WHERE j.id=? AND j.actor_id=? AND j.generation=? AND c.generation=j.generation AND j.state=?)`, id, actor.ID, generation, state).Scan(&exists)
	if err != nil {
		return err
	}
	if !exists {
		return ErrConflict
	}
	return nil
}

func readPlan(ctx context.Context, tx *sql.Tx, id int64) (Plan, Job, []string, error) {
	var jobID int64
	if err := tx.QueryRowContext(ctx, `SELECT job_id FROM ai_agent_plans WHERE id=?`, id).Scan(&jobID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			err = ErrNotFound
		}
		return Plan{}, Job{}, nil, err
	}
	plans, err := readPlans(ctx, tx, jobID)
	if err != nil {
		return Plan{}, Job{}, nil, err
	}
	var result Plan
	for _, p := range plans {
		if p.ID == id {
			result = p
			break
		}
	}
	job, err := scanJob(tx.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM ai_agent_jobs WHERE id=?`, jobID))
	if err != nil {
		return Plan{}, Job{}, nil, err
	}
	var raw string
	var ids []string
	if err = tx.QueryRowContext(ctx, `SELECT tool_call_ids FROM ai_agent_plans WHERE id=?`, id).Scan(&raw); err != nil {
		return Plan{}, Job{}, nil, err
	}
	err = json.Unmarshal([]byte(raw), &ids)
	return result, job, ids, err
}

type prepared struct {
	business   PreparedBatch
	steps      []preparedStep
	operations []Operation
	service    *Service
	actor      Actor
}
type preparedStep struct {
	memory        *memoryEdit
	businessIndex int
}

func (s *Service) prepare(ctx context.Context, actor Actor, ops []Operation) (*prepared, error) {
	p := &prepared{service: s, actor: actor, operations: ops}
	business := []Operation{}
	for _, op := range ops {
		if op.Kind == "memory.edit" {
			var value memoryEdit
			if err := json.Unmarshal(op.Input, &value); err != nil {
				return nil, ErrInvalid
			}
			p.steps = append(p.steps, preparedStep{memory: &value})
		} else {
			p.steps = append(p.steps, preparedStep{businessIndex: len(business)})
			business = append(business, op)
		}
	}
	if len(business) > 0 {
		var err error
		p.business, err = s.config.Operations.Prepare(ctx, actor, business)
		if err != nil {
			return nil, err
		}
		if p.business == nil {
			return nil, ErrUnavailable
		}
	}
	return p, nil
}
func (p *prepared) Finalize(committed bool) {
	if p != nil && p.business != nil {
		p.business.Finalize(committed)
	}
}
func (p *prepared) Apply(ctx context.Context, tx *sql.Tx) (json.RawMessage, error) {
	values := []json.RawMessage{}
	items := historyItems(p.operations, managementlog.Succeeded, "")
	for index, step := range p.steps {
		managementlog.SetResult(ctx, managementlog.Result{})
		if item := step.memory; item != nil {
			var v Memory
			var err error
			if item.Kind == "personal" {
				v, err = p.service.writePersonalMemory(ctx, tx, p.actor, item.OwnerID, item.Content, item.ExpectedRevision, p.service.now().UnixMilli())
			} else {
				if err = p.service.memoryAccess(ctx, tx, p.actor, item.Kind, item.OwnerID, true); err != nil {
					return nil, err
				}
				v, err = writeMemory(ctx, tx, p.actor, item.Kind, item.Content, item.ExpectedRevision, p.service.now().UnixMilli())
			}
			if err != nil {
				return nil, err
			}
			raw, err := json.Marshal(v)
			if err != nil {
				return nil, err
			}
			values = append(values, raw)
		} else {
			value, err := p.business.ApplyOperation(ctx, tx, step.businessIndex)
			if err != nil {
				return nil, err
			}
			values = append(values, value)
			historyReceipt(&items[index], p.operations[index], value)
		}
		if outcome := managementlog.OutcomeFromContext(ctx); outcome != "" {
			items[index].Outcome = outcome
		}
		if step.memory != nil && step.memory.Kind == "personal" && step.memory.OwnerID == 0 {
			items[index].TargetID = fmt.Sprint(p.actor.ID)
		}
	}
	managementlog.SetItemsResult(ctx, items)
	return json.Marshal(map[string]any{"status": "completed", "operations": values})
}

func (s *Service) Decide(ctx context.Context, actor Actor, id int64, approve bool, feedback string) (resultPlan Plan, resultErr error) {
	if len(feedback) > 1<<20 {
		return Plan{}, ErrInvalid
	}
	tx, err := s.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return Plan{}, err
	}
	plan, job, _, err := readPlan(ctx, tx, id)
	if err == nil {
		err = s.authorize(ctx, tx, actor, "ai.chat")
	}
	if err == nil && job.ActorID == actor.ID && plan.State != "pending" {
		var active bool
		err = tx.QueryRowContext(ctx, `SELECT EXISTS(
 SELECT 1 FROM ai_agent_plans p JOIN ai_agent_jobs j ON j.id=p.job_id
 JOIN ai_agent_messages m ON m.conversation_id=j.conversation_id,
 json_each(m.tool_calls) call,json_each(p.tool_call_ids) wanted
 WHERE p.id=? AND m.active=1 AND json_extract(call.value,'$.id')=wanted.value
)`, id).Scan(&active)
		if err == nil && !active {
			err = s.authorize(ctx, tx, actor, "ai.archives")
		}
	}
	if err == nil && job.ActorID == actor.ID && plan.State == "pending" {
		ctx, err = s.planHistoryContext(ctx, tx, actor, plan, approve)
	}
	_ = tx.Rollback()
	if err != nil {
		return Plan{}, err
	}
	if job.ActorID != actor.ID {
		return Plan{}, ErrForbidden
	}
	if plan.State != "pending" {
		managementlog.MarkReplay(ctx)
		return plan, nil
	}
	defer func() {
		resultErr = errors.Join(resultErr, s.historyFailureAfterRollback(ctx, plan.Operations, resultErr))
	}()
	var pending *prepared
	if approve {
		pending, err = s.prepare(ctx, actor, plan.Operations)
		if err != nil {
			if errors.Is(err, authz.ErrElevatedRequired) {
				return Plan{}, err
			}
			return s.planFailed(ctx, actor, id, err)
		}
	}
	committed := false
	defer func() {
		if pending != nil {
			pending.Finalize(committed)
		}
	}()
	tx, err = s.db.BeginTx(ctx, nil)
	if err != nil {
		return Plan{}, err
	}
	defer tx.Rollback()
	plan, job, ids, err := readPlan(ctx, tx, id)
	if err != nil {
		return Plan{}, err
	}
	if plan.State != "pending" {
		if err = s.authorize(ctx, tx, actor, "ai.chat"); err != nil {
			return Plan{}, err
		}
		managementlog.MarkReplay(ctx)
		return plan, nil
	}
	if err = s.checkChatJob(ctx, tx, actor, job.ID, job.Generation, "waiting_confirmation"); err != nil {
		return Plan{}, err
	}
	ctx, err = s.planHistory(ctx, tx, actor, plan, approve)
	if err != nil {
		return Plan{}, err
	}
	state := "rejected"
	payload := map[string]any{"status": "rejected", "feedback": feedback}
	if approve {
		result, e := pending.Apply(ctx, tx)
		if e != nil {
			_ = tx.Rollback()
			pending.Finalize(false)
			pending = nil
			if errors.Is(e, authz.ErrElevatedRequired) {
				return Plan{}, e
			}
			return s.planFailed(ctx, actor, id, e)
		}
		state = "approved"
		payload = map[string]any{"status": "completed", "feedback": feedback, "result": result}
	} else {
		managementlog.SetResult(ctx, managementlog.Result{Outcome: managementlog.Rejected, Code: "human_declined",
			Items: []managementlog.Item{{Action: "plan.reject", TargetKind: "ai_plan", TargetID: fmt.Sprint(id), Outcome: managementlog.Rejected, Code: "human_declined"}}})
	}
	raw, _ := json.Marshal(payload)
	if err = s.finishPlan(ctx, tx, plan, job, ids, state, raw); err != nil {
		return Plan{}, err
	}
	if err = managementlog.Commit(ctx, tx); err != nil {
		return Plan{}, err
	}
	committed = true
	now := s.now().UnixMilli()
	plan.State = state
	plan.Result = raw
	plan.DecidedAt = &now
	s.wake()
	return plan, nil
}

func (s *Service) finishPlan(ctx context.Context, tx *sql.Tx, plan Plan, job Job, ids []string, state string, result json.RawMessage) error {
	now := s.now().UnixMilli()
	update, err := tx.ExecContext(ctx, `UPDATE ai_agent_plans SET state=?,result=?,decided_at=? WHERE id=? AND state='pending'`, state, string(result), now, plan.ID)
	if err != nil {
		return err
	}
	n, err := update.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrConflict
	}
	for _, id := range ids {
		if _, err = appendMessage(ctx, tx, job.ConversationID, job.Generation, "tool", string(result), nil, id, now); err != nil {
			return err
		}
	}
	_, err = tx.ExecContext(ctx, `UPDATE ai_agent_jobs SET state='pending',updated_at=? WHERE id=? AND state='waiting_confirmation'`, now, job.ID)
	return err
}

func (s *Service) planFailed(ctx context.Context, actor Actor, id int64, cause error) (Plan, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Plan{}, err
	}
	defer tx.Rollback()
	plan, job, ids, err := readPlan(ctx, tx, id)
	if err != nil {
		return Plan{}, err
	}
	if plan.State != "pending" {
		if err = s.authorize(ctx, tx, actor, "ai.chat"); err != nil {
			return Plan{}, err
		}
		managementlog.MarkReplay(ctx)
		return plan, nil
	}
	if err = s.checkChatJob(ctx, tx, actor, job.ID, job.Generation, "waiting_confirmation"); err != nil {
		return Plan{}, err
	}
	setHistoryFailure(ctx, plan.Operations, cause)
	raw, _ := json.Marshal(map[string]any{"status": "failed", "reason": cause.Error(), "applied": false})
	if err = s.finishPlan(ctx, tx, plan, job, ids, "failed", raw); err != nil {
		return Plan{}, err
	}
	if err = managementlog.Commit(ctx, tx); err != nil {
		return Plan{}, err
	}
	plan.State = "failed"
	plan.Result = raw
	now := s.now().UnixMilli()
	plan.DecidedAt = &now
	s.wake()
	return plan, nil
}

func toolError(err error) json.RawMessage {
	raw, _ := json.Marshal(map[string]any{"error": fmt.Sprint(err)})
	return raw
}
