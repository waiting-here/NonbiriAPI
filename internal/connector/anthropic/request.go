// Package anthropic implements the first-party Anthropic Messages connector.
// It translates the public OpenAI Chat Completions ingress explicitly and
// rejects every combination that cannot be represented without loss.
package anthropic

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/connector/openai"
)

const (
	BuiltInDefaultMaxTokens int64 = 65536
	MaxMaxTokens            int64 = 2147483647
	maxMessages                   = 4096
	maxTools                      = 128
	maxStopSequences              = 4
	maxStopBytes                  = 256
	maxImageURLBytes              = 8 << 10
	maxToolIDBytes                = 128
	maxDescriptionBytes           = 16 << 10
	maxJSONDepth                  = 64
	maxJSONFields                 = 4096
)

var (
	ErrInvalidRequest  = errors.New("anthropic connector: invalid request")
	ErrDefaultTokens   = errors.New("anthropic connector: default max tokens unavailable")
	functionNameRegexp = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
)

type outboundRequest struct {
	Model         string            `json:"model"`
	MaxTokens     int64             `json:"max_tokens"`
	Messages      []outboundMessage `json:"messages"`
	System        string            `json:"system,omitempty"`
	Tools         []outboundTool    `json:"tools,omitempty"`
	ToolChoice    *outboundChoice   `json:"tool_choice,omitempty"`
	Metadata      outboundMetadata  `json:"metadata"`
	Stream        bool              `json:"stream,omitempty"`
	Temperature   json.RawMessage   `json:"temperature,omitempty"`
	TopP          json.RawMessage   `json:"top_p,omitempty"`
	StopSequences []string          `json:"stop_sequences,omitempty"`
}

type outboundMessage struct {
	Role    string          `json:"role"`
	Content []outboundBlock `json:"content"`
}

type outboundBlock struct {
	Type      string          `json:"type"`
	Text      *string         `json:"text,omitempty"`
	Source    *imageSource    `json:"source,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   any             `json:"content,omitempty"`
}

// newTextBlock preserves the distinction between a non-text union member and
// an explicitly empty text block. A plain string with omitempty would turn
// {"type":"text","text":""} into the invalid, lossy {"type":"text"}.
func newTextBlock(value string) outboundBlock {
	return outboundBlock{Type: "text", Text: &value}
}

type imageSource struct {
	Type      string `json:"type"`
	URL       string `json:"url,omitempty"`
	MediaType string `json:"media_type,omitempty"`
	Data      string `json:"data,omitempty"`
}

type outboundTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema"`
}

type outboundChoice struct {
	Type                   string `json:"type"`
	Name                   string `json:"name,omitempty"`
	DisableParallelToolUse bool   `json:"disable_parallel_tool_use,omitempty"`
}

type outboundMetadata struct {
	UserID string `json:"user_id"`
}

// SupportsRequest performs connector-specific fidelity validation without
// resolving a physical target, reading a site setting, or touching a secret.
// It is safe to call before selection for both personal and charity routes.
func SupportsRequest(request *openai.ChatRequest) bool {
	body, err := compileRequest(request, "placeholder-model", "nbu_v3_placeholder", BuiltInDefaultMaxTokens)
	clear(body)
	return err == nil
}

func resolveDefaultMaxTokens(ctx context.Context, provider connectorcontract.AnthropicDefaultMaxTokensProvider) (int64, error) {
	if provider == nil {
		return BuiltInDefaultMaxTokens, nil
	}
	raw, err := provider.RawAnthropicDefaultMaxTokens(ctx)
	if err != nil {
		return 0, ErrDefaultTokens
	}
	if raw == nil {
		return BuiltInDefaultMaxTokens, nil
	}
	if *raw < 1 || *raw > MaxMaxTokens {
		return 0, ErrDefaultTokens
	}
	return *raw, nil
}

