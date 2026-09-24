package imageactivity

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDeclaredParametersPreserveExactStepsAndCombinations(t *testing.T) {
	rules := []ParameterRule{
		{Key: Prompt, Supported: true, Required: true, Type: "string", MaxLength: ptr(4), LengthUnit: "utf16_units"},
		{Key: N, Supported: true, Type: "integer", Minimum: ptr(float64(1)), Maximum: ptr(float64(16))},
		{Key: NegativePrompt, Supported: true, Type: "string", LengthUnit: "utf8_bytes"},
		{Key: Guidance, Supported: true, Type: "number", Minimum: ptr(float64(0)), Maximum: ptr(float64(20)), Step: ptr(0.1)},
		{Key: Seed, Supported: true, Type: "integer"},
		{Key: Quality, Supported: true, Type: "string", LengthUnit: "unicode_scalars", Enum: []json.RawMessage{json.RawMessage(`"draft"`), json.RawMessage(`"high"`)}, Default: json.RawMessage(`"draft"`)},
		{Key: Size, Supported: true, Type: "string", LengthUnit: "utf8_bytes", Enum: []json.RawMessage{json.RawMessage(`"small"`), json.RawMessage(`"large"`)}, Default: json.RawMessage(`"small"`)},
	}
	combinations := []CombinationRule{{Keys: []ParameterKey{Quality, Size}, Allowed: [][]json.RawMessage{{json.RawMessage(`"draft"`), json.RawMessage(`"small"`)}, {json.RawMessage(`"high"`), json.RawMessage(`"large"`)}}}}
	if err := validateParameters(rules, combinations); err != nil {
		t.Fatal(err)
	}
	for _, bounds := range [][2]float64{{0, 16}, {1, 100}, {1.5, 16}, {17, 17}} {
		invalid := append([]ParameterRule(nil), rules...)
		invalid[1].Minimum, invalid[1].Maximum = ptr(bounds[0]), ptr(bounds[1])
		if err := validateParameters(invalid, combinations); err == nil {
			t.Fatalf("unusable image count declaration accepted: %v", bounds)
		}
	}
	id, _ := newID("imdl_")
	input := SubmitInput{ModelID: id, ExpectedModelRevision: "1", Prompt: "😀😀", Guidance: json.RawMessage("0.3"), Seed: json.RawMessage("9007199254740991")}
	values, n, err := normalizeSubmit(input, rules, combinations)
	if err != nil || n != 1 || values[Quality] != "draft" {
		t.Fatalf("valid settings %v", err)
	}
	for _, mutate := range []func(*SubmitInput){
		func(v *SubmitInput) { v.Prompt += "x" },
		func(v *SubmitInput) { v.Guidance = json.RawMessage("0.30000000000000004") },
		func(v *SubmitInput) { v.Seed = json.RawMessage("9007199254740992") },
		func(v *SubmitInput) { v.Quality = json.RawMessage(`"high"`) },
		func(v *SubmitInput) { v.Resolution = json.RawMessage(`"unsupported"`) },
		func(v *SubmitInput) { v.N = ptr(17) },
		func(v *SubmitInput) { v.NegativePrompt, _ = json.Marshal(strings.Repeat("a", maxPrompt)) },
	} {
		invalid := input
		mutate(&invalid)
		if _, _, err = normalizeSubmit(invalid, rules, combinations); err == nil {
			t.Fatal("invalid parameter accepted")
		}
	}
	if _, err = parsePayment(Price{"0", "0"}, 1); err == nil {
		t.Fatal("free task allowed")
	}
	if _, err = parsePayment(Price{"1.5", "1"}, 1); err == nil {
		t.Fatal("fractional activity price")
	}
	if _, err = parsePayment(Price{"170141183460469231731687303715884105727", "1"}, 16); err == nil {
		t.Fatal("activity price overflow")
	}
}
func TestMappingRejectsCollisionsAndRequestPathInjection(t *testing.T) {
	mapping := Mapping{ModelPointer: "/model", Parameters: map[ParameterKey]string{Prompt: "/payload/prompt", N: "/n"}, Constants: []Constant{{Pointer: "/response_format", Value: json.RawMessage(`"base64"`)}}}
	if err := validateMapping(mapping, false); err != nil {
		t.Fatal(err)
	}
	request, err := buildRequest("opaque-model", map[ParameterKey]any{Prompt: "literal", N: float64(2)}, mapping)
	if err != nil || !json.Valid(request) {
		t.Fatalf("request %v", err)
	}
	mapping.Constants = append(mapping.Constants, Constant{Pointer: "/payload", Value: json.RawMessage("false")})
	if err = validateMapping(mapping, false); err == nil {
		t.Fatal("parent field collision allowed")
	}
	mapping.Constants = nil
	mapping.Parameters[Size] = "/model"
	if err = validateMapping(mapping, false); err == nil {
		t.Fatal("model collision allowed")
	}
}
