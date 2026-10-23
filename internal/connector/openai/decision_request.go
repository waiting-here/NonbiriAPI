package openai

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/waiting-here/NonbiriAPI/internal/requestbody"
)

// DecisionRequest is an independently validated, non-streaming Decisions
// request. Its evidence and questions never enter a chat transformation.
type DecisionRequest struct {
	fields    []jsonField
	bodyLimit int64
	excluded  []string
	questions []decisionQuestion
	Model     string
}

type decisionQuestion struct {
	kind   string
	name   json.RawMessage
	values []json.RawMessage
	labels []string
}

func (*DecisionRequest) String() string       { return "[redacted decision request]" }
func (*DecisionRequest) GoString() string     { return "[redacted decision request]" }
func (*DecisionRequest) LogValue() slog.Value { return slog.StringValue("[redacted decision request]") }

func DecodeDecisionRequest(reader io.Reader, limit int64) (*DecisionRequest, error) {
	limit = requestbody.DecoderLimit(limit)
	if reader == nil {
		return nil, invalidField("body", "expected one JSON object")
	}
	body, err := readBounded(reader, limit)
	if err != nil {
		return nil, err
	}
	defer clear(body)
	if !validateDecisionJSON(body) {
		return nil, invalidField("body", "expected one valid UTF-8 JSON object")
	}
	fields, err := decodeJSONObject(body, maxTopLevelFields)
	if err != nil {
		return nil, err
	}
	r := &DecisionRequest{fields: fields, bodyLimit: limit}
	valid := false
	defer func() {
		if !valid {
			r.Clear()
		}
	}()
	inputSeen := false
	images := 0
	for _, field := range fields {
		switch field.name {
		case "model":
			if json.Unmarshal(field.value, &r.Model) != nil || !validOpaqueText(r.Model, MaxPlatformModelRunes, true) {
				return nil, invalidField("model", "expected a valid model name")
			}
		case "input":
			if !validDecisionInput(field.value, &images) {
				return nil, invalidField("input", "expected supported messages or matching tool history with at most 128 images")
			}
			inputSeen = true
		case "questions":
			r.questions, err = decodeDecisionQuestions(field.value, &images)
			if err != nil {
				return nil, invalidField("questions", "expected 1 to 400 predicate, choice, or score questions within request limits")
			}
		case "stream":
			if !bytes.Equal(bytes.TrimSpace(field.value), []byte("false")) {
				return nil, invalidField("stream", "decisions do not support streaming")
			}
		case "safety_identifier":
			if !optionalDecisionString(field.value, 0, 128, true) {
				return nil, invalidField("safety_identifier", "expected a string of at most 128 characters or null")
			}
		case "prompt_cache_key":
			if !optionalDecisionString(field.value, 0, 64, true) {
				return nil, invalidField("prompt_cache_key", "expected a string of at most 64 characters or null")
			}
		case "prompt_cache_options":
			if !isJSONNull(field.value) {
				object, ok := decisionObject(field.value, "mode", "ttl")
				if !ok || !optionalDecisionEnum(object["mode"], true, "implicit", "explicit") || !optionalDecisionEnum(object["ttl"], false, "30m") {
					return nil, invalidField("prompt_cache_options", "unsupported cache options")
				}
			}
		default:
			return nil, invalidField("request", "unsupported decisions field")
		}
	}
	if r.Model == "" {
		return nil, invalidField("model", "required field is missing")
	}
	if !inputSeen {
		return nil, invalidField("input", "required field is missing")
	}
	if len(r.questions) == 0 {
		return nil, invalidField("questions", "required field is missing")
	}
	valid = true
	return r, nil
}

func decisionObject(raw []byte, keys ...string) (map[string]json.RawMessage, bool) {
	fields, ok := borrowedObjectFields(raw)
	if !ok {
		return nil, false
	}
	object := fieldsByName(fields)
	for key := range object {
		found := false
		for _, allowed := range keys {
			if key == allowed {
				found = true
				break
			}
		}
		if !found {
			return nil, false
		}
	}
	return object, true
}

