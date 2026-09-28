package imageactivity

import (
	"bytes"
	"encoding/json"
	"strings"
	"unicode/utf8"
)

type fixedCapability struct {
	compiled   CompiledCapability
	rules      []ParameterRule
	name       string
	recognized bool
}

func fixedNumber(value float64) *float64 { return &value }
func fixedLength(value int) *int         { return &value }
func fixedJSON(value any) json.RawMessage {
	raw, _ := json.Marshal(value)
	return raw
}

func fixedBaseRules() []ParameterRule {
	return []ParameterRule{
		{Key: Prompt, Supported: true, Required: true, Type: "string", MinLength: fixedLength(1), MaxLength: fixedLength(maxPrompt), LengthUnit: "utf16_units"},
		{Key: N, Supported: true, Type: "integer", Minimum: fixedNumber(1), Maximum: fixedNumber(4), Step: fixedNumber(1), Default: fixedJSON(1)},
		{Key: NegativePrompt, Type: "string", MaxLength: fixedLength(maxPrompt), LengthUnit: "utf8_bytes"},
		{Key: Size, Type: "string", LengthUnit: "utf8_bytes"},
		{Key: AspectRatio, Type: "string", LengthUnit: "utf8_bytes"},
		{Key: Resolution, Type: "string", LengthUnit: "utf8_bytes"},
		{Key: Seed, Supported: true, Type: "integer", Minimum: fixedNumber(0), Maximum: fixedNumber(999999999), Step: fixedNumber(1)},
		{Key: Steps, Type: "integer"},
		{Key: Guidance, Type: "number"},
		{Key: Quality, Type: "string", LengthUnit: "utf8_bytes"},
	}
}

func fixedObject(raw json.RawMessage) (map[string]json.RawMessage, error) {
	if len(raw) == 0 || len(raw) > 32768 || jsonDepth(raw) > 16 || !json.Valid(raw) {
		return nil, ErrInvalid
	}
	if end, err := valueEnd(raw, 0, 0); err != nil || whitespace(raw, end) != len(raw) {
		return nil, ErrInvalid
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(raw, &object) != nil || object == nil {
		return nil, ErrInvalid
	}
	return object, nil
}

// Discover one complete extension namespace. Unrelated catalog fields never
// become capability facts, and competing namespaces remain unsupported.
func fixedNamespace(metadata json.RawMessage) (string, map[string]json.RawMessage, bool, error) {
	object, err := fixedObject(metadata)
	if err != nil {
		return "", nil, false, err
	}
	prefix := ""
	for key := range object {
		if !strings.HasSuffix(key, "_type") {
			continue
		}
		candidate := strings.TrimSuffix(key, "_type")
		if candidate == "" || !safeText(candidate, 128) {
			continue
		}
		_, settings := object[candidate+"_settings"]
		_, supports := object[candidate+"_supports"]
		if !settings && !supports {
			continue
		}
		if prefix != "" || !settings || !supports {
			return "", object, true, ErrInvalid
		}
		prefix = candidate
	}
	if prefix == "" {
		return "", object, false, ErrNotFound
	}
	return prefix, object, true, nil
}

func fixedBool(object map[string]json.RawMessage, key string) (bool, error) {
	raw, present := object[key]
	if !present || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return false, nil
	}
	var value bool
	if json.Unmarshal(raw, &value) != nil || string(raw) == "null" {
		return false, ErrInvalid
	}
	return value, nil
}

func fixedOptional(object map[string]json.RawMessage, key string) (json.RawMessage, bool) {
	raw, present := object[key]
	return raw, present && !bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}

func fixedOptions(raw json.RawMessage) ([]string, string, error) {
	object, err := fixedObject(raw)
	if err != nil {
		return nil, "", err
	}
	var options []string
	if raw, present := fixedOptional(object, "options"); !present {
		return nil, "", nil
	} else if json.Unmarshal(raw, &options) != nil || len(options) > 128 {
		return nil, "", ErrInvalid
	}
	if len(options) == 0 {
		return nil, "", nil
	}
	seen := map[string]bool{}
	for _, option := range options {
		if !validSizeLabel(option) || seen[option] {
			return nil, "", ErrInvalid
		}
		seen[option] = true
	}
	selected := options[0]
	if raw, ok := fixedOptional(object, "default"); ok {
		if json.Unmarshal(raw, &selected) != nil {
			return nil, "", ErrInvalid
		}
		if !seen[selected] {
			selected = options[0]
		}
	}
	return options, selected, nil
}

