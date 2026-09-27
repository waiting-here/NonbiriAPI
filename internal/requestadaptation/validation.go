package requestadaptation

import (
	"bytes"
	"encoding/json"
	"math"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/waiting-here/NonbiriAPI/internal/strictjson"
)

func validJSON(data []byte) bool {
	if len(data) == 0 || len(data) > MaxConfigurationBytes || strictjson.ValidateObjectWithFieldLimit(data, 16384) != nil {
		return false
	}
	var value any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if decoder.Decode(&value) != nil {
		return false
	}
	return boundedDepth(value, 1)
}

func boundedDepth(value any, depth int) bool {
	if depth > MaxDepth {
		return false
	}
	switch item := value.(type) {
	case map[string]any:
		for _, child := range item {
			if !boundedDepth(child, depth+1) {
				return false
			}
		}
	case []any:
		for _, child := range item {
			if !boundedDepth(child, depth+1) {
				return false
			}
		}
	}
	return true
}

func ParsePatch(data []byte) (Patch, error) {
	if !validJSON(data) {
		return Patch{}, ErrInvalid
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(data, &fields) != nil {
		return Patch{}, ErrInvalid
	}
	var patch Patch
	for name, raw := range fields {
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return Patch{}, ErrInvalid
		}
		switch name {
		case "expected_revision":
			var text string
			if json.Unmarshal(raw, &text) != nil || text == "" || len(text) > 19 || len(text) > 1 && text[0] == '0' {
				return Patch{}, ErrInvalid
			}
			value, err := strconv.ParseInt(text, 10, 64)
			if err != nil || value < 0 {
				return Patch{}, ErrInvalid
			}
			patch.ExpectedRevision = value
		case "forward_headers":
			value, err := parseListEdit(raw)
			if err != nil {
				return Patch{}, err
			}
			patch.ForwardHeaders = &value
		case "fixed_headers":
			value, err := parseMapEdit(raw)
			if err != nil {
				return Patch{}, err
			}
			patch.FixedHeaders = &value
		case "body_defaults":
			value, err := parseMapEdit(raw)
			if err != nil {
				return Patch{}, err
			}
			patch.BodyDefaults = &value
		case "body_forced":
			value, err := parseMapEdit(raw)
			if err != nil {
				return Patch{}, err
			}
			patch.BodyForced = &value
		case "native_extension_paths":
			value, err := parseListEdit(raw)
			if err != nil {
				return Patch{}, err
			}
			patch.NativeExtensionPaths = &value
		default:
			return Patch{}, ErrInvalid
		}
	}
	if _, ok := fields["expected_revision"]; !ok || len(fields) < 2 {
		return Patch{}, ErrInvalid
	}
	return patch, nil
}

func parseListEdit(raw []byte) (ListEdit, error) {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields == nil {
		return ListEdit{}, ErrInvalid
	}
	for name, value := range fields {
		if name != "mode" && name != "values" || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return ListEdit{}, ErrInvalid
		}
	}
	var out ListEdit
	if json.Unmarshal(fields["mode"], &out.Mode) != nil || out.Mode != ModeReplace && out.Mode != ModeInherit {
		return ListEdit{}, ErrInvalid
	}
	if rawValues, ok := fields["values"]; ok && json.Unmarshal(rawValues, &out.Values) != nil {
		return ListEdit{}, ErrInvalid
	}
	if out.Mode == ModeInherit && len(out.Values) != 0 {
		return ListEdit{}, ErrInvalid
	}
	if out.Values == nil {
		out.Values = []string{}
	}
	return out, nil
}

func parseMapEdit(raw []byte) (MapEdit, error) {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields == nil {
		return MapEdit{}, ErrInvalid
	}
	for name, value := range fields {
		if name != "mode" && name != "values" || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return MapEdit{}, ErrInvalid
		}
	}
	var out MapEdit
	if json.Unmarshal(fields["mode"], &out.Mode) != nil || out.Mode != ModeReplace && out.Mode != ModeInherit {
		return MapEdit{}, ErrInvalid
	}
	if rawValues, ok := fields["values"]; ok {
		var rawEdits map[string]json.RawMessage
		if json.Unmarshal(rawValues, &rawEdits) != nil || rawEdits == nil {
			return MapEdit{}, ErrInvalid
		}
		out.Values = make(map[string]ValueEdit, len(rawEdits))
		for key, rawEdit := range rawEdits {
			var editFields map[string]json.RawMessage
			if json.Unmarshal(rawEdit, &editFields) != nil || editFields == nil {
				return MapEdit{}, ErrInvalid
			}
			for field := range editFields {
				if field != "action" && field != "value" {
					return MapEdit{}, ErrInvalid
				}
			}
			var edit ValueEdit
			if json.Unmarshal(rawEdit, &edit) != nil {
				return MapEdit{}, ErrInvalid
			}
			_, hasValue := editFields["value"]
			if edit.Action != "replace" && hasValue || edit.Action == "replace" && !hasValue {
				return MapEdit{}, ErrInvalid
			}
			out.Values[key] = edit
		}
	}
	if out.Mode == ModeInherit && len(out.Values) != 0 {
		return MapEdit{}, ErrInvalid
	}
	if out.Values == nil {
		out.Values = map[string]ValueEdit{}
	}
	for _, edit := range out.Values {
		if edit.Action != "keep" && edit.Action != "replace" && edit.Action != "clear" ||
			(edit.Action == "replace") != (len(edit.Value) > 0) {
			return MapEdit{}, ErrInvalid
		}
	}
	return out, nil
}

