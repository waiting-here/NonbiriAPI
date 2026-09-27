package imageactivity

import (
	"bytes"
	"encoding/json"
	"errors"
	"math/big"
	"reflect"
	"sort"
	"strconv"
)

type CapabilitySupport string

const (
	CapabilitySupported   CapabilitySupport = "supported"
	CapabilityUnsupported CapabilitySupport = "unsupported"
	CapabilityUnknown     CapabilitySupport = "unknown"
)

type ProfileField struct {
	Rule           ParameterRule   `json:"rule"`
	SupportPointer string          `json:"support_pointer,omitempty"`
	SupportEquals  json.RawMessage `json:"support_equals,omitempty"`
	EnumPointer    string          `json:"enum_pointer,omitempty"`
	DefaultPointer string          `json:"default_pointer,omitempty"`
	MinimumPointer string          `json:"minimum_pointer,omitempty"`
	MaximumPointer string          `json:"maximum_pointer,omitempty"`
	StepPointer    string          `json:"step_pointer,omitempty"`
}

type ProfileSize struct {
	Capability          SizeCapability `json:"capability"`
	CombinationsPointer string         `json:"combinations_pointer,omitempty"`
	AutoPointer         string         `json:"auto_pointer,omitempty"`
}

// CapabilityProfile is a bounded declarative extractor. There is no script,
// expression evaluator, regex, or network fetch in the profile format.
type CapabilityProfile struct {
	Version            int            `json:"version"`
	Fields             []ProfileField `json:"fields"`
	Size               *ProfileSize   `json:"size,omitempty"`
	CatalogTypePointer string         `json:"catalog_type_pointer,omitempty"`
	ImageValues        []string       `json:"image_values,omitempty"`
}

type CapabilityField struct {
	Rule    ParameterRule     `json:"rule"`
	Support CapabilitySupport `json:"support"`
	Source  string            `json:"source"`
}

type ManualCapability struct {
	Rule    ParameterRule     `json:"rule"`
	Support CapabilitySupport `json:"support"`
}

type CompiledCapability struct {
	SchemaVersion int               `json:"schema_version"`
	Readiness     string            `json:"readiness"`
	Parameters    []CapabilityField `json:"parameters"`
	Size          *SizeCapability   `json:"size,omitempty"`
	Problems      []ParameterKey    `json:"problems,omitempty"`
	CatalogType   string            `json:"catalog_type"`
}

func profilePointers(f ProfileField) []string {
	return []string{f.SupportPointer, f.EnumPointer, f.DefaultPointer, f.MinimumPointer, f.MaximumPointer, f.StepPointer}
}

func ValidateCapabilityProfile(p CapabilityProfile) error {
	encoded, err := json.Marshal(p)
	if err != nil || len(encoded) > 262144 || p.Version != 1 || len(p.Fields) == 0 || len(p.Fields) > 10 {
		return ErrInvalid
	}
	seen := map[ParameterKey]bool{}
	extractions := 0
	for _, f := range p.Fields {
		if !keyValid(f.Rule.Key) || seen[f.Rule.Key] || f.Rule.Type != "string" && f.Rule.Type != "integer" && f.Rule.Type != "number" {
			return ErrInvalid
		}
		seen[f.Rule.Key] = true
		if f.SupportPointer == "" && len(f.SupportEquals) != 0 {
			return ErrInvalid
		}
		if len(f.SupportEquals) > 0 {
			if _, err := scalar(f.SupportEquals); err != nil {
				return ErrInvalid
			}
		}
		for _, pointer := range profilePointers(f) {
			if pointer == "" {
				continue
			}
			parts, err := pointerParts(pointer, false)
			if err != nil || len(parts) > 16 {
				return ErrInvalid
			}
			extractions++
		}
		if f.Rule.Type == "string" && (f.MinimumPointer != "" || f.MaximumPointer != "" || f.StepPointer != "") || f.Rule.Type != "string" && f.EnumPointer != "" && f.Rule.Dimensions != nil {
			return ErrInvalid
		}
		probe := f.Rule
		if f.SupportPointer != "" {
			probe.Supported = true
		}
		prompt := ParameterRule{Key: Prompt, Supported: true, Required: true, Type: "string", LengthUnit: "utf8_bytes"}
		count := ParameterRule{Key: N, Supported: true, Type: "integer"}
		rules := []ParameterRule{prompt, count}
		switch probe.Key {
		case Prompt:
			probe.Supported, probe.Required = true, true
			rules[0] = probe
		case N:
			probe.Supported = true
			rules[1] = probe
		default:
			rules = append(rules, probe)
		}
		if validateParameters(rules, nil) != nil {
			return ErrInvalid
		}
	}
	if p.Size != nil {
		if p.Size.Capability.Mode == "" || p.Size.Capability.Validate() != nil && p.Size.CombinationsPointer == "" {
			return ErrInvalid
		}
		for _, pointer := range []string{p.Size.CombinationsPointer, p.Size.AutoPointer} {
			if pointer != "" {
				parts, err := pointerParts(pointer, false)
				if err != nil || len(parts) > 16 {
					return ErrInvalid
				}
				extractions++
			}
		}
	}
	if p.CatalogTypePointer != "" {
		parts, err := pointerParts(p.CatalogTypePointer, false)
		if err != nil || len(parts) > 16 || len(p.ImageValues) == 0 || len(p.ImageValues) > 128 {
			return ErrInvalid
		}
		seenValues := map[string]bool{}
		for _, value := range p.ImageValues {
			if !validSizeLabel(value) || seenValues[value] {
				return ErrInvalid
			}
			seenValues[value] = true
		}
		extractions++
	} else if len(p.ImageValues) > 0 {
		return ErrInvalid
	}
	if extractions > 64 {
		return ErrInvalid
	}
	return nil
}

