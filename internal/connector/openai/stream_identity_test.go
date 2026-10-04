package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func responseIdentityChunk(id, delta, finish string) string {
	encoded, _ := json.Marshal(id)
	return `{"id":` + string(encoded) + `,"object":"chat.completion.chunk","created":1,"model":"upstream","choices":[{"index":0,"delta":` + delta + `,"finish_reason":` + finish + `}]}`
}

func responseIdentityFrames(id, usage string) []string {
	frames := []string{
		"data: " + responseIdentityChunk(id, `{"role":"assistant","content":"prefix"}`, `null`) + "\n\n",
		"data: " + responseIdentityChunk("reasoning-id", `{"reasoning_content":"thought","content":"suffix"}`, `null`) + "\n\n",
		"data: " + namedStreamTool + "\n\n",
		"data: " + namedStreamToolFinish + "\n\n",
	}
	if usage != "" {
		frames = append(frames, "data: "+`{"id":"usage-id","object":"chat.completion.chunk","created":1,"model":"upstream","choices":[],"usage":`+usage+"}\n\n")
	}
	return append(frames, "data: [DONE]\n\n")
}

func responseIdentityAttempt(t *testing.T, frames []string, flatten bool, config func(*AdapterConfig), secret string) (AttemptResult, string) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { writeSSE(w, frames...) }))
	defer server.Close()
	adapter := adapterForServer(t, server.URL, config, nil)
	writer := httptest.NewRecorder()
	result := adapter.AttemptWithPolicy(context.Background(), writer, testTarget(server.URL, []byte(secret), []byte("mock-cipher")), streamRequest(t), namedStreamPolicy(flatten))
	return result, writer.Body.String()
}

// Canonical comparison includes all payload and metadata fields except the
// response ID, covering text, reasoning, tool arguments, finish and usage.
func responseIdentityProjection(t *testing.T, body string, stable bool) (string, string) {
	t.Helper()
	var output strings.Builder
	var id string
	for _, line := range strings.Split(body, "\n") {
		if !strings.HasPrefix(line, "data: {") {
			continue
		}
		var root map[string]json.RawMessage
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &root); err != nil {
			t.Fatal(err)
		}
		var next string
		if json.Unmarshal(root["id"], &next) != nil || !validOpaqueText(next, maxResponseIDRunes, true) {
			t.Fatalf("invalid projected ID: %q", root["id"])
		}
		if id == "" {
			id = next
		}
		if stable && next != id {
			t.Fatalf("unstable IDs %q and %q", id, next)
		}
		delete(root, "id")
		encoded, err := json.Marshal(root)
		if err != nil {
			t.Fatal(err)
		}
		output.Write(encoded)
		output.WriteByte('\n')
	}
	return output.String(), id
}

func TestStreamResponseIdentityPreservesFullProjection(t *testing.T) {
	ids := []string{"", " leading", "trailing\u00a0", "control\nvalue", strings.Repeat("界", 513)}
	usages := []string{"", `{"prompt_tokens":7,"completion_tokens":11,"total_tokens":18}`, `{"prompt_tokens":-1,"completion_tokens":11,"total_tokens":10}`}
	seenIDs := make(map[string]bool)
	for _, flatten := range []bool{false, true} {
		for usageIndex, usage := range usages {
			baseline, ordinary := responseIdentityAttempt(t, responseIdentityFrames("valid-first", usage), flatten, nil, "mock-key")
			if !baseline.Success {
				t.Fatalf("baseline=%+v", baseline)
			}
			expected, _ := responseIdentityProjection(t, ordinary, false)
			if !strings.Contains(ordinary, `"id":"valid-first"`) || !strings.Contains(ordinary, `"reasoning_content":"thought"`) || !strings.Contains(ordinary, `"content":"prefix"`) {
				t.Fatalf("baseline payload=%q", ordinary)
			}
			for index, id := range ids {
				t.Run(fmt.Sprintf("flatten=%t/usage=%d/id=%d", flatten, usageIndex, index), func(t *testing.T) {
					result, body := responseIdentityAttempt(t, responseIdentityFrames(id, usage), flatten, nil, "mock-key")
					if !result.Success || result.Usage != baseline.Usage || !strings.HasSuffix(body, "data: [DONE]\n\n") {
						t.Fatalf("result=%+v body=%q", result, body)
					}
					actual, synthetic := responseIdentityProjection(t, body, true)
					if actual != expected {
						t.Fatalf("payload changed:\n%s\n%s", expected, actual)
					}
					if !strings.HasPrefix(synthetic, "chatcmpl-") || seenIDs[synthetic] {
						t.Fatalf("ID not unique to attempt: %q", synthetic)
					}
					seenIDs[synthetic] = true
				})
			}
		}
	}
}

