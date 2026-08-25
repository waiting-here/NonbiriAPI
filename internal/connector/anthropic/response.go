package anthropic

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"time"
	"unicode/utf8"

	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/connector/openai"
)

var ErrInvalidResponse = errors.New("anthropic connector: invalid upstream response")

type completionEnvelope struct {
	ID      string             `json:"id"`
	Object  string             `json:"object"`
	Created int64              `json:"created"`
	Model   string             `json:"model"`
	Choices []completionChoice `json:"choices"`
	Usage   *callerUsage       `json:"usage,omitempty"`
}

type completionChoice struct {
	Index        int               `json:"index"`
	Message      completionMessage `json:"message"`
	FinishReason string            `json:"finish_reason"`
}

type completionMessage struct {
	Role      string           `json:"role"`
	Content   any              `json:"content"`
	ToolCalls []callerToolCall `json:"tool_calls,omitempty"`
}

type callerToolCall struct {
	ID       string             `json:"id"`
	Type     string             `json:"type"`
	Function callerToolFunction `json:"function"`
}

type callerToolFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type callerUsage struct {
	PromptTokens        int64               `json:"prompt_tokens"`
	CompletionTokens    int64               `json:"completion_tokens"`
	TotalTokens         int64               `json:"total_tokens"`
	PromptTokensDetails callerPromptDetails `json:"prompt_tokens_details"`
}

type callerPromptDetails struct {
	CachedTokens     int64 `json:"cached_tokens"`
	CacheWriteTokens int64 `json:"cache_write_tokens"`
}

