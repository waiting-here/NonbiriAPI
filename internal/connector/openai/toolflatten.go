package openai

import (
	"bytes"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"unicode"
)

const (
	maxFlattenCalls     = 32
	maxFlattenArguments = 64 << 10
	maxFlattenAggregate = 256 << 10
)

var errToolFlatten = errors.New("openai connector: tool flattening failed")
var errFlattenStreamRejected = errors.New("openai connector: flattened stream response rejected")

func jsonString(raw json.RawMessage, dst *string) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) > 0 && trimmed[0] == '"' && json.Unmarshal(trimmed, dst) == nil
}

// flattenCompletion converts only a structurally valid OpenAI completion. It
// returns the original bytes when no choice carries tool_calls and never
// reserializes arguments (the function.arguments string is copied verbatim).
func flattenCompletion(body []byte) ([]byte, error) {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(body, &root); err != nil {
		return nil, errToolFlatten
	}
	var choices []json.RawMessage
	if err := json.Unmarshal(root["choices"], &choices); err != nil {
		return nil, errToolFlatten
	}
	totalCalls, totalArguments := 0, 0
	changed := false
	for i, raw := range choices {
		var choice map[string]json.RawMessage
		if err := json.Unmarshal(raw, &choice); err != nil {
			return nil, errToolFlatten
		}
		messageRaw, ok := choice["message"]
		if !ok {
			continue
		}
		var message map[string]json.RawMessage
		if err := json.Unmarshal(messageRaw, &message); err != nil {
			return nil, errToolFlatten
		}
		callsRaw, ok := message["tool_calls"]
		if !ok {
			continue
		}
		blocks, calls, err := flattenToolCallsWithBudget(callsRaw, &totalCalls, &totalArguments)
		if err != nil {
			return nil, err
		}
		if len(calls) == 0 {
			return nil, errToolFlatten
		}
		content, err := flattenedContent(message["content"], blocks)
		if err != nil {
			return nil, err
		}
		message["content"] = json.RawMessage(strconv.Quote(content))
		delete(message, "tool_calls")
		if finish, ok := choice["finish_reason"]; ok {
			var reason string
			if json.Unmarshal(finish, &reason) == nil && reason == "tool_calls" {
				choice["finish_reason"] = json.RawMessage(`"stop"`)
			}
		}
		messageBytes, _ := json.Marshal(message)
		choice["message"] = messageBytes
		choices[i], _ = json.Marshal(choice)
		changed = true
	}
	if !changed {
		return append([]byte(nil), body...), nil
	}
	root["choices"], _ = json.Marshal(choices)
	return json.Marshal(root)
}

type flattenedCall struct {
	ID        string
	Name      string
	Arguments string
	Index     *int64
}

func flattenToolCalls(raw json.RawMessage) (string, []flattenedCall, error) {
	totalCalls, totalArguments := 0, 0
	return flattenToolCallsWithBudget(raw, &totalCalls, &totalArguments)
}

