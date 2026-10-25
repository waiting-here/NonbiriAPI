package aiagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/waiting-here/NonbiriAPI/internal/agentplan"
	"github.com/waiting-here/NonbiriAPI/internal/agentrequest"
)

var errToolContext = errors.New("模型窗口没有足够空间接收工具回执，请增加上下文窗口或减少输出上限、模板及工具配置")

func readToolError(name string, err error) json.RawMessage {
	if agentplan.IsReadLimit(err) {
		return toolResultTooLarge(name, "read_limit")
	}
	return toolError(err)
}

func toolResultTooLarge(name, reason string) json.RawMessage {
	filters := []string{}
	retry := "请调整模型上下文窗口、输出上限或工具配置后重试。"
	switch name {
	case "donations":
		filters = []string{"donation_id", "status", "owner_id"}
		retry = "请指定 donation_id、status，或在有身份权限时指定 owner_id，再次查询全部匹配记录。"
	case "donation_models":
		filters = []string{"donation_id", "donation_key_id", "source", "query"}
		retry = "请用 donation_id、donation_key_id、source 或型号/名称子串 query 缩小范围后重试。"
	case "review_history":
		filters = []string{"donation_id", "decision"}
		retry = "请指定 donation_id 或 decision 缩小审核记录范围后重试。"
	case "charity_models":
		filters = []string{"model_id", "query"}
		retry = "请指定 model_id 或名称子串 query 缩小范围后重试。"
	case "users":
		filters = []string{"user_id", "query"}
		retry = "请指定站内 user_id 读取一个账号详情，或用用户名/Discord ID query 缩小列表范围后重试。"
	case "web_search":
		filters = []string{"query"}
		retry = "请使用更具体的 query 缩小搜索范围后重试。"
	case "edit_memory":
		retry = "记忆读取没有范围筛选，请缩短保存的记忆正文或调整模型窗口、输出上限后重试。"
	}
	limits := map[string]int{"max_bytes": agentplan.ReadMaxBytes}
	switch name {
	case "donations", "donation_models", "review_history", "charity_models", "users":
		limits["max_items"] = agentplan.ReadMaxItems
	}
	raw, _ := json.Marshal(map[string]any{"error": map[string]any{
		"code": "tool_result_too_large", "message": "本次结果超过读取或当前上下文预算，未返回部分数据。",
		"tool": name, "reason": reason, "retryable": true, "filters": filters, "retry": retry,
		"limits": limits,
	}})
	return raw
}

func toolMessage(call toolCall, value json.RawMessage) json.RawMessage {
	raw, _ := json.Marshal(map[string]string{"role": "tool", "tool_call_id": call.ID, "content": string(value)})
	return raw
}

func modelInputBudget(settings agentrequest.Settings) int {
	budget := settings.ContextWindow - settings.MaxOutputTokens
	return budget - max(128, budget/10)
}

func modelInputSize(messages []json.RawMessage, tools, format []byte, settings agentrequest.Settings) (int, int) {
	raw, _ := json.Marshal(messages)
	// Leave room for the queued attribution and request fields, including the
	// bounded extra parameters. Message JSON includes tool-string escaping.
	return estimatedTokens(raw) + estimatedTokens(tools) + estimatedTokens(format), len(raw) + len(tools) + len(format) + len(settings.ExtraParameters) + (64 << 10)
}

func modelInputFits(messages []json.RawMessage, tools, format []byte, settings agentrequest.Settings) bool {
	tokens, size := modelInputSize(messages, tools, format, settings)
	return tokens <= modelInputBudget(settings) && size <= agentrequest.MaxBodyBytes
}

type toolRoundBudget struct {
	tokens, bytes, historyBytes int
	limit                       int
	reservedTokens              int
	reservedBytes               int
	reservedHistory             int
	reserved                    map[string]toolResultCost
}

type toolResultCost struct{ tokens, bytes, history int }

func toolTokenCost(message []byte) int {
	// The complete messages array already paid the estimator's fixed overhead.
	// Each appended message adds its text and at most one token for a comma.
	return estimatedTokens(message) - estimatedTokens(nil) + 1
}

func historyBytes(history []Message) int {
	total := 0
	for _, m := range history {
		wire := len(m.Wire)
		if wire == 0 {
			wire = 2 // Stored default wire_json is {}.
		}
		total += len(m.Role) + len(m.Content) + len(m.ToolCalls) + len(m.ToolCallID) + wire + 128
	}
	return total
}

func newToolRoundBudget(messages []json.RawMessage, history []Message, tools, format []byte, settings agentrequest.Settings, calls []toolCall) *toolRoundBudget {
	tokens, size := modelInputSize(messages, tools, format, settings)
	b := &toolRoundBudget{tokens: tokens, bytes: size, historyBytes: historyBytes(history), limit: modelInputBudget(settings), reserved: map[string]toolResultCost{}}
	for _, call := range calls {
		b.reserve(call, toolResultTooLarge(call.Function.Name, "context_budget"))
	}
	return b
}