func fixedOrderedKeys(raw json.RawMessage) []string {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if _, err := decoder.Token(); err != nil {
		return nil
	}
	keys := []string{}
	for decoder.More() {
		token, err := decoder.Token()
		key, ok := token.(string)
		var value json.RawMessage
		if err != nil || !ok || decoder.Decode(&value) != nil {
			return nil
		}
		keys = append(keys, key)
	}
	return keys
}

func fixedSize(settings map[string]json.RawMessage) (*SizeCapability, string, string, error) {
	if raw, present := fixedOptional(settings, "customSizeMapping"); present {
		grid, err := fixedObject(raw)
		if err != nil || len(grid) == 0 || len(grid) > 128 {
			return nil, "", "", ErrInvalid
		}
		size := &SizeCapability{Mode: ResolutionRatioGrid}
		for _, resolution := range fixedOrderedKeys(raw) {
			ratios, e := fixedObject(grid[resolution])
			if e != nil || !validSizeLabel(resolution) || len(ratios) == 0 || len(ratios) > 128 {
				return nil, "", "", ErrInvalid
			}
			for _, ratio := range fixedOrderedKeys(grid[resolution]) {
				var dimension struct {
					Width  int `json:"width"`
					Height int `json:"height"`
				}
				if json.Unmarshal(ratios[ratio], &dimension) != nil || !validSizeLabel(ratio) || dimension.Width < 1 || dimension.Height < 1 {
					return nil, "", "", ErrInvalid
				}
				size.Combinations = append(size.Combinations, SizeCombination{Ratio: ratio, Resolution: resolution, Width: dimension.Width, Height: dimension.Height, Tier: resolution})
				if len(size.Combinations) > 2048 {
					return nil, "", "", ErrInvalid
				}
			}
		}
		resolution := size.Combinations[0].Resolution
		if raw, ok := fixedOptional(settings, "resolution"); ok {
			object, e := fixedObject(raw)
			if e != nil {
				return nil, "", "", ErrInvalid
			}
			if value, exists := fixedOptional(object, "default"); exists {
				var declared string
				if json.Unmarshal(value, &declared) != nil {
					return nil, "", "", ErrInvalid
				}
				if _, exists := grid[declared]; exists {
					resolution = declared
				}
			}
		}
		ratio := fixedOrderedKeys(grid[resolution])[0]
		if raw, ok := fixedOptional(settings, "aspectRatio"); ok {
			object, e := fixedObject(raw)
			if e != nil {
				return nil, "", "", ErrInvalid
			}
			if value, exists := fixedOptional(object, "default"); exists {
				var declared string
				if json.Unmarshal(value, &declared) != nil {
					return nil, "", "", ErrInvalid
				}
				ratios, _ := fixedObject(grid[resolution])
				if _, exists := ratios[declared]; exists {
					ratio = declared
				}
			}
		}
		if _, err := size.ResolveSize(SizeInput{Ratio: ratio, Resolution: resolution}); err != nil {
			return nil, "", "", err
		}
		return size, ratio, resolution, nil
	}
	if raw, present := fixedOptional(settings, "customSizeMappingString"); present {
		mapping, err := fixedObject(raw)
		if err != nil || len(mapping) == 0 || len(mapping) > 128 {
			return nil, "", "", ErrInvalid
		}
		size := &SizeCapability{Mode: RatioSizeMap}
		for _, ratio := range fixedOrderedKeys(raw) {
			var dimensions string
			if json.Unmarshal(mapping[ratio], &dimensions) != nil || !validSizeLabel(ratio) {
				return nil, "", "", ErrInvalid
			}
			width, height, ok := parseSizePair(dimensions)
			if !ok {
				return nil, "", "", ErrInvalid
			}
			size.Combinations = append(size.Combinations, SizeCombination{Ratio: ratio, Width: width, Height: height})
		}
		return size, size.Combinations[0].Ratio, "", size.Validate()
	}
	if raw, present := fixedOptional(settings, "aspectRatio"); present {
		ratios, ratio, err := fixedOptions(raw)
		if err != nil {
			return nil, "", "", err
		}
		if len(ratios) == 0 {
			axis := DimensionRange{Minimum: 256, Maximum: 2048, Step: 8}
			return &SizeCapability{Mode: WidthHeight, Width: &axis, Height: &axis}, "", "", nil
		}
		resolutions, resolution := []string{""}, ""
		if raw, present := fixedOptional(settings, "resolution"); present {
			resolutions, resolution, err = fixedOptions(raw)
			if err != nil {
				return nil, "", "", err
			}
			if len(resolutions) == 0 {
				resolutions = []string{""}
			}
		}
		if len(ratios)*len(resolutions) > 2048 {
			return nil, "", "", ErrInvalid
		}
		size := &SizeCapability{Mode: RatioResolution}
		for _, r := range ratios {
			for _, tier := range resolutions {
				size.Combinations = append(size.Combinations, SizeCombination{Ratio: r, Resolution: tier, Tier: tier})
			}
		}
		return size, ratio, resolution, size.Validate()
	}
	axis := DimensionRange{Minimum: 256, Maximum: 2048, Step: 8}
	return &SizeCapability{Mode: WidthHeight, Width: &axis, Height: &axis}, "", "", nil
}