func cloneDocument(d Document) Document {
	out := d
	out.ForwardHeaders.Values = append([]string{}, d.ForwardHeaders.Values...)
	out.NativeExtensionPaths.Values = append([]string{}, d.NativeExtensionPaths.Values...)
	out.FixedHeaders.Values = make(map[string]string, len(d.FixedHeaders.Values))
	for name, value := range d.FixedHeaders.Values {
		out.FixedHeaders.Values[name] = value
	}
	out.BodyDefaults.Values = cloneBody(d.BodyDefaults.Values)
	out.BodyForced.Values = cloneBody(d.BodyForced.Values)
	return out
}

func cloneBody(values map[string]json.RawMessage) map[string]json.RawMessage {
	out := make(map[string]json.RawMessage, len(values))
	for path, value := range values {
		out[path] = append(json.RawMessage(nil), value...)
	}
	return out
}

func ApplyPatch(previous Document, patch Patch, scope Scope) (Document, error) {
	if scope != ScopeEndpoint && scope != ScopeCharityModel && scope != ScopeBinding {
		return Document{}, ErrInvalid
	}
	out := cloneDocument(previous)
	if patch.ForwardHeaders != nil {
		out.ForwardHeaders = ListSection{Mode: patch.ForwardHeaders.Mode, Values: append([]string{}, patch.ForwardHeaders.Values...)}
	}
	if patch.NativeExtensionPaths != nil {
		out.NativeExtensionPaths = ListSection{Mode: patch.NativeExtensionPaths.Mode, Values: append([]string{}, patch.NativeExtensionPaths.Values...)}
	}
	if patch.FixedHeaders != nil {
		values, err := applyHeaderEdits(previous.FixedHeaders.Values, patch.FixedHeaders.Values)
		if err != nil {
			out.Clear()
			return Document{}, err
		}
		out.FixedHeaders = HeaderSection{Mode: patch.FixedHeaders.Mode, Values: values}
	}
	if patch.BodyDefaults != nil {
		values, err := applyBodyEdits(previous.BodyDefaults.Values, patch.BodyDefaults.Values)
		if err != nil {
			out.Clear()
			return Document{}, err
		}
		out.BodyDefaults = BodySection{Mode: patch.BodyDefaults.Mode, Values: values}
	}
	if patch.BodyForced != nil {
		values, err := applyBodyEdits(previous.BodyForced.Values, patch.BodyForced.Values)
		if err != nil {
			out.Clear()
			return Document{}, err
		}
		out.BodyForced = BodySection{Mode: patch.BodyForced.Mode, Values: values}
	}
	if err := ValidateDocument(out, scope); err != nil {
		out.Clear()
		return Document{}, err
	}
	return out, nil
}

func applyHeaderEdits(previous map[string]string, edits map[string]ValueEdit) (map[string]string, error) {
	out := make(map[string]string, len(edits))
	for name, edit := range edits {
		switch edit.Action {
		case "keep":
			value, ok := previous[name]
			if !ok {
				return nil, ErrInvalid
			}
			out[name] = value
		case "replace":
			var value string
			if bytes.Equal(bytes.TrimSpace(edit.Value), []byte("null")) || json.Unmarshal(edit.Value, &value) != nil {
				return nil, ErrInvalid
			}
			out[name] = value
		case "clear":
		default:
			return nil, ErrInvalid
		}
	}
	return out, nil
}

func applyBodyEdits(previous map[string]json.RawMessage, edits map[string]ValueEdit) (map[string]json.RawMessage, error) {
	out := make(map[string]json.RawMessage, len(edits))
	for path, edit := range edits {
		switch edit.Action {
		case "keep":
			value, ok := previous[path]
			if !ok {
				return nil, ErrInvalid
			}
			out[path] = append(json.RawMessage(nil), value...)
		case "replace":
			if len(edit.Value) == 0 || !json.Valid(edit.Value) {
				return nil, ErrInvalid
			}
			out[path] = append(json.RawMessage(nil), edit.Value...)
		case "clear":
		default:
			return nil, ErrInvalid
		}
	}
	return out, nil
}

