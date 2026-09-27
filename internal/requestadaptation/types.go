// Package requestadaptation validates and stores bounded request adaptations.
// Management projections and routine formatting never reveal configured values.
package requestadaptation

import (
	"encoding/json"
	"errors"
	"log/slog"
)

const (
	MaxConfigurationBytes = 64 << 10
	MaxDepth              = 16
	MaxHeaderNames        = 64
	MaxHeaderNameBytes    = 128
	MaxHeaderValueBytes   = 4096
	MaxAddedHeaderBytes   = 16 << 10
	MaxBodyPaths          = 128
	MaxPathBytes          = 512
)

var (
	ErrInvalid     = errors.New("request adaptation: invalid configuration")
	ErrConflict    = errors.New("request adaptation: revision conflict")
	ErrUnavailable = errors.New("request adaptation: unavailable")
)

type Scope string

const (
	ScopeEndpoint     Scope = "endpoint"
	ScopeCharityModel Scope = "charity_model"
	ScopeBinding      Scope = "binding"
)

type Ref struct {
	Scope Scope
	ID    int64
}

func (r Ref) valid() bool {
	return r.ID > 0 && (r.Scope == ScopeEndpoint || r.Scope == ScopeCharityModel || r.Scope == ScopeBinding)
}

type Mode string

const (
	ModeInherit Mode = "inherit"
	ModeReplace Mode = "replace"
)

type ListSection struct {
	Mode   Mode     `json:"mode"`
	Values []string `json:"values"`
}

type HeaderSection struct {
	Mode   Mode              `json:"mode"`
	Values map[string]string `json:"values"`
}

type BodySection struct {
	Mode   Mode                       `json:"mode"`
	Values map[string]json.RawMessage `json:"values"`
}

// Document is sensitive and belongs only in an attempt or the encrypted store.
type Document struct {
	ForwardHeaders       ListSection   `json:"forward_headers"`
	FixedHeaders         HeaderSection `json:"fixed_headers"`
	BodyDefaults         BodySection   `json:"body_defaults"`
	BodyForced           BodySection   `json:"body_forced"`
	NativeExtensionPaths ListSection   `json:"native_extension_paths"`
}

func (Document) String() string   { return "[redacted request adaptation]" }
func (Document) GoString() string { return "[redacted request adaptation]" }
func (Document) LogValue() slog.Value {
	return slog.StringValue("[redacted request adaptation]")
}

func Empty(scope Scope) Document {
	mode := ModeReplace
	if scope == ScopeBinding {
		mode = ModeInherit
	}
	return Document{
		ForwardHeaders:       ListSection{Mode: mode, Values: []string{}},
		FixedHeaders:         HeaderSection{Mode: mode, Values: map[string]string{}},
		BodyDefaults:         BodySection{Mode: mode, Values: map[string]json.RawMessage{}},
		BodyForced:           BodySection{Mode: mode, Values: map[string]json.RawMessage{}},
		NativeExtensionPaths: ListSection{Mode: mode, Values: []string{}},
	}
}

func (d *Document) Clear() {
	if d == nil {
		return
	}
	for name := range d.FixedHeaders.Values {
		delete(d.FixedHeaders.Values, name)
	}
	for _, values := range []map[string]json.RawMessage{d.BodyDefaults.Values, d.BodyForced.Values} {
		for path, value := range values {
			clear(value)
			delete(values, path)
		}
	}
	*d = Document{}
}

type ValueEdit struct {
	Action string          `json:"action"`
	Value  json.RawMessage `json:"value,omitempty"`
}

type ListEdit struct {
	Mode   Mode     `json:"mode"`
	Values []string `json:"values"`
}

type MapEdit struct {
	Mode   Mode                 `json:"mode"`
	Values map[string]ValueEdit `json:"values"`
}

// Omitted partitions preserve the previous partition; ParsePatch rejects null.
type Patch struct {
	ExpectedRevision     int64
	ForwardHeaders       *ListEdit
	FixedHeaders         *MapEdit
	BodyDefaults         *MapEdit
	BodyForced           *MapEdit
	NativeExtensionPaths *ListEdit
}

type ValueProjection struct {
	HasValue bool   `json:"has_value"`
	Mask     string `json:"mask,omitempty"`
}

type ListProjection struct {
	Mode   Mode     `json:"mode"`
	Values []string `json:"values"`
	Source Scope    `json:"source,omitempty"`
}

type MapProjection struct {
	Mode   Mode                       `json:"mode"`
	Values map[string]ValueProjection `json:"values"`
	Source Scope                      `json:"source,omitempty"`
}

// Projection contains structure and opaque presence only; it is safe for GET,
// idempotency replay, and personal export.
type Projection struct {
	Revision             string         `json:"revision"`
	ForwardHeaders       ListProjection `json:"forward_headers"`
	FixedHeaders         MapProjection  `json:"fixed_headers"`
	BodyDefaults         MapProjection  `json:"body_defaults"`
	BodyForced           MapProjection  `json:"body_forced"`
	NativeExtensionPaths ListProjection `json:"native_extension_paths"`
}

type Snapshot struct {
	Document Document
	Revision string
	Sources  [5]Scope
}

func (Snapshot) String() string   { return "[redacted request adaptation snapshot]" }
func (Snapshot) GoString() string { return "[redacted request adaptation snapshot]" }
func (Snapshot) LogValue() slog.Value {
	return slog.StringValue("[redacted request adaptation snapshot]")
}

func (s *Snapshot) Clear() {
	if s == nil {
		return
	}
	s.Document.Clear()
	*s = Snapshot{}
}
