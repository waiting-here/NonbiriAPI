package imageactivity

import (
	"encoding/json"
	"reflect"
	"testing"
)

func fixedMetadata(settings, supports string) json.RawMessage {
	return json.RawMessage(`{"id":"atelier-test","atelier_type":"image","atelier_friendly_name":"Synthetic studio","atelier_settings":` + settings + `,"atelier_supports":` + supports + `}`)
}

func TestFixedCapabilityFourSizeModesAndSingleResolution(t *testing.T) {
	for _, tc := range []struct {
		name, settings string
		mode           SizeMode
		input          SizeInput
		values         map[ParameterKey]any
		tier           string
	}{
		{"single resolution", `{"customSizeMapping":{"compact":{"1:1":{"width":512,"height":512},"16:9":{"width":768,"height":432}}},"resolution":{"default":"compact"},"aspectRatio":{"default":"1:1"}}`, ResolutionRatioGrid, SizeInput{Ratio: "16:9", Resolution: "compact"}, map[ParameterKey]any{Size: "768x432", AspectRatio: "16:9", Resolution: "compact"}, "compact"},
		{"mapped string", `{"customSizeMappingString":{"16:9":"768x432","9:16":"432x768"}}`, RatioSizeMap, SizeInput{Ratio: "9:16"}, map[ParameterKey]any{Size: "432x768", AspectRatio: "9:16"}, ""},
		{"ratio and resolution", `{"aspectRatio":{"options":["1:1","16:9"],"default":"16:9"},"resolution":{"options":["compact"],"default":"compact"}}`, RatioResolution, SizeInput{Ratio: "16:9", Resolution: "compact"}, map[ParameterKey]any{AspectRatio: "16:9", Resolution: "compact"}, "compact"},
		{"ratio alone", `{"aspectRatio":{"options":["1:1","16:9"]}}`, RatioResolution, SizeInput{Ratio: "1:1"}, map[ParameterKey]any{AspectRatio: "1:1"}, ""},
		{"bounded axes", `{}`, WidthHeight, SizeInput{Size: "1024x512"}, map[ParameterKey]any{Size: "1024x512"}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			capability := compileFixedCapability(fixedMetadata(tc.settings, `{}`))
			if capability.compiled.Readiness != "ready" || capability.compiled.Size == nil || capability.compiled.Size.Mode != tc.mode {
				t.Fatalf("unsupported size mode: %+v", capability.compiled)
			}
			resolved, err := capability.compiled.Size.ResolveSize(tc.input)
			if err != nil || !reflect.DeepEqual(resolved.Values, tc.values) || resolved.Selection.Tier != tc.tier {
				t.Fatalf("resolved %+v: %v", resolved, err)
			}
			if tc.mode == RatioResolution && (resolved.Selection.Width != 0 || resolved.Selection.Height != 0) {
				t.Fatal("unknown dimensions were invented")
			}
			model := modelSnapshot{size: capability.compiled.Size, input: ModelInput{Parameters: capability.rules}}
			input, rules, err := linkedSubmission(SubmitInput{}, model)
			if err != nil {
				t.Fatal(err)
			}
			if validateParameters(rules, nil) != nil {
				t.Fatal("generated parameter rules are invalid")
			}
			if tc.name == "single resolution" && string(input.Resolution) != `"compact"` {
				t.Fatal("single resolution dropped from the request")
			}
			if tc.name == "ratio alone" && len(input.Resolution) != 0 {
				t.Fatal("missing resolution was synthesized")
			}
			if tc.mode == WidthHeight {
				for _, value := range []string{"255x512", "2049x512", "1025x512"} {
					if _, err := capability.compiled.Size.ResolveSize(SizeInput{Size: value}); err == nil {
						t.Fatal("axis boundary was relaxed")
					}
				}
			}
		})
	}
}