func TestStreamResponseIdentityRejectsOriginalSecretReflection(t *testing.T) {
	const secret = "mock-sensitive-credential"
	for _, flatten := range []bool{false, true} {
		for _, split := range []bool{false, true} {
			t.Run(fmt.Sprintf("flatten=%t/split=%t", flatten, split), func(t *testing.T) {
				first := " " + secret
				frames := []string{}
				if split {
					first = " " + secret[:12]
				}
				frames = append(frames, "data: "+responseIdentityChunk(first, `{"content":"prefix"}`, `null`)+"\n\n")
				if split {
					frames = append(frames, "data: "+responseIdentityChunk(secret[12:], `{}`, `"stop"`)+"\n\n")
				}
				frames = append(frames, "data: [DONE]\n\n")
				result, body := responseIdentityAttempt(t, frames, flatten, nil, secret)
				if result.Success || result.Committed != split || !strings.Contains(result.Diagnostic, "rejected") || strings.Contains(body, secret) || strings.Contains(body, "[DONE]") {
					t.Fatalf("result=%+v body=%q", result, body)
				}
			})
		}
	}
}

func TestStreamResponseIdentityKeepsValidationStrict(t *testing.T) {
	initial := responseIdentityChunk(" ", `{"content":"prefix"}`, `null`)
	cases := []string{
		strings.Replace(initial, `"id":" "`, `"id":null`, 1),
		strings.Replace(initial, `"id":" "`, `"id":42`, 1),
		strings.Replace(initial, `"id":" "`, `"id":{}`, 1),
		strings.Replace(initial, `"id":" ",`, ``, 1),
		strings.Replace(initial, `"id":" "`, `"id":" ","id":"other"`, 1),
		strings.Replace(initial, `"created":1`, `"created":-1`, 1),
		strings.Replace(initial, `"created":1`, `"created":"1"`, 1),
		strings.Replace(initial, `"model":"upstream"`, `"model":false`, 1),
		strings.Replace(initial, `"object":"chat.completion.chunk"`, `"object":"wrong"`, 1),
		strings.Replace(initial, `"index":0`, `"index":"0"`, 1),
		strings.Replace(initial, `"delta":{"content":"prefix"}`, `"delta":null`, 1),
		initial + "trailing",
	}
	for index, bad := range cases {
		t.Run(fmt.Sprint(index), func(t *testing.T) {
			state := streamResponseIdentity{}
			compact, _, _, err := validateStreamChunk([]byte(bad), &state, nil)
			clear(compact)
			if err == nil || state.seen || state.id != "" {
				t.Fatalf("accepted malformed field: err=%v state=%+v", err, state)
			}
		})
	}
	for _, first := range []string{"valid", " "} {
		state := streamResponseIdentity{}
		compact, _, _, err := validateStreamChunk([]byte(responseIdentityChunk(first, `{}`, `null`)), &state, nil)
		clear(compact)
		if err != nil {
			t.Fatal(err)
		}
		compact, _, _, err = validateStreamChunk([]byte(responseIdentityChunk(" ", `{}`, `null`)), &state, nil)
		clear(compact)
		if (err == nil) != (first == " ") {
			t.Fatalf("later invalid string: first=%q err=%v", first, err)
		}
		for _, raw := range []string{"null", "42", "{}"} {
			next := strings.Replace(initial, `"id":" "`, `"id":`+raw, 1)
			compact, _, _, err = validateStreamChunk([]byte(next), &state, nil)
			clear(compact)
			if err == nil {
				t.Fatalf("later ID %s accepted", raw)
			}
		}
	}
}

