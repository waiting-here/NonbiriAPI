package gateway

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"math"
	"strings"

	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
)

type nativeUsage struct {
	Input struct {
		Total      *int64 `json:"total"`
		NoCache    *int64 `json:"noCache"`
		CacheRead  *int64 `json:"cacheRead"`
		CacheWrite *int64 `json:"cacheWrite"`
	} `json:"inputTokens"`
	Output struct {
		Total     *int64 `json:"total"`
		Text      *int64 `json:"text"`
		Reasoning *int64 `json:"reasoning"`
	} `json:"outputTokens"`
}

func parseUsage(raw []byte) (contract.Usage, error) {
	var out contract.Usage
	if len(raw) == 0 || isNull(raw) {
		return out, nil
	}
	var value nativeUsage
	if json.Unmarshal(raw, &value) != nil {
		return out, errResponse
	}
	for _, n := range []*int64{value.Input.Total, value.Input.NoCache, value.Input.CacheRead, value.Input.CacheWrite, value.Output.Total, value.Output.Text, value.Output.Reasoning} {
		if n != nil && *n < 0 {
			return out, errResponse
		}
	}
	values := []*int64{value.Input.NoCache, value.Input.CacheRead, value.Input.CacheWrite}
	sum, missing := int64(0), 0
	for _, n := range values {
		if n == nil {
			missing++
			continue
		}
		if *n > math.MaxInt64-sum {
			return out, errResponse
		}
		sum += *n
	}
	if value.Input.Total != nil {
		if sum > *value.Input.Total {
			return out, errResponse
		}
		remaining := *value.Input.Total - sum
		if missing == 0 && remaining != 0 {
			return out, errResponse
		}
		if missing == 1 || remaining == 0 {
			for i, n := range values {
				if n == nil {
					n := remaining
					values[i] = &n
				}
			}
			missing = 0
		}
	}
	if value.Output.Total != nil {
		outputKnown := int64(0)
		for _, n := range []*int64{value.Output.Text, value.Output.Reasoning} {
			if n != nil {
				if *n > math.MaxInt64-outputKnown {
					return out, errResponse
				}
				outputKnown += *n
			}
		}
		if outputKnown > *value.Output.Total {
			return out, errResponse
		}
	}
	if missing != 0 || value.Output.Total == nil {
		return out, nil
	}
	out = contract.Usage{UncachedInputTokens: *values[0], CacheReadInputTokens: *values[1], CacheWriteInputTokens: *values[2], OutputTokens: *value.Output.Total, Present: true}
	if _, ok := usageTotal(out); !ok {
		return contract.Usage{}, errResponse
	}
	return out, nil
}

func usageTotal(value contract.Usage) (int64, bool) {
	total := int64(0)
	for _, n := range []int64{value.UncachedInputTokens, value.CacheReadInputTokens, value.CacheWriteInputTokens, value.OutputTokens} {
		if n < 0 || n > math.MaxInt64-total {
			return 0, false
		}
		total += n
	}
	return total, true
}
func callerUsage(value contract.Usage) any {
	if !value.Present {
		return nil
	}
	total, _ := usageTotal(value)
	return map[string]any{"prompt_tokens": total - value.OutputTokens, "completion_tokens": value.OutputTokens, "total_tokens": total,
		"prompt_tokens_details": map[string]int64{"cached_tokens": value.CacheReadInputTokens, "cache_creation_tokens": value.CacheWriteInputTokens}}
}

func finishReason(raw []byte) (string, error) {
	value, err := parseObject(raw)
	reason, ok := text(value["unified"])
	if err != nil || !ok || !only(value, "unified", "raw") {
		return "", errResponse
	}
	switch reason {
	case "stop", "length":
		return reason, nil
	case "tool-calls":
		return "tool_calls", nil
	case "content-filter":
		return "content_filter", nil
	default:
		return "", errResponse
	}
}
func warningsOK(raw []byte) bool {
	if len(raw) == 0 {
		return true
	}
	var values []json.RawMessage
	return json.Unmarshal(raw, &values) == nil && values != nil && len(values) == 0
}
func responseID() string {
	var value [12]byte
	_, _ = rand.Read(value[:])
	return "chatcmpl-" + hex.EncodeToString(value[:])
}

type toolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