func ValidHeaderName(name string) bool {
	if name == "" || len(name) > MaxHeaderNameBytes || !utf8.ValidString(name) {
		return false
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
			(c >= '0' && c <= '9') || strings.ContainsRune("!#$%&'*+-.^_`|~", rune(c))) {
			return false
		}
	}
	lower := strings.ToLower(name)
	if strings.HasPrefix(lower, "proxy-") || strings.HasPrefix(lower, "x-forwarded-") ||
		strings.HasPrefix(lower, "anthropic-") || strings.HasPrefix(lower, "ai-") ||
		strings.Contains(lower, "csrf") || strings.Contains(lower, "session") || strings.Contains(lower, "caller-key") {
		return false
	}
	switch lower {
	case "host", "content-length", "transfer-encoding", "connection", "keep-alive", "te", "trailer", "upgrade",
		"authorization", "proxy-authorization", "cookie", "set-cookie", "content-type", "accept", "x-api-key", "forwarded":
		return false
	}
	return true
}

func validHeaderValue(value string) bool {
	if len(value) > MaxHeaderValueBytes || !utf8.ValidString(value) {
		return false
	}
	for i := 0; i < len(value); i++ {
		if value[i] == 0 || value[i] == '\r' || value[i] == '\n' {
			return false
		}
	}
	return true
}

// ParsePointer accepts RFC6901 object member paths, never the root. Runtime
// traversal rejects arrays, so a numeric object member remains unambiguous.
func ParsePointer(path string) ([]string, error) {
	if path == "" || len(path) > MaxPathBytes || path[0] != '/' || !utf8.ValidString(path) {
		return nil, ErrInvalid
	}
	raw := strings.Split(path[1:], "/")
	if len(raw) == 0 || len(raw) > MaxDepth {
		return nil, ErrInvalid
	}
	parts := make([]string, len(raw))
	for i, item := range raw {
		var out strings.Builder
		for n := 0; n < len(item); n++ {
			if item[n] != '~' {
				out.WriteByte(item[n])
				continue
			}
			if n+1 >= len(item) || item[n+1] != '0' && item[n+1] != '1' {
				return nil, ErrInvalid
			}
			if item[n+1] == '0' {
				out.WriteByte('~')
			} else {
				out.WriteByte('/')
			}
			n++
		}
		parts[i] = out.String()
		if parts[i] == "" || parts[i] == "__proto__" || parts[i] == "prototype" || parts[i] == "constructor" {
			return nil, ErrInvalid
		}
	}
	return parts, nil
}

var protectedPaths = []string{
	"/model", "/messages", "/input", "/tools", "/tool_choice", "/user", "/safety_identifier",
	"/stream", "/stream_options", "/store", "/metadata", "/providerOptions/gateway/user",
	"/usage", "/billing", "/credential", "/route", "/endpoint", "/price",
	"/prompt", "/system", "/content", "/toolChoice", "/maxOutputTokens",
}

func overlapsPath(a, b string) bool {
	return a == b || strings.HasPrefix(a, b+"/") || strings.HasPrefix(b, a+"/")
}

func validPaths(paths []string) bool {
	seen := make([]string, 0, len(paths))
	for _, path := range paths {
		if _, err := ParsePointer(path); err != nil {
			return false
		}
		for _, protected := range protectedPaths {
			if overlapsPath(path, protected) {
				return false
			}
		}
		for _, prior := range seen {
			if overlapsPath(path, prior) {
				return false
			}
		}
		seen = append(seen, path)
	}
	return true
}

