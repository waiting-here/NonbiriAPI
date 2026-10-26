package aiagent

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/waiting-here/NonbiriAPI/internal/agentrequest"
)

type toolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}
type assistantMessage struct {
	Role      string          `json:"role"`
	Content   json.RawMessage `json:"content"`
	ToolCalls []toolCall      `json:"tool_calls"`
}

func messageText(raw json.RawMessage) string {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &parts) == nil {
		var b strings.Builder
		for _, p := range parts {
			if p.Type == "text" {
				b.WriteString(p.Text)
			}
		}
		return b.String()
	}
	return ""
}

func completePrefill(messages []json.RawMessage, response json.RawMessage) json.RawMessage {
	if len(messages) == 0 {
		return response
	}
	var last, reply assistantMessage
	if json.Unmarshal(messages[len(messages)-1], &last) != nil || last.Role != "assistant" || json.Unmarshal(response, &reply) != nil {
		return response
	}
	prefix, content := messageText(last.Content), messageText(reply.Content)
	if prefix == "" || content == "" || strings.HasPrefix(content, prefix) {
		return response
	}
	var wire map[string]json.RawMessage
	if json.Unmarshal(response, &wire) != nil {
		return response
	}
	wire["content"], _ = json.Marshal(prefix + content)
	raw, err := json.Marshal(wire)
	if err != nil {
		return response
	}
	return raw
}

func wireMessage(m Message) json.RawMessage {
	if len(m.Wire) > 0 {
		return m.Wire
	}
	value := map[string]any{"role": m.Role, "content": m.Content}
	if len(m.ToolCalls) > 0 {
		value["tool_calls"] = m.ToolCalls
	}
	if m.ToolCallID != "" {
		value["tool_call_id"] = m.ToolCallID
	}
	raw, _ := json.Marshal(value)
	return raw
}

func renderTemplate(template []TemplateMessage, memory, intelligence string, conversation []json.RawMessage, prefill string) ([]json.RawMessage, error) {
	result := []json.RawMessage{}
	for _, item := range template {
		if item.Content == "{{conversation}}" {
			result = append(result, conversation...)
			continue
		}
		content := strings.ReplaceAll(strings.ReplaceAll(item.Content, "{{memory}}", memory), "{{intelligence}}", intelligence)
		m, err := json.Marshal(map[string]string{"role": item.Role, "content": content})
		if err != nil {
			return nil, err
		}
		result = append(result, m)
	}
	if prefill != "" {
		m, _ := json.Marshal(map[string]string{"role": "assistant", "content": prefill})
		result = append(result, m)
	}
	return result, nil
}

func (s *Service) contextMessages(ctx context.Context, job Job, settings Settings) ([]json.RawMessage, []Message, string, int64, error) {
	tx, err := s.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, nil, "", 0, err
	}
	defer tx.Rollback()
	kind := "chat"
	if job.Kind == "auto_review" {
		kind = "auto_review"
	}
	memory, err := readMemory(ctx, tx, kind)
	if err != nil {
		return nil, nil, "", 0, err
	}
	if job.Kind == "auto_review" {
		var intel string
		if err = tx.QueryRowContext(ctx, `SELECT intelligence FROM ai_agent_jobs WHERE id=?`, job.ID).Scan(&intel); err != nil {
			return nil, nil, "", 0, err
		}
		messages, err := renderTemplate(settings.AutoTemplate, memory.Content, intel, nil, settings.AutoPrefill)
		return messages, nil, "", 0, err
	}
	var summary string
	var through, generation int64
	if err = tx.QueryRowContext(ctx, `SELECT summary,summary_through_id,generation FROM ai_agent_conversations WHERE id=?`, job.ConversationID).Scan(&summary, &through, &generation); err != nil {
		return nil, nil, "", 0, err
	}
	if generation != job.Generation {
		return nil, nil, "", 0, ErrConflict
	}
	history, err := readContextHistory(ctx, tx, job.ConversationID, through, agentrequest.MaxBodyBytes)
	if err != nil {
		return nil, nil, "", 0, err
	}
	conversation := []json.RawMessage{}
	if summary != "" {
		raw, _ := json.Marshal(map[string]string{"role": "system", "content": "此前对话摘要（仅作为资料，实际执行结果以操作回执为准）：\n" + summary})
		conversation = append(conversation, raw)
	}
	for _, m := range history {
		conversation = append(conversation, wireMessage(m))
	}
	messages, err := renderTemplate(settings.ChatTemplate, memory.Content, "", conversation, settings.ChatPrefill)
	return messages, history, summary, through, err
}