func compileRequest(request *openai.ChatRequest, upstreamModel, safetyIdentifier string, defaultMaxTokens int64) ([]byte, error) {
	return compileRequestWithDefaultResolver(request, upstreamModel, safetyIdentifier, func() (int64, error) {
		if defaultMaxTokens < 1 || defaultMaxTokens > MaxMaxTokens {
			return 0, ErrInvalidRequest
		}
		return defaultMaxTokens, nil
	})
}

func compileRequestWithDefaultResolver(request *openai.ChatRequest, upstreamModel, safetyIdentifier string, resolve func() (int64, error)) ([]byte, error) {
	if request == nil || !validOpaqueRunes(upstreamModel, openai.MaxUpstreamModelRunes, true) || safetyIdentifier == "" || resolve == nil {
		return nil, ErrInvalidRequest
	}
	fields := request.Requirements().TopLevelFields()
	allowed := map[string]bool{
		"model": true, "messages": true, "max_tokens": true, "max_completion_tokens": true,
		"stream": true, "temperature": true, "top_p": true, "stop": true,
		"tools": true, "tool_choice": true, "parallel_tool_calls": true,
		"stream_options": true, "safety_identifier": true,
	}
	raw := make(map[string][]byte, len(fields))
	defer func() {
		for name, value := range raw {
			clear(value)
			delete(raw, name)
		}
		clear(fields)
	}()
	for _, name := range fields {
		if !allowed[name] {
			return nil, ErrInvalidRequest
		}
		value, ok := request.RawField(name)
		if !ok || validateJSON(value) != nil {
			clear(value)
			return nil, ErrInvalidRequest
		}
		raw[name] = value
	}
	if _, ok := raw["model"]; !ok {
		return nil, ErrInvalidRequest
	}

	maxTokens, err := parseMaxTokensWithDefaultResolver(raw["max_tokens"], raw["max_completion_tokens"], resolve)
	if err != nil {
		return nil, err
	}
	stream, err := parseNullableBool(raw["stream"], false)
	if err != nil {
		return nil, err
	}
	if err := validateStreamOptions(raw["stream_options"]); err != nil {
		return nil, err
	}
	temperature, err := parseUnitNumber(raw["temperature"])
	if err != nil {
		return nil, err
	}
	topP, err := parseUnitNumber(raw["top_p"])
	if err != nil {
		clear(temperature)
		return nil, err
	}
	stop, err := parseStop(raw["stop"])
	if err != nil {
		clear(temperature)
		clear(topP)
		return nil, err
	}

	tools, toolNames, err := parseTools(raw["tools"])
	if err != nil {
		clear(temperature)
		clear(topP)
		return nil, err
	}
	choice, omitTools, err := parseToolChoice(raw["tool_choice"], toolNames)
	if err != nil {
		clear(temperature)
		clear(topP)
		clearTools(tools)
		return nil, err
	}
	parallel, parallelPresent, err := parseOptionalBool(raw["parallel_tool_calls"])
	if err != nil {
		clear(temperature)
		clear(topP)
		clearTools(tools)
		return nil, ErrInvalidRequest
	}
	if parallelPresent && !parallel && len(tools) > 0 && !omitTools {
		if choice == nil {
			choice = &outboundChoice{Type: "auto"}
		}
		choice.DisableParallelToolUse = true
	}
	// OpenAI's auto choice is a no-op when no tools are declared. Omitting the
	// Anthropic field preserves that meaning and avoids manufacturing a request
	// which an upstream must reject.
	if len(tools) == 0 && choice != nil && choice.Type == "auto" {
		choice = nil
	}
	if omitTools {
		clearTools(tools)
		tools = nil
		choice = nil
	}

	messages, system, err := parseMessages(raw["messages"])
	if err != nil {
		clear(temperature)
		clear(topP)
		clearTools(tools)
		return nil, err
	}
	out := outboundRequest{
		Model: upstreamModel, MaxTokens: maxTokens, Messages: messages, System: system,
		Tools: tools, ToolChoice: choice, Metadata: outboundMetadata{UserID: safetyIdentifier},
		Stream: stream, Temperature: temperature, TopP: topP, StopSequences: stop,
	}
	body, err := marshalJSONNoEscapeLimited(out, MaxTranslatedRequestBytes)
	clear(temperature)
	clear(topP)
	clearTools(tools)
	clearMessages(messages)
	if err != nil {
		clear(body)
		return nil, ErrInvalidRequest
	}
	return body, nil
}

