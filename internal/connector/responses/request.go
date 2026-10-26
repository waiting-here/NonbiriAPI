// Package responses maps stateless Responses requests to Chat Completions.
package responses

import (
	"bytes"
	"encoding/json"
	"net/url"
	"regexp"
	"strings"

	"github.com/waiting-here/NonbiriAPI/internal/connector/chatwire"
	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/connector/openai"
	"github.com/waiting-here/NonbiriAPI/internal/requestadaptation"
)

var namePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

func URL(base, _ string, _ bool) (string, error) {
	return strings.TrimSuffix(base, "/") + "/responses", nil
}
func SupportsRequest(r *openai.ChatRequest) bool {
	body, err := Compile(r, "model", contract.AttemptPolicy{SafetyIdentifier: "consumer"})
	clear(body)
	return err == nil
}
func Compile(r *openai.ChatRequest, model string, policy contract.AttemptPolicy) ([]byte, error) {
	if r == nil {
		return nil, chatwire.Rejection("request")
	}
	body, err := r.LogicalBody()
	if err != nil {
		return nil, err
	}
	defer clear(body)
	root, err := chatwire.Object(body)
	if err != nil || !chatwire.Keys(root, "model", "messages", "stream", "stream_options", "max_tokens", "max_completion_tokens", "temperature", "top_p", "n", "tools", "tool_choice", "parallel_tool_calls", "response_format", "reasoning_effort", "store", "safety_identifier", "prompt_cache_key", "prompt_cache_retention") {
		return nil, chatwire.RejectionReason("request", "unsupported top-level field")
	}
	if !chatwire.Null(root["n"]) {
		n, ok := chatwire.Integer(root["n"])
		if !ok || n != 1 {
			return nil, chatwire.RejectionReason("n", "expected the integer 1")
		}
	}
	if !chatwire.Null(root["store"]) && string(root["store"]) != "false" {
		return nil, chatwire.RejectionReason("store", "expected false for this connector")
	}
	if !chatwire.Null(root["stream_options"]) {
		options, err := chatwire.Object(root["stream_options"])
		if err != nil || !chatwire.Keys(options, "include_usage") {
			return nil, chatwire.RejectionReason("stream_options", "expected only include_usage with a boolean value")
		}
		if v, ok := options["include_usage"]; ok && string(v) != "true" && string(v) != "false" {
			return nil, chatwire.RejectionReason("stream_options", "expected only include_usage with a boolean value")
		}
	}
	input, err := messages(root["messages"])
	if err != nil {
		return nil, err
	}
	out := map[string]any{"model": model, "input": input, "stream": r.Stream, "store": false, "safety_identifier": policy.SafetyIdentifier, "include": []string{"reasoning.encrypted_content"}}
	var maximum int64
	for _, name := range []string{"max_tokens", "max_completion_tokens"} {
		if !chatwire.Null(root[name]) {
			value, ok := chatwire.Integer(root[name])
			if !ok || value < 1 || value > 2147483647 {
				return nil, chatwire.RejectionReason(name, "expected an integer from 1 to 2147483647")
			}
			if maximum != 0 && maximum != value {
				return nil, chatwire.RejectionReason("max_completion_tokens/max_tokens", "conflicting output budgets")
			}
			maximum = value
		}
	}
	if maximum > 0 {
		out["max_output_tokens"] = maximum
	}
	for _, name := range []string{"temperature", "top_p"} {
		if !chatwire.Null(root[name]) {
			value, ok := chatwire.Number(root[name])
			max := 2.0
			if name == "top_p" {
				max = 1
			}
			if !ok || value < 0 || value > max {
				return nil, chatwire.RejectionReason(name, "expected a number within the supported sampling range")
			}
			out[name] = value
		}
	}
	if !chatwire.Null(root["reasoning_effort"]) {
		value, ok := chatwire.String(root["reasoning_effort"])
		if !ok {
			return nil, chatwire.Rejection("reasoning_effort")
		}
		switch value {
		case "none", "minimal", "low", "medium", "high", "xhigh":
		default:
			return nil, chatwire.Rejection("reasoning_effort")
		}
		out["reasoning"] = map[string]string{"effort": value}
	}
	for _, name := range []string{"prompt_cache_key", "prompt_cache_retention"} {
		if !chatwire.Null(root[name]) {
			value, ok := chatwire.String(root[name])
			if !ok || name == "prompt_cache_key" && len(value) > 64 || name == "prompt_cache_retention" && value != "in-memory" && value != "24h" {
				return nil, chatwire.Rejection(name)
			}
			out[name] = value
		}
	}
	names := map[string]bool{}
	if !chatwire.Null(root["tools"]) {
		var definitions []json.RawMessage
		if json.Unmarshal(root["tools"], &definitions) != nil || len(definitions) > 128 {
			return nil, chatwire.Rejection("tools")
		}
		tools := []any{}
		for _, raw := range definitions {
			tool, err := chatwire.Object(raw)
			if err != nil || !chatwire.Keys(tool, "type", "function") || string(tool["type"]) != `"function"` {
				return nil, chatwire.Rejection("tools")
			}
			function, err := chatwire.Object(tool["function"])
			if err != nil || !chatwire.Keys(function, "name", "description", "parameters", "strict") {
				return nil, chatwire.Rejection("tools")
			}
			name, ok := chatwire.String(function["name"])
			if !ok || !namePattern.MatchString(name) || names[name] {
				return nil, chatwire.Rejection("tools")
			}
			names[name] = true
			value := map[string]any{"type": "function", "name": name}
			if raw, ok := function["description"]; ok {
				description, ok := chatwire.String(raw)
				if !ok {
					return nil, chatwire.Rejection("tools")
				}
				value["description"] = description
			}
			if raw, ok := function["parameters"]; ok {
				params, err := chatwire.Object(raw)
				if err != nil || string(params["type"]) != `"object"` {
					return nil, chatwire.Rejection("tools")
				}
				value["parameters"] = params
			}
			// Responses otherwise normalizes omitted strict to true; Chat omits it as false.
			strict := false
			if raw, ok := function["strict"]; ok && !chatwire.Null(raw) {
				if json.Unmarshal(raw, &strict) != nil {
					return nil, chatwire.Rejection("tools")
				}
			}
			value["strict"] = strict
			tools = append(tools, value)
		}
		out["tools"] = tools
	}
	if !chatwire.Null(root["tool_choice"]) {
		if choice, ok := chatwire.String(root["tool_choice"]); ok {
			if choice != "none" && choice != "auto" && choice != "required" || len(names) == 0 && choice != "none" {
				return nil, chatwire.Rejection("tool_choice")
			}
			out["tool_choice"] = choice
		} else {
			choice, err := chatwire.Object(root["tool_choice"])
			if err != nil || !chatwire.Keys(choice, "type", "function") || string(choice["type"]) != `"function"` {
				return nil, chatwire.Rejection("tool_choice")
			}
			function, err := chatwire.Object(choice["function"])
			name, _ := chatwire.String(function["name"])
			if err != nil || !chatwire.Keys(function, "name") || !names[name] {
				return nil, chatwire.Rejection("tool_choice")
			}
			out["tool_choice"] = map[string]string{"type": "function", "name": name}
		}
	}
	if value, ok := root["parallel_tool_calls"]; ok && !chatwire.Null(value) {
		if string(value) != "true" && string(value) != "false" {
			return nil, chatwire.Rejection("parallel_tool_calls")
		}
		out["parallel_tool_calls"] = json.RawMessage(value)
	}
	if !chatwire.Null(root["response_format"]) {
		format, err := chatwire.Object(root["response_format"])
		kind, _ := chatwire.String(format["type"])
		if err != nil {
			return nil, chatwire.Rejection("response_format")
		}
		value := map[string]any{"type": kind}
		switch kind {
		case "text", "json_object":
			if !chatwire.Keys(format, "type") {
				return nil, chatwire.Rejection("response_format")
			}
		case "json_schema":
			if !chatwire.Keys(format, "type", "json_schema") {
				return nil, chatwire.Rejection("response_format")
			}
			schema, err := chatwire.Object(format["json_schema"])
			if err != nil || !chatwire.Keys(schema, "name", "description", "schema", "strict") {
				return nil, chatwire.Rejection("response_format")
			}
			name, ok := chatwire.String(schema["name"])
			if !ok || !namePattern.MatchString(name) {
				return nil, chatwire.Rejection("response_format")
			}
			if _, err := chatwire.Object(schema["schema"]); err != nil {
				return nil, chatwire.Rejection("response_format")
			}
			for k, v := range schema {
				value[k] = v
			}
			if raw, ok := schema["strict"]; ok && !chatwire.Null(raw) && string(raw) != "true" && string(raw) != "false" {
				return nil, chatwire.Rejection("response_format")
			}
			if raw, ok := schema["description"]; ok {
				if _, ok := chatwire.String(raw); !ok {
					return nil, chatwire.Rejection("response_format")
				}
			}
		default:
			return nil, chatwire.Rejection("response_format")
		}
		out["text"] = map[string]any{"format": value}
	}
	for path := range policy.NativeExtensions {
		if path != "/reasoning/summary" && path != "/text/verbosity" {
			return nil, chatwire.Rejection("native_extensions")
		}
	}
	compiled, err := json.Marshal(out)
	if err != nil {
		return nil, err
	}
	defer clear(compiled)
	merged, err := requestadaptation.MergeNative(compiled, policy.NativeExtensions, r.RequestBodyLimit()+(16<<10))
	if err != nil {
		return nil, err
	}
	for path, raw := range policy.NativeExtensions {
		value, ok := chatwire.String(raw)
		if !ok || path == "/reasoning/summary" && value != "auto" && value != "concise" && value != "detailed" || path == "/text/verbosity" && value != "low" && value != "medium" && value != "high" {
			clear(merged)
			return nil, chatwire.Rejection("native_extensions")
		}
	}
	return merged, nil
}

