package openai

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"strings"

	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/requestbody"
)

// InferenceRequest owns a bounded snapshot of one direct inference protocol.
// Uploaded parts belong only to this request and are never persisted.
type InferenceRequest struct {
	fields    []jsonField
	parts     []inferencePart
	excluded  []string
	operation contract.Operation
	bodyLimit int64
	Model     string
	Stream    bool
	count     int
	documents []json.RawMessage
}

func (*InferenceRequest) String() string   { return "[redacted inference request]" }
func (*InferenceRequest) GoString() string { return "[redacted inference request]" }
func (*InferenceRequest) LogValue() slog.Value {
	return slog.StringValue("[redacted inference request]")
}
func (r *InferenceRequest) Operation() contract.Operation {
	if r == nil {
		return ""
	}
	return r.operation
}
func (r *InferenceRequest) RequestBodyLimit() int64 {
	if r == nil {
		return requestbody.DefaultBytes
	}
	return requestbody.DecoderLimit(r.bodyLimit)
}
func (r *InferenceRequest) MaxOutputTokens() int64 {
	if r == nil {
		return 0
	}
	for _, name := range []string{"max_output_tokens", "max_completion_tokens", "max_tokens"} {
		if raw, ok := r.RawField(name); ok {
			var value int64
			valid := decodeNonNegativeInt(raw, &value)
			clear(raw)
			if valid {
				return value
			}
		}
	}
	return 0
}
func (r *InferenceRequest) RawField(name string) ([]byte, bool) {
	if r != nil {
		for _, field := range r.fields {
			if field.name == name {
				return bytes.Clone(field.value), true
			}
		}
	}
	return nil, false
}
func (r *InferenceRequest) LogicalBody() ([]byte, error) {
	if r == nil {
		return nil, ErrInvalidRequest
	}
	return json.Marshal(fieldsByName(r.fields))
}
func (r *InferenceRequest) Clear() {
	if r == nil {
		return
	}
	clearFields(r.fields)
	for _, part := range r.parts {
		clear(part.data)
	}
	clearRawMessages(r.documents)
	*r = InferenceRequest{}
}
func (r *InferenceRequest) CloneForAttempt() *InferenceRequest {
	if r == nil {
		return nil
	}
	clone := *r
	clone.fields = make([]jsonField, len(r.fields))
	for i, field := range r.fields {
		clone.fields[i] = jsonField{name: field.name, value: bytes.Clone(field.value)}
	}
	clone.parts = cloneInferenceParts(r.parts)
	clone.documents = make([]json.RawMessage, len(r.documents))
	for i, document := range r.documents {
		clone.documents[i] = bytes.Clone(document)
	}
	clone.excluded = append([]string(nil), r.excluded...)
	return &clone
}
func (r *InferenceRequest) ExcludeFields(names []string) error {
	if r == nil {
		return ErrInvalidRequest
	}
	normalized, err := NormalizeExcludedRequestFields(names)
	if err != nil {
		return err
	}
	// Lifecycle controls are validated before exclusions and cannot be removed.
	for _, name := range normalized {
		if inferenceProtectedField(name) {
			return invalidField("request", "a required inference field cannot be excluded")
		}
	}
	r.excluded = normalized
	r.fields = excludeRequestFields(r.fields, normalized)
	return nil
}
func inferenceProtectedField(name string) bool {
	switch name {
	case "model", "input", "prompt", "query", "documents", "image", "images", "mask", "file", "voice", "store", "background", "previous_response_id", "conversation":
		return true
	}
	return false
}

func DecodeInferenceRequest(reader io.Reader, operation contract.Operation, limit int64) (*InferenceRequest, error) {
	return decodeInferenceRequest(reader, operation, limit, nil)
}

