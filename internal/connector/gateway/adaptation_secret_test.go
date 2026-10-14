package gateway

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
	"github.com/waiting-here/NonbiriAPI/internal/connector/openai"
)

func TestMarkedAdaptationSuccessReflection(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, flatten := range []bool{false, true} {
			for _, fromBody := range []bool{false, true} {
				for _, marked := range []bool{false, true} {
					t.Run(fmt.Sprintf("stream%v/flatten%v/body%v/marked%v", stream, flatten, fromBody, marked), func(t *testing.T) {
						const value = "configured-private-token"
						body := strings.Replace(chatOK, "Hello", value, 1)
						kind := "application/json"
						if stream {
							body = strings.Replace(sse(textParts...), "Hello", value, 1)
							kind = "text/event-stream"
						}
						backend := &fakeBackend{do: func(request *http.Request) (*http.Response, error) {
							if fromBody {
								var sent map[string]json.RawMessage
								data, err := io.ReadAll(request.Body)
								if err != nil || json.Unmarshal(data, &sent) != nil || string(sent["vendor_token"]) != `"configured-private-token"` {
									t.Fatalf("native secret was not sent: %s, %v", data, err)
								}
							} else if request.Header.Get("X-Token") != value {
								t.Fatal("marked header was not sent")
							}
							return response(body, kind), nil
						}}
						adapter, err := NewAdapter(backend)
						if err != nil {
							t.Fatal(err)
						}
						policy := contract.AttemptPolicy{AdditionalHeaders: http.Header{"X-Token": {value}}, HasAdaptation: true, FlattenToolCalls: flatten}
						if fromBody {
							policy.AdditionalHeaders = nil
							policy.NativeExtensions = map[string]json.RawMessage{"/vendor_token": json.RawMessage(`"configured-private-token"`)}
						}
						if marked {
							policy.ResponseSecrets = [][]byte{[]byte(value)}
						}
						request := chat(t, fmt.Sprintf(`{"model":"public-model","messages":[{"role":"user","content":"hi"}],"max_tokens":128,"stream":%v}`, stream))
						defer request.Clear()
						out := httptest.NewRecorder()
						result := adapter.AttemptWithPolicy(context.Background(), out, target(), secret(), request, nil, "", policy)
						if result.Success == marked || result.UpstreamStatus != http.StatusOK || marked && strings.Contains(out.Body.String(), value) {
							t.Fatalf("wrong response boundary: %+v %s", result, out.Body)
						}
					})
				}
			}
		}
	}
}

func TestMarkedAdaptationEmbeddingReflection(t *testing.T) {
	for _, marked := range []bool{false, true} {
		t.Run(fmt.Sprint(marked), func(t *testing.T) {
			const value = "0.12345678"
			backend := &fakeBackend{do: func(request *http.Request) (*http.Response, error) {
				data, err := io.ReadAll(request.Body)
				var sent map[string]json.RawMessage
				if err != nil || json.Unmarshal(data, &sent) != nil || string(sent["vendor_token"]) != `"0.12345678"` || !strings.HasSuffix(request.URL.Path, "/embedding-model") {
					t.Fatalf("embedding extension was not sent: %s, %v", data, err)
				}
				return response(`{"embeddings":[[0.12345678,1]],"usage":{"tokens":4}}`, "application/json"), nil
			}}
			adapter, err := NewAdapter(backend)
			if err != nil {
				t.Fatal(err)
			}
			request, err := openai.DecodeEmbeddingRequest(strings.NewReader(`{"model":"public-model","input":"hello"}`), 1<<20)
			if err != nil {
				t.Fatal(err)
			}
			defer request.Clear()
			policy := contract.AttemptPolicy{NativeExtensions: map[string]json.RawMessage{"/vendor_token": json.RawMessage(`"0.12345678"`)}, HasAdaptation: true}
			if marked {
				policy.ResponseSecrets = [][]byte{[]byte(value)}
			}
			out := httptest.NewRecorder()
			result := adapter.AttemptWithPolicy(context.Background(), out, target(), secret(), nil, request, "", policy)
			if result.Success == marked || result.UpstreamStatus != http.StatusOK || marked && (out.Body.Len() != 0 || result.Diagnostic != "upstream response was rejected") {
				t.Fatalf("wrong embedding boundary: %+v %s", result, out.Body)
			}
		})
	}
}
