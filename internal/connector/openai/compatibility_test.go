package openai

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
)

func TestChatReasoningControlsReachMockUpstreamUnchanged(t *testing.T) {
	tests := []struct {
		name       string
		extra      string
		stream     bool
		exclude    []string
		forceStore bool
		want       map[string]string
		absent     []string
	}{
		{
			name:  "anthropic-style thinking remains a client extension",
			extra: `,"thinking":{"type":"enabled","budget_tokens":4096},"vendor":{"nested":[1,{"x":true}]}`,
			want:  map[string]string{"thinking": `{"type":"enabled","budget_tokens":4096}`, "vendor": `{"nested":[1,{"x":true}]}`},
		},
		{
			name:  "reasoning effort",
			extra: `,"reasoning_effort":"low"`,
			want:  map[string]string{"reasoning_effort": `"low"`}, absent: []string{"thinking", "reasoning"},
		},
		{
			name:   "unified reasoning effort in stream",
			extra:  `,"reasoning":{"effort":"medium"},"stream_options":{"include_usage":false,"vendor":1},"store":true`,
			stream: true, forceStore: true,
			want:   map[string]string{"reasoning": `{"effort":"medium"}`, "stream_options": `{"include_usage":true,"vendor":1}`, "store": `false`},
			absent: []string{"thinking", "reasoning_effort"},
		},
		{
			name:   "null stream options still collect usage",
			extra:  `,"stream_options":null`,
			stream: true,
			want:   map[string]string{"stream_options": `{"include_usage":true}`},
		},
		{
			name:   "no reasoning control is invented",
			absent: []string{"thinking", "reasoning", "reasoning_effort"},
		},
		{
			name:    "top-level exclusion removes thinking",
			extra:   `,"thinking":{"type":"enabled","budget_tokens":4096}`,
			exclude: []string{"thinking"}, absent: []string{"thinking", "reasoning", "reasoning_effort"},
		},
		{
			name:  "excluded store stays excluded under force policy",
			extra: `,"store":true`, exclude: []string{"store"}, forceStore: true,
			absent: []string{"store"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var sent []byte
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				if request.URL.Path != "/v1/chat/completions" {
					t.Errorf("outbound path = %q", request.URL.Path)
				}
				if request.Header.Get("Authorization") != "Bearer mock-upstream-key" {
					t.Error("outbound authorization was not the target credential")
				}
				var err error
				sent, err = io.ReadAll(request.Body)
				if err != nil {
					t.Errorf("read outbound body: %v", err)
				}
				if test.stream {
					writeSSE(writer,
						"data: "+validChunk+"\n\n",
						`data: {"id":"finish","object":"chat.completion.chunk","created":1,"model":"upstream","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`+"\n\n",
						"data: [DONE]\n\n")
				} else {
					writer.Header().Set("Content-Type", "application/json")
					_, _ = io.WriteString(writer, validCompletion)
				}
			}))
			defer server.Close()
			body := `{"model":"public/model","messages":[{"role":"user","content":"hello"}],"stream":` +
				map[bool]string{true: "true", false: "false"}[test.stream] + test.extra + `}`
			request := decodeAdapterRequest(t, body)
			if err := request.ExcludeFields(test.exclude); err != nil {
				t.Fatal(err)
			}
			adapter := adapterForServer(t, server.URL, nil, nil)
			result := adapter.AttemptWithPolicy(context.Background(), httptest.NewRecorder(),
				testTarget(server.URL+"/v1", []byte("mock-upstream-key"), nil), request,
				connectorcontract.AttemptPolicy{SafetyIdentifier: "nbu_authoritative", ForceStoreFalse: test.forceStore})
			if !result.Success {
				t.Fatalf("attempt failed: %+v", result)
			}
			var got map[string]json.RawMessage
			if err := json.Unmarshal(sent, &got); err != nil {
				t.Fatalf("outbound JSON: %v", err)
			}
			for name, wantJSON := range test.want {
				var wantValue, gotValue any
				if json.Unmarshal([]byte(wantJSON), &wantValue) != nil || json.Unmarshal(got[name], &gotValue) != nil || !reflect.DeepEqual(gotValue, wantValue) {
					t.Errorf("outbound %s = %s, want %s", name, got[name], wantJSON)
				}
			}
			for _, name := range test.absent {
				if _, ok := got[name]; ok {
					t.Errorf("outbound %s was unexpectedly present", name)
				}
			}
			if string(got["model"]) != `"upstream/model"` || string(got["safety_identifier"]) != `"nbu_authoritative"` || !strings.Contains(string(got["messages"]), "hello") {
				t.Error("model, input, or safety authority was lost")
			}
		})
	}
}

