package anthropic

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/backend"
	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
)

func TestMarkedAdaptationSuccessReflection(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, flatten := range []bool{false, true} {
			for _, fromBody := range []bool{false, true} {
				for _, marked := range []bool{false, true} {
					t.Run(fmt.Sprintf("stream%v/flatten%v/body%v/marked%v", stream, flatten, fromBody, marked), func(t *testing.T) {
						const value = "configured-private-token"
						body := `{"id":"msg_1","type":"message","role":"assistant","model":"claude","content":[{"type":"text","text":"configured-private-token"}],"stop_reason":"end_turn","stop_sequence":null,"usage":{}}`
						kind := "application/json"
						if stream {
							kind = "text/event-stream"
							body = messageStart(`{"input_tokens":1,"output_tokens":0}`) + namedEvent("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`) + namedEvent("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"configured-private-token"}}`) + namedEvent("content_block_stop", `{"type":"content_block_stop","index":0}`) + messageDelta("end_turn", `{"output_tokens":2}`) + messageStop()
						}
						client := &fakeEndpointClient{baseURL: "https://api.example/v1", do: func(request *http.Request) (*http.Response, error) {
							if fromBody {
								var sent map[string]json.RawMessage
								data, err := io.ReadAll(request.Body)
								if err != nil || json.Unmarshal(data, &sent) != nil || string(sent["vendor_token"]) != `"configured-private-token"` {
									t.Fatalf("native secret was not sent: %s, %v", data, err)
								}
							} else if request.Header.Get("X-Token") != value {
								t.Fatal("marked header was not sent")
							}
							return fakeResponse(200, kind, body), nil
						}}
						adapter, err := NewAdapter(AdapterConfig{Backend: fakeBackend{open: func(string) (backend.EndpointClient, error) { return client, nil }}})
						if err != nil {
							t.Fatal(err)
						}
						policy := contract.AttemptPolicy{AdditionalHeaders: http.Header{"X-Token": {value}}, HasAdaptation: true, FlattenToolCalls: flatten, SafetyIdentifier: "nbu_safe"}
						if fromBody {
							policy.AdditionalHeaders = nil
							policy.NativeExtensions = map[string]json.RawMessage{"/vendor_token": json.RawMessage(`"configured-private-token"`)}
						}
						if marked {
							policy.ResponseSecrets = [][]byte{[]byte(value)}
						}
						request := mustChatRequest(t, fmt.Sprintf(`{"model":"p/m","messages":[{"role":"user","content":"hi"}],"max_tokens":128,"stream":%v}`, stream))
						defer request.Clear()
						out := httptest.NewRecorder()
						result := adapter.AttemptWithPolicy(context.Background(), out, NewTarget(client.baseURL, "claude", NewCredential([]byte("upstream-key-long"), nil)), request, policy)
						if result.Success == marked || result.UpstreamStatus != http.StatusOK || marked && strings.Contains(out.Body.String(), value) {
							t.Fatalf("wrong response boundary: %+v %s", result, out.Body)
						}
					})
				}
			}
		}
	}
}
