// Package gemini adapts the GenerateContent REST protocol to Chat Completions.
package gemini

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/url"
	"regexp"
	"strings"

	"github.com/waiting-here/NonbiriAPI/internal/connector/chatwire"
	"github.com/waiting-here/NonbiriAPI/internal/connector/continuation"
	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/connector/openai"
	"github.com/waiting-here/NonbiriAPI/internal/requestadaptation"
)

var modelPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,511}$`)
var functionPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.:-]{0,63}$`)

func URL(base, model string, stream bool) (string, error) {
	model = strings.TrimPrefix(model, "models/")
	if !modelPattern.MatchString(model) || model == "." || model == ".." {
		return "", chatwire.Rejection("model")
	}
	suffix := ":generateContent"
	if stream {
		suffix = ":streamGenerateContent?alt=sse"
	}
	return strings.TrimSuffix(base, "/") + "/models/" + url.PathEscape(model) + suffix, nil
}

func SupportsRequest(request *openai.ChatRequest) bool {
	body, err := Compile(request, "model", contract.AttemptPolicy{SafetyIdentifier: "nbu_v3_placeholder"})
	clear(body)
	return err == nil
}

func Compile(request *openai.ChatRequest, model string, policy contract.AttemptPolicy) ([]byte, error) {
	if request == nil {
		return nil, chatwire.Rejection("request")
	}
	if policy.FlattenToolCalls {
		return nil, chatwire.Rejection("flatten_tool_calls")
	}
	if policy.ForceStoreFalse {
		return nil, chatwire.Rejection("store")
	}
	if _, err := URL("https://example.invalid/v1beta", model, request.Stream); err != nil {
		return nil, err
	}
	body, err := request.LogicalBody()
	if err != nil {
		return nil, err
	}
	defer clear(body)
	root, err := chatwire.Object(body)
	if err != nil {
		return nil, chatwire.Rejection("request")
	}
	if !chatwire.Keys(root, "model", "messages", "stream", "stream_options", "max_tokens", "max_completion_tokens", "temperature", "top_p", "stop", "n", "tools", "tool_choice", "parallel_tool_calls", "response_format", "safety_identifier") {
		return nil, chatwire.RejectionReason("request", "unsupported top-level field")
	}
	if !chatwire.Null(root["n"]) {
		n, ok := chatwire.Integer(root["n"])
		if !ok || n != 1 {
			return nil, chatwire.RejectionReason("n", "expected the integer 1")
		}
	}
	if !chatwire.Null(root["stream_options"]) {
		options, err := chatwire.Object(root["stream_options"])
		if err != nil || !chatwire.Keys(options, "include_usage") {
			return nil, chatwire.RejectionReason("stream_options", "expected only include_usage with a boolean value")
		}
		if value, ok := options["include_usage"]; ok && !bytes.Equal(value, []byte("true")) && !bytes.Equal(value, []byte("false")) {
			return nil, chatwire.RejectionReason("stream_options", "expected only include_usage with a boolean value")
		}
	}
	config := map[string]any{}
	var maxTokens int64
	for _, key := range []string{"max_tokens", "max_completion_tokens"} {
		if !chatwire.Null(root[key]) {
			value, ok := chatwire.Integer(root[key])
			if !ok || value < 1 || value > 2147483647 {
				return nil, chatwire.RejectionReason(key, "expected an integer from 1 to 2147483647")
			}
			if maxTokens != 0 && maxTokens != value {
				return nil, chatwire.RejectionReason("max_completion_tokens/max_tokens", "conflicting output budgets")
			}
			maxTokens = value
		}
	}
	if maxTokens != 0 {
		config["maxOutputTokens"] = maxTokens
	}
	for _, pair := range [][2]string{{"temperature", "temperature"}, {"top_p", "topP"}} {
		if !chatwire.Null(root[pair[0]]) {
			value, ok := chatwire.Number(root[pair[0]])
			max := 2.0
			if pair[0] == "top_p" {
				max = 1
			}
			if !ok || value < 0 || value > max {
				return nil, chatwire.RejectionReason(pair[0], "expected a number within the supported sampling range")
			}
			config[pair[1]] = value
		}
	}
	if !chatwire.Null(root["stop"]) {
		var values []string
		if s, ok := chatwire.String(root["stop"]); ok {
			values = []string{s}
		} else if json.Unmarshal(root["stop"], &values) != nil {
			return nil, chatwire.Rejection("stop")
		}
		if len(values) > 5 {
			return nil, chatwire.Rejection("stop")
		}
		config["stopSequences"] = values
	}
	if !chatwire.Null(root["response_format"]) {
		format, err := chatwire.Object(root["response_format"])
		kind, _ := chatwire.String(format["type"])
		if err != nil || !chatwire.Keys(format, "type") || kind != "text" && kind != "json_object" {
			return nil, chatwire.Rejection("response_format")
		}
		if kind == "json_object" {
			config["responseMimeType"] = "application/json"
		}
	}
	definitions, names, err := tools(root["tools"])
	if err != nil {
		return nil, err
	}
	if !chatwire.Null(root["parallel_tool_calls"]) {
		raw := root["parallel_tool_calls"]
		if !bytes.Equal(raw, []byte("true")) && !bytes.Equal(raw, []byte("false")) || len(names) > 0 && bytes.Equal(raw, []byte("false")) {
			return nil, chatwire.Rejection("parallel_tool_calls")
		}
	}
	contents, system, err := messages(root["messages"])
	if err != nil {
		return nil, err
	}
	out := map[string]any{"contents": contents, "labels": map[string]string{"safety_identifier": strings.ToLower(policy.SafetyIdentifier)}}
	if len(system) > 0 {
		out["systemInstruction"] = map[string]any{"parts": system}
	}
	if len(config) > 0 {
		out["generationConfig"] = config
	}
	if len(definitions) > 0 {
		out["tools"] = []any{map[string]any{"functionDeclarations": definitions}}
	}
	if !chatwire.Null(root["tool_choice"]) {
		mode, name := "", ""
		if choice, ok := chatwire.String(root["tool_choice"]); ok {
			switch choice {
			case "none":
				mode = "NONE"
			case "auto":
				mode = "AUTO"
			case "required":
				mode = "ANY"
			default:
				return nil, chatwire.Rejection("tool_choice")
			}
		} else {
			choice, err := chatwire.Object(root["tool_choice"])
			if err != nil || !chatwire.Keys(choice, "type", "function") || string(choice["type"]) != `"function"` {
				return nil, chatwire.Rejection("tool_choice")
			}
			function, err := chatwire.Object(choice["function"])
			if err != nil || !chatwire.Keys(function, "name") {
				return nil, chatwire.Rejection("tool_choice")
			}
			name, _ = chatwire.String(function["name"])
			if !names[name] {
				return nil, chatwire.Rejection("tool_choice")
			}
			mode = "ANY"
		}
		if len(names) == 0 && mode != "NONE" {
			return nil, chatwire.Rejection("tool_choice")
		}
		choice := map[string]any{"mode": mode}
		if name != "" {
			choice["allowedFunctionNames"] = []string{name}
		}
		out["toolConfig"] = map[string]any{"functionCallingConfig": choice}
	}
	for path := range policy.NativeExtensions {
		if path != "/generationConfig/thinkingConfig" && !strings.HasPrefix(path, "/generationConfig/thinkingConfig/") && path != "/safetySettings" && !strings.HasPrefix(path, "/safetySettings/") {
			return nil, chatwire.Rejection("native_extensions")
		}
	}
	compiled, err := json.Marshal(out)
	if err != nil {
		return nil, err
	}
	defer clear(compiled)
	merged, err := requestadaptation.MergeNative(compiled, policy.NativeExtensions, request.RequestBodyLimit()+(16<<10))
	if err != nil {
		return nil, err
	}
	if !validNative(merged) {
		clear(merged)
		return nil, chatwire.Rejection("native_extensions")
	}
	return merged, nil
}

