package openai

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/url"
	"strings"
)

const DefaultMaxImageResponseBytes int64 = 64 << 20

// projectImageResponse retains standard image fields without fetching URLs or
// retaining the prompt, image contents, or vendor-only response metadata.
func projectImageResponse(raw []byte, request *ImageRequest, stream bool) ([]byte, Usage, bool, error) {
	invalid := func() ([]byte, Usage, bool, error) { return nil, Usage{}, false, errInvalidUpstreamResponse }
	if request == nil || int64(len(raw)) > DefaultMaxImageResponseBytes || !validateEmbeddingJSON(raw) {
		return invalid()
	}
	fields, ok := borrowedObjectFields(raw)
	if !ok {
		return invalid()
	}
	root := fieldsByName(fields)
	if hasUpstreamError(root) {
		return invalid()
	}
	output := map[string]json.RawMessage{}
	completed := false
	if stream {
		var kind string
		if json.Unmarshal(root["type"], &kind) != nil || kind != "image_generation.partial_image" && kind != "image_generation.completed" {
			return invalid()
		}
		completed = kind == "image_generation.completed"
		if !validImageBase64(root["b64_json"]) {
			return invalid()
		}
		output["type"], output["b64_json"] = root["type"], root["b64_json"]
		if !completed {
			var index int64
			if !decodeNonNegativeInt(root["partial_image_index"], &index) {
				return invalid()
			}
			output["partial_image_index"] = root["partial_image_index"]
		}
		if len(root["created_at"]) != 0 {
			var created int64
			if !decodeNonNegativeInt(root["created_at"], &created) {
				return invalid()
			}
			output["created_at"] = root["created_at"]
		}
	} else {
		var created int64
		if !decodeNonNegativeInt(root["created"], &created) {
			return invalid()
		}
		output["created"] = root["created"]
		images := make([]map[string]json.RawMessage, 0, request.Count)
		if !visitJSONArray(root["data"], func(raw []byte) bool {
			if len(images) >= request.Count {
				return false
			}
			fields, ok := borrowedObjectFields(raw)
			if !ok {
				return false
			}
			values := fieldsByName(fields)
			image := map[string]json.RawMessage{}
			if value := values["b64_json"]; len(value) > 0 && !isJSONNull(value) {
				if !validImageBase64(value) {
					return false
				}
				image["b64_json"] = value
			}
			if value := values["url"]; len(value) > 0 && !isJSONNull(value) {
				var text string
				if json.Unmarshal(value, &text) != nil || len(text) > 16384 {
					return false
				}
				parsed, err := url.Parse(text)
				if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" {
					return false
				}
				image["url"] = value
			}
			if len(image) == 0 {
				return false
			}
			if value := values["revised_prompt"]; len(value) > 0 && !isJSONNull(value) {
				var text string
				if json.Unmarshal(value, &text) != nil {
					return false
				}
				image["revised_prompt"] = value
			}
			images = append(images, image)
			return true
		}) || len(images) != request.Count {
			return invalid()
		}
		data, err := json.Marshal(images)
		if err != nil {
			return invalid()
		}
		defer clear(data)
		output["data"] = data
	}
	for _, name := range []string{"background", "output_format", "quality", "size"} {
		if value := root[name]; len(value) > 0 && !isJSONNull(value) {
			var text string
			if json.Unmarshal(value, &text) != nil || !validOpaqueText(text, 512, false) {
				return invalid()
			}
			output[name] = value
		}
	}
	usage := imageUsage(root["usage"])
	if usage.Present {
		output["usage"] = projectImageUsage(root["usage"])
	}
	projected, err := json.Marshal(output)
	if err != nil || int64(len(projected)) > DefaultMaxImageResponseBytes {
		clear(projected)
		return invalid()
	}
	return projected, usage, completed, nil
}

func validImageBase64(raw []byte) bool {
	if len(raw) < 3 || raw[0] != '"' || raw[len(raw)-1] != '"' {
		return false
	}
	var reader io.Reader = bytes.NewReader(raw[1 : len(raw)-1])
	if bytes.ContainsRune(raw, '\\') {
		var value string
		if json.Unmarshal(raw, &value) != nil || strings.ContainsAny(value, "\r\n") {
			return false
		}
		reader = strings.NewReader(value)
	}
	count, err := io.Copy(io.Discard, base64.NewDecoder(base64.StdEncoding.Strict(), reader))
	return err == nil && count > 0
}

func projectImageUsage(raw []byte) json.RawMessage {
	fields, _ := borrowedObjectFields(raw)
	root := fieldsByName(fields)
	output := map[string]json.RawMessage{}
	for _, name := range []string{"input_tokens", "output_tokens", "total_tokens"} {
		output[name] = root[name]
	}
	for _, name := range []string{"input_tokens_details", "output_tokens_details"} {
		fields, ok := borrowedObjectFields(root[name])
		if !ok {
			continue
		}
		detail := map[string]json.RawMessage{}
		for _, field := range fields {
			var value int64
			if (field.name == "image_tokens" || field.name == "text_tokens") && decodeNonNegativeInt(field.value, &value) {
				detail[field.name] = field.value
			}
		}
		if len(detail) != 0 {
			output[name], _ = json.Marshal(detail)
		}
	}
	projected, _ := json.Marshal(output)
	return projected
}

func imageUsage(raw []byte) Usage {
	if len(raw) == 0 || isJSONNull(raw) {
		return Usage{}
	}
	fields, ok := borrowedObjectFields(raw)
	if !ok {
		return Usage{}
	}
	root := fieldsByName(fields)
	var input, output, total int64
	if !decodeNonNegativeInt(root["input_tokens"], &input) || !decodeNonNegativeInt(root["output_tokens"], &output) || !decodeNonNegativeInt(root["total_tokens"], &total) {
		return Usage{}
	}
	sum, ok := addChecked(input, output)
	if !ok {
		return Usage{TotalMismatch: true}
	}
	return Usage{Present: true, UncachedInputTokens: input, OutputTokens: output, TotalMismatch: sum != total}
}
