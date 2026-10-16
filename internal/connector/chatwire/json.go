package chatwire

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"math"
	"unicode/utf8"

	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
)

var ErrProtocol = errors.New("upstream protocol was invalid")

func Object(raw []byte) (map[string]json.RawMessage, error) {
	if len(raw) == 0 || len(raw) > 64<<20 || !utf8.Valid(raw) {
		return nil, ErrProtocol
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := jsonValue(decoder, 0); err != nil {
		return nil, ErrProtocol
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return nil, ErrProtocol
	}
	var value map[string]json.RawMessage
	if json.Unmarshal(raw, &value) != nil || value == nil {
		return nil, ErrProtocol
	}
	return value, nil
}
func jsonValue(decoder *json.Decoder, depth int) error {
	if depth > 64 {
		return ErrProtocol
	}
	token, err := decoder.Token()
	if err != nil {
		return ErrProtocol
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delimiter {
	case '{':
		seen := map[string]bool{}
		for decoder.More() {
			key, err := decoder.Token()
			name, ok := key.(string)
			if err != nil || !ok || seen[name] || len(seen) >= 1024 {
				return ErrProtocol
			}
			seen[name] = true
			if err := jsonValue(decoder, depth+1); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim('}') {
			return ErrProtocol
		}
	case '[':
		for decoder.More() {
			if err := jsonValue(decoder, depth+1); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim(']') {
			return ErrProtocol
		}
	default:
		return ErrProtocol
	}
	return nil
}
func String(raw []byte) (string, bool) {
	var value string
	if len(raw) == 0 || raw[0] != '"' || json.Unmarshal(raw, &value) != nil {
		return "", false
	}
	return value, true
}
func Integer(raw []byte) (int64, bool) {
	var value int64
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) || json.Unmarshal(raw, &value) != nil || value < 0 {
		return 0, false
	}
	return value, true
}
func Number(raw []byte) (float64, bool) {
	var value float64
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) || json.Unmarshal(raw, &value) != nil || math.IsInf(value, 0) || math.IsNaN(value) {
		return 0, false
	}
	return value, true
}
func Null(raw []byte) bool { return len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) }
func Keys(object map[string]json.RawMessage, names ...string) bool {
	for key := range object {
		found := false
		for _, name := range names {
			if key == name {
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
func UsageJSON(usage contract.Usage) any {
	if !usage.Present {
		return nil
	}
	prompt := usage.UncachedInputTokens + usage.CacheReadInputTokens + usage.CacheWriteInputTokens
	return map[string]any{"prompt_tokens": prompt, "completion_tokens": usage.OutputTokens, "total_tokens": prompt + usage.OutputTokens, "prompt_tokens_details": map[string]int64{"cached_tokens": usage.CacheReadInputTokens, "cache_write_tokens": usage.CacheWriteInputTokens}}
}
func Chunk(id, model string, created int64, delta map[string]any, finish string, usage contract.Usage) ([]byte, error) {
	choices := []any{}
	if delta != nil || finish != "" {
		var reason any
		if finish != "" {
			reason = finish
		}
		if delta == nil {
			delta = map[string]any{}
		}
		choices = append(choices, map[string]any{"index": 0, "delta": delta, "finish_reason": reason})
	}
	object := map[string]any{"id": id, "object": "chat.completion.chunk", "created": created, "model": model, "choices": choices}
	if usage.Present {
		object["usage"] = UsageJSON(usage)
	}
	return json.Marshal(object)
}
func Completion(id, model string, created int64, message map[string]any, finish string, usage contract.Usage) ([]byte, error) {
	object := map[string]any{"id": id, "object": "chat.completion", "created": created, "model": model, "choices": []any{map[string]any{"index": 0, "message": message, "finish_reason": finish}}}
	if usage.Present {
		object["usage"] = UsageJSON(usage)
	}
	return json.Marshal(object)
}
func Rejection(field string) error {
	return &contract.RequestRejection{Stage: "protocol preflight", Field: field, Reason: "field cannot be represented by this connector"}
}
