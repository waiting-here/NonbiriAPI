package openai

import (
	"bytes"
	"encoding/json"
	"math"
	"regexp"
	"sort"
	"strings"

	"github.com/waiting-here/NonbiriAPI/internal/connector/continuation"
	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
)

var responseFunctionName = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// compileChatResponse maps only fields with an equivalent Chat representation.
// Protocol selection is explicit and never depends on an upstream error.
func (r *InferenceRequest) compileChatResponse(model string, policy contract.AttemptPolicy) ([]byte, error) {
	return r.compileChatResponseMode(model, policy, false)
}

func (r *InferenceRequest) compileChatResponseMode(model string, policy contract.AttemptPolicy, gateway bool) ([]byte, error) {
	object := fieldsByName(r.fields)
	bad := func() ([]byte, error) {
		return nil, invalidField("request", "Responses fields cannot be represented by the selected Chat protocol")
	}
	for key := range object {
		switch key {
		case "model", "input", "instructions", "stream", "store", "background", "tools", "tool_choice", "parallel_tool_calls", "max_output_tokens", "temperature", "top_p", "text":
		default:
			return bad()
		}
	}
	messages, err := responseChatMessagesMode(object["input"], object["instructions"], gateway)
	if err != nil {
		return nil, err
	}
	out := map[string]any{"model": model, "messages": messages, "stream": r.Stream, "store": false, "n": 1}
	if policy.SafetyIdentifier != "" {
		out["safety_identifier"] = policy.SafetyIdentifier
	}
	if r.Stream {
		out["stream_options"] = map[string]bool{"include_usage": true}
	}
	if raw := object["max_output_tokens"]; raw != nil && !isJSONNull(raw) {
		var n int64
		if !decodeNonNegativeInt(raw, &n) || n < 1 || n > math.MaxInt32 {
			return bad()
		}
		out["max_completion_tokens"] = n
	}
	for key, max := range map[string]float64{"temperature": 2, "top_p": 1} {
		if raw := object[key]; raw != nil && !isJSONNull(raw) {
			if !systemOneNumber(raw, max) {
				return bad()
			}
			out[key] = raw
		}
	}
	if raw := object["parallel_tool_calls"]; raw != nil && !isJSONNull(raw) {
		if !bytes.Equal(raw, []byte("true")) && !bytes.Equal(raw, []byte("false")) {
			return bad()
		}
		out["parallel_tool_calls"] = raw
	}
	names := map[string]bool{}
	if raw := object["tools"]; raw != nil && !isJSONNull(raw) {
		var tools []json.RawMessage
		if json.Unmarshal(raw, &tools) != nil || tools == nil || len(tools) > 128 {
			return bad()
		}
		mapped := []any{}
		for _, raw := range tools {
			tool, ok := decisionObject(raw, "type", "name", "description", "parameters", "strict")
			name, valid := decisionString(tool["name"], 1, 64)
			if !ok || !valid || !responseFunctionName.MatchString(name) || names[name] || !bytes.Equal(tool["type"], []byte(`"function"`)) || !optionalDecisionString(tool["description"], 0, int(r.RequestBodyLimit()), true) {
				return bad()
			}
			names[name] = true
			function := map[string]any{"name": name}
			if raw := tool["description"]; raw != nil {
				function["description"] = raw
			}
			strict := true
			if raw := tool["strict"]; raw != nil && !isJSONNull(raw) {
				if json.Unmarshal(raw, &strict) != nil {
					return bad()
				}
			}
			params := tool["parameters"]
			if params == nil || isJSONNull(params) {
				params = json.RawMessage(`{"type":"object","properties":{}}`)
			}
			schema, ok := systemOneMap(params)
			if !ok {
				return bad()
			}
			if tool["strict"] == nil || isJSONNull(tool["strict"]) {
				schema, ok = strictResponseSchema(schema, 0)
				if !ok {
					return bad()
				}
			}
			function["parameters"] = schema
			function["strict"] = strict
			mapped = append(mapped, map[string]any{"type": "function", "function": function})
		}
		out["tools"] = mapped
	}
	if raw := object["tool_choice"]; raw != nil && !isJSONNull(raw) {
		if choice, ok := decisionString(raw, 0, 32); ok {
			if choice != "auto" && choice != "none" && choice != "required" || choice == "required" && len(names) == 0 {
				return bad()
			}
			out["tool_choice"] = choice
		} else {
			choice, ok := decisionObject(raw, "type", "name")
			name, _ := decisionString(choice["name"], 1, 64)
			if !ok || !bytes.Equal(choice["type"], []byte(`"function"`)) || !names[name] {
				return bad()
			}
			out["tool_choice"] = map[string]any{"type": "function", "function": map[string]string{"name": name}}
		}
	}
	if raw := object["text"]; raw != nil && !isJSONNull(raw) {
		text, ok := decisionObject(raw, "format")
		if !ok {
			return bad()
		}
		if format := text["format"]; format != nil && !isJSONNull(format) {
			value, ok := decisionObject(format, "type", "name", "description", "schema", "strict")
			if !ok {
				return bad()
			}
			kind, _ := decisionString(value["type"], 1, 32)
			switch kind {
			case "text", "json_object":
				if len(value) != 1 {
					return bad()
				}
				out["response_format"] = map[string]string{"type": kind}
			case "json_schema":
				name, ok := decisionString(value["name"], 1, 64)
				if !ok || !responseFunctionName.MatchString(name) {
					return bad()
				}
				schema, ok := systemOneMap(value["schema"])
				if !ok {
					return bad()
				}
				wrapped := map[string]any{"name": name, "schema": schema}
				if raw := value["strict"]; raw != nil {
					if !isJSONNull(raw) && !bytes.Equal(raw, []byte("true")) && !bytes.Equal(raw, []byte("false")) {
						return bad()
					}
					wrapped["strict"] = raw
				}
				if raw := value["description"]; raw != nil {
					if !optionalDecisionString(raw, 0, int(r.RequestBodyLimit()), true) {
						return bad()
					}
					wrapped["description"] = raw
				}
				out["response_format"] = map[string]any{"type": "json_schema", "json_schema": wrapped}
			default:
				return bad()
			}
		}
	}
	encoded, err := json.Marshal(out)
	if int64(len(encoded)) > r.RequestBodyLimit()+(16<<10) {
		clear(encoded)
		return nil, ErrPayloadTooLarge
	}
	return encoded, err
}

