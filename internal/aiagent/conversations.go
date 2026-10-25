package aiagent

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
)

func readMessages(ctx context.Context, tx *sql.Tx, conversation int64, active bool, before int64, limit int) ([]Message, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id,role,content,tool_calls,tool_call_id,created_at,active,generation,wire_json FROM ai_agent_messages WHERE conversation_id=? AND (?=0 OR active=1) AND (?=0 OR id<?) ORDER BY id DESC LIMIT ?`, conversation, active, before, before, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []Message{}
	for rows.Next() {
		var m Message
		var calls, wire string
		if err = rows.Scan(&m.ID, &m.Role, &m.Content, &calls, &m.ToolCallID, &m.CreatedAt, &m.Active, &m.Generation, &wire); err != nil {
			return nil, err
		}
		if calls != "[]" {
			m.ToolCalls = json.RawMessage(calls)
		}
		if wire != "{}" {
			m.Wire = json.RawMessage(wire)
		}
		result = append(result, m)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	for i, j := 0, len(result)-1; i < j; i, j = i+1, j-1 {
		result[i], result[j] = result[j], result[i]
	}
	return result, nil
}

func readPlans(ctx context.Context, tx *sql.Tx, job int64) ([]Plan, error) {
	rows, err := tx.QueryContext(ctx, `SELECT `+planColumns+` FROM ai_agent_plans p WHERE p.job_id=? ORDER BY p.id`, job)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Plan{}
	for rows.Next() {
		p, err := scanPlan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Service) Conversation(ctx context.Context, actor Actor) (Conversation, error) {
	tx, err := s.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return Conversation{}, err
	}
	defer tx.Rollback()
	if err = s.authorize(ctx, tx, actor, "ai.chat"); err != nil {
		return Conversation{}, err
	}
	out := Conversation{Messages: []Message{}, Plans: []Plan{}}
	out.Enabled, err = s.chatEnabled(ctx, tx)
	if err != nil {
		return out, err
	}
	err = tx.QueryRowContext(ctx, `SELECT id,generation,summary FROM ai_agent_conversations WHERE actor_id=?`, actor.ID).Scan(&out.ID, &out.Generation, &out.Summary)
	if errors.Is(err, sql.ErrNoRows) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	out.Messages, err = readMessages(ctx, tx, out.ID, true, 0, 101)
	if err != nil {
		return out, err
	}
	if len(out.Messages) > 100 {
		out.HasOlder = true
		out.Messages = out.Messages[1:]
	}
	j, err := scanJob(tx.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM ai_agent_jobs WHERE conversation_id=? ORDER BY id DESC LIMIT 1`, out.ID))
	if err != nil && !errors.Is(err, ErrNotFound) {
		return out, err
	}
	if err == nil {
		out.Job = &j
	}
	if len(out.Messages) > 0 {
		out.Plans, err = readConversationPlans(ctx, tx, out.ID, 0, out.Messages[0].ID, false, 100)
		if err != nil {
			return out, err
		}
	}
	// Release the SQL snapshot before asking the driver's independently
	// synchronized queue. The queue projection is advisory, never authority.
	if err = tx.Rollback(); err != nil {
		return out, err
	}
	if out.Job != nil {
		out.Job.Queue, _ = s.config.Driver.Queue(ctx, out.Job.RequestID)
	}
	return out, nil
}

func (s *Service) Messages(ctx context.Context, actor Actor, owner, before int64, archive bool) ([]Message, error) {
	tx, err := s.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	permission := "ai.chat"
	if archive || owner != actor.ID {
		permission = "ai.archives"
	}
	if err = s.authorize(ctx, tx, actor, permission); err != nil {
		return nil, err
	}
	var id int64
	if err = tx.QueryRowContext(ctx, `SELECT id FROM ai_agent_conversations WHERE actor_id=?`, owner).Scan(&id); errors.Is(err, sql.ErrNoRows) {
		return []Message{}, nil
	} else if err != nil {
		return nil, err
	}
	return readMessages(ctx, tx, id, !archive, before, 100)
}