func parseToolCall(value object) (toolCall, error) {
	var call toolCall
	id, idOK := text(value["toolCallId"])
	name, nameOK := text(value["toolName"])
	input, inputOK := text(value["input"])
	if !idOK || !nameOK || !inputOK || !opaque(id, 512) || !opaque(name, 64) {
		return call, errResponse
	}
	if raw, ok := value["providerExecuted"]; ok && string(raw) != "false" {
		return call, errResponse
	}
	if _, err := parseObject([]byte(input)); err != nil {
		return call, errResponse
	}
	call.ID, call.Type, call.Function.Name, call.Function.Arguments = id, "function", name, input
	return call, nil
}

func translateChat(raw []byte, model string, created int64) ([]byte, contract.Usage, error) {
	root, err := parseObject(raw)
	if err != nil || !warningsOK(root["warnings"]) {
		return nil, contract.Usage{}, errResponse
	}
	reason, err := finishReason(root["finishReason"])
	if err != nil {
		return nil, contract.Usage{}, err
	}
	usage, err := parseUsage(root["usage"])
	if err != nil {
		return nil, usage, err
	}
	var content []json.RawMessage
	if json.Unmarshal(root["content"], &content) != nil || len(content) == 0 || len(content) > 4096 {
		return nil, usage, errResponse
	}
	var textContent, reasoningContent strings.Builder
	calls := []toolCall{}
	seen := map[string]bool{}
	for _, raw := range content {
		part, err := parseObject(raw)
		kind, ok := text(part["type"])
		if err != nil || !ok {
			return nil, usage, errResponse
		}
		switch kind {
		case "text", "reasoning":
			value, ok := text(part["text"])
			if !ok {
				return nil, usage, errResponse
			}
			if kind == "reasoning" {
				reasoningContent.WriteString(value)
			} else {
				textContent.WriteString(value)
			}
		case "tool-call":
			call, err := parseToolCall(part)
			if err != nil || seen[call.ID] || len(calls) >= 128 {
				return nil, usage, errResponse
			}
			seen[call.ID] = true
			calls = append(calls, call)
		default:
			return nil, usage, errResponse
		}
	}
	if (reason == "tool_calls") != (len(calls) > 0) {
		return nil, usage, errResponse
	}
	message := map[string]any{"role": "assistant", "content": textContent.String()}
	if reasoningContent.Len() != 0 {
		message["reasoning_content"] = reasoningContent.String()
	}
	if len(calls) > 0 {
		message["tool_calls"] = calls
		if textContent.Len() == 0 {
			message["content"] = nil
		}
	}
	out := map[string]any{"id": responseID(), "object": "chat.completion", "created": created, "model": model, "choices": []any{map[string]any{"index": 0, "message": message, "finish_reason": reason}}}
	if usage.Present {
		out["usage"] = callerUsage(usage)
	}
	body, err := json.Marshal(out)
	return body, usage, err
}

func translateEmbedding(raw []byte, model string, count int) ([]byte, contract.Usage, error) {
	root, err := parseObject(raw)
	if err != nil {
		return nil, contract.Usage{}, errResponse
	}
	var vectors [][]json.RawMessage
	if json.Unmarshal(root["embeddings"], &vectors) != nil || len(vectors) != count {
		return nil, contract.Usage{}, errResponse
	}
	dimensions := 0
	data := make([]any, len(vectors))
	for i, vector := range vectors {
		if len(vector) == 0 || len(vector) > 65536 || dimensions != 0 && dimensions != len(vector) {
			return nil, contract.Usage{}, errResponse
		}
		dimensions = len(vector)
		converted := make([]float64, len(vector))
		for j, rawNumber := range vector {
			var n float64
			if isNull(rawNumber) || json.Unmarshal(rawNumber, &n) != nil || math.IsNaN(n) || math.IsInf(n, 0) {
				return nil, contract.Usage{}, errResponse
			}
			converted[j] = n
		}
		data[i] = map[string]any{"object": "embedding", "index": i, "embedding": converted}
	}
	usage := contract.Usage{}
	if raw, present := root["usage"]; present && !isNull(raw) {
		var value struct {
			Tokens *int64 `json:"tokens"`
		}
		if json.Unmarshal(raw, &value) != nil || value.Tokens != nil && *value.Tokens < 0 {
			return nil, usage, errResponse
		}
		if value.Tokens != nil {
			usage.Present = true
			usage.UncachedInputTokens = *value.Tokens
		}
	}
	out := map[string]any{"object": "list", "model": model, "data": data}
	if usage.Present {
		out["usage"] = map[string]int64{"prompt_tokens": usage.UncachedInputTokens, "total_tokens": usage.UncachedInputTokens}
	}
	body, err := json.Marshal(out)
	return body, usage, err
}