func tools(raw []byte) ([]any, map[string]bool, error) {
	names := map[string]bool{}
	if chatwire.Null(raw) {
		return nil, names, nil
	}
	var values []json.RawMessage
	if json.Unmarshal(raw, &values) != nil || len(values) > 128 {
		return nil, nil, chatwire.Rejection("tools")
	}
	out := make([]any, 0, len(values))
	for _, raw := range values {
		tool, err := chatwire.Object(raw)
		if err != nil || !chatwire.Keys(tool, "type", "function") || string(tool["type"]) != `"function"` {
			return nil, nil, chatwire.Rejection("tools")
		}
		function, err := chatwire.Object(tool["function"])
		if err != nil || !chatwire.Keys(function, "name", "description", "parameters", "strict") {
			return nil, nil, chatwire.Rejection("tools")
		}
		name, ok := chatwire.String(function["name"])
		if !ok || !functionPattern.MatchString(name) || names[name] {
			return nil, nil, chatwire.Rejection("tools")
		}
		names[name] = true
		if !chatwire.Null(function["strict"]) && !bytes.Equal(function["strict"], []byte("false")) {
			return nil, nil, chatwire.Rejection("tools")
		}
		value := map[string]any{"name": name}
		if raw, ok := function["description"]; ok {
			description, ok := chatwire.String(raw)
			if !ok {
				return nil, nil, chatwire.Rejection("tools")
			}
			value["description"] = description
		}
		if raw, ok := function["parameters"]; ok {
			schema, err := chatwire.Object(raw)
			if err != nil || string(schema["type"]) != `"object"` {
				return nil, nil, chatwire.Rejection("tools")
			}
			value["parametersJsonSchema"] = schema
		}
		out = append(out, value)
	}
	return out, names, nil
}

