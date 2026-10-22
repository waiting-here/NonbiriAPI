package requestadaptation

import (
	"encoding/json"
	"log/slog"
)

// ConfigurationValueProjection includes a value only when it has no response
// protection markers. RawMessage preserves null and every other JSON type.
type ConfigurationValueProjection struct {
	ValueProjection
	Value json.RawMessage `json:"value,omitempty"`
}

func (ConfigurationValueProjection) String() string   { return "[redacted adaptation value]" }
func (ConfigurationValueProjection) GoString() string { return "[redacted adaptation value]" }
func (ConfigurationValueProjection) LogValue() slog.Value {
	return slog.StringValue("[redacted adaptation value]")
}

type ConfigurationMapProjection struct {
	Mode   Mode                                    `json:"mode"`
	Values map[string]ConfigurationValueProjection `json:"values"`
	Source Scope                                   `json:"source,omitempty"`
}

func (ConfigurationMapProjection) String() string   { return "[redacted adaptation partition]" }
func (ConfigurationMapProjection) GoString() string { return "[redacted adaptation partition]" }
func (ConfigurationMapProjection) LogValue() slog.Value {
	return slog.StringValue("[redacted adaptation partition]")
}

// ConfigurationProjection is only for authorized configuration GET responses.
// Persistence, mutation receipts, and exports use the value-free Projection.
type ConfigurationProjection struct {
	ExcludedRequestFields []string                   `json:"excluded_request_fields,omitempty"`
	Revision              string                     `json:"revision"`
	ForwardHeaders        ListProjection             `json:"forward_headers"`
	FixedHeaders          ConfigurationMapProjection `json:"fixed_headers"`
	BodyDefaults          ConfigurationMapProjection `json:"body_defaults"`
	BodyForced            ConfigurationMapProjection `json:"body_forced"`
	NativeExtensionPaths  ListProjection             `json:"native_extension_paths"`
}

func (ConfigurationProjection) String() string   { return "[redacted adaptation configuration]" }
func (ConfigurationProjection) GoString() string { return "[redacted adaptation configuration]" }
func (ConfigurationProjection) LogValue() slog.Value {
	return slog.StringValue("[redacted adaptation configuration]")
}

func (s Snapshot) ConfigurationProjection() ConfigurationProjection {
	safe := s.Projection()
	project := func(section MapProjection, values map[string]json.RawMessage) ConfigurationMapProjection {
		out := ConfigurationMapProjection{Mode: section.Mode, Source: section.Source, Values: make(map[string]ConfigurationValueProjection, len(section.Values))}
		for key, presence := range section.Values {
			value := ConfigurationValueProjection{ValueProjection: presence}
			if len(presence.SensitivePaths) == 0 {
				value.Value = append(json.RawMessage(nil), values[key]...)
				value.Mask = ""
			}
			out.Values[key] = value
		}
		return out
	}
	headers := make(map[string]json.RawMessage, len(s.Document.FixedHeaders.Values))
	for name, value := range s.Document.FixedHeaders.Values {
		headers[name], _ = json.Marshal(value)
	}
	return ConfigurationProjection{
		ExcludedRequestFields: safe.ExcludedRequestFields,
		Revision:              safe.Revision,
		ForwardHeaders:        safe.ForwardHeaders,
		FixedHeaders:          project(safe.FixedHeaders, headers),
		BodyDefaults:          project(safe.BodyDefaults, s.Document.BodyDefaults.Values),
		BodyForced:            project(safe.BodyForced, s.Document.BodyForced.Values),
		NativeExtensionPaths:  safe.NativeExtensionPaths,
	}
}