func translateNonStream(body []byte, publicModel string, started time.Time) ([]byte, connectorcontract.Usage, []string, error) {
	root, err := strictObject(body)
	if err != nil || !onlyKeys(root, "id", "type", "role", "model", "content", "stop_reason", "stop_sequence", "stop_details", "container", "usage") {
		return nil, connectorcontract.Usage{}, nil, ErrInvalidResponse
	}
	var id, kind, role, upstreamModel string
	if json.Unmarshal(root["id"], &id) != nil || !validOpaqueRunes(id, 512, true) ||
		json.Unmarshal(root["type"], &kind) != nil || kind != "message" ||
		json.Unmarshal(root["role"], &role) != nil || role != "assistant" ||
		json.Unmarshal(root["model"], &upstreamModel) != nil || !validOpaqueRunes(upstreamModel, openai.MaxUpstreamModelRunes, true) {
		return nil, connectorcontract.Usage{}, nil, ErrInvalidResponse
	}
	finish, upstreamStopReason, err := parseStopReasonKind(root["stop_reason"])
	if err != nil {
		return nil, connectorcontract.Usage{}, nil, ErrInvalidResponse
	}
	containerPresent, err := validateContainer(root["container"])
	if err != nil || containerPresent {
		return nil, connectorcontract.Usage{}, nil, ErrInvalidResponse
	}
	if err := validateStopDetails(root["stop_details"], finish); err != nil {
		return nil, connectorcontract.Usage{}, nil, ErrInvalidResponse
	}
	if validateStopSequence(root["stop_sequence"], upstreamStopReason) != nil {
		return nil, connectorcontract.Usage{}, nil, ErrInvalidResponse
	}
	var blocks []json.RawMessage
	if json.Unmarshal(root["content"], &blocks) != nil || len(blocks) > maxMessages {
		return nil, connectorcontract.Usage{}, nil, ErrInvalidResponse
	}
	var content strings.Builder
	textSeen := false
	tools := make([]callerToolCall, 0)
	toolIDs := make(map[string]struct{})
	semantic := make([]string, 0, 1+len(blocks)*3)
	semantic = append(semantic, id)
	for _, rawBlock := range blocks {
		block, err := strictObject(rawBlock)
		if err != nil {
			return nil, connectorcontract.Usage{}, nil, ErrInvalidResponse
		}
		var blockType string
		if json.Unmarshal(block["type"], &blockType) != nil {
			return nil, connectorcontract.Usage{}, nil, ErrInvalidResponse
		}
		switch blockType {
		case "text":
			if !onlyKeys(block, "type", "text") {
				return nil, connectorcontract.Usage{}, nil, ErrInvalidResponse
			}
			var text string
			if json.Unmarshal(block["text"], &text) != nil {
				return nil, connectorcontract.Usage{}, nil, ErrInvalidResponse
			}
			content.WriteString(text)
			textSeen = true
			semantic = append(semantic, text)
		case "tool_use":
			if len(tools) == maxTools {
				return nil, connectorcontract.Usage{}, nil, ErrInvalidResponse
			}
			if !onlyKeys(block, "type", "id", "name", "input") {
				return nil, connectorcontract.Usage{}, nil, ErrInvalidResponse
			}
			var callID, name string
			if json.Unmarshal(block["id"], &callID) != nil || !validToolID(callID) ||
				json.Unmarshal(block["name"], &name) != nil || !functionNameRegexp.MatchString(name) {
				return nil, connectorcontract.Usage{}, nil, ErrInvalidResponse
			}
			if _, duplicate := toolIDs[callID]; duplicate {
				return nil, connectorcontract.Usage{}, nil, ErrInvalidResponse
			}
			toolIDs[callID] = struct{}{}
			if _, err := strictObject(block["input"]); err != nil {
				return nil, connectorcontract.Usage{}, nil, ErrInvalidResponse
			}
			var compact bytes.Buffer
			if json.Compact(&compact, block["input"]) != nil {
				return nil, connectorcontract.Usage{}, nil, ErrInvalidResponse
			}
			arguments := compact.String()
			semantic = append(semantic, callID, name, arguments)
			tools = append(tools, callerToolCall{ID: callID, Type: "function", Function: callerToolFunction{Name: name, Arguments: arguments}})
		default:
			return nil, connectorcontract.Usage{}, nil, ErrInvalidResponse
		}
	}
	if (finish == "tool_calls") != (len(tools) > 0) {
		return nil, connectorcontract.Usage{}, nil, ErrInvalidResponse
	}
	usage, err := parseAnthropicUsage(root["usage"], true)
	if err != nil {
		return nil, connectorcontract.Usage{}, nil, ErrInvalidResponse
	}
	var contentValue any
	if textSeen || len(tools) == 0 {
		contentValue = content.String()
	}
	message := completionMessage{Role: "assistant", Content: contentValue, ToolCalls: tools}
	envelope := completionEnvelope{
		ID: id, Object: "chat.completion", Created: started.Unix(), Model: publicModel,
		Choices: []completionChoice{{Index: 0, Message: message, FinishReason: finish}},
	}
	if usage.Present {
		visible, ok := visibleUsage(usage)
		if ok {
			envelope.Usage = &visible
		} else {
			usage = connectorcontract.Usage{}
		}
	}
	out, err := marshalJSONNoEscapeLimited(envelope, MaxCallerJSONResponseBytes)
	if err != nil {
		clear(out)
		return nil, connectorcontract.Usage{}, nil, ErrInvalidResponse
	}
	return out, usage, semantic, nil
}

func parseStopReason(raw []byte) (string, error) {
	mapped, _, err := parseStopReasonKind(raw)
	return mapped, err
}

func parseStopReasonKind(raw []byte) (string, string, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return "", "", ErrInvalidResponse
	}
	var reason string
	if json.Unmarshal(trimmed, &reason) != nil {
		return "", "", ErrInvalidResponse
	}
	switch reason {
	case "end_turn", "stop_sequence":
		return "stop", reason, nil
	case "max_tokens", "model_context_window_exceeded":
		return "length", reason, nil
	case "tool_use":
		return "tool_calls", reason, nil
	case "refusal":
		return "content_filter", reason, nil
	default:
		return "", "", ErrInvalidResponse
	}
}

func validateStopSequence(raw []byte, upstreamReason string) error {
	trimmed := bytes.TrimSpace(raw)
	present := len(trimmed) != 0 && !bytes.Equal(trimmed, []byte("null"))
	if upstreamReason != "stop_sequence" {
		if present {
			return ErrInvalidResponse
		}
		return nil
	}
	if !present {
		return ErrInvalidResponse
	}
	var sequence string
	if json.Unmarshal(trimmed, &sequence) != nil || !validStop(sequence) {
		return ErrInvalidResponse
	}
	return nil
}

