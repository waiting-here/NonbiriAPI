package gateway

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
)

func TestDeclaredGatewayExtensionAndHeaderReachStreamMock(t *testing.T) {
	var sent map[string]json.RawMessage
	b := &fakeBackend{do: func(request *http.Request) (*http.Response, error) {
		if request.Header.Get("X-Research") != "gateway-test" || request.Header.Get("Authorization") != "Bearer sk-test-credential-long" || request.Header.Get("Ai-Language-Model-Id") != "provider/private-model" {
			t.Errorf("outbound headers: %v", request.Header)
		}
		body, err := io.ReadAll(request.Body)
		if err != nil || json.Unmarshal(body, &sent) != nil {
			t.Errorf("outbound JSON: %v, %s", err, body)
		}
		return response(sse(textParts...), "text/event-stream"), nil
	}}
	adapter, err := NewAdapter(b)
	if err != nil {
		t.Fatal(err)
	}
	request := chat(t, `{"model":"public-model","messages":[{"role":"user","content":"hi"}],"max_tokens":128,"stream":true}`)
	result := adapter.AttemptWithPolicy(context.Background(), httptest.NewRecorder(), target(), secret(), request, nil, "server-label",
		contract.AttemptPolicy{
			AdditionalHeaders: http.Header{"X-Research": {"gateway-test"}},
			NativeExtensions:  map[string]json.RawMessage{"/providerOptions/gateway/custom": json.RawMessage(`{"nested":true}`)},
			HasAdaptation:     true,
		})
	var providerOptions map[string]map[string]json.RawMessage
	if err := json.Unmarshal(sent["providerOptions"], &providerOptions); err != nil {
		t.Fatal(err)
	}
	if !result.Success || b.calls != 1 || string(providerOptions["gateway"]["user"]) != `"server-label"` || string(providerOptions["gateway"]["custom"]) != `{"nested":true}` {
		t.Fatalf("result=%+v, outbound=%v", result, sent)
	}
}
