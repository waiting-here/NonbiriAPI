package tooltext

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestStreamBuffersPreserveByteOffsetsAndCleanup(t *testing.T) {
	states := make(map[int]*StreamChoiceState)
	consume := func(delta, finish string) map[string]json.RawMessage {
		t.Helper()
		root, _, err := AccumulateStreamChunk([]byte(fmt.Sprintf(`{"choices":[{"index":0,"delta":%s,"finish_reason":%s}]}`, delta, finish)), states)
		if err != nil {
			t.Fatal(err)
		}
		return root
	}
	consume(`{"content":"前缀🙂"}`, "null")
	prefix := states[0].Content
	MarkStreamContentEmitted(states)
	consume(`{"content":"后","tool_calls":[{"index":0,"id":"call","type":"function","function":{"name":"lookup","arguments":"{\"query\":\""}}]}`, "null")
	arguments := states[0].Tools[0].Arguments
	root := consume(`{"content":"续","tool_calls":[{"index":0,"function":{"arguments":"世界\"}"}}]}`, `"tool_calls"`)
	body, err := StreamCompletionBodyAfter(root, states)
	if err != nil {
		t.Fatal(err)
	}
	var completion struct {
		Choices []struct {
			Message struct {
				Content string
				Calls   []struct {
					Function struct{ Arguments string }
				} `json:"tool_calls"`
			}
		}
	}
	if err = json.Unmarshal(body, &completion); err != nil {
		t.Fatal(err)
	}
	message := completion.Choices[0].Message
	if prefix != "前缀🙂" || arguments != `{"query":"` || message.Content != "后续" ||
		len(message.Calls) != 1 || message.Calls[0].Function.Arguments != `{"query":"世界"}` {
		t.Fatalf("prefix or terminal suffix changed: %s", body)
	}
	tool := states[0].Tools[0]
	ClearStreamStates(states)
	if states[0].Content != "" || states[0].Tools != nil || tool.ID != "" || tool.Name != "" || tool.Arguments != "" ||
		states[0].contentBuffer != nil || tool.argumentsBuffer != nil {
		t.Fatal("clear retained buffered values")
	}
	// Cleanup ends the old stream; a new stream starts with new metadata.
	states = make(map[int]*StreamChoiceState)
	consume(`{"content":"fresh","tool_calls":[{"index":0,"id":"call","type":"function","function":{"name":"lookup","arguments":"{}"}}]}`, "null")
	if states[0].Content != "fresh" || states[0].Tools[0].Arguments != "{}" {
		t.Fatal("cleared accumulator appended to old data")
	}
}

func TestStreamArgumentBufferRejectsOverflowBeforeAppend(t *testing.T) {
	states := make(map[int]*StreamChoiceState)
	consume := func(arguments string) error {
		raw, err := json.Marshal(map[string]any{"choices": []any{map[string]any{
			"index": 0, "delta": map[string]any{"tool_calls": []any{map[string]any{
				"index": 0, "function": map[string]any{"arguments": arguments},
			}}},
		}}})
		if err != nil {
			t.Fatal(err)
		}
		_, _, err = AccumulateStreamChunk(raw, states)
		return err
	}
	if err := consume(strings.Repeat("x", MaxFlattenArguments)); err != nil {
		t.Fatal(err)
	}
	if err := consume("y"); !errors.Is(err, ErrToolFlatten) {
		t.Fatalf("overflow=%v", err)
	}
	if len(states[0].Tools[0].Arguments) != MaxFlattenArguments {
		t.Fatal("rejected bytes were retained")
	}
	ClearStreamStates(states)
}
