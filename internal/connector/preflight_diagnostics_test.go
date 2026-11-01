package connector

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/connector/openai"
	"github.com/waiting-here/NonbiriAPI/internal/requestattempt"
)

func requirePreflightDetail(t *testing.T, err error, field, reason string) {
	t.Helper()
	var rejected *contract.RequestRejection
	if !errors.As(err, &rejected) || rejected.Field != field || rejected.Reason != reason {
		t.Fatalf("preflight detail = %v; want %s: %s", err, field, reason)
	}
	detail := requestattempt.NewDetail(rejected.Field, rejected.Reason)
	decoded := requestattempt.DecodeDetail(requestattempt.EncodeDetail(detail))
	if decoded == nil || decoded.Field != field || decoded.Reason != reason || decoded.Suggestion == "" {
		t.Fatalf("persisted detail = %#v", decoded)
	}
	if strings.Contains(err.Error(), "PRIVATE") || strings.Contains(requestattempt.EncodeDetail(detail), "PRIVATE") {
		t.Fatal("caller values entered the diagnostic")
	}
}

func TestInferencePreflightPreservesSpecificChecks(t *testing.T) {
	registry := NewDefaultRegistry()
	const conversion = "Responses fields cannot be represented by the selected Chat protocol"
	for _, tc := range []struct {
		name, body, field, reason string
		kind                      contract.Type
		operation                 contract.Operation
		headers                   http.Header
	}{
		{"chat reasoning", `{"model":"m","input":"PRIVATE_INPUT","reasoning":{"effort":"low"}}`, "reasoning", conversion, contract.TypeOpenAIChatCompletions, contract.OperationResponses, nil},
		{"gateway v3 tools", `{"model":"m","input":"PRIVATE_INPUT","tools":[{"type":"web_search"}]}`, "tools", conversion, contract.TypeAISDKGatewayV3, contract.OperationResponses, nil},
		{"gateway v4 text", `{"model":"m","input":"PRIVATE_INPUT","text":{"verbosity":"low"}}`, "text", conversion, contract.TypeAISDKGatewayV4, contract.OperationResponses, nil},
		{"unknown option", `{"model":"m","input":"PRIVATE_INPUT","PRIVATE_OPTION":"PRIVATE_VALUE"}`, "request", conversion, contract.TypeOpenAIChatCompletions, contract.OperationResponses, nil},
		{"incomplete tool history", `{"model":"m","input":[{"type":"function_call_output","call_id":"PRIVATE_CALL","output":"PRIVATE_OUTPUT"}]}`, "input", "Responses input cannot be represented as complete Chat messages", contract.TypeOpenAIChatCompletions, contract.OperationResponses, nil},
		{"rerank documents", `{"model":"m","query":"PRIVATE_QUERY","documents":[{"image":"PRIVATE_IMAGE"}]}`, "documents", "the selected protocol requires text documents", contract.TypeCohereRerank, contract.OperationRerank, nil},
		{"system option", `{"model":"m","state":"PRIVATE_STATE","questions":{"q":{"type":"noul"}}}`, "headers", "System One option headers require a single true value; images and box questions require the images opt-in", contract.TypeTypeSafeSystemOne, contract.OperationSystemOne, http.Header{"Typesafe-Danger-Zone-Images": []string{"PRIVATE_VALUE"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request, err := openai.DecodeInferenceRequest(strings.NewReader(tc.body), tc.operation, 0)
			if err != nil {
				t.Fatal(err)
			}
			defer request.Clear()
			request.SetOptionHeaders(tc.headers)
			target := contract.NewTarget(tc.kind, "https://upstream.example", "model")
			requirePreflightDetail(t, registry.CheckInferenceTarget(target, request, contract.AttemptPolicy{}), tc.field, tc.reason)
		})
	}
}

func TestChatPreflightReportsFidelityAndTypedValidation(t *testing.T) {
	registry := NewDefaultRegistry()
	for _, tc := range []struct {
		name, body, field, reason string
		kind                      contract.Type
	}{
		{"stream null", `{"model":"m","messages":[{"role":"user","content":"PRIVATE_INPUT"}],"stream":null}`, "stream", "expected a boolean", contract.TypeOpenAICompatible},
		{"foreign continuation", `{"model":"m","messages":[{"role":"assistant","content":"PRIVATE_INPUT","extra_content":{"google":{"thoughtSignature":"PRIVATE_SIGNATURE"}}}]}`, "extra_content.google", "field cannot be represented by this connector", contract.TypeOpenAIChatCompletions},
		{"parallel tools type", `{"model":"m","messages":[{"role":"user","content":"PRIVATE_INPUT"}],"parallel_tool_calls":"PRIVATE_VALUE"}`, "parallel_tool_calls", "expected a boolean or null", contract.TypeAnthropicCompatible},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request, err := openai.DecodeChatRequest(strings.NewReader(tc.body), 0)
			if err != nil {
				t.Fatal(err)
			}
			defer request.Clear()
			requirePreflightDetail(t, registry.CheckTargetRequest(contract.NewTarget(tc.kind, "https://upstream.example", "model"), contract.OperationChatCompletions, request, nil, contract.AttemptPolicy{}), tc.field, tc.reason)
		})
	}
}

