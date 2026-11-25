package openai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSameReasoningWireAcrossUnrelatedTargets(t *testing.T) {
	for _, model := range []string{"community/alpha", "unlabelled-relay-27"} {
		t.Run(model, func(t *testing.T) {
			// Only the configured target changes. Neither the wire nor the client
			// supplies any routing hint about the origin of the typed blocks.
			const message = `{"role":"assistant","content":"answer","reasoning_details":[{"type":"reasoning.text","text":"plan","signature":"proof"},{"type":"reasoning.encrypted","data":"opaque"}]}`
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				var body struct{ Model string }
				if err := json.NewDecoder(req.Body).Decode(&body); err != nil || body.Model != model {
					t.Errorf("upstream model = %q, err = %v", body.Model, err)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, strings.Replace(validCompletion, `{"role":"assistant","content":"ok"}`, message, 1))
			}))
			defer server.Close()
			adapter := adapterForServer(t, server.URL, nil, nil)
			target := NewTarget(server.URL, model, NewCredential([]byte("test-only-key"), nil))
			req := decodeAdapterRequest(t, `{"model":"public","messages":[{"role":"user","content":"question"}]}`)
			writer := httptest.NewRecorder()
			result := adapter.Attempt(context.Background(), writer, target, req, "fixture-caller")
			if !result.Success || result.Usage.OutputTokens != 3 {
				t.Fatalf("attempt = %+v", result)
			}
			var response struct {
				Choices []struct{ Message map[string]json.RawMessage }
			}
			if err := json.Unmarshal(writer.Body.Bytes(), &response); err != nil || len(response.Choices) != 1 {
				t.Fatal(writer.Body.String(), err)
			}
			out := response.Choices[0].Message
			if string(out["content"]) != `"answer"` || string(out["reasoning_content"]) != `"plan"` ||
				!strings.Contains(string(out["reasoning_details"]), `"signature":"proof"`) || !strings.Contains(string(out["reasoning_details"]), `"data":"opaque"`) {
				t.Fatal(out)
			}
		})
	}
}