func messages(raw []byte) ([]any, error) {
	var values []json.RawMessage
	if json.Unmarshal(raw, &values) != nil || len(values) == 0 || len(values) > 4096 {
		return nil, chatwire.Rejection("messages")
	}
	out := []any{}
	pending := map[string]bool{}
	seen := map[string]bool{}
	for _, raw := range values {
		message, err := chatwire.Object(raw)
		if err != nil || !chatwire.Keys(message, "role", "content", "refusal", "tool_calls", "tool_call_id", "extra_content", "reasoning_content") {
			return nil, chatwire.Rejection("messages")
		}
		role, _ := chatwire.String(message["role"])
		if role != "tool" && len(pending) > 0 {
			return nil, chatwire.Rejection("tool_call_id")
		}
		if role == "tool" {
			if len(message["tool_calls"])+len(message["refusal"])+len(message["extra_content"])+len(message["reasoning_content"]) > 0 {
				return nil, chatwire.Rejection("messages")
			}
			id, _ := chatwire.String(message["tool_call_id"])
			text, ok := chatwire.String(message["content"])
			if !pending[id] || !ok {
				return nil, chatwire.Rejection("tool_call_id")
			}
			delete(pending, id)
			out = append(out, map[string]string{"type": "function_call_output", "call_id": id, "output": text})
			continue
		}
		if role != "system" && role != "developer" && role != "user" && role != "assistant" || len(message["tool_call_id"]) > 0 {
			return nil, chatwire.Rejection("messages")
		}
		if role != "assistant" && len(message["tool_calls"])+len(message["refusal"])+len(message["extra_content"])+len(message["reasoning_content"]) > 0 {
			return nil, chatwire.Rejection("messages")
		}
		if !chatwire.Null(message["extra_content"]) {
			reasoning, summary, err := reasoningExtension(message["extra_content"])
			if err != nil {
				return nil, chatwire.Rejection("extra_content")
			}
			if value, ok := message["reasoning_content"]; ok && !chatwire.Null(value) {
				text, ok := chatwire.String(value)
				if !ok || text != summary {
					return nil, chatwire.Rejection("reasoning_content")
				}
			}
			out = append(out, reasoning...)
		} else if !chatwire.Null(message["reasoning_content"]) {
			return nil, chatwire.Rejection("reasoning_content")
		}
		parts, err := content(message["content"], role)
		if err != nil {
			return nil, err
		}
		if !chatwire.Null(message["refusal"]) {
			text, ok := chatwire.String(message["refusal"])
			if !ok {
				return nil, chatwire.Rejection("refusal")
			}
			parts = append(parts, map[string]string{"type": "refusal", "refusal": text})
		}
		if len(parts) > 0 {
			out = append(out, map[string]any{"type": "message", "role": role, "content": parts})
		}
		if !chatwire.Null(message["tool_calls"]) {
			var calls []json.RawMessage
			if json.Unmarshal(message["tool_calls"], &calls) != nil || len(calls) > 128 {
				return nil, chatwire.Rejection("tool_calls")
			}
			for _, raw := range calls {
				call, err := chatwire.Object(raw)
				if err != nil || !chatwire.Keys(call, "id", "type", "function") || string(call["type"]) != `"function"` {
					return nil, chatwire.Rejection("tool_calls")
				}
				id, ok := chatwire.String(call["id"])
				if !ok || id == "" || len(id) > 128 || seen[id] {
					return nil, chatwire.Rejection("tool_calls")
				}
				seen[id] = true
				pending[id] = true
				function, err := chatwire.Object(call["function"])
				name, _ := chatwire.String(function["name"])
				args, ok := chatwire.String(function["arguments"])
				if err != nil || !chatwire.Keys(function, "name", "arguments") || !namePattern.MatchString(name) || !ok {
					return nil, chatwire.Rejection("tool_calls")
				}
				if _, err := chatwire.Object([]byte(args)); err != nil {
					return nil, chatwire.Rejection("tool_calls")
				}
				out = append(out, map[string]string{"type": "function_call", "call_id": id, "name": name, "arguments": args})
			}
		}
		if len(parts) == 0 && len(pending) == 0 {
			return nil, chatwire.Rejection("content")
		}
	}
	if len(pending) > 0 {
		return nil, chatwire.Rejection("tool_call_id")
	}
	return out, nil
}
func content(raw []byte, role string) ([]any, error) {
	if chatwire.Null(raw) {
		if role == "assistant" {
			return nil, nil
		}
		return nil, chatwire.Rejection("content")
	}
	kind := "input_text"
	if role == "assistant" {
		kind = "output_text"
	}
	if text, ok := chatwire.String(raw); ok {
		return []any{map[string]string{"type": kind, "text": text}}, nil
	}
	var values []json.RawMessage
	if json.Unmarshal(raw, &values) != nil {
		return nil, chatwire.Rejection("content")
	}
	out := []any{}
	for _, raw := range values {
		part, err := chatwire.Object(raw)
		if err != nil {
			return nil, chatwire.Rejection("content")
		}
		switch string(part["type"]) {
		case `"text"`:
			text, ok := chatwire.String(part["text"])
			if !ok || !chatwire.Keys(part, "type", "text") {
				return nil, chatwire.Rejection("content")
			}
			out = append(out, map[string]string{"type": kind, "text": text})
		case `"image_url"`:
			image, err := chatwire.Object(part["image_url"])
			if role != "user" || !chatwire.Keys(part, "type", "image_url") || err != nil || !chatwire.Keys(image, "url", "detail") {
				return nil, chatwire.Rejection("image_url")
			}
			value, ok := chatwire.String(image["url"])
			parsed, parseErr := url.Parse(value)
			if !ok || parseErr != nil || parsed.User != nil || parsed.Scheme != "https" && parsed.Scheme != "data" || parsed.Scheme == "https" && parsed.Hostname() == "" {
				return nil, chatwire.Rejection("image_url")
			}
			detail := "auto"
			if !chatwire.Null(image["detail"]) {
				detail, ok = chatwire.String(image["detail"])
				if !ok || detail != "auto" && detail != "low" && detail != "high" {
					return nil, chatwire.Rejection("image_url")
				}
			}
			out = append(out, map[string]string{"type": "input_image", "image_url": value, "detail": detail})
		default:
			return nil, chatwire.Rejection("content")
		}
	}
	return out, nil
}
func reasoningExtension(raw []byte) ([]any, string, error) {
	if len(raw) > 1<<20 {
		return nil, "", chatwire.ErrProtocol
	}
	outer, err := chatwire.Object(raw)
	if err != nil || !chatwire.Keys(outer, "openai") {
		return nil, "", chatwire.ErrProtocol
	}
	inner, err := chatwire.Object(outer["openai"])
	if err != nil || !chatwire.Keys(inner, "reasoning") {
		return nil, "", chatwire.ErrProtocol
	}
	var items []json.RawMessage
	if json.Unmarshal(inner["reasoning"], &items) != nil || len(items) == 0 || len(items) > 128 {
		return nil, "", chatwire.ErrProtocol
	}
	out := []any{}
	var summary strings.Builder
	seen := map[string]bool{}
	for _, raw := range items {
		item, text, err := reasoningItem(raw)
		if err != nil {
			return nil, "", err
		}
		id, _ := chatwire.String(item["id"])
		if seen[id] {
			return nil, "", chatwire.ErrProtocol
		}
		seen[id] = true
		out = append(out, item)
		summary.WriteString(text)
	}
	return out, summary.String(), nil
}
func reasoningItem(raw []byte) (map[string]json.RawMessage, string, error) {
	item, err := chatwire.Object(raw)
	if err != nil || !chatwire.Keys(item, "type", "id", "summary", "encrypted_content", "status") || string(item["type"]) != `"reasoning"` {
		return nil, "", chatwire.ErrProtocol
	}
	id, ok := chatwire.String(item["id"])
	if !ok || id == "" || len(id) > 512 {
		return nil, "", chatwire.ErrProtocol
	}
	if status, ok := item["status"]; ok && string(status) != `"completed"` && string(status) != `"incomplete"` {
		return nil, "", chatwire.ErrProtocol
	}
	if raw, ok := item["encrypted_content"]; ok && !chatwire.Null(raw) {
		text, ok := chatwire.String(raw)
		if !ok || len(text) > 64<<10 {
			return nil, "", chatwire.ErrProtocol
		}
	}
	var summary []json.RawMessage
	if json.Unmarshal(item["summary"], &summary) != nil || chatwire.Null(item["summary"]) {
		return nil, "", chatwire.ErrProtocol
	}
	var text bytes.Buffer
	for _, raw := range summary {
		part, err := chatwire.Object(raw)
		value, ok := chatwire.String(part["text"])
		if err != nil || !chatwire.Keys(part, "type", "text") || string(part["type"]) != `"summary_text"` || !ok {
			return nil, "", chatwire.ErrProtocol
		}
		text.WriteString(value)
	}
	return item, text.String(), nil
}
