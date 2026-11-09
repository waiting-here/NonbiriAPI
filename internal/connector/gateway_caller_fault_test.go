package connector

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/gatewaypolicy"
	"github.com/waiting-here/NonbiriAPI/internal/smartbackoff"
)

func TestGatewayCallerFaultResponsesAndOperationBoundary(t *testing.T) {
	const message = "Unable to submit request because it has a maxOutputTokens value of 100000 but the supported range is from 1 (inclusive) to 65537 (exclusive). Update the value and try again."
	payload := fmt.Sprintf(`{"error":{"type":"AI_APICallError","message":%q,"param":{"type":"AI_APICallError","message":%q,"statusCode":400,"isRetryable":false}}}`, message, message)
	for _, kind := range []contract.Type{contract.TypeAISDKGatewayV3, contract.TypeAISDKGatewayV4} {
		for _, tc := range []struct {
			op   contract.Operation
			body string
		}{
			{contract.OperationResponses, `{"model":"public","input":"hello","max_output_tokens":100000,"store":false}`},
			{contract.OperationEmbeddings, `{"model":"public","input":"hello"}`},
			{contract.OperationImagesGenerations, `{"model":"public","prompt":"hello"}`},
			{contract.OperationRerank, `{"model":"public","query":"hello","documents":["one"]}`},
		} {
			t.Run(fmt.Sprintf("%s/%s", kind, tc.op), func(t *testing.T) {
				input := gatewayWiringInput(t, kind, tc.op, tc.body)
				input.Policy.GatewayModel = &gatewaypolicy.Model{Storage: gatewaypolicy.OmitFalse}
				b := &gatewayWiringBackend{do: func(r *http.Request) (*http.Response, error) {
					var sent map[string]json.RawMessage
					if err := json.NewDecoder(r.Body).Decode(&sent); err != nil {
						t.Fatal(err)
					}
					if tc.op == contract.OperationResponses && string(sent["maxOutputTokens"]) != "100000" {
						t.Fatal("Responses budget was not translated")
					}
					return &http.Response{StatusCode: 400, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(payload))}, nil
				}}
				driver, err := NewDefaultRegistry().NewConnector(kind, Dependencies{Backend: b})
				if err != nil {
					t.Fatal(err)
				}
				result := driver.Attempt(smartbackoff.WithCharityCall(t.Context(), true), input)
				want := contract.StreakUpstreamFailure
				if tc.op == contract.OperationResponses {
					want = contract.StreakNeutral
				}
				if b.calls != 1 || result.Success || result.Committed || result.UpstreamStatus != 400 || result.FailureOrigin != contract.OriginUpstreamResponse || result.StreakDisposition != want || result.ErrorDetail.CallerFault().Matches(400, "AI_APICallError") != (want == contract.StreakNeutral) {
					t.Fatalf("calls=%d result=%+v", b.calls, result)
				}
			})
		}
	}
}
