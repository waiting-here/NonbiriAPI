package anthropic

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/backend"
	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
)

func TestDeclaredNativeThinkingAndHeaderReachAnthropicMock(t *testing.T) {
	var sent map[string]json.RawMessage
	client := &fakeEndpointClient{baseURL: "https://api.example/v1", do: func(request *http.Request) (*http.Response, error) {
		if request.Header.Get("X-Research") != "test-value" || request.Header.Get("X-Api-Key") != "sk-test" || request.Header.Get("Anthropic-Version") != AnthropicVersion {
			t.Errorf("outbound headers: %v", request.Header)
		}
		body, err := io.ReadAll(request.Body)
		if err != nil || json.Unmarshal(body, &sent) != nil {
			t.Errorf("outbound JSON: %v, %s", err, body)
		}
		return fakeResponse(http.StatusOK, "application/json", `{"id":"msg_1","type":"message","role":"assistant","model":"claude","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","stop_sequence":null,"usage":{}}`), nil
	}}
	adapter, err := NewAdapter(AdapterConfig{Backend: fakeBackend{open: func(string) (backend.EndpointClient, error) { return client, nil }}})
	if err != nil {
		t.Fatal(err)
	}
	request := mustChatRequest(t, `{"model":"p/m","messages":[{"role":"user","content":"hi"}],"max_tokens":2048}`)
	result := adapter.AttemptWithPolicy(context.Background(), httptest.NewRecorder(),
		NewTarget(client.baseURL, "claude", NewCredential([]byte("sk-test"), []byte("cipher"))), request,
		connectorcontract.AttemptPolicy{
			SafetyIdentifier:  "nbu_safe",
			AdditionalHeaders: http.Header{"X-Research": {"test-value"}},
			NativeExtensions:  map[string]json.RawMessage{"/thinking": json.RawMessage(`{"type":"enabled","budget_tokens":1024}`)},
			HasAdaptation:     true,
		})
	if !result.Success || string(sent["model"]) != `"claude"` || string(sent["max_tokens"]) != `2048` || string(sent["thinking"]) != `{"budget_tokens":1024,"type":"enabled"}` {
		t.Fatalf("result=%+v, outbound=%v", result, sent)
	}
}
