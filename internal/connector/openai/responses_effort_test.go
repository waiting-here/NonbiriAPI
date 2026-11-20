package openai

import (
	"encoding/json"
	"testing"

	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
)

func TestResponsesEffortMapsToChatAndPreservesNativeWire(t *testing.T) {
	for _, effort := range []string{"none", "minimal", "low", "medium", "high", "xhigh"} {
		r := inferenceRequestForTest(t, contract.OperationResponses, `{"model":"public","input":"hi","reasoning":{"effort":"`+effort+`"}}`)
		for _, kind := range []contract.Type{contract.TypeOpenAIChatCompletions, contract.TypeOpenAIResponses, contract.TypeOpenAICompatible} {
			if err := r.CheckTarget(kind, contract.AttemptPolicy{}); err != nil {
				t.Fatal(kind, err)
			}
			wire, _, err := r.marshalInference("real", kind, contract.AttemptPolicy{})
			if err != nil {
				t.Fatal(err)
			}
			var o map[string]json.RawMessage
			if err := json.Unmarshal(wire, &o); err != nil {
				t.Fatal(err)
			}
			if kind == contract.TypeOpenAIChatCompletions {
				if string(o["reasoning_effort"]) != `"`+effort+`"` || o["reasoning"] != nil {
					t.Fatalf("%s: %s", kind, wire)
				}
			} else if string(o["reasoning"]) != `{"effort":"`+effort+`"}` || o["reasoning_effort"] != nil {
				t.Fatalf("%s: %s", kind, wire)
			}
		}
	}
}
