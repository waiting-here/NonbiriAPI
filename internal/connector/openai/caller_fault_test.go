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
	"github.com/waiting-here/NonbiriAPI/internal/smartbackoff"
	"github.com/waiting-here/NonbiriAPI/internal/upstreamerror"
)

func TestCallerParameterRequiresExactWireValue(t *testing.T) {
	ctx := smartbackoff.WithCharityCall(t.Context(), true)
	request := decodeAdapterRequest(t, `{"model":"m","messages":[],"temperature":1.0,"response_format":{"type": "json_object"}}`)
	for _, tc := range []struct {
		name, body string
		policy     contract.AttemptPolicy
		want       bool
	}{
		{"unchanged", `{"temperature":1.0}`, contract.AttemptPolicy{}, true},
		{"normalized", `{"temperature":1}`, contract.AttemptPolicy{}, false},
		{"overwritten", `{"temperature":0.5}`, contract.AttemptPolicy{}, false},
		{"removed", `{}`, contract.AttemptPolicy{}, false},
		{"adapted", `{"temperature":1.0}`, contract.AttemptPolicy{HasAdaptation: true}, false},
		{"headers", `{"temperature":1.0}`, contract.AttemptPolicy{AdditionalHeaders: http.Header{"X-Mode": {"value"}}}, false},
		{"managed", `{"temperature":1.0}`, contract.AttemptPolicy{ManagedHeaders: http.Header{"X-Session": {"value"}}}, false},
		{"extensions", `{"temperature":1.0}`, contract.AttemptPolicy{NativeExtensions: map[string]json.RawMessage{"/temperature": json.RawMessage(`1.0`)}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			match := unchangedCallerParameters(ctx, &http.Response{StatusCode: 400}, request.fields, []byte(tc.body), tc.policy)
			if got := match != nil && match("temperature"); got != tc.want {
				t.Fatalf("matched=%t want=%t", got, tc.want)
			}
		})
	}
	match := unchangedCallerParameters(ctx, &http.Response{StatusCode: 400}, request.fields, []byte(`{"response_format":{"type":"json_object"}}`), contract.AttemptPolicy{})
	if match != nil && match("response_format") {
		t.Fatal("codec whitespace normalization was treated as original bytes")
	}
}

func TestCallerParameterHTTPFailurePreservesDiagnostics(t *testing.T) {
	const payload = `{"error":{"type":"invalid_request_error","code":"unsupported_parameter","param":"temperature","message":"Unsupported generation parameter."}}`
	for _, charity := range []bool{false, true} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("charity=%t/stream=%t", charity, stream), func(t *testing.T) {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					var body map[string]json.RawMessage
					if json.NewDecoder(r.Body).Decode(&body) != nil || string(body["temperature"]) != "0.25" {
						t.Error("unexpected outbound parameter")
					}
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(400)
					_, _ = io.WriteString(w, payload)
				}))
				defer server.Close()
				ctx := smartbackoff.WithCharityCall(context.Background(), charity)
				captured := ""
				ctx = upstreamerror.WithCapture(ctx, func(_ context.Context, e upstreamerror.Event) { captured = string(e.Bytes()) })
				request := decodeAdapterRequest(t, fmt.Sprintf(`{"model":"public/model","messages":[],"temperature":0.25,"stream":%t}`, stream))
				writer := httptest.NewRecorder()
				result := adapterForServer(t, server.URL, nil, nil).Attempt(ctx, writer, testTarget(server.URL, []byte("secret-material"), nil), request, "caller")
				want := contract.StreakUpstreamFailure
				if charity {
					want = contract.StreakNeutral
				}
				if result.StreakDisposition != want || result.Failure != FailureUpstream || result.Success || result.Committed || result.FailureOrigin != contract.OriginUpstreamResponse || result.UpstreamStatus != 400 || writer.Body.Len() != 0 {
					t.Fatalf("result=%+v", result)
				}
				if captured != payload || result.ErrorDetail.Code() != "unsupported_parameter" || result.ErrorDetail.Message() != "Unsupported generation parameter." {
					t.Fatal("classification changed diagnostics")
				}
			})
		}
	}
}

func TestCallerParameterNativeResponsesAndConversion(t *testing.T) {
	for _, kind := range []contract.Type{contract.TypeOpenAICompatible, contract.TypeOpenAIResponses, contract.TypeOpenAIChatCompletions} {
		t.Run(string(kind), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(422)
				_, _ = io.WriteString(w, `{"error":{"type":"invalid_request_error","code":"invalid_value","param":"temperature","message":"Unsupported value."}}`)
			}))
			defer server.Close()
			request := inferenceRequestForTest(t, contract.OperationResponses, `{"model":"m","input":"hi","temperature":0.25}`)
			result := adapterForServer(t, server.URL, nil, nil).AttemptInference(smartbackoff.WithCharityCall(t.Context(), true), httptest.NewRecorder(), testTarget(server.URL, []byte("secret-material"), nil), request, kind, contract.AttemptPolicy{})
			want := contract.StreakNeutral
			if kind == contract.TypeOpenAIChatCompletions {
				want = contract.StreakUpstreamFailure
			}
			if result.StreakDisposition != want || result.Success || result.UpstreamStatus != 422 {
				t.Fatalf("result=%+v", result)
			}
		})
	}
}

