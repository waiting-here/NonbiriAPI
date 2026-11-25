package tooltext

import (
	"encoding/json"
	"maps"
	"slices"
)

func (state *StreamChoiceState) accumulateReasoning(delta map[string]json.RawMessage) {
	var continuation map[string]json.RawMessage
	for _, key := range [...]string{"reasoning", "reasoning_details", "thinking_blocks", "encrypted_content", "extra_content"} {
		if raw, ok := delta[key]; ok {
			if continuation == nil {
				continuation = make(map[string]json.RawMessage)
			}
			continuation[key] = raw
		}
	}
	canonical, hasCanonical := delta["reasoning_content"]
	if continuation == nil && len(state.pendingContinuation) == 0 {
		var text string
		if JsonString(canonical, &text) {
			state.pendingReasoning = append(state.pendingReasoning, text...)
		}
		return
	}
	if len(state.pendingReasoning) != 0 {
		// The earlier reasoning-only deltas can be kept together before the
		// first opaque or structured continuation arrives.
		prefix, _ := json.Marshal(string(state.pendingReasoning))
		state.pendingContinuation = append(state.pendingContinuation, map[string]json.RawMessage{"reasoning_content": prefix})
		clear(state.pendingReasoning)
		state.pendingReasoning = state.pendingReasoning[:0]
	}
	if hasCanonical {
		if continuation == nil {
			continuation = make(map[string]json.RawMessage)
		}
		continuation["reasoning_content"] = canonical
	}
	if len(continuation) != 0 {
		state.pendingContinuation = append(state.pendingContinuation, continuation)
	}
}

func (state *StreamChoiceState) clearPendingReasoning() {
	clear(state.pendingReasoning)
	state.pendingReasoning = state.pendingReasoning[:0]
	for _, delta := range state.pendingContinuation {
		for _, raw := range delta {
			clear(raw)
		}
	}
	clear(state.pendingContinuation)
	state.pendingContinuation = state.pendingContinuation[:0]
}

// StreamContinuationFrames returns bounded, unframed JSON deltas for reasoning
// retained during tool buffering. Each choice keeps its source item boundaries,
// including signature-only deltas, without resending tool or usage data.
func StreamContinuationFrames(first map[string]json.RawMessage, states map[int]*StreamChoiceState, maxFrame int, remaining int64) ([][]byte, error) {
	var frames [][]byte
	var root map[string]json.RawMessage
	for _, index := range slices.Sorted(maps.Keys(states)) {
		state := states[index]
		for i, delta := range state.pendingContinuation {
			if root == nil {
				root = make(map[string]json.RawMessage, len(first))
				maps.Copy(root, first)
				delete(root, "usage")
			}
			if i == 0 && !state.roleEmitted {
				role := state.Role
				if role == "" {
					role = "assistant"
				}
				delta = maps.Clone(delta)
				delta["role"], _ = json.Marshal(role)
			}
			choices, err := json.Marshal([]map[string]any{{"index": index, "delta": delta, "finish_reason": nil}})
			if err == nil {
				root["choices"] = choices
				var payload []byte
				payload, err = json.Marshal(root)
				clear(choices)
				if err == nil && len(payload)+8 <= maxFrame && int64(len(payload)+8) <= remaining {
					frames = append(frames, payload)
					remaining -= int64(len(payload) + 8)
					continue
				}
				clear(payload)
			}
			for _, frame := range frames {
				clear(frame)
			}
			return nil, ErrToolFlatten
		}
	}
	return frames, nil
}