func decodeInferenceRequest(reader io.Reader, operation contract.Operation, limit int64, parts []inferencePart) (*InferenceRequest, error) {
	if !contract.IsHTTPInference(operation) || reader == nil {
		return nil, ErrInvalidRequest
	}
	limit = requestbody.DecoderLimit(limit)
	body, err := readBounded(reader, limit)
	if err != nil {
		return nil, err
	}
	defer clear(body)
	if !validateEmbeddingJSON(body) {
		return nil, invalidField("body", "expected one valid UTF-8 JSON object")
	}
	fields, err := decodeJSONObject(body, maxTopLevelFields)
	if err != nil {
		return nil, err
	}
	r := &InferenceRequest{fields: fields, operation: operation, bodyLimit: limit, count: 1}
	valid := false
	defer func() {
		if !valid {
			r.Clear()
		}
	}()
	object := fieldsByName(fields)
	if json.Unmarshal(object["model"], &r.Model) != nil || !validOpaqueText(r.Model, MaxPlatformModelRunes, true) {
		return nil, invalidField("model", "expected a valid model name")
	}
	if value, ok := object["stream"]; ok && (json.Unmarshal(value, &r.Stream) != nil || isJSONNull(value)) {
		return nil, invalidField("stream", "expected a boolean")
	}
	if value := object["n"]; len(value) > 0 && !isJSONNull(value) {
		var n int64
		if !decodeNonNegativeInt(value, &n) || n < 1 || n > 128 {
			return nil, invalidField("n", "expected an integer from 1 to 128")
		}
		r.count = int(n)
	}
	if err := ValidateStatelessInference(body, operation); err != nil {
		return nil, err
	}
	text := func(name string) bool { _, ok := decisionString(object[name], 1, int(limit)); return ok }
	switch operation {
	case contract.OperationResponses, contract.OperationResponsesCompact, contract.OperationResponsesInputTokens:
		if len(object["input"]) == 0 || isJSONNull(object["input"]) {
			return nil, invalidField("input", "inline input is required")
		}
		var stringInput string
		if json.Unmarshal(object["input"], &stringInput) != nil {
			var input []json.RawMessage
			if json.Unmarshal(object["input"], &input) != nil || input == nil {
				return nil, invalidField("input", "expected inline text or input items")
			}
		}
		if operation != contract.OperationResponses && r.Stream {
			return nil, invalidField("stream", "this operation does not support streaming")
		}
	case contract.OperationCompletions:
		promptCount := embeddingInputCount(object["prompt"])
		if len(object["prompt"]) == 0 || isJSONNull(object["prompt"]) || bytes.Equal(object["prompt"], []byte(`""`)) {
			promptCount = 1
		}
		if promptCount == 0 {
			return nil, invalidField("prompt", "expected text, tokens, or a nonempty batch")
		}
		r.count *= promptCount
		if r.count > 4096 {
			return nil, invalidField("n", "too many completion results")
		}
	case contract.OperationImagesEdits, contract.OperationImagesVariations:
		if operation == contract.OperationImagesEdits && !text("prompt") {
			return nil, invalidField("prompt", "a text prompt is required")
		}
		if len(parts) == 0 {
			if operation == contract.OperationImagesVariations || !validInlineImages(object["images"]) {
				return nil, invalidField("images", "inline image URLs or uploaded image parts are required")
			}
		}
		if operation == contract.OperationImagesVariations && r.Stream {
			return nil, invalidField("stream", "variations do not support streaming")
		}
	case contract.OperationAudioSpeech:
		if !text("input") || !text("voice") {
			return nil, invalidField("input", "text input and a built-in voice name are required")
		}
		if value := object["stream_format"]; len(value) > 0 {
			var format string
			if json.Unmarshal(value, &format) != nil || format != "sse" && format != "audio" {
				return nil, invalidField("stream_format", "expected audio or sse")
			}
			r.Stream = format == "sse"
		}
	case contract.OperationAudioTranscriptions, contract.OperationAudioTranslations:
		if len(parts) == 0 {
			return nil, invalidField("file", "an uploaded audio file is required")
		}
		if operation == contract.OperationAudioTranslations && r.Stream {
			return nil, invalidField("stream", "translations do not support streaming")
		}
	case contract.OperationModerations:
		if len(object["input"]) == 0 || isJSONNull(object["input"]) {
			return nil, invalidField("input", "text or image input is required")
		}
		if r.Stream {
			return nil, invalidField("stream", "moderations do not support streaming")
		}
	case contract.OperationRerank:
		if !text("query") || json.Unmarshal(object["documents"], &r.documents) != nil || len(r.documents) == 0 || len(r.documents) > 1000 {
			return nil, invalidField("documents", "a query and 1 to 1000 documents are required")
		}
		for _, document := range r.documents {
			if _, ok := decisionString(document, 0, int(limit)); !ok {
				var item map[string]json.RawMessage
				if json.Unmarshal(document, &item) != nil || item == nil {
					return nil, invalidField("documents", "expected strings or document objects")
				}
			}
		}
		if r.Stream {
			return nil, invalidField("stream", "rerank does not support streaming")
		}
		for _, name := range []string{"top_n", "top_k"} {
			if value := object[name]; len(value) > 0 && !isJSONNull(value) {
				var n int64
				if !decodeNonNegativeInt(value, &n) || n < 1 || n > int64(len(r.documents)) {
					return nil, invalidField(name, "expected a positive count no larger than documents")
				}
			}
		}
		if object["top_n"] != nil && object["top_k"] != nil {
			return nil, invalidField("top_n", "use only one result count field")
		}
		for _, name := range []string{"return_documents", "truncation", "truncate"} {
			if raw := object[name]; raw != nil && !bytes.Equal(raw, []byte("true")) && !bytes.Equal(raw, []byte("false")) {
				return nil, invalidField(name, "expected a boolean")
			}
		}
	}
	if len(parts) > 0 {
		r.parts = cloneInferenceParts(parts)
		if err := r.validateParts(); err != nil {
			return nil, err
		}
	}
	valid = true
	return r, nil
}

func validInlineImages(raw []byte) bool {
	n := 0
	return visitJSONArray(raw, func(value []byte) bool {
		var item map[string]json.RawMessage
		if json.Unmarshal(value, &item) != nil {
			return false
		}
		var url string
		if json.Unmarshal(item["image_url"], &url) != nil || !(strings.HasPrefix(url, "https://") || strings.HasPrefix(url, "http://") || strings.HasPrefix(url, "data:")) {
			return false
		}
		n++
		return n <= 128
	}) && n > 0
}

