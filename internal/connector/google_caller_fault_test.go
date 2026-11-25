package connector

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/smartbackoff"
)

func TestGoogleInvalidArgumentAcrossChatConnectors(t *testing.T) {
	const turn = `{"error":{"code":400,"message":"Requests ending with a model turn are not supported.","status":"INVALID_ARGUMENT"}}`
	const fields = `{"error":{"code":400,"message":"Invalid settings.","status":"INVALID_ARGUMENT","details":[{"@type":"type.googleapis.com/google.rpc.BadRequest","fieldViolations":[{"field":"generationConfig.temperature"},{"field":"generationConfig.maxOutputTokens"}]}]}}`
	const credential = `{"error":{"code":400,"message":"API key not valid.","status":"INVALID_ARGUMENT","details":[{"@type":"type.googleapis.com/google.rpc.ErrorInfo","reason":"API_KEY_INVALID","domain":"googleapis.com"}]}}`
	const length = `{"error":{"code":400,"message":"The input token count (129) exceeds the maximum number of tokens allowed (128).","status":"INVALID_ARGUMENT"}}`
	for _, kind := range []contract.Type{contract.TypeGoogleGemini, contract.TypeOpenAICompatible, contract.TypeOpenAIResponses, contract.TypeAnthropicCompatible, contract.TypeAISDKGatewayV3, contract.TypeAISDKGatewayV4} {
		for _, tc := range []struct {
			name, role, payload      string
			stream, adapted, neutral bool
		}{
			{"reported turn", "assistant", turn, false, false, true},
			{"stream refused before output", "assistant", turn, true, false, true},
			{"caller ended with user", "user", turn, false, false, true},
			{"configured rewrite", "assistant", turn, false, true, true},
			{"explicit budget and temperature", "user", fields, false, false, true},
			{"input token limit", "user", length, false, false, true},
			{"invalid key", "assistant", credential, false, false, false},
		} {
			t.Run(string(kind)+"/"+tc.name, func(t *testing.T) {
				body := fmt.Sprintf(`{"model":"public","messages":[{"role":"user","content":"hello"},{"role":%q,"content":"hi"}],"temperature":0.5,"max_tokens":128,"stream":%t}`, tc.role, tc.stream)
				input := gatewayWiringInput(t, kind, contract.OperationChatCompletions, body)
				input.Policy.HasAdaptation = tc.adapted
				b := &gatewayWiringBackend{do: func(r *http.Request) (*http.Response, error) {
					return &http.Response{StatusCode: 400, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(tc.payload))}, nil
				}}
				driver, err := NewDefaultRegistry().NewConnector(kind, Dependencies{Backend: b})
				if err != nil {
					t.Fatal(err)
				}
				got := driver.Attempt(smartbackoff.WithCharityCall(t.Context(), true), input)
				if b.calls != 1 || got.Success || got.Committed || got.UpstreamStatus != 400 || got.FailureOrigin != contract.OriginUpstreamResponse ||
					(got.StreakDisposition == contract.StreakNeutral) != tc.neutral || got.ErrorDetail.CallerFault().Matches(400, "400") != tc.neutral {
					t.Fatalf("calls=%d, result=%+v", b.calls, got)
				}
			})
		}
	}
}

func TestGoogleInvalidArgumentOnResponsesInput(t *testing.T) {
	for _, kind := range []contract.Type{contract.TypeOpenAICompatible, contract.TypeOpenAIResponses, contract.TypeOpenAIChatCompletions} {
		t.Run(string(kind), func(t *testing.T) {
			input := gatewayWiringInput(t, kind, contract.OperationResponses, `{"model":"public","input":[{"role":"user","content":"hello"},{"role":"assistant","content":"hi"}],"max_output_tokens":128,"store":false}`)
			b := &gatewayWiringBackend{do: func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 400, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"error":{"code":400,"status":"INVALID_ARGUMENT","message":"Requests ending with a model turn are not supported."}}`))}, nil
			}}
			driver, err := NewDefaultRegistry().NewConnector(kind, Dependencies{Backend: b})
			if err != nil {
				t.Fatal(err)
			}
			got := driver.Attempt(smartbackoff.WithCharityCall(t.Context(), true), input)
			if b.calls != 1 || got.StreakDisposition != contract.StreakNeutral || !got.ErrorDetail.CallerFault().Matches(400, "400") {
				t.Fatalf("calls=%d result=%+v", b.calls, got)
			}
		})
	}
}
