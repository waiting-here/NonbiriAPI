package imageactivity

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func dimensionTestRules() []ParameterRule {
	return []ParameterRule{
		{Key: Prompt, Supported: true, Required: true, Type: "string", LengthUnit: "utf8_bytes"},
		{Key: N, Supported: true, Type: "integer"},
		{Key: Size, Supported: true, Type: "string", LengthUnit: "utf8_bytes",
			Dimensions: &DimensionRule{Format: "width_height", Width: DimensionRange{7, 37, 5}, Height: DimensionRange{11, 41, 6}}},
	}
}

func TestIndependentDimensionsEnforceCanonicalValuesAndAnchoredSteps(t *testing.T) {
	rules := dimensionTestRules()
	if err := validateParameters(rules, nil); err != nil {
		t.Fatal(err)
	}
	id, _ := newID("imdl_")
	input := SubmitInput{ModelID: id, ExpectedModelRevision: "1", Prompt: "bounded"}
	for _, value := range []string{"7x11", "37x41", "12x17", "32x35"} {
		input.Size, _ = json.Marshal(value)
		values, _, err := normalizeSubmit(input, rules, nil)
		if err != nil || values[Size] != value {
			t.Fatalf("valid independent dimensions %q: %v", value, err)
		}
	}
	for _, value := range []string{
		"6x11", "38x11", "7x10", "7x42", "8x11", "7x12", "10x12", "11x7",
		"07x11", "7x011", "+7x11", "7x+11", "-7x11", "7x-11", "0x11", "7x0",
		"7X11", "7×11", "7 x 11", " 7x11", "7x11 ", "7.0x11", "7e0x11", "７x11",
		"7x11x1", "7xx11", "x11", "7x", "", "65537x11", strings.Repeat("9", 200) + "x11",
	} {
		input.Size, _ = json.Marshal(value)
		if _, _, err := normalizeSubmit(input, rules, nil); !errors.Is(err, ErrInvalid) {
			t.Fatalf("invalid dimensions %q accepted: %v", value, err)
		}
	}
	input.Size = json.RawMessage("7")
	if _, _, err := normalizeSubmit(input, rules, nil); err == nil {
		t.Fatal("numeric public size bypassed dimension validation")
	}
	input.Size = nil
	values, _, err := normalizeSubmit(input, rules, nil)
	if err != nil || values[Size] != nil {
		t.Fatalf("optional blank dimensions gained a value: %v", err)
	}
	rules[2].Default = json.RawMessage(`"12x17"`)
	values, _, err = normalizeSubmit(input, rules, nil)
	if err != nil || values[Size] != "12x17" {
		t.Fatalf("dimension default %v", err)
	}
}

func TestDimensionConfigurationRejectsInvalidRulesAndDependentValues(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*ParameterRule)
	}{
		{"wrong format", func(r *ParameterRule) { r.Dimensions.Format = "expression" }},
		{"wrong key", func(r *ParameterRule) { r.Key = Quality }},
		{"numeric type", func(r *ParameterRule) { r.Type = "integer"; r.LengthUnit = "" }},
		{"unsupported", func(r *ParameterRule) { r.Supported = false }},
		{"zero minimum", func(r *ParameterRule) { r.Dimensions.Width.Minimum = 0 }},
		{"inverted range", func(r *ParameterRule) { r.Dimensions.Width.Minimum = 38 }},
		{"oversized maximum", func(r *ParameterRule) { r.Dimensions.Height.Maximum = maxDimension + 1 }},
		{"missing axis", func(r *ParameterRule) { r.Dimensions.Height = DimensionRange{} }},
		{"zero step", func(r *ParameterRule) { r.Dimensions.Width.Step = 0 }},
		{"negative step", func(r *ParameterRule) { r.Dimensions.Height.Step = -1 }},
		{"oversized step", func(r *ParameterRule) { r.Dimensions.Height.Step = maxDimension + 1 }},
		{"invalid default", func(r *ParameterRule) { r.Default = json.RawMessage(`"8x11"`) }},
		{"invalid enum", func(r *ParameterRule) { r.Enum = []json.RawMessage{json.RawMessage(`"7x12"`)} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rules := dimensionTestRules()
			tc.edit(&rules[2])
			if err := validateParameters(rules, nil); err == nil {
				t.Fatal("invalid dimension declaration accepted")
			}
		})
	}
	rules := dimensionTestRules()
	rules[2].Dimensions.Width = DimensionRange{1, maxDimension, maxDimension}
	rules[2].Dimensions.Height = DimensionRange{maxDimension, maxDimension, 1}
	rules[2].Default = json.RawMessage(`"1x65536"`)
	if err := validateParameters(rules, nil); err != nil {
		t.Fatalf("inclusive hard bound rejected: %v", err)
	}
	rules = dimensionTestRules()
	rules[2].Enum = []json.RawMessage{json.RawMessage(`"7x11"`), json.RawMessage(`"12x17"`)}
	rules[2].Default = json.RawMessage(`"7x11"`)
	combinations := []CombinationRule{{Keys: []ParameterKey{Size}, Allowed: [][]json.RawMessage{{json.RawMessage(`"7x11"`)}}}}
	if err := validateParameters(rules, combinations); err != nil {
		t.Fatal(err)
	}
	id, _ := newID("imdl_")
	input := SubmitInput{ModelID: id, ExpectedModelRevision: "1", Prompt: "bounded", Size: json.RawMessage(`"12x17"`)}
	if _, _, err := normalizeSubmit(input, rules, combinations); err == nil {
		t.Fatal("dimension-valid value bypassed combination restrictions")
	}
	input.Size = json.RawMessage(`"17x23"`)
	if _, _, err := normalizeSubmit(input, rules, nil); err == nil {
		t.Fatal("dimension-valid value bypassed enum restrictions")
	}
	combinations[0].Allowed[0][0] = json.RawMessage(`"8x11"`)
	if err := validateParameters(rules, combinations); err == nil {
		t.Fatal("invalid dimensions in declared combination accepted")
	}
	var rule ParameterRule
	if err := json.Unmarshal([]byte(`{"key":"size","dimensions":{"format":"width_height","width":{"minimum":1.5,"maximum":10,"step":1},"height":{"minimum":1,"maximum":10,"step":1}}}`), &rule); err == nil {
		t.Fatal("fractional dimension bound decoded as an integer")
	}
}

