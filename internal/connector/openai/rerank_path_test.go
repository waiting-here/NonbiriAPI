package openai

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
)

func TestInferenceCustomRerankPathKeepsOtherOperations(t *testing.T) {
	for _, test := range []struct {
		op                contract.Operation
		body, reply, want string
	}{
		{contract.OperationRerank, `{"model":"public/model","query":"q","documents":["first","second"]}`, `{"results":[{"index":1,"relevance_score":0.9}],"usage":{"total_tokens":7}}`, "/compatible-api/v1/reranks"},
		{contract.OperationResponses, `{"model":"public/model","input":"hi"}`, `{"id":"r1","object":"response","status":"completed","model":"private-model","output":[],"usage":{"input_tokens":2,"output_tokens":3,"total_tokens":5}}`, "/v1/responses"},
	} {
		t.Run(string(test.op), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != test.want || r.Header.Get("Authorization") != "Bearer fixture-secret" {
					t.Errorf("unexpected path or credential")
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, test.reply)
			}))
			defer server.Close()
			request := inferenceRequestForTest(t, test.op, test.body)
			target := NewTarget(server.URL+"/v1", "private-model", NewCredential([]byte("fixture-secret"), nil)).WithRerankPath("/compatible-api/v1/reranks")
			result := adapterForServer(t, server.URL, nil, nil).AttemptInference(context.Background(), httptest.NewRecorder(), target, request, contract.TypeOpenAICompatible, contract.AttemptPolicy{})
			if !result.Success {
				t.Fatalf("failed: %+v", result)
			}
		})
	}
}
