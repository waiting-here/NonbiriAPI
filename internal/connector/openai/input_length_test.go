package openai

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/smartbackoff"
)

func TestInputLengthFaultIgnoresConfiguredPolicy(t *testing.T) {
	const payload = `{"error":{"type":"invalid_request_error","code":"invalid_value","param":"messages","message":"system/developer 消息字符数超限：6001，最多 5000"}}`
	for _, tc := range []struct {
		name   string
		policy contract.AttemptPolicy
	}{
		{"native", contract.AttemptPolicy{}},
		{"adapted", contract.AttemptPolicy{HasAdaptation: true}},
		{"added headers", contract.AttemptPolicy{AdditionalHeaders: http.Header{"X-Mode": {"test"}}}},
		{"managed headers", contract.AttemptPolicy{ManagedHeaders: http.Header{"X-Session": {"0123456789abcdef0123456789abcdef"}}}},
		{"native extensions", contract.AttemptPolicy{NativeExtensions: map[string]json.RawMessage{"/vendor_option": json.RawMessage(`true`)}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(400)
				_, _ = io.WriteString(w, payload)
			}))
			defer server.Close()
			request := decodeAdapterRequest(t, `{"model":"m","messages":[{"role":"system","content":"hello"}]}`)
			ctx := smartbackoff.WithCharityCall(t.Context(), true)
			tc.policy.SafetyIdentifier = "caller"
			result := adapterForServer(t, server.URL, nil, nil).AttemptWithPolicy(ctx, httptest.NewRecorder(), testTarget(server.URL, []byte("secret-material"), nil), request, tc.policy)
			if result.StreakDisposition != contract.StreakNeutral || result.Success || result.Committed || result.Failure != FailureUpstream || result.UpstreamStatus != 400 || !result.ErrorDetail.CallerFault().Matches(400, "invalid_value") {
				t.Fatalf("result=%+v", result)
			}
		})
	}
}

func TestInputLengthFaultNativeJSONAndConversion(t *testing.T) {
	for _, route := range []string{"responses", "responses conversion", "embedding", "image", "decision", "multipart", "cohere"} {
		t.Run(route, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(422)
				_, _ = io.WriteString(w, `{"error":{"type":"invalid_request_error","code":"context_length_exceeded","param":null,"message":"Input exceeds context length."}}`)
			}))
			defer server.Close()
			adapter := adapterForServer(t, server.URL, nil, nil)
			ctx := smartbackoff.WithCharityCall(t.Context(), true)
			target := testTarget(server.URL, []byte("secret-material"), nil)
			writer := httptest.NewRecorder()
			policy := contract.AttemptPolicy{HasAdaptation: true, SafetyIdentifier: "caller"}
			var result AttemptResult
			switch route {
			case "responses", "responses conversion":
				kind := contract.TypeOpenAIResponses
				if route == "responses conversion" {
					kind = contract.TypeOpenAIChatCompletions
				}
				r := inferenceRequestForTest(t, contract.OperationResponses, `{"model":"m","input":"hi"}`)
				result = adapter.AttemptInference(ctx, writer, target, r, kind, policy)
			case "embedding":
				r, err := DecodeEmbeddingRequest(strings.NewReader(`{"model":"m","input":"hi"}`), 0)
				if err != nil {
					t.Fatal(err)
				}
				defer r.Clear()
				result = adapter.AttemptEmbedding(ctx, writer, target, r, policy)
			case "image":
				r, err := DecodeImageRequest(strings.NewReader(`{"model":"m","prompt":"hi"}`), 0)
				if err != nil {
					t.Fatal(err)
				}
				defer r.Clear()
				result = adapter.AttemptImage(ctx, writer, target, r, policy)
			case "decision":
				r := decodeDecision(t, decisionBody)
				result = adapter.AttemptDecision(ctx, writer, target, r, policy)
			case "multipart":
				r := uploadForTest(t, contract.OperationAudioTranscriptions, map[string]string{"model": "m", "prompt": "hi"}, map[string][]byte{"file": []byte("audio")})
				result = adapter.AttemptInference(ctx, writer, target, r, contract.TypeOpenAICompatible, policy)
			case "cohere":
				r := inferenceRequestForTest(t, contract.OperationRerank, `{"model":"m","query":"hi","documents":["text"]}`)
				result = adapter.AttemptInference(ctx, writer, target, r, contract.TypeCohereRerank, policy)
			}
			want := contract.StreakNeutral
			if route == "multipart" || route == "cohere" {
				want = contract.StreakUpstreamFailure
			}
			if result.StreakDisposition != want || result.Success || result.Committed || result.Failure != FailureUpstream || result.UpstreamStatus != 422 || result.ErrorDetail.CallerFault().Matches(422, "context_length_exceeded") != (want == contract.StreakNeutral) {
				t.Fatalf("result=%+v", result)
			}
		})
	}
}

func TestInputLengthFaultAdaptationKeepsSecretsHidden(t *testing.T) {
	const secret = "configured-private-value"
	for _, tc := range []struct {
		name, code, param string
		neutral           bool
	}{
		{"input value", "invalid_value", "messages", true},
		{"context limit", "context_length_exceeded", "input", true},
		{"arbitrary code", secret, "messages", false},
		{"ordinary parameter", "invalid_value", "temperature", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("X-Private") != secret || r.Header.Get("X-Session") == "" {
					t.Error("configured headers were not sent")
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(400)
				_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"type": "invalid_request_error", "code": tc.code, "param": tc.param, "message": "消息字符数超限：6001，最多 5000；" + secret}})
			}))
			defer server.Close()
			policy := contract.AttemptPolicy{SafetyIdentifier: "caller", HasAdaptation: true, AdditionalHeaders: http.Header{"X-Private": {secret}}, ManagedHeaders: http.Header{"X-Session": {"0123456789abcdef0123456789abcdef"}}, ResponseSecrets: [][]byte{[]byte(secret)}}
			ctx := smartbackoff.WithCharityCall(t.Context(), true)
			writer := httptest.NewRecorder()
			result := adapterForServer(t, server.URL, nil, nil).AttemptWithPolicy(ctx, writer, testTarget(server.URL, []byte("secret-material"), nil), decodeAdapterRequest(t, `{"model":"m","messages":[],"temperature":0.25}`), policy)
			want, wantCode := contract.StreakUpstreamFailure, ""
			if tc.neutral {
				want, wantCode = contract.StreakNeutral, tc.code
			}
			if result.StreakDisposition != want || result.Failure != FailureUpstream || result.UpstreamStatus != 400 || result.ErrorDetail.Code() != wantCode || strings.Contains(result.ErrorDetail.Message(), secret) || strings.Contains(result.Diagnostic, secret) || strings.Contains(writer.Body.String(), secret) {
				t.Fatalf("confidential failure boundary changed: %+v", result)
			}
		})
	}
}
