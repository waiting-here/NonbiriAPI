package anthropic

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/backend"
	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
)

func TestAttemptPreservesSafeReportedError(t *testing.T) {
	const secret, ciphertext, safety = "reflected-credential-83", "mock-cipher-mock-cipher", "opaque-caller-27"
	for _, status := range []int{200, 429, 529} {
		for _, stream := range []bool{false, true} {
			if status == 200 && stream {
				continue
			}
			t.Run(fmt.Sprintf("status=%d/stream=%t", status, stream), func(t *testing.T) {
				body, err := json.Marshal(map[string]any{"type": "error", "error": map[string]any{
					"type": "overloaded_error", "message": "Please retry later. private.example 203.0.113.9 [2001:db8::9] private-model " + secret + " " + ciphertext + " " + safety,
					"debug": "hidden internal data",
				}})
				if err != nil {
					t.Fatal(err)
				}
				client := &fakeEndpointClient{baseURL: "https://private.example/v1", do: func(*http.Request) (*http.Response, error) {
					response := fakeResponse(status, "application/json", string(body))
					response.Header.Set("Location", "https://private.example/retry")
					response.Header.Set("Set-Cookie", "private=1")
					return response, nil
				}}
				adapter, err := NewAdapter(AdapterConfig{Backend: fakeBackend{open: func(string) (backend.EndpointClient, error) { return client, nil }}})
				if err != nil {
					t.Fatal(err)
				}
				request := mustChatRequest(t, fmt.Sprintf(`{"model":"public/model","messages":[{"role":"user","content":"hello"}],"stream":%t}`, stream))
				keyBytes, cipherBytes := []byte(secret), []byte(ciphertext)
				recorder := httptest.NewRecorder()
				result := adapter.Attempt(context.Background(), recorder, NewTarget(client.baseURL, "private-model", NewCredential(keyBytes, cipherBytes)), request, safety)
				if result.Failure != connectorcontract.FailureUpstream || result.Success || result.Committed || result.UpstreamStatus != status || recorder.Body.Len() != 0 || result.Usage.Present {
					t.Fatalf("result=%+v", result)
				}
				if result.ErrorDetail.Code() != "overloaded_error" || !strings.Contains(result.ErrorDetail.Message(), "Please retry later.") {
					t.Fatalf("detail=%+v", result.ErrorDetail)
				}
				for _, forbidden := range []string{secret, ciphertext, safety, "private.example", "203.0.113.9", "2001:db8", "private-model", "hidden internal data"} {
					if strings.Contains(result.ErrorDetail.Message(), forbidden) || strings.Contains(result.Diagnostic, forbidden) {
						t.Fatalf("error leaked %q", forbidden)
					}
				}
				if strings.Trim(string(keyBytes), "\x00") != "" || strings.Trim(string(cipherBytes), "\x00") != "" || recorder.Header().Get("Location") != "" || recorder.Header().Get("Set-Cookie") != "" {
					t.Fatal("headers or credentials escaped the boundary")
				}
			})
		}
	}
}

func TestAttemptReportsSafeStreamErrorBeforeAndAfterCommit(t *testing.T) {
	for _, committed := range []bool{false, true} {
		t.Run(fmt.Sprintf("committed=%t", committed), func(t *testing.T) {
			stream := ""
			if committed {
				stream = messageStart(`{"input_tokens":1,"output_tokens":0}`)
			}
			stream += namedEvent("error", `{"type":"error","error":{"type":"overloaded_error","message":"Retry later at https://private.example with reflected-credential-83 and opaque-caller-27"}}`) + messageStop()
			client := &fakeEndpointClient{baseURL: "https://private.example/v1", do: func(*http.Request) (*http.Response, error) {
				return fakeResponse(200, "text/event-stream", stream), nil
			}}
			adapter, err := NewAdapter(AdapterConfig{Backend: fakeBackend{open: func(string) (backend.EndpointClient, error) { return client, nil }}})
			if err != nil {
				t.Fatal(err)
			}
			request := mustChatRequest(t, `{"model":"public/model","messages":[{"role":"user","content":"hello"}],"stream":true}`)
			recorder := httptest.NewRecorder()
			result := adapter.Attempt(context.Background(), recorder, NewTarget(client.baseURL, "private-model", NewCredential([]byte("reflected-credential-83"), []byte("mock-cipher-mock-cipher"))), request, "opaque-caller-27")
			if result.Failure != connectorcontract.FailureUpstream || result.Success || result.Committed != committed || result.ErrorDetail.Code() != "overloaded_error" {
				t.Fatalf("result=%+v body=%q", result, recorder.Body.String())
			}
			if !committed {
				if recorder.Body.Len() != 0 {
					t.Fatal("pre-commit error wrote to caller")
				}
				return
			}
			body := recorder.Body.String()
			if recorder.Code != 200 || strings.Count(body, `"error":`) != 1 || strings.Contains(body, "[DONE]") {
				t.Fatalf("terminal=%q", body)
			}
			frames := strings.Split(strings.TrimSpace(body), "\n\n")
			var envelope httperr.Envelope
			if err := json.Unmarshal([]byte(strings.TrimPrefix(frames[len(frames)-1], "data: ")), &envelope); err != nil {
				t.Fatal(err)
			}
			if envelope.Error.Source != httperr.SourceUpstream || envelope.Error.Code != httperr.CodeUpstream || envelope.Error.UpstreamCode != "overloaded_error" || !strings.Contains(envelope.Error.Message, "Retry later") || envelope.Error.Diag != "" {
				t.Fatalf("envelope=%+v", envelope)
			}
			for _, forbidden := range []string{"private.example", "reflected-credential-83", "opaque-caller-27"} {
				if strings.Contains(body, forbidden) {
					t.Fatalf("frame leaked %q", forbidden)
				}
			}
		})
	}
}