// ParseCapabilityProfile rejects duplicate keys and unknown fields, including
// nested fields. Private profile documents must be imported explicitly by an
// administrator; catalog metadata is a separate, less trusted input.
func ParseCapabilityProfile(raw []byte) (CapabilityProfile, error) {
	var p CapabilityProfile
	if len(raw) == 0 || len(raw) > 262144 || !json.Valid(raw) {
		return p, ErrInvalid
	}
	end, err := valueEnd(raw, 0, 0)
	if err != nil || len(bytes.TrimSpace(raw[end:])) != 0 || jsonDepth(raw) > 16 {
		return p, ErrInvalid
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&p); err != nil || ValidateCapabilityProfile(p) != nil {
		return CapabilityProfile{}, ErrInvalid
	}
	return p, nil
}

func jsonDepth(raw []byte) int {
	depth, maximum := 0, 0
	inString, escaped := false, false
	for _, b := range raw {
		if inString {
			if escaped {
				escaped = false
			} else if b == '\\' {
				escaped = true
			} else if b == '"' {
				inString = false
			}
			continue
		}
		switch b {
		case '"':
			inString = true
		case '{', '[':
			depth++
			if depth > maximum {
				maximum = depth
			}
		case '}', ']':
			depth--
		}
	}
	return maximum
}

func metadataValue(metadata []byte, pointer string) (json.RawMessage, error) {
	if pointer == "" {
		return nil, ErrNotFound
	}
	if len(metadata) == 0 || len(metadata) > 32768 || !json.Valid(metadata) || jsonDepth(metadata) > 16 {
		return nil, ErrInvalid
	}
	end, err := valueEnd(metadata, 0, 0)
	if err != nil || len(bytes.TrimSpace(metadata[end:])) != 0 {
		return nil, ErrInvalid
	}
	value, err := rawPointer(metadata, pointer, false)
	if err != nil {
		return nil, err
	}
	return append(json.RawMessage(nil), value...), nil
}

func extractNumber(metadata []byte, pointer string) (*float64, error) {
	raw, err := metadataValue(metadata, pointer)
	if err != nil {
		return nil, err
	}
	v, err := scalar(raw)
	if err != nil {
		return nil, err
	}
	n, ok := v.(float64)
	if !ok {
		return nil, ErrInvalid
	}
	return &n, nil
}

