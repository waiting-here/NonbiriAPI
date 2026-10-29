package agentrequest

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"sort"
	"strings"

	"github.com/waiting-here/NonbiriAPI/internal/egress"
)

func codexParameters(raw json.RawMessage) (map[string]json.RawMessage, error) {
	values, err := responsesParameters(raw, true)
	if err != nil {
		return nil, err
	}
	for key, value := range values {
		switch key {
		case "reasoning":
			var object map[string]string
			if json.Unmarshal(value, &object) != nil {
				return nil, ErrInvalid
			}
			for k, v := range object {
				if k == "effort" && oneOf(v, "none", "minimal", "low", "medium", "high", "xhigh") || k == "summary" && oneOf(v, "auto", "concise", "detailed") {
					continue
				}
				return nil, ErrInvalid
			}
		case "text", "parallel_tool_calls", "prompt_cache_key":
		case "service_tier":
			var v string
			if json.Unmarshal(value, &v) != nil || !oneOf(v, "auto", "default", "priority") {
				return nil, ErrInvalid
			}
		default:
			return nil, ErrInvalid
		}
	}
	return values, nil
}
func codexBody(in Input, s Settings) ([]byte, error) {
	if _, err := codexParameters(s.ExtraParameters); err != nil {
		return nil, err
	}
	encoded, err := responsesBody(in, s)
	if err != nil {
		return nil, err
	}
	var body map[string]json.RawMessage
	if json.Unmarshal(encoded, &body) != nil {
		return nil, ErrInvalid
	}
	var input []json.RawMessage
	if json.Unmarshal(body["input"], &input) != nil {
		return nil, ErrInvalid
	}
	filtered := make([]json.RawMessage, 0, len(input))
	var instructions []string
	for _, item := range input {
		var message struct {
			Type    string `json:"type"`
			Role    string `json:"role"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		}
		_ = json.Unmarshal(item, &message)
		if message.Type == "message" && message.Role == "developer" {
			for _, part := range message.Content {
				if part.Type != "input_text" {
					return nil, ErrInvalid
				}
				instructions = append(instructions, part.Text)
			}
		} else {
			filtered = append(filtered, item)
		}
	}
	body["instructions"], _ = json.Marshal(strings.Join(instructions, "\n\n"))
	body["input"], _ = json.Marshal(filtered)
	delete(body, "max_output_tokens")
	delete(body, "temperature")
	delete(body, "top_p")
	raw, err := json.Marshal(body)
	if err != nil || len(raw) > MaxBodyBytes {
		return nil, ErrInvalid
	}
	return raw, nil
}

// Codex may finish without repeating output. Completed items retain their
// native order and encrypted reasoning for the next stateless tool turn.
func parseCodexResult(id int64, raw []byte, s Settings) (Result, bool, string, error) {
	out := Result{RequestID: id, RawResponse: string(raw), Usage: Usage{Unknown: true}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events, errs := egress.StreamSSE(ctx, io.NopCloser(bytes.NewReader(raw)), egress.SSEOptions{MaxBytes: MaxBodyBytes, MaxEventBytes: MaxBodyBytes})
	items := map[int]json.RawMessage{}
	itemIDs := map[string]int{}
	nextIndex := 0
	total := 0
	terminal, completed := false, false
	code := "protocol"
	responseID := ""
	var failed error
	for event := range events {
		if event.Data == "[DONE]" && terminal {
			continue
		}
		var value struct {
			Type     string          `json:"type"`
			Index    *int            `json:"output_index"`
			Item     json.RawMessage `json:"item"`
			Response json.RawMessage `json:"response"`
		}
		if json.Unmarshal([]byte(event.Data), &value) != nil || value.Type == "" || event.Event != "" && event.Event != "message" && event.Event != value.Type || terminal {
			failed = ErrProtocol
			continue
		}
		if len(value.Response) > 0 {
			var identity struct {
				ID string `json:"id"`
			}
			if json.Unmarshal(value.Response, &identity) != nil {
				failed = ErrProtocol
				continue
			}
			if identity.ID != "" {
				if responseID != "" && identity.ID != responseID {
					failed = ErrProtocol
					continue
				}
				responseID = identity.ID
			}
		}
		switch value.Type {
		case "response.output_item.done":
			var item struct {
				ID string `json:"id"`
			}
			if len(value.Item) == 0 || json.Unmarshal(value.Item, &item) != nil {
				failed = ErrProtocol
				continue
			}
			index := nextIndex
			if value.Index != nil {
				index = *value.Index
			} else if existing, ok := itemIDs[item.ID]; item.ID != "" && ok {
				index = existing
			}
			if index < 0 || index >= 10000 {
				failed = ErrProtocol
				continue
			}
			if previous, exists := items[index]; exists {
				if !bytes.Equal(previous, value.Item) {
					failed = ErrProtocol
				}
				continue
			}
			total += len(value.Item)
			if total > MaxBodyBytes {
				failed = ErrProtocol
				continue
			}
			if item.ID != "" {
				if old, ok := itemIDs[item.ID]; ok && old != index {
					failed = ErrProtocol
					continue
				}
				itemIDs[item.ID] = index
			}
			items[index] = value.Item
			nextIndex = max(nextIndex, index+1)
		case "response.completed":
			terminal = true
			var response map[string]json.RawMessage
			if json.Unmarshal(value.Response, &response) != nil || response == nil {
				failed = ErrProtocol
				continue
			}
			if response["status"] == nil {
				response["status"] = json.RawMessage(`"completed"`)
			}
			output, e := mergeCodexOutput(response["output"], items)
			if e != nil {
				failed = ErrProtocol
				continue
			}
			response["output"], _ = json.Marshal(output)
			encoded, _ := json.Marshal(response)
			parsed, err := projectResponses(id, encoded, s)
			parsed.RawResponse = string(raw)
			out = parsed
			if err != nil {
				failed = err
			} else {
				completed = true
				code = ""
			}
		case "response.failed", "response.incomplete", "error":
			terminal = true
			failed = ErrProtocol
			code = "upstream_error"
			if value.Type == "response.incomplete" {
				code = "upstream_incomplete"
			}
			var response struct {
				Usage json.RawMessage `json:"usage"`
			}
			_ = json.Unmarshal(value.Response, &response)
			out.Usage = parseResponseUsage(response.Usage)
		}
	}
	for err := range errs {
		if err != nil {
			failed = ErrProtocol
		}
	}
	if !completed || failed != nil {
		if code == "" {
			code = "protocol"
		}
		return out, false, code, ErrProtocol
	}
	return out, true, "", nil
}

func mergeCodexOutput(terminal json.RawMessage, items map[int]json.RawMessage) ([]json.RawMessage, error) {
	var output []json.RawMessage
	if len(terminal) > 0 && json.Unmarshal(terminal, &output) != nil {
		return nil, ErrProtocol
	}
	if len(terminal) == 0 {
		keys := make([]int, 0, len(items))
		for i := range items {
			keys = append(keys, i)
		}
		sort.Ints(keys)
		output = make([]json.RawMessage, 0, len(keys))
		for i, index := range keys {
			if i != index {
				return nil, ErrProtocol
			}
			output = append(output, items[index])
		}
		return output, nil
	}
	for index, done := range items {
		if index >= len(output) {
			return nil, ErrProtocol
		}
		var earlier, current map[string]json.RawMessage
		if json.Unmarshal(done, &earlier) != nil || earlier == nil || json.Unmarshal(output[index], &current) != nil || current == nil {
			return nil, ErrProtocol
		}
		for k, v := range earlier {
			if final, exists := current[k]; exists {
				var a, b any
				da := json.NewDecoder(bytes.NewReader(v))
				da.UseNumber()
				db := json.NewDecoder(bytes.NewReader(final))
				db.UseNumber()
				if da.Decode(&a) != nil || db.Decode(&b) != nil {
					return nil, ErrProtocol
				}
				ca, _ := json.Marshal(a)
				cb, _ := json.Marshal(b)
				if !bytes.Equal(ca, cb) {
					return nil, ErrProtocol
				}
			} else {
				current[k] = v
			}
		}
		output[index], _ = json.Marshal(current)
	}
	return output, nil
}
