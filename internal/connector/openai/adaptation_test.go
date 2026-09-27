package openai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/requestadaptation"
)

func TestAdaptedOpenAIChatOutboundExcludesUnsupportedThinking(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(map[bool]string{false: "ordinary", true: "stream"}[stream], func(t *testing.T) {
			var sent map[string]json.RawMessage
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				if request.Header.Get("X-Research") != "configured" || request.Header.Get("Authorization") != "Bearer mock-upstream-key" || request.Header.Get("Cookie") != "" {
					t.Errorf("outbound headers: %v", request.Header)
				}
				body, err := io.ReadAll(request.Body)
				if err != nil || json.Unmarshal(body, &sent) != nil {
					t.Errorf("outbound JSON: %v, %s", err, body)
				}
				if stream {
					writeSSE(writer, "data: "+validChunk+"\n\n", `data: {"id":"finish","object":"chat.completion.chunk","created":1,"model":"upstream","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`+"\n\n", "data: [DONE]\n\n")
				} else {
					writer.Header().Set("Content-Type", "application/json")
					_, _ = io.WriteString(writer, validCompletion)
				}
			}))
			defer server.Close()
			doc := requestadaptation.Empty(requestadaptation.ScopeEndpoint)
			doc.BodyForced.Values["/reasoning_effort"] = json.RawMessage(`"low"`)
			logical := `{"model":"public/model","messages":[{"role":"user","content":"hello"}],"thinking":{"type":"enabled","budget_tokens":4096},"stream":` + map[bool]string{false: "false", true: "true"}[stream] + `}`
			request := decodeAdapterRequest(t, logical)
			if err := request.ExcludeFields([]string{"thinking"}); err != nil {
				t.Fatal(err)
			}
			filtered, err := request.LogicalBody()
			if err != nil {
				t.Fatal(err)
			}
			adapted, err := requestadaptation.ApplyBody(filtered, doc, MaxRequestBodyBytes)
			if err != nil {
				t.Fatal(err)
			}
			request = decodeAdapterRequest(t, string(adapted))
			adapter := adapterForServer(t, server.URL, nil, nil)
			result := adapter.AttemptWithPolicy(context.Background(), httptest.NewRecorder(), testTarget(server.URL+"/v1", []byte("mock-upstream-key"), nil), request,
				connectorcontract.AttemptPolicy{SafetyIdentifier: "nbu_authoritative", AdditionalHeaders: http.Header{"X-Research": {"configured"}}, HasAdaptation: true})
			if !result.Success || string(sent["reasoning_effort"]) != `"low"` || sent["thinking"] != nil || string(sent["model"]) != `"upstream/model"` {
				t.Fatalf("result=%+v, outbound=%v", result, sent)
			}
		})
	}
}

func TestAdaptedOpenAIErrorNeverReflectsConfiguredValue(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(writer, `{"error":{"message":"configured-private-value refused"}}`)
	}))
	defer server.Close()
	adapter := adapterForServer(t, server.URL, nil, nil)
	result := adapter.AttemptWithPolicy(context.Background(), httptest.NewRecorder(), testTarget(server.URL+"/v1", []byte("mock-upstream-key"), nil),
		decodeAdapterRequest(t, `{"model":"p/m","messages":[{"role":"user","content":"hi"}]}`),
		connectorcontract.AttemptPolicy{AdditionalHeaders: http.Header{"X-Private": {"configured-private-value"}}, HasAdaptation: true})
	if result.Success || strings.Contains(result.Diagnostic, "configured-private-value") || strings.Contains(result.ErrorDetail.Message(), "configured-private-value") {
		t.Fatalf("upstream error reflected configuration: %+v", result)
	}
}

func TestAdaptedOpenAIEmbeddingOutboundPreservesBoundedExtension(t *testing.T) {
	var sent map[string]json.RawMessage
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("X-Research") != "embedding-test" || request.Header.Get("Authorization") != "Bearer mock-upstream-key" {
			t.Errorf("outbound headers: %v", request.Header)
		}
		body, err := io.ReadAll(request.Body)
		if err != nil || json.Unmarshal(body, &sent) != nil {
			t.Errorf("outbound JSON: %v, %s", err, body)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, embeddingResponse("float", `{"prompt_tokens":2,"total_tokens":2}`))
	}))
	defer server.Close()
	doc := requestadaptation.Empty(requestadaptation.ScopeEndpoint)
	doc.BodyForced.Values["/vendor_option"] = json.RawMessage(`{"enabled":true}`)
	adapted, err := requestadaptation.ApplyBody([]byte(`{"model":"public/model","input":["one","two"]}`), doc, MaxRequestBodyBytes)
	if err != nil {
		t.Fatal(err)
	}
	request, err := DecodeEmbeddingRequest(strings.NewReader(string(adapted)), MaxRequestBodyBytes)
	if err != nil {
		t.Fatal(err)
	}
	adapter := adapterForServer(t, server.URL, nil, nil)
	result := adapter.AttemptEmbedding(context.Background(), httptest.NewRecorder(), testTarget(server.URL+"/v1", []byte("mock-upstream-key"), nil), request,
		connectorcontract.AttemptPolicy{SafetyIdentifier: "nbu_authoritative", AdditionalHeaders: http.Header{"X-Research": {"embedding-test"}}, HasAdaptation: true})
	if !result.Success || string(sent["model"]) != `"upstream/model"` || string(sent["vendor_option"]) != `{"enabled":true}` || string(sent["user"]) != `"nbu_authoritative"` {
		t.Fatalf("result=%+v, outbound=%v", result, sent)
	}
}