func parseMaxTokens(first, second []byte, fallback int64) (int64, error) {
	return parseMaxTokensWithDefaultResolver(first, second, func() (int64, error) {
		if fallback < 1 || fallback > MaxMaxTokens {
			return 0, ErrInvalidRequest
		}
		return fallback, nil
	})
}

func parseMaxTokensWithDefaultResolver(first, second []byte, resolve func() (int64, error)) (int64, error) {
	a, aSet, err := parseOptionalPositiveInt(first)
	if err != nil {
		return 0, err
	}
	b, bSet, err := parseOptionalPositiveInt(second)
	if err != nil {
		return 0, err
	}
	switch {
	case aSet && bSet && a != b:
		return 0, ErrInvalidRequest
	case aSet:
		return a, nil
	case bSet:
		return b, nil
	default:
		if resolve == nil {
			return 0, ErrInvalidRequest
		}
		fallback, err := resolve()
		if err != nil {
			return 0, err
		}
		if fallback < 1 || fallback > MaxMaxTokens {
			return 0, ErrInvalidRequest
		}
		return fallback, nil
	}
}

func parseOptionalPositiveInt(raw []byte) (int64, bool, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return 0, false, nil
	}
	var value int64
	if json.Unmarshal(trimmed, &value) != nil || value < 1 || value > MaxMaxTokens {
		return 0, false, ErrInvalidRequest
	}
	return value, true, nil
}

func parseNullableBool(raw []byte, fallback bool) (bool, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return fallback, nil
	}
	var value bool
	if json.Unmarshal(trimmed, &value) != nil {
		return false, ErrInvalidRequest
	}
	return value, nil
}

func parseOptionalBool(raw []byte) (bool, bool, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return false, false, nil
	}
	var value bool
	if json.Unmarshal(trimmed, &value) != nil {
		return false, false, ErrInvalidRequest
	}
	return value, true, nil
}

func validateStreamOptions(raw []byte) error {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil
	}
	object, err := strictObject(trimmed)
	if err != nil || len(object) > 1 {
		return ErrInvalidRequest
	}
	for key, value := range object {
		if key != "include_usage" {
			return ErrInvalidRequest
		}
		if _, _, err := parseOptionalBool(value); err != nil || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return ErrInvalidRequest
		}
	}
	return nil
}

func parseUnitNumber(raw []byte) (json.RawMessage, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, nil
	}
	if trimmed[0] != '-' && (trimmed[0] < '0' || trimmed[0] > '9') {
		return nil, ErrInvalidRequest
	}
	var number json.Number
	if json.Unmarshal(trimmed, &number) != nil || !unitDecimalInRange(number.String()) {
		return nil, ErrInvalidRequest
	}
	return append(json.RawMessage(nil), trimmed...), nil
}