func appendMessage(ctx context.Context, tx *sql.Tx, conversation, generation int64, role, content string, calls json.RawMessage, callID string, now int64) (int64, error) {
	if len(calls) == 0 {
		calls = json.RawMessage(`[]`)
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO ai_agent_messages(conversation_id,generation,role,content,tool_calls,tool_call_id,created_at) VALUES(?,?,?,?,?,?,?)`, conversation, generation, role, content, string(calls), callID, now)
	if err != nil {
		return 0, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE ai_agent_conversations SET updated_at=? WHERE id=?`, now, conversation); err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

func sendDigest(input SendInput) string {
	raw, _ := json.Marshal(struct {
		Content    string
		Generation int64
	}{input.Content, input.ExpectedGeneration})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func editDigest(messageID int64, input SendInput) string {
	raw, _ := json.Marshal(struct {
		MessageID int64
		Input     string
	}{messageID, sendDigest(input)})
	sum := sha256.Sum256(append([]byte("edit:"), raw...))
	return hex.EncodeToString(sum[:])
}

func replayChatJob(ctx context.Context, tx *sql.Tx, actorID int64, key, digest string) (Job, bool, error) {
	job, err := scanJob(tx.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM ai_agent_jobs WHERE actor_id=? AND client_key=?`, actorID, key))
	if errors.Is(err, ErrNotFound) {
		return Job{}, false, nil
	}
	if err != nil {
		return Job{}, false, err
	}
	var previous string
	if err = tx.QueryRowContext(ctx, `SELECT client_digest FROM ai_agent_jobs WHERE id=?`, job.ID).Scan(&previous); err != nil {
		return Job{}, false, err
	}
	if previous != digest {
		return Job{}, false, ErrConflict
	}
	return job, true, nil
}

// Finish tool calls abandoned by cancellation or a process interruption before
// accepting another user turn. The result records that no unfinished plan ran.
func closeInterruptedTools(ctx context.Context, tx *sql.Tx, conversation, generation, now int64) error {
	rows, err := tx.QueryContext(ctx, `SELECT m.tool_calls FROM ai_agent_messages m WHERE m.conversation_id=? AND m.active=1 AND m.role='assistant' ORDER BY m.id DESC LIMIT 1`, conversation)
	if err != nil {
		return err
	}
	var calls []toolCall
	for rows.Next() {
		var raw string
		if err = rows.Scan(&raw); err != nil {
			break
		}
		var values []toolCall
		if err = json.Unmarshal([]byte(raw), &values); err != nil {
			break
		}
		calls = append(calls, values...)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return err
	}
	for _, call := range calls {
		var found bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM ai_agent_messages WHERE conversation_id=? AND active=1 AND role='tool' AND tool_call_id=?)`, conversation, call.ID).Scan(&found); err != nil {
			return err
		}
		if !found {
			if _, err = appendMessage(ctx, tx, conversation, generation, "tool", `{"status":"interrupted","message":"该工具调用已中断，未完成的操作没有执行。"}`, nil, call.ID, now); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Service) Send(ctx context.Context, actor Actor, input SendInput) (Job, error) {
	if strings.TrimSpace(input.Content) == "" || len(input.Content) > 4<<20 || len(input.RequestKey) < 16 || len(input.RequestKey) > 128 {
		return Job{}, ErrInvalid
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Job{}, err
	}
	defer tx.Rollback()
	if err = s.authorize(ctx, tx, actor, "ai.chat"); err != nil {
		return Job{}, err
	}
	if old, found, e := replayChatJob(ctx, tx, actor.ID, input.RequestKey, sendDigest(input)); e != nil || found {
		return old, e
	}
	if e := s.requireChat(ctx, tx, actor); e != nil {
		return Job{}, e
	}
	now := s.now().UnixMilli()
	var id, generation int64
	err = tx.QueryRowContext(ctx, `SELECT id,generation FROM ai_agent_conversations WHERE actor_id=?`, actor.ID).Scan(&id, &generation)
	if errors.Is(err, sql.ErrNoRows) {
		if input.ExpectedGeneration != 0 {
			return Job{}, ErrConflict
		}
		result, e := tx.ExecContext(ctx, `INSERT INTO ai_agent_conversations(actor_id,actor_role,actor_label,created_at,updated_at) VALUES(?,?,?,?,?)`, actor.ID, actor.Role, actor.Label, now, now)
		if e != nil {
			return Job{}, e
		}
		id, err = result.LastInsertId()
		generation = 1
	} else if err == nil && generation != input.ExpectedGeneration {
		return Job{}, ErrConflict
	}
	if err != nil {
		return Job{}, err
	}
	var busy bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM ai_agent_jobs WHERE conversation_id=? AND state IN ('pending','running','waiting_confirmation'))`, id).Scan(&busy); err != nil {
		return Job{}, err
	}
	if busy {
		return Job{}, ErrConflict
	}
	if err = closeInterruptedTools(ctx, tx, id, generation, now); err != nil {
		return Job{}, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE ai_agent_conversations SET actor_role=?,actor_label=? WHERE id=?`, actor.Role, actor.Label, id); err != nil {
		return Job{}, err
	}
	kind := "chat"
	if _, command := compactInstructions(input.Content); command {
		kind = "compact"
	}
	if _, err = appendMessage(ctx, tx, id, generation, "user", input.Content, nil, "", now); err != nil {
		return Job{}, err
	}
	job, err := createChatJob(ctx, tx, actor, id, generation, kind, input.RequestKey, now)
	if err != nil {
		return Job{}, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE ai_agent_jobs SET client_digest=? WHERE id=?`, sendDigest(input), job.ID); err != nil {
		return Job{}, err
	}
	if err = tx.Commit(); err != nil {
		return Job{}, err
	}
	s.wake()
	return job, nil
}

func createChatJob(ctx context.Context, tx *sql.Tx, actor Actor, id, generation int64, kind, key string, now int64) (Job, error) {
	result, err := tx.ExecContext(ctx, `INSERT INTO ai_agent_jobs(kind,actor_id,actor_role,actor_label,session_hash,session_generation,client_key,conversation_id,generation,state,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,'pending',?,?)`, kind, actor.ID, actor.Role, actor.Label, actor.SessionHash, actor.SessionGeneration, key, id, generation, now, now)
	if err != nil {
		return Job{}, err
	}
	jobID, err := result.LastInsertId()
	if err != nil {
		return Job{}, err
	}
	return scanJob(tx.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM ai_agent_jobs WHERE id=?`, jobID))
}