func fixedSizeRules(rules []ParameterRule, size *SizeCapability, ratio, resolution string) error {
	if size == nil || size.Validate() != nil {
		return ErrInvalid
	}
	for i := range rules {
		rule := &rules[i]
		switch rule.Key {
		case Size:
			rule.Supported = size.Mode != RatioResolution
			if !rule.Supported {
				continue
			}
			if size.Mode == WidthHeight {
				rule.Default = fixedJSON("1024x1024")
				rule.Dimensions = &DimensionRule{Format: "width_height", Width: *size.Width, Height: *size.Height}
			}
		case AspectRatio, Resolution:
			if size.Mode == WidthHeight || rule.Key == Resolution && size.Mode == RatioSizeMap {
				continue
			}
			values := []string{}
			seen := map[string]bool{}
			for _, row := range size.Combinations {
				value := row.Ratio
				if rule.Key == Resolution {
					value = row.Resolution
				}
				if value != "" && !seen[value] {
					values = append(values, value)
					seen[value] = true
				}
			}
			if len(values) == 0 || len(values) > 128 {
				if len(values) > 128 {
					return ErrInvalid
				}
				continue
			}
			rule.Supported = true
			for _, value := range values {
				rule.Enum = append(rule.Enum, fixedJSON(value))
			}
			selected := ratio
			if rule.Key == Resolution {
				selected = resolution
			}
			rule.Default = fixedJSON(selected)
		}
	}
	return nil
}