// unitDecimalInRange compares a valid JSON number with [0, 1] without first
// rounding it to binary floating point. That distinction matters at the two
// boundaries: 1.0000000000000000001 and -1e-10000 are both out of range even
// though a float64 conversion can round them to 1 and negative zero.
func unitDecimalInRange(value string) bool {
	if value == "" {
		return false
	}
	negative := value[0] == '-'
	if negative {
		value = value[1:]
	}
	exponent := ""
	if index := strings.IndexAny(value, "eE"); index >= 0 {
		exponent = value[index+1:]
		value = value[:index]
	}
	integerDigits := len(value)
	if point := strings.IndexByte(value, '.'); point >= 0 {
		integerDigits = point
		value = value[:point] + value[point+1:]
	}
	firstNonZero := 0
	for firstNonZero < len(value) && value[firstNonZero] == '0' {
		firstNonZero++
	}
	if firstNonZero == len(value) {
		return true
	}
	if negative {
		return false
	}
	var exponentValue int64
	if exponent != "" {
		parsed, err := strconv.ParseInt(exponent, 10, 64)
		if err != nil {
			return exponent[0] == '-'
		}
		exponentValue = parsed
	}
	// Compare exponentValue+(integerDigits-firstNonZero) with 1 without
	// performing an addition that a deliberately huge exponent could overflow.
	boundaryExponent := int64(1 - (integerDigits - firstNonZero))
	if exponentValue < boundaryExponent {
		return true
	}
	if exponentValue > boundaryExponent {
		return false
	}
	significant := value[firstNonZero:]
	if significant[0] != '1' {
		return false
	}
	for index := 1; index < len(significant); index++ {
		if significant[index] != '0' {
			return false
		}
	}
	return true
}

func parseStop(raw []byte) ([]string, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, nil
	}
	var one string
	if json.Unmarshal(trimmed, &one) == nil {
		if !validStop(one) {
			return nil, ErrInvalidRequest
		}
		return []string{one}, nil
	}
	var many []string
	if json.Unmarshal(trimmed, &many) != nil || len(many) < 1 || len(many) > maxStopSequences {
		return nil, ErrInvalidRequest
	}
	for _, value := range many {
		if !validStop(value) {
			return nil, ErrInvalidRequest
		}
	}
	return many, nil
}

func validStop(value string) bool {
	return value != "" && utf8.ValidString(value) && len([]byte(value)) <= maxStopBytes
}

func parseTools(raw []byte) ([]outboundTool, map[string]struct{}, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, map[string]struct{}{}, nil
	}
	var entries []json.RawMessage
	if json.Unmarshal(trimmed, &entries) != nil || len(entries) > maxTools {
		return nil, nil, ErrInvalidRequest
	}
	names := make(map[string]struct{}, len(entries))
	tools := make([]outboundTool, 0, len(entries))
	for _, entry := range entries {
		object, err := strictObject(entry)
		if err != nil || !onlyKeys(object, "type", "function") {
			clearTools(tools)
			return nil, nil, ErrInvalidRequest
		}
		var kind string
		if json.Unmarshal(object["type"], &kind) != nil || kind != "function" {
			clearTools(tools)
			return nil, nil, ErrInvalidRequest
		}
		function, err := strictObject(object["function"])
		if err != nil || !onlyKeys(function, "name", "description", "parameters") {
			clearTools(tools)
			return nil, nil, ErrInvalidRequest
		}
		var name, description string
		if json.Unmarshal(function["name"], &name) != nil || !functionNameRegexp.MatchString(name) {
			clearTools(tools)
			return nil, nil, ErrInvalidRequest
		}
		if _, duplicate := names[name]; duplicate {
			clearTools(tools)
			return nil, nil, ErrInvalidRequest
		}
		if value, ok := function["description"]; ok {
			if json.Unmarshal(value, &description) != nil || !utf8.ValidString(description) || len([]byte(description)) > maxDescriptionBytes {
				clearTools(tools)
				return nil, nil, ErrInvalidRequest
			}
		}
		parameters, err := strictObject(function["parameters"])
		if err != nil {
			clearTools(tools)
			return nil, nil, ErrInvalidRequest
		}
		_ = parameters
		names[name] = struct{}{}
		tools = append(tools, outboundTool{Name: name, Description: description, InputSchema: append(json.RawMessage(nil), function["parameters"]...)})
	}
	return tools, names, nil
}