func TestFixedCapabilitiesDefaultsAndRequestLimits(t *testing.T) {
	metadata := fixedMetadata(`{"promptCharacterLimit":4,"steps":{"max":8,"default":4},"cfgScale":{"scale":[1,1.5,2],"default":1.5},"quality":true}`, `{"negative_prompts":true,"steps":true}`)
	capability := compileFixedCapability(metadata)
	if capability.compiled.Readiness != "ready" {
		t.Fatal("valid metadata rejected")
	}
	input := SubmitInput{ModelID: "imdl_AAAAAAAAAAAAAAAAAAAAAA", ExpectedModelRevision: "1", Prompt: "😀😀", Size: json.RawMessage(`"1024x1024"`)}
	params, n, err := normalizeSubmit(input, capability.rules, nil)
	if err != nil || n != 1 || params[Steps] != float64(4) || params[Guidance] != 1.5 || params[Quality] != "low" {
		t.Fatalf("default values %+v: %v", params, err)
	}
	body, err := buildRequest("atelier-test", params, fixedMapping())
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]any
	if json.Unmarshal(body, &wire) != nil || wire["async"] != true || wire["response_format"] != "b64_json" || wire["cfg_scale"] != 1.5 || wire["guidance"] != nil || wire["seed"] != nil {
		t.Fatalf("invalid fixed request: %s", body)
	}
	for _, tc := range []struct {
		name string
		edit func(*SubmitInput)
	}{
		{"UTF16 emoji limit", func(input *SubmitInput) { input.Prompt = "😀😀a" }},
		{"count limit", func(input *SubmitInput) { count := 5; input.N = &count }},
		{"negative seed", func(input *SubmitInput) { input.Seed = json.RawMessage("-1") }},
		{"seed upper limit", func(input *SubmitInput) { input.Seed = json.RawMessage("1000000000") }},
		{"fractional seed", func(input *SubmitInput) { input.Seed = json.RawMessage("0.5") }},
		{"steps upper limit", func(input *SubmitInput) { input.Steps = json.RawMessage("9") }},
		{"discrete guidance", func(input *SubmitInput) { input.Guidance = json.RawMessage("1.5000000000000002") }},
		{"quality enum", func(input *SubmitInput) { input.Quality = json.RawMessage(`"ultra"`) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			candidate := input
			tc.edit(&candidate)
			if _, _, err := normalizeSubmit(candidate, capability.rules, nil); err == nil {
				t.Fatal("unsupported parameter accepted")
			}
		})
	}
	input.Seed = json.RawMessage("0")
	if params, _, err = normalizeSubmit(input, capability.rules, nil); err != nil || params[Seed] != float64(0) {
		t.Fatal("zero seed must remain an explicit seed")
	}
}

func TestFixedMetadataRejectsUnknownAndAmbiguousSpecifications(t *testing.T) {
	for _, raw := range []json.RawMessage{
		json.RawMessage(`{}`),
		json.RawMessage(`{"type":"image"}`),
		json.RawMessage(`{"a_type":"image","a_settings":{}}`),
		json.RawMessage(`{"a_type":"image","a_settings":{},"a_supports":{},"b_type":"image","b_settings":{},"b_supports":{}}`),
		json.RawMessage(`{"a_type":"image","a_settings":{},"a_supports":{},"a_settings":{"quality":true}}`),
		fixedMetadata(`{"customSizeMapping":{}}`, `{}`),
		fixedMetadata(`{"customSizeMappingString":{"square":"512X512"}}`, `{}`),
		fixedMetadata(`{"aspectRatio":{"options":["1:1","1:1"]}}`, `{}`),
		fixedMetadata(`{"aspectRatio":{"options":["1:1"]},"resolution":{"options":"unknown"}}`, `{}`),
		fixedMetadata(`{"cfgScale":{"scale":[1,1.5],"default":"unknown"}}`, `{}`),
		fixedMetadata(`{"cfgScale":{"scale":[1,1]}}`, `{}`),
		fixedMetadata(`{"steps":{"max":8,"default":"unknown"}}`, `{"steps":true}`),
		fixedMetadata(`{"steps":{"max":-1}}`, `{"steps":true}`),
		fixedMetadata(`{"promptCharacterLimit":-1}`, `{}`),
		fixedMetadata(`{"quality":"unknown"}`, `{}`),
	} {
		capability := compileFixedCapability(raw)
		if capability.compiled.Readiness != "pending" || capability.compiled.Size != nil || len(fixedCapabilityIssues("model", capability.compiled)) == 0 {
			t.Fatalf("unsupported metadata accepted: %s", raw)
		}
		if validateParameters(capability.rules, nil) != nil {
			t.Fatal("unsupported metadata leaked invalid rules into pending configuration")
		}
	}
}