// Responses tries strict normalization when strict is omitted. Limit automatic
// conversion to this common schema subset; callers can select strict explicitly
// for other schemas instead of guessing whether Responses would fall back.
func strictResponseSchema(schema map[string]json.RawMessage, depth int) (map[string]json.RawMessage, bool) {
	if depth > 32 {
		return nil, false
	}
	for key := range schema {
		switch key {
		case "type", "properties", "required", "additionalProperties", "items", "anyOf", "$defs", "$ref", "description", "title", "enum":
		default:
			return nil, false
		}
	}
	if raw := schema["additionalProperties"]; raw != nil && !bytes.Equal(raw, []byte("false")) {
		return nil, false
	}
	if schema["type"] == nil && schema["$ref"] == nil && schema["anyOf"] == nil {
		return nil, false
	}
	if raw := schema["$ref"]; raw != nil {
		ref, ok := decisionString(raw, 1, 512)
		if !ok || ref != "#" && !strings.HasPrefix(ref, "#/") {
			return nil, false
		}
	}
	objectType, arrayType := false, false
	if raw := schema["type"]; raw != nil {
		var types []string
		if value, ok := decisionString(raw, 1, 16); ok {
			types = []string{value}
		} else if json.Unmarshal(raw, &types) != nil || len(types) == 0 {
			return nil, false
		}
		for _, kind := range types {
			switch kind {
			case "object":
				objectType = true
			case "array":
				arrayType = true
			case "string", "number", "integer", "boolean", "null":
			default:
				return nil, false
			}
		}
	}
	if arrayType && schema["items"] == nil {
		return nil, false
	}
	if schema["properties"] != nil && !objectType {
		return nil, false
	}
	if properties, ok := schema["properties"]; ok {
		children, ok := systemOneMap(properties)
		if !ok {
			return nil, false
		}
		keys := []string{}
		for key, raw := range children {
			child, ok := systemOneMap(raw)
			if !ok {
				return nil, false
			}
			child, ok = strictResponseSchema(child, depth+1)
			if !ok {
				return nil, false
			}
			children[key], _ = json.Marshal(child)
			keys = append(keys, key)
		}
		sort.Strings(keys)
		schema["required"], _ = json.Marshal(keys)
		schema["properties"], _ = json.Marshal(children)
		schema["additionalProperties"] = json.RawMessage("false")
	} else if objectType {
		schema["additionalProperties"] = json.RawMessage("false")
		schema["required"] = json.RawMessage("[]")
	}
	for _, key := range []string{"items"} {
		raw := schema[key]
		if raw != nil && (len(raw) == 0 || raw[0] != '{') {
			return nil, false
		}
		if len(raw) > 0 && raw[0] == '{' {
			child, ok := systemOneMap(raw)
			if !ok {
				return nil, false
			}
			child, ok = strictResponseSchema(child, depth+1)
			if !ok {
				return nil, false
			}
			schema[key], _ = json.Marshal(child)
		}
	}
	for _, key := range []string{"$defs"} {
		if raw := schema[key]; raw != nil {
			children, ok := systemOneMap(raw)
			if !ok {
				return nil, false
			}
			for name, raw := range children {
				child, ok := systemOneMap(raw)
				if !ok {
					return nil, false
				}
				child, ok = strictResponseSchema(child, depth+1)
				if !ok {
					return nil, false
				}
				children[name], _ = json.Marshal(child)
			}
			schema[key], _ = json.Marshal(children)
		}
	}
	for _, key := range []string{"anyOf"} {
		if raw := schema[key]; raw != nil {
			var children []map[string]json.RawMessage
			if json.Unmarshal(raw, &children) != nil || children == nil {
				return nil, false
			}
			for i, child := range children {
				normalized, ok := strictResponseSchema(child, depth+1)
				if !ok {
					return nil, false
				}
				children[i] = normalized
			}
			schema[key], _ = json.Marshal(children)
		}
	}
	return schema, true
}

