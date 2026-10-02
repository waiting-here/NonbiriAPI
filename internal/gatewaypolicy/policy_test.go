package gatewaypolicy

import (
	"strings"
	"testing"
)

func TestCapabilitiesAreScopedToExactEndpointAndModel(t *testing.T) {
	config, err := Parse(`{"models":[{"base_url":"https://gateway.example/native/","model":"anthropic/model-a","adapter":"anthropic_adaptive","efforts":["low","max"],"max_output_tokens":128000,"storage":"omit_false"},{"base_url":"https://gateway.example/native","model":"openai/model-b","adapter":"openai_responses","efforts":["high","max"],"storage":"openai"}]}`)
	if err != nil {
		t.Fatal(err)
	}
	model := config.Lookup("https://gateway.example/native", "anthropic/model-a")
	if !model.AllowsEffort("max") || model.AllowsEffort("high") || model.AllowsEffort("xhigh") || model.MaxOutputTokens != 128000 || model.Storage != OmitFalse {
		t.Fatal("configured capability changed")
	}
	for _, lookup := range [][2]string{{"https://other.example/native", "anthropic/model-a"}, {"https://gateway.example/other", "anthropic/model-a"}, {"https://gateway.example/native", "anthropic/model-other"}} {
		if config.Lookup(lookup[0], lookup[1]).HasReasoning() {
			t.Fatal("capability escaped its exact target")
		}
	}
	if !config.Lookup("https://gateway.example/native", "openai/model-b").AllowsEffort("max") {
		t.Fatal("Responses effort capability lost")
	}
}

func TestInvalidCapabilitiesFailWithoutEchoingConfiguration(t *testing.T) {
	const entry = `{"base_url":"https://private.example/native","model":"private-model","adapter":"openai_chat","efforts":["high"]}`
	for _, raw := range []string{
		`{"models":[` + strings.Replace(entry, `["high"]`, `["max"]`, 1) + `]}`,
		`{"models":[` + strings.Replace(entry, `"adapter":"openai_chat"`, `"adapter":"anthropic_effort","storage":"openai","max_output_tokens":128000`, 1) + `]}`,
		`{"models":[` + strings.Replace(entry, `["high"]`, `["high","high"]`, 1) + `]}`,
		`{"models":[` + entry + `,` + entry + `]}`,
		`{"models":[` + entry[:len(entry)-1] + `,"max_output_tokens":2147483648}]}`,
		`{"models":[` + entry[:len(entry)-1] + `,"unknown":true}]}`,
		`{"models":[` + strings.Replace(entry, `"adapter":"openai_chat"`, `"adapter":"anthropic_effort"`, 1) + `]}`,
		`{"models":[],"models":[]}`, `{"models":null}`,
	} {
		_, err := Parse(raw)
		if err == nil {
			t.Fatal("invalid configuration accepted")
		}
		if strings.Contains(err.Error(), "private") {
			t.Fatal("configuration leaked in error")
		}
	}
}