func TestDimensionMetadataAndServerAdmissionUseStoredRules(t *testing.T) {
	f := newFixture(t)
	f.configure(t)
	input := ModelInput{ExpectedRevision: "1", DisplayName: "Canvas", Description: "Independent dimensions", Enabled: true,
		Price: Price{"2", "1"}, Parameters: dimensionTestRules(), Combinations: []CombinationRule{},
		Mapping: Mapping{ModelPointer: "/engine", Parameters: map[ParameterKey]string{Prompt: "/text", N: "/count", Size: "/canvas"}, Constants: []Constant{}}}
	input.Parameters[2].Required = true
	if _, err := f.service.PutModel(f.ctx(f.admin), f.admin, f.model, f.key(), input); err != nil {
		t.Fatal(err)
	}
	models, err := f.service.UserModels(f.ctx(f.user), f.user, 20, "")
	if err != nil || len(models.Data) != 1 {
		t.Fatalf("public model %v", err)
	}
	rule := models.Data[0].Parameters[2]
	if rule.Dimensions == nil || *rule.Dimensions != *input.Parameters[2].Dimensions {
		t.Fatalf("public dimensions %+v", rule.Dimensions)
	}
	raw, _ := json.Marshal(models)
	for _, private := range []string{"upstream_model_id", "mapping", "/canvas", "indicator_pointer", "studio-test", "base_url"} {
		if strings.Contains(string(raw), private) {
			t.Fatalf("public schema leaked %q", private)
		}
	}
	request := SubmitInput{ModelID: f.model, ExpectedModelRevision: "2", Prompt: "bounded", Size: json.RawMessage(`"8x11"`)}
	if _, err := f.service.Submit(f.ctx(f.user), f.user, f.key(), request); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid dimension reached admission: %v", err)
	}
	var tasks int
	if err := f.database.QueryRow("SELECT count(*) FROM image_activity_tasks").Scan(&tasks); err != nil || tasks != 0 {
		t.Fatalf("invalid dimension charged or queued %d %v", tasks, err)
	}
	wallet, err := f.limited.Wallet(f.ctx(f.user), f.user)
	if err != nil || wallet.Paper != "100" || wallet.Brush != "100" {
		t.Fatalf("invalid dimension changed wallet %+v %v", wallet, err)
	}
	request.Size = json.RawMessage(`"12x17"`)
	accepted, err := f.service.Submit(f.ctx(f.user), f.user, f.key(), request)
	if err != nil {
		t.Fatal(err)
	}
	f.wait(t, func() bool {
		row, err := f.service.readTask(context.Background(), accepted.Value.Task.ID)
		return err == nil && row.state == "succeeded"
	})
	f.upstream.mu.Lock()
	value := f.upstream.submitted[0]["canvas"]
	f.upstream.mu.Unlock()
	if value != "12x17" || f.upstream.posts.Load() != 1 {
		t.Fatalf("canonical size not mapped intact: %v", value)
	}
	f.checkLedger(t)
}
