package gemini

import (
	"encoding/json"
	"errors"
	"testing"

	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
)

func TestEffortUsesResolvedGenerateContentModel(t *testing.T) {
	for _, tc := range []struct{ model, effort, want string }{
		{"models/gemini-3.8-flash", "low", `{"thinkingLevel":"LOW"}`},
		{"gemini-3.8-flash", "medium", `{"thinkingLevel":"MEDIUM"}`},
		{"gemini-3.8-flash", "high", `{"thinkingLevel":"HIGH"}`},
		{"gemini-3.8-flash", "minimal", ""}, {"gemini-3.8-flash", "none", ""}, {"gemini-3.8-flash", "xhigh", ""},
		{"gemini-3.6-flash", "minimal", `{"thinkingLevel":"MINIMAL"}`},
		{"gemini-3-pro-preview", "medium", ""}, {"gemini-3.1-pro-preview-customtools", "medium", `{"thinkingLevel":"MEDIUM"}`},
		{"gemini-3.1-flash-lite-image", "low", ""}, {"gemini-3.1-flash-lite", "minimal", `{"thinkingLevel":"MINIMAL"}`},
		{"gemini-4-carbon", "xhigh", ""}, {"gemini-4-argon", "minimal", `{"thinkingLevel":"LOW"}`},
		{"gemini-4-neon", "xhigh", `{"thinkingLevel":"XHIGH"}`}, {"gemini-4-hydrogen", "xhigh", `{"thinkingLevel":"XHIGH"}`},
		{"gemini-2.5-pro", "none", ""}, {"gemini-2.5-pro", "high", `{"thinkingBudget":24576}`},
		{"gemini-2.5-flash", "minimal", `{"thinkingBudget":1024}`}, {"gemini-2.5-flash", "none", `{"thinkingBudget":0}`},
		{"gemini-2.5-flash-lite", "low", `{"thinkingBudget":1024}`}, {"gemini-2.5-flash-lite", "medium", `{"thinkingBudget":8192}`},
		{"gemini-2.5-flash-lite-preview-09-2025", "none", `{"thinkingBudget":0}`},
		{"gemini-4-neon-proxy", "xhigh", ""}, {"my-gemini-3.8-flash", "low", ""}, {"gemini-flash-latest", "low", ""},
	} {
		t.Run(tc.model+"/"+tc.effort, func(t *testing.T) {
			r := request(t, `{"model":"public","messages":[{"role":"user","content":"hello"}],"reasoning_effort":"`+tc.effort+`"}`)
			if !SupportsRequest(r) {
				t.Fatal("general protocol check must defer model selection")
			}
			body, err := Compile(r, tc.model, contract.AttemptPolicy{})
			if tc.want == "" {
				checkGenerationRejection(t, err, "reasoning_effort", "effort is not enabled for this model")
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			var out struct {
				GenerationConfig struct{ ThinkingConfig json.RawMessage }
			}
			if err := json.Unmarshal(body, &out); err != nil {
				t.Fatal(err)
			}
			equalJSON(t, out.GenerationConfig.ThinkingConfig, tc.want)
		})
	}
}

func checkGenerationRejection(t *testing.T, err error, field, reason string) {
	t.Helper()
	var rejected *contract.RequestRejection
	if !errors.As(err, &rejected) || rejected.Field != field || rejected.Reason != reason {
		t.Fatalf("got %v; want %s: %s", err, field, reason)
	}
}

func TestNativeGenerationSamplingAndLimits(t *testing.T) {
	const prefix = `{"model":"public","messages":[{"role":"user","content":"hi"}],`
	r := request(t, prefix+`"seed":-2147483648,"presence_penalty":-2,"frequency_penalty":1.5,"top_k":40}`)
	b, err := Compile(r, "gemini-2.5-flash", contract.AttemptPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]json.RawMessage
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	equalJSON(t, out["generationConfig"], `{"seed":-2147483648,"presencePenalty":-2,"frequencyPenalty":1.5,"topK":40}`)
	for _, tc := range []struct{ model, field, value, reason string }{
		{"gemini-2.5-pro", "seed", "2147483648", "expected a signed 32-bit integer"},
		{"gemini-2.5-pro", "seed", "0.5", "expected a signed 32-bit integer"},
		{"gemini-2.5-pro", "frequency_penalty", "2", "expected a number from -2 inclusive to 2 exclusive"},
		{"gemini-2.5-pro", "presence_penalty", "-2.01", "expected a number from -2 to 2"},
		{"gemini-2.5-pro", "top_k", "0", "expected an integer from 1 to 2147483647"},
		{"gemini-3.8-flash", "presence_penalty", "0.5", "sampling parameter is not supported by this model"},
		{"gemini-4-neon", "frequency_penalty", "0", "sampling parameter is not supported by this model"},
		{"gemini-3.6-flash", "top_k", "5", "sampling parameter is not supported by this model"},
		{"gemini-3.6-flash", "reasoning_effort", `"caller-private"`, "unsupported effort value"},
	} {
		_, err := Compile(request(t, prefix+`"`+tc.field+`":`+tc.value+`}`), tc.model, contract.AttemptPolicy{})
		checkGenerationRejection(t, err, tc.field, tc.reason)
	}
	custom, err := Compile(request(t, prefix+`"presence_penalty":2,"frequency_penalty":2}`), "custom-endpoint", contract.AttemptPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(custom, &out); err != nil {
		t.Fatal(err)
	}
	equalJSON(t, out["generationConfig"], `{"presencePenalty":2,"frequencyPenalty":2}`)
	defaults := request(t, prefix+`"seed":0,"reasoning_effort":null,"temperature":0,"top_p":0.7}`)
	b, err = Compile(defaults, "gemini-3.8-flash", contract.AttemptPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	equalJSON(t, out["generationConfig"], `{"temperature":0,"topP":0.7,"seed":0}`)
}

func TestNativeThinkingFieldCollisionAndRanges(t *testing.T) {
	const prefix = "/generationConfig/thinkingConfig"
	const body = `{"model":"public","messages":[{"role":"user","content":"hi"}]`
	withEffort := request(t, body+`,"reasoning_effort":"low"}`)
	withoutEffort := request(t, body+`}`)
	for _, key := range []string{prefix, prefix + "/includeThoughts"} {
		flag := json.RawMessage("true")
		if key == prefix {
			flag = json.RawMessage(`{"includeThoughts":true}`)
		}
		native := map[string]json.RawMessage{key: flag}
		compiled, err := Compile(withEffort, "gemini-3.8-flash", contract.AttemptPolicy{NativeExtensions: native})
		if err != nil {
			t.Fatal(err)
		}
		var out struct {
			GenerationConfig struct{ ThinkingConfig json.RawMessage }
		}
		if err := json.Unmarshal(compiled, &out); err != nil {
			t.Fatal(err)
		}
		equalJSON(t, out.GenerationConfig.ThinkingConfig, `{"includeThoughts":true,"thinkingLevel":"LOW"}`)
		if len(native) != 1 || string(native[key]) != string(flag) {
			t.Fatal("changed stored native extension")
		}
	}
	for _, tc := range []struct{ path, value string }{
		{prefix, `{"includeThoughts":true,"thinkingLevel":"LOW"}`}, {prefix, `{"thinkingBudget":1024}`},
		{prefix + "/thinkingBudget", "128"}, {prefix + "/thinkingLevel", `"LOW"`},
	} {
		_, err := Compile(withEffort, "gemini-3.8-flash", contract.AttemptPolicy{NativeExtensions: map[string]json.RawMessage{tc.path: json.RawMessage(tc.value)}})
		checkGenerationRejection(t, err, "reasoning_effort", "reasoning effort conflicts with native thinking settings")
	}
	for _, tc := range []struct {
		model, key, value string
		allowed           bool
	}{
		{"gemini-2.5-pro", "thinkingBudget", "127", false}, {"gemini-2.5-pro", "thinkingBudget", "128", true},
		{"gemini-2.5-pro", "thinkingBudget", "32768", true}, {"gemini-2.5-pro", "thinkingBudget", "32769", false},
		{"gemini-2.5-flash-lite", "thinkingBudget", "511", false}, {"gemini-2.5-flash-lite", "thinkingBudget", "512", true},
		{"gemini-2.5-flash-lite", "thinkingBudget", "-1", true}, {"gemini-2.5-flash", "thinkingBudget", "0", true},
		{"gemini-2.5-flash", "thinkingBudget", "24577", false}, {"gemini-2.5-pro", "thinkingLevel", `"HIGH"`, false},
		{"gemini-3.8-flash", "thinkingBudget", "0", false}, {"gemini-3.8-flash", "thinkingLevel", `"MINIMAL"`, false},
		{"gemini-4-neon", "thinkingLevel", `"XHIGH"`, true}, {"gemini-4-argon", "thinkingLevel", `"XHIGH"`, false},
		{"private", "thinkingBudget", "128", true}, {"private", "thinkingLevel", `"XHIGH"`, false},
	} {
		_, err := Compile(withoutEffort, tc.model, contract.AttemptPolicy{NativeExtensions: map[string]json.RawMessage{prefix + "/" + tc.key: json.RawMessage(tc.value)}})
		if tc.allowed {
			if err != nil {
				t.Fatal(tc, err)
			}
		} else {
			checkGenerationRejection(t, err, "native_extensions", "native thinking setting is not supported by this model")
		}
	}
}
