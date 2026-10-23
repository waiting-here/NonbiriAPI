package openai

import (
	"encoding/json"

	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
)

// The public rerank wire uses results/index/relevance_score and platform model
// identity. Provider-specific usage is interpreted only for the selected type.
func projectRerankResponse(raw []byte, request *InferenceRequest, kind contract.Type) ([]byte, Usage, error) {
	invalid := func() ([]byte, Usage, error) { return nil, Usage{}, errInvalidUpstreamResponse }
	if !validateEmbeddingJSON(raw) {
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
	key := "results"
	if kind == contract.TypeVoyageRerank {
		key = "data"
	}
	var rows []map[string]json.RawMessage
	if json.Unmarshal(root[key], &rows) != nil || rows == nil || len(rows) == 0 || len(rows) > len(request.documents) {
		return invalid()
	}
	seen := map[int64]bool{}
	results := make([]map[string]any, len(rows))
	returnDocuments := false
	rawReturn, _ := request.RawField("return_documents")
	_ = json.Unmarshal(rawReturn, &returnDocuments)
	clear(rawReturn)
	for i, row := range rows {
		var index int64
		if !decodeNonNegativeInt(row["index"], &index) || isJSONNull(row["index"]) || index >= int64(len(request.documents)) || seen[index] {
			return invalid()
		}
		seen[index] = true
		score, ok := decisionNumber(row["relevance_score"])
		if !ok {
			return invalid()
		}
		result := map[string]any{"index": index, "relevance_score": score}
		if returnDocuments {
			var text string
			if json.Unmarshal(request.documents[index], &text) == nil {
				result["document"] = map[string]string{"text": text}
			} else {
				result["document"] = request.documents[index]
			}
		}
		if spans := row["relevant_spans"]; kind == contract.TypeCohereRerank && len(spans) > 0 && validJSONArray(spans) {
			result["relevant_spans"] = spans
		}
		results[i] = result
	}
	usage := Usage{}
	switch kind {
	case contract.TypeCohereRerank:
		var meta struct {
			BilledUnits map[string]json.RawMessage `json:"billed_units"`
		}
		if json.Unmarshal(root["meta"], &meta) == nil {
			usage.SearchUnitsMicros, usage.SearchUnitsPresent = contract.ParseSearchUnits(meta.BilledUnits["search_units"])
		}
	case contract.TypeSiliconFlowRerank:
		var tokens map[string]json.RawMessage
		if json.Unmarshal(root["tokens"], &tokens) == nil {
			var input, output int64
			if !isJSONNull(tokens["input_tokens"]) && !isJSONNull(tokens["output_tokens"]) && decodeNonNegativeInt(tokens["input_tokens"], &input) && decodeNonNegativeInt(tokens["output_tokens"], &output) {
				if _, ok := addChecked(input, output); ok {
					usage = Usage{Present: true, UncachedInputTokens: input, OutputTokens: output}
				}
			}
		}
	default:
		var tokens map[string]json.RawMessage
		if json.Unmarshal(root["usage"], &tokens) == nil {
			var total int64
			if !isJSONNull(tokens["total_tokens"]) && decodeNonNegativeInt(tokens["total_tokens"], &total) {
				usage = Usage{Present: true, UncachedInputTokens: total}
			}
		}
	}
	public := map[string]any{"object": "list", "model": request.Model, "results": results}
	publicUsage := map[string]any{}
	if usage.Present {
		publicUsage["prompt_tokens"] = usage.UncachedInputTokens
		publicUsage["completion_tokens"] = usage.OutputTokens
		publicUsage["total_tokens"] = usage.UncachedInputTokens + usage.OutputTokens
	}
	if usage.SearchUnitsPresent {
		publicUsage["search_units"] = json.Number(formatSearchUnits(usage.SearchUnitsMicros))
	}
	if len(publicUsage) > 0 {
		public["usage"] = publicUsage
	}
	out, err := json.Marshal(public)
	return out, usage, err
}

func formatSearchUnits(value int64) string {
	// json.Number retains the fixed-point quantity without binary float rounding.
	whole, _ := json.Marshal(value / 1000000)
	fraction, _ := json.Marshal(value % 1000000)
	if value%1000000 == 0 {
		return string(whole)
	}
	padding := "000000"[:6-len(fraction)]
	return string(whole) + "." + padding + string(fraction)
}