func signature(raw []byte) (string, error) {
	if chatwire.Null(raw) {
		return "", nil
	}
	outer, err := chatwire.Object(raw)
	if err != nil || !continuation.ValidExtra(outer, "google") {
		return "", chatwire.Rejection("extra_content")
	}
	google, err := chatwire.Object(outer["google"])
	if err == nil && chatwire.Keys(google, "parts") && len(google["parts"]) > 0 {
		return "", nil
	}
	if err != nil || !chatwire.Keys(google, "thought_signature") {
		return "", chatwire.Rejection("extra_content")
	}
	value, ok := chatwire.String(google["thought_signature"])
	if !ok || len(value) == 0 || len(value) > 64<<10 {
		return "", chatwire.Rejection("extra_content")
	}
	if decoded, err := base64.StdEncoding.Strict().DecodeString(value); err != nil {
		return "", chatwire.Rejection("extra_content")
	} else {
		clear(decoded)
	}
	return value, nil
}

func messages(raw []byte) ([]any, []any, error) {
	var input []json.RawMessage
	if json.Unmarshal(raw, &input) != nil || len(input) == 0 || len(input) > 4096 {
		return nil, nil, chatwire.Rejection("messages")
	}
	var contents, system []any
	pending := map[string]string{}
	seen := map[string]bool{}
	for _, raw := range input {
		message, err := chatwire.Object(raw)
		if err != nil || !chatwire.Keys(message, "role", "content", "tool_calls", "tool_call_id", "extra_content", "reasoning_content") {
			return nil, nil, chatwire.Rejection("messages")
		}
		role, ok := chatwire.String(message["role"])
		if !ok {
			return nil, nil, chatwire.Rejection("messages")
		}
		if role != "tool" && len(pending) > 0 {
			return nil, nil, chatwire.Rejection("tool_call_id")
		}
		parts, err := content(message["content"], role)
		if err != nil {
			return nil, nil, err
		}
		if role == "system" || role == "developer" {
			if len(contents) > 0 || len(message["tool_calls"])+len(message["tool_call_id"])+len(message["extra_content"])+len(message["reasoning_content"]) > 0 {
				return nil, nil, chatwire.Rejection("messages")
			}
			system = append(system, parts...)
			continue
		}
		if role == "tool" {
			if len(message["tool_calls"])+len(message["extra_content"])+len(message["reasoning_content"]) > 0 {
				return nil, nil, chatwire.Rejection("messages")
			}
			id, ok := chatwire.String(message["tool_call_id"])
			name := pending[id]
			if !ok || name == "" {
				return nil, nil, chatwire.Rejection("tool_call_id")
			}
			delete(pending, id)
			text, ok := chatwire.String(message["content"])
			if !ok {
				return nil, nil, chatwire.Rejection("content")
			}
			response, err := chatwire.Object([]byte(text))
			var result any = response
			if err != nil {
				result = map[string]string{"result": text}
			}
			parts = []any{map[string]any{"functionResponse": map[string]any{"id": id, "name": name, "response": result}}}
			role = "user"
		} else if role == "assistant" {
			if len(message["tool_call_id"]) > 0 {
				return nil, nil, chatwire.Rejection("tool_call_id")
			}
			if !chatwire.Null(message["reasoning_content"]) {
				text, ok := chatwire.String(message["reasoning_content"])
				if !ok {
					return nil, nil, chatwire.Rejection("reasoning_content")
				}
				parts = append([]any{map[string]any{"text": text, "thought": true}}, parts...)
			}
			sig, err := signature(message["extra_content"])
			if err != nil {
				return nil, nil, err
			}
			if sig != "" {
				if len(parts) == 0 {
					return nil, nil, chatwire.Rejection("extra_content")
				}
				parts[len(parts)-1].(map[string]any)["thoughtSignature"] = sig
			}
			if !chatwire.Null(message["tool_calls"]) {
				var calls []json.RawMessage
				if json.Unmarshal(message["tool_calls"], &calls) != nil || len(calls) > 128 {
					return nil, nil, chatwire.Rejection("tool_calls")
				}
				for _, raw := range calls {
					call, err := chatwire.Object(raw)
					if err != nil || !chatwire.Keys(call, "id", "type", "function", "extra_content") || string(call["type"]) != `"function"` {
						return nil, nil, chatwire.Rejection("tool_calls")
					}
					id, ok := chatwire.String(call["id"])
					if !ok || id == "" || len(id) > 128 || seen[id] {
						return nil, nil, chatwire.Rejection("tool_calls")
					}
					seen[id] = true
					function, err := chatwire.Object(call["function"])
					if err != nil || !chatwire.Keys(function, "name", "arguments") {
						return nil, nil, chatwire.Rejection("tool_calls")
					}
					name, ok := chatwire.String(function["name"])
					if !ok || !functionPattern.MatchString(name) {
						return nil, nil, chatwire.Rejection("tool_calls")
					}
					args, ok := chatwire.String(function["arguments"])
					if !ok {
						return nil, nil, chatwire.Rejection("tool_calls")
					}
					arguments, err := chatwire.Object([]byte(args))
					if err != nil {
						return nil, nil, chatwire.Rejection("tool_calls")
					}
					pending[id] = name
					part := map[string]any{"functionCall": map[string]any{"id": id, "name": name, "args": arguments}}
					sig, err := signature(call["extra_content"])
					if err != nil {
						return nil, nil, err
					}
					if sig != "" {
						part["thoughtSignature"] = sig
					}
					parts = append(parts, part)
				}
			}
			parts, err = replayParts(message["extra_content"], parts)
			if err != nil {
				return nil, nil, err
			}
			role = "model"
		} else if role != "user" || len(message["tool_calls"])+len(message["tool_call_id"])+len(message["extra_content"])+len(message["reasoning_content"]) > 0 {
			return nil, nil, chatwire.Rejection("messages")
		}
		if len(parts) == 0 {
			return nil, nil, chatwire.Rejection("content")
		}
		// Parallel function responses belong to the same user turn.
		if len(contents) > 0 {
			last := contents[len(contents)-1].(map[string]any)
			if last["role"] == role {
				last["parts"] = append(last["parts"].([]any), parts...)
				continue
			}
		}
		contents = append(contents, map[string]any{"role": role, "parts": parts})
	}
	if len(pending) > 0 || len(contents) == 0 {
		return nil, nil, chatwire.Rejection("messages")
	}
	return contents, system, nil
}