func decisionString(raw []byte, min, max int) (string, bool) {
	var s string
	if len(raw) == 0 || raw[0] != '"' || json.Unmarshal(raw, &s) != nil {
		return "", false
	}
	n := utf8.RuneCountInString(s)
	return s, n >= min && n <= max
}

func optionalDecisionString(raw []byte, min, max int, nullable bool) bool {
	if len(raw) == 0 || nullable && isJSONNull(raw) {
		return true
	}
	_, ok := decisionString(raw, min, max)
	return ok
}

func optionalDecisionEnum(raw []byte, nullable bool, values ...string) bool {
	if len(raw) == 0 || nullable && isJSONNull(raw) {
		return true
	}
	s, ok := decisionString(raw, 0, 128)
	if ok {
		for _, value := range values {
			if s == value {
				return true
			}
		}
	}
	return false
}

func decisionValue(raw []byte) (json.RawMessage, bool) {
	if bytes.Equal(raw, []byte("true")) || bytes.Equal(raw, []byte("false")) {
		return append(json.RawMessage(nil), raw...), true
	}
	s, ok := decisionString(raw, 0, 1<<20)
	if !ok {
		return nil, false
	}
	encoded, _ := json.Marshal(s)
	return encoded, true
}

func decodeDecisionQuestions(raw []byte, images *int) ([]decisionQuestion, error) {
	var questions []decisionQuestion
	ok := visitJSONArray(raw, func(item []byte) bool {
		if len(questions) >= 400 {
			return false
		}
		object, ok := decisionObject(item, "type", "instructions", "name", "choices", "levels")
		if !ok {
			return false
		}
		kind, ok := decisionString(object["type"], 1, 32)
		if !ok {
			return false
		}
		if !validDecisionInstructions(object["instructions"], images) || !optionalDecisionString(object["name"], 0, 1<<20, false) {
			return false
		}
		q := decisionQuestion{kind: kind, name: json.RawMessage("null")}
		if name, present := object["name"]; present {
			q.name = append(json.RawMessage(nil), name...)
		}
		switch kind {
		case "predicate":
			if object["choices"] != nil || object["levels"] != nil {
				return false
			}
		case "choice":
			if object["levels"] != nil {
				return false
			}
			seen := map[string]bool{}
			ok = visitJSONArray(object["choices"], func(raw []byte) bool {
				choice, ok := decisionObject(raw, "value", "description")
				if !ok || !optionalDecisionString(choice["description"], 0, 1<<20, false) {
					return false
				}
				value, ok := decisionValue(choice["value"])
				if !ok || seen[string(value)] || len(q.values) >= 255 {
					return false
				}
				seen[string(value)] = true
				q.values = append(q.values, value)
				return true
			})
			if !ok || len(q.values) < 2 {
				return false
			}
		case "score":
			if object["choices"] != nil {
				return false
			}
			ok = visitJSONArray(object["levels"], func(raw []byte) bool {
				level, ok := decisionObject(raw, "label", "description")
				if !ok || !optionalDecisionString(level["description"], 0, 1<<20, false) {
					return false
				}
				label, ok := decisionString(level["label"], 0, 1<<20)
				if !ok || len(q.labels) >= 10 {
					return false
				}
				q.labels = append(q.labels, label)
				return true
			})
			if !ok || len(q.labels) < 2 {
				return false
			}
		default:
			return false
		}
		questions = append(questions, q)
		return true
	})
	if !ok || len(questions) == 0 {
		return nil, ErrInvalidRequest
	}
	return questions, nil
}

func validDecisionInstructions(raw []byte, images *int) bool {
	if _, ok := decisionString(raw, 0, 1<<20); ok {
		return true
	}
	parts := 0
	return visitJSONArray(raw, func(part []byte) bool {
		parts++
		return validDecisionPart(part, "question", images)
	}) && parts > 0
}

