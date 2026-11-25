package openai

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
)

type chatUsageTrailer int

const (
	ordinaryChatChunk chatUsageTrailer = iota
	separateChatUsage
	duplicateChatUsage
)

type chatFinish struct {
	id, reason, native string
	created            int64
	envelope           [sha256.Size]byte
	hadUsage           bool
}

type chatStreamFinishes map[int64]chatFinish

type chatTrailerProbe struct {
	finishes *chatStreamFinishes
	root     map[string]json.RawMessage
	hasUsage bool
	envelope [sha256.Size]byte
	hashed   bool
	result   chatUsageTrailer
}

// Some Chat servers repeat a finished choice in the final usage frame. Only
// remove the choice when it repeats the same terminal identity and contains
// no payload or metadata that would be lost. The caller validates JSON first.
func (p *chatTrailerProbe) observe(choice map[string]json.RawMessage, onlyChoice bool) {
	if p == nil {
		return
	}
	if len(*p.finishes) == 0 && (choice["finish_reason"] == nil || isJSONNull(choice["finish_reason"])) {
		return
	}
	var index int64
	if !decodeNonNegativeInt(choice["index"], &index) || isJSONNull(choice["index"]) {
		return
	}
	previous, seen := (*p.finishes)[index]
	if seen {
		delete(*p.finishes, index)
	}
	reason, ok := decisionString(choice["finish_reason"], 1, 128)
	if !ok {
		return
	}
	id, ok := decisionString(p.root["id"], 1, maxResponseIDRunes)
	var created int64
	if !ok || !decodeNonNegativeInt(p.root["created"], &created) {
		return
	}
	var native string
	if value := choice["native_finish_reason"]; value != nil && !isJSONNull(value) {
		if native, ok = decisionString(value, 1, 128); !ok {
			return
		}
	}
	current := chatFinish{id: id, created: created, reason: reason, native: native, hadUsage: p.hasUsage}
	if p.hasUsage {
		if !p.hashed {
			p.envelope = chatUsageEnvelope(p.root)
			p.hashed = true
		}
		current.envelope = p.envelope
		if onlyChoice && seen && previous.id == id && previous.created == created && previous.reason == reason && previous.native == native && emptyChatFinishChoice(choice) {
			p.result = separateChatUsage
			if previous.hadUsage && previous.envelope == current.envelope {
				p.result = duplicateChatUsage
			}
		}
	}
	if *p.finishes == nil {
		*p.finishes = make(chatStreamFinishes)
	}
	if len(*p.finishes) < maxChoices {
		(*p.finishes)[index] = current
	}
}

func emptyChatFinishChoice(choice map[string]json.RawMessage) bool {
	for key, raw := range choice {
		switch key {
		case "index", "finish_reason", "native_finish_reason", "delta":
		case "logprobs":
			if !isJSONNull(raw) {
				return false
			}
		default:
			return false
		}
	}
	delta, ok := decisionObject(choice["delta"], "role", "content")
	return ok && (delta["role"] == nil || exactString(delta, "role", "assistant")) &&
		(delta["content"] == nil || isJSONNull(delta["content"]) || bytes.Equal(delta["content"], []byte(`""`)))
}

// Include the complete usage and every top-level extension when deciding
// whether an entire frame is redundant. Additional metadata must reach callers.
func chatUsageEnvelope(root map[string]json.RawMessage) [sha256.Size]byte {
	fields := make(map[string]json.RawMessage, len(root)-1)
	for key, value := range root {
		if key != "choices" {
			fields[key] = value
		}
	}
	raw, _ := json.Marshal(fields)
	defer clear(raw)
	return sha256.Sum256(raw)
}
