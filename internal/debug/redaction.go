package debug

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"strings"
	"unicode/utf8"
)

const (
	maxJSONDepth  = 64
	maxJSONFields = 1024
)

var (
	redactedKeyNames = map[string]struct{}{
		"authorization": {}, "proxy_authorization": {}, "cookie": {}, "set_cookie": {},
		"x_api_key": {}, "api_key": {}, "apikey": {}, "secret": {}, "client_secret": {},
		"access_token": {}, "refresh_token": {}, "caller_key": {}, "ciphertext": {},
		"safety_identifier": {}, "hmac": {}, "hmac_value": {},
		"signature": {}, "base_url": {}, "baseurl": {}, "origin": {},
		"endpoint_id": {}, "endpoint_key_id": {}, "binding_id": {}, "donation_id": {},
		"donation_key_id": {}, "physical_id": {}, "upstream_model_id": {},
	}
	textBearerPattern = regexp.MustCompile(`(?i)\bBearer\s+[^\s,;]+`)
	textCallerPattern = regexp.MustCompile(`\bnbk_[A-Za-z0-9_-]+`)
	textKeyPattern    = regexp.MustCompile(`(?i)["']?(authorization|proxy[-_]?authorization|cookie|set[-_]?cookie|x[-_]?api[-_]?key|api[_-]?key|secret|client[_-]?secret|access[_-]?token|refresh[_-]?token|caller[-_]?key|ciphertext|safety[-_]?identifier|hmac(?:[-_]?value)?|signature|base[-_]?url|origin|(?:endpoint|endpoint[-_]?key|binding|donation|donation[-_]?key|physical)[-_]?id|upstream[-_]?model[-_]?id)["']?\s*[:=]\s*(?:"[^"]*"|'[^']*'|[^,;\s}\]]+)`)
	textHMACPattern   = regexp.MustCompile(`(?i)\b(?:hmac|sha(?:1|224|256|384|512)|signature)\s*[-_:=]\s*[^\s,;]+`)
)

type orderedMember struct {
	name  string
	value *orderedValue
}

type orderedValue struct {
	kind  byte // object, array, string, number, bool, null
	text  string
	obj   []orderedMember
	items []*orderedValue
}

// Projection is a bounded, redacted JSON/text result.  Data is safe to place
// into a trace; on parse/size failure it contains only metadata, never source
// bytes.
type Projection struct {
	Data          []byte
	Truncated     bool
	OriginalBytes int
	CapturedBytes int
	ParseCategory string
}

