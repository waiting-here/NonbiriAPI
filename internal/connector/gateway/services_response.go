package gateway

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"math"
	"strings"

	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/connector/openai"
)

func translateImage(raw []byte, request *openai.ImageRequest, created int64) ([]byte, contract.Usage, error) {
	invalid := func() ([]byte, contract.Usage, error) { return nil, contract.Usage{}, errResponse }
	if request == nil || request.Count < 1 || int64(len(raw)) > openai.DefaultMaxImageResponseBytes {
		return invalid()
	}
	root, err := parseObject(raw)
	if err != nil || !warningsOK(root["warnings"]) {
		return invalid()
	}
	if raw, present := root["isRetryable"]; present {
		var retryable bool
		if isNull(raw) || json.Unmarshal(raw, &retryable) != nil {
			return invalid()
		}
	}
	var images []string
	if json.Unmarshal(root["images"], &images) != nil || len(images) != request.Count {
		return invalid()
	}
	data := make([]map[string]string, len(images))
	for i, encoded := range images {
		if encoded == "" || strings.ContainsAny(encoded, "\r\n") {
			return invalid()
		}
		count, err := io.Copy(io.Discard, base64.NewDecoder(base64.StdEncoding.Strict(), strings.NewReader(encoded)))
		if err != nil || count == 0 {
			return invalid()
		}
		data[i] = map[string]string{"b64_json": encoded}
	}
	usage := flatImageUsage(root["usage"])
	output := map[string]any{"created": created, "data": data}
	if usage.Present {
		total, _ := usageTotal(usage)
		output["usage"] = map[string]int64{"input_tokens": usage.UncachedInputTokens, "output_tokens": usage.OutputTokens, "total_tokens": total}
	}
	body, err := json.Marshal(output)
	if int64(len(body)) > openai.DefaultMaxImageResponseBytes {
		clear(body)
		return nil, usage, errResponse
	}
	return body, usage, err
}

// Flat image counters carry no text/image partition. Missing or invalid
// counters leave usage unknown, matching the existing image billing boundary.
func flatImageUsage(raw []byte) contract.Usage {
	fields, err := parseObject(raw)
	if err != nil {
		return contract.Usage{}
	}
	input, inputOK := nonnegativeInteger(fields["inputTokens"])
	output, outputOK := nonnegativeInteger(fields["outputTokens"])
	total, totalOK := nonnegativeInteger(fields["totalTokens"])
	if !inputOK || !outputOK || !totalOK {
		return contract.Usage{}
	}
	if input > math.MaxInt64-output {
		return contract.Usage{TotalMismatch: true}
	}
	return contract.Usage{Present: true, UncachedInputTokens: input, OutputTokens: output, TotalMismatch: input+output != total}
}

func nonnegativeInteger(raw []byte) (int64, bool) {
	var value int64
	if len(raw) == 0 || isNull(raw) || json.Unmarshal(raw, &value) != nil || value < 0 {
		return 0, false
	}
	return value, true
}

func translateRerank(raw []byte, request *openai.InferenceRequest) ([]byte, contract.Usage, error) {
	invalid := func() ([]byte, contract.Usage, error) { return nil, contract.Usage{}, errResponse }
	if request == nil || request.Operation() != contract.OperationRerank {
		return invalid()
	}
	root, err := parseObject(raw)
	if err != nil || !warningsOK(root["warnings"]) {
		return invalid()
	}
	documents, _, err := rerankDocuments(request)
	if err != nil {
		return invalid()
	}
	defer clearDocuments(documents)
	var ranking []json.RawMessage
	if json.Unmarshal(root["ranking"], &ranking) != nil || len(ranking) == 0 || len(ranking) > len(documents) {
		return invalid()
	}
	topN, present := request.RawField("top_n")
	if present && !isNull(topN) {
		count, ok := nonnegativeInteger(topN)
		clear(topN)
		if !ok || int64(len(ranking)) > count {
			return invalid()
		}
	}
	clear(topN)
	rawReturn, _ := request.RawField("return_documents")
	var returnDocuments bool
	_ = json.Unmarshal(rawReturn, &returnDocuments)
	clear(rawReturn)
	seen := make(map[int64]bool, len(ranking))
	results := make([]map[string]any, len(ranking))
	for i, rawRow := range ranking {
		row, err := parseObject(rawRow)
		index, validIndex := nonnegativeInteger(row["index"])
		var score float64
		if err != nil || !validIndex || index >= int64(len(documents)) || seen[index] ||
			len(row["relevanceScore"]) == 0 || isNull(row["relevanceScore"]) || json.Unmarshal(row["relevanceScore"], &score) != nil || math.IsNaN(score) || math.IsInf(score, 0) {
			return invalid()
		}
		seen[index] = true
		result := map[string]any{"index": index, "relevance_score": score}
		if returnDocuments {
			if value, ok := text(documents[index]); ok {
				result["document"] = map[string]string{"text": value}
			} else {
				result["document"] = documents[index]
			}
		}
		results[i] = result
	}
	body, err := json.Marshal(map[string]any{"object": "list", "model": request.Model, "results": results})
	if int64(len(body)) > maxJSONBytes {
		clear(body)
		return invalid()
	}
	// The native reranking contract defines no usage. Vendor metadata cannot
	// supply billing units without an independently defined mapping.
	return body, contract.Usage{}, err
}