func parseToolChoice(raw []byte, names map[string]struct{}) (*outboundChoice, bool, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, false, nil
	}
	var text string
	if json.Unmarshal(trimmed, &text) == nil {
		switch text {
		case "none":
			return nil, true, nil
		case "auto":
			return &outboundChoice{Type: "auto"}, false, nil
		case "required":
			if len(names) == 0 {
				return nil, false, ErrInvalidRequest
			}
			return &outboundChoice{Type: "any"}, false, nil
		default:
			return nil, false, ErrInvalidRequest
		}
	}
	object, err := strictObject(trimmed)
	if err != nil || !onlyKeys(object, "type", "function") {
		return nil, false, ErrInvalidRequest
	}
	var kind string
	if json.Unmarshal(object["type"], &kind) != nil || kind != "function" {
		return nil, false, ErrInvalidRequest
	}
	function, err := strictObject(object["function"])
	if err != nil || !onlyKeys(function, "name") {
		return nil, false, ErrInvalidRequest
	}
	var name string
	if json.Unmarshal(function["name"], &name) != nil {
		return nil, false, ErrInvalidRequest
	}
	if _, ok := names[name]; !ok {
		return nil, false, ErrInvalidRequest
	}
	return &outboundChoice{Type: "tool", Name: name}, false, nil
}

func parseMessages(raw []byte) ([]outboundMessage, string, error) {
	trimmed := bytes.TrimSpace(raw)
	var entries []json.RawMessage
	if len(trimmed) == 0 || json.Unmarshal(trimmed, &entries) != nil || len(entries) == 0 || len(entries) > maxMessages {
		return nil, "", ErrInvalidRequest
	}
	messages := make([]outboundMessage, 0, len(entries))
	systemParts := make([]string, 0)
	pending := make(map[string]struct{})
	usedToolIDs := make(map[string]struct{})
	for _, entry := range entries {
		object, err := strictObject(entry)
		if err != nil {
			clearMessages(messages)
			return nil, "", ErrInvalidRequest
		}
		var role string
		if json.Unmarshal(object["role"], &role) != nil {
			clearMessages(messages)
			return nil, "", ErrInvalidRequest
		}
		switch role {
		case "system", "developer":
			if !onlyKeys(object, "role", "content") {
				clearMessages(messages)
				return nil, "", ErrInvalidRequest
			}
			parts, err := parseTextContent(object["content"])
			if err != nil {
				clearMessages(messages)
				return nil, "", err
			}
			systemParts = append(systemParts, parts...)
		case "user":
			if len(pending) != 0 || !onlyKeys(object, "role", "content") {
				clearMessages(messages)
				return nil, "", ErrInvalidRequest
			}
			blocks, err := parseUserContent(object["content"])
			if err != nil || len(blocks) == 0 {
				clearMessages(messages)
				return nil, "", ErrInvalidRequest
			}
			appendMessage(&messages, "user", blocks)
		case "assistant":
			if len(pending) != 0 || !onlyKeys(object, "role", "content", "tool_calls") {
				clearMessages(messages)
				return nil, "", ErrInvalidRequest
			}
			blocks, err := parseAssistantContent(object["content"])
			if err != nil {
				clearMessages(messages)
				return nil, "", err
			}
			calls, err := parseAssistantToolCalls(object["tool_calls"], pending, usedToolIDs)
			if err != nil {
				clearMessages(messages)
				return nil, "", err
			}
			blocks = append(blocks, calls...)
			if len(blocks) == 0 {
				clearMessages(messages)
				return nil, "", ErrInvalidRequest
			}
			appendMessage(&messages, "assistant", blocks)
		case "tool":
			if !onlyKeys(object, "role", "content", "tool_call_id") {
				clearMessages(messages)
				return nil, "", ErrInvalidRequest
			}
			var id string
			if json.Unmarshal(object["tool_call_id"], &id) != nil || !validToolID(id) {
				clearMessages(messages)
				return nil, "", ErrInvalidRequest
			}
			if _, ok := pending[id]; !ok {
				clearMessages(messages)
				return nil, "", ErrInvalidRequest
			}
			content, err := parseToolResultContent(object["content"])
			if err != nil {
				clearMessages(messages)
				return nil, "", err
			}
			delete(pending, id)
			appendMessage(&messages, "user", []outboundBlock{{Type: "tool_result", ToolUseID: id, Content: content}})
		default:
			clearMessages(messages)
			return nil, "", ErrInvalidRequest
		}
	}
	if len(pending) != 0 || len(messages) == 0 {
		clearMessages(messages)
		return nil, "", ErrInvalidRequest
	}
	return messages, strings.Join(systemParts, "\n"), nil
}

