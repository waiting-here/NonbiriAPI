package gemini

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/connector/chatwire"
	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
)

type decoder struct {
	includeUsage                                          bool
	id, model                                             string
	created                                               int64
	terminal, finished, roleSent, usageSeen, usageInvalid bool
	finish, textSignature                                 string
	text, thought                                         strings.Builder
	calls                                                 []any
	callIDs                                               map[string]bool
	usage                                                 contract.Usage
}

func NewDecoder(model string, now time.Time, includeUsage bool) chatwire.Decoder {
	return &decoder{id: fmt.Sprintf("chatcmpl-%x", now.UnixNano()), model: model, created: now.Unix(), callIDs: map[string]bool{}, includeUsage: includeUsage}
}
func (d *decoder) Clear()                { *d = decoder{} }
func (d *decoder) Usage() contract.Usage { return d.usage }
func (d *decoder) JSON(raw []byte) ([]byte, contract.Usage, error) {
	if _, err := d.consume(raw); err != nil {
		return nil, d.usage, err
	}
	if !d.terminal {
		return nil, d.usage, chatwire.ErrProtocol
	}
	message := map[string]any{"role": "assistant", "content": d.text.String()}
	if d.thought.Len() != 0 {
		message["reasoning_content"] = d.thought.String()
	}
	if len(d.calls) > 0 {
		message["tool_calls"] = d.calls
		if d.text.Len() == 0 {
			message["content"] = nil
		}
	}
	if d.textSignature != "" {
		message["extra_content"] = signatureExtra(d.textSignature)
	}
	out, err := chatwire.Completion(d.id, d.model, d.created, message, d.finish, d.usage)
	return out, d.usage, err
}
func (d *decoder) Event(name string, raw []byte) ([][]byte, error) {
	if name != "" && name != "message" || d.finished {
		return nil, chatwire.ErrProtocol
	}
	deltas, err := d.consume(raw)
	if err != nil {
		return nil, err
	}
	var frames [][]byte
	for _, delta := range deltas {
		if !d.roleSent {
			delta["role"] = "assistant"
			d.roleSent = true
		}
		frame, err := chatwire.Chunk(d.id, d.model, d.created, delta, "", contract.Usage{})
		if err != nil {
			return nil, err
		}
		frames = append(frames, frame)
	}
	return frames, nil
}
func (d *decoder) Finish() ([][]byte, error) {
	if !d.terminal || d.finished {
		return nil, chatwire.ErrProtocol
	}
	d.finished = true
	delta := map[string]any{}
	if !d.roleSent {
		delta["role"] = "assistant"
	}
	frame, err := chatwire.Chunk(d.id, d.model, d.created, delta, d.finish, contract.Usage{})
	if err != nil {
		return nil, err
	}
	frames := [][]byte{frame}
	if d.usage.Present && d.includeUsage {
		frame, err = chatwire.Chunk(d.id, d.model, d.created, nil, "", d.usage)
		if err != nil {
			return nil, err
		}
		frames = append(frames, frame)
	}
	return frames, nil
}
func signatureExtra(value string) any {
	return map[string]any{"google": map[string]string{"thought_signature": value}}
}
func nativeSignature(raw []byte) (string, error) {
	if len(raw) == 0 {
		return "", nil
	}
	value, ok := chatwire.String(raw)
	if !ok || value == "" || len(value) > 64<<10 {
		return "", chatwire.ErrProtocol
	}
	decoded, err := base64.StdEncoding.Strict().DecodeString(value)
	clear(decoded)
	if err != nil {
		return "", chatwire.ErrProtocol
	}
	return value, nil
}
func (d *decoder) consume(raw []byte) ([]map[string]any, error) {
	root, err := chatwire.Object(raw)
	if err != nil {
		return nil, err
	}
	if _, ok := root["error"]; ok {
		return nil, chatwire.ErrProtocol
	}
	if usage, ok := root["usageMetadata"]; ok {
		d.observeUsage(usage)
	}
	var candidates []json.RawMessage
	if value, ok := root["candidates"]; ok && json.Unmarshal(value, &candidates) != nil {
		return nil, chatwire.ErrProtocol
	}
	if len(candidates) > 1 {
		return nil, chatwire.ErrProtocol
	}
	if len(candidates) == 0 {
		if feedback, ok := root["promptFeedback"]; ok {
			p, err := chatwire.Object(feedback)
			if err != nil {
				return nil, err
			}
			if reason, ok := chatwire.String(p["blockReason"]); ok && reason != "" && reason != "BLOCK_REASON_UNSPECIFIED" {
				if d.terminal || d.text.Len() != 0 || len(d.calls) > 0 {
					return nil, chatwire.ErrProtocol
				}
				switch reason {
				case "SAFETY", "OTHER", "BLOCKLIST", "PROHIBITED_CONTENT", "IMAGE_SAFETY":
				default:
					return nil, chatwire.ErrProtocol
				}
				d.terminal = true
				d.finish = "content_filter"
				return nil, nil
			}
		}
		if _, ok := root["usageMetadata"]; !ok {
			return nil, chatwire.ErrProtocol
		}
		return nil, nil
	}
	if d.terminal {
		return nil, chatwire.ErrProtocol
	}
	candidate, err := chatwire.Object(candidates[0])
	if err != nil {
		return nil, err
	}
	if index, ok := candidate["index"]; ok {
		value, ok := chatwire.Integer(index)
		if !ok || value != 0 {
			return nil, chatwire.ErrProtocol
		}
	}
	var parts []json.RawMessage
	if !chatwire.Null(candidate["content"]) {
		content, err := chatwire.Object(candidate["content"])
		if err != nil || !chatwire.Keys(content, "role", "parts") {
			return nil, chatwire.ErrProtocol
		}
		if role, ok := content["role"]; ok && string(role) != `"model"` {
			return nil, chatwire.ErrProtocol
		}
		if json.Unmarshal(content["parts"], &parts) != nil {
			return nil, chatwire.ErrProtocol
		}
	}
	deltas := make([]map[string]any, 0, len(parts))
	for _, raw := range parts {
		part, err := chatwire.Object(raw)
		if err != nil || !chatwire.Keys(part, "text", "thought", "thoughtSignature", "functionCall") {
			return nil, chatwire.ErrProtocol
		}
		sig, err := nativeSignature(part["thoughtSignature"])
		if err != nil {
			return nil, err
		}
		thought := false
		if value, ok := part["thought"]; ok && json.Unmarshal(value, &thought) != nil {
			return nil, chatwire.ErrProtocol
		}
		textRaw, hasText := part["text"]
		callRaw, hasCall := part["functionCall"]
		if hasText && hasCall || !hasText && !hasCall && sig == "" {
			return nil, chatwire.ErrProtocol
		}
		if hasCall {
			if thought || len(d.calls) >= 128 {
				return nil, chatwire.ErrProtocol
			}
			call, err := chatwire.Object(callRaw)
			if err != nil || !chatwire.Keys(call, "id", "name", "args") {
				return nil, chatwire.ErrProtocol
			}
			name, ok := chatwire.String(call["name"])
			if !ok || !functionPattern.MatchString(name) {
				return nil, chatwire.ErrProtocol
			}
			args, err := chatwire.Object(call["args"])
			if err != nil {
				return nil, err
			}
			arguments, err := json.Marshal(args)
			if err != nil {
				return nil, err
			}
			id := fmt.Sprintf("call_%x_%d", d.created, len(d.calls))
			if raw, ok := call["id"]; ok {
				id, ok = chatwire.String(raw)
				if !ok || id == "" || len(id) > 128 || strings.ContainsAny(id, "\r\n\x00") {
					return nil, chatwire.ErrProtocol
				}
			}
			if d.callIDs[id] {
				return nil, chatwire.ErrProtocol
			}
			d.callIDs[id] = true
			value := map[string]any{"id": id, "type": "function", "function": map[string]string{"name": name, "arguments": string(arguments)}}
			clear(arguments)
			if sig != "" {
				value["extra_content"] = signatureExtra(sig)
			}
			streamValue := map[string]any{}
			for key, v := range value {
				streamValue[key] = v
			}
			streamValue["index"] = len(d.calls)
			d.calls = append(d.calls, value)
			deltas = append(deltas, map[string]any{"tool_calls": []any{streamValue}})
			continue
		}
		delta := map[string]any{}
		if hasText {
			text, ok := chatwire.String(textRaw)
			if !ok {
				return nil, chatwire.ErrProtocol
			}
			if thought {
				if d.text.Len() != 0 || len(d.calls) > 0 || d.textSignature != "" {
					return nil, chatwire.ErrProtocol
				}
				d.thought.WriteString(text)
				delta["reasoning_content"] = text
			} else {
				if d.textSignature != "" || len(d.calls) > 0 {
					return nil, chatwire.ErrProtocol
				}
				d.text.WriteString(text)
				delta["content"] = text
			}
		}
		if sig != "" {
			if d.textSignature != "" || len(d.calls) > 0 || d.text.Len() == 0 && d.thought.Len() == 0 {
				return nil, chatwire.ErrProtocol
			}
			d.textSignature = sig
			delta["extra_content"] = signatureExtra(sig)
		}
		deltas = append(deltas, delta)
	}
	if raw, ok := candidate["finishReason"]; ok {
		reason, ok := chatwire.String(raw)
		if !ok {
			return nil, chatwire.ErrProtocol
		}
		switch reason {
		case "STOP":
			d.finish = "stop"
			if len(d.calls) > 0 {
				d.finish = "tool_calls"
			}
		case "MAX_TOKENS":
			d.finish = "length"
		case "SAFETY", "RECITATION", "BLOCKLIST", "PROHIBITED_CONTENT", "SPII", "IMAGE_SAFETY", "IMAGE_PROHIBITED_CONTENT":
			d.finish = "content_filter"
		default:
			return nil, chatwire.ErrProtocol
		}
		d.terminal = true
	}
	return deltas, nil
}
func (d *decoder) observeUsage(raw []byte) {
	value, valid := parseUsage(raw)
	if d.usageSeen && value.Present && d.usage.Present && (value.UncachedInputTokens+value.CacheReadInputTokens < d.usage.UncachedInputTokens+d.usage.CacheReadInputTokens || value.OutputTokens < d.usage.OutputTokens || value.CacheReadInputTokens < d.usage.CacheReadInputTokens) {
		valid = false
	}
	d.usageSeen = true
	d.usageInvalid = d.usageInvalid || !valid
	value.TotalMismatch = value.TotalMismatch || d.usage.TotalMismatch
	if d.usageInvalid {
		value = contract.Usage{TotalMismatch: value.TotalMismatch}
	}
	d.usage = value
}
func parseUsage(raw []byte) (contract.Usage, bool) {
	root, err := chatwire.Object(raw)
	if err != nil {
		return contract.Usage{}, false
	}
	counts := map[string]int64{}
	for _, key := range []string{"promptTokenCount", "candidatesTokenCount", "totalTokenCount", "cachedContentTokenCount", "thoughtsTokenCount", "toolUsePromptTokenCount"} {
		value, exists := root[key]
		if !exists {
			if key == "promptTokenCount" || key == "totalTokenCount" {
				return contract.Usage{}, false
			}
			continue
		}
		n, ok := chatwire.Integer(value)
		if !ok {
			return contract.Usage{}, false
		}
		counts[key] = n
	}
	prompt, read, output, thought := counts["promptTokenCount"], counts["cachedContentTokenCount"], counts["candidatesTokenCount"], counts["thoughtsTokenCount"]
	if thought > math.MaxInt64-output || prompt > math.MaxInt64-output-thought {
		return contract.Usage{TotalMismatch: true}, false
	}
	output += thought
	mismatch := prompt+output != counts["totalTokenCount"]
	if read > prompt || counts["toolUsePromptTokenCount"] != 0 {
		return contract.Usage{TotalMismatch: mismatch}, false
	}
	usage := contract.Usage{Present: true, UncachedInputTokens: prompt - read, CacheReadInputTokens: read, OutputTokens: output, TotalMismatch: mismatch}
	normalizeModalities(&usage, root, prompt, counts["candidatesTokenCount"], thought)
	return usage, true
}
