package openai

import (
	"bytes"
	"encoding/json"
	"math"
)

func projectDecisionResponse(body []byte, request *DecisionRequest) ([]byte, Usage, error) {
	invalid := func() ([]byte, Usage, error) { return nil, Usage{}, errInvalidUpstreamResponse }
	if request == nil || !validateEmbeddingJSON(body) {
		return invalid()
	}
	fields, ok := borrowedObjectFields(body)
	if !ok {
		return invalid()
	}
	root := fieldsByName(fields)
	if hasUpstreamError(root) || !requiredString(root, "model", MaxUpstreamModelRunes, true) {
		return invalid()
	}
	var answers []json.RawMessage
	if json.Unmarshal(root["answers"], &answers) != nil || len(answers) != len(request.questions) {
		return invalid()
	}
	defer clearRawMessages(answers)
	for i, raw := range answers {
		if !validDecisionAnswer(raw, request.questions[i]) {
			return invalid()
		}
	}
	usage, publicUsage := responseTokenUsage(root["usage"])
	projected := map[string]any{"model": request.Model, "answers": answers}
	if usage.Present {
		projected["usage"] = publicUsage
	}
	out, err := json.Marshal(projected)
	if err != nil {
		return invalid()
	}
	return out, usage, nil
}

func validDecisionAnswer(raw []byte, q decisionQuestion) bool {
	object, ok := decisionObject(raw, "type", "name", "probability", "choice", "confidence", "probabilities", "score")
	if !ok {
		return false
	}
	name := object["name"]
	if !sameDecisionValue(name, q.name) {
		return false
	}
	kind, ok := decisionString(object["type"], 1, 32)
	if !ok {
		return false
	}
	if kind == "refusal" {
		_, ok := decisionObject(raw, "type", "name")
		return ok
	}
	if kind != q.kind {
		return false
	}
	if kind == "predicate" {
		if _, ok := decisionObject(raw, "type", "name", "probability"); !ok {
			return false
		}
		return decisionProbability(object["probability"])
	}
	if !decisionProbability(object["confidence"]) {
		return false
	}
	if kind == "choice" {
		if _, ok := decisionObject(raw, "type", "name", "choice", "confidence", "probabilities"); !ok {
			return false
		}
		found := false
		for _, value := range q.values {
			if sameDecisionValue(value, object["choice"]) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	} else {
		if _, ok := decisionObject(raw, "type", "name", "score", "confidence", "probabilities"); !ok {
			return false
		}
		value, ok := decisionNumber(object["score"])
		if !ok || value < 0 || value > float64(len(q.labels)-1) {
			return false
		}
	}
	var probabilities []json.RawMessage
	if json.Unmarshal(object["probabilities"], &probabilities) != nil {
		return false
	}
	defer clearRawMessages(probabilities)
	count := len(q.values)
	if kind == "score" {
		count = len(q.labels)
	}
	if len(probabilities) != count {
		return false
	}
	seen := make([]bool, count)
	for _, raw := range probabilities {
		keys := []string{"value", "probability"}
		if kind == "score" {
			keys = append(keys, "label")
		}
		p, ok := decisionObject(raw, keys...)
		if !ok || !decisionProbability(p["probability"]) {
			return false
		}
		index := -1
		if kind == "choice" {
			for i, value := range q.values {
				if sameDecisionValue(value, p["value"]) {
					index = i
					break
				}
			}
		} else {
			var value int64
			if isJSONNull(p["value"]) || !decodeNonNegativeInt(p["value"], &value) || value >= int64(count) {
				return false
			}
			index = int(value)
			label, ok := decisionString(p["label"], 0, 1<<20)
			if !ok || label != q.labels[index] {
				return false
			}
		}
		if index < 0 || seen[index] {
			return false
		}
		seen[index] = true
	}
	return true
}

func sameDecisionValue(a, b []byte) bool {
	if bytes.Equal(a, []byte("null")) || bytes.Equal(b, []byte("null")) {
		return bytes.Equal(a, b)
	}
	first, ok := decisionValue(a)
	if !ok {
		return false
	}
	defer clear(first)
	second, ok := decisionValue(b)
	if !ok {
		return false
	}
	defer clear(second)
	return bytes.Equal(first, second)
}
func decisionNumber(raw []byte) (float64, bool) {
	var value float64
	if len(raw) == 0 || isJSONNull(raw) || json.Unmarshal(raw, &value) != nil || math.IsNaN(value) || math.IsInf(value, 0) {
		return 0, false
	}
	return value, true
}
func decisionProbability(raw []byte) bool {
	value, ok := decisionNumber(raw)
	return ok && value >= 0 && value <= 1
}

// responseTokenUsage is shared by Decisions and Responses. Reasoning tokens
// are already included in output_tokens and must never be charged twice.
func responseTokenUsage(raw []byte) (Usage, map[string]any) {
	if len(raw) == 0 || isJSONNull(raw) || !validateEmbeddingJSON(raw) {
		return Usage{}, nil
	}
	fields, ok := borrowedObjectFields(raw)
	if !ok {
		return Usage{}, nil
	}
	root := fieldsByName(fields)
	var input, output, total, read, write, reasoning int64
	integer := func(raw []byte, dst *int64) bool { return !isJSONNull(raw) && decodeNonNegativeInt(raw, dst) }
	if !integer(root["input_tokens"], &input) || !integer(root["output_tokens"], &output) || !integer(root["total_tokens"], &total) {
		return Usage{}, nil
	}
	sum, sumOK := addChecked(input, output)
	mismatch := !sumOK || sum != total
	invalid := func() (Usage, map[string]any) { return Usage{TotalMismatch: mismatch}, nil }
	if !sumOK {
		return invalid()
	}
	if details := root["input_tokens_details"]; len(details) != 0 && !isJSONNull(details) {
		fields, ok := borrowedObjectFields(details)
		if !ok {
			return invalid()
		}
		values := fieldsByName(fields)
		if value := values["cached_tokens"]; len(value) != 0 && !integer(value, &read) {
			return invalid()
		}
		if value := values["cache_write_tokens"]; len(value) != 0 && !integer(value, &write) {
			return invalid()
		}
	}
	if details := root["output_tokens_details"]; len(details) != 0 && !isJSONNull(details) {
		fields, ok := borrowedObjectFields(details)
		if !ok {
			return invalid()
		}
		values := fieldsByName(fields)
		if value := values["reasoning_tokens"]; len(value) != 0 && !integer(value, &reasoning) {
			return invalid()
		}
	}
	cache, ok := addChecked(read, write)
	if !ok || cache > input || reasoning > output {
		return invalid()
	}
	usage := Usage{Present: true, UncachedInputTokens: input - cache, CacheReadInputTokens: read, CacheWriteInputTokens: write, OutputTokens: output, TotalMismatch: mismatch}
	return usage, map[string]any{"input_tokens": input, "output_tokens": output, "total_tokens": total,
		"input_tokens_details": map[string]int64{"cached_tokens": read, "cache_write_tokens": write}, "output_tokens_details": map[string]int64{"reasoning_tokens": reasoning}}
}

// ParseResponseUsage validates the token buckets shared by native Responses
// and Decisions without inferring consumption from the reported total.
func ParseResponseUsage(raw []byte) Usage {
	usage, _ := responseTokenUsage(raw)
	return usage
}