func parseAnthropicUsage(raw []byte, full bool) (connectorcontract.Usage, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return connectorcontract.Usage{}, nil
	}
	object, err := strictObject(trimmed)
	allowed := []string{"input_tokens", "cache_creation_input_tokens", "cache_read_input_tokens", "output_tokens", "output_tokens_details", "server_tool_use"}
	if full {
		allowed = append(allowed, "cache_creation", "inference_geo", "service_tier")
	}
	if err != nil || !onlyKeys(object, allowed...) {
		return connectorcontract.Usage{}, ErrInvalidResponse
	}
	usage := connectorcontract.Usage{Present: true}
	known := true
	outputKnown := true
	fields := []struct {
		name string
		dst  *int64
	}{
		{"input_tokens", &usage.UncachedInputTokens},
		{"cache_creation_input_tokens", &usage.CacheWriteInputTokens},
		{"cache_read_input_tokens", &usage.CacheReadInputTokens},
		{"output_tokens", &usage.OutputTokens},
	}
	for index, field := range fields {
		value, ok := object[field.name]
		if !ok {
			if full && (index == 0 || index == 3) {
				known = false
				if index == 3 {
					outputKnown = false
				}
			}
			continue
		}
		if isNull(value) || json.Unmarshal(value, field.dst) != nil || *field.dst < 0 {
			known = false
			if index == 3 {
				outputKnown = false
			}
		}
	}
	if err := validateUsageMetadata(object, full, usage.OutputTokens, outputKnown); err != nil {
		return connectorcontract.Usage{}, err
	}
	if !known {
		return connectorcontract.Usage{}, nil
	}
	if _, ok := visibleUsage(usage); !ok {
		return connectorcontract.Usage{}, nil
	}
	return usage, nil
}

func validateUsageMetadata(object map[string]json.RawMessage, full bool, outputTokens int64, outputKnown bool) error {
	if full {
		if err := validateCountObject(object["cache_creation"], "ephemeral_1h_input_tokens", "ephemeral_5m_input_tokens"); err != nil {
			return err
		}
		if raw := bytes.TrimSpace(object["inference_geo"]); len(raw) > 0 && !bytes.Equal(raw, []byte("null")) {
			var value string
			if json.Unmarshal(raw, &value) != nil || !utf8.ValidString(value) || len([]byte(value)) > 256 {
				return ErrInvalidResponse
			}
		}
		if raw := bytes.TrimSpace(object["service_tier"]); len(raw) > 0 && !bytes.Equal(raw, []byte("null")) {
			var value string
			if json.Unmarshal(raw, &value) != nil || (value != "standard" && value != "priority" && value != "batch") {
				return ErrInvalidResponse
			}
		}
	}
	if err := validateCountObject(object["server_tool_use"], "web_fetch_requests", "web_search_requests"); err != nil {
		return err
	}
	if raw := bytes.TrimSpace(object["output_tokens_details"]); len(raw) > 0 && !bytes.Equal(raw, []byte("null")) {
		details, err := strictObject(raw)
		if err != nil || !onlyKeys(details, "thinking_tokens") || len(details) != 1 {
			return ErrInvalidResponse
		}
		var thinking int64
		if json.Unmarshal(details["thinking_tokens"], &thinking) != nil || thinking < 0 || (outputKnown && thinking > outputTokens) {
			return ErrInvalidResponse
		}
	}
	return nil
}

func validateCountObject(raw []byte, names ...string) error {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil
	}
	object, err := strictObject(trimmed)
	if err != nil || !onlyKeys(object, names...) || len(object) != len(names) {
		return ErrInvalidResponse
	}
	for _, name := range names {
		var value int64
		if json.Unmarshal(object[name], &value) != nil || value < 0 {
			return ErrInvalidResponse
		}
	}
	return nil
}

