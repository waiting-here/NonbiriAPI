package connector

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/waiting-here/NonbiriAPI/internal/gatewaypolicy"
	"strings"
	"testing"

	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/connector/openai"
)

func TestGeminiEffortPreflightUsesThePhysicalTarget(t *testing.T) {
	r, err := openai.DecodeChatRequest(strings.NewReader(`{"model":"gemini-4-neon","messages":[{"role":"user","content":"hi"}],"reasoning_effort":"xhigh"}`), 0)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Clear()
	reg := NewDefaultRegistry()
	if !reg.SupportsRequest(contract.TypeGoogleGemini, r) {
		t.Fatal("routing rejected a representable request before resolving the target")
	}
	for _, tc := range []struct {
		model   string
		allowed bool
	}{{"models/gemini-4-hydrogen", true}, {"gemini-3.8-flash", false}, {"private-4-neon", false}} {
		err := reg.CheckTargetRequest(contract.NewTarget(contract.TypeGoogleGemini, "https://example.invalid", tc.model), contract.OperationChatCompletions, r, nil, contract.AttemptPolicy{})
		if tc.allowed {
			if err != nil {
				t.Fatal(err)
			}
		} else {
			requirePreflightDetail(t, err, "reasoning_effort", "effort is not enabled for this model")
		}
	}
}

func TestGenerationEffortDispatchMatchesPreflight(t *testing.T) {
	reg := NewDefaultRegistry()
	for _, tc := range []struct {
		kind               contract.Type
		operation          contract.Operation
		model, input, want string
	}{
		{contract.TypeGoogleGemini, contract.OperationChatCompletions, "models/gemini-3.8-flash", `{"model":"public","messages":[{"role":"user","content":"hi"}],"reasoning_effort":"medium"}`, "MEDIUM"},
		{contract.TypeGoogleGemini, contract.OperationChatCompletions, "gemini-3.8-flash", `{"model":"public","messages":[{"role":"user","content":"hi"}],"reasoning_effort":"xhigh"}`, ""},
		{contract.TypeAISDKGatewayV4, contract.OperationResponses, "private", `{"model":"public","input":"hi","reasoning":{"effort":"medium"}}`, "medium"},
		{contract.TypeOpenAIChatCompletions, contract.OperationResponses, "private", `{"model":"public","input":"hi","reasoning":{"effort":"medium"}}`, "medium"},
	} {
		t.Run(string(tc.kind)+"/"+tc.want, func(t *testing.T) {
			input := gatewayWiringInput(t, tc.kind, tc.operation, tc.input)
			input.Target = contract.NewTarget(tc.kind, input.Target.BaseURL(), tc.model)
			if tc.kind == contract.TypeAISDKGatewayV4 {
				input.Policy.GatewayModel = &gatewaypolicy.Model{Storage: gatewaypolicy.OmitFalse}
			}
			preflight := gatewayWiringCheck(reg, input)
			if (preflight == nil) != (tc.want != "") {
				t.Fatalf("unexpected preflight: %v", preflight)
			}
			b := &gatewayWiringBackend{do: func(req *http.Request) (*http.Response, error) {
				if tc.want == "" {
					t.Fatal("rejected request reached transport")
				}
				var wire map[string]json.RawMessage
				if err := json.NewDecoder(req.Body).Decode(&wire); err != nil {
					t.Fatal(err)
				}
				switch tc.kind {
				case contract.TypeGoogleGemini:
					if !strings.HasSuffix(req.URL.Path, "/models/gemini-3.8-flash:generateContent") || string(wire["generationConfig"]) != `{"thinkingConfig":{"thinkingLevel":"MEDIUM"}}` || wire["reasoning_effort"] != nil {
						t.Fatalf("wrong Gemini wire: %s %s", req.URL.Path, wire)
					}
					return gatewayWiringResponse(`{"candidates":[{"index":0,"content":{"role":"model","parts":[{"text":"answer"}]},"finishReason":"STOP"}]}`, false), nil
				case contract.TypeAISDKGatewayV4:
					if string(wire["reasoning"]) != `"medium"` || wire["reasoning_effort"] != nil {
						t.Fatalf("wrong Gateway wire: %s", wire)
					}
					return gatewayWiringResponse(gatewayWiringJSON, false), nil
				default:
					if string(wire["reasoning_effort"]) != `"medium"` || wire["reasoning"] != nil {
						t.Fatalf("wrong Chat wire: %s", wire)
					}
					return gatewayWiringResponse(`{"id":"c1","object":"chat.completion","created":1,"model":"private","choices":[{"index":0,"message":{"role":"assistant","content":"answer"},"finish_reason":"stop"}]}`, false), nil
				}
			}}
			driver, err := reg.NewConnector(tc.kind, Dependencies{Backend: b})
			if err != nil {
				t.Fatal(err)
			}
			result := driver.Attempt(context.Background(), input)
			if tc.want == "" {
				if result.Success || b.calls != 0 {
					t.Fatalf("rejected dispatch: %+v", result)
				}
				return
			}
			if !result.Success || b.calls != 1 {
				t.Fatalf("dispatch: %+v calls=%d", result, b.calls)
			}
		})
	}
}