// flattenToolCallsWithBudget validates one choice while charging its calls and
// argument bytes against the enclosing response budget. The counters are
// charged only after the complete choice succeeds, so a malformed choice never
// leaves a partially accepted projection behind.
func flattenToolCallsWithBudget(raw json.RawMessage, totalCalls, totalArguments *int) (string, []flattenedCall, error) {
	if totalCalls == nil || totalArguments == nil || *totalCalls < 0 || *totalArguments < 0 {
		return "", nil, errToolFlatten
	}
	var rows []json.RawMessage
	if err := json.Unmarshal(raw, &rows); err != nil || len(rows) == 0 || len(rows) > maxFlattenCalls {
		return "", nil, errToolFlatten
	}
	if len(rows) > maxFlattenCalls-*totalCalls {
		return "", nil, errToolFlatten
	}
	calls := make([]flattenedCall, 0, len(rows))
	seenIDs := make(map[string]struct{}, len(rows))
	allIndexed, anyIndexed := true, false
	aggregate := 0
	for _, row := range rows {
		var call map[string]json.RawMessage
		if json.Unmarshal(row, &call) != nil {
			return "", nil, errToolFlatten
		}
		var typ string
		if !jsonString(call["type"], &typ) || typ != "function" {
			return "", nil, errToolFlatten
		}
		var id string
		if idRaw, ok := call["id"]; ok {
			if !jsonString(idRaw, &id) || !validToolID(id) {
				return "", nil, errToolFlatten
			}
			if _, ok := seenIDs[id]; ok {
				return "", nil, errToolFlatten
			}
			seenIDs[id] = struct{}{}
		}
		fn, ok := call["function"]
		if !ok {
			return "", nil, errToolFlatten
		}
		var function map[string]json.RawMessage
		if json.Unmarshal(fn, &function) != nil {
			return "", nil, errToolFlatten
		}
		var name, args string
		if !jsonString(function["name"], &name) || !validToolName(name) {
			return "", nil, errToolFlatten
		}
		if !jsonString(function["arguments"], &args) || strings.Contains(args, "</mx_tool>") {
			return "", nil, errToolFlatten
		}
		argsBytes := len(args)
		// Check the response-wide budget before retaining this argument string
		// in the flattened call set. A response may not accumulate 32 separate
		// 64 KiB calls merely because they came from different choices.
		argsBytes = len([]byte(args))
		if argsBytes > maxFlattenArguments || argsBytes > maxFlattenAggregate-aggregate ||
			*totalArguments > maxFlattenAggregate-aggregate-argsBytes {
			return "", nil, errToolFlatten
		}
		index, indexed := call["index"]
		if indexed {
			anyIndexed = true
			var n int64
			if json.Unmarshal(index, &n) != nil || n < 0 {
				return "", nil, errToolFlatten
			}
			calls = append(calls, flattenedCall{ID: id, Name: name, Arguments: args, Index: &n})
		} else {
			allIndexed = false
			calls = append(calls, flattenedCall{ID: id, Name: name, Arguments: args})
		}
		aggregate += argsBytes
	}
	if anyIndexed && !allIndexed {
		return "", nil, errToolFlatten
	}
	if anyIndexed {
		// The upstream index is an ordering key, not an array position. The
		// wire contract requires a strictly increasing, unique sequence but
		// deliberately permits gaps (for example 0, 2). Preserve the caller's
		// array order while validating that ordering here.
		for i, call := range calls {
			if call.Index == nil || (i > 0 && *call.Index <= *calls[i-1].Index) {
				return "", nil, errToolFlatten
			}
		}
	}
	blocks := make([]string, 0, len(calls))
	for _, call := range calls {
		attr := ` name="` + escapeToolAttr(call.Name) + `"`
		if call.ID != "" {
			attr += ` id="` + escapeToolAttr(call.ID) + `"`
		}
		blocks = append(blocks, "<mx_tool"+attr+">\n"+call.Arguments+"\n</mx_tool>")
	}
	*totalCalls += len(calls)
	*totalArguments += aggregate
	return strings.Join(blocks, "\n"), calls, nil
}

func flattenedContent(raw json.RawMessage, blocks string) (string, error) {
	var content string
	if len(raw) != 0 && !bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		if json.Unmarshal(raw, &content) != nil {
			return "", errToolFlatten
		}
	}
	if content == "" {
		return blocks, nil
	}
	return content + "\n\n" + blocks, nil
}

func escapeToolAttr(value string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&apos;").Replace(value)
}

func unescapeToolAttr(value string) (string, bool) {
	if !strings.Contains(value, "&") {
		if strings.ContainsAny(value, "<>'") {
			return "", false
		}
		return value, true
	}
	var out strings.Builder
	out.Grow(len(value))
	for i := 0; i < len(value); {
		if value[i] != '&' {
			// Flatten output escapes the XML-sensitive attribute characters
			// deterministically. Accepting their raw spellings would make the
			// reverse parser non-canonical and could consume text that the
			// response formatter would never have emitted.
			if value[i] == '<' || value[i] == '>' || value[i] == '\'' {
				return "", false
			}
			out.WriteByte(value[i])
			i++
			continue
		}
		end := strings.IndexByte(value[i+1:], ';')
		if end < 0 {
			return "", false
		}
		end += i + 1
		entity := value[i : end+1]
		decoded := ""
		switch entity {
		case "&amp;":
			decoded = "&"
			// Do not inspect the following ordinary bytes here. For example,
			// the legal id `a&lt;` is canonically emitted as `a&amp;lt;`:
			// `&amp;` decodes to a literal ampersand and the following `lt;`
			// remains ordinary id text. Rejecting that spelling would make the
			// canonical escape/unescape round-trip lossy.
		case "&lt;":
			decoded = "<"
		case "&gt;":
			decoded = ">"
		case "&quot;":
			decoded = `"`
		case "&apos;":
			decoded = "'"
		default:
			return "", false
		}
		out.WriteString(decoded)
		i = end + 1
	}
	return out.String(), true
}

