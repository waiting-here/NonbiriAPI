package openai

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"math"
	"net/http"

	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
)

const systemOneImagesHeader = "TypeSafe-Danger-Zone-Images"
const systemOneLongHeader = "TypeSafe-Danger-Zone-Long-Context"

type systemOneOptions struct{ images, longContext, invalid bool }

// SetOptionHeaders retains only the two explicit System One opt-ins. Invalid
// values are refused during protocol preflight, after the original client audit.
func (r *InferenceRequest) SetOptionHeaders(headers http.Header) {
	if r == nil || r.operation != contract.OperationSystemOne {
		return
	}
	r.systemOneOptions = systemOneOptions{}
	for name, dst := range map[string]*bool{systemOneImagesHeader: &r.systemOneOptions.images, systemOneLongHeader: &r.systemOneOptions.longContext} {
		values := headers.Values(name)
		if len(values) == 0 {
			continue
		}
		if len(values) != 1 || values[0] != "true" {
			r.systemOneOptions.invalid = true
			continue
		}
		*dst = true
	}
}

func (r *InferenceRequest) checkSystemOneOptions() error {
	object := fieldsByName(r.fields)
	needImages := object["assets"] != nil
	var questions map[string]map[string]json.RawMessage
	_ = json.Unmarshal(object["questions"], &questions)
	for _, q := range questions {
		if bytes.Equal(q["type"], []byte(`"box"`)) {
			needImages = true
		}
	}
	if r.systemOneOptions.invalid || needImages && !r.systemOneOptions.images {
		return invalidField("headers", "System One option headers require a single true value; images and box questions require the images opt-in")
	}
	return nil
}
func (r *InferenceRequest) applyOptionHeaders(headers http.Header) {
	if r.operation != contract.OperationSystemOne {
		return
	}
	headers.Del(systemOneImagesHeader)
	headers.Del(systemOneLongHeader)
	if r.systemOneOptions.images {
		headers.Set(systemOneImagesHeader, "true")
	}
	if r.systemOneOptions.longContext {
		headers.Set(systemOneLongHeader, "true")
	}
}

func structuredSystemOne(raw []byte, nullable bool) bool {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || isJSONNull(raw) {
		return nullable
	}
	if raw[0] != '"' && raw[0] != '{' && raw[0] != '[' {
		return false
	}
	var value any
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if d.Decode(&value) != nil {
		return false
	}
	return finiteSystemOne(value, 0)
}
func finiteSystemOne(value any, depth int) bool {
	if depth > 64 {
		return false
	}
	switch v := value.(type) {
	case json.Number:
		n, err := v.Float64()
		return err == nil && !math.IsInf(n, 0) && !math.IsNaN(n)
	case map[string]any:
		for _, item := range v {
			if !finiteSystemOne(item, depth+1) {
				return false
			}
		}
	case []any:
		for _, item := range v {
			if !finiteSystemOne(item, depth+1) {
				return false
			}
		}
	}
	return true
}
func systemOneMap(raw []byte) (map[string]json.RawMessage, bool) {
	fields, ok := borrowedObjectFields(raw)
	if !ok {
		return nil, false
	}
	return fieldsByName(fields), true
}
func validateSystemOne(object map[string]json.RawMessage, parts []inferencePart) error {
	bad := func(field string) error { return invalidField(field, "invalid System One request field") }
	for name := range object {
		if name != "model" && name != "state" && name != "questions" && name != "assets" {
			return bad(name)
		}
	}
	if !structuredSystemOne(object["state"], false) {
		return bad("state")
	}
	questions, ok := systemOneMap(object["questions"])
	if !ok || len(questions) == 0 {
		return bad("questions")
	}
	for name, raw := range questions {
		if name == "" {
			return bad("questions")
		}
		q, valid := decisionObject(raw, "type", "instructions", "criteria")
		if !valid || !structuredSystemOne(q["instructions"], true) {
			return bad("questions")
		}
		kind, _ := decisionString(q["type"], 1, 16)
		switch kind {
		case "noul", "choice":
			if kind == "noul" && (q["criteria"] == nil || isJSONNull(q["criteria"])) {
				continue
			}
			criteria, valid := systemOneMap(q["criteria"])
			if !valid || kind == "choice" && (len(criteria) < 1 || len(criteria) > 255) {
				return bad("questions")
			}
			for key, criterion := range criteria {
				if key == "" || kind == "noul" && key != "true" && key != "false" || !structuredSystemOne(criterion, true) {
					return bad("questions")
				}
			}
		case "score":
			var criteria []json.RawMessage
			if json.Unmarshal(q["criteria"], &criteria) != nil || len(criteria) < 1 || len(criteria) > 10 {
				return bad("questions")
			}
			for _, criterion := range criteria {
				if !structuredSystemOne(criterion, false) {
					return bad("questions")
				}
			}
		case "box":
			if q["criteria"] != nil {
				return bad("questions")
			}
		default:
			return bad("questions")
		}
	}
	uploads := map[string]bool{}
	for _, part := range parts {
		if part.name == "request" || part.name == "" || len(part.data) == 0 || uploads[part.name] {
			return bad("assets")
		}
		uploads[part.name] = true
	}
	used := map[string]bool{}
	if raw := object["assets"]; raw != nil {
		assets, valid := systemOneMap(raw)
		if !valid || len(assets) == 0 {
			return bad("assets")
		}
		for name, asset := range assets {
			a, valid := decisionObject(asset, "type", "format", "data_base64", "part")
			if name == "" || !valid || a["type"] != nil && !bytes.Equal(a["type"], []byte(`"image"`)) {
				return bad("assets")
			}
			if ref, exists := a["part"]; exists {
				part, valid := decisionString(ref, 1, 256)
				if !valid || !uploads[part] || used[part] {
					return bad("assets")
				}
				used[part] = true
			} else {
				format, _ := decisionString(a["format"], 1, 4)
				encoded, valid := decisionString(a["data_base64"], 1, int(DefaultMaxImageResponseBytes))
				if !valid || format != "png" && format != "jpg" && format != "jpeg" {
					return bad("assets")
				}
				data, err := base64.StdEncoding.Strict().DecodeString(encoded)
				size := len(data)
				clear(data)
				if err != nil || size == 0 {
					return bad("assets")
				}
			}
		}
	}
	if len(used) != len(uploads) {
		return bad("assets")
	}
	return nil
}