func validDecisionInput(raw []byte, images *int) bool {
	if _, ok := decisionString(raw, 0, 10<<20); ok {
		return true
	}
	calls := map[string]string{}
	outputs := map[string]bool{}
	return visitJSONArray(raw, func(raw []byte) bool {
		object, ok := decisionObject(raw, "type", "role", "content", "phase", "arguments", "call_id", "name", "id", "namespace", "status", "input", "output")
		if !ok {
			return false
		}
		kind := "message"
		if object["type"] != nil {
			kind, ok = decisionString(object["type"], 1, 32)
			if !ok {
				return false
			}
		}
		if kind == "message" {
			if _, ok := decisionObject(raw, "type", "role", "content", "phase"); !ok {
				return false
			}
			role, ok := decisionString(object["role"], 1, 16)
			if !ok || role != "user" && role != "developer" && role != "system" && role != "assistant" {
				return false
			}
			if !optionalDecisionEnum(object["phase"], true, "commentary", "final_answer") || role != "assistant" && len(object["phase"]) != 0 && !isJSONNull(object["phase"]) {
				return false
			}
			return validDecisionContent(object["content"], role, images)
		}
		if !optionalDecisionString(object["id"], 0, 1<<20, true) || !optionalDecisionEnum(object["status"], true, "in_progress", "completed", "incomplete") {
			return false
		}
		id, ok := decisionString(object["call_id"], 1, 64)
		if !ok {
			return false
		}
		switch kind {
		case "function_call", "custom_tool_call":
			payload := "arguments"
			maxName := 128
			if kind == "custom_tool_call" {
				payload, maxName = "input", 64
			}
			if _, ok := decisionObject(raw, "type", "call_id", "name", "id", "namespace", "status", payload); !ok {
				return false
			}
			if _, ok := decisionString(object["name"], 1, maxName); !ok {
				return false
			}
			if _, ok := decisionString(object[payload], 0, 1<<20); !ok || !optionalDecisionString(object["namespace"], 1, 64, true) || calls[id] != "" {
				return false
			}
			calls[id] = kind
			return true
		case "function_call_output", "custom_tool_call_output":
			if _, ok := decisionObject(raw, "type", "call_id", "output", "id", "status"); !ok {
				return false
			}
			if calls[id] != strings.TrimSuffix(kind, "_output") || outputs[id] {
				return false
			}
			outputs[id] = true
			return validDecisionContent(object["output"], "tool", images)
		default:
			return false
		}
	})
}

func validDecisionContent(raw []byte, role string, images *int) bool {
	if _, ok := decisionString(raw, 0, 10<<20); ok {
		return true
	}
	return visitJSONArray(raw, func(part []byte) bool {
		return validDecisionPart(part, role, images)
	})
}

func validDecisionPart(raw []byte, role string, images *int) bool {
	object, ok := decisionObject(raw, "type", "text", "refusal", "image_url", "detail", "data", "prompt_cache_breakpoint")
	if !ok {
		return false
	}
	kind, ok := decisionString(object["type"], 1, 32)
	if !ok {
		return false
	}
	keys := []string{"type"}
	if role != "question" && (kind == "input_text" || kind == "input_image") {
		keys = append(keys, "prompt_cache_breakpoint")
	}
	if breakpoint := object["prompt_cache_breakpoint"]; len(breakpoint) != 0 && !isJSONNull(breakpoint) {
		bp, ok := decisionObject(breakpoint, "mode")
		if !ok || !bytes.Equal(bp["mode"], []byte(`"explicit"`)) {
			return false
		}
	}
	switch kind {
	case "input_text":
		if role == "assistant" {
			return false
		}
		if _, ok := decisionObject(raw, append(keys, "text")...); !ok {
			return false
		}
		_, ok = decisionString(object["text"], 0, 10<<20)
		return ok
	case "output_text", "refusal":
		if role != "assistant" {
			return false
		}
		field := "text"
		if kind == "refusal" {
			field = "refusal"
		}
		if _, ok := decisionObject(raw, "type", field); !ok {
			return false
		}
		_, ok = decisionString(object[field], 0, 10<<20)
		return ok
	case "input_image":
		if role != "user" && role != "tool" && role != "question" || !optionalDecisionEnum(object["detail"], true, "low", "high", "auto", "original") {
			return false
		}
		if _, ok := decisionObject(raw, append(keys, "image_url", "detail")...); !ok {
			return false
		}
		value, ok := decisionString(object["image_url"], 1, 1<<30)
		if !ok {
			return false
		}
		parsed, err := url.Parse(value)
		if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http" && parsed.Scheme != "data") || parsed.Scheme != "data" && (parsed.Hostname() == "" || parsed.User != nil) {
			return false
		}
		*images++
		return *images <= 128
	case "input_json":
		if role != "user" && role != "question" {
			return false
		}
		if _, ok := decisionObject(raw, "type", "data"); !ok {
			return false
		}
		return validDecisionJSON(object["data"])
	default:
		return false
	}
}