func parseTextContent(raw []byte) ([]string, error) {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return []string{text}, nil
	}
	var entries []json.RawMessage
	if json.Unmarshal(raw, &entries) != nil || len(entries) == 0 {
		return nil, ErrInvalidRequest
	}
	out := make([]string, 0, len(entries))
	for _, entry := range entries {
		object, err := strictObject(entry)
		if err != nil || !onlyKeys(object, "type", "text") {
			return nil, ErrInvalidRequest
		}
		var kind, value string
		if json.Unmarshal(object["type"], &kind) != nil || kind != "text" || json.Unmarshal(object["text"], &value) != nil {
			return nil, ErrInvalidRequest
		}
		out = append(out, value)
	}
	return out, nil
}

func parseUserContent(raw []byte) ([]outboundBlock, error) {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return []outboundBlock{newTextBlock(text)}, nil
	}
	var entries []json.RawMessage
	if json.Unmarshal(raw, &entries) != nil || len(entries) == 0 {
		return nil, ErrInvalidRequest
	}
	out := make([]outboundBlock, 0, len(entries))
	for _, entry := range entries {
		object, err := strictObject(entry)
		if err != nil {
			return nil, ErrInvalidRequest
		}
		var kind string
		if json.Unmarshal(object["type"], &kind) != nil {
			return nil, ErrInvalidRequest
		}
		switch kind {
		case "text":
			if !onlyKeys(object, "type", "text") {
				return nil, ErrInvalidRequest
			}
			var value string
			if json.Unmarshal(object["text"], &value) != nil {
				return nil, ErrInvalidRequest
			}
			out = append(out, newTextBlock(value))
		case "image_url":
			if !onlyKeys(object, "type", "image_url") {
				return nil, ErrInvalidRequest
			}
			source, err := parseImage(object["image_url"])
			if err != nil {
				return nil, err
			}
			out = append(out, outboundBlock{Type: "image", Source: source})
		default:
			return nil, ErrInvalidRequest
		}
	}
	return out, nil
}

func parseAssistantContent(raw []byte) ([]outboundBlock, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, nil
	}
	parts, err := parseTextContent(raw)
	if err != nil {
		return nil, err
	}
	out := make([]outboundBlock, 0, len(parts))
	for _, text := range parts {
		out = append(out, newTextBlock(text))
	}
	return out, nil
}

func parseAssistantToolCalls(raw []byte, pending, used map[string]struct{}) ([]outboundBlock, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, nil
	}
	var entries []json.RawMessage
	if json.Unmarshal(trimmed, &entries) != nil || len(entries) == 0 || len(entries) > maxTools {
		return nil, ErrInvalidRequest
	}
	out := make([]outboundBlock, 0, len(entries))
	for _, entry := range entries {
		object, err := strictObject(entry)
		if err != nil || !onlyKeys(object, "id", "type", "function") {
			return nil, ErrInvalidRequest
		}
		var id, kind string
		if json.Unmarshal(object["id"], &id) != nil || !validToolID(id) || json.Unmarshal(object["type"], &kind) != nil || kind != "function" {
			return nil, ErrInvalidRequest
		}
		if _, duplicate := used[id]; duplicate {
			return nil, ErrInvalidRequest
		}
		function, err := strictObject(object["function"])
		if err != nil || !onlyKeys(function, "name", "arguments") {
			return nil, ErrInvalidRequest
		}
		var name, arguments string
		if json.Unmarshal(function["name"], &name) != nil || !functionNameRegexp.MatchString(name) || json.Unmarshal(function["arguments"], &arguments) != nil {
			return nil, ErrInvalidRequest
		}
		input := []byte(arguments)
		if _, err := strictObject(input); err != nil {
			clear(input)
			return nil, ErrInvalidRequest
		}
		pending[id] = struct{}{}
		used[id] = struct{}{}
		out = append(out, outboundBlock{Type: "tool_use", ID: id, Name: name, Input: append(json.RawMessage(nil), input...)})
		clear(input)
	}
	return out, nil
}