func extractRule(metadata []byte, f ProfileField) (CapabilityField, error) {
	out := CapabilityField{Rule: f.Rule, Support: CapabilityUnknown, Source: "profile"}
	for _, pointer := range profilePointers(f) {
		if pointer != "" {
			out.Source = "discovered"
			break
		}
	}
	if f.SupportPointer == "" {
		if f.Rule.Supported {
			out.Support = CapabilitySupported
		} else {
			out.Support = CapabilityUnsupported
		}
	} else {
		raw, err := metadataValue(metadata, f.SupportPointer)
		if err != nil {
			return out, err
		}
		if len(f.SupportEquals) != 0 {
			actual, err := scalar(raw)
			if err != nil {
				return out, err
			}
			want, _ := scalar(f.SupportEquals)
			if scalarEqual(actual, want) {
				out.Support = CapabilitySupported
			} else {
				out.Support = CapabilityUnsupported
			}
		} else {
			var supported bool
			if json.Unmarshal(raw, &supported) != nil || !bytes.Equal(bytes.TrimSpace(raw), []byte("true")) && !bytes.Equal(bytes.TrimSpace(raw), []byte("false")) {
				return out, ErrInvalid
			}
			if supported {
				out.Support = CapabilitySupported
			} else {
				out.Support = CapabilityUnsupported
			}
		}
	}
	if out.Support != CapabilitySupported {
		out.Rule.Supported = false
		out.Rule.Required = false
		return out, nil
	}
	out.Rule.Supported = true
	for _, item := range []struct {
		pointer string
		set     func(*float64)
	}{
		{f.MinimumPointer, func(n *float64) { out.Rule.Minimum = n }},
		{f.MaximumPointer, func(n *float64) { out.Rule.Maximum = n }},
		{f.StepPointer, func(n *float64) { out.Rule.Step = n }},
	} {
		if item.pointer == "" {
			continue
		}
		n, err := extractNumber(metadata, item.pointer)
		if err != nil {
			return out, err
		}
		item.set(n)
	}
	if f.DefaultPointer != "" {
		raw, err := metadataValue(metadata, f.DefaultPointer)
		if err != nil {
			return out, err
		}
		if _, err := scalar(raw); err != nil {
			return out, err
		}
		out.Rule.Default = raw
	}
	if f.EnumPointer != "" {
		raw, err := metadataValue(metadata, f.EnumPointer)
		if err != nil {
			return out, err
		}
		choices, err := rawArray(raw, 128)
		if err != nil || len(choices) == 0 {
			return out, ErrInvalid
		}
		out.Rule.Enum = make([]json.RawMessage, len(choices))
		for i, choice := range choices {
			if _, err := scalar(choice); err != nil {
				return out, err
			}
			out.Rule.Enum[i] = append(json.RawMessage(nil), choice...)
		}
	}
	return out, nil
}

func narrowerThanSource(source, manual ParameterRule) bool {
	if source.Type != manual.Type || !source.Supported || !manual.Supported || source.Required && !manual.Required || source.LengthUnit != manual.LengthUnit {
		return false
	}
	if source.Minimum != nil && (manual.Minimum == nil || *manual.Minimum < *source.Minimum) || source.Maximum != nil && (manual.Maximum == nil || *manual.Maximum > *source.Maximum) {
		return false
	}
	if source.MinLength != nil && (manual.MinLength == nil || *manual.MinLength < *source.MinLength) || source.MaxLength != nil && (manual.MaxLength == nil || *manual.MaxLength > *source.MaxLength) {
		return false
	}
	if source.Step != nil {
		if manual.Step == nil || !integralDecimalRatio(*manual.Step, *source.Step) {
			return false
		}
		base := float64(0)
		if source.Minimum != nil {
			base = *source.Minimum
		}
		manualBase := float64(0)
		if manual.Minimum != nil {
			manualBase = *manual.Minimum
		}
		if !integralDecimalRatio(manualBase-base, *source.Step) {
			return false
		}
	}
	if len(manual.Default) > 0 {
		v, err := scalar(manual.Default)
		if err != nil || ruleValue(source, v, true) != nil {
			return false
		}
	}
	if len(source.Enum) > 0 {
		if len(manual.Enum) == 0 {
			return false
		}
		for _, value := range manual.Enum {
			v, err := scalar(value)
			if err != nil || ruleValue(source, v, true) != nil {
				return false
			}
		}
	}
	return source.Dimensions == nil || reflect.DeepEqual(source.Dimensions, manual.Dimensions)
}