func (r *DecisionRequest) RequestBodyLimit() int64 {
	if r == nil {
		return requestbody.DefaultBytes
	}
	return requestbody.DecoderLimit(r.bodyLimit)
}
func (r *DecisionRequest) LogicalBody() ([]byte, error) {
	if r == nil {
		return nil, ErrInvalidRequest
	}
	return json.Marshal(fieldsByName(r.fields))
}
func (r *DecisionRequest) RawField(name string) ([]byte, bool) {
	if r != nil {
		for _, field := range r.fields {
			if field.name == name {
				return append([]byte(nil), field.value...), true
			}
		}
	}
	return nil, false
}
func (r *DecisionRequest) Clear() {
	if r == nil {
		return
	}
	clearFields(r.fields)
	for _, question := range r.questions {
		clear(question.name)
		for _, value := range question.values {
			clear(value)
		}
	}
	*r = DecisionRequest{}
}
func (r *DecisionRequest) CloneForAttempt() *DecisionRequest {
	if r == nil {
		return nil
	}
	clone := &DecisionRequest{
		Model: r.Model, bodyLimit: r.bodyLimit,
		fields:    make([]jsonField, len(r.fields)),
		questions: make([]decisionQuestion, len(r.questions)),
		excluded:  append([]string(nil), r.excluded...),
	}
	for i, field := range r.fields {
		clone.fields[i] = jsonField{name: field.name, value: append(json.RawMessage(nil), field.value...)}
	}
	for i, question := range r.questions {
		clone.questions[i] = decisionQuestion{
			kind: question.kind, name: append(json.RawMessage(nil), question.name...),
			values: make([]json.RawMessage, len(question.values)),
			labels: append([]string(nil), question.labels...),
		}
		for j, value := range question.values {
			clone.questions[i].values[j] = append(json.RawMessage(nil), value...)
		}
	}
	return clone
}
func (r *DecisionRequest) ExcludeFields(names []string) error {
	if r == nil {
		return ErrInvalidRequest
	}
	fields, err := NormalizeExcludedRequestFields(names)
	if err != nil {
		return err
	}
	r.excluded = fields
	r.fields = excludeRequestFields(r.fields, fields)
	return nil
}
func (r *DecisionRequest) marshalUpstream(model, identifier string) ([]byte, error) {
	if r == nil || !validOpaqueText(model, MaxUpstreamModelRunes, true) || !validSafetyIdentifier(identifier) {
		return nil, ErrInvalidRequest
	}
	object := fieldsByName(r.fields)
	delete(object, "stream")
	object["model"], _ = json.Marshal(model)
	object["safety_identifier"], _ = json.Marshal(identifier)
	body, err := json.Marshal(object)
	if int64(len(body)) > r.RequestBodyLimit()+(16<<10) {
		clear(body)
		return nil, ErrPayloadTooLarge
	}
	return body, err
}