func parseToolResultContent(raw []byte) (any, error) {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text, nil
	}
	parts, err := parseTextContent(raw)
	if err != nil {
		return nil, err
	}
	blocks := make([]outboundBlock, 0, len(parts))
	for _, value := range parts {
		blocks = append(blocks, newTextBlock(value))
	}
	return blocks, nil
}

func parseImage(raw []byte) (*imageSource, error) {
	var rawURL string
	if json.Unmarshal(raw, &rawURL) != nil {
		object, err := strictObject(raw)
		if err != nil || !onlyKeys(object, "url") || json.Unmarshal(object["url"], &rawURL) != nil {
			return nil, ErrInvalidRequest
		}
	}
	if !utf8.ValidString(rawURL) {
		return nil, ErrInvalidRequest
	}
	parsed, err := url.Parse(rawURL)
	if err == nil && parsed.Scheme == "https" && parsed.Host != "" && parsed.User == nil {
		if len([]byte(rawURL)) > maxImageURLBytes {
			return nil, ErrInvalidRequest
		}
		return &imageSource{Type: "url", URL: rawURL}, nil
	}
	const prefix = "data:image/"
	if !strings.HasPrefix(rawURL, prefix) {
		return nil, ErrInvalidRequest
	}
	mediaAndData := strings.TrimPrefix(rawURL, prefix)
	media, encoded, ok := strings.Cut(mediaAndData, ";base64,")
	if !ok {
		return nil, ErrInvalidRequest
	}
	mediaTypes := map[string]string{"jpeg": "image/jpeg", "png": "image/png", "gif": "image/gif", "webp": "image/webp"}
	mediaType, ok := mediaTypes[media]
	if !ok || encoded == "" {
		return nil, ErrInvalidRequest
	}
	decoded, err := base64.StdEncoding.Strict().DecodeString(encoded)
	if err != nil || len(decoded) == 0 {
		clear(decoded)
		return nil, ErrInvalidRequest
	}
	clear(decoded)
	return &imageSource{Type: "base64", MediaType: mediaType, Data: encoded}, nil
}

func appendMessage(messages *[]outboundMessage, role string, blocks []outboundBlock) {
	if len(*messages) > 0 && (*messages)[len(*messages)-1].Role == role {
		(*messages)[len(*messages)-1].Content = append((*messages)[len(*messages)-1].Content, blocks...)
		return
	}
	*messages = append(*messages, outboundMessage{Role: role, Content: blocks})
}

func validToolID(value string) bool { return validOpaque(value, maxToolIDBytes, false) }

func validOpaque(value string, max int, rejectEdgeSpace bool) bool {
	if value == "" || !utf8.ValidString(value) || len([]byte(value)) > max || (rejectEdgeSpace && strings.TrimSpace(value) != value) {
		return false
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f || r == utf8.RuneError {
			return false
		}
	}
	return true
}

func onlyKeys(object map[string]json.RawMessage, allowed ...string) bool {
	if object == nil {
		return false
	}
	set := make(map[string]struct{}, len(allowed))
	for _, name := range allowed {
		set[name] = struct{}{}
	}
	for name := range object {
		if _, ok := set[name]; !ok {
			return false
		}
	}
	return true
}

