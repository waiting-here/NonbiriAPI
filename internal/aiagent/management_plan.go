package aiagent

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/managementlog"
)

func (s *Service) decideManagement(ctx context.Context, actor Actor, plan Plan, job Job, feedback string) (Plan, error) {
	manager, ok := s.config.Operations.(ManagementOperations)
	if !ok || actor.Automatic {
		return Plan{}, ErrForbidden
	}
	preview, err := manager.PreviewManagement(ctx, actor, plan.Operations[0])
	if err != nil {
		return s.planFailed(ctx, actor, plan.ID, err)
	}
	if preview.RequiresElevation && actor.Elevation == "" {
		return Plan{}, authz.ErrElevatedRequired
	}
	claimContext := managementlog.WithoutOperation(ctx)
	tx, err := s.db.BeginTx(claimContext, nil)
	if err != nil {
		return Plan{}, err
	}
	defer tx.Rollback()
	current, _, _, err := readPlan(ctx, tx, plan.ID)
	if err != nil {
		return Plan{}, err
	}
	if current.JobID != plan.JobID {
		return Plan{}, ErrConflict
	}
	if current.State != "pending" {
		if err = s.authorize(ctx, tx, actor, "ai.chat"); err != nil {
			return Plan{}, err
		}
		managementlog.MarkReplay(ctx)
		return current, nil
	}
	if err = s.checkChatJob(ctx, tx, actor, job.ID, job.Generation, "waiting_confirmation"); err != nil {
		return Plan{}, err
	}
	now := s.now().UnixMilli()
	var entropy [24]byte
	if _, err = rand.Read(entropy[:]); err != nil {
		return Plan{}, err
	}
	requestKey := "agent-plan-" + hex.EncodeToString(entropy[:])
	claim, _ := json.Marshal(map[string]string{"status": "executing", "request_key": requestKey})
	// Durable at-most-once claim BEFORE calling a handler that owns its commit.
	// A lost response or restart must not automatically repeat a destructive API.
	if _, err = tx.ExecContext(ctx, `UPDATE ai_agent_plans SET state='approved',result=?,decided_at=? WHERE id=? AND state='pending'`, string(claim), now, plan.ID); err != nil {
		return Plan{}, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE ai_agent_jobs SET state='running',updated_at=? WHERE id=?`, now, job.ID); err != nil {
		return Plan{}, err
	}
	if err = tx.Commit(); err != nil {
		return Plan{}, err
	}

	work, cancel := context.WithTimeout(ctx, 2*time.Minute)
	stop := context.AfterFunc(s.ctx, cancel)
	s.mu.Lock()
	if s.managementRunning == nil {
		s.managementRunning = make(map[int64]runningJob)
	}
	s.managementRunning[job.ID] = runningJob{cancel: cancel, kind: job.Kind, actorID: actor.ID}
	s.mu.Unlock()
	defer func() { stop(); cancel(); s.mu.Lock(); delete(s.managementRunning, job.ID); s.mu.Unlock() }()
	var result json.RawMessage
	guard, executeErr := s.read.BeginTx(work, &sql.TxOptions{ReadOnly: true})
	if executeErr == nil {
		executeErr = s.checkChatJob(work, guard, actor, job.ID, job.Generation, "running")
		_ = guard.Rollback()
	}
	if executeErr == nil {
		result, executeErr = executeManagementSafely(work, manager, actor, plan.Operations[0], requestKey)
	}
	status, state := "completed", "approved"
	var receipt struct {
		Status int `json:"status_code"`
		Body   struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		} `json:"body"`
	}
	_ = json.Unmarshal(result, &receipt)
	if receipt.Status == 202 {
		status = "accepted"
	}
	if receipt.Status >= 400 {
		status, state = "failed", "failed"
	}
	if executeErr != nil {
		status, state = "unknown", "failed"
	}
	// Elevation is a known pre-mutation rejection. Keep this exact plan for the
	// interlocutor to re-approve through the existing password challenge.
	needsElevation := errors.Is(executeErr, authz.ErrElevatedRequired) || receipt.Body.Error.Code == "elevated_required"
	payload := map[string]any{"status": status, "feedback": feedback, "request_key": requestKey}
	if len(result) > 0 {
		payload["result"] = result
	}
	if executeErr != nil {
		payload["reason"] = executeErr.Error()
		payload["retry_automatically"] = false
	}
	raw, _ := json.Marshal(payload)
	// Persist the receipt even after HTTP disconnect/cancellation. It is evidence,
	// never permission to resume a cancelled or edited conversation.
	finish, done := context.WithTimeout(context.Background(), 10*time.Second)
	defer done()
	tx, err = s.db.BeginTx(finish, nil)
	if err != nil {
		return Plan{}, err
	}
	defer tx.Rollback()
	var active bool
	if err = tx.QueryRowContext(finish, `SELECT EXISTS(SELECT 1 FROM ai_agent_jobs j JOIN ai_agent_conversations c ON c.id=j.conversation_id WHERE j.id=? AND j.state='running' AND c.generation=j.generation)`, job.ID).Scan(&active); err != nil {
		return Plan{}, err
	}
	if needsElevation && active {
		if _, err = tx.ExecContext(finish, `UPDATE ai_agent_plans SET state='pending',result=NULL,decided_at=NULL WHERE id=? AND state='approved'`, plan.ID); err != nil {
			return Plan{}, err
		}
		if _, err = tx.ExecContext(finish, `UPDATE ai_agent_jobs SET state='waiting_confirmation',updated_at=? WHERE id=?`, s.now().UnixMilli(), job.ID); err != nil {
			return Plan{}, err
		}
		if err = tx.Commit(); err != nil {
			return Plan{}, err
		}
		return Plan{}, authz.ErrElevatedRequired
	}
	if _, err = tx.ExecContext(finish, `UPDATE ai_agent_plans SET state=?,result=? WHERE id=? AND state='approved' AND json_extract(result,'$.status')='executing'`, state, string(raw), plan.ID); err != nil {
		return Plan{}, err
	}
	if active {
		for _, id := range plan.ToolCallIDs {
			if _, err = appendMessage(finish, tx, job.ConversationID, job.Generation, "tool", string(raw), nil, id, s.now().UnixMilli()); err != nil {
				return Plan{}, err
			}
		}
		if _, err = tx.ExecContext(finish, `UPDATE ai_agent_jobs SET state='pending',updated_at=? WHERE id=? AND state='running'`, s.now().UnixMilli(), job.ID); err != nil {
			return Plan{}, err
		}
	}
	if err = tx.Commit(); err != nil {
		return Plan{}, err
	}
	plan.State, plan.Result, plan.DecidedAt = state, raw, &now
	if active {
		s.wake()
	}
	return plan, nil
}

func executeManagementSafely(ctx context.Context, manager ManagementOperations, actor Actor, op Operation, key string) (result json.RawMessage, err error) {
	defer func() {
		if recover() != nil {
			result = nil
			err = errors.New("management handler interrupted; verify actual state before retrying")
		}
	}()
	return manager.ExecuteManagement(ctx, actor, op, key)
}

// No executable work is reconstructed from an interrupted external operation.
func (s *Service) recoverManagement(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `UPDATE ai_agent_plans SET state='failed',result=json_set(result,'$.status','unknown','$.reason','服务在管理操作返回结果前中断，请查询实际状态；不会自动重试。','$.retry_automatically',json('false')) WHERE state='approved' AND json_extract(result,'$.status')='executing'`)
	return err
}