func validNative(raw []byte) bool {
	root, err := chatwire.Object(raw)
	if err != nil {
		return false
	}
	if value, ok := root["generationConfig"]; ok {
		config, err := chatwire.Object(value)
		if err != nil {
			return false
		}
		if value, ok := config["thinkingConfig"]; ok {
			thinking, err := chatwire.Object(value)
			if err != nil || !chatwire.Keys(thinking, "includeThoughts", "thinkingBudget", "thinkingLevel") {
				return false
			}
			if value, ok := thinking["includeThoughts"]; ok && string(value) != "true" && string(value) != "false" {
				return false
			}
			if value, ok := thinking["thinkingBudget"]; ok {
				var n int64
				if json.Unmarshal(value, &n) != nil || chatwire.Null(value) || n < -1 || n > 2147483647 {
					return false
				}
			}
			if value, ok := thinking["thinkingLevel"]; ok {
				s, ok := chatwire.String(value)
				if !ok || s != "MINIMAL" && s != "LOW" && s != "MEDIUM" && s != "HIGH" {
					return false
				}
			}
			if _, a := thinking["thinkingLevel"]; a {
				if _, b := thinking["thinkingBudget"]; b {
					return false
				}
			}
		}
	}
	if value, ok := root["safetySettings"]; ok {
		var settings []json.RawMessage
		if json.Unmarshal(value, &settings) != nil || chatwire.Null(value) || len(settings) > 5 {
			return false
		}
		seen := map[string]bool{}
		for _, raw := range settings {
			setting, err := chatwire.Object(raw)
			if err != nil || !chatwire.Keys(setting, "category", "threshold") {
				return false
			}
			category, _ := chatwire.String(setting["category"])
			threshold, _ := chatwire.String(setting["threshold"])
			switch category {
			case "HARM_CATEGORY_HARASSMENT", "HARM_CATEGORY_HATE_SPEECH", "HARM_CATEGORY_SEXUALLY_EXPLICIT", "HARM_CATEGORY_DANGEROUS_CONTENT", "HARM_CATEGORY_CIVIC_INTEGRITY":
			default:
				return false
			}
			switch threshold {
			case "BLOCK_LOW_AND_ABOVE", "BLOCK_MEDIUM_AND_ABOVE", "BLOCK_ONLY_HIGH", "BLOCK_NONE", "OFF":
			default:
				return false
			}
			if seen[category] {
				return false
			}
			seen[category] = true
		}
	}
	return true
}