// RedactJSON parses one JSON value while preserving object member order,
// rejects duplicate members and excessive nesting, and replaces sensitive
// values before encoding.  Invalid or over-limit input is represented only by
// bounded parse metadata.
func RedactJSON(raw []byte, maxBytes int) Projection {
	if maxBytes <= 0 {
		maxBytes = MaxEventBytes
	}
	p := Projection{OriginalBytes: len(raw)}
	if !utf8.Valid(raw) {
		return redactionMetadata(p, "invalid_utf8", len(raw) > maxBytes, maxBytes)
	}
	if len(raw) > maxBytes {
		return redactionMetadata(p, "size_limit", true, maxBytes)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	value, fields, err := parseOrdered(dec, 0)
	if err != nil || value == nil || fields > maxJSONFields {
		category := "invalid"
		if errors.Is(err, errJSONDepth) {
			category = "depth_limit"
		} else if errors.Is(err, errJSONFields) {
			category = "field_limit"
		}
		return redactionMetadata(p, category, false, maxBytes)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return redactionMetadata(p, "trailing_value", false, maxBytes)
	}
	redactOrdered(value)
	encoded := encodeOrdered(value)
	if len(encoded) > maxBytes {
		return redactionMetadata(p, "size_limit", true, maxBytes)
	}
	p.Data = encoded
	p.CapturedBytes = len(encoded)
	p.ParseCategory = "valid"
	return p
}

// RedactText applies the same secret/token patterns to non-JSON caller-visible
// text, strips controls, repairs no invalid bytes, and returns metadata instead
// of retaining unbounded text.
func RedactText(raw []byte, maxBytes int) Projection {
	if maxBytes <= 0 {
		maxBytes = MaxResponseBytes
	}
	p := Projection{OriginalBytes: len(raw)}
	if !utf8.Valid(raw) {
		return redactionMetadata(p, "invalid_utf8", true, maxBytes)
	}
	if jsonProjection := RedactJSON(raw, maxBytes); jsonProjection.ParseCategory == "valid" {
		return jsonProjection
	} else {
		trimmed := bytes.TrimSpace(raw)
		if len(trimmed) != 0 && (trimmed[0] == '{' || trimmed[0] == '[') {
			// Anything that presents as JSON but could not be fully inspected
			// must never fall through to raw text capture: malformed or nested
			// secret values may otherwise be retained in the trace.
			return jsonProjection
		}
	}
	if projection, ok := redactSSE(raw, maxBytes); ok {
		return projection
	}
	text := string(raw)
	text = textBearerPattern.ReplaceAllString(text, "Bearer [REDACTED]")
	text = textCallerPattern.ReplaceAllString(text, "[REDACTED]")
	text = textKeyPattern.ReplaceAllString(text, "$1=[REDACTED]")
	text = textHMACPattern.ReplaceAllString(text, "[REDACTED]")
	text = stripControls(text)
	if len(text) > maxBytes {
		text = truncateUTF8(text, maxBytes)
		p.Truncated = true
	}
	p.Data = []byte(text)
	p.CapturedBytes = len(p.Data)
	p.ParseCategory = "text"
	return p
}

func parseOrdered(dec *json.Decoder, depth int) (*orderedValue, int, error) {
	if depth > maxJSONDepth {
		return nil, 0, errJSONDepth
	}
	token, err := dec.Token()
	if err != nil {
		return nil, 0, err
	}
	value := &orderedValue{}
	// Count every parsed value node, including array elements. A limit that
	// counts only object members lets a large flat array retain an unbounded
	// number of heap nodes while still looking like a small JSON document.
	fields := 1
	switch token := token.(type) {
	case json.Delim:
		switch token {
		case '{':
			value.kind = '{'
			seen := make(map[string]struct{})
			for dec.More() {
				nameToken, err := dec.Token()
				if err != nil {
					return nil, fields, err
				}
				name, ok := nameToken.(string)
				if !ok {
					return nil, fields, errors.New("object key")
				}
				if _, exists := seen[name]; exists {
					return nil, fields, errors.New("duplicate object key")
				}
				seen[name] = struct{}{}
				fields++ // object member key/value node
				if fields > maxJSONFields {
					return nil, fields, errJSONFields
				}
				child, childFields, err := parseOrdered(dec, depth+1)
				if err != nil {
					return nil, fields + childFields, err
				}
				fields += childFields
				if fields > maxJSONFields {
					return nil, fields, errJSONFields
				}
				value.obj = append(value.obj, orderedMember{name: name, value: child})
			}
			end, err := dec.Token()
			if err != nil || end != json.Delim('}') {
				return nil, fields, errors.New("object terminator")
			}
		case '[':
			value.kind = '['
			for dec.More() {
				child, childFields, err := parseOrdered(dec, depth+1)
				if err != nil {
					return nil, fields + childFields, err
				}
				fields += childFields
				if fields > maxJSONFields {
					return nil, fields, errJSONFields
				}
				value.items = append(value.items, child)
			}
			end, err := dec.Token()
			if err != nil || end != json.Delim(']') {
				return nil, fields, errors.New("array terminator")
			}
		default:
			return nil, fields, errors.New("unexpected delimiter")
		}
	case string:
		value.kind = 's'
		value.text = token
	case json.Number:
		value.kind = 'n'
		value.text = token.String()
	case bool:
		value.kind = 'b'
		if token {
			value.text = "true"
		} else {
			value.text = "false"
		}
	case nil:
		value.kind = '0'
		value.text = "null"
	default:
		return nil, fields, errors.New("unexpected token")
	}
	return value, fields, nil
}

var (
	errJSONDepth  = errors.New("json depth limit")
	errJSONFields = errors.New("json field limit")
)

func redactOrdered(value *orderedValue) {
	if value == nil {
		return
	}
	switch value.kind {
	case '{':
		for index := range value.obj {
			if _, sensitive := redactedKeyNames[normalizeRedactionKey(value.obj[index].name)]; sensitive {
				value.obj[index].value = &orderedValue{kind: 's', text: "[REDACTED]"}
				continue
			}
			redactOrdered(value.obj[index].value)
		}
	case '[':
		for _, item := range value.items {
			redactOrdered(item)
		}
	case 's':
		value.text = redactStringValue(value.text)
	}
}

// redactEffectiveJSON is the narrow exception for system-built body-free
// effective summaries. Generic JSON still redacts origin/safety_identifier as
// sensitive values, but these two exact state-only objects are safe to show
// because they contain no URL, physical id, HMAC, or caller value. Any extra
// member or unknown state falls back to the ordinary sensitive-key marker.
func redactEffectiveJSON(raw []byte, maxBytes int) []byte {
	if maxBytes <= 0 {
		maxBytes = MaxEffectiveSummaryBytes
	}
	if !utf8.Valid(raw) || len(raw) > maxBytes {
		return RedactJSON(raw, maxBytes).Data
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	value, fields, err := parseOrdered(dec, 0)
	if err != nil || value == nil || fields > maxJSONFields {
		return RedactJSON(raw, maxBytes).Data
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return RedactJSON(raw, maxBytes).Data
	}
	redactEffectiveOrdered(value)
	encoded := encodeOrdered(value)
	if len(encoded) > maxBytes {
		return redactionMetadata(Projection{OriginalBytes: len(raw)}, "size_limit", true, maxBytes).Data
	}
	return encoded
}

func redactEffectiveOrdered(value *orderedValue) {
	if value == nil || value.kind != '{' {
		redactOrdered(value)
		return
	}
	for index := range value.obj {
		member := &value.obj[index]
		switch normalizeRedactionKey(member.name) {
		case "origin":
			if safeStateObject(member.value, false) || safeCallerEffectiveObject(member.value, false) {
				continue
			}
			member.value = &orderedValue{kind: 's', text: "[REDACTED]"}
		case "safety_identifier":
			if safeStateObject(member.value, true) || safeCallerEffectiveObject(member.value, true) {
				continue
			}
			member.value = &orderedValue{kind: 's', text: "[REDACTED]"}
		case "store", "flatten_tool_calls":
			if safeCallerEffectiveObject(member.value, false) {
				continue
			}
			// These policy fields have one frozen live wire shape. An unknown
			// state or extra member is not a partially trusted projection: hide
			// the complete value so future additions cannot accidentally leak
			// caller/effective details through the generic recursive redactor.
			member.value = &orderedValue{kind: 's', text: "[REDACTED]"}
		default:
			if _, sensitive := redactedKeyNames[normalizeRedactionKey(member.name)]; sensitive {
				member.value = &orderedValue{kind: 's', text: "[REDACTED]"}
				continue
			}
			redactOrdered(member.value)
		}
	}
}

var safeEffectiveStates = map[string]struct{}{
	"not_selected_not_evaluated": {}, "evaluated": {}, "default": {},
	"caller_value": {}, "forced_false": {}, "applied": {}, "not_applied": {},
	"unchanged": {}, "hidden": {}, "unknown": {},
}

var safePairStates = map[string]struct{}{
	"applied": {}, "unchanged": {}, "hidden": {},
}

func safeStateObject(value *orderedValue, allowValueHidden bool) bool {
	if value == nil || value.kind != '{' {
		return false
	}
	hasState, hasHidden := false, false
	for _, member := range value.obj {
		switch normalizeRedactionKey(member.name) {
		case "state":
			if hasState || member.value == nil || member.value.kind != 's' {
				return false
			}
			if _, ok := safeEffectiveStates[member.value.text]; !ok {
				return false
			}
			hasState = true
		case "value_hidden":
			if !allowValueHidden || hasHidden || member.value == nil || member.value.kind != 'b' || member.value.text != "true" {
				return false
			}
			hasHidden = true
		default:
			return false
		}
	}
	return hasState && (!allowValueHidden || hasHidden)
}

// safeCallerEffectiveObject admits the system-built pair used by live
// policy projections. Both members remain state-only; sensitive pairs also
// require value_hidden:true on both sides. This lets the wire explain whether
// a policy was applied without exposing a caller store/safety value.
func safeCallerEffectiveObject(value *orderedValue, sensitive bool) bool {
	if value == nil || value.kind != '{' || len(value.obj) != 2 {
		return false
	}
	seenCaller, seenEffective := false, false
	for _, member := range value.obj {
		if member.value == nil {
			return false
		}
		switch normalizeRedactionKey(member.name) {
		case "caller_value":
			if seenCaller || !safeCallerValue(member.value, sensitive) {
				return false
			}
			seenCaller = true
		case "effective":
			if seenEffective || !safeEffectiveStateObject(member.value, sensitive) {
				return false
			}
			seenEffective = true
		default:
			return false
		}
	}
	return seenCaller && seenEffective
}

func safeCallerValue(value *orderedValue, sensitive bool) bool {
	if value == nil {
		return false
	}
	if sensitive {
		// A hidden safety identifier has no caller value at all. Keeping this
		// exact null shape prevents a redaction marker from being mistaken for
		// an actual identifier.
		return value.kind == '0'
	}
	return value.kind == '0' || value.kind == 'b'
}

func safeEffectiveStateObject(value *orderedValue, sensitive bool) bool {
	if value == nil || value.kind != '{' {
		return false
	}
	hasState, hasHidden, hasValue := false, false, false
	for _, member := range value.obj {
		switch normalizeRedactionKey(member.name) {
		case "state":
			if hasState || member.value == nil || member.value.kind != 's' {
				return false
			}
			if _, ok := safePairStates[member.value.text]; !ok {
				return false
			}
			hasState = true
		case "value_hidden":
			if !sensitive || hasHidden || member.value == nil || member.value.kind != 'b' || member.value.text != "true" {
				return false
			}
			hasHidden = true
		case "value":
			if sensitive || hasValue || member.value == nil || (member.value.kind != '0' && member.value.kind != 'b') {
				return false
			}
			hasValue = true
		default:
			return false
		}
	}
	return hasState && (!sensitive || hasHidden)
}

func normalizeRedactionKey(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, "-", "_")
	return value
}

// redactStringValue closes the gap where a secret-shaped token appears in a
// normal (non-sensitive-key) string value. Key-based replacement remains the
// primary defense, but bearer/caller/HMAC patterns are sink-independent.
func redactStringValue(value string) string {
	value = textBearerPattern.ReplaceAllString(value, "Bearer [REDACTED]")
	value = textCallerPattern.ReplaceAllString(value, "[REDACTED]")
	value = textKeyPattern.ReplaceAllString(value, "$1=[REDACTED]")
	value = textHMACPattern.ReplaceAllString(value, "[REDACTED]")
	return value
}

// redactSSE recognizes the plain-text framing used by streaming responses and
// redacts JSON carried by each data line independently. A complete SSE stream
// is not itself JSON, so passing it through RedactJSON would otherwise leave a
// nested secret-bearing data object to the text regexes. Invalid data lines
// that look like JSON become bounded parse metadata rather than retaining
// source bytes.
func redactSSE(raw []byte, maxBytes int) (Projection, bool) {
	if len(raw) == 0 || !bytes.Contains(raw, []byte("data:")) {
		return Projection{}, false
	}
	text := string(raw)
	lines := strings.Split(text, "\n")
	hasData := false
	for index, line := range lines {
		trimmed := strings.TrimLeft(line, " \t")
		if !strings.HasPrefix(trimmed, "data:") {
			continue
		}
		hasData = true
		prefixLength := len(line) - len(trimmed)
		prefix := line[:prefixLength] + "data:"
		data := strings.TrimSpace(strings.TrimPrefix(trimmed, "data:"))
		if data == "" {
			lines[index] = prefix
			continue
		}
		if data == "[DONE]" {
			// This exact OpenAI stream terminator is protocol metadata, not a
			// JSON array. Preserve it so a debug preview can still prove the
			// connector reached its terminal sentinel.
			lines[index] = prefix + " [DONE]"
			continue
		}
		projection := RedactJSON([]byte(data), maxBytes)
		if projection.ParseCategory == "valid" {
			lines[index] = prefix + " " + string(projection.Data)
			continue
		}
		if first := data[0]; first == '{' || first == '[' {
			lines[index] = prefix + " " + string(projection.Data)
			continue
		}
		lines[index] = prefix + " " + redactStringValue(data)
	}
	if !hasData {
		return Projection{}, false
	}
	text = redactStringValue(strings.Join(lines, "\n"))
	text = stripControls(text)
	projection := Projection{OriginalBytes: len(raw), ParseCategory: "text"}
	if len(text) > maxBytes {
		text = truncateUTF8(text, maxBytes)
		projection.Truncated = true
	}
	projection.Data = []byte(text)
	projection.CapturedBytes = len(projection.Data)
	return projection, true
}

func encodeOrdered(value *orderedValue) []byte {
	if value == nil {
		return []byte("null")
	}
	var out bytes.Buffer
	encodeOrderedInto(&out, value)
	return out.Bytes()
}

func encodeOrderedInto(out *bytes.Buffer, value *orderedValue) {
	switch value.kind {
	case '{':
		out.WriteByte('{')
		for index, member := range value.obj {
			if index > 0 {
				out.WriteByte(',')
			}
			encodedName, _ := json.Marshal(member.name)
			out.Write(encodedName)
			out.WriteByte(':')
			encodeOrderedInto(out, member.value)
		}
		out.WriteByte('}')
	case '[':
		out.WriteByte('[')
		for index, item := range value.items {
			if index > 0 {
				out.WriteByte(',')
			}
			encodeOrderedInto(out, item)
		}
		out.WriteByte(']')
	case 's':
		encoded, _ := json.Marshal(value.text)
		out.Write(encoded)
	case 'n', 'b', '0':
		out.WriteString(value.text)
	default:
		out.WriteString("null")
	}
}

func redactionMetadata(p Projection, category string, truncated bool, maxBytes int) Projection {
	p.Truncated = truncated
	p.ParseCategory = category
	marker := map[string]any{
		"parse_category": category,
		"original_bytes": p.OriginalBytes,
		"captured_bytes": 0,
	}
	if truncated {
		marker["truncated"] = true
	}
	data, _ := json.Marshal(marker)
	if len(data) > maxBytes {
		// This marker is deliberately constant-size, but keep the fallback valid
		// JSON if a future caller supplies an unusually tiny limit.
		data = []byte(`{"truncated":true}`)
	}
	p.Data = data
	p.CapturedBytes = len(data)
	return p
}

func stripControls(value string) string {
	var out strings.Builder
	out.Grow(len(value))
	for _, r := range value {
		if (r < 0x20 && r != '\t' && r != '\n' && r != '\r') || r == 0x7f {
			continue
		}
		out.WriteRune(r)
	}
	return out.String()
}

func truncateUTF8(value string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	if len(value) <= maxBytes {
		return value
	}
	cut := maxBytes
	for cut > 0 && !utf8.ValidString(value[:cut]) {
		cut--
	}
	if cut <= 0 {
		return ""
	}
	return value[:cut]
}

func rawProjection(raw []byte, maxBytes int) []byte {
	return RedactJSON(raw, maxBytes).Data
}

func buildTracePayload(input TraceInput) map[string]any {
	mode := input.Mode
	if mode != ModeDry && mode != ModeLive {
		mode = ModeDry
	}
	payload := map[string]any{
		"request_id": safeIdentifier(input.RequestID, maxRequestIdentifierBytes),
		"mode":       mode,
		"model":      safeModel(input.Model, 512),
		"terminal":   safeTerminal(input.Terminal),
	}
	if input.ReceivedAt > 0 {
		payload["received_at"] = input.ReceivedAt
	}
	if input.Personal {
		payload["route"] = "personal"
	} else if input.Charity {
		payload["route"] = "charity"
	} else {
		payload["route"] = "unknown"
	}
	if len(input.RawRequest) != 0 {
		payload["raw_request"] = json.RawMessage(rawProjection(input.RawRequest, MaxRawRequestBytes))
		if object, ok := parseProjectionObject(input.RawRequest); ok {
			payload["parameters"] = boundedParameters(parameterProjection(object))
			var messages, tools []byte
			if field, found := objectField(object, "messages"); found {
				messages = encodeOrdered(field)
			}
			if field, found := objectField(object, "tools"); found {
				tools = encodeOrdered(field)
			}
			setMessagesTools(payload, messages, tools)
		}
	}
	if len(input.Messages) != 0 || len(input.Tools) != 0 {
		setMessagesTools(payload, input.Messages, input.Tools)
	}
	if len(input.Parameters) != 0 {
		payload["parameters"] = boundedJSONValue(input.Parameters, MaxParametersBytes)
	}
	if len(input.Effective) != 0 {
		payload["effective"] = json.RawMessage(redactEffectiveJSON(input.Effective, MaxEffectiveSummaryBytes))
	} else if mode == ModeDry {
		payload["effective"] = map[string]any{
			"connector": map[string]any{"state": "not_selected_not_evaluated"},
			"key":       map[string]any{"state": "not_selected_not_evaluated"},
			"store":     map[string]any{"state": "not_selected_not_evaluated"},
			"origin":    map[string]any{"state": "not_selected_not_evaluated"},
		}
	}
	if status := safeInt(input.Status); status != 0 {
		payload["status"] = status
	}
	if input.ContentType != "" {
		payload["content_type"] = safeContentType(input.ContentType)
	}
	if input.DebugModeHeader == "dry-run" {
		payload["debug_mode"] = input.DebugModeHeader
	}
	if len(input.Response) != 0 {
		payload["response"] = responseProjectionValue(input.Response, MaxResponseBytes, input.ResponseOriginalBytes, input.ResponseCapturedBytes, input.ResponseTruncated)
	}
	if input.Usage != nil && mode == ModeLive {
		payload["usage"] = input.Usage
	}
	if category := safeErrorCategory(input.ErrorCategory); category != "" {
		payload["error"] = category
	}
	if input.Truncated {
		payload["truncated"] = true
	}
	if input.Incomplete {
		payload["incomplete"] = true
	}
	if input.Dropped != 0 {
		payload["dropped"] = input.Dropped
	}
	return payload
}

func safeTerminal(value string) string {
	value = safeIdentifier(value, 64)
	switch value {
	case "received", "validated", "routing", "dispatching", "streaming", "completed",
		"dry_completed", "failed", "cancelled", "incomplete":
		return value
	default:
		return "incomplete"
	}
}

func safeErrorCategory(value string) string {
	value = strings.ToLower(stripControls(truncateUTF8(value, 128)))
	switch value {
	case "", "invalid_request", "payload_too_large", "caller_cancelled", "upstream",
		"internal", "sink", "unknown":
		return value
	default:
		return "internal"
	}
}

func parseProjectionObject(raw []byte) (*orderedValue, bool) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	value, _, err := parseOrdered(dec, 0)
	if err != nil || value == nil || value.kind != '{' {
		return nil, false
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, false
	}
	redactOrdered(value)
	return value, true
}