func strictObject(raw []byte) (map[string]json.RawMessage, error) {
	if validateJSON(raw) != nil || len(bytes.TrimSpace(raw)) == 0 || bytes.TrimSpace(raw)[0] != '{' {
		return nil, ErrInvalidRequest
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(raw, &object) != nil || object == nil {
		return nil, ErrInvalidRequest
	}
	return object, nil
}

func validateJSON(raw []byte) error {
	if len(raw) == 0 || !utf8.Valid(raw) || validateJSONSurrogateEscapes(raw) != nil {
		return ErrInvalidRequest
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	fields := 0
	if err := validateJSONValue(decoder, 0, &fields); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return ErrInvalidRequest
	}
	return nil
}

// encoding/json replaces an unpaired UTF-16 surrogate escape with U+FFFD.
// Reject those malformed escapes lexically so an upstream or caller cannot
// smuggle a lossy identifier through validation. A literal U+FFFD and a
// properly paired high/low surrogate remain valid JSON.
func validateJSONSurrogateEscapes(raw []byte) error {
	inString := false
	for index := 0; index < len(raw); index++ {
		switch raw[index] {
		case '"':
			inString = !inString
		case '\\':
			if !inString || index+1 >= len(raw) {
				continue
			}
			if raw[index+1] != 'u' {
				index++
				continue
			}
			first, ok := parseHexCodeUnit(raw, index+2)
			if !ok {
				return ErrInvalidRequest
			}
			switch {
			case first >= 0xd800 && first <= 0xdbff:
				if index+7 >= len(raw) || raw[index+6] != '\\' || raw[index+7] != 'u' {
					return ErrInvalidRequest
				}
				second, ok := parseHexCodeUnit(raw, index+8)
				if !ok || second < 0xdc00 || second > 0xdfff {
					return ErrInvalidRequest
				}
				index += 11
			case first >= 0xdc00 && first <= 0xdfff:
				return ErrInvalidRequest
			default:
				index += 5
			}
		}
	}
	return nil
}

func parseHexCodeUnit(raw []byte, start int) (uint16, bool) {
	if start < 0 || start+4 > len(raw) {
		return 0, false
	}
	var value uint16
	for _, current := range raw[start : start+4] {
		value <<= 4
		switch {
		case current >= '0' && current <= '9':
			value |= uint16(current - '0')
		case current >= 'a' && current <= 'f':
			value |= uint16(current-'a') + 10
		case current >= 'A' && current <= 'F':
			value |= uint16(current-'A') + 10
		default:
			return 0, false
		}
	}
	return value, true
}

func validateJSONValue(decoder *json.Decoder, depth int, fields *int) error {
	if depth > maxJSONDepth {
		return ErrInvalidRequest
	}
	token, err := decoder.Token()
	if err != nil {
		return ErrInvalidRequest
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			key, ok := keyToken.(string)
			if err != nil || !ok {
				return ErrInvalidRequest
			}
			if _, duplicate := seen[key]; duplicate {
				return ErrInvalidRequest
			}
			seen[key] = struct{}{}
			*fields++
			if *fields > maxJSONFields || validateJSONValue(decoder, depth+1, fields) != nil {
				return ErrInvalidRequest
			}
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim('}') {
			return ErrInvalidRequest
		}
	case '[':
		for decoder.More() {
			if validateJSONValue(decoder, depth+1, fields) != nil {
				return ErrInvalidRequest
			}
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim(']') {
			return ErrInvalidRequest
		}
	default:
		return ErrInvalidRequest
	}
	return nil
}

func clearTools(tools []outboundTool) {
	for i := range tools {
		clear(tools[i].InputSchema)
		tools[i].InputSchema = nil
	}
}

func clearMessages(messages []outboundMessage) {
	for i := range messages {
		for j := range messages[i].Content {
			clear(messages[i].Content[j].Input)
			messages[i].Content[j].Input = nil
			messages[i].Content[j].Text = nil
		}
		clear(messages[i].Content)
		messages[i].Content = nil
	}
	clear(messages)
}
