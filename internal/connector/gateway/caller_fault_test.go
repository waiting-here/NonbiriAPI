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
	"github.com/waiting-here/NonbiriAPI/internal/smartbackoff"
	"github.com/waiting-here/NonbiriAPI/internal/upstreamerror"
)

func TestGatewayCallerFaultKeepsRejectedResponse(t *testing.T) {
	for _, kind := range []contract.Type{contract.TypeAISDKGatewayV3, contract.TypeAISDKGatewayV4} {
		for _, stream := range []bool{false, true} {
			for _, tc := range []struct{ name, role, budget, message string }{
				{"model turn", "assistant", `"max_tokens":128,`, "Requests ending with a model turn are not supported."},
				{"output budget", "user", `"max_completion_tokens":100000,`, "Unable to submit request because it has a maxOutputTokens value of 100000 but the supported range is from 1 (inclusive) to 65537 (exclusive). Update the value and try again."},
				{"legacy output budget", "user", `"max_tokens":100000,`, "Unable to submit request because it has a maxOutputTokens value of 100000 but the supported range is from 1 (inclusive) to 65537 (exclusive). Update the value and try again."},
			} {
				t.Run(fmt.Sprintf("%s/%t/%s", kind, stream, tc.name), func(t *testing.T) {
					payload, _ := json.Marshal(map[string]any{"error": map[string]any{"type": "AI_APICallError", "message": tc.message, "param": map[string]any{"type": "AI_APICallError", "statusCode": 400, "message": tc.message, "isRetryable": false}}})
					b := &fakeBackend{do: func(r *http.Request) (*http.Response, error) {
						body, err := io.ReadAll(r.Body)
						if err != nil || !strings.Contains(string(body), `"prompt"`) || !strings.Contains(string(body), `"maxOutputTokens"`) {
							t.Fatal("native request was not compiled", err)
						}
						return &http.Response{StatusCode: 400, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(string(payload)))}, nil
					}}
					a, err := NewAdapter(b)
					if err != nil {
						t.Fatal(err)
					}
					request := chat(t, fmt.Sprintf(`{"model":"test","messages":[{"role":"%s","content":"hello"}],%s"stream":%t}`, tc.role, tc.budget, stream))
					defer request.Clear()
					captured := ""
					ctx := upstreamerror.WithCapture(smartbackoff.WithCharityCall(t.Context(), true), func(_ context.Context, event upstreamerror.Event) { captured = string(event.Bytes()) })
					writer := httptest.NewRecorder()
					result := a.Attempt(ctx, writer, contract.NewTarget(kind, target().BaseURL(), "provider/private-model"), secret(), request, nil, "")
					if result.Success || result.Committed || result.Failure != contract.FailureUpstream || result.FailureOrigin != contract.OriginUpstreamResponse || result.StreakDisposition != contract.StreakNeutral || result.UpstreamStatus != 400 || writer.Body.Len() != 0 || b.calls != 1 {
						t.Fatalf("rejected outcome changed: %+v", result)
					}
					if result.ErrorDetail.Code() != "AI_APICallError" || result.ErrorDetail.Message() != tc.message || !result.ErrorDetail.CallerFault().Matches(400, "AI_APICallError") || captured != string(payload) {
						t.Fatal("caller evidence or original diagnostics lost")
					}
				})
			}
		}
	}
}

func TestGatewayCallerFaultDoesNotExemptUnprovenRequests(t *testing.T) {
	const payload = `{"error":{"type":"AI_APICallError","message":"Requests ending with a model turn are not supported.","param":{"type":"AI_APICallError","statusCode":400,"isRetryable":false,"message":"Requests ending with a model turn are not supported."}}}`
	for _, tc := range []struct {
		name     string
		personal bool
		status   int
		policy   contract.AttemptPolicy
	}{
		{"personal", true, 400, contract.AttemptPolicy{}},
		{"adapted", false, 400, contract.AttemptPolicy{HasAdaptation: true}},
		{"accepted", false, 200, contract.AttemptPolicy{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := &fakeBackend{do: func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: tc.status, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(payload))}, nil
			}}
			a, err := NewAdapter(b)
			if err != nil {
				t.Fatal(err)
			}
			request := chat(t, `{"model":"test","messages":[{"role":"assistant","content":"hello"}]}`)
			defer request.Clear()
			result := a.AttemptWithPolicy(smartbackoff.WithCharityCall(t.Context(), !tc.personal), httptest.NewRecorder(), target(), secret(), request, nil, "", tc.policy)
			want := contract.StreakUpstreamFailure
			if !tc.personal && tc.status == 400 {
				want = contract.StreakNeutral
			}
			if result.Success || result.Committed || result.StreakDisposition != want || result.ErrorDetail.CallerFault().Matches(400, "AI_APICallError") != (want == contract.StreakNeutral) {
				t.Fatalf("unproven response exempted: %+v", result)
			}
		})
	}
}