func objectField(value *orderedValue, name string) (*orderedValue, bool) {
	if value == nil || value.kind != '{' {
		return nil, false
	}
	for _, member := range value.obj {
		if member.name == name {
			return member.value, true
		}
	}
	return nil, false
}

func parameterProjection(value *orderedValue) []map[string]any {
	if value == nil || value.kind != '{' {
		return nil
	}
	parameters := make([]map[string]any, 0, len(value.obj))
	seen := make(map[string]struct{}, len(value.obj))
	for _, member := range value.obj {
		if member.name == "messages" || member.name == "tools" {
			continue
		}
		seen[member.name] = struct{}{}
		item := map[string]any{"name": member.name, "presence": presence(member.value)}
		item["value"] = json.RawMessage(encodeOrdered(member.value))
		parameters = append(parameters, item)
	}
	// Keep a stable vocabulary for the common OpenAI controls. An omitted
	// parameter is useful debug information, but its absence must never be
	// confused with JSON null, false, zero, or an empty value.
	for _, name := range knownParameterNames {
		if _, ok := seen[name]; ok {
			continue
		}
		parameters = append(parameters, map[string]any{"name": name, "presence": "absent"})
	}
	return parameters
}

var knownParameterNames = []string{
	"model", "stream", "temperature", "top_p", "max_tokens", "max_completion_tokens",
	"n", "stop", "presence_penalty", "frequency_penalty", "logit_bias", "user",
	"tool_choice", "response_format", "seed", "store", "safety_identifier", "stream_options",
	"parallel_tool_calls", "reasoning_effort", "reasoning", "reasoning_tokens", "reasoning_summary",
}

