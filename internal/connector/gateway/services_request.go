package gateway

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"

	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/connector/openai"
	"github.com/waiting-here/NonbiriAPI/internal/requestadaptation"
)

func specificationVersion(kind contract.Type) string {
	if kind == contract.TypeAISDKGatewayV4 {
		return "4"
	}
	return "3"
}

func encodeV4PromptFiles(prompt []any) {
	for _, rawMessage := range prompt {
		message, ok := rawMessage.(map[string]any)
		if !ok {
			continue
		}
		parts, _ := message["content"].([]any)
		for _, rawPart := range parts {
			part, ok := rawPart.(map[string]any)
			if !ok || part["type"] != "file" {
				continue
			}
			value, ok := part["data"].(string)
			if !ok {
				continue
			}
			if strings.HasPrefix(value, "data:") {
				_, encoded, _ := strings.Cut(value, ",")
				part["data"] = map[string]string{"type": "data", "data": encoded}
			} else {
				part["data"] = map[string]string{"type": "url", "url": value}
			}
		}
	}
}

func SupportsEmbeddingForType(kind contract.Type, request *openai.EmbeddingRequest) bool {
	body, err := compileEmbeddingForType(kind, request, "")
	clear(body)
	return err == nil
}

func CheckEmbeddingTarget(target contract.Target, request *openai.EmbeddingRequest, policy contract.AttemptPolicy) error {
	body, err := compileEmbeddingPolicy(target.Type(), request, "", policy.NativeExtensions)
	clear(body)
	return err
}

func compileEmbeddingPolicy(kind contract.Type, request *openai.EmbeddingRequest, attribution string, native map[string]json.RawMessage) ([]byte, error) {
	body, err := compileEmbeddingForType(kind, request, attribution)
	if err != nil {
		return nil, err
	}
	return mergeServiceNative(body, native, "/values", "/dimensions")
}

func SupportsImage(request *openai.ImageRequest) bool {
	body, err := compileImage(request, "")
	clear(body)
	return err == nil
}

func CheckImageTarget(target contract.Target, request *openai.ImageRequest, policy contract.AttemptPolicy) error {
	body, err := compileImagePolicy(target.Type(), request, "", policy.NativeExtensions)
	clear(body)
	return err
}

func compileImagePolicy(kind contract.Type, request *openai.ImageRequest, attribution string, native map[string]json.RawMessage) ([]byte, error) {
	if !kind.AISDKGateway() {
		return nil, reject("native compile", "type", "expected a Gateway endpoint")
	}
	body, err := compileImage(request, attribution)
	if err != nil {
		return nil, err
	}
	return mergeServiceNative(body, native, "/prompt", "/n", "/size", "/seed", "/aspectRatio")
}

func compileImage(request *openai.ImageRequest, attribution string) ([]byte, error) {
	if request == nil || request.Count < 1 || request.Count > 10 {
		return nil, reject("native compile", "request", "expected an image generation request")
	}
	if request.Stream {
		return nil, reject("native compile", "stream", "Gateway image generation does not support streaming")
	}
	output := map[string]any{"n": request.Count}
	for _, name := range request.TopLevelFields() {
		raw, _ := request.RawField(name)
		err := compileImageField(output, name, raw)
		clear(raw)
		if err != nil {
			return nil, err
		}
	}
	if _, ok := output["prompt"]; !ok {
		return nil, reject("native compile", "prompt", "a nonempty prompt is required")
	}
	addAttribution(output, attribution)
	return json.Marshal(output)
}

func compileImageField(output map[string]any, name string, raw []byte) error {
	switch name {
	case "model", "safety_identifier":
	case "prompt":
		value, ok := text(raw)
		if !ok || value == "" {
			return reject("native compile", name, "expected a nonempty string")
		}
		output[name] = value
	case "n":
		if !isNull(raw) && !number(raw, 1, 10, true) {
			return reject("native compile", name, "expected an image count from 1 to 10")
		}
	case "size", "aspect_ratio":
		if isNull(raw) {
			return nil
		}
		value, ok := text(raw)
		separator, field := "x", "size"
		if name == "aspect_ratio" {
			separator, field = ":", "aspectRatio"
		}
		if !ok || !positivePair(value, separator) {
			return reject("native compile", name, "expected two positive integers separated by "+separator)
		}
		output[field] = value
	case "seed":
		if isNull(raw) {
			return nil
		}
		if !number(raw, -9007199254740991, 9007199254740991, true) {
			return reject("native compile", name, "expected a safely representable integer")
		}
		output[name] = json.RawMessage(bytes.Clone(raw))
	case "response_format":
		if isNull(raw) {
			return nil
		}
		value, ok := text(raw)
		if !ok || value != "b64_json" {
			return reject("native compile", name, "Gateway images require b64_json")
		}
	case "stream":
		if !isNull(raw) && !bytes.Equal(raw, []byte("false")) {
			return reject("native compile", name, "Gateway image generation does not support streaming")
		}
	default:
		return reject("native compile", name, "unsupported Gateway image field")
	}
	return nil
}

