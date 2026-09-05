package openai

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
)

func TestStreamCumulativeUsageAcrossForwardingPolicies(t *testing.T) {
	for _, flatten := range []bool{false, true} {
		for _, terminal := range []bool{false, true} {
			t.Run(fmt.Sprintf("flatten_%v_terminal_usage_%v", flatten, terminal), func(t *testing.T) {
				frames := []string{}
				for output := 1; output <= 128; output++ {
					frames = append(frames, fmt.Sprintf("data: {\"id\":\"cumulative\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"upstream\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"x\"},\"finish_reason\":null}],\"usage\":{\"prompt_tokens\":31,\"completion_tokens\":%d,\"total_tokens\":%d}}\n\n", output, 31+output))
				}
				frames = append(frames, `data: {"id":"cumulative","object":"chat.completion.chunk","created":1,"model":"upstream","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`+"\n\n")
				if terminal {
					frames = append(frames, `data: {"id":"cumulative","object":"chat.completion.chunk","created":1,"model":"upstream","choices":[],"usage":{"prompt_tokens":31,"completion_tokens":128,"total_tokens":159}}`+"\n\n")
				}
				frames = append(frames, "data: [DONE]\n\n")
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { writeSSE(w, frames...) }))
				defer server.Close()
				adapter := adapterForServer(t, server.URL, nil, nil)
				recorder := httptest.NewRecorder()
				result := adapter.AttemptWithPolicy(context.Background(), recorder, testTarget(server.URL, []byte("sk-cumulative"), []byte("cipher-cumulative")), streamRequest(t), connectorcontract.AttemptPolicy{SafetyIdentifier: "nbu_cumulative", FlattenToolCalls: flatten})
				if !result.Success || !result.Usage.Present || result.Usage.UncachedInputTokens != 31 || result.Usage.OutputTokens != 128 {
					t.Fatalf("cumulative result=%+v", result)
				}
				if !strings.HasSuffix(recorder.Body.String(), "data: [DONE]\n\n") {
					t.Fatal("terminal marker missing")
				}
				if flatten && strings.Count(recorder.Body.String(), `"usage"`) != 1 {
					t.Fatal("flattening must emit only the last cumulative usage snapshot")
				}
			})
		}
	}
}

func TestCumulativeUsageFailuresAndToolConversion(t *testing.T) {
	cases := []struct {
		name   string
		middle string
		finish bool
		known  bool
		output int64
	}{
		{"cumulative", `{"prompt_tokens":31,"completion_tokens":12,"total_tokens":43}`, true, true, 14},
		{"duplicate", `{"prompt_tokens":31,"completion_tokens":10,"total_tokens":41}`, true, true, 14},
		{"missing", `null`, true, true, 14},
		{"regression", `{"prompt_tokens":31,"completion_tokens":9,"total_tokens":40}`, true, false, 0},
		{"input-conflict", `{"prompt_tokens":32,"completion_tokens":12,"total_tokens":44}`, true, false, 0},
		{"malformed", `{"prompt_tokens":31,"completion_tokens":-1,"total_tokens":30}`, true, false, 0},
		{"truncated", `{"prompt_tokens":31,"completion_tokens":12,"total_tokens":43}`, false, true, 14},
	}
	for _, flatten := range []bool{false, true} {
		for _, test := range cases {
			t.Run(fmt.Sprintf("%s_flatten_%v", test.name, flatten), func(t *testing.T) {
				tool := strings.TrimSuffix(policyToolChunk, "}") + `,"usage":{"prompt_tokens":31,"completion_tokens":10,"total_tokens":41}}`
				chunk := func(usage string) string {
					return `data: {"id":"flatten-tool","object":"chat.completion.chunk","created":1,"model":"upstream","choices":[],"usage":` + usage + "}\n\n"
				}
				frames := []string{"data: " + tool + "\n\n", chunk(test.middle), chunk(`{"prompt_tokens":31,"completion_tokens":14,"total_tokens":45}`)}
				if test.finish {
					frames = append(frames, `data: {"id":"flatten-tool","object":"chat.completion.chunk","created":1,"model":"upstream","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`+"\n\n", "data: [DONE]\n\n")
				}
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { writeSSE(w, frames...) }))
				defer server.Close()
				adapter := adapterForServer(t, server.URL, nil, nil)
				recorder := httptest.NewRecorder()
				result := adapter.AttemptWithPolicy(context.Background(), recorder, testTarget(server.URL, []byte("sk-cumulative"), []byte("cipher-cumulative")), streamRequest(t), connectorcontract.AttemptPolicy{SafetyIdentifier: "nbu_cumulative", FlattenToolCalls: flatten})
				if result.Success != test.finish || result.Usage.Present != test.known || result.Usage.OutputTokens != test.output {
					t.Fatalf("result=%+v", result)
				}
				if flatten && test.finish {
					if strings.Contains(recorder.Body.String(), `"tool_calls":`) || strings.Count(recorder.Body.String(), `"usage"`) != 1 {
						t.Fatal("converted stream exposed structured tool deltas or repeated usage")
					}
				}
			})
		}
	}
}

func TestFlattenUsageGuardsInspectEachRepresentationOnce(t *testing.T) {
	for _, leak := range []bool{false, true} {
		t.Run(fmt.Sprintf("leak_%v", leak), func(t *testing.T) {
			const part = "unique-part-"
			frames := []string{}
			if leak {
				// Both snapshots are read, even though only the last one is emitted.
				for output := 1; output <= 2; output++ {
					frames = append(frames, fmt.Sprintf("data: {\"id\":\"guards\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"upstream\",\"choices\":[],\"metadata\":\"%s\",\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":%d,\"total_tokens\":%d}}\n\n", part, output, 1+output))
				}
			} else {
				frames = append(frames, `data: {"id":"guards","object":"chat.completion.chunk","created":1,"model":"upstream","choices":[{"index":0,"delta":{"content":"unique-part-"},"finish_reason":null}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`+"\n\n")
			}
			frames = append(frames, `data: {"id":"guards","object":"chat.completion.chunk","created":1,"model":"upstream","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`+"\n\n", "data: [DONE]\n\n")
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { writeSSE(w, frames...) }))
			defer server.Close()
			adapter := adapterForServer(t, server.URL, nil, nil)
			recorder := httptest.NewRecorder()
			result := adapter.AttemptWithPolicy(context.Background(), recorder, testTarget(server.URL, []byte(part+part), []byte("cipher-guard")), streamRequest(t), connectorcontract.AttemptPolicy{SafetyIdentifier: "nbu_guard", FlattenToolCalls: true})
			if result.Success == leak {
				t.Fatalf("unexpected guard result: %+v", result)
			}
			if strings.Contains(recorder.Body.String(), part+part) {
				t.Fatal("credential escaped the source guard")
			}
		})
	}
}
