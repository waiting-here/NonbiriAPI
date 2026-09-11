package game

import (
	"encoding/json"
	"maps"
)

// Configuration is a complete validated view of one registry's raw keys.
// Only module codecs interpret values; the host owns the master switch.
type Configuration struct {
	enabled  bool
	registry *Registry
	values   map[string]ConfigValue
}

func CompileConfiguration(registry *Registry, raw map[string]string) (Configuration, error) {
	if !registry.Sealed() {
		return Configuration{}, ErrInvalidContract
	}
	enabled, err := RawBool(raw, GamesEnabledKey, false)
	if err != nil {
		return Configuration{}, err
	}
	result := Configuration{enabled: enabled, registry: registry, values: make(map[string]ConfigValue)}
	for _, module := range registry.Descriptors() {
		value, err := module.Codec.Compile(maps.Clone(raw))
		if err != nil {
			return Configuration{}, err
		}
		if value == nil || value.Enabled() && !enabled {
			return Configuration{}, ErrInvalidConfig
		}
		declared := make(map[string]bool)
		for _, key := range module.Codec.Keys() {
			declared[key] = true
		}
		canonical := value.Raw()
		if len(canonical) != len(declared) {
			return Configuration{}, ErrInvalidConfig
		}
		for key := range canonical {
			if !declared[key] {
				return Configuration{}, ErrInvalidConfig
			}
		}
		result.values[module.ID] = value
	}
	return result, nil
}

func (configuration Configuration) Enabled() bool { return configuration.enabled }
func (configuration Configuration) Value(id string) (ConfigValue, bool) {
	value, ok := configuration.values[id]
	return value, ok
}

func (registry *Registry) ConfigKeys() []string {
	keys := []string{GamesEnabledKey}
	for _, module := range registry.Descriptors() {
		keys = append(keys, module.Codec.Keys()...)
	}
	return keys
}

func (configuration Configuration) Raw() map[string]string {
	result := map[string]string{GamesEnabledKey: BoolRaw(configuration.enabled)}
	for _, module := range configuration.registry.Descriptors() {
		for key, value := range configuration.values[module.ID].Raw() {
			result[key] = value
		}
	}
	return result
}

// WireConfiguration preserves named module objects without requiring the
// host to know their DTOs or add a switch when a module is registered.
type WireConfiguration struct {
	Revision      string
	MasterEnabled bool
	Modules       map[string]json.RawMessage
}

func (wire WireConfiguration) MarshalJSON() ([]byte, error) {
	fields := maps.Clone(wire.Modules)
	if fields == nil {
		fields = make(map[string]json.RawMessage)
	}
	fields["revision"] = ConfigJSON(wire.Revision)
	fields["master_enabled"] = ConfigJSON(wire.MasterEnabled)
	return json.Marshal(fields)
}

func (wire *WireConfiguration) UnmarshalJSON(body []byte) error {
	var fields map[string]json.RawMessage
	if DecodeConfigPatch(body, &fields) != nil || json.Unmarshal(fields["revision"], &wire.Revision) != nil || !ValidConfigRevision(wire.Revision) || json.Unmarshal(fields["master_enabled"], &wire.MasterEnabled) != nil {
		return ErrInvalidConfig
	}
	delete(fields, "revision")
	delete(fields, "master_enabled")
	wire.Modules = fields
	return nil
}

func (configuration Configuration) Wire(revision string) WireConfiguration {
	result := WireConfiguration{Revision: revision, MasterEnabled: configuration.enabled, Modules: make(map[string]json.RawMessage)}
	for id, value := range configuration.values {
		result.Modules[id] = value.Wire()
	}
	return result
}

type ConfigurationPatch struct {
	ExpectedRevision string
	MasterEnabled    *bool
	Modules          map[string]json.RawMessage
}

func (patch ConfigurationPatch) MarshalJSON() ([]byte, error) {
	fields := maps.Clone(patch.Modules)
	if fields == nil {
		fields = make(map[string]json.RawMessage)
	}
	fields["expected_revision"] = ConfigJSON(patch.ExpectedRevision)
	if patch.MasterEnabled != nil {
		fields["master_enabled"] = ConfigJSON(*patch.MasterEnabled)
	}
	return json.Marshal(fields)
}

func (registry *Registry) DecodePatch(body []byte) (ConfigurationPatch, error) {
	if !registry.Sealed() {
		return ConfigurationPatch{}, ErrInvalidContract
	}
	var fields map[string]json.RawMessage
	if err := DecodeConfigPatch(body, &fields); err != nil {
		return ConfigurationPatch{}, err
	}
	var patch ConfigurationPatch
	if json.Unmarshal(fields["expected_revision"], &patch.ExpectedRevision) != nil || !canonicalPositiveU128(patch.ExpectedRevision) {
		return patch, ErrInvalidConfig
	}
	delete(fields, "expected_revision")
	if value, ok := fields["master_enabled"]; ok {
		var enabled bool
		if json.Unmarshal(value, &enabled) != nil {
			return patch, ErrInvalidConfig
		}
		patch.MasterEnabled = &enabled
		delete(fields, "master_enabled")
	}
	if len(fields) == 0 && patch.MasterEnabled == nil {
		return patch, ErrInvalidConfig
	}
	known := make(map[string]ConfigCodec)
	for _, module := range registry.Descriptors() {
		known[module.ID] = module.Codec
	}
	for id, value := range fields {
		codec, exists := known[id]
		if !exists {
			return patch, ErrInvalidConfig
		}
		if err := codec.ValidatePatch(value); err != nil {
			return patch, err
		}
	}
	patch.Modules = fields
	return patch, nil
}

func (configuration Configuration) Patch(patch ConfigurationPatch, revision string) (Configuration, error) {
	if patch.ExpectedRevision != revision {
		return Configuration{}, ErrRevisionConflict
	}
	for id := range patch.Modules {
		if _, ok := configuration.values[id]; !ok {
			return Configuration{}, ErrInvalidConfig
		}
	}
	enabled := configuration.enabled
	if patch.MasterEnabled != nil {
		enabled = *patch.MasterEnabled
	}
	raw := configuration.Raw()
	raw[GamesEnabledKey] = BoolRaw(enabled)
	for _, module := range configuration.registry.Descriptors() {
		if body, exists := patch.Modules[module.ID]; exists {
			value, err := module.Codec.Merge(configuration.values[module.ID], body, enabled)
			if err != nil {
				return Configuration{}, err
			}
			for key, item := range value.Raw() {
				raw[key] = item
			}
		}
	}
	return CompileConfiguration(configuration.registry, raw)
}