func boundedParameters(parameters []map[string]any) any {
	if len(parameters) == 0 {
		return []map[string]any{}
	}
	encoded, err := json.Marshal(parameters)
	if err != nil {
		return json.RawMessage(`{"parse_category":"marshal_error","captured_bytes":0}`)
	}
	return boundedJSONValue(encoded, MaxParametersBytes)
}

func boundedJSONValue(raw []byte, maxBytes int) any {
	projection := RedactJSON(raw, maxBytes)
	return json.RawMessage(projection.Data)
}

func setMessagesTools(payload map[string]any, messages, tools []byte) {
	if len(messages) == 0 && len(tools) == 0 {
		return
	}
	remaining := MaxMessagesToolsBytes
	if len(messages) != 0 {
		payload["messages"] = boundedJSONValueWithBudget(messages, &remaining)
	}
	if len(tools) != 0 {
		payload["tools"] = boundedJSONValueWithBudget(tools, &remaining)
	}
}

func boundedJSONValueWithBudget(raw []byte, remaining *int) any {
	if remaining == nil || *remaining <= 0 {
		return json.RawMessage(redactionMetadata(Projection{OriginalBytes: len(raw)}, "size_limit", true, MaxMessagesToolsBytes).Data)
	}
	budget := *remaining
	projection := RedactJSON(raw, budget)
	// Consume the caller-visible input budget, not merely the smaller redacted
	// output. This prevents two individually legal fields from bypassing the
	// frozen aggregate messages+tools cap.
	if len(raw) >= budget {
		*remaining = 0
	} else {
		*remaining -= len(raw)
	}
	return json.RawMessage(projection.Data)
}

