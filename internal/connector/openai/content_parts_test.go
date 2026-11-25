package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/upstreamerror"
)

const chatThinkingPart = `{"type":"thinking","thinking":[{"type":"text","text":"想"},{"type":"text","text":"法\n"}],"closed":false,"signature":null}`
const chatTextPart = `{"type":"text","text":"  答案 "}`

func TestChatContentPartsKeepAmbiguousOrOpaqueContent(t *testing.T) {
	for _, body := range []string{
		`{"content":null}`,
		`{"content":"ordinary"}`,
		`{"content":[{"type":"image_url","image_url":{"url":"url"}},` + chatTextPart + `]}`,
		`{"content":[{"type":"text","text":"citation","citations":[17]}]}`,
		`{"content":[{"type":"thinking","thinking":[{"type":"reference","reference_ids":[17]}]}]}`,
		`{"content":[{"type":"thinking","thinking":[],"signature":"opaque"}]}`,
		`{"content":[{"type":"thinking","thinking":[],"closed":"false"}]}`,
		`{"content":[{"type":"thinking","thinking":null}]}`,
		`{"content":[{"type":"text","text":"first","text":"second"}]}`,
		`{"content":[{"type":"text","text":null}]}`,
		`{"content":[` + chatThinkingPart + `],"reasoning_content":"different"}`,
		`{"content":[` + chatThinkingPart + `],"reasoning_content":{}}`,
	} {
		raw := strings.Replace(validCompletion, `{"role":"assistant","content":"ok"}`, body, 1)
		_, projected, err := validateCompletion([]byte(raw))
		if err != nil || projected != nil {
			t.Fatalf("unexpected projection for %s: %s, %v", body, projected, err)
		}
	}
}

func TestChatContentPartsWrappedCompletionAndTools(t *testing.T) {
	// Existing reasoning may duplicate the typed representation. A signed tool
	// and a separate opaque choice exercise independent choice projection.
	const call = `{"id":"c","type":"function","function":{"name":"lookup","arguments":"{}"},"extra_content":{"google":{"thought_signature":"opaque"}}}`
	const opaque = `{"type":"future","id":9007199254740993}`
	first := `{"index":0,"message":{"role":"assistant","content":[` + chatTextPart + "," + chatThinkingPart + "," + chatTextPart + `],"reasoning_content":"想法\n","tool_calls":[` + call + `],"extra_content":{"vendor":{"opaque":"kept"}}},"finish_reason":"tool_calls"}`
	second := `{"index":1,"message":{"content":[` + opaque + `]},"finish_reason":"stop"}`
	third := `{"index":2,"message":{"content":[]},"finish_reason":"stop"}`
	raw := `{"success":true,"data":{"id":"c","object":"chat.completion","created":1,"model":"m","choices":[` + first + "," + second + "," + third + `],"usage":{"prompt_tokens":2,"completion_tokens":3,"total_tokens":5}}}`
	response := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(raw))}
	sink := httptest.NewRecorder()
	adapter := Adapter{maxJSONResponseBytes: DefaultMaxJSONResponseBytes}
	result := adapter.nonStream(context.Background(), sink, response, nil)
	if !result.Success || !result.Usage.Present || result.Usage.OutputTokens != 3 {
		t.Fatalf("result = %+v", result)
	}
	var completion struct {
		Choices []struct {
			Message map[string]json.RawMessage
			Finish  string `json:"finish_reason"`
		}
	}
	if err := json.Unmarshal(sink.Body.Bytes(), &completion); err != nil || len(completion.Choices) != 3 {
		t.Fatalf("completion = %s (%v)", sink.Body, err)
	}
	choices := completion.Choices
	msg := choices[0].Message
	if string(msg["content"]) != `"  答案   答案 "` || string(msg["reasoning_content"]) != `"想法\n"` || choices[0].Finish != "tool_calls" ||
		!strings.Contains(string(msg["tool_calls"]), `"thought_signature":"opaque"`) || !strings.Contains(string(msg["extra_content"]), `"opaque":"kept"`) ||
		string(choices[1].Message["content"]) != "["+opaque+"]" || string(choices[2].Message["content"]) != `""` {
		t.Fatal(sink.Body.String())
	}
}