func TestStreamResponseIdentityKeepsTerminationAndBounds(t *testing.T) {
	first := "data: " + responseIdentityChunk(" ", `{"content":"prefix"}`, `null`) + "\n\n"
	for _, flatten := range []bool{false, true} {
		for _, terminal := range []string{"", "event: error\ndata: {\"error\":{\"message\":\"unavailable\"}}\n\n"} {
			result, body := responseIdentityAttempt(t, []string{first, terminal}, flatten, nil, "mock-key")
			if result.Success || !result.Committed || result.Usage.Present || strings.Contains(body, "[DONE]") || !strings.Contains(body, `"error"`) {
				t.Fatalf("result=%+v body=%q", result, body)
			}
		}
		result, body := responseIdentityAttempt(t, []string{first, "data: [DONE]\n\n"}, flatten, func(c *AdapterConfig) {
			c.MaxSSEEventBytes = len(strings.TrimSuffix(strings.TrimPrefix(first, "data: "), "\n\n"))
		}, "mock-key")
		if result.Success || result.Committed || !strings.Contains(result.Diagnostic, "bounds") || body != "" {
			t.Fatalf("enlarged event bypassed bound: result=%+v body=%q", result, body)
		}
	}
	state := streamResponseIdentity{id: "chatcmpl-fixed"}
	if !state.withinBounds([]byte("1234"), 4, 10, 38) || !state.withinBounds([]byte("1234"), 4, 10, 38) || state.withinBounds([]byte("1"), 4, 10, 38) {
		t.Fatal("cumulative repaired frame budget was not enforced")
	}
}

func TestStreamResponseIdentityCancellationRemainsLive(t *testing.T) {
	for _, flatten := range []bool{false, true} {
		t.Run(fmt.Sprintf("flatten=%t", flatten), func(t *testing.T) {
			upstreamCanceled := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				writeSSE(w, "data: "+responseIdentityChunk(" ", `{"content":"prefix"}`, `null`)+"\n\n")
				<-r.Context().Done()
				close(upstreamCanceled)
			}))
			defer server.Close()
			adapter := adapterForServer(t, server.URL, nil, nil)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			writer := &signalingRecorder{ResponseRecorder: httptest.NewRecorder(), wrote: make(chan struct{})}
			done := make(chan AttemptResult, 1)
			request := streamRequest(t)
			go func() {
				done <- adapter.AttemptWithPolicy(ctx, writer, testTarget(server.URL, []byte("mock-key"), []byte("mock-cipher")), request, namedStreamPolicy(flatten))
			}()
			select {
			case <-writer.wrote:
			case <-time.After(2 * time.Second):
				cancel()
				t.Fatal("initial repaired content was buffered")
			}
			cancel()
			select {
			case result := <-done:
				if result.Success || result.Failure != FailureCanceled || !result.Committed || result.Usage.Present {
					t.Fatalf("result=%+v", result)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("adapter failed to cancel")
			}
			select {
			case <-upstreamCanceled:
			case <-time.After(2 * time.Second):
				t.Fatal("upstream failed to cancel")
			}
			if !strings.Contains(writer.Body.String(), `"content":"prefix"`) || strings.Contains(writer.Body.String(), "[DONE]") || strings.Contains(writer.Body.String(), `"error"`) {
				t.Fatal(writer.Body.String())
			}
		})
	}
}
