package tooltext

import (
	"bytes"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"unicode"
)

const (
	MaxFlattenCalls     = 32
	MaxFlattenArguments = 64 << 10
	MaxFlattenAggregate = 256 << 10
)

var ErrToolFlatten = errors.New("connector: tool flattening failed")
var ErrFlattenStreamRejected = errors.New("connector: flattened stream response rejected")

func JsonString(raw json.RawMessage, dst *string) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) > 0 && trimmed[0] == '"' && json.Unmarshal(trimmed, dst) == nil
}

// FlattenCompletion converts only a structurally valid OpenAI completion. It
// returns the original bytes when no choice carries tool_calls and never
// reserializes arguments (the function.arguments string is copied verbatim).
func FlattenCompletion(body []byte) ([]byte, error) {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(body, &root); err != nil {
		return nil, ErrToolFlatten
	}
	var choices []json.RawMessage
	if err := json.Unmarshal(root["choices"], &choices); err != nil {
		return nil, ErrToolFlatten
	}
	totalCalls, totalArguments := 0, 0
	changed := false
	for i, raw := range choices {
		var choice map[string]json.RawMessage
		if err := json.Unmarshal(raw, &choice); err != nil {
			return nil, ErrToolFlatten
		}
		messageRaw, ok := choice["message"]
		if !ok {
			continue
		}
		var message map[string]json.RawMessage
		if err := json.Unmarshal(messageRaw, &message); err != nil {
			return nil, ErrToolFlatten
		}
		callsRaw, ok := message["tool_calls"]
		if !ok {
			continue
		}
		blocks, calls, err := FlattenToolCallsWithBudget(callsRaw, &totalCalls, &totalArguments)
		if err != nil {
			return nil, err
		}
		if len(calls) == 0 {
			return nil, ErrToolFlatten
		}
		content, err := FlattenedContent(message["content"], blocks)
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

type FlattenedCall struct {
	ID        string
	Name      string
	Arguments string
	Index     *int64
}

func FlattenToolCalls(raw json.RawMessage) (string, []FlattenedCall, error) {
	totalCalls, totalArguments := 0, 0
	return FlattenToolCallsWithBudget(raw, &totalCalls, &totalArguments)
}

// FlattenToolCallsWithBudget validates one choice while charging its calls and
// argument bytes against the enclosing response budget. The counters are
// charged only after the complete choice succeeds, so a malformed choice never
// leaves a partially accepted projection behind.
func FlattenToolCallsWithBudget(raw json.RawMessage, totalCalls, totalArguments *int) (string, []FlattenedCall, error) {
	if totalCalls == nil || totalArguments == nil || *totalCalls < 0 || *totalArguments < 0 {
		return "", nil, ErrToolFlatten
	}
	var rows []json.RawMessage
	if err := json.Unmarshal(raw, &rows); err != nil || len(rows) == 0 || len(rows) > MaxFlattenCalls {
		return "", nil, ErrToolFlatten
	}
	if len(rows) > MaxFlattenCalls-*totalCalls {
		return "", nil, ErrToolFlatten
	}
	calls := make([]FlattenedCall, 0, len(rows))
	seenIDs := make(map[string]struct{}, len(rows))
	allIndexed, anyIndexed := true, false
	aggregate := 0
	for _, row := range rows {
		var call map[string]json.RawMessage
		if json.Unmarshal(row, &call) != nil {
			return "", nil, ErrToolFlatten
		}
		var typ string
		if !JsonString(call["type"], &typ) || typ != "function" {
			return "", nil, ErrToolFlatten
		}
		var id string
		if idRaw, ok := call["id"]; ok {
			if !JsonString(idRaw, &id) || !ValidToolID(id) {
				return "", nil, ErrToolFlatten
			}
			if _, ok := seenIDs[id]; ok {
				return "", nil, ErrToolFlatten
			}
			seenIDs[id] = struct{}{}
		}
		fn, ok := call["function"]
		if !ok {
			return "", nil, ErrToolFlatten
		}
		var function map[string]json.RawMessage
		if json.Unmarshal(fn, &function) != nil {
			return "", nil, ErrToolFlatten
		}
		var name, args string
		if !JsonString(function["name"], &name) || !ValidToolName(name) {
			return "", nil, ErrToolFlatten
		}
		if !JsonString(function["arguments"], &args) || strings.Contains(args, "</mx_tool>") {
			return "", nil, ErrToolFlatten
		}
		argsBytes := len(args)
		// Check the response-wide budget before retaining this argument string
		// in the flattened call set. A response may not accumulate 32 separate
		// 64 KiB calls merely because they came from different choices.
		argsBytes = len([]byte(args))
		if argsBytes > MaxFlattenArguments || argsBytes > MaxFlattenAggregate-aggregate ||
			*totalArguments > MaxFlattenAggregate-aggregate-argsBytes {
			return "", nil, ErrToolFlatten
		}
		index, indexed := call["index"]
		if indexed {
			anyIndexed = true
			var n int64
			if json.Unmarshal(index, &n) != nil || n < 0 {
				return "", nil, ErrToolFlatten
			}
			calls = append(calls, FlattenedCall{ID: id, Name: name, Arguments: args, Index: &n})
		} else {
			allIndexed = false
			calls = append(calls, FlattenedCall{ID: id, Name: name, Arguments: args})
		}
		aggregate += argsBytes
	}
	if anyIndexed && !allIndexed {
		return "", nil, ErrToolFlatten
	}
	if anyIndexed {
		// The upstream index is an ordering key, not an array position. The
		// wire contract requires a strictly increasing, unique sequence but
		// deliberately permits gaps (for example 0, 2). Preserve the caller's
		// array order while validating that ordering here.
		for i, call := range calls {
			if call.Index == nil || (i > 0 && *call.Index <= *calls[i-1].Index) {
				return "", nil, ErrToolFlatten
			}
		}
	}
	blocks := make([]string, 0, len(calls))
	for _, call := range calls {
		attr := ` name="` + EscapeToolAttr(call.Name) + `"`
		if call.ID != "" {
			attr += ` id="` + EscapeToolAttr(call.ID) + `"`
		}
		blocks = append(blocks, "<mx_tool"+attr+">\n"+call.Arguments+"\n</mx_tool>")
	}
	*totalCalls += len(calls)
	*totalArguments += aggregate
	return strings.Join(blocks, "\n"), calls, nil
}

func FlattenedContent(raw json.RawMessage, blocks string) (string, error) {
	var content string
	if len(raw) != 0 && !bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		if json.Unmarshal(raw, &content) != nil {
			return "", ErrToolFlatten
		}
	}
	if content == "" {
		return blocks, nil
	}
	return content + "\n\n" + blocks, nil
}

func EscapeToolAttr(value string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&apos;").Replace(value)
}

func UnescapeToolAttr(value string) (string, bool) {
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

func ValidToolName(name string) bool {
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

func ValidToolID(id string) bool {
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

func ToolNames(raw []byte) map[string]struct{} {
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
		if json.Unmarshal(fn["name"], &name) != nil || !ValidToolName(name) {
			return nil
		}
		if _, duplicate := set[name]; duplicate {
			return nil
		}
		set[name] = struct{}{}
	}
	return set
}

func ReverseMessages(messages []json.RawMessage, allowed map[string]struct{}) ([]json.RawMessage, bool) {
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
		calls, prefix, results, ok := ParseFlattenedContentWithBudget(content, allowed, &totalCalls, &totalBytes)
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

type ReverseCall struct{ ID, Name, Arguments string }

type StreamToolDelta struct {
	ID        string
	IDSeen    bool
	Name      string
	NameSeen  bool
	Arguments string
}

type StreamChoiceState struct {
	Index               int
	Role                string
	RoleSeen            bool
	Content             string
	EmittedContentBytes int
	FinishReason        string
	FinishSeen          bool
	FinishEmitted       bool
	Tools               map[int64]*StreamToolDelta
}

func MarkStreamContentEmitted(states map[int]*StreamChoiceState) {
	for _, state := range states {
		state.EmittedContentBytes = len(state.Content)
		if state.FinishSeen {
			state.FinishEmitted = true
		}
	}
}

// StreamToolCount and StreamArgumentBytes are checked before every append.
// Keeping the checks in the accumulator (rather than only at terminal
// reconstruction) prevents a response with many choices from retaining
// 32*64 KiB of tool arguments before finally being rejected.
func StreamToolCount(states map[int]*StreamChoiceState) int {
	count := 0
	for _, state := range states {
		count += len(state.Tools)
	}
	return count
}

func StreamArgumentBytes(states map[int]*StreamChoiceState) (int, bool) {
	total := 0
	for _, state := range states {
		for _, tool := range state.Tools {
			bytes := len(tool.Arguments)
			if bytes > MaxFlattenAggregate-total {
				return 0, false
			}
			total += bytes
		}
	}
	return total, true
}

func ClearStreamStates(states map[int]*StreamChoiceState) {
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

func AccumulateStreamChunk(data []byte, states map[int]*StreamChoiceState) (map[string]json.RawMessage, bool, error) {
	var root map[string]json.RawMessage
	if json.Unmarshal(data, &root) != nil {
		return nil, false, ErrToolFlatten
	}
	var choices []json.RawMessage
	if json.Unmarshal(root["choices"], &choices) != nil {
		return nil, false, ErrToolFlatten
	}
	hasTools := false
	for _, raw := range choices {
		var choice map[string]json.RawMessage
		if json.Unmarshal(raw, &choice) != nil {
			return nil, false, ErrToolFlatten
		}
		var index int
		if json.Unmarshal(choice["index"], &index) != nil || index < 0 {
			return nil, false, ErrToolFlatten
		}
		state := states[index]
		if state == nil {
			state = &StreamChoiceState{Index: index, Tools: make(map[int64]*StreamToolDelta)}
			states[index] = state
		}
		if rawFinish, ok := choice["finish_reason"]; ok {
			if !bytes.Equal(bytes.TrimSpace(rawFinish), []byte("null")) {
				var finish string
				if json.Unmarshal(rawFinish, &finish) != nil {
					return nil, false, ErrToolFlatten
				}
				if state.FinishSeen && state.FinishReason != finish {
					return nil, false, ErrToolFlatten
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
			return nil, false, ErrToolFlatten
		}
		if role, ok := delta["role"]; ok {
			var value string
			if !JsonString(role, &value) {
				return nil, false, ErrToolFlatten
			}
			if state.RoleSeen && state.Role != value {
				return nil, false, ErrToolFlatten
			}
			state.RoleSeen = true
			state.Role = value
		}
		if content, ok := delta["content"]; ok {
			var text string
			trimmed := bytes.TrimSpace(content)
			if !bytes.Equal(trimmed, []byte("null")) {
				if !JsonString(content, &text) {
					return nil, false, ErrToolFlatten
				}
				state.Content += text
			}
		}
		callsRaw, ok := delta["tool_calls"]
		if !ok {
			continue
		}
		var calls []json.RawMessage
		if json.Unmarshal(callsRaw, &calls) != nil || len(calls) > MaxFlattenCalls {
			return nil, false, ErrToolFlatten
		}
		hasTools = true
		for _, callRaw := range calls {
			var call map[string]json.RawMessage
			if json.Unmarshal(callRaw, &call) != nil {
				return nil, false, ErrToolFlatten
			}
			var callIndex int64
			if json.Unmarshal(call["index"], &callIndex) != nil || callIndex < 0 {
				return nil, false, ErrToolFlatten
			}
			tool := state.Tools[callIndex]
			if tool == nil {
				if StreamToolCount(states) >= MaxFlattenCalls {
					return nil, false, ErrToolFlatten
				}
				tool = &StreamToolDelta{}
				state.Tools[callIndex] = tool
			}
			if typ, ok := call["type"]; ok {
				var value string
				if !JsonString(typ, &value) || value != "function" {
					return nil, false, ErrToolFlatten
				}
			}
			if id, ok := call["id"]; ok {
				var value string
				if !JsonString(id, &value) || !ValidToolID(value) {
					return nil, false, ErrToolFlatten
				}
				if tool.IDSeen && tool.ID != value {
					return nil, false, ErrToolFlatten
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
				return nil, false, ErrToolFlatten
			}
			if name, ok := function["name"]; ok {
				var value string
				if !JsonString(name, &value) || !ValidToolName(value) || (tool.NameSeen && tool.Name != value) {
					return nil, false, ErrToolFlatten
				}
				tool.NameSeen = true
				tool.Name = value
			}
			if args, ok := function["arguments"]; ok {
				var value string
				if !JsonString(args, &value) {
					return nil, false, ErrToolFlatten
				}
				current, valid := StreamArgumentBytes(states)
				if !valid || len(value) > MaxFlattenArguments-len(tool.Arguments) ||
					len(value) > MaxFlattenAggregate-current {
					return nil, false, ErrToolFlatten
				}
				tool.Arguments += value
			}
			if len([]byte(tool.Arguments)) > MaxFlattenArguments {
				return nil, false, ErrToolFlatten
			}
		}
	}
	return root, hasTools, nil
}

func StreamCompletionBody(first map[string]json.RawMessage, states map[int]*StreamChoiceState) ([]byte, error) {
	return StreamCompletionBodyAfter(first, states)
}

// StreamCompletionBodyAfter reconstructs a completion from the buffered
// stream state.  Content before EmittedContentBytes has already been sent as
// ordinary deltas; only the suffix is included in the terminal rewrite.  This
// keeps the final assembled stream in the same order as the non-stream
// flattening result without replaying committed content.
func StreamCompletionBodyAfter(first map[string]json.RawMessage, states map[int]*StreamChoiceState) ([]byte, error) {
	if len(states) == 0 {
		return nil, ErrToolFlatten
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
			return nil, ErrToolFlatten
		}
		if state.EmittedContentBytes < 0 || state.EmittedContentBytes > len(state.Content) {
			return nil, ErrToolFlatten
		}
		content := state.Content[state.EmittedContentBytes:]
		if len(state.Tools) == 0 && state.FinishReason == "tool_calls" {
			return nil, ErrToolFlatten
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
				return nil, ErrToolFlatten
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
				if tool.IDSeen && !ValidToolID(tool.ID) {
					return nil, ErrToolFlatten
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
	root := make(map[string]json.RawMessage)
	for key, value := range first {
		root[key] = value
	}
	// Usage is emitted as the original upstream usage chunk after the
	// rewritten finish chunk, never folded into the generated content chunk.
	delete(root, "usage")
	root["choices"], _ = json.Marshal(choices)
	if len(choices) == 0 {
		return nil, ErrToolFlatten
	}
	return json.Marshal(root)
}

// CompletionToStreamFrames turns one flattened completion into the two
// terminal stream frames required by the flatten contract: a content delta
// carrying the generated text, followed by a separate rewritten finish
// chunk.  Usage is deliberately absent from both frames and is forwarded from
// the original upstream usage chunk by the stream adapter.
func CompletionToStreamFrames(body []byte) (contentFrame, finishFrame []byte, err error) {
	var root map[string]json.RawMessage
	if json.Unmarshal(body, &root) != nil {
		return nil, nil, ErrToolFlatten
	}
	var choices []map[string]json.RawMessage
	if json.Unmarshal(root["choices"], &choices) != nil || len(choices) == 0 {
		return nil, nil, ErrToolFlatten
	}
	contentChoices := make([]map[string]json.RawMessage, 0, len(choices))
	finishChoices := make([]map[string]json.RawMessage, 0, len(choices))
	for _, choice := range choices {
		message, ok := choice["message"]
		if !ok {
			return nil, nil, ErrToolFlatten
		}
		var messageFields map[string]json.RawMessage
		if json.Unmarshal(message, &messageFields) != nil {
			return nil, nil, ErrToolFlatten
		}
		contentChoice := make(map[string]json.RawMessage)
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
			return nil, nil, ErrToolFlatten
		}
		finishReason, ok := choice["finish_reason"]
		if !ok {
			return nil, nil, ErrToolFlatten
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
		return nil, nil, ErrToolFlatten
	}
	root["choices"], _ = json.Marshal(finishChoices)
	finishFrame, err = json.Marshal(root)
	if err != nil {
		clear(contentFrame)
		return nil, nil, ErrToolFlatten
	}
	return contentFrame, finishFrame, nil
}

func CompletionToStreamChunk(body []byte) ([]byte, error) {
	var root map[string]json.RawMessage
	if json.Unmarshal(body, &root) != nil {
		return nil, ErrToolFlatten
	}
	var choices []map[string]json.RawMessage
	if json.Unmarshal(root["choices"], &choices) != nil {
		return nil, ErrToolFlatten
	}
	for _, choice := range choices {
		message, ok := choice["message"]
		if !ok {
			return nil, ErrToolFlatten
		}
		choice["delta"] = message
		delete(choice, "message")
	}
	root["choices"], _ = json.Marshal(choices)
	return json.Marshal(root)
}

func ParseFlattenedContent(content string, allowed map[string]struct{}) ([]ReverseCall, string, []string, bool) {
	totalCalls, totalBytes := 0, 0
	return ParseFlattenedContentWithBudget(content, allowed, &totalCalls, &totalBytes)
}

// ParseFlattenedContentWithBudget parses one assistant message and charges its
// complete call/result pairs to the request-wide reverse budget. Charges are
// committed only after the message is fully valid, preserving the all-or-none
// rule for malformed messages.
func ParseFlattenedContentWithBudget(content string, allowed map[string]struct{}, totalCalls, totalBytes *int) ([]ReverseCall, string, []string, bool) {
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
	var calls []ReverseCall
	var results []string
	seen := map[string]struct{}{}
	aggregate := 0
	for len(rest) > 0 {
		if len(calls) >= MaxFlattenCalls || *totalCalls+len(calls) >= MaxFlattenCalls {
			return nil, "", nil, false
		}
		if !strings.HasPrefix(rest, "<mx_tool") {
			return nil, "", nil, false
		}
		endOpen := strings.Index(rest, ">")
		if endOpen < 0 {
			return nil, "", nil, false
		}
		name, id, ok := ParseToolOpen(rest[:endOpen+1])
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
		if argsBytes > MaxFlattenArguments || argsBytes > MaxFlattenAggregate-aggregate ||
			*totalBytes > MaxFlattenAggregate-aggregate-argsBytes || strings.Contains(args, "</mx_tool>") {
			return nil, "", nil, false
		}
		aggregate += argsBytes
		if _, dup := seen[id]; dup {
			return nil, "", nil, false
		}
		seen[id] = struct{}{}
		calls = append(calls, ReverseCall{ID: id, Name: name, Arguments: args})
		rest = rest[close+len("\n</mx_tool>"):]
		if !strings.HasPrefix(rest, "\n<mx_tool_result") {
			return nil, "", nil, false
		}
		rest = rest[1:]
		resultEndOpen := strings.Index(rest, ">")
		if resultEndOpen < 0 {
			return nil, "", nil, false
		}
		resultID, resultOK := ParseToolResultOpen(rest[:resultEndOpen+1])
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
		if strings.Contains(result, "</mx_tool_result>") || resultBytes > MaxFlattenArguments ||
			resultBytes > MaxFlattenAggregate-aggregate || *totalBytes > MaxFlattenAggregate-aggregate-resultBytes {
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

func ParseToolOpen(open string) (string, string, bool) {
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
		value, ok := UnescapeToolAttr(body[eq+2 : end])
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
	return name, id, ValidToolName(name) && (id == "" || ValidToolID(id))
}

func ParseToolResultOpen(open string) (string, bool) {
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
	id, ok := UnescapeToolAttr(body[4 : 4+end])
	return id, ok && ValidToolID(id)
}
