package agentrequest

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/waiting-here/NonbiriAPI/internal/egress"
)

func responsesParameters(raw json.RawMessage, subscription bool) (map[string]json.RawMessage, error) {
	if len(raw) == 0 {
		raw = json.RawMessage("{}")
	}
	if len(raw) > 32768 {
		return nil, ErrInvalid
	}
	var values map[string]json.RawMessage
	if json.Unmarshal(raw, &values) != nil || values == nil {
		return nil, ErrInvalid
	}
	for key, value := range values {
		switch key {
		case "reasoning":
			var object map[string]string
			if json.Unmarshal(value, &object) != nil || object == nil {
				return nil, ErrInvalid
			}
			for name, v := range object {
				allowed := false
				switch name {
				case "effort":
					allowed = oneOf(v, "none", "minimal", "low", "medium", "high", "xhigh", "max")
				case "summary":
					allowed = oneOf(v, "auto", "concise", "detailed")
				case "mode":
					allowed = oneOf(v, "standard", "pro")
				case "context":
					allowed = oneOf(v, "auto", "all_turns", "current_turn")
				}
				if !allowed {
					return nil, ErrInvalid
				}
			}
		case "text":
			var object map[string]string
			if json.Unmarshal(value, &object) != nil || object == nil {
				return nil, ErrInvalid
			}
			for k, v := range object {
				if k != "verbosity" || !oneOf(v, "low", "medium", "high") {
					return nil, ErrInvalid
				}
			}
		case "parallel_tool_calls":
			var v bool
			if json.Unmarshal(value, &v) != nil {
				return nil, ErrInvalid
			}
		case "service_tier":
			var v string
			if json.Unmarshal(value, &v) != nil || !oneOf(v, "auto", "default", "flex", "priority", "fast", "ultrafast") {
				return nil, ErrInvalid
			}
		case "prompt_cache_key":
			var v string
			if json.Unmarshal(value, &v) != nil || len(v) > 64 {
				return nil, ErrInvalid
			}
		case "prompt_cache_retention":
			var v string
			if subscription || json.Unmarshal(value, &v) != nil || !oneOf(v, "in-memory", "24h") {
				return nil, ErrInvalid
			}
		default:
			return nil, ErrInvalid
		}
	}
	return values, nil
}
func oneOf(v string, values ...string) bool {
	for _, s := range values {
		if s == v {
			return true
		}
	}
	return false
}
func nativeIdentity(s Settings) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%s\x00%d\x00%s\x00%d", s.Provider, s.BaseURL, s.DriverID, s.Model, s.ConnectionRevision)))
	return hex.EncodeToString(sum[:])
}
func chatMessages(raw json.RawMessage) (json.RawMessage, error) {
	var messages []map[string]json.RawMessage
	if json.Unmarshal(raw, &messages) != nil {
		return nil, ErrInvalid
	}
	for _, m := range messages {
		delete(m, "responses_output")
		delete(m, "responses_provider")
	}
	return json.Marshal(messages)
}
func responseInput(raw json.RawMessage, s Settings) ([]json.RawMessage, error) {
	var messages []map[string]json.RawMessage
	if json.Unmarshal(raw, &messages) != nil {
		return nil, ErrInvalid
	}
	items := []json.RawMessage{}
	appendValue := func(v any) { encoded, _ := json.Marshal(v); items = append(items, encoded) }
	for _, message := range messages {
		var role string
		_ = json.Unmarshal(message["role"], &role)
		var provider string
		_ = json.Unmarshal(message["responses_provider"], &provider)
		if role == "assistant" && provider == nativeIdentity(s) && len(message["responses_output"]) > 0 {
			var output []json.RawMessage
			if json.Unmarshal(message["responses_output"], &output) != nil || output == nil {
				return nil, ErrInvalid
			}
			items = append(items, output...)
			continue
		}
		if role == "tool" {
			var id string
			if json.Unmarshal(message["tool_call_id"], &id) != nil || id == "" {
				return nil, ErrInvalid
			}
			var output string
			if json.Unmarshal(message["content"], &output) != nil {
				return nil, ErrInvalid
			}
			appendValue(map[string]any{"type": "function_call_output", "call_id": id, "output": output})
			continue
		}
		if role == "system" {
			role = "developer"
		}
		if !oneOf(role, "developer", "user", "assistant") {
			return nil, ErrInvalid
		}
		content := []any{}
		kind := "input_text"
		if role == "assistant" {
			kind = "output_text"
		}
		var text string
		if json.Unmarshal(message["content"], &text) == nil {
			if text != "" {
				content = append(content, map[string]string{"type": kind, "text": text})
			}
		} else if len(message["content"]) > 0 && !bytes.Equal(message["content"], []byte("null")) {
			var parts []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			}
			if json.Unmarshal(message["content"], &parts) != nil {
				return nil, ErrInvalid
			}
			for _, part := range parts {
				if part.Type != "text" {
					return nil, ErrInvalid
				}
				content = append(content, map[string]string{"type": kind, "text": part.Text})
			}
		}
		if len(content) > 0 {
			appendValue(map[string]any{"type": "message", "role": role, "content": content})
		}
		if role == "assistant" && len(message["tool_calls"]) > 0 {
			var calls []struct {
				ID       string `json:"id"`
				Type     string `json:"type"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			}
			if json.Unmarshal(message["tool_calls"], &calls) != nil {
				return nil, ErrInvalid
			}
			for _, call := range calls {
				if call.ID == "" || call.Type != "function" || call.Function.Name == "" || !json.Valid([]byte(call.Function.Arguments)) {
					return nil, ErrInvalid
				}
				item := map[string]any{"type": "function_call", "call_id": call.ID, "name": call.Function.Name, "arguments": call.Function.Arguments}
				if s.Provider == ProviderChatGPT {
					item["namespace"] = "nonbiri"
				}
				appendValue(item)
			}
		}
	}
	return items, nil
}
func responsesBody(in Input, s Settings) ([]byte, error) {
	body, err := responsesParameters(s.ExtraParameters, s.Provider == ProviderChatGPT)
	if err != nil {
		return nil, err
	}
	put := func(k string, v any) { body[k], _ = json.Marshal(v) }
	input, err := responseInput(in.Messages, s)
	if err != nil {
		return nil, err
	}
	put("model", s.Model)
	put("input", input)
	put("stream", true)
	put("store", false)
	// Encrypted output keeps stateless reasoning replay usable on Responses
	// providers that do not include it by default.
	put("include", []string{"reasoning.encrypted_content"})
	if s.Provider != ProviderChatGPT {
		if s.MaxOutputTokens > 0 {
			put("max_output_tokens", s.MaxOutputTokens)
		}
		if s.Temperature != nil {
			put("temperature", *s.Temperature)
		}
		if s.TopP != nil {
			put("top_p", *s.TopP)
		}
	}
	if len(in.Tools) > 0 {
		var tools []struct {
			Type     string                     `json:"type"`
			Function map[string]json.RawMessage `json:"function"`
		}
		if json.Unmarshal(in.Tools, &tools) != nil || len(tools) > 128 {
			return nil, ErrInvalid
		}
		mapped := []any{}
		for _, tool := range tools {
			if tool.Type != "function" || tool.Function == nil {
				return nil, ErrInvalid
			}
			value := map[string]json.RawMessage{"type": json.RawMessage(`"function"`), "strict": json.RawMessage("false")}
			for k, v := range tool.Function {
				if !oneOf(k, "name", "description", "parameters", "strict") {
					return nil, ErrInvalid
				}
				value[k] = v
			}
			mapped = append(mapped, value)
		}
		if len(mapped) > 0 {
			if s.Provider == ProviderChatGPT {
				put("tools", []any{map[string]any{"type": "namespace", "name": "nonbiri", "description": "Operations available to the current site operator.", "tools": mapped}})
			} else {
				put("tools", mapped)
			}
		}
	}
	if len(in.ToolChoice) > 0 {
		var choice string
		if json.Unmarshal(in.ToolChoice, &choice) == nil {
			if !oneOf(choice, "auto", "none", "required") {
				return nil, ErrInvalid
			}
			put("tool_choice", choice)
		} else {
			var choice struct {
				Type     string `json:"type"`
				Function struct {
					Name string `json:"name"`
				} `json:"function"`
			}
			if json.Unmarshal(in.ToolChoice, &choice) != nil || choice.Type != "function" || choice.Function.Name == "" {
				return nil, ErrInvalid
			}
			v := map[string]string{"type": "function", "name": choice.Function.Name}
			if s.Provider == ProviderChatGPT {
				v["namespace"] = "nonbiri"
			}
			put("tool_choice", v)
		}
	}
	if len(in.ResponseFormat) > 0 {
		var format map[string]json.RawMessage
		if json.Unmarshal(in.ResponseFormat, &format) != nil {
			return nil, ErrInvalid
		}
		var kind string
		_ = json.Unmarshal(format["type"], &kind)
		mapped := format
		if kind == "json_schema" {
			if json.Unmarshal(format["json_schema"], &mapped) != nil || mapped == nil {
				return nil, ErrInvalid
			}
			mapped["type"] = json.RawMessage(`"json_schema"`)
		} else if !oneOf(kind, "text", "json_object") {
			return nil, ErrInvalid
		}
		text := map[string]json.RawMessage{}
		if len(body["text"]) > 0 {
			_ = json.Unmarshal(body["text"], &text)
		}
		text["format"], _ = json.Marshal(mapped)
		put("text", text)
	}
	raw, err := json.Marshal(body)
	if err != nil || len(raw) > MaxBodyBytes {
		return nil, ErrInvalid
	}
	return raw, nil
}
func parseResponseUsage(raw json.RawMessage) Usage {
	u := Usage{Unknown: true}
	var v struct {
		Input        *int64 `json:"input_tokens"`
		Output       *int64 `json:"output_tokens"`
		InputDetails struct {
			Cached     int64 `json:"cached_tokens"`
			CacheWrite int64 `json:"cache_write_tokens"`
		} `json:"input_tokens_details"`
		OutputDetails struct {
			Reasoning int64 `json:"reasoning_tokens"`
		} `json:"output_tokens_details"`
	}
	if json.Unmarshal(raw, &v) != nil || v.Input == nil || v.Output == nil {
		return u
	}
	for _, n := range []int64{*v.Input, *v.Output, v.InputDetails.Cached, v.InputDetails.CacheWrite, v.OutputDetails.Reasoning} {
		if n < 0 || n > 1000000000 {
			return u
		}
	}
	if v.InputDetails.Cached > *v.Input || v.OutputDetails.Reasoning > *v.Output {
		return u
	}
	return Usage{InputTokens: *v.Input, OutputTokens: *v.Output, CachedInputTokens: v.InputDetails.Cached, CacheWriteInputTokens: v.InputDetails.CacheWrite, ReasoningTokens: v.OutputDetails.Reasoning}
}
func projectResponses(id int64, raw json.RawMessage, s Settings) (Result, error) {
	out := Result{RequestID: id, Usage: Usage{Unknown: true}}
	var response struct {
		Status string            `json:"status"`
		Output []json.RawMessage `json:"output"`
		Usage  json.RawMessage   `json:"usage"`
		Error  json.RawMessage   `json:"error"`
	}
	if json.Unmarshal(raw, &response) != nil {
		return out, ErrProtocol
	}
	out.Usage = parseResponseUsage(response.Usage)
	if response.Status != "completed" || response.Output == nil || len(response.Output) > 10000 || len(response.Error) > 0 && string(response.Error) != "null" {
		return out, ErrProtocol
	}
	var text, refusal strings.Builder
	calls := []any{}
	seen := map[string]bool{}
	for _, item := range response.Output {
		var v struct {
			Type      string `json:"type"`
			Role      string `json:"role"`
			CallID    string `json:"call_id"`
			Name      string `json:"name"`
			Namespace string `json:"namespace"`
			Arguments string `json:"arguments"`
			Content   []struct {
				Type    string `json:"type"`
				Text    string `json:"text"`
				Refusal string `json:"refusal"`
			} `json:"content"`
		}
		if json.Unmarshal(item, &v) != nil {
			return out, ErrProtocol
		}
		switch v.Type {
		case "reasoning":
		case "message":
			if v.Role != "assistant" {
				return out, ErrProtocol
			}
			for _, part := range v.Content {
				switch part.Type {
				case "output_text":
					text.WriteString(part.Text)
				case "refusal":
					refusal.WriteString(part.Refusal)
				}
			}
		case "function_call":
			if v.CallID == "" || seen[v.CallID] || v.Name == "" || !json.Valid([]byte(v.Arguments)) || v.Namespace != "" && v.Namespace != "nonbiri" {
				return out, ErrProtocol
			}
			seen[v.CallID] = true
			calls = append(calls, map[string]any{"id": v.CallID, "type": "function", "function": map[string]string{"name": v.Name, "arguments": v.Arguments}})
		default:
			return out, ErrProtocol
		}
	}
	message := map[string]any{"role": "assistant", "content": text.String(), "responses_output": response.Output, "responses_provider": nativeIdentity(s)}
	out.FinishReason = "stop"
	if len(calls) > 0 {
		message["tool_calls"] = calls
		out.FinishReason = "tool_calls"
	}
	if refusal.Len() > 0 {
		message["refusal"] = refusal.String()
		out.FinishReason = "content_filter"
	}
	out.Message, _ = json.Marshal(message)
	return out, nil
}
func parseResponsesResult(id int64, raw []byte, s Settings) (Result, bool, string, error) {
	out := Result{RequestID: id, RawResponse: string(raw), Usage: Usage{Unknown: true}}
	completed := false
	code := "protocol"
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events, errs := egress.StreamSSE(ctx, io.NopCloser(bytes.NewReader(raw)), egress.SSEOptions{MaxBytes: MaxBodyBytes, MaxEventBytes: MaxBodyBytes})
	terminal := false
	var parseErr error
	for event := range events {
		if event.Data == "[DONE]" && terminal {
			continue
		}
		var value struct {
			Type     string          `json:"type"`
			Response json.RawMessage `json:"response"`
		}
		if json.Unmarshal([]byte(event.Data), &value) != nil || value.Type == "" || event.Event != "message" && event.Event != "" && event.Event != value.Type || terminal {
			parseErr = ErrProtocol
			continue
		}
		switch value.Type {
		case "response.completed", "response.failed", "response.incomplete":
			terminal = true
			var response struct {
				Usage json.RawMessage `json:"usage"`
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			_ = json.Unmarshal(value.Response, &response)
			out.Usage = parseResponseUsage(response.Usage)
			if value.Type == "response.completed" {
				parsed, e := projectResponses(id, value.Response, s)
				parsed.RawResponse = string(raw)
				out = parsed
				if e != nil {
					parseErr = e
				} else {
					completed = true
					code = ""
				}
			} else {
				parseErr = ErrProtocol
				code = "upstream_incomplete"
				if value.Type == "response.failed" {
					code = "upstream_error"
					if oneOf(response.Error.Code, "subscription_sharing_usage_limit_exceeded", "subscription_sharing_usage_unavailable", "subscription_sharing_user_not_eligible", "subscription_sharing_unsupported_capability") {
						code = response.Error.Code
					}
				}
			}
		case "error":
			parseErr = ErrProtocol
			terminal = true
			code = "upstream_error"
		}
	}
	for err := range errs {
		if err != nil {
			parseErr = ErrProtocol
		}
	}
	if !completed || parseErr != nil {
		if code == "" {
			code = "protocol"
		}
		return out, false, code, ErrProtocol
	}
	return out, true, "", nil
}