func presence(value *orderedValue) string {
	if value == nil {
		return "absent"
	}
	switch value.kind {
	case '0':
		return "null"
	case 'b':
		if value.text == "false" {
			return "false"
		}
		return "true"
	case 'n':
		if isZeroNumber(value.text) {
			return "zero"
		}
	case 's':
		if value.text == "" {
			return "empty_string"
		}
	case '[':
		if len(value.items) == 0 {
			return "empty_array"
		}
	case '{':
		if len(value.obj) == 0 {
			return "empty_object"
		}
	}
	return "value"
}

var zeroNumberPattern = regexp.MustCompile(`^[+-]?0+(?:\.0*)?(?:[eE][+-]?[0-9]+)?$`)

func isZeroNumber(value string) bool { return zeroNumberPattern.MatchString(value) }

func safeContentType(raw string) string {
	value := strings.TrimSpace(strings.ToLower(raw))
	if len(value) > 128 || strings.ContainsAny(value, "\r\n\x00") {
		return ""
	}
	if index := strings.IndexByte(value, ';'); index >= 0 {
		value = strings.TrimSpace(value[:index])
	}
	return value
}

func safeIdentifier(value string, max int) string {
	if max <= 0 {
		return ""
	}
	if len(value) > max || !utf8.ValidString(value) {
		return ""
	}
	for _, r := range value {
		if !(r == '-' || r == '_' || r == '.' || r == ':' || r == '/' || r == ' ' ||
			(r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')) {
			return ""
		}
	}
	return value
}

// safeModel preserves the caller-visible model namespace, including the
// Unicode [公益] marker, while still rejecting controls, invalid UTF-8, and
// overlong values. It is intentionally less restrictive than safeIdentifier:
// model names are opaque protocol identifiers, not ASCII event IDs.
func safeModel(value string, max int) string {
	if max <= 0 || len(value) > max || !utf8.ValidString(value) {
		return ""
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f || r == '\u2028' || r == '\u2029' {
			return ""
		}
	}
	return value
}

func textProjectionValue(raw []byte, maxBytes int) any {
	projection := RedactText(raw, maxBytes)
	if projection.ParseCategory == "text" {
		// Plain text and SSE frames are not JSON. Keeping them as a JSON string
		// avoids making the enclosing trace object unmarshalable.
		return string(projection.Data)
	}
	return json.RawMessage(projection.Data)
}

func responseProjectionValue(raw []byte, maxBytes, originalBytes, capturedBytes int, forcedTruncated bool) any {
	projection := RedactText(raw, maxBytes)
	if originalBytes <= 0 {
		originalBytes = len(raw)
	}
	if capturedBytes <= 0 {
		capturedBytes = len(raw)
	}
	truncated := forcedTruncated || projection.Truncated || originalBytes > capturedBytes || originalBytes > maxBytes
	var value any
	if projection.ParseCategory == "text" {
		value = string(projection.Data)
	} else {
		value = json.RawMessage(projection.Data)
	}
	if !truncated {
		return value
	}
	// A plain/SSE projection normally has a string value. Once the caller
	// visible response exceeded the capture limit, wrap that value with the
	// section-level accounting required by the wire contract. JSON parse
	// metadata remains a JSON object in data and is never expanded back into
	// captured source bytes.
	return map[string]any{
		"data":           value,
		"truncated":      true,
		"original_bytes": originalBytes,
		"captured_bytes": capturedBytes,
	}
}

func safeInt(value int) int {
	if value < 100 || value > 999 {
		return 0
	}
	return value
}

func marshalJSON(value any) []byte {
	data, err := json.Marshal(value)
	if err != nil {
		return []byte(`{"parse_category":"marshal_error","captured_bytes":0}`)
	}
	return data
}

// mergeTraceProjection combines two already-redacted top-level trace
// projections. It is used only for request-scoped observer upserts; each
// field remains bounded by the original projection and the merged result is
// fail-closed if the aggregate trace budget is exceeded.
func mergeTraceProjection(previous, update []byte) []byte {
	var prior map[string]json.RawMessage
	var next map[string]json.RawMessage
	if json.Unmarshal(previous, &prior) != nil || json.Unmarshal(update, &next) != nil || prior == nil || next == nil {
		return update
	}
	for key, value := range next {
		prior[key] = value
	}
	// A wrapper terminal projection is the logical request authority. It is
	// merged with the observer's body-free effective summary, but transient
	// attempt state must not survive a successful logical completion as a stale
	// upstream error. Usage, when present from the connector observer, remains
	// valid caller-safe metadata; an explicit null from a later attempt clears
	// an earlier retry's usage before the terminal update arrives.
	if raw := next["terminal"]; len(raw) != 0 {
		var terminal string
		if json.Unmarshal(raw, &terminal) == nil && (terminal == "completed" || terminal == "dry_completed") {
			delete(prior, "error")
			delete(prior, "attempt_state")
		}
	}
	merged, err := json.Marshal(prior)
	if err != nil {
		return update
	}
	if len(merged) > MaxTraceBytes {
		return RedactJSON(merged, MaxTraceBytes).Data
	}
	return merged
}

func preserveTerminalProjection(previous, merged []byte) []byte {
	var prior, next map[string]json.RawMessage
	if json.Unmarshal(previous, &prior) != nil || json.Unmarshal(merged, &next) != nil || prior == nil || next == nil {
		return merged
	}
	var terminal string
	if raw := prior["terminal"]; len(raw) != 0 {
		_ = json.Unmarshal(raw, &terminal)
	}
	if !isLogicalTerminal(terminal) {
		return merged
	}
	// Once the wrapper has published a logical terminal projection, a late
	// observer is allowed to fill only body-free safe metadata that the wrapper
	// cannot know itself. Explicit nulls and transient attempt state are never
	// applied, and logical terminal/status/response/error fields remain exactly
	// as published by the wrapper.
	allowed := map[string]struct{}{
		"effective": {}, "attempt_index": {}, "commit_stage": {},
	}
	for key := range allowed {
		if value, ok := next[key]; ok {
			if string(bytes.TrimSpace(value)) == "null" {
				continue
			}
			prior[key] = value
		}
	}
	// A failed/cancelled/incomplete logical request may finish its bounded
	// observer drain after the wrapper has published the terminal projection.
	// Permit only the category from the newest attempt, never an older silent
	// retry. Completed and dry_completed deliberately take the stricter path
	// above and can never acquire an old failure category.
	if terminal == "failed" || terminal == "cancelled" || terminal == "incomplete" {
		if category, ok := safeJSONCategory(next["error"]); ok {
			priorAttempt := jsonAttemptIndex(prior["attempt_index"])
			nextAttempt := jsonAttemptIndex(next["attempt_index"])
			if nextAttempt >= priorAttempt {
				encodedCategory, _ := json.Marshal(category)
				prior["error"] = json.RawMessage(encodedCategory)
				if state, ok := safeJSONTerminal(next["attempt_state"]); ok && state != "completed" && state != "dry_completed" {
					encodedState, _ := json.Marshal(state)
					prior["attempt_state"] = json.RawMessage(encodedState)
				}
			}
		}
	}
	if value, ok := next["usage"]; ok && string(bytes.TrimSpace(value)) != "null" {
		prior["usage"] = value
	}
	encoded, err := json.Marshal(prior)
	if err != nil || len(encoded) > MaxTraceBytes {
		return previous
	}
	return encoded
}

func jsonAttemptIndex(raw json.RawMessage) int {
	if len(raw) == 0 {
		return -1
	}
	var value int
	if err := json.Unmarshal(raw, &value); err != nil || value < 0 || value > 1_000_000 {
		return -1
	}
	return value
}

func safeJSONCategory(raw json.RawMessage) (string, bool) {
	if len(raw) == 0 || string(bytes.TrimSpace(raw)) == "null" {
		return "", false
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", false
	}
	value = safeErrorCategory(value)
	return value, value != ""
}

func safeJSONTerminal(raw json.RawMessage) (string, bool) {
	if len(raw) == 0 {
		return "", false
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", false
	}
	value = safeTerminal(value)
	return value, true
}

func isLogicalTerminal(value string) bool {
	switch value {
	case "completed", "dry_completed", "failed", "cancelled", "incomplete":
		return true
	default:
		return false
	}
}