func positivePair(value, separator string) bool {
	left, right, ok := strings.Cut(value, separator)
	if !ok {
		return false
	}
	for _, part := range []string{left, right} {
		if part == "" {
			return false
		}
		for _, digit := range part {
			if digit < '0' || digit > '9' {
				return false
			}
		}
		n, err := strconv.ParseUint(part, 10, 32)
		if err != nil || n == 0 {
			return false
		}
	}
	return true
}

func SupportsInference(request *openai.InferenceRequest) bool {
	body, err := compileRerank(request, "")
	clear(body)
	return err == nil
}

func CheckInferenceTarget(target contract.Target, request *openai.InferenceRequest, policy contract.AttemptPolicy) error {
	body, err := compileRerankPolicy(target.Type(), request, "", policy.NativeExtensions)
	clear(body)
	return err
}

func compileRerankPolicy(kind contract.Type, request *openai.InferenceRequest, attribution string, native map[string]json.RawMessage) ([]byte, error) {
	if !kind.AISDKGateway() {
		return nil, reject("native compile", "type", "expected a Gateway endpoint")
	}
	body, err := compileRerank(request, attribution)
	if err != nil {
		return nil, err
	}
	return mergeServiceNative(body, native, "/documents", "/query", "/topN")
}

func rerankDocuments(request *openai.InferenceRequest) ([]json.RawMessage, string, error) {
	raw, _ := request.RawField("documents")
	defer clear(raw)
	var documents []json.RawMessage
	if json.Unmarshal(raw, &documents) != nil || len(documents) == 0 || len(documents) > 1000 {
		return nil, "", reject("native compile", "documents", "expected 1 to 1000 documents")
	}
	kind := ""
	for _, document := range documents {
		next := "text"
		if _, ok := text(document); !ok {
			if _, err := parseObject(document); err != nil {
				clearDocuments(documents)
				return nil, "", reject("native compile", "documents", "expected strings or document objects")
			}
			next = "object"
		}
		if kind != "" && next != kind {
			clearDocuments(documents)
			return nil, "", reject("native compile", "documents", "mixed text and object documents cannot be represented")
		}
		kind = next
	}
	return documents, kind, nil
}

func clearDocuments(documents []json.RawMessage) {
	for _, document := range documents {
		clear(document)
	}
}

func compileRerank(request *openai.InferenceRequest, attribution string) ([]byte, error) {
	if request == nil || request.Operation() != contract.OperationRerank {
		return nil, reject("native compile", "operation", "only rerank is supported by this entry point")
	}
	if request.Stream {
		return nil, reject("native compile", "stream", "rerank does not support streaming")
	}
	raw, err := request.LogicalBody()
	if err != nil {
		return nil, err
	}
	defer clear(raw)
	fields, err := parseObject(raw)
	if err != nil {
		return nil, err
	}
	documents, kind, err := rerankDocuments(request)
	if err != nil {
		return nil, err
	}
	defer clearDocuments(documents)
	output := map[string]any{"documents": map[string]any{"type": kind, "values": documents}}
	for name, value := range fields {
		switch name {
		case "model", "documents":
		case "query":
			query, ok := text(value)
			if !ok || query == "" {
				return nil, reject("native compile", name, "expected a nonempty query")
			}
			output[name] = query
		case "top_n":
			if !isNull(value) {
				if !number(value, 1, float64(len(documents)), true) {
					return nil, reject("native compile", name, "expected a count no larger than documents")
				}
				output["topN"] = value
			}
		case "return_documents":
			if !bytes.Equal(value, []byte("true")) && !bytes.Equal(value, []byte("false")) {
				return nil, reject("native compile", name, "expected a boolean")
			}
		case "stream":
			if !bytes.Equal(value, []byte("false")) {
				return nil, reject("native compile", name, "rerank does not support streaming")
			}
		default:
			return nil, reject("native compile", name, "unsupported Gateway rerank field")
		}
	}
	if _, ok := output["query"]; !ok {
		return nil, reject("native compile", "query", "a query is required")
	}
	addAttribution(output, attribution)
	return json.Marshal(output)
}

func addAttribution(output map[string]any, attribution string) {
	if attribution != "" {
		output["providerOptions"] = map[string]any{"gateway": map[string]string{"user": attribution}}
	}
}

// Explicit native extensions cannot replace fields whose caller semantics have
// already been checked. Provider options remain available to controlled policy.
func mergeServiceNative(body []byte, native map[string]json.RawMessage, protected ...string) ([]byte, error) {
	if len(native) == 0 {
		return body, nil
	}
	for path := range native {
		for _, field := range protected {
			if path == field || strings.HasPrefix(path, field+"/") || strings.HasPrefix(field, path+"/") {
				clear(body)
				return nil, reject("native adaptation", field, "native extension conflicts with translated input")
			}
		}
	}
	merged, err := requestadaptation.MergeNative(body, native, maxJSONBytes)
	clear(body)
	return merged, err
}