func responseChatMessages(input, instructions []byte) ([]any, error) {
	return responseChatMessagesMode(input, instructions, false)
}

func responseChatMessagesMode(input, instructions []byte, gateway bool) ([]any, error) {
	bad := func() ([]any, error) {
		return nil, invalidField("input", "Responses input cannot be represented as complete Chat messages")
	}
	messages := []any{}
	if instructions != nil && !isJSONNull(instructions) {
		text, ok := decisionString(instructions, 0, int(DefaultMaxJSONResponseBytes))
		if !ok {
			return bad()
		}
		messages = append(messages, map[string]any{"role": "system", "content": text})
	}
	if text, ok := decisionString(input, 0, int(DefaultMaxJSONResponseBytes)); ok {
		return append(messages, map[string]any{"role": "user", "content": text}), nil
	}
	var items []json.RawMessage
	if json.Unmarshal(input, &items) != nil || items == nil {
		return bad()
	}
	pending := map[string]bool{}
	seen := map[string]bool{}
	for _, raw := range items {
		item, ok := systemOneMap(raw)
		if !ok {
			return bad()
		}
		kind, _ := decisionString(item["type"], 0, 64)
		var extra json.RawMessage
		if value, present := item["extra_content"]; present {
			outer, ok := systemOneMap(value)
			if !gateway || !ok || !continuation.ValidExtra(outer, "gateway") || outer["gateway"] == nil {
				return bad()
			}
			extra = value
		}
		switch kind {
		case "reasoning":
			if !gateway || len(pending) > 0 {
				return bad()
			}
			for name := range item {
				if name != "type" && name != "id" && name != "status" && name != "summary" && name != "content" && name != "extra_content" {
					return bad()
				}
			}
			if item["status"] != nil && !bytes.Equal(item["status"], []byte(`"completed"`)) {
				return bad()
			}
			if item["summary"] != nil && !bytes.Equal(bytes.TrimSpace(item["summary"]), []byte(`[]`)) {
				return bad()
			}
			var parts []map[string]json.RawMessage
			if json.Unmarshal(item["content"], &parts) != nil || parts == nil {
				return bad()
			}
			var reasoning strings.Builder
			for _, part := range parts {
				if len(part) != 2 || string(part["type"]) != `"reasoning_text"` {
					return bad()
				}
				value, ok := decisionString(part["text"], 0, int(DefaultMaxJSONResponseBytes))
				if !ok {
					return bad()
				}
				reasoning.WriteString(value)
			}
			message := map[string]any{"role": "assistant", "content": nil, "reasoning_content": reasoning.String()}
			if extra != nil {
				message["extra_content"] = extra
			}
			if len(messages) > 0 {
				last := messages[len(messages)-1].(map[string]any)
				if last["role"] == "assistant" && (extra == nil || last["extra_content"] == nil) {
					previous, _ := last["reasoning_content"].(string)
					last["reasoning_content"] = previous + reasoning.String()
					if extra != nil {
						last["extra_content"] = extra
					}
					continue
				}
			}
			messages = append(messages, message)
		case "", "message":
			if len(pending) > 0 {
				return bad()
			}
			for key := range item {
				if key != "type" && key != "role" && key != "content" && key != "status" && key != "id" && !(gateway && key == "extra_content") {
					return bad()
				}
			}
			if raw := item["status"]; raw != nil && !bytes.Equal(raw, []byte(`"completed"`)) {
				return bad()
			}
			role, _ := decisionString(item["role"], 1, 32)
			if role != "user" && role != "assistant" && role != "system" && role != "developer" {
				return bad()
			}
			content, err := responseChatContent(item["content"], role)
			if err != nil {
				return bad()
			}
			message := map[string]any{"role": role, "content": content}
			if extra != nil {
				if role != "assistant" {
					return bad()
				}
				message["extra_content"] = extra
			}
			if gateway && role == "assistant" && len(messages) > 0 {
				last := messages[len(messages)-1].(map[string]any)
				if last["role"] == "assistant" && last["reasoning_content"] != nil && last["content"] == nil && (extra == nil || last["extra_content"] == nil) {
					last["content"] = content
					if extra != nil {
						last["extra_content"] = extra
					}
					continue
				}
			}
			messages = append(messages, message)
		case "function_call":
			if len(pending) > 0 && len(messages) > 0 {
				last, _ := messages[len(messages)-1].(map[string]any)
				if last["role"] == "tool" {
					return bad()
				}
			}
			allowed := []string{"type", "id", "call_id", "name", "arguments", "status"}
			if gateway {
				allowed = append(allowed, "extra_content")
			}
			object, ok := decisionObject(raw, allowed...)
			if !ok {
				return bad()
			}
			if status := object["status"]; status != nil && !bytes.Equal(status, []byte(`"completed"`)) {
				return bad()
			}
			id, ok := decisionString(object["call_id"], 1, 512)
			name, valid := decisionString(object["name"], 1, 64)
			arguments, argsOK := decisionString(object["arguments"], 0, int(DefaultMaxJSONResponseBytes))
			if !ok || !valid || !responseFunctionName.MatchString(name) || !argsOK || seen[id] {
				return bad()
			}
			seen[id] = true
			pending[id] = true
			tool := map[string]any{"id": id, "type": "function", "function": map[string]string{"name": name, "arguments": arguments}}
			if len(messages) > 0 {
				last, _ := messages[len(messages)-1].(map[string]any)
				if last["role"] == "assistant" && (last["tool_calls"] != nil || gateway) && (extra == nil || last["extra_content"] == nil) {
					calls, _ := last["tool_calls"].([]any)
					last["tool_calls"] = append(calls, tool)
					if extra != nil {
						last["extra_content"] = extra
					}
					continue
				}
			}
			message := map[string]any{"role": "assistant", "content": nil, "tool_calls": []any{tool}}
			if extra != nil {
				message["extra_content"] = extra
			}
			messages = append(messages, message)
		case "function_call_output":
			object, ok := decisionObject(raw, "type", "id", "call_id", "output", "status")
			if !ok {
				return bad()
			}
			if status := object["status"]; status != nil && !bytes.Equal(status, []byte(`"completed"`)) {
				return bad()
			}
			id, ok := decisionString(object["call_id"], 1, 512)
			output, valid := decisionString(object["output"], 0, int(DefaultMaxJSONResponseBytes))
			if !ok || !valid || !pending[id] {
				return bad()
			}
			delete(pending, id)
			messages = append(messages, map[string]any{"role": "tool", "tool_call_id": id, "content": output})
		default:
			return bad()
		}
	}
	if len(pending) > 0 {
		return bad()
	}
	return messages, nil
}