func integralDecimalRatio(numerator, denominator float64) bool {
	if denominator <= 0 || !finite(numerator) || !finite(denominator) {
		return false
	}
	toRat := func(n float64) *big.Rat {
		v, _ := new(big.Rat).SetString(strconv.FormatFloat(n, 'f', -1, 64))
		return v
	}
	return new(big.Rat).Quo(toRat(numerator), toRat(denominator)).IsInt()
}

// CompileCapability compiles safe source facts and manual constraints without
// ever making a parse failure look like an unrestricted model. A ready result
// can be projected to the legacy ParameterRule submission validator.
func CompileCapability(p CapabilityProfile, metadata []byte, manual []ManualCapability) (CompiledCapability, error) {
	out := CompiledCapability{SchemaVersion: 2, Readiness: "pending", Parameters: []CapabilityField{}, Problems: []ParameterKey{}, CatalogType: "unknown"}
	if err := ValidateCapabilityProfile(p); err != nil {
		return out, err
	}
	if p.CatalogTypePointer != "" {
		raw, err := metadataValue(metadata, p.CatalogTypePointer)
		if err == nil {
			if value, parseErr := scalar(raw); parseErr == nil {
				if label, ok := value.(string); ok {
					out.CatalogType = "other"
					for _, imageValue := range p.ImageValues {
						if label == imageValue {
							out.CatalogType = "image"
							break
						}
					}
				}
			}
		}
	}
	overrides := map[ParameterKey]ManualCapability{}
	for _, m := range manual {
		if !keyValid(m.Rule.Key) || m.Support != CapabilitySupported && m.Support != CapabilityUnsupported || m.Rule.Supported != (m.Support == CapabilitySupported) {
			return out, ErrInvalid
		}
		if _, duplicate := overrides[m.Rule.Key]; duplicate {
			return out, ErrInvalid
		}
		overrides[m.Rule.Key] = m
	}
	allReady := true
	for _, f := range p.Fields {
		field, err := extractRule(metadata, f)
		if err != nil {
			if !errors.Is(err, ErrNotFound) && !errors.Is(err, ErrInvalid) {
				return out, err
			}
			field = CapabilityField{Rule: f.Rule, Support: CapabilityUnknown, Source: "discovered"}
			field.Rule.Supported, field.Rule.Required = false, false
		}
		if m, ok := overrides[f.Rule.Key]; ok {
			if field.Support == CapabilityUnsupported && m.Support == CapabilitySupported || field.Support == CapabilitySupported && m.Support == CapabilitySupported && !narrowerThanSource(field.Rule, m.Rule) {
				return out, ErrConflict
			}
			field = CapabilityField{Rule: m.Rule, Support: m.Support, Source: "manual"}
			delete(overrides, f.Rule.Key)
		}
		if field.Support == CapabilityUnknown {
			allReady = false
			out.Problems = append(out.Problems, f.Rule.Key)
		}
		out.Parameters = append(out.Parameters, field)
	}
	for _, override := range overrides {
		out.Parameters = append(out.Parameters, CapabilityField{Rule: override.Rule, Support: override.Support, Source: "manual"})
	}
	if p.Size != nil {
		size := p.Size.Capability
		if p.Size.CombinationsPointer != "" {
			raw, err := metadataValue(metadata, p.Size.CombinationsPointer)
			if err != nil || json.Unmarshal(raw, &size.Combinations) != nil {
				allReady = false
			}
		}
		if p.Size.AutoPointer != "" {
			raw, err := metadataValue(metadata, p.Size.AutoPointer)
			if err != nil || json.Unmarshal(raw, &size.Auto) != nil {
				allReady = false
			}
		}
		if size.Validate() != nil {
			allReady = false
		} else {
			out.Size = &size
		}
	}
	sort.Slice(out.Parameters, func(i, j int) bool { return out.Parameters[i].Rule.Key < out.Parameters[j].Rule.Key })
	if !allReady {
		return out, nil
	}
	rules := make([]ParameterRule, 0, len(out.Parameters))
	for _, field := range out.Parameters {
		rules = append(rules, field.Rule)
	}
	if validateParameters(rules, nil) != nil {
		return out, nil
	}
	out.Readiness = "ready"
	out.Problems = nil
	return out, nil
}