func (b *toolRoundBudget) reserve(call toolCall, value json.RawMessage) {
	message := toolMessage(call, value)
	cost := toolResultCost{toolTokenCost(message), len(message) + 1, toolHistoryBytes(call, value)}
	b.reserved[call.ID] = cost
	b.reservedTokens += cost.tokens
	b.reservedBytes += cost.bytes
	b.reservedHistory += cost.history
}

func (b *toolRoundBudget) reservePlanFailures(calls []toolCall, cause error) {
	// HTML escaping has the largest wire expansion among the cleaned, bounded
	// error text. Reserve that cost once; later calls reference the first one.
	if cause == nil {
		cause = errors.New(strings.Repeat("<", 256))
	}
	values := planFailureReceipts(calls, cause)
	for _, call := range calls {
		b.reserve(call, values[call.ID])
	}
}

func planFailureReceipts(calls []toolCall, err error) map[string]json.RawMessage {
	values := map[string]json.RawMessage{}
	if len(calls) == 0 {
		return values
	}
	reason := strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, strings.ToValidUTF8(err.Error(), "�"))
	if len(reason) > 256 {
		cut := 253
		for cut > 0 && !utf8.RuneStart(reason[cut]) {
			cut--
		}
		reason = reason[:cut] + "…"
	}
	values[calls[0].ID] = toolError(errors.New("计划未通过校验，未生成待确认计划：" + reason))
	for _, call := range calls[1:] {
		raw, _ := json.Marshal(map[string]string{"error": "plan_failed", "details_tool_call_id": calls[0].ID})
		values[call.ID] = raw
	}
	return values
}

func toolHistoryBytes(call toolCall, value json.RawMessage) int {
	return len("tool") + len(value) + len(call.ID) + 4 + 128
}

func (b *toolRoundBudget) fits(tokens, size, history int) bool {
	return b.tokens+b.reservedTokens+tokens <= b.limit && b.bytes+b.reservedBytes+size <= agentrequest.MaxBodyBytes && b.historyBytes+b.reservedHistory+history <= agentrequest.MaxBodyBytes
}

func (b *toolRoundBudget) accept(call toolCall, value json.RawMessage) json.RawMessage {
	failure := toolResultTooLarge(call.Function.Name, "context_budget")
	failureMessage := toolMessage(call, failure)
	cost := b.reserved[call.ID]
	delete(b.reserved, call.ID)
	b.reservedTokens -= cost.tokens
	b.reservedBytes -= cost.bytes
	b.reservedHistory -= cost.history
	if len(value) > agentplan.ReadMaxBytes {
		value = toolResultTooLarge(call.Function.Name, "read_limit")
	}
	message := toolMessage(call, value)
	if !b.fits(toolTokenCost(message), len(message)+1, toolHistoryBytes(call, value)) {
		value, message = failure, failureMessage
	}
	b.tokens += toolTokenCost(message)
	b.bytes += len(message) + 1
	b.historyBytes += toolHistoryBytes(call, value)
	return value
}

func (s *Service) prepareToolRound(ctx context.Context, job Job, actor Actor, settings Settings, messages []json.RawMessage, tools, format []byte, calls, writes []toolCall, planError error) ([]json.RawMessage, *toolRoundBudget, error) {
	driver, err := s.config.Driver.Settings(ctx)
	if err != nil {
		return nil, nil, err
	}
	var history []Message
	if job.Kind != "auto_review" {
		messages, history, _, _, err = s.contextMessages(ctx, job, settings)
		if err != nil {
			return nil, nil, err
		}
	}
	b := newToolRoundBudget(messages, history, tools, format, driver, calls)
	b.reservePlanFailures(writes, planError)
	if !b.fits(0, 0, 0) && job.Kind != "auto_review" {
		// Only older context is summarized: the newest assistant tool call is
		// retained until this loop has persisted every corresponding result.
		if _, err = s.compactIfNeeded(ctx, job, actor, settings, tools, true); err != nil {
			return nil, nil, err
		}
		messages, history, _, _, err = s.contextMessages(ctx, job, settings)
		if err != nil {
			return nil, nil, err
		}
		b = newToolRoundBudget(messages, history, tools, format, driver, calls)
		b.reservePlanFailures(writes, planError)
	}
	if !b.fits(0, 0, 0) {
		return nil, nil, fmt.Errorf("%w；原始记录仍保留", errToolContext)
	}
	return messages, b, nil
}

func manualSizeReview(reason string) ReviewResult {
	return ReviewResult{Decision: "manual", InternalReason: reason, UserNote: "本次内容较多，需要人工审核。", Bindings: []ReviewBinding{}}
}
