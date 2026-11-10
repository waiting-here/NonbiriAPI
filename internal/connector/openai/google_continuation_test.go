package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGoogleChatSignaturesSurviveJSONAndStreamRoundTrip(t *testing.T) {
	const message = `{"role":"assistant","content":"checking","extra_content":{"google":{"thought_signature":"bWVzc2FnZQ==","future_field":{"enabled":true}},"custom_vendor":{"opaque":"message-extension"}},"tool_calls":[{"id":"call_1","type":"function","function":{"name":"clock","arguments":"{}"},"extra_content":{"google":{"thought_signature":"dG9vbA==","future_field":[1,2]},"custom_vendor":{"opaque":"tool-extension"}}}]}`
	const messages = `[` + message + `,{"role":"tool","tool_call_id":"call_1","content":"done"},{"role":"user","content":"continue"}]`
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprint(stream), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var sent map[string]json.RawMessage
				if err := json.NewDecoder(r.Body).Decode(&sent); err != nil {
					t.Error(err)
				}
				if string(sent["messages"]) != messages {
					t.Error("Google continuation history changed before dispatch")
				}
				if stream {
					w.Header().Set("Content-Type", "text/event-stream")
					delta := strings.Replace(message, `"id":"call_1"`, `"index":0,"id":"call_1"`, 1)
					fmt.Fprintf(w, "data: {\"id\":\"chat\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"gemini\",\"choices\":[{\"index\":0,\"delta\":%s,\"finish_reason\":null}]}\n\n", delta)
					fmt.Fprint(w, "data: {\"id\":\"chat\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"gemini\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"tool_calls\"}],\"usage\":{\"prompt_tokens\":2,\"completion_tokens\":3,\"total_tokens\":5}}\n\ndata: [DONE]\n\n")
				} else {
					w.Header().Set("Content-Type", "application/json")
					fmt.Fprintf(w, `{"id":"chat","object":"chat.completion","created":1,"model":"gemini","choices":[{"index":0,"message":%s,"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":2,"completion_tokens":3,"total_tokens":5}}`, message)
				}
			}))
			defer server.Close()
			request := decodeAdapterRequest(t, fmt.Sprintf(`{"model":"public","messages":%s,"stream":%t}`, messages, stream))
			recorder := httptest.NewRecorder()
			result := adapterForServer(t, server.URL, nil, nil).Attempt(context.Background(), recorder, testTarget(server.URL, []byte("fixture-key"), nil), request, "nbu_fixture")
			if !result.Success || !result.Committed {
				t.Fatalf("result=%+v body=%s", result, recorder.Body.String())
			}
			for _, signature := range []string{"bWVzc2FnZQ==", "dG9vbA=="} {
				if !strings.Contains(recorder.Body.String(), `"thought_signature":"`+signature+`"`) {
					t.Fatal("Google continuation signature lost in response")
				}
			}
			for _, extension := range []string{`"future_field":{"enabled":true}`, `"future_field":[1,2]`, `"opaque":"message-extension"`, `"opaque":"tool-extension"`} {
				if !strings.Contains(recorder.Body.String(), extension) {
					t.Fatal("opaque Chat extension lost in response")
				}
			}
		})
	}
}
