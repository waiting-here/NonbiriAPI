package openai

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
)

func TestInferenceResponseFailurePreservesValidatedUsage(t *testing.T) {
	for _, test := range []struct {
		name, status, output string
		stream               bool
	}{
		{"failed JSON", "failed", `[]`, false},
		{"invalid output JSON", "completed", `{}`, false},
		{"failed SSE", "failed", `[]`, true},
		{"invalid output SSE", "completed", `{}`, true},
	} {
		for _, total := range []string{"5", "null", "-1"} {
			t.Run(fmt.Sprintf("%s/total=%s", test.name, total), func(t *testing.T) {
				usage := `{"input_tokens":2,"output_tokens":3,"total_tokens":` + total + `}`
				response := `{"id":"r1","object":"response","status":"` + test.status + `","output":` + test.output + `,"usage":` + usage + `}`
				if test.status == "failed" {
					response = strings.TrimSuffix(response, "}") + `,"error":{"code":"server_error","message":"failed"}}`
				}
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					if test.stream {
						w.Header().Set("Content-Type", "text/event-stream")
						_, _ = io.WriteString(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"partial\"}\n\n")
						_, _ = io.WriteString(w, `data: {"type":"response.`+test.status+`","response":`+response+"}\n\n")
					} else {
						_, _ = io.WriteString(w, response)
					}
				}))
				defer server.Close()
				request := inferenceRequestForTest(t, contract.OperationResponses, fmt.Sprintf(`{"model":"m","input":"hi","stream":%t}`, test.stream))
				writer := httptest.NewRecorder()
				result := adapterForServer(t, server.URL, nil, nil).AttemptInference(context.Background(), writer, NewTarget(server.URL, "private", NewCredential([]byte("secret-material"), nil)), request, contract.TypeOpenAICompatible, contract.AttemptPolicy{})
				if result.Success || result.Committed != test.stream || result.Usage.Present != (total == "5") || strings.Contains(writer.Body.String(), "response.completed") {
					t.Fatalf("failed response facts changed: %+v %s", result, writer.Body.String())
				}
				if total == "5" && (result.Usage.UncachedInputTokens != 2 || result.Usage.OutputTokens != 3) {
					t.Fatalf("valid usage lost: %+v", result.Usage)
				}
			})
		}
	}
}

func TestInferenceImageEditPartialUsageRemainsConservative(t *testing.T) {
	for _, complete := range []bool{false, true} {
		request := inferenceRequestForTest(t, contract.OperationImagesEdits, `{"model":"m","prompt":"edit","images":[{"image_url":"data:image/png;base64,aW1hZ2U="}],"n":2,"stream":true}`)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			frame := "data: " + `{"type":"image_edit.completed","b64_json":"aW1hZ2U=","usage":{"input_tokens":2,"output_tokens":3,"total_tokens":5,"input_tokens_details":{"text_tokens":1,"image_tokens":1},"output_tokens_details":{"text_tokens":0,"image_tokens":3}}}` + "\n\n"
			_, _ = io.WriteString(w, frame)
			if complete {
				_, _ = io.WriteString(w, frame)
			}
		}))
		writer := httptest.NewRecorder()
		result := adapterForServer(t, server.URL, nil, nil).AttemptInference(context.Background(), writer, NewTarget(server.URL, "private", NewCredential([]byte("secret-material"), nil)), request, contract.TypeOpenAICompatible, contract.AttemptPolicy{})
		server.Close()
		count := int64(0)
		if complete {
			count = 2
		}
		if result.Success != complete || !result.Committed || result.Usage.Present != complete || result.Usage.UncachedInputTokens != 2*count || result.Usage.OutputTokens != 3*count || result.Usage.Modalities.InputPresent != complete || result.Usage.Modalities.OutputPresent != complete {
			t.Fatalf("complete=%t usage=%+v result=%+v", complete, result.Usage, result)
		}
	}
}