// Edit archives the abandoned suffix. Existing execution receipts remain
// immutable and visible to administrators even when their prose is replaced.
func (s *Service) Edit(ctx context.Context, actor Actor, messageID int64, input SendInput) (Job, error) {
	if strings.TrimSpace(input.Content) == "" || len(input.Content) > 4<<20 || len(input.RequestKey) < 16 || len(input.RequestKey) > 128 {
		return Job{}, ErrInvalid
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Job{}, err
	}
	defer tx.Rollback()
	if err = s.authorize(ctx, tx, actor, "ai.chat"); err != nil {
		return Job{}, err
	}
	if old, found, e := replayChatJob(ctx, tx, actor.ID, input.RequestKey, editDigest(messageID, input)); e != nil || found {
		return old, e
	}
	if err = s.requireChat(ctx, tx, actor); err != nil {
		return Job{}, err
	}
	var conversation, generation, summaryThrough int64
	var role string
	err = tx.QueryRowContext(ctx, `SELECT c.id,c.generation,c.summary_through_id,m.role FROM ai_agent_conversations c JOIN ai_agent_messages m ON m.conversation_id=c.id WHERE c.actor_id=? AND m.id=? AND m.active=1`, actor.ID, messageID).Scan(&conversation, &generation, &summaryThrough, &role)
	if errors.Is(err, sql.ErrNoRows) {
		return Job{}, ErrNotFound
	}
	if err != nil {
		return Job{}, err
	}
	if generation != input.ExpectedGeneration {
		return Job{}, ErrConflict
	}
	if role != "user" && role != "assistant" {
		return Job{}, ErrForbidden
	}
	if actor.Role != "admin" {
		var latest int64
		if err = tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(id),0) FROM ai_agent_messages WHERE conversation_id=? AND active=1 AND role='user'`, conversation).Scan(&latest); err != nil {
			return Job{}, err
		}
		if latest != messageID || role != "user" {
			return Job{}, ErrForbidden
		}
	}
	now := s.now().UnixMilli()
	cancelled, err := supersedeConversation(ctx, tx, conversation, now)
	if err != nil {
		return Job{}, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE ai_agent_messages SET active=0 WHERE conversation_id=? AND active=1 AND id>=?`, conversation, messageID); err != nil {
		return Job{}, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE ai_agent_conversations SET generation=generation+1,summary=CASE WHEN summary_through_id>=? THEN '' ELSE summary END,summary_through_id=CASE WHEN summary_through_id>=? THEN 0 ELSE summary_through_id END,updated_at=? WHERE id=?`, messageID, messageID, now, conversation); err != nil {
		return Job{}, err
	}
	generation++
	if _, err = appendMessage(ctx, tx, conversation, generation, role, input.Content, nil, "", now); err != nil {
		return Job{}, err
	}
	kind := "chat"
	if _, command := compactInstructions(input.Content); role == "user" && command {
		kind = "compact"
	}
	job, err := createChatJob(ctx, tx, actor, conversation, generation, kind, input.RequestKey, now)
	if err != nil {
		return Job{}, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE ai_agent_jobs SET client_digest=? WHERE id=?`, editDigest(messageID, input), job.ID); err != nil {
		return Job{}, err
	}
	if err = tx.Commit(); err != nil {
		return Job{}, err
	}
	for _, id := range cancelled {
		s.cancelJob(id)
	}
	s.wake()
	return job, nil
}