func validToolName(name string) bool {
	if len(name) == 0 || len(name) > 64 {
		return false
	}
	for _, r := range name {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
			return false
		}
	}
	return true
}

func validToolID(id string) bool {
	if id == "" || len([]byte(id)) > 128 {
		return false
	}
	for _, r := range id {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

// ReverseFlatten returns a single immutable transformed request. Any
// malformed/partial/non-matching block causes the original request to be
// cloned unchanged, allowing normal OpenAI/Anthropic validation to decide its
// fate without inventing tool calls or results.
func (r *ChatRequest) ReverseFlatten() (*ChatRequest, error) {
	clone := r.CloneForAttempt()
	if clone == nil {
		return nil, ErrInvalidRequest
	}
	toolsRaw, ok := r.RawField("tools")
	if !ok {
		clear(toolsRaw)
		return clone, nil
	}
	defer clear(toolsRaw)
	allowed := toolNames(toolsRaw)
	if allowed == nil {
		return clone, nil
	}
	messagesIndex := -1
	for i, field := range clone.fields {
		if field.name == "messages" {
			messagesIndex = i
			break
		}
	}
	if messagesIndex < 0 {
		return clone, nil
	}
	var messages []json.RawMessage
	if json.Unmarshal(clone.fields[messagesIndex].value, &messages) != nil {
		return clone, nil
	}
	transformed, changed := reverseMessages(messages, allowed)
	if !changed {
		return clone, nil
	}
	clone.fields[messagesIndex].value, _ = json.Marshal(transformed)
	clone.requirements = projectCapabilities(clone.fields, clone.Stream)
	return clone, nil
}

func toolNames(raw []byte) map[string]struct{} {
	var rows []map[string]json.RawMessage
	if json.Unmarshal(raw, &rows) != nil || len(rows) == 0 {
		return nil
	}
	set := make(map[string]struct{}, len(rows))
	for _, row := range rows {
		var typ string
		if json.Unmarshal(row["type"], &typ) != nil || typ != "function" {
			return nil
		}
		var fn map[string]json.RawMessage
		if json.Unmarshal(row["function"], &fn) != nil {
			return nil
		}
		var name string
		if json.Unmarshal(fn["name"], &name) != nil || !validToolName(name) {
			return nil
		}
		if _, duplicate := set[name]; duplicate {
			return nil
		}
		set[name] = struct{}{}
	}
	return set
}

func reverseMessages(messages []json.RawMessage, allowed map[string]struct{}) ([]json.RawMessage, bool) {
	// The flattened history carries each tool result inline in the assistant
	// content. On a successful parse, turn that one message into the normal
	// assistant tool_calls message followed immediately by one tool message per
	// result. A malformed message is copied byte-for-byte and never partially
	// consumed.
	out := make([]json.RawMessage, 0, len(messages))
	changed := false
	totalCalls, totalBytes := 0, 0
	for _, raw := range messages {
		var msg map[string]json.RawMessage
		if json.Unmarshal(raw, &msg) != nil {
			out = append(out, raw)
			continue
		}
		var role string
		if json.Unmarshal(msg["role"], &role) != nil || role != "assistant" {
			out = append(out, raw)
			continue
		}
		// A structured tool call is already canonical and must not be mixed with
		// the text parser.
		if _, structured := msg["tool_calls"]; structured {
			out = append(out, raw)
			continue
		}
		var content string
		if json.Unmarshal(msg["content"], &content) != nil {
			out = append(out, raw)
			continue
		}
		calls, prefix, results, ok := parseFlattenedContentWithBudget(content, allowed, &totalCalls, &totalBytes)
		if !ok || len(calls) == 0 || len(calls) != len(results) {
			out = append(out, raw)
			continue
		}
		var toolCalls []map[string]any
		for _, call := range calls {
			toolCalls = append(toolCalls, map[string]any{"id": call.ID, "type": "function", "function": map[string]string{"name": call.Name, "arguments": call.Arguments}})
		}
		msg["content"] = json.RawMessage(strconv.Quote(prefix))
		msg["tool_calls"], _ = json.Marshal(toolCalls)
		assistant, err := json.Marshal(msg)
		if err != nil {
			out = append(out, raw)
			continue
		}
		out = append(out, assistant)
		for i, call := range calls {
			toolMessage, err := json.Marshal(map[string]string{
				"role":         "tool",
				"tool_call_id": call.ID,
				"content":      results[i],
			})
			if err != nil {
				return messages, false
			}
			out = append(out, toolMessage)
		}
		changed = true
	}
	return out, changed
}

type reverseCall struct{ ID, Name, Arguments string }

type streamToolDelta struct {
	ID        string
	IDSeen    bool
	Name      string
	NameSeen  bool
	Arguments string
}

type streamChoiceState struct {
	Index               int
	Role                string
	RoleSeen            bool
	Content             string
	EmittedContentBytes int
	FinishReason        string
	FinishSeen          bool
	FinishEmitted       bool
	Tools               map[int64]*streamToolDelta
}

func markStreamContentEmitted(states map[int]*streamChoiceState) {
	for _, state := range states {
		state.EmittedContentBytes = len(state.Content)
		if state.FinishSeen {
			state.FinishEmitted = true
		}
	}
}

// streamToolCount and streamArgumentBytes are checked before every append.
// Keeping the checks in the accumulator (rather than only at terminal
// reconstruction) prevents a response with many choices from retaining
// 32*64 KiB of tool arguments before finally being rejected.
func streamToolCount(states map[int]*streamChoiceState) int {
	count := 0
	for _, state := range states {
		count += len(state.Tools)
	}
	return count
}

func streamArgumentBytes(states map[int]*streamChoiceState) (int, bool) {
	total := 0
	for _, state := range states {
		for _, tool := range state.Tools {
			bytes := len(tool.Arguments)
			if bytes > maxFlattenAggregate-total {
				return 0, false
			}
			total += bytes
		}
	}
	return total, true
}

func clearStreamStates(states map[int]*streamChoiceState) {
	for _, state := range states {
		clear([]byte(state.Content))
		state.Content = ""
		for _, tool := range state.Tools {
			clear([]byte(tool.ID))
			clear([]byte(tool.Name))
			clear([]byte(tool.Arguments))
			tool.ID, tool.Name, tool.Arguments = "", "", ""
		}
		state.Tools = nil
	}
}

func accumulateStreamChunk(data []byte, states map[int]*streamChoiceState) (map[string]json.RawMessage, bool, error) {
	var root map[string]json.RawMessage
	if json.Unmarshal(data, &root) != nil {
		return nil, false, errToolFlatten
	}
	var choices []json.RawMessage
	if json.Unmarshal(root["choices"], &choices) != nil {
		return nil, false, errToolFlatten
	}
	hasTools := false
	for _, raw := range choices {
		var choice map[string]json.RawMessage
		if json.Unmarshal(raw, &choice) != nil {
			return nil, false, errToolFlatten
		}
		var index int
		if json.Unmarshal(choice["index"], &index) != nil || index < 0 {
			return nil, false, errToolFlatten
		}
		state := states[index]
		if state == nil {
			state = &streamChoiceState{Index: index, Tools: make(map[int64]*streamToolDelta)}
			states[index] = state
		}
		if rawFinish, ok := choice["finish_reason"]; ok {
			if !bytes.Equal(bytes.TrimSpace(rawFinish), []byte("null")) {
				var finish string
				if json.Unmarshal(rawFinish, &finish) != nil {
					return nil, false, errToolFlatten
				}
				if state.FinishSeen && state.FinishReason != finish {
					return nil, false, errToolFlatten
				}
				state.FinishSeen = true
				state.FinishReason = finish
			}
		}
		deltaRaw, ok := choice["delta"]
		if !ok {
			continue
		}
		var delta map[string]json.RawMessage
		if json.Unmarshal(deltaRaw, &delta) != nil {
			return nil, false, errToolFlatten
		}
		if role, ok := delta["role"]; ok {
			var value string
			if !jsonString(role, &value) {
				return nil, false, errToolFlatten
			}
			if state.RoleSeen && state.Role != value {
				return nil, false, errToolFlatten
			}
			state.RoleSeen = true
			state.Role = value
		}
		if content, ok := delta["content"]; ok {
			var text string
			trimmed := bytes.TrimSpace(content)
			if !bytes.Equal(trimmed, []byte("null")) {
				if !jsonString(content, &text) {
					return nil, false, errToolFlatten
				}
				state.Content += text
			}
		}
		callsRaw, ok := delta["tool_calls"]
		if !ok {
			continue
		}
		var calls []json.RawMessage
		if json.Unmarshal(callsRaw, &calls) != nil || len(calls) > maxFlattenCalls {
			return nil, false, errToolFlatten
		}
		hasTools = true
		for _, callRaw := range calls {
			var call map[string]json.RawMessage
			if json.Unmarshal(callRaw, &call) != nil {
				return nil, false, errToolFlatten
			}
			var callIndex int64
			if json.Unmarshal(call["index"], &callIndex) != nil || callIndex < 0 {
				return nil, false, errToolFlatten
			}
			tool := state.Tools[callIndex]
			if tool == nil {
				if streamToolCount(states) >= maxFlattenCalls {
					return nil, false, errToolFlatten
				}
				tool = &streamToolDelta{}
				state.Tools[callIndex] = tool
			}
			if typ, ok := call["type"]; ok {
				var value string
				if !jsonString(typ, &value) || value != "function" {
					return nil, false, errToolFlatten
				}
			}
			if id, ok := call["id"]; ok {
				var value string
				if !jsonString(id, &value) || !validToolID(value) {
					return nil, false, errToolFlatten
				}
				if tool.IDSeen && tool.ID != value {
					return nil, false, errToolFlatten
				}
				tool.IDSeen = true
				tool.ID = value
			}
			functionRaw, ok := call["function"]
			if !ok {
				continue
			}
			var function map[string]json.RawMessage
			if json.Unmarshal(functionRaw, &function) != nil {
				return nil, false, errToolFlatten
			}
			if name, ok := function["name"]; ok {
				var value string
				if !jsonString(name, &value) || !validToolName(value) || (tool.NameSeen && tool.Name != value) {
					return nil, false, errToolFlatten
				}
				tool.NameSeen = true
				tool.Name = value
			}
			if args, ok := function["arguments"]; ok {
				var value string
				if !jsonString(args, &value) {
					return nil, false, errToolFlatten
				}
				current, valid := streamArgumentBytes(states)
				if !valid || len(value) > maxFlattenArguments-len(tool.Arguments) ||
					len(value) > maxFlattenAggregate-current {
					return nil, false, errToolFlatten
				}
				tool.Arguments += value
			}
			if len([]byte(tool.Arguments)) > maxFlattenArguments {
				return nil, false, errToolFlatten
			}
		}
	}
	return root, hasTools, nil
}

func streamCompletionBody(first map[string]json.RawMessage, states map[int]*streamChoiceState) ([]byte, error) {
	return streamCompletionBodyAfter(first, states)
}

// streamCompletionBodyAfter reconstructs a completion from the buffered
// stream state.  Content before EmittedContentBytes has already been sent as
// ordinary deltas; only the suffix is included in the terminal rewrite.  This
// keeps the final assembled stream in the same order as the non-stream
// flattening result without replaying committed content.
func streamCompletionBodyAfter(first map[string]json.RawMessage, states map[int]*streamChoiceState) ([]byte, error) {
	if len(states) == 0 {
		return nil, errToolFlatten
	}
	indices := make([]int, 0, len(states))
	for index := range states {
		indices = append(indices, index)
	}
	for i := 0; i < len(indices); i++ {
		for j := i + 1; j < len(indices); j++ {
			if indices[j] < indices[i] {
				indices[i], indices[j] = indices[j], indices[i]
			}
		}
	}
	choices := make([]map[string]any, 0, len(indices))
	for _, index := range indices {
		state := states[index]
		if state.FinishReason == "" {
			return nil, errToolFlatten
		}
		if state.EmittedContentBytes < 0 || state.EmittedContentBytes > len(state.Content) {
			return nil, errToolFlatten
		}
		content := state.Content[state.EmittedContentBytes:]
		if len(state.Tools) == 0 && state.FinishReason == "tool_calls" {
			return nil, errToolFlatten
		}
		// A choice whose ordinary content and finish marker were already
		// committed has nothing left for the terminal rewrite. If its finish
		// marker arrived after another choice introduced tools, however, it was
		// suppressed along with that mixed frame and must be retained here.
		if len(state.Tools) == 0 && state.FinishEmitted {
			continue
		}
		message := map[string]any{"role": state.Role, "content": content}
		if message["role"] == "" {
			message["role"] = "assistant"
		}
		if len(state.Tools) > 0 {
			if state.FinishReason != "tool_calls" {
				return nil, errToolFlatten
			}
			toolIndices := make([]int64, 0, len(state.Tools))
			for toolIndex := range state.Tools {
				toolIndices = append(toolIndices, toolIndex)
			}
			for i := 0; i < len(toolIndices); i++ {
				for j := i + 1; j < len(toolIndices); j++ {
					if toolIndices[j] < toolIndices[i] {
						toolIndices[i], toolIndices[j] = toolIndices[j], toolIndices[i]
					}
				}
			}
			calls := make([]map[string]any, 0, len(toolIndices))
			for _, toolIndex := range toolIndices {
				tool := state.Tools[toolIndex]
				if tool.IDSeen && !validToolID(tool.ID) {
					return nil, errToolFlatten
				}
				call := map[string]any{"index": toolIndex, "type": "function", "function": map[string]string{"name": tool.Name, "arguments": tool.Arguments}}
				if tool.ID != "" {
					call["id"] = tool.ID
				}
				calls = append(calls, call)
			}
			message["tool_calls"] = calls
		}
		choices = append(choices, map[string]any{"index": index, "message": message, "finish_reason": state.FinishReason})
	}
	root := make(map[string]json.RawMessage, len(first)+1)
	for key, value := range first {
		root[key] = value
	}
	// Usage is emitted as the original upstream usage chunk after the
	// rewritten finish chunk, never folded into the generated content chunk.
	delete(root, "usage")
	root["choices"], _ = json.Marshal(choices)
	if len(choices) == 0 {
		return nil, errToolFlatten
	}
	return json.Marshal(root)
}

// completionToStreamFrames turns one flattened completion into the two
// terminal stream frames required by the flatten contract: a content delta
// carrying the generated text, followed by a separate rewritten finish
// chunk.  Usage is deliberately absent from both frames and is forwarded from
// the original upstream usage chunk by the stream adapter.
func completionToStreamFrames(body []byte) (contentFrame, finishFrame []byte, err error) {
	var root map[string]json.RawMessage
	if json.Unmarshal(body, &root) != nil {
		return nil, nil, errToolFlatten
	}
	var choices []map[string]json.RawMessage
	if json.Unmarshal(root["choices"], &choices) != nil || len(choices) == 0 {
		return nil, nil, errToolFlatten
	}
	contentChoices := make([]map[string]json.RawMessage, 0, len(choices))
	finishChoices := make([]map[string]json.RawMessage, 0, len(choices))
	for _, choice := range choices {
		message, ok := choice["message"]
		if !ok {
			return nil, nil, errToolFlatten
		}
		var messageFields map[string]json.RawMessage
		if json.Unmarshal(message, &messageFields) != nil {
			return nil, nil, errToolFlatten
		}
		contentChoice := make(map[string]json.RawMessage, len(choice)+1)
		for key, value := range choice {
			if key != "message" && key != "finish_reason" {
				contentChoice[key] = value
			}
		}
		contentChoice["delta"] = message
		contentChoice["finish_reason"] = json.RawMessage("null")
		contentChoices = append(contentChoices, contentChoice)

		index, ok := choice["index"]
		if !ok {
			return nil, nil, errToolFlatten
		}
		finishReason, ok := choice["finish_reason"]
		if !ok {
			return nil, nil, errToolFlatten
		}
		finishChoices = append(finishChoices, map[string]json.RawMessage{
			"index":         index,
			"delta":         json.RawMessage(`{}`),
			"finish_reason": finishReason,
		})
	}
	delete(root, "usage")
	root["choices"], _ = json.Marshal(contentChoices)
	contentFrame, err = json.Marshal(root)
	if err != nil {
		return nil, nil, errToolFlatten
	}
	root["choices"], _ = json.Marshal(finishChoices)
	finishFrame, err = json.Marshal(root)
	if err != nil {
		clear(contentFrame)
		return nil, nil, errToolFlatten
	}
	return contentFrame, finishFrame, nil
}

func completionToStreamChunk(body []byte) ([]byte, error) {
	var root map[string]json.RawMessage
	if json.Unmarshal(body, &root) != nil {
		return nil, errToolFlatten
	}
	var choices []map[string]json.RawMessage
	if json.Unmarshal(root["choices"], &choices) != nil {
		return nil, errToolFlatten
	}
	for _, choice := range choices {
		message, ok := choice["message"]
		if !ok {
			return nil, errToolFlatten
		}
		choice["delta"] = message
		delete(choice, "message")
	}
	root["choices"], _ = json.Marshal(choices)
	return json.Marshal(root)
}

func parseFlattenedContent(content string, allowed map[string]struct{}) ([]reverseCall, string, []string, bool) {
	totalCalls, totalBytes := 0, 0
	return parseFlattenedContentWithBudget(content, allowed, &totalCalls, &totalBytes)
}

// parseFlattenedContentWithBudget parses one assistant message and charges its
// complete call/result pairs to the request-wide reverse budget. Charges are
// committed only after the message is fully valid, preserving the all-or-none
// rule for malformed messages.
func parseFlattenedContentWithBudget(content string, allowed map[string]struct{}, totalCalls, totalBytes *int) ([]reverseCall, string, []string, bool) {
	if totalCalls == nil || totalBytes == nil || *totalCalls < 0 || *totalBytes < 0 {
		return nil, "", nil, false
	}
	start := strings.Index(content, "<mx_tool")
	if start < 0 {
		return nil, "", nil, false
	}
	prefix := content[:start]
	if start != 0 {
		if !strings.HasSuffix(prefix, "\n\n") {
			return nil, "", nil, false
		}
		prefix = prefix[:len(prefix)-2]
	}
	rest := content[start:]
	var calls []reverseCall
	var results []string
	seen := map[string]struct{}{}
	aggregate := 0
	for len(rest) > 0 {
		if len(calls) >= maxFlattenCalls || *totalCalls+len(calls) >= maxFlattenCalls {
			return nil, "", nil, false
		}
		if !strings.HasPrefix(rest, "<mx_tool") {
			return nil, "", nil, false
		}
		endOpen := strings.Index(rest, ">")
		if endOpen < 0 {
			return nil, "", nil, false
		}
		name, id, ok := parseToolOpen(rest[:endOpen+1])
		if !ok {
			return nil, "", nil, false
		}
		if _, ok := allowed[name]; !ok || id == "" {
			return nil, "", nil, false
		}
		// The wire grammar has one exact ASCII LF delimiter after the opening
		// tag. In particular, CRLF is not a tolerated spelling. The argument
		// string begins after that delimiter and may itself begin with LF.
		if endOpen+1 >= len(rest) || rest[endOpen+1] != '\n' {
			return nil, "", nil, false
		}
		argumentStart := endOpen + 2
		close := strings.Index(rest[argumentStart:], "\n</mx_tool>")
		if close < 0 {
			return nil, "", nil, false
		}
		close += argumentStart
		args := rest[argumentStart:close]
		argsBytes := len([]byte(args))
		if argsBytes > maxFlattenArguments || argsBytes > maxFlattenAggregate-aggregate ||
			*totalBytes > maxFlattenAggregate-aggregate-argsBytes || strings.Contains(args, "</mx_tool>") {
			return nil, "", nil, false
		}
		aggregate += argsBytes
		if _, dup := seen[id]; dup {
			return nil, "", nil, false
		}
		seen[id] = struct{}{}
		calls = append(calls, reverseCall{ID: id, Name: name, Arguments: args})
		rest = rest[close+len("\n</mx_tool>"):]
		if !strings.HasPrefix(rest, "\n<mx_tool_result") {
			return nil, "", nil, false
		}
		rest = rest[1:]
		resultEndOpen := strings.Index(rest, ">")
		if resultEndOpen < 0 {
			return nil, "", nil, false
		}
		resultID, resultOK := parseToolResultOpen(rest[:resultEndOpen+1])
		if !resultOK || resultID != id {
			return nil, "", nil, false
		}
		resultClose := strings.Index(rest[resultEndOpen+1:], "</mx_tool_result>")
		if resultClose < 0 {
			return nil, "", nil, false
		}
		resultClose += resultEndOpen + 1
		result := rest[resultEndOpen+1 : resultClose]
		resultBytes := len([]byte(result))
		if strings.Contains(result, "</mx_tool_result>") || resultBytes > maxFlattenArguments ||
			resultBytes > maxFlattenAggregate-aggregate || *totalBytes > maxFlattenAggregate-aggregate-resultBytes {
			return nil, "", nil, false
		}
		aggregate += resultBytes
		results = append(results, result)
		rest = rest[resultClose+len("</mx_tool_result>"):]
		if len(rest) == 0 {
			break
		}
		if !strings.HasPrefix(rest, "\n<mx_tool") || strings.HasPrefix(rest, "\n\n") {
			return nil, "", nil, false
		}
	}
	*totalCalls += len(calls)
	*totalBytes += aggregate
	return calls, prefix, results, true
}

func parseToolOpen(open string) (string, string, bool) {
	if !strings.HasPrefix(open, `<mx_tool `) || !strings.HasSuffix(open, `>`) {
		return "", "", false
	}
	body := strings.TrimSuffix(strings.TrimPrefix(open, `<mx_tool `), `>`)
	var name, id string
	seenName, seenID := false, false
	for len(body) > 0 {
		eq := strings.IndexByte(body, '=')
		if eq <= 0 || len(body) <= eq+2 || body[eq+1] != '"' {
			return "", "", false
		}
		key := body[:eq]
		end := eq + 2
		for end < len(body) && body[end] != '"' {
			end++
		}
		if end >= len(body) {
			return "", "", false
		}
		value, ok := unescapeToolAttr(body[eq+2 : end])
		if !ok {
			return "", "", false
		}
		remainder := body[end+1:]
		if len(remainder) > 0 && !strings.ContainsRune(" \t\r\n", rune(remainder[0])) {
			return "", "", false
		}
		switch key {
		case "name":
			if seenName || seenID {
				return "", "", false
			}
			seenName = true
			name = value
		case "id":
			if !seenName || seenID {
				return "", "", false
			}
			seenID = true
			id = value
		default:
			return "", "", false
		}
		if remainder == "" {
			break
		}
		// The emitted grammar uses exactly one ASCII space between the
		// fixed-order attributes. Tabs, repeated spaces, and trailing
		// whitespace are malformed history and remain ordinary text.
		if remainder[0] != ' ' || len(remainder) == 1 {
			return "", "", false
		}
		body = remainder[1:]
	}
	return name, id, validToolName(name) && (id == "" || validToolID(id))
}

func parseToolResultOpen(open string) (string, bool) {
	if !strings.HasPrefix(open, `<mx_tool_result `) || !strings.HasSuffix(open, `>`) {
		return "", false
	}
	body := strings.TrimSuffix(strings.TrimPrefix(open, `<mx_tool_result `), `>`)
	if !strings.HasPrefix(body, `id="`) {
		return "", false
	}
	end := strings.IndexByte(body[4:], '"')
	if end < 0 || 4+end != len(body)-1 {
		return "", false
	}
	id, ok := unescapeToolAttr(body[4 : 4+end])
	return id, ok && validToolID(id)
}