func content(raw []byte, role string) ([]any, error) {
	if chatwire.Null(raw) {
		if role == "assistant" {
			return nil, nil
		}
		return nil, chatwire.Rejection("content")
	}
	if text, ok := chatwire.String(raw); ok {
		return []any{map[string]any{"text": text}}, nil
	}
	var input []json.RawMessage
	if json.Unmarshal(raw, &input) != nil {
		return nil, chatwire.Rejection("content")
	}
	out := make([]any, 0, len(input))
	for _, raw := range input {
		part, err := chatwire.Object(raw)
		if err != nil {
			return nil, chatwire.Rejection("content")
		}
		kind, _ := chatwire.String(part["type"])
		switch kind {
		case "text":
			if !chatwire.Keys(part, "type", "text") {
				return nil, chatwire.Rejection("content")
			}
			text, ok := chatwire.String(part["text"])
			if !ok {
				return nil, chatwire.Rejection("content")
			}
			out = append(out, map[string]any{"text": text})
		case "image_url":
			if role != "user" || !chatwire.Keys(part, "type", "image_url") {
				return nil, chatwire.Rejection("image_url")
			}
			image, err := chatwire.Object(part["image_url"])
			if err != nil || !chatwire.Keys(image, "url", "detail") {
				return nil, chatwire.Rejection("image_url")
			}
			if !chatwire.Null(image["detail"]) && string(image["detail"]) != `"auto"` {
				return nil, chatwire.Rejection("image_url")
			}
			value, ok := chatwire.String(image["url"])
			if !ok {
				return nil, chatwire.Rejection("image_url")
			}
			comma := strings.IndexByte(value, ',')
			if comma < 0 {
				return nil, chatwire.Rejection("image_url")
			}
			media := strings.TrimSuffix(strings.TrimPrefix(value[:comma], "data:"), ";base64")
			if value[:comma] != "data:"+media+";base64" || media != "image/png" && media != "image/jpeg" && media != "image/gif" && media != "image/webp" {
				return nil, chatwire.Rejection("image_url")
			}
			encoded := value[comma+1:]
			data, err := base64.StdEncoding.Strict().DecodeString(encoded)
			if err != nil || len(data) == 0 {
				clear(data)
				return nil, chatwire.Rejection("image_url")
			}
			clear(data)
			out = append(out, map[string]any{"inlineData": map[string]string{"mimeType": media, "data": encoded}})
		default:
			return nil, chatwire.Rejection("content")
		}
	}
	return out, nil
}
