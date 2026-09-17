// Package gateway implements the native AI SDK Gateway language/embedding v3
// wire protocol. Its request compiler is also the pure routing fidelity check.
package gateway

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/waiting-here/NonbiriAPI/internal/connector/openai"
	"github.com/waiting-here/NonbiriAPI/internal/strictjson"
)

var errRequest = errors.New("gateway: request cannot be represented faithfully")
var errResponse = errors.New("gateway: invalid upstream response")

type object = map[string]json.RawMessage

func parseObject(raw []byte) (object, error) {
	if strictjson.ValidateObjectWithFieldLimit(raw, 16384) != nil {
		return nil, errRequest
	}
	var value object
	if json.Unmarshal(raw, &value) != nil {
		return nil, errRequest
	}
	return value, nil
}
func only(value object, keys ...string) bool {
	for key := range value {
		found := false
		for _, allowed := range keys {
			if key == allowed {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
func text(raw []byte) (string, bool) {
	var out string
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) || json.Unmarshal(raw, &out) != nil {
		return "", false
	}
	return out, utf8.ValidString(out)
}
func opaque(value string, maximum int) bool {
	if value == "" || strings.TrimSpace(value) != value || !utf8.ValidString(value) || utf8.RuneCountInString(value) > maximum {
		return false
	}
	for _, ch := range value {
		if unicode.IsControl(ch) {
			return false
		}
	}
	return true
}
func number(raw []byte, minimum, maximum float64, integer bool) bool {
	var value float64
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) || json.Unmarshal(raw, &value) != nil || math.IsNaN(value) || math.IsInf(value, 0) {
		return false
	}
	return value >= minimum && value <= maximum && (!integer || value == math.Trunc(value))
}
func isNull(raw []byte) bool { return bytes.Equal(bytes.TrimSpace(raw), []byte("null")) }

func SupportsRequest(request *openai.ChatRequest) bool {
	body, err := compileChat(request, "")
	clear(body)
	return err == nil
}

func compileChat(request *openai.ChatRequest, attribution string) ([]byte, error) {
	if request == nil {
		return nil, errRequest
	}
	output := map[string]any{}
	fields := object{}
	for _, name := range request.Requirements().TopLevelFields() {
		fields[name], _ = request.RawField(name)
	}
	defer func() {
		for _, raw := range fields {
			clear(raw)
		}
	}()
	for name, raw := range fields {
		switch name {
		case "model", "messages", "tools", "tool_choice", "safety_identifier":
		case "stream":
			if !bytes.Equal(raw, []byte("true")) && !bytes.Equal(raw, []byte("false")) {
				return nil, errRequest
			}
		case "stream_options":
			if isNull(raw) {
				continue
			}
			options, err := parseObject(raw)
			if err != nil || !only(options, "include_usage") {
				return nil, errRequest
			}
			if value, ok := options["include_usage"]; ok && !bytes.Equal(value, []byte("true")) && !bytes.Equal(value, []byte("false")) {
				return nil, errRequest
			}
		case "max_tokens", "temperature", "top_p", "top_k", "presence_penalty", "frequency_penalty", "seed":
			if isNull(raw) {
				continue
			}
			wire := map[string]string{"max_tokens": "maxOutputTokens", "temperature": "temperature", "top_p": "topP", "top_k": "topK", "presence_penalty": "presencePenalty", "frequency_penalty": "frequencyPenalty", "seed": "seed"}[name]
			min, max, integer := 0.0, 2.0, false
			switch name {
			case "max_tokens":
				min, max, integer = 1, 2147483647, true
			case "top_k":
				max, integer = 2147483647, true
			case "top_p":
				max = 1
			case "presence_penalty", "frequency_penalty":
				min = -2
			case "seed":
				min, max, integer = -9007199254740991, 9007199254740991, true
			}
			if !number(raw, min, max, integer) {
				return nil, errRequest
			}
			output[wire] = json.RawMessage(raw)
		case "stop":
			if isNull(raw) {
				continue
			}
			var stops []string
			if value, ok := text(raw); ok {
				stops = []string{value}
			} else {
				var entries []json.RawMessage
				if json.Unmarshal(raw, &entries) != nil || entries == nil {
					return nil, errRequest
				}
				stops = make([]string, len(entries))
				for i, entry := range entries {
					value, ok := text(entry)
					if !ok {
						return nil, errRequest
					}
					stops[i] = value
				}
			}
			if len(stops) > 64 {
				return nil, errRequest
			}
			for _, value := range stops {
				if value == "" || !utf8.ValidString(value) {
					return nil, errRequest
				}
			}
			output["stopSequences"] = stops
		case "n":
			if !number(raw, 1, 1, true) {
				return nil, errRequest
			}
		case "logit_bias":
			value, err := parseObject(raw)
			if err != nil || len(value) != 0 {
				return nil, errRequest
			}
		case "logprobs":
			if !bytes.Equal(raw, []byte("false")) {
				return nil, errRequest
			}
		case "response_format":
			value, err := parseObject(raw)
			kind, ok := text(value["type"])
			if err != nil || !ok || kind != "text" || len(value) != 1 {
				return nil, errRequest
			}
		default:
			return nil, errRequest
		}
	}
	prompt, err := compilePrompt(fields["messages"])
	if err != nil {
		return nil, err
	}
	output["prompt"] = prompt
	names := map[string]bool{}
	if raw, ok := fields["tools"]; ok {
		var tools []json.RawMessage
		if json.Unmarshal(raw, &tools) != nil || tools == nil || len(tools) > 128 {
			return nil, errRequest
		}
		translated := make([]any, 0, len(tools))
		for _, rawTool := range tools {
			tool, err := parseObject(rawTool)
			kind, ok := text(tool["type"])
			if err != nil || !only(tool, "type", "function") || !ok || kind != "function" {
				return nil, errRequest
			}
			fn, err := parseObject(tool["function"])
			name, ok := text(fn["name"])
			if err != nil || !only(fn, "name", "description", "parameters", "strict") || !ok || !opaque(name, 64) || names[name] {
				return nil, errRequest
			}
			names[name] = true
			if _, err := parseObject(fn["parameters"]); err != nil {
				return nil, errRequest
			}
			entry := map[string]any{"type": "function", "name": name, "inputSchema": fn["parameters"]}
			if raw, ok := fn["description"]; ok {
				value, valid := text(raw)
				if !valid {
					return nil, errRequest
				}
				entry["description"] = value
			}
			if raw, ok := fn["strict"]; ok {
				if !bytes.Equal(raw, []byte("true")) && !bytes.Equal(raw, []byte("false")) {
					return nil, errRequest
				}
				entry["strict"] = raw
			}
			translated = append(translated, entry)
		}
		output["tools"] = translated
	}
	if raw, ok := fields["tool_choice"]; ok {
		if choice, valid := text(raw); valid {
			if choice != "auto" && choice != "none" && choice != "required" {
				return nil, errRequest
			}
			if choice == "required" && len(names) == 0 {
				return nil, errRequest
			}
			output["toolChoice"] = map[string]string{"type": choice}
		} else {
			choice, err := parseObject(raw)
			kind, valid := text(choice["type"])
			fn, fnErr := parseObject(choice["function"])
			name, nameOK := text(fn["name"])
			if err != nil || fnErr != nil || !only(choice, "type", "function") || !only(fn, "name") || !valid || kind != "function" || !nameOK || !names[name] {
				return nil, errRequest
			}
			output["toolChoice"] = map[string]string{"type": "tool", "toolName": name}
		}
	}
	if attribution != "" {
		output["providerOptions"] = map[string]any{"gateway": map[string]string{"user": attribution}}
	}
	return json.Marshal(output)
}

func compilePrompt(raw []byte) ([]any, error) {
	var messages []json.RawMessage
	if json.Unmarshal(raw, &messages) != nil || len(messages) == 0 || len(messages) > 4096 {
		return nil, errRequest
	}
	result := make([]any, 0, len(messages))
	calls := map[string]string{}
	finished := map[string]bool{}
	for _, rawMessage := range messages {
		message, err := parseObject(rawMessage)
		role, ok := text(message["role"])
		if err != nil || !ok {
			return nil, errRequest
		}
		if role == "system" {
			value, ok := text(message["content"])
			if !only(message, "role", "content") || !ok {
				return nil, errRequest
			}
			result = append(result, map[string]any{"role": role, "content": value})
			continue
		}
		if role == "tool" {
			id, ok := text(message["tool_call_id"])
			value, contentOK := text(message["content"])
			if !only(message, "role", "content", "tool_call_id") || !ok || !contentOK || calls[id] == "" || finished[id] {
				return nil, errRequest
			}
			finished[id] = true
			result = append(result, map[string]any{"role": "tool", "content": []any{map[string]any{"type": "tool-result", "toolCallId": id, "toolName": calls[id], "output": map[string]string{"type": "text", "value": value}}}})
			continue
		}
		if role != "user" && role != "assistant" || !only(message, "role", "content", "tool_calls") {
			return nil, errRequest
		}
		parts, err := compileContent(message["content"], role)
		if err != nil {
			return nil, err
		}
		if rawCalls, present := message["tool_calls"]; present {
			var toolCalls []json.RawMessage
			if role != "assistant" || json.Unmarshal(rawCalls, &toolCalls) != nil || len(toolCalls) == 0 || len(toolCalls) > 128 {
				return nil, errRequest
			}
			for _, rawCall := range toolCalls {
				call, err := parseObject(rawCall)
				id, idOK := text(call["id"])
				kind, kindOK := text(call["type"])
				fn, fnErr := parseObject(call["function"])
				name, nameOK := text(fn["name"])
				args, argsOK := text(fn["arguments"])
				if err != nil || fnErr != nil || !only(call, "id", "type", "function") || !only(fn, "name", "arguments") || !idOK || !opaque(id, 512) || !kindOK || kind != "function" || !nameOK || !opaque(name, 64) || !argsOK || calls[id] != "" {
					return nil, errRequest
				}
				if _, err := parseObject([]byte(args)); err != nil {
					return nil, errRequest
				}
				calls[id] = name
				parts = append(parts, map[string]any{"type": "tool-call", "toolCallId": id, "toolName": name, "input": json.RawMessage(args)})
			}
		}
		if len(parts) == 0 {
			return nil, errRequest
		}
		result = append(result, map[string]any{"role": role, "content": parts})
	}
	return result, nil
}

func compileContent(raw []byte, role string) ([]any, error) {
	if role == "assistant" && (len(raw) == 0 || isNull(raw)) {
		return []any{}, nil
	}
	if value, ok := text(raw); ok {
		return []any{map[string]string{"type": "text", "text": value}}, nil
	}
	var parts []json.RawMessage
	if json.Unmarshal(raw, &parts) != nil || parts == nil || len(parts) > 4096 {
		return nil, errRequest
	}
	out := make([]any, 0, len(parts))
	for _, rawPart := range parts {
		part, err := parseObject(rawPart)
		kind, ok := text(part["type"])
		if err != nil || !ok {
			return nil, errRequest
		}
		switch kind {
		case "text":
			value, ok := text(part["text"])
			if !ok || !only(part, "type", "text") {
				return nil, errRequest
			}
			out = append(out, map[string]string{"type": "text", "text": value})
		case "image_url":
			image, err := parseObject(part["image_url"])
			value, ok := text(image["url"])
			if role != "user" || err != nil || !ok || !only(part, "type", "image_url") || !only(image, "url", "detail") {
				return nil, errRequest
			}
			if detail, present := image["detail"]; present {
				value, ok := text(detail)
				if !ok || value != "auto" {
					return nil, errRequest
				}
			}
			media, err := imageMediaType(value)
			if err != nil {
				return nil, err
			}
			out = append(out, map[string]string{"type": "file", "data": value, "mediaType": media})
		default:
			return nil, errRequest
		}
	}
	return out, nil
}

func imageMediaType(value string) (string, error) {
	if strings.HasPrefix(value, "data:") {
		metadata, data, ok := strings.Cut(strings.TrimPrefix(value, "data:"), ",")
		if !ok || !strings.HasSuffix(metadata, ";base64") {
			return "", errRequest
		}
		media := strings.TrimSuffix(metadata, ";base64")
		if media != "image/png" && media != "image/jpeg" && media != "image/webp" && media != "image/gif" {
			return "", errRequest
		}
		decoded, err := base64.StdEncoding.Strict().DecodeString(data)
		empty := len(decoded) == 0
		clear(decoded)
		if err != nil || empty {
			return "", errRequest
		}
		return media, nil
	}
	u, err := url.Parse(value)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil || u.Fragment != "" {
		return "", errRequest
	}
	return "image/*", nil
}

func SupportsEmbedding(request *openai.EmbeddingRequest) bool {
	body, err := compileEmbedding(request, "")
	clear(body)
	return err == nil
}
func compileEmbedding(request *openai.EmbeddingRequest, attribution string) ([]byte, error) {
	if request == nil || request.EncodingFormat != "float" || request.Dimensions != 0 {
		return nil, errRequest
	}
	for _, field := range request.TopLevelFields() {
		switch field {
		case "model", "input", "encoding_format", "stream":
		default:
			return nil, errRequest
		}
	}
	raw, _ := request.RawField("input")
	defer clear(raw)
	var values []string
	if value, ok := text(raw); ok {
		values = []string{value}
	} else if json.Unmarshal(raw, &values) != nil || len(values) == 0 {
		return nil, errRequest
	}
	if len(values) != request.InputCount {
		return nil, errRequest
	}
	out := map[string]any{"values": values}
	if attribution != "" {
		out["providerOptions"] = map[string]any{"gateway": map[string]string{"user": attribution}}
	}
	return json.Marshal(out)
}