func validateStopDetails(raw []byte, finishReason string) error {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil
	}
	if finishReason != "content_filter" {
		return ErrInvalidResponse
	}
	object, err := strictObject(trimmed)
	if err != nil || !onlyKeys(object, "type", "category", "explanation") {
		return ErrInvalidResponse
	}
	var kind string
	if json.Unmarshal(object["type"], &kind) != nil || kind != "refusal" {
		return ErrInvalidResponse
	}
	if rawCategory := bytes.TrimSpace(object["category"]); len(rawCategory) > 0 && !bytes.Equal(rawCategory, []byte("null")) {
		var category string
		if json.Unmarshal(rawCategory, &category) != nil || !oneOf(category, "cyber", "bio", "frontier_llm", "reasoning_extraction", "general_harms") {
			return ErrInvalidResponse
		}
	}
	if rawExplanation := bytes.TrimSpace(object["explanation"]); len(rawExplanation) > 0 && !bytes.Equal(rawExplanation, []byte("null")) {
		var explanation string
		if json.Unmarshal(rawExplanation, &explanation) != nil || !utf8.ValidString(explanation) || len([]byte(explanation)) > maxDescriptionBytes {
			return ErrInvalidResponse
		}
	}
	return nil
}

func validateContainer(raw []byte) (bool, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return false, nil
	}
	object, err := strictObject(trimmed)
	if err != nil || !onlyKeys(object, "id", "expires_at", "skills") {
		return false, ErrInvalidResponse
	}
	var id, expiresAt string
	if json.Unmarshal(object["id"], &id) != nil || !validOpaqueRunes(id, 512, true) ||
		json.Unmarshal(object["expires_at"], &expiresAt) != nil || len(expiresAt) > 128 {
		return false, ErrInvalidResponse
	}
	if _, err := time.Parse(time.RFC3339, expiresAt); err != nil {
		return false, ErrInvalidResponse
	}
	if rawSkills := bytes.TrimSpace(object["skills"]); len(rawSkills) > 0 && !bytes.Equal(rawSkills, []byte("null")) {
		var skills []json.RawMessage
		if json.Unmarshal(rawSkills, &skills) != nil || len(skills) > maxTools {
			return false, ErrInvalidResponse
		}
		for _, skill := range skills {
			skillObject, err := strictObject(skill)
			if err != nil || !onlyKeys(skillObject, "skill_id", "type", "version") || len(skillObject) != 3 {
				return false, ErrInvalidResponse
			}
			var skillID, kind, version string
			if json.Unmarshal(skillObject["skill_id"], &skillID) != nil || !validOpaqueRunes(skillID, 512, true) ||
				json.Unmarshal(skillObject["type"], &kind) != nil || !oneOf(kind, "anthropic", "custom") ||
				json.Unmarshal(skillObject["version"], &version) != nil || !validOpaqueRunes(version, 512, true) {
				return false, ErrInvalidResponse
			}
		}
	}
	return true, nil
}

func oneOf(value string, allowed ...string) bool {
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}

func visibleUsage(usage connectorcontract.Usage) (callerUsage, bool) {
	if !usage.Present || usage.UncachedInputTokens < 0 || usage.CacheWriteInputTokens < 0 || usage.CacheReadInputTokens < 0 || usage.OutputTokens < 0 {
		return callerUsage{}, false
	}
	prompt, ok := checkedAdd(usage.UncachedInputTokens, usage.CacheWriteInputTokens)
	if !ok {
		return callerUsage{}, false
	}
	prompt, ok = checkedAdd(prompt, usage.CacheReadInputTokens)
	if !ok {
		return callerUsage{}, false
	}
	total, ok := checkedAdd(prompt, usage.OutputTokens)
	if !ok {
		return callerUsage{}, false
	}
	return callerUsage{
		PromptTokens: prompt, CompletionTokens: usage.OutputTokens, TotalTokens: total,
		PromptTokensDetails: callerPromptDetails{CachedTokens: usage.CacheReadInputTokens, CacheWriteTokens: usage.CacheWriteInputTokens},
	}, true
}

func checkedAdd(first, second int64) (int64, bool) {
	if second > 0 && first > math.MaxInt64-second {
		return 0, false
	}
	return first + second, true
}

func validOpaqueRunes(value string, maxRunes int, rejectEdgeSpace bool) bool {
	if value == "" || !utf8.ValidString(value) || utf8.RuneCountInString(value) > maxRunes || (rejectEdgeSpace && strings.TrimSpace(value) != value) {
		return false
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f || r == utf8.RuneError {
			return false
		}
	}
	return true
}