func compileFixedCapability(metadata json.RawMessage) fixedCapability {
	out := fixedCapability{rules: fixedBaseRules(), compiled: CompiledCapability{SchemaVersion: 2, Readiness: "pending", CatalogType: "unknown"}}
	prefix, object, recognized, err := fixedNamespace(metadata)
	out.recognized = recognized
	problem := func(key ParameterKey) fixedCapability {
		out.rules = fixedBaseRules()
		out.compiled.Problems = []ParameterKey{key}
		out.compiled.Parameters = []CapabilityField{}
		for _, rule := range out.rules {
			out.compiled.Parameters = append(out.compiled.Parameters, CapabilityField{Rule: rule, Support: CapabilityUnknown, Source: "discovered"})
		}
		return out
	}
	if err != nil {
		return problem(Size)
	}
	var kind string
	if json.Unmarshal(object[prefix+"_type"], &kind) != nil || !safeText(kind, 128) {
		return problem(Size)
	}
	if kind != "image" {
		out.compiled.CatalogType = "other"
		return problem(Size)
	}
	out.compiled.CatalogType = "image"
	settings, err := fixedObject(object[prefix+"_settings"])
	if err != nil {
		return problem(Size)
	}
	supports, err := fixedObject(object[prefix+"_supports"])
	if bytes.Equal(bytes.TrimSpace(object[prefix+"_supports"]), []byte("null")) {
		supports, err = map[string]json.RawMessage{}, nil
	}
	if err != nil {
		return problem(Size)
	}
	if raw, exists := object[prefix+"_friendly_name"]; exists {
		if json.Unmarshal(raw, &out.name) != nil || !safeText(out.name, 512) || utf8.RuneCountInString(out.name) > 128 {
			out.name = ""
		}
	}
	negative, err := fixedBool(supports, "negative_prompts")
	if err != nil {
		return problem(NegativePrompt)
	}
	steps, err := fixedBool(supports, "steps")
	if err != nil {
		return problem(Steps)
	}
	for i := range out.rules {
		rule := &out.rules[i]
		switch rule.Key {
		case Prompt:
			if raw, present := fixedOptional(settings, "promptCharacterLimit"); present {
				limit, e := fixedCanonicalNumber(raw)
				if e != nil || !safeInteger(limit) || limit < 0 {
					return problem(Prompt)
				}
				if limit == 0 {
					continue
				}
				if limit > maxPrompt {
					limit = maxPrompt
				}
				rule.MaxLength = fixedLength(int(limit))
			}
		case NegativePrompt:
			rule.Supported = negative
		case Steps:
			if steps {
				raw, exists := fixedOptional(settings, "steps")
				if !exists {
					continue
				}
				object, e := fixedObject(raw)
				if e != nil {
					return problem(Steps)
				}
				maximumRaw, exists := fixedOptional(object, "max")
				if !exists {
					continue
				}
				maximum, e := fixedCanonicalNumber(maximumRaw)
				if e != nil || !safeInteger(maximum) || maximum < 0 {
					return problem(Steps)
				}
				if maximum == 0 {
					continue
				}
				rule.Supported, rule.Minimum, rule.Maximum, rule.Step = true, fixedNumber(1), fixedNumber(maximum), fixedNumber(1)
				rule.Default = fixedJSON(maximum)
				if raw, present := fixedOptional(object, "default"); present {
					selected, e := fixedCanonicalNumber(raw)
					if e != nil || !safeInteger(selected) {
						return problem(Steps)
					}
					if selected < 1 {
						selected = 1
					}
					if selected > maximum {
						selected = maximum
					}
					rule.Default = fixedJSON(selected)
				}
			}
		case Guidance:
			if raw, present := fixedOptional(settings, "cfgScale"); present {
				object, e := fixedObject(raw)
				if e != nil {
					return problem(Guidance)
				}
				scale, exists := fixedOptional(object, "scale")
				if !exists {
					continue
				}
				if json.Unmarshal(scale, &rule.Enum) != nil || len(rule.Enum) > 128 {
					return problem(Guidance)
				}
				if len(rule.Enum) == 0 {
					continue
				}
				for _, option := range rule.Enum {
					if _, err := fixedCanonicalNumber(option); err != nil {
						return problem(Guidance)
					}
				}
				rule.Supported, rule.Default = true, rule.Enum[0]
				if raw, present := fixedOptional(object, "default"); present {
					selected, e := fixedCanonicalNumber(raw)
					if e != nil {
						return problem(Guidance)
					}
					for _, option := range rule.Enum {
						value, e := scalar(option)
						if e == nil && scalarEqual(value, selected) {
							rule.Default = raw
							break
						}
					}
				}
			}
		case Quality:
			if raw, present := settings["quality"]; present && string(raw) != "false" && string(raw) != "null" {
				if string(raw) != "true" {
					if _, err := fixedObject(raw); err != nil {
						return problem(Quality)
					}
				}
				rule.Supported = true
				for _, quality := range []string{"low", "medium", "high"} {
					rule.Enum = append(rule.Enum, fixedJSON(quality))
				}
				rule.Default = fixedJSON("low")
			}
		}
	}
	size, ratio, resolution, err := fixedSize(settings)
	if err != nil || fixedSizeRules(out.rules, size, ratio, resolution) != nil || validateParameters(out.rules, nil) != nil {
		return problem(Size)
	}
	out.compiled.Size, out.compiled.Readiness = size, "ready"
	out.compiled.Parameters = make([]CapabilityField, 0, len(out.rules))
	for _, rule := range out.rules {
		support := CapabilityUnsupported
		if rule.Supported {
			support = CapabilitySupported
		}
		out.compiled.Parameters = append(out.compiled.Parameters, CapabilityField{Rule: rule, Support: support, Source: "discovered"})
	}
	return out
}

func fixedCapabilityIssues(modelID string, capability CompiledCapability) []CheckIssue {
	issues := []CheckIssue{}
	if capability.Readiness == "ready" {
		return issues
	}
	for _, key := range capability.Problems {
		issues = append(issues, CheckIssue{ModelID: modelID, FieldPath: "parameters." + string(key), Code: "unsupported_metadata", SafeMessage: "The upstream model metadata is missing or unsupported. Refresh the catalog or choose another image model."})
	}
	if len(issues) == 0 {
		issues = append(issues, CheckIssue{ModelID: modelID, FieldPath: "parameters", Code: "unsupported_metadata", SafeMessage: "The upstream model metadata is missing or unsupported. Refresh the catalog or choose another image model."})
	}
	return issues
}

func fixedDisplayName(modelID, friendly string) string {
	if friendly != "" {
		return friendly
	}
	name := []rune(modelID)
	if len(name) > 128 {
		name = name[:128]
	}
	if len(name) == 0 || !safeText(string(name), 512) {
		return "Image model"
	}
	return string(name)
}