func TestCallerParameterArkErrorKeepsFailureAndRawDiagnostics(t *testing.T) {
	const payload = "{\"error\":{\"code\":\"InvalidParameter\",\"message\":\"The parameter `reasoning_effort` specified in the request are not valid: value `min` is invalid.\",\"param\":\"reasoning_effort\",\"type\":\"BadRequest\"}}"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]json.RawMessage
		if json.NewDecoder(r.Body).Decode(&body) != nil || string(body["reasoning_effort"]) != `"min"` {
			t.Error("unexpected outbound parameter")
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, payload)
	}))
	defer server.Close()
	for _, charity := range []bool{false, true} {
		t.Run(fmt.Sprintf("charity=%t", charity), func(t *testing.T) {
			ctx := smartbackoff.WithCharityCall(t.Context(), charity)
			captured := ""
			ctx = upstreamerror.WithCapture(ctx, func(_ context.Context, e upstreamerror.Event) { captured = string(e.Bytes()) })
			request := decodeAdapterRequest(t, `{"model":"public/model","messages":[],"reasoning_effort":"min","stream":true}`)
			writer := httptest.NewRecorder()
			result := adapterForServer(t, server.URL, nil, nil).Attempt(ctx, writer, testTarget(server.URL, []byte("secret-material"), nil), request, "caller")
			want := contract.StreakUpstreamFailure
			if charity {
				want = contract.StreakNeutral
			}
			if result.StreakDisposition != want || result.Failure != FailureUpstream || result.Success || result.Committed || result.FailureOrigin != contract.OriginUpstreamResponse || result.UpstreamStatus != 400 || writer.Body.Len() != 0 {
				t.Fatalf("result=%+v", result)
			}
			if captured != payload || result.ErrorDetail.Code() != "InvalidParameter" {
				t.Fatal("classification changed the original error")
			}
		})
	}
}

func TestCallerParameterNativeCodecNormalization(t *testing.T) {
	for _, tc := range []struct {
		name, raw string
		want      contract.StreakDisposition
	}{
		{"unchanged", `{"effort":"low"}`, contract.StreakNeutral},
		{"normalized", `{"effort": "low"}`, contract.StreakUpstreamFailure},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]json.RawMessage
				if json.NewDecoder(r.Body).Decode(&body) != nil || string(body["reasoning"]) != `{"effort":"low"}` {
					t.Error("native codec did not produce the expected compact value")
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(400)
				_, _ = io.WriteString(w, `{"error":{"type":"invalid_request_error","code":"unsupported_value","param":"reasoning","message":"Unsupported value."}}`)
			}))
			defer server.Close()
			request := inferenceRequestForTest(t, contract.OperationResponses, `{"model":"m","input":"hi","reasoning":`+tc.raw+`}`)
			result := adapterForServer(t, server.URL, nil, nil).AttemptInference(smartbackoff.WithCharityCall(t.Context(), true), httptest.NewRecorder(), testTarget(server.URL, []byte("secret-material"), nil), request, contract.TypeOpenAIResponses, contract.AttemptPolicy{})
			if result.StreakDisposition != tc.want || result.UpstreamStatus != 400 {
				t.Fatalf("native codec result=%+v", result)
			}
		})
	}
}

func TestCallerParameterNativeEmbeddingAndImage(t *testing.T) {
	for _, param := range []string{"dimensions", "quality"} {
		t.Run(param, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(400)
				_, _ = fmt.Fprintf(w, `{"error":{"type":"invalid_request_error","code":"unsupported_value","param":%q,"message":"Unsupported value."}}`, param)
			}))
			defer server.Close()
			adapter := adapterForServer(t, server.URL, nil, nil)
			ctx := smartbackoff.WithCharityCall(t.Context(), true)
			target := testTarget(server.URL, []byte("secret-material"), nil)
			var result AttemptResult
			if param == "dimensions" {
				r, err := DecodeEmbeddingRequest(strings.NewReader(`{"model":"m","input":"hi","dimensions":512}`), 0)
				if err != nil {
					t.Fatal(err)
				}
				defer r.Clear()
				result = adapter.AttemptEmbedding(ctx, httptest.NewRecorder(), target, r, contract.AttemptPolicy{SafetyIdentifier: "caller"})
			} else {
				r, err := DecodeImageRequest(strings.NewReader(`{"model":"m","prompt":"hi","quality":"hd"}`), 0)
				if err != nil {
					t.Fatal(err)
				}
				defer r.Clear()
				result = adapter.AttemptImage(ctx, httptest.NewRecorder(), target, r, contract.AttemptPolicy{SafetyIdentifier: "caller"})
			}
			if result.StreakDisposition != contract.StreakNeutral || result.Success || result.Failure != FailureUpstream || !result.ErrorDetail.CallerFault().Matches(400, "unsupported_value") {
				t.Fatalf("result=%+v", result)
			}
		})
	}
}

func TestCallerParameterAfterAcceptedHTTPRemainsUpstreamFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"error":{"type":"invalid_request_error","code":"invalid_value","param":"temperature","message":"Invalid value."}}`)
	}))
	defer server.Close()
	request := decodeAdapterRequest(t, `{"model":"m","messages":[],"temperature":0.25}`)
	result := adapterForServer(t, server.URL, nil, nil).Attempt(smartbackoff.WithCharityCall(t.Context(), true), httptest.NewRecorder(), testTarget(server.URL, []byte("secret-material"), nil), request, "caller")
	if result.StreakDisposition != contract.StreakUpstreamFailure || result.UpstreamStatus != 200 || result.ErrorDetail.CallerFault().Matches(400, "invalid_value") || !strings.Contains(result.ErrorDetail.Message(), "Invalid value") {
		t.Fatalf("result=%+v", result)
	}
}
