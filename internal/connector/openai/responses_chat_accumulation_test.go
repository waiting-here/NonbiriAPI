package openai

import (
	"strings"
	"testing"
)

func TestResponseChatBuffersPreserveFragmentsAndCallGrowth(t *testing.T) {
	state := responseChatState{model: "public", gatewayReasoning: true}
	consume := func(delta string) {
		t.Helper()
		raw := strings.TrimSuffix(strings.TrimPrefix(chatResponseChunk(delta, "null", "null"), "data: "), "\n\n")
		if err := state.consume([]byte(raw), true); err != nil {
			t.Fatal(err)
		}
	}
	consume(`{"content":"你","refusal":"不","reasoning_content":"想","tool_calls":[{"index":0,"id":"first","type":"function","function":{"name":"lookup","arguments":"{\"query\":\""}}]}`)
	text, refusal, reasoning, arguments := state.text, state.refusal, state.reasoning, state.calls[0].arguments
	// Growing the call slice must not copy an active string builder.
	consume(`{"content":"好","refusal":"能","reasoning_content":"法","tool_calls":[{"index":1,"id":"second","type":"function","function":{"name":"other","arguments":"{}"}},{"index":0,"function":{"arguments":"世界\"}"}}]}`)
	if text != "你" || refusal != "不" || reasoning != "想" || arguments != `{"query":"` {
		t.Fatal("appending changed a retained prefix")
	}
	if state.text != "你好" || state.refusal != "不能" || state.reasoning != "想法" || len(state.calls) != 2 ||
		state.calls[0].arguments != `{"query":"世界"}` || state.calls[1].arguments != "{}" {
		t.Fatal("fragmented channels or tool arguments changed")
	}
	state.clear()
	consume(`{"content":"new","tool_calls":[{"index":0,"id":"new","type":"function","function":{"name":"lookup","arguments":"{}"}}]}`)
	if state.text != "new" || state.refusal != "" || state.reasoning != "" || len(state.calls) != 1 || state.calls[0].arguments != "{}" {
		t.Fatal("cleared state retained old content")
	}
	state.clear()
}
