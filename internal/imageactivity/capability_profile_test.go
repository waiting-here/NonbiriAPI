package imageactivity

import (
	"encoding/json"
	"errors"
	"testing"
)

func exampleCapabilityProfile() CapabilityProfile {
	return CapabilityProfile{Version: 1, Fields: []ProfileField{
		{Rule: ParameterRule{Key: Prompt, Supported: true, Required: true, Type: "string", LengthUnit: "unicode_scalars"}},
		{Rule: ParameterRule{Key: N, Supported: true, Type: "integer", Minimum: numberPointer(1), Maximum: numberPointer(4), Default: json.RawMessage(`1`)}},
		{Rule: ParameterRule{Key: Guidance, Type: "number"}, SupportPointer: "/support/guidance", MinimumPointer: "/guidance/min", MaximumPointer: "/guidance/max", StepPointer: "/guidance/step", DefaultPointer: "/guidance/default"},
	}}
}

func numberPointer(n float64) *float64 { return &n }

func TestCapabilityProfileExtractionAndManualConflict(t *testing.T) {
	p := exampleCapabilityProfile()
	metadata := []byte(`{"support":{"guidance":true},"guidance":{"min":0,"max":2,"step":0.25,"default":0.5}}`)
	compiled, err := CompileCapability(p, metadata, nil)
	if err != nil || compiled.Readiness != "ready" || len(compiled.Parameters) != 3 {
		t.Fatalf("valid source capability did not become ready: %+v %v", compiled, err)
	}
	var guidance ParameterRule
	for _, field := range compiled.Parameters {
		if field.Rule.Key == Guidance {
			guidance = field.Rule
			if field.Source != "discovered" || field.Support != CapabilitySupported {
				t.Fatalf("discovered provenance lost: %+v", field)
			}
		}
	}
	if guidance.Step == nil || *guidance.Step != 0.25 || string(guidance.Default) != "0.5" {
		t.Fatalf("numeric extraction changed precision: %+v", guidance)
	}
	missing, err := CompileCapability(p, []byte(`{"support":{}}`), nil)
	if err != nil || missing.Readiness != "pending" || len(missing.Problems) != 1 || missing.Problems[0] != Guidance {
		t.Fatalf("missing discovery field should remain pending: %+v %v", missing, err)
	}
	manual := []ManualCapability{{Rule: ParameterRule{Key: Guidance, Supported: true, Type: "number", Minimum: numberPointer(0), Maximum: numberPointer(1), Step: numberPointer(0.25)}, Support: CapabilitySupported}}
	resolved, err := CompileCapability(p, []byte(`{"support":{}}`), manual)
	if err != nil || resolved.Readiness != "ready" {
		t.Fatalf("valid manual completion should make model ready: %+v %v", resolved, err)
	}
	manual[0].Rule.Maximum = numberPointer(3)
	if _, err := CompileCapability(p, metadata, manual); !errors.Is(err, ErrConflict) {
		t.Fatalf("manual range cannot widen discovered source: %v", err)
	}
	if _, err := CompileCapability(p, []byte(`{"support":{"guidance":false}}`), manual); !errors.Is(err, ErrConflict) {
		t.Fatalf("manual override cannot enable explicitly unsupported field: %v", err)
	}
}

func TestMetadataExtractionWithoutSupportPointerIsDiscovered(t *testing.T) {
	profile := CapabilityProfile{Version: 1, Fields: []ProfileField{
		{Rule: ParameterRule{Key: Prompt, Supported: true, Required: true, Type: "string", LengthUnit: "utf8_bytes"}},
		{Rule: ParameterRule{Key: N, Supported: true, Type: "integer", Minimum: numberPointer(1), Maximum: numberPointer(1)}},
		{Rule: ParameterRule{Key: Quality, Supported: true, Type: "string", LengthUnit: "utf8_bytes"}, EnumPointer: "/quality"},
	}}
	compiled, err := CompileCapability(profile, []byte(`{"quality":["draft","high"]}`), nil)
	if err != nil || compiled.Readiness != "ready" {
		t.Fatalf("enum-only metadata extraction: %+v %v", compiled, err)
	}
	for _, field := range compiled.Parameters {
		if field.Rule.Key == Quality {
			if field.Source != "discovered" || len(field.Rule.Enum) != 2 {
				t.Fatalf("metadata-derived enum mislabeled as profile: %+v", field)
			}
			return
		}
	}
	t.Fatal("quality field missing")
}

func TestCapabilityProfileRejectsExecutableOrAmbiguousInput(t *testing.T) {
	for _, raw := range [][]byte{
		[]byte(`{"version":1,"version":1,"fields":[]}`),
		[]byte(`{"version":1,"fields":[],"script":"eval(1)"}`),
		[]byte(`{"version":1,"fields":[{"rule":{"key":"prompt","supported":true,"required":true,"type":"string"},"support_pointer":"/bad~2path"}]}`),
	} {
		if _, err := ParseCapabilityProfile(raw); err == nil {
			t.Fatalf("ambiguous or executable profile accepted: %s", raw)
		}
	}
	p := exampleCapabilityProfile()
	compiled, err := CompileCapability(p, []byte(`{"support":{"guidance":true},"guidance":{"min":0,"max":2,"step":"oops","default":0.5}}`), nil)
	if err != nil || compiled.Readiness != "pending" {
		t.Fatalf("malformed discovery must fail closed to pending: %+v %v", compiled, err)
	}
	p = exampleCapabilityProfile()
	p.Fields[2].Rule.Step = numberPointer(-1)
	if err := ValidateCapabilityProfile(p); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid static rule was accepted: %v", err)
	}
	p.Fields[2].Rule.Step = nil
	p.Fields[2].Rule.Required = true
	if err := ValidateCapabilityProfile(p); err != nil {
		t.Fatalf("dynamic support must retain its required constraint: %v", err)
	}
}