func TestChatContentPartsStreamTransition(t *testing.T) {
	chunk := func(message, finish string) string {
		return "data: " + responseIdentityChunk("c", message, finish) + "\n\n"
	}
	usage := "data: " + `{"id":"c","object":"chat.completion.chunk","created":1,"model":"m","choices":[],"usage":{"prompt_tokens":2,"completion_tokens":3,"total_tokens":5}}` + "\n\n"
	frames := []string{
		chunk(`{"role":"assistant","content":[`+chatThinkingPart+`]}`, "null"),
		chunk(`{"content":[{"type":"thinking","closed":null,"thinking":[]},`+chatTextPart+`]}`, "null"),
		chunk(`{"content":"后续。"}`, `"stop"`),
		usage, "data: [DONE]\n\n",
	}
	for _, flatten := range []bool{false, true} {
		result, wire := responseIdentityAttempt(t, frames, flatten, nil, "mock-credential")
		if !result.Success || !result.Usage.Present || result.Usage.OutputTokens != 3 || !strings.HasSuffix(wire, "data: [DONE]\n\n") ||
			strings.Contains(wire, `"content":[`) || strings.Count(wire, `"reasoning_content":"想法\n"`) != 1 ||
			strings.Count(wire, `"content":"  答案 "`) != 1 || strings.Count(wire, `"content":"后续。"`) != 1 {
			t.Fatalf("flatten=%t result=%+v wire=%s", flatten, result, wire)
		}
	}
}

func TestChatContentPartsGuardsAndProjectionBounds(t *testing.T) {
	sse := func(delta string) string { return "data: " + responseIdentityChunk("c", delta, "null") + "\n\n" }
	for _, flatten := range []bool{false, true} {
		frames := []string{sse(`{"content":"split-"}`), sse(`{"content":[{"type":"text","text":"key"}]}`), "data: [DONE]\n\n"}
		result, wire := responseIdentityAttempt(t, frames, flatten, nil, "split-key")
		if result.Success || !result.Committed || !strings.Contains(result.Diagnostic, "rejected") || strings.Contains(wire, "[DONE]") || strings.Contains(wire, `"key"`) {
			t.Fatalf("projected stream guard: %+v %s", result, wire)
		}
	}
	for _, flatten := range []bool{false, true} {
		raw := strings.Replace(validCompletion, `"ok"`, "["+chatThinkingPart+","+chatTextPart+"]", 1)
		guard := newResponseGuard([]byte("thinking"))
		sink := httptest.NewRecorder()
		response := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(raw))}
		result := (&Adapter{maxJSONResponseBytes: DefaultMaxJSONResponseBytes}).nonStreamWithPolicy(context.Background(), sink, response, guard, contract.AttemptPolicy{FlattenToolCalls: flatten}, upstreamerror.Context{})
		guard.Clear()
		if result.Success || result.Committed || sink.Body.Len() != 0 || !strings.Contains(result.Diagnostic, "rejected") {
			t.Fatalf("source guard: %+v %s", result, sink.Body)
		}
	}
	answer := strings.Repeat("<reply>&", 100)
	raw := strings.Replace(validCompletion, `"ok"`, `[{"type":"text","text":"`+answer+`"}]`, 1)
	sink := httptest.NewRecorder()
	bounded := &Adapter{maxJSONResponseBytes: int64(len(raw))}
	response := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(raw))}
	if result := bounded.nonStream(context.Background(), sink, response, nil); !result.Success || !strings.Contains(sink.Body.String(), answer) {
		t.Fatalf("markup exceeded the original budget: %+v", result)
	}
	// Several short input lines form one projected data line. This limit
	// applies even when the upstream ID was valid and needed no repair.
	message := fmt.Sprintf(`{"content":[{"type":"text","text":"%s"}]}`, strings.Repeat("x", 30))
	data := strings.ReplaceAll(responseIdentityChunk("c", message, "null"), ",", ",\ndata: ")
	result, wire := responseIdentityAttempt(t, []string{"data: " + data + "\n\n", "data: [DONE]\n\n"}, false, func(config *AdapterConfig) { config.MaxSSELineBytes = 90 }, "mock-credential")
	if result.Success || result.Committed || wire != "" || !strings.Contains(result.Diagnostic, "bounds") {
		t.Fatalf("projected line: %+v %s", result, wire)
	}
}