func TestContradictoryOpenAIUsageIsUnknown(t *testing.T) {
	for _, raw := range []string{
		`{"prompt_tokens":3,"completion_tokens":4,"total_tokens":6}`,
		`{"prompt_tokens":3,"completion_tokens":4,"total_tokens":8}`,
		`{"prompt_tokens":9223372036854775807,"completion_tokens":1,"total_tokens":9223372036854775807}`,
	} {
		t.Run(raw, func(t *testing.T) {
			if usage, err := parseUsage([]byte(raw)); !errors.Is(err, errUsageMalformed) || usage.Present {
				t.Fatalf("contradictory usage was trusted: %+v, %v", usage, err)
			}
			completion := []byte(`{"id":"x","object":"chat.completion","created":1,"model":"m","choices":[{"index":0,"message":{}}],"usage":` + raw + `}`)
			if usage, err := validateCompletion(completion); err != nil || usage.Present {
				t.Fatalf("completion usage = %+v, %v", usage, err)
			}
			chunk := []byte(`{"id":"x","object":"chat.completion.chunk","created":1,"model":"m","choices":[],"usage":` + raw + `}`)
			_, usage, malformed, err := validateChunk(chunk)
			if err != nil || !malformed || usage.Present {
				t.Fatalf("stream usage = %+v, malformed %v, err %v", usage, malformed, err)
			}
		})
	}
}

func TestStreamRejectsUsageOnlyBeforeOutput(t *testing.T) {
	const usageOnly = `{"id":"x","object":"chat.completion.chunk","created":1,"model":"m","choices":[],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`
	for _, flatten := range []bool{false, true} {
		t.Run(map[bool]string{true: "flatten", false: "ordinary"}[flatten], func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writeSSE(writer, "data: "+usageOnly+"\n\n", "data: [DONE]\n\n")
			}))
			defer server.Close()
			adapter := adapterForServer(t, server.URL, nil, nil)
			recorder := httptest.NewRecorder()
			result := adapter.AttemptWithPolicy(context.Background(), recorder,
				testTarget(server.URL, []byte("mock-upstream-key"), nil), streamRequest(t),
				connectorcontract.AttemptPolicy{SafetyIdentifier: "nbu_authoritative", FlattenToolCalls: flatten})
			if result.Success || result.Failure != FailureUpstream || result.Committed {
				t.Fatalf("premature stream success: %+v, body=%q", result, recorder.Body.String())
			}
			if strings.Contains(recorder.Body.String(), "data: [DONE]") {
				t.Fatalf("invalid completion was forwarded as successful: %q", recorder.Body.String())
			}
		})
	}
}

func TestStreamUsageOptionAuthorityWithNullAndMalformedValues(t *testing.T) {
	request, err := DecodeChatRequest(strings.NewReader(`{"model":"p/m","stream":true,"stream_options":null}`), MaxRequestBodyBytes)
	if err != nil {
		t.Fatal(err)
	}
	defer request.Clear()
	body, err := request.marshalUpstream("upstream", "nbu_safe")
	if err != nil {
		t.Fatal(err)
	}
	defer clear(body)
	var fields map[string]json.RawMessage
	if json.Unmarshal(body, &fields) != nil || string(fields["stream_options"]) != `{"include_usage":true}` {
		t.Fatalf("stream usage option was not authoritative: %s", body)
	}
	for _, option := range []string{`"wrong"`, `[]`, `123`} {
		invalid := `{"model":"p/m","stream":true,"stream_options":` + option + `}`
		if decoded, err := DecodeChatRequest(strings.NewReader(invalid), MaxRequestBodyBytes); err == nil {
			decoded.Clear()
			t.Fatalf("invalid stream_options accepted: %s", invalid)
		}
	}
}

func TestStreamAcceptsUsageBeforeValidatedOutput(t *testing.T) {
	const usageOnly = `{"id":"x","object":"chat.completion.chunk","created":1,"model":"m","choices":[],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`
	for _, flatten := range []bool{false, true} {
		t.Run(map[bool]string{true: "flatten", false: "ordinary"}[flatten], func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writeSSE(writer, "data: "+usageOnly+"\n\n", "data: "+validChunk+"\n\n", "data: [DONE]\n\n")
			}))
			defer server.Close()
			adapter := adapterForServer(t, server.URL, nil, nil)
			recorder := httptest.NewRecorder()
			result := adapter.AttemptWithPolicy(context.Background(), recorder,
				testTarget(server.URL, []byte("mock-upstream-key"), nil), streamRequest(t),
				connectorcontract.AttemptPolicy{SafetyIdentifier: "nbu_authoritative", FlattenToolCalls: flatten})
			if !result.Success || !result.Committed || !result.Usage.Present || result.Usage.UncachedInputTokens != 1 || result.Usage.OutputTokens != 1 {
				t.Fatalf("valid output after leading usage rejected: %+v", result)
			}
			if !strings.Contains(recorder.Body.String(), usageOnly) || !strings.Contains(recorder.Body.String(), "data: [DONE]") {
				t.Fatalf("validated stream lost usage or completion: %q", recorder.Body.String())
			}
		})
	}
}
