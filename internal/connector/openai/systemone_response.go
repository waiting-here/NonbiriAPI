package openai

import (
	"bytes"
	"encoding/json"
	"math"
	"strconv"
)

func systemOneUsage(raw []byte) Usage {
	fields, ok := systemOneMap(raw)
	if !ok {
		return Usage{}
	}
	var input, output int64
	if isJSONNull(fields["input_tokens"]) || isJSONNull(fields["output_tokens"]) || !decodeNonNegativeInt(fields["input_tokens"], &input) || !decodeNonNegativeInt(fields["output_tokens"], &output) {
		return Usage{}
	}
	if _, ok := addChecked(input, output); !ok {
		return Usage{}
	}
	return Usage{Present: true, UncachedInputTokens: input, OutputTokens: output}
}
func systemOneNumber(raw []byte, max float64) bool {
	var n float64
	return len(raw) > 0 && !isJSONNull(raw) && json.Unmarshal(raw, &n) == nil && !math.IsInf(n, 0) && !math.IsNaN(n) && n >= 0 && n <= max
}
func systemOneProbabilities(raw []byte, keys map[string]json.RawMessage) bool {
	values, ok := systemOneMap(raw)
	if !ok || len(values) != len(keys) {
		return false
	}
	for key := range keys {
		if !systemOneNumber(values[key], 1) {
			return false
		}
	}
	return true
}
func projectSystemOneResponse(raw []byte, request *InferenceRequest) ([]byte, Usage, error) {
	if !validateEmbeddingJSON(raw) {
		return nil, Usage{}, errInvalidUpstreamResponse
	}
	root, ok := systemOneMap(raw)
	if !ok {
		return nil, Usage{}, errInvalidUpstreamResponse
	}
	usage := systemOneUsage(root["usage"])
	bad := func() ([]byte, Usage, error) { return nil, usage, errInvalidUpstreamResponse }
	if hasUpstreamError(root) {
		return bad()
	}
	if _, ok := decisionString(root["model"], 1, MaxUpstreamModelRunes); !ok {
		return bad()
	}
	questions, _ := systemOneMap(fieldsByName(request.fields)["questions"])
	answers, ok := systemOneMap(root["answers"])
	if !ok || len(answers) != len(questions) {
		return bad()
	}
	projected := map[string]any{}
	for name, rawQuestion := range questions {
		q, _ := systemOneMap(rawQuestion)
		a, ok := systemOneMap(answers[name])
		if !ok || !bytes.Equal(a["type"], q["type"]) {
			return bad()
		}
		kind, _ := decisionString(q["type"], 1, 16)
		item := map[string]json.RawMessage{"type": a["type"]}
		switch kind {
		case "noul":
			if !systemOneNumber(a["noul"], 1) {
				return bad()
			}
			item["noul"] = a["noul"]
		case "choice":
			criteria, _ := systemOneMap(q["criteria"])
			choice, ok := decisionString(a["choice"], 1, int(request.RequestBodyLimit()))
			if !ok || criteria[choice] == nil || !systemOneNumber(a["confidence"], 1) || !systemOneProbabilities(a["probabilities"], criteria) {
				return bad()
			}
			for _, key := range []string{"choice", "confidence", "probabilities"} {
				item[key] = a[key]
			}
		case "score":
			var criteria []json.RawMessage
			_ = json.Unmarshal(q["criteria"], &criteria)
			legend := map[string]json.RawMessage{}
			for i, value := range criteria {
				legend[strconv.Itoa(i)] = value
			}
			returned, ok := systemOneMap(a["legend"])
			if !ok || len(returned) != len(legend) {
				return bad()
			}
			for key, value := range legend {
				var left, right any
				_ = json.Unmarshal(value, &left)
				if json.Unmarshal(returned[key], &right) != nil {
					return bad()
				}
				l, _ := json.Marshal(left)
				r, _ := json.Marshal(right)
				if !bytes.Equal(l, r) {
					return bad()
				}
			}
			if !systemOneNumber(a["score"], float64(len(criteria)-1)) || !systemOneNumber(a["confidence"], 1) || !systemOneProbabilities(a["probabilities"], legend) {
				return bad()
			}
			for _, key := range []string{"score", "confidence", "legend", "probabilities"} {
				item[key] = a[key]
			}
		case "box":
			box, ok := decisionObject(a["box"], "top", "bottom", "left", "right")
			if !ok || len(box) != 4 || a["confidence"] == nil || !isJSONNull(a["confidence"]) && !systemOneNumber(a["confidence"], 1) {
				return bad()
			}
			for _, value := range box {
				if !systemOneNumber(value, 1) {
					return bad()
				}
			}
			item["box"] = a["box"]
			item["confidence"] = a["confidence"]
		default:
			return bad()
		}
		projected[name] = item
	}
	out := map[string]any{"model": request.Model, "answers": projected}
	if usage.Present {
		out["usage"] = map[string]int64{"input_tokens": usage.UncachedInputTokens, "output_tokens": usage.OutputTokens}
	}
	body, err := json.Marshal(out)
	return body, usage, err
}