func TestFixedCapabilitiesNullableOptionalMetadataAndSafeDefaults(t *testing.T) {
	for _, settings := range []string{
		`{"customSizeMapping":null,"customSizeMappingString":null,"aspectRatio":null,"resolution":null,"cfgScale":null,"steps":null,"quality":null,"promptCharacterLimit":null}`,
		`{"aspectRatio":{"options":[]},"cfgScale":{"scale":[]},"steps":{"max":0},"promptCharacterLimit":0}`,
		`{"cfgScale":{"scale":null},"steps":{"max":null}}`,
	} {
		capability := compileFixedCapability(fixedMetadata(settings, `{"negative_prompts":null,"steps":true}`))
		if capability.compiled.Readiness != "ready" || capability.compiled.Size.Mode != WidthHeight {
			t.Fatalf("optional metadata incorrectly blocked a supported model: %s", settings)
		}
		for _, rule := range capability.rules {
			if rule.Key == Guidance || rule.Key == Steps || rule.Key == Quality || rule.Key == NegativePrompt {
				if rule.Supported {
					t.Fatal("nullable metadata enabled an unsupported parameter")
				}
			}
		}
	}
	capability := compileFixedCapability(fixedMetadata(`{"aspectRatio":{"options":["1:1"]},"resolution":{"options":[]},"steps":{"max":8,"default":99},"cfgScale":{"scale":[1,1.5],"default":99}}`, `{"steps":true}`))
	if capability.compiled.Readiness != "ready" || capability.compiled.Size.Mode != RatioResolution || capability.compiled.Size.Combinations[0].Resolution != "" {
		t.Fatal("empty resolution options invented a tier")
	}
	for _, rule := range capability.rules {
		if rule.Key == Steps && string(rule.Default) != "8" || rule.Key == Guidance && string(rule.Default) != "1" {
			t.Fatal("optional controls did not select a valid bounded default")
		}
	}
}

func TestFixedProtocolReceiptsAndPollStates(t *testing.T) {
	adapter := fixedAdapter("https://images.example.test/v1")
	if ValidateAdapter(adapter) != nil {
		t.Fatal("invalid built-in protocol")
	}
	for _, tc := range []struct{ body, state string }{
		{`{"data":[{"b64_json":"synthetic"}]}`, "succeeded"},
		{`{"status":"done","data":[{"b64_json":"synthetic"}]}`, "succeeded"},
		{`{"async":true,"task_id":"opaque","status":"queued"}`, "running"},
		{`{"async":true,"task_id":"opaque","status":"running"}`, "running"},
		{`{"async":true,"task_id":"opaque"}`, "running"},
		{`{"status":"error","error":{"message":"private upstream text"}}`, "failed"},
		{`{"async":true,"task_id":"opaque","status":"done"}`, ""},
		{`{"async":true,"task_id":"opaque","data":[{}]}`, ""},
		{`{"async":true}`, ""},
		{`{"task_id":"opaque","status":"queued"}`, ""},
		{`{"status":"unexpected","data":[{}]}`, ""},
	} {
		state, err := submissionState([]byte(tc.body), adapter)
		if tc.state == "" && err == nil || tc.state != "" && (err != nil || state != tc.state) {
			t.Fatalf("receipt %s => %s %v", tc.body, state, err)
		}
	}
	for _, tc := range []struct{ body, state string }{
		{`{"status":"queued"}`, "running"}, {`{"status":"running"}`, "running"},
		{`{"status":"done"}`, "succeeded"}, {`{"status":"error"}`, "failed"}, {`{"status":"unexpected"}`, ""},
	} {
		state, err := responseState([]byte(tc.body), adapter.Response)
		if tc.state == "" && err == nil || tc.state != "" && (err != nil || state != tc.state) {
			t.Fatalf("poll %s => %s %v", tc.body, state, err)
		}
	}
	for _, base := range []string{"https://images.example.test", "https://images.example.test/", "https://images.example.test/v1", "https://images.example.test/v1/"} {
		adapter := fixedAdapter(base)
		target, err := requestURL(base, adapter.Discovery.Path, "")
		if err != nil || target != "https://images.example.test/v1/models" {
			t.Fatalf("unexpected endpoint %s => %s %v", base, target, err)
		}
	}
}