func responseChatContent(raw []byte, role string) (any, error) {
	if text, ok := decisionString(raw, 0, int(DefaultMaxJSONResponseBytes)); ok {
		return text, nil
	}
	var parts []json.RawMessage
	if json.Unmarshal(raw, &parts) != nil || parts == nil {
		return nil, ErrInvalidRequest
	}
	output := []any{}
	for _, raw := range parts {
		part, ok := systemOneMap(raw)
		if !ok {
			return nil, ErrInvalidRequest
		}
		kind, _ := decisionString(part["type"], 1, 32)
		switch kind {
		case "input_text", "output_text":
			for key := range part {
				if key != "type" && key != "text" && !(kind == "output_text" && (key == "annotations" || key == "logprobs") && bytes.Equal(bytes.TrimSpace(part[key]), []byte("[]"))) {
					return nil, ErrInvalidRequest
				}
			}
			text, ok := decisionString(part["text"], 0, int(DefaultMaxJSONResponseBytes))
			if !ok || kind == "output_text" && role != "assistant" {
				return nil, ErrInvalidRequest
			}
			output = append(output, map[string]any{"type": "text", "text": text})
		case "refusal":
			value, ok := decisionObject(raw, "type", "refusal")
			refusal, valid := decisionString(value["refusal"], 0, int(DefaultMaxJSONResponseBytes))
			if !ok || !valid || role != "assistant" {
				return nil, ErrInvalidRequest
			}
			output = append(output, map[string]any{"type": "refusal", "refusal": refusal})
		case "input_image":
			value, ok := decisionObject(raw, "type", "image_url", "detail")
			url, valid := decisionString(value["image_url"], 1, int(DefaultMaxJSONResponseBytes))
			if !ok || !valid || role != "user" || !(strings.HasPrefix(url, "https://") || strings.HasPrefix(url, "http://") || strings.HasPrefix(url, "data:image/")) {
				return nil, ErrInvalidRequest
			}
			image := map[string]any{"url": url}
			if detail := value["detail"]; detail != nil {
				if !optionalDecisionEnum(detail, false, "auto", "low", "high") {
					return nil, ErrInvalidRequest
				}
				image["detail"] = detail
			}
			output = append(output, map[string]any{"type": "image_url", "image_url": image})
		default:
			return nil, ErrInvalidRequest
		}
	}
	return output, nil
}