func supersedeConversation(ctx context.Context, tx *sql.Tx, id, now int64) ([]int64, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id FROM ai_agent_jobs WHERE conversation_id=? AND state IN ('pending','running','waiting_confirmation')`, id)
	if err != nil {
		return nil, err
	}
	ids := []int64{}
	for rows.Next() {
		var n int64
		if err = rows.Scan(&n); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, n)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE ai_agent_plans SET state='superseded',decided_at=? WHERE state='pending' AND job_id IN (SELECT id FROM ai_agent_jobs WHERE conversation_id=?)`, now, id); err != nil {
		return nil, err
	}
	_, err = tx.ExecContext(ctx, `UPDATE ai_agent_jobs SET state='cancelled',error='对话上下文已更新',updated_at=? WHERE conversation_id=? AND state IN ('pending','running','waiting_confirmation')`, now, id)
	return ids, err
}

func (s *Service) Clear(ctx context.Context, actor Actor, expected int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = s.authorize(ctx, tx, actor, "ai.chat"); err != nil {
		return err
	}
	var id, generation int64
	err = tx.QueryRowContext(ctx, `SELECT id,generation FROM ai_agent_conversations WHERE actor_id=?`, actor.ID).Scan(&id, &generation)
	if errors.Is(err, sql.ErrNoRows) && expected == 0 {
		return nil
	}
	if err != nil {
		return err
	}
	if generation != expected {
		return ErrConflict
	}
	now := s.now().UnixMilli()
	ids, err := supersedeConversation(ctx, tx, id, now)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE ai_agent_messages SET active=0 WHERE conversation_id=? AND active=1`, id); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE ai_agent_conversations SET generation=generation+1,summary='',summary_through_id=0,updated_at=? WHERE id=?`, now, id); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	for _, id := range ids {
		s.cancelJob(id)
	}
	return nil
}

func (s *Service) Cancel(ctx context.Context, actor Actor, id int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = s.authorize(ctx, tx, actor, "ai.chat"); err != nil {
		return err
	}
	j, err := scanJob(tx.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM ai_agent_jobs WHERE id=?`, id))
	if err != nil {
		return err
	}
	if j.ActorID != actor.ID || j.Kind == "auto_review" {
		return ErrForbidden
	}
	now := s.now().UnixMilli()
	if _, err = tx.ExecContext(ctx, `UPDATE ai_agent_jobs SET state='cancelled',error='请求已取消',updated_at=? WHERE id=? AND state IN ('pending','running','waiting_confirmation')`, now, id); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE ai_agent_plans SET state='superseded',decided_at=? WHERE job_id=? AND state='pending'`, now, id); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	s.cancelJob(id)
	return nil
}