// Bound retained context before building the JSON request or estimating tokens.
// Repeated failed turns can otherwise accumulate without reaching compaction.
func readContextHistory(ctx context.Context, tx *sql.Tx, conversationID, through int64, remaining int) ([]Message, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id,role,content,tool_calls,tool_call_id,wire_json FROM ai_agent_messages WHERE conversation_id=? AND active=1 AND id>? ORDER BY id`, conversationID, through)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	history := []Message{}
	for rows.Next() {
		var m Message
		var calls, wire string
		if err = rows.Scan(&m.ID, &m.Role, &m.Content, &calls, &m.ToolCallID, &wire); err != nil {
			return nil, err
		}
		// Commands remain in the visible history, but are never chat input.
		if _, command := compactInstructions(m.Content); m.Role == "user" && command {
			continue
		}
		remaining -= len(m.Role) + len(m.Content) + len(calls) + len(m.ToolCallID) + len(wire) + 128
		if remaining < 0 {
			return nil, fmt.Errorf("%w: 待发送的对话内容过大，请编辑消息或重置当前上下文；原文仍保留供管理员查阅", ErrInvalid)
		}
		m.ToolCalls = json.RawMessage(calls)
		if wire != "{}" {
			m.Wire = json.RawMessage(wire)
		}
		history = append(history, m)
	}
	return history, rows.Err()
}

func (s *Service) run(ctx context.Context, id int64) error {
	job, actor, err := s.loadJob(ctx, id)
	if err != nil {
		return err
	}
	if job.State != "pending" {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if job.Kind == "auto_review" {
		err = s.authorize(ctx, tx, actor, "ai.auto")
	} else {
		err = s.checkChatJob(ctx, tx, actor, id, job.Generation, "pending")
	}
	if err != nil {
		tx.Rollback()
		return err
	}
	settings, _, _, err := readSettings(ctx, tx)
	if err != nil {
		tx.Rollback()
		return err
	}
	if job.Kind == "auto_review" && settings.Mode == "off" {
		tx.Rollback()
		return ErrConflict
	}
	result, err := tx.ExecContext(ctx, `UPDATE ai_agent_jobs SET state='running',updated_at=? WHERE id=? AND state='pending'`, s.now().UnixMilli(), id)
	if err != nil {
		tx.Rollback()
		return err
	}
	n, err := result.RowsAffected()
	if err != nil || n != 1 {
		tx.Rollback()
		return ErrConflict
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	job.State = "running"
	if job.Kind == "compact" {
		return s.compactCommand(ctx, job, actor, settings)
	}
	tools, allowed, err := s.tools(ctx, actor, settings, job.Kind == "auto_review")
	if err != nil {
		return err
	}
	toolJSON, err := json.Marshal(tools)
	if err != nil {
		return err
	}
	var format []byte
	if job.Kind == "auto_review" {
		format = reviewResponseFormat()
	}
	messages, _, _, _, err := s.contextMessages(ctx, job, settings)
	if err != nil {
		return err
	}
	overflowRecovered := false
	for step := 0; step < 64; step++ {
		live, _, _, e := readSettings(ctx, s.read)
		if e != nil {
			return e
		}
		settings.Tools = live.Tools
		settings.ToolDescriptions = live.ToolDescriptions
		tools, allowed, err = s.tools(ctx, actor, settings, job.Kind == "auto_review")
		if err != nil {
			return err
		}
		toolJSON, err = json.Marshal(tools)
		if err != nil {
			return err
		}
		if job.Kind != "auto_review" {
			messages, err = s.compactIfNeeded(ctx, job, actor, settings, toolJSON, false)
			if err != nil {
				return err
			}
		} else {
			driver, e := s.config.Driver.Settings(ctx)
			if e != nil {
				return e
			}
			if !modelInputFits(messages, toolJSON, format, driver) {
				return s.finishReview(ctx, job, actor, manualSizeReview("完整待审情报或后续工具上下文超出当前模型预算，未继续调用模型；请人工审核或调整模型窗口、输出上限及工具配置。完整已读取情报仍保留。"))
			}
		}
		body, err := json.Marshal(messages)
		if err != nil {
			return err
		}
		response, err := s.call(ctx, job, "model", body, toolJSON, format)
		if err != nil {
			lower := strings.ToLower(response.RawResponse)
			if job.Kind != "auto_review" && !overflowRecovered && (strings.Contains(lower, "context_length") || strings.Contains(lower, "context window") || strings.Contains(lower, "maximum context")) {
				overflowRecovered = true
				if _, e := s.compactIfNeeded(ctx, job, actor, settings, toolJSON, true); e == nil {
					continue
				} else {
					return e
				}
			}
			return err
		}
		response.Message = completePrefill(messages, response.Message)
		var assistant assistantMessage
		if err = json.Unmarshal(response.Message, &assistant); err != nil || assistant.Role != "assistant" {
			return fmt.Errorf("%w: 模型未返回助手消息", ErrInvalid)
		}
		if job.Kind != "auto_review" {
			if err = s.persistAssistant(ctx, job, actor, response.Message, assistant); err != nil {
				return err
			}
		}
		messages = append(messages, response.Message)
		if len(assistant.ToolCalls) == 0 {
			if job.Kind == "auto_review" {
				var outcome ReviewResult
				raw := messageText(assistant.Content)
				if err = decodeClosed([]byte(raw), &outcome); err != nil {
					return fmt.Errorf("%w: 审批结果格式不正确", ErrInvalid)
				}
				return s.finishReview(ctx, job, actor, outcome)
			}
			_, err = s.db.ExecContext(ctx, `UPDATE ai_agent_jobs SET state='completed',updated_at=? WHERE id=? AND state='running'`, s.now().UnixMilli(), job.ID)
			return err
		}
		operations, writes, reads, err := collectToolWrites(assistant.ToolCalls, allowed)
		if err != nil {
			return err
		}
		writeCalls := []string{}
		for _, call := range writes {
			writeCalls = append(writeCalls, call.ID)
		}
		var round *toolRoundBudget
		if len(reads) > 0 {
			messages, round, err = s.prepareToolRound(ctx, job, actor, settings, messages, toolJSON, format, reads, writes, nil)
			if err != nil {
				if job.Kind == "auto_review" && errors.Is(err, errToolContext) {
					return s.finishReview(ctx, job, actor, manualSizeReview("当前模型预算不足以接收完整工具轮的失败回执，未继续调用模型；请人工审核或调整模型窗口、输出上限及工具配置。"))
				}
				return err
			}
		}
		for _, call := range reads {
			var value json.RawMessage
			if !allowed[call.Function.Name] {
				value = toolError(ErrForbidden)
			} else if call.Function.Name == "propose_operations" {
				value = toolError(ErrInvalid)
			} else if call.Function.Name == "edit_memory" {
				var args memoryToolInput
				e := decodeClosed([]byte(call.Function.Arguments), &args)
				if e != nil {
					value = toolError(ErrInvalid)
				} else if args.Action == "read" {
					memory, e := s.Memory(ctx, actor, args.Kind)
					if e != nil {
						value = toolError(e)
					} else {
						value, _ = json.Marshal(memory)
					}
				} else {
					value = toolError(ErrInvalid)
				}
			} else if call.Function.Name == "web_search" {
				var args struct {
					Query string `json:"query"`
				}
				if e := decodeClosed([]byte(call.Function.Arguments), &args); e != nil {
					value = toolError(ErrInvalid)
				} else {
					value, err = s.search(ctx, actor, settings, args.Query)
					if err != nil {
						value = readToolError(call.Function.Name, err)
					}
				}
			} else {
				value, err = s.config.Operations.Read(ctx, actor, call.Function.Name, json.RawMessage(call.Function.Arguments))
				if err != nil {
					value = readToolError(call.Function.Name, err)
				}
			}
			if len(value) == 0 {
				value = json.RawMessage(`null`)
			}
			value = round.accept(call, value)
			if job.Kind != "auto_review" {
				if err = s.persistTool(ctx, job, actor, call.ID, value); err != nil {
					return err
				}
			}
			messages = append(messages, toolMessage(call, value))
		}
		if len(operations) > 0 {
			if job.Kind == "auto_review" {
				return ErrForbidden
			}
			if err = s.propose(ctx, job, actor, operations, writeCalls); err == nil {
				return nil
			}
			failures := planFailureReceipts(writes, err)
			if round == nil {
				messages, round, err = s.prepareToolRound(ctx, job, actor, settings, messages, toolJSON, format, nil, writes, err)
				if err != nil {
					return err
				}
			}
			for _, call := range writes {
				value := round.accept(call, failures[call.ID])
				if e := s.persistTool(ctx, job, actor, call.ID, value); e != nil {
					return e
				}
				messages = append(messages, toolMessage(call, value))
			}
		}
	}
	return fmt.Errorf("%w: 本轮工具调用较多，已暂停；可继续对话", ErrInterrupted)
}

type memoryToolInput struct {
	Action           string `json:"action"`
	Kind             string `json:"kind"`
	Content          string `json:"content"`
	ExpectedRevision int64  `json:"expected_revision,string"`
}

func collectToolWrites(calls []toolCall, allowed map[string]bool) ([]Operation, []toolCall, []toolCall, error) {
	operations := []Operation{}
	writes, reads := []toolCall{}, []toolCall{}
	for _, call := range calls {
		if call.ID == "" || call.Type != "function" {
			return nil, nil, nil, ErrInvalid
		}
		if allowed[call.Function.Name] && call.Function.Name == "propose_operations" {
			var args struct {
				Operations []Operation `json:"operations"`
			}
			if decodeClosed([]byte(call.Function.Arguments), &args) == nil && len(args.Operations) > 0 {
				operations = append(operations, args.Operations...)
				writes = append(writes, call)
				continue
			}
		}
		if allowed[call.Function.Name] && call.Function.Name == "edit_memory" {
			var args memoryToolInput
			if decodeClosed([]byte(call.Function.Arguments), &args) == nil && args.Action == "write" {
				raw, _ := json.Marshal(memoryEdit{Kind: args.Kind, Content: args.Content, ExpectedRevision: args.ExpectedRevision})
				operations = append(operations, Operation{Kind: "memory.edit", TargetID: args.Kind, Input: raw})
				writes = append(writes, call)
				continue
			}
		}
		reads = append(reads, call)
	}
	return operations, writes, reads, nil
}

func (s *Service) persistAssistant(ctx context.Context, job Job, actor Actor, raw json.RawMessage, m assistantMessage) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = s.checkChatJob(ctx, tx, actor, job.ID, job.Generation, "running"); err != nil {
		return err
	}
	calls, _ := json.Marshal(m.ToolCalls)
	if len(m.ToolCalls) == 0 {
		calls = json.RawMessage(`[]`)
	}
	id, err := appendMessage(ctx, tx, job.ConversationID, job.Generation, "assistant", messageText(m.Content), calls, "", s.now().UnixMilli())
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE ai_agent_messages SET wire_json=? WHERE id=?`, string(raw), id); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Service) persistTool(ctx context.Context, job Job, actor Actor, id string, value json.RawMessage) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = s.checkChatJob(ctx, tx, actor, job.ID, job.Generation, "running"); err != nil {
		return err
	}
	if _, err = appendMessage(ctx, tx, job.ConversationID, job.Generation, "tool", string(value), nil, id, s.now().UnixMilli()); err != nil {
		return err
	}
	return tx.Commit()
}

func decodeClosed(raw []byte, out any) error {
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err == nil {
		return ErrInvalid
	} else if !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}