func ValidateDocument(d Document, scope Scope) error {
	if scope != ScopeEndpoint && scope != ScopeCharityModel && scope != ScopeBinding {
		return ErrInvalid
	}
	modes := []Mode{d.ForwardHeaders.Mode, d.FixedHeaders.Mode, d.BodyDefaults.Mode, d.BodyForced.Mode, d.NativeExtensionPaths.Mode}
	for _, mode := range modes {
		if mode != ModeReplace && (scope != ScopeBinding || mode != ModeInherit) {
			return ErrInvalid
		}
	}
	if d.ForwardHeaders.Mode == ModeInherit && len(d.ForwardHeaders.Values) != 0 ||
		d.FixedHeaders.Mode == ModeInherit && len(d.FixedHeaders.Values) != 0 ||
		d.BodyDefaults.Mode == ModeInherit && len(d.BodyDefaults.Values) != 0 ||
		d.BodyForced.Mode == ModeInherit && len(d.BodyForced.Values) != 0 ||
		d.NativeExtensionPaths.Mode == ModeInherit && len(d.NativeExtensionPaths.Values) != 0 {
		return ErrInvalid
	}
	names := make(map[string]struct{})
	for _, name := range d.ForwardHeaders.Values {
		if !ValidHeaderName(name) {
			return ErrInvalid
		}
		lower := strings.ToLower(name)
		if _, duplicate := names[lower]; duplicate {
			return ErrInvalid
		}
		names[lower] = struct{}{}
	}
	fixed := make(map[string]struct{})
	for name, value := range d.FixedHeaders.Values {
		if !ValidHeaderName(name) || !validHeaderValue(value) {
			return ErrInvalid
		}
		lower := strings.ToLower(name)
		if _, duplicate := fixed[lower]; duplicate {
			return ErrInvalid
		}
		fixed[lower] = struct{}{}
		names[lower] = struct{}{}
	}
	if len(names) > MaxHeaderNames {
		return ErrInvalid
	}
	pathCount := len(d.BodyDefaults.Values) + len(d.BodyForced.Values) + len(d.NativeExtensionPaths.Values)
	if pathCount > MaxBodyPaths {
		return ErrInvalid
	}
	for _, values := range []map[string]json.RawMessage{d.BodyDefaults.Values, d.BodyForced.Values} {
		paths := make([]string, 0, len(values))
		for path, raw := range values {
			if !json.Valid(raw) {
				return ErrInvalid
			}
			parts, err := ParsePointer(path)
			if err != nil {
				return ErrInvalid
			}
			if samplerField(parts[0]) {
				if len(parts) != 1 || ValidateSamplerFields(map[string]json.RawMessage{parts[0]: raw}) != nil {
					return ErrInvalid
				}
			}
			paths = append(paths, path)
		}
		if !validPaths(paths) {
			return ErrInvalid
		}
	}
	if !validPaths(d.NativeExtensionPaths.Values) {
		return ErrInvalid
	}
	for _, path := range d.NativeExtensionPaths.Values {
		if !nativeRootAllowed(path) {
			return ErrInvalid
		}
	}
	if _, err := ForcedMaxOutput(d); err != nil {
		return ErrInvalid
	}
	encoded, err := json.Marshal(d)
	if err != nil || len(encoded) > MaxConfigurationBytes || !validJSON(encoded) {
		return ErrInvalid
	}
	return nil
}

func samplerField(name string) bool {
	switch name {
	case "temperature", "top_p", "presence_penalty", "frequency_penalty", "max_tokens", "max_completion_tokens":
		return true
	}
	return false
}

func ValidateSamplerFields(body map[string]json.RawMessage) error {
	for name, bounds := range map[string][2]float64{
		"temperature": {0, 2}, "top_p": {0, 1}, "presence_penalty": {-2, 2},
		"frequency_penalty": {-2, 2}, "max_tokens": {1, math.MaxInt32},
		"max_completion_tokens": {1, math.MaxInt32},
	} {
		raw, present := body[name]
		if !present {
			continue
		}
		trimmed := bytes.TrimSpace(raw)
		// Optional sampling fields retain their established null semantics.
		// Forced output limits are separately required to provide a budget.
		if bytes.Equal(trimmed, []byte("null")) {
			continue
		}
		if len(trimmed) == 0 || trimmed[0] == '"' {
			return ErrInvalid
		}
		var value json.Number
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		if decoder.Decode(&value) != nil {
			return ErrInvalid
		}
		if strings.HasPrefix(name, "max_") {
			integer, err := strconv.ParseInt(value.String(), 10, 64)
			if err != nil || integer < 1 || integer > math.MaxInt32 {
				return ErrInvalid
			}
			continue
		}
		parsed, err := value.Float64()
		if err != nil || math.IsNaN(parsed) || math.IsInf(parsed, 0) || parsed < bounds[0] || parsed > bounds[1] {
			return ErrInvalid
		}
	}
	return nil
}

func ForcedMaxOutput(d Document) (int64, error) {
	var floor int64
	for _, path := range []string{"/max_tokens", "/max_completion_tokens"} {
		raw, present := d.BodyForced.Values[path]
		if !present {
			continue
		}
		trimmed := bytes.TrimSpace(raw)
		if len(trimmed) == 0 || trimmed[0] == '"' {
			return 0, ErrInvalid
		}
		var number json.Number
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		if decoder.Decode(&number) != nil {
			return 0, ErrInvalid
		}
		value, err := strconv.ParseInt(number.String(), 10, 64)
		if err != nil || value < 1 || value > math.MaxInt32 {
			return 0, ErrInvalid
		}
		if value > floor {
			floor = value
		}
	}
	return floor, nil
}

func canonicalHeader(name string) string { return http.CanonicalHeaderKey(name) }