// ValidateStatelessInference is also used before exclusions and adaptations.
// Free-form text and function parameter schemas remain opaque.
func ValidateStatelessInference(body []byte, operation contract.Operation) error {
	var root map[string]json.RawMessage
	if json.Unmarshal(body, &root) != nil || root == nil {
		return ErrInvalidRequest
	}
	controls := []string{"store"}
	if operation == contract.OperationResponses || operation == contract.OperationResponsesCompact || operation == contract.OperationResponsesInputTokens {
		controls = append(controls, "background")
	}
	for _, key := range controls {
		if value := root[key]; len(value) > 0 && !isJSONNull(value) && !bytes.Equal(bytes.TrimSpace(value), []byte("false")) {
			return invalidField(key, "persistent and background requests are not supported")
		}
	}
	for _, key := range []string{"previous_response_id", "conversation", "prompt_id", "agent_id", "assistant_id", "thread_id"} {
		if value := root[key]; len(value) > 0 && !isJSONNull(value) {
			return invalidField(key, "persistent resource references are not supported")
		}
	}
	if operation == contract.OperationResponses || operation == contract.OperationResponsesCompact || operation == contract.OperationResponsesInputTokens {
		if value := root["prompt"]; len(value) > 0 && !isJSONNull(value) {
			return invalidField("prompt", "stored prompt references are not supported")
		}
	}
	for _, key := range []string{"input", "images", "image", "mask", "messages"} {
		if !statelessContent(root[key], 0) {
			return invalidField(key, "persistent resource references are not supported")
		}
	}
	if !statelessTools(root["tools"], 0) {
		return invalidField("tools", "tools requiring hosted resources are not supported")
	}
	return nil
}

func statelessTools(raw []byte, depth int) bool {
	if len(raw) == 0 || isJSONNull(raw) {
		return true
	}
	if depth > 8 {
		return false
	}
	var tools []map[string]json.RawMessage
	if json.Unmarshal(raw, &tools) != nil {
		return false
	}
	for _, tool := range tools {
		var kind string
		_ = json.Unmarshal(tool["type"], &kind)
		switch kind {
		case "function", "custom", "computer", "computer_use_preview", "local_shell", "apply_patch", "tool_search", "web_search", "web_search_2025_08_26", "web_search_preview", "web_search_preview_2025_03_11", "image_generation":
		case "namespace":
			if !statelessTools(tool["tools"], depth+1) {
				return false
			}
		case "shell":
			var environment map[string]json.RawMessage
			if json.Unmarshal(tool["environment"], &environment) != nil || !bytes.Equal(environment["type"], []byte(`"local"`)) || environment["skills"] != nil {
				return false
			}
		default:
			return false
		}
		for _, key := range []string{"file_id", "file_ids", "container_id", "vector_store_ids", "container", "skill_ids"} {
			if value := tool[key]; len(value) > 0 && !isJSONNull(value) {
				return false
			}
		}
		if kind == "image_generation" && !statelessContent(tool["input_image_mask"], 0) {
			return false
		}
	}
	return true
}

func statelessContent(raw []byte, depth int) bool {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || depth > 64 {
		return depth <= 64
	}
	if raw[0] == '[' {
		return visitJSONArray(raw, func(value []byte) bool { return statelessContent(value, depth+1) })
	}
	if raw[0] != '{' {
		return true
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(raw, &object) != nil {
		return false
	}
	var kind string
	_ = json.Unmarshal(object["type"], &kind)
	switch kind {
	case "item_reference", "file_search_call", "code_interpreter_call", "mcp_call", "mcp_list_tools", "mcp_approval_request", "mcp_approval_response":
		return false
	}
	for _, key := range []string{"file_id", "container_id", "vector_store_id", "vector_store_ids", "audio_id"} {
		if value := object[key]; len(value) > 0 && !isJSONNull(value) {
			return false
		}
	}
	if kind == "audio" && object["id"] != nil {
		return false
	}
	if kind == "tool_search_output" && !statelessTools(object["tools"], 0) {
		return false
	}
	if kind == "shell_call" && object["environment"] != nil {
		var environment map[string]json.RawMessage
		if json.Unmarshal(object["environment"], &environment) != nil || !bytes.Equal(environment["type"], []byte(`"local"`)) || environment["skills"] != nil {
			return false
		}
	}
	for _, key := range []string{"content", "output", "image_url", "image", "mask", "audio", "file"} {
		if !statelessContent(object[key], depth+1) {
			return false
		}
	}
	return true
}

// WithLogicalBody revalidates an adapted request while retaining its bounded uploads.
func (r *InferenceRequest) WithLogicalBody(body []byte) (*InferenceRequest, error) {
	if r == nil {
		return nil, ErrInvalidRequest
	}
	return decodeInferenceRequest(bytes.NewReader(body), r.operation, r.bodyLimit, r.parts)
}
