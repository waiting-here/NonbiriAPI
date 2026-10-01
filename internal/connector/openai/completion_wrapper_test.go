package openai

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
)

func TestCompletionWrapperExactShapeAndUsage(t *testing.T) {
	extended := strings.TrimSuffix(validCompletion, "}") + ",\"success\":false,\"data\":{\"extra\":true}}"
	for _, tc := range []struct {
		name, body string
		ok         bool
	}{
		{"ordinary", validCompletion, true},
		{"ordinary-extensions", extended, true},
		{"wrapper-inner-extensions", "{\"success\":true,\"data\":" + extended + "}", true},
		{"wrapper", `{"success":true,"data":` + validCompletion + "}", true},
		{"duplicate", `{"success":true,"success":true,"data":` + validCompletion + "}", false},
		{"false", `{"success":false,"data":` + validCompletion + "}", false},
		{"unknown", `{"success":true,"data":` + validCompletion + `,"meta":{}}`, false},
		{"error", `{"success":true,"data":` + validCompletion + `,"error":null}`, false},
		{"conflict", `{"success":true,"data":` + validCompletion + `,"choices":[]}`, false},
		{"nested", `{"success":true,"data":{"success":true,"data":` + validCompletion + "}}", false},
		{"null", `{"success":true,"data":null}`, false},
		{"malformed-inner", `{"success":true,"data":{"object":"chat.completion"}}`, false},
		{"inner-error", `{"success":true,"data":{"error":{},"choices":[]}}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			adapter := adapterForServer(t, server.URL, nil, nil)
			request := decodeAdapterRequest(t, `{"model":"public/model","messages":[{"role":"user","content":"hi"}]}`)
			defer request.Clear()
			out := httptest.NewRecorder()
			result := adapter.Attempt(context.Background(), out, NewTarget(server.URL, "private", NewCredential([]byte("credential-material-long"), []byte("ciphertext-material-long"))), request, "nbu_test")
			if result.Success != tc.ok {
				t.Fatalf("result=%+v body=%s", result, out.Body)
			}
			if tc.ok {
				expectedUsage, _ := validateCompletion([]byte(validCompletion))
				if result.Usage != expectedUsage || result.StreakDisposition != contract.StreakSuccess || result.FailureOrigin != contract.OriginNone {
					t.Fatalf("facts=%+v", result)
				}
				var fields map[string]json.RawMessage
				if json.Unmarshal(out.Body.Bytes(), &fields) != nil || fields["choices"] == nil {
					t.Fatal(out.Body.String())
				}
				if (tc.name == "ordinary-extensions" || tc.name == "wrapper-inner-extensions") && out.Body.String() != extended {
					t.Fatalf("standard completion extensions changed: %s", out.Body)
				}
			} else if out.Body.Len() != 0 || result.StreakDisposition != contract.StreakUpstreamFailure {
				t.Fatalf("invalid output=%s facts=%+v", out.Body, result)
			}
		})
	}
}

type failedCompletionWriter struct{ header http.Header }

func (w *failedCompletionWriter) Header() http.Header       { return w.header }
func (w *failedCompletionWriter) WriteHeader(int)           {}
func (w *failedCompletionWriter) Write([]byte) (int, error) { return 0, errors.New("disconnected") }
func TestCompletionProtocolSuccessSurvivesDownstreamFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(validCompletion))
	}))
	defer server.Close()
	a := adapterForServer(t, server.URL, nil, nil)
	r := decodeAdapterRequest(t, `{"model":"public/model","messages":[{"role":"user","content":"hi"}]}`)
	defer r.Clear()
	result := a.Attempt(context.Background(), &failedCompletionWriter{header: make(http.Header)}, NewTarget(server.URL, "private", NewCredential([]byte("credential-material-long"), nil)), r, "nbu_test")
	if result.Success || result.Failure != contract.FailureSink || result.StreakDisposition != contract.StreakSuccess || result.UpstreamStatus != 200 {
		t.Fatalf("%+v", result)
	}
}
func TestFlattenSinkHoldsToolsUntilValidatedTerminal(t *testing.T) {
	for _, complete := range []bool{false, true} {
		out := httptest.NewRecorder()
		sink := NewFlattenSink(out, 1<<20, 1<<20, nil)
		defer sink.Clear()
		for _, data := range []string{
			`{"id":"c","object":"chat.completion.chunk","created":1,"model":"public/model","choices":[{"index":0,"delta":{"role":"assistant","content":"before "},"finish_reason":null}]}`,
			`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call","type":"function","function":{"name":"lookup","arguments":"{}"}}]},"finish_reason":null}]}`,
		} {
			if _, err := sink.Write([]byte("data: " + data + "\n\n")); err != nil {
				t.Fatal(err)
			}
		}
		if strings.Contains(out.Body.String(), "mx_tool") || !strings.Contains(out.Body.String(), "before ") {
			t.Fatal(out.Body.String())
		}
		if !complete {
			if sink.Complete(time.Second) == nil {
				t.Fatal("incomplete tool emitted")
			}
			continue
		}
		_, _ = sink.Write([]byte("data: " + `{"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}` + "\n\n"))
		_, _ = sink.Write([]byte("data: [DONE]\n\n"))
		if err := sink.Complete(time.Second); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out.Body.String(), "mx_tool") || strings.Contains(out.Body.String(), "tool_calls") || strings.Count(out.Body.String(), "before ") != 1 || !strings.HasSuffix(out.Body.String(), "data: [DONE]\n\n") {
			t.Fatal(out.Body.String())
		}
	}
}

func TestFlattenSinkRejectsGeneratedSecretAndOversizedFrame(t *testing.T) {
	for _, limit := range []int{1 << 20, 100} {
		out := httptest.NewRecorder()
		guard := newResponseGuard([]byte(`<mx_tool name="lookup"`))
		defer guard.Clear()
		reject := guard.ContainsJSON
		if limit == 100 {
			reject = nil
		}
		sink := NewFlattenSink(out, 1<<20, limit, reject)
		defer sink.Clear()
		for _, data := range []string{
			`{"id":"c","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call","type":"function","function":{"name":"lookup","arguments":"{}"}}]},"finish_reason":null}]}`,
			`{"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
			`[DONE]`,
		} {
			if _, err := sink.Write([]byte("data: " + data + "\n\n")); err != nil {
				t.Fatal(err)
			}
		}
		sink.Flush()
		if out.Flushed {
			t.Fatal("buffered tools flushed response headers")
		}
		if err := sink.Complete(time.Second); err == nil || sink.ProjectionError() == nil || out.Body.Len() != 0 {
			t.Fatalf("generated output crossed guard: err=%v body=%s", err, out.Body)
		}
	}
}