func TestPreflightReportsUnsupportedPoliciesWithoutChangingAdmission(t *testing.T) {
	registry := NewDefaultRegistry()
	inference, err := openai.DecodeInferenceRequest(strings.NewReader(`{"model":"m","input":"PRIVATE_INPUT"}`), contract.OperationResponses, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer inference.Clear()
	decision, err := openai.DecodeDecisionRequest(strings.NewReader(`{"model":"m","input":"PRIVATE_INPUT","questions":[{"type":"predicate","name":"q","instructions":"PRIVATE_INSTRUCTIONS"}]}`), 0)
	if err != nil {
		t.Fatal(err)
	}
	defer decision.Clear()
	for _, tc := range []struct {
		name, field, reason string
		policy              contract.AttemptPolicy
	}{
		{"extensions", "native_extensions", "field cannot be represented by this connector", contract.AttemptPolicy{NativeExtensions: map[string]json.RawMessage{"/PRIVATE_OPTION": json.RawMessage(`"PRIVATE_VALUE"`)}}},
		{"flatten", "flatten_tool_calls", "field cannot be represented by this connector", contract.AttemptPolicy{FlattenToolCalls: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requirePreflightDetail(t, registry.CheckInferenceTarget(contract.NewTarget(contract.TypeOpenAICompatible, "https://upstream.example", "model"), inference, tc.policy), tc.field, tc.reason)
			requirePreflightDetail(t, registry.CheckDecisionTarget(contract.NewTarget(contract.TypeOpenAIDecisions, "https://upstream.example", "model"), decision, tc.policy), tc.field, tc.reason)
		})
	}
	requirePreflightDetail(t, registry.CheckDecisionTarget(contract.NewTarget(contract.TypeOpenAIDecisions, "https://upstream.example", "model"), decision, contract.AttemptPolicy{ForceStoreFalse: true}), "store", "OpenAI key storage policy is incompatible with this connector")
	requirePreflightDetail(t, registry.CheckInferenceTarget(contract.NewTarget(contract.TypeCohereRerank, "https://upstream.example", "model"), inference, contract.AttemptPolicy{}), "request", "unsupported operation")
	if err := registry.CheckInferenceTarget(contract.NewTarget(contract.TypeOpenAICompatible, "https://upstream.example", "model"), inference, contract.AttemptPolicy{}); err != nil {
		t.Fatal(err)
	}
	if err := registry.CheckDecisionTarget(contract.NewTarget(contract.TypeOpenAIDecisions, "https://upstream.example", "model"), decision, contract.AttemptPolicy{}); err != nil {
		t.Fatal(err)
	}
}

func TestPreflightNormalizationKeepsOnlyRegisteredValidationDetails(t *testing.T) {
	valid := &openai.ValidationError{Detail: requestattempt.NewDetail("headers", "expected a boolean")}
	requirePreflightDetail(t, normalizePreflightError(fmt.Errorf("PRIVATE_WRAPPER: %w", valid)), "headers", "expected a boolean")
	for _, detail := range []*requestattempt.RejectionDetail{
		nil,
		{Field: "headers", Reason: "PRIVATE_REASON"},
	} {
		requirePreflightDetail(t, normalizePreflightError(&openai.ValidationError{Detail: detail}), "request", "request cannot be represented by this connector")
	}
	requirePreflightDetail(t, normalizePreflightError(&openai.ValidationError{Detail: &requestattempt.RejectionDetail{Field: "PRIVATE_FIELD", Reason: "expected a boolean", Suggestion: "PRIVATE_SUGGESTION"}}), "request", "expected a boolean")
}
