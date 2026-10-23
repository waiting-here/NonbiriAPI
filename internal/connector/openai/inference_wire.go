package openai

import (
	"bytes"
	"encoding/json"
	"strings"

	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
)

func InferenceSupported(kind contract.Type, operation contract.Operation) bool {
	if !contract.IsHTTPInference(operation) {
		return false
	}
	if kind == contract.TypeOpenAICompatible {
		return operation != contract.OperationSystemOne
	}
	if kind == contract.TypeOpenAIChatCompletions {
		return operation == contract.OperationResponses
	}
	if kind == contract.TypeTypeSafeSystemOne {
		return operation == contract.OperationSystemOne
	}
	if kind == contract.TypeOpenAIResponses {
		return operation == contract.OperationResponses || operation == contract.OperationResponsesCompact || operation == contract.OperationResponsesInputTokens
	}
	return operation == contract.OperationRerank && (kind == contract.TypeCohereRerank || kind == contract.TypeJinaRerank || kind == contract.TypeSiliconFlowRerank || kind == contract.TypeVoyageRerank)
}

func (r *InferenceRequest) CheckTarget(kind contract.Type, policy contract.AttemptPolicy) error {
	if r == nil || !InferenceSupported(kind, r.operation) || len(policy.NativeExtensions) != 0 || policy.FlattenToolCalls {
		return &contract.RequestRejection{Stage: "protocol preflight", Field: "request", Reason: "request cannot be represented by this connector"}
	}
	if r.operation == contract.OperationRerank {
		body, _, err := r.marshalInference("model", kind, contract.AttemptPolicy{})
		clear(body)
		return err
	}
	if kind == contract.TypeOpenAIChatCompletions {
		body, err := r.compileChatResponse("model", policy)
		clear(body)
		return err
	}
	if r.operation == contract.OperationSystemOne {
		return r.checkSystemOneOptions()
	}
	return nil
}

func inferencePath(operation contract.Operation) string {
	switch operation {
	case contract.OperationSystemOne:
		return "/systemone"
	case contract.OperationResponses:
		return "/responses"
	case contract.OperationResponsesCompact:
		return "/responses/compact"
	case contract.OperationResponsesInputTokens:
		return "/responses/input_tokens"
	case contract.OperationCompletions:
		return "/completions"
	case contract.OperationImagesEdits:
		return "/images/edits"
	case contract.OperationImagesVariations:
		return "/images/variations"
	case contract.OperationAudioSpeech:
		return "/audio/speech"
	case contract.OperationAudioTranscriptions:
		return "/audio/transcriptions"
	case contract.OperationAudioTranslations:
		return "/audio/translations"
	case contract.OperationModerations:
		return "/moderations"
	case contract.OperationRerank:
		return "/rerank"
	}
	return ""
}

func (r *InferenceRequest) marshalInference(model string, kind contract.Type, policy contract.AttemptPolicy) ([]byte, string, error) {
	if r == nil || !validOpaqueText(model, MaxUpstreamModelRunes, true) || !InferenceSupported(kind, r.operation) {
		return nil, "", ErrInvalidRequest
	}
	object := fieldsByName(r.fields)
	if kind == contract.TypeOpenAIChatCompletions {
		body, err := r.compileChatResponse(model, policy)
		return body, "application/json", err
	}
	object["model"], _ = json.Marshal(model)
	if r.operation == contract.OperationResponses {
		object["store"] = json.RawMessage("false")
		object["background"] = json.RawMessage("false")
		if policy.SafetyIdentifier != "" {
			object["safety_identifier"], _ = json.Marshal(policy.SafetyIdentifier)
		}
	}
	if r.operation == contract.OperationResponsesCompact || r.operation == contract.OperationResponsesInputTokens {
		delete(object, "store")
		delete(object, "background")
		delete(object, "stream")
	}
	if r.operation == contract.OperationCompletions && r.Stream && !excludedField(r.excluded, "stream_options") {
		options := map[string]json.RawMessage{}
		_ = json.Unmarshal(object["stream_options"], &options)
		if options == nil {
			options = map[string]json.RawMessage{}
		}
		options["include_usage"] = json.RawMessage("true")
		object["stream_options"], _ = json.Marshal(options)
	}
	if r.operation == contract.OperationRerank {
		delete(object, "stream")
		if kind == contract.TypeVoyageRerank {
			if raw := object["top_n"]; raw != nil {
				object["top_k"] = raw
				delete(object, "top_n")
			}
		} else if raw := object["top_k"]; raw != nil {
			object["top_n"] = raw
			delete(object, "top_k")
		}
		allowed := map[string]bool{"model": true, "query": true, "documents": true, "top_n": true, "top_k": true, "return_documents": true}
		switch kind {
		case contract.TypeCohereRerank:
			allowed["max_tokens_per_doc"] = true
			allowed["priority"] = true
			allowed["relevant_spans"] = true
			delete(object, "return_documents")
		case contract.TypeVoyageRerank:
			allowed["truncation"] = true
		case contract.TypeSiliconFlowRerank:
			allowed["max_chunks_per_doc"] = true
			allowed["overlap_tokens"] = true
		default:
			allowed["max_chunks_per_doc"] = true
			allowed["max_doc_length"] = true
			allowed["truncate"] = true
		}
		for name := range object {
			if !allowed[name] {
				return nil, "", invalidField("request", "rerank field is not supported by the selected protocol")
			}
		}
		if kind == contract.TypeCohereRerank || kind == contract.TypeVoyageRerank || kind == contract.TypeSiliconFlowRerank {
			documents := make([]string, len(r.documents))
			for i, raw := range r.documents {
				if json.Unmarshal(raw, &documents[i]) != nil {
					var object map[string]json.RawMessage
					if json.Unmarshal(raw, &object) != nil || len(object) != 1 || json.Unmarshal(object["text"], &documents[i]) != nil {
						return nil, "", invalidField("documents", "the selected protocol requires text documents")
					}
				}
			}
			object["documents"], _ = json.Marshal(documents)
		}
	}
	if len(r.parts) > 0 {
		return r.marshalMultipart(object)
	}
	body, err := json.Marshal(object)
	if err != nil || int64(len(body)) > r.RequestBodyLimit()+(16<<10) {
		clear(body)
		return nil, "", ErrPayloadTooLarge
	}
	return body, "application/json", nil
}

func (r *InferenceRequest) responseMedia(media string) bool {
	if r.Stream {
		return media == "text/event-stream"
	}
	if r.operation == contract.OperationAudioSpeech {
		return strings.HasPrefix(media, "audio/") || media == "application/octet-stream"
	}
	if r.operation == contract.OperationAudioTranscriptions || r.operation == contract.OperationAudioTranslations {
		raw, _ := r.RawField("response_format")
		defer clear(raw)
		var format string
		_ = json.Unmarshal(raw, &format)
		if format == "text" || format == "srt" || format == "vtt" {
			return media == "text/plain" || media == "text/vtt" || media == "application/x-subrip"
		}
	}
	return media == "application/json"
}

func nativeResponseProjection(raw []byte, request *InferenceRequest) ([]byte, Usage, error) {
	if !validateEmbeddingJSON(raw) {
		return nil, Usage{}, errInvalidUpstreamResponse
	}
	fields, ok := borrowedObjectFields(raw)
	if !ok {
		return nil, Usage{}, errInvalidUpstreamResponse
	}
	root := fieldsByName(fields)
	usage := nativeInferenceUsage(root, request.operation)
	if hasUpstreamError(root) {
		return nil, usage, errInvalidUpstreamResponse
	}
	switch request.operation {
	case contract.OperationResponses:
		var status, object string
		_ = json.Unmarshal(root["status"], &status)
		_ = json.Unmarshal(root["object"], &object)
		if object != "response" || status != "completed" && status != "incomplete" || !validJSONArray(root["output"]) {
			return nil, usage, errInvalidUpstreamResponse
		}
		root["store"] = json.RawMessage("false")
		root["background"] = json.RawMessage("false")
	case contract.OperationResponsesCompact:
		if !bytes.Equal(root["object"], []byte(`"response.compaction"`)) || !validJSONArray(root["output"]) {
			return nil, usage, errInvalidUpstreamResponse
		}
	case contract.OperationResponsesInputTokens:
		var tokens int64
		if !decodeNonNegativeInt(root["input_tokens"], &tokens) || isJSONNull(root["input_tokens"]) {
			return nil, Usage{}, errInvalidUpstreamResponse
		}
		// A count is not evidence of consumed tokens.
	case contract.OperationCompletions:
		if !validLegacyChoices(root["choices"], false, request.count, nil) {
			return nil, usage, errInvalidUpstreamResponse
		}
	case contract.OperationAudioTranscriptions, contract.OperationAudioTranslations:
		if _, ok := decisionString(root["text"], 0, int(request.RequestBodyLimit())); !ok {
			return nil, usage, errInvalidUpstreamResponse
		}
	case contract.OperationModerations:
		var results []map[string]json.RawMessage
		if json.Unmarshal(root["results"], &results) != nil || len(results) == 0 {
			return nil, Usage{}, errInvalidUpstreamResponse
		}
		for _, result := range results {
			if !bytes.Equal(result["flagged"], []byte("true")) && !bytes.Equal(result["flagged"], []byte("false")) {
				return nil, Usage{}, errInvalidUpstreamResponse
			}
		}
	default:
		return nil, Usage{}, errInvalidUpstreamResponse
	}
	if root["model"] != nil || request.operation == contract.OperationResponses || request.operation == contract.OperationCompletions {
		root["model"], _ = json.Marshal(request.Model)
	}
	delete(root, "safety_identifier")
	body, err := json.Marshal(root)
	return body, usage, err
}

func nativeInferenceUsage(root map[string]json.RawMessage, operation contract.Operation) Usage {
	switch operation {
	case contract.OperationSystemOne:
		return systemOneUsage(root["usage"])
	case contract.OperationResponses, contract.OperationResponsesCompact:
		return ParseResponseUsage(root["usage"])
	case contract.OperationCompletions:
		return ParseChatUsage(root["usage"])
	case contract.OperationAudioSpeech, contract.OperationAudioTranscriptions, contract.OperationAudioTranslations:
		return ParseAudioUsage(root["usage"])
	}
	return Usage{}
}

func inferenceErrorUsage(raw []byte, operation contract.Operation) (Usage, bool) {
	if !validateEmbeddingJSON(raw) {
		return Usage{}, false
	}
	var root map[string]json.RawMessage
	if json.Unmarshal(raw, &root) != nil {
		return Usage{}, false
	}
	if operation == contract.OperationResponses && root["response"] != nil {
		var nested map[string]json.RawMessage
		if json.Unmarshal(root["response"], &nested) != nil {
			return Usage{}, false
		}
		root = nested
	}
	if len(root["usage"]) == 0 || isJSONNull(root["usage"]) {
		return Usage{}, false
	}
	return nativeInferenceUsage(root, operation), true
}

func validJSONArray(raw []byte) bool {
	var values []json.RawMessage
	return len(raw) > 0 && raw[0] == '[' && json.Unmarshal(raw, &values) == nil
}

func validLegacyChoices(raw []byte, stream bool, count int, finished map[int64]bool) bool {
	var choices []map[string]json.RawMessage
	if json.Unmarshal(raw, &choices) != nil || choices == nil || !stream && len(choices) != count {
		return false
	}
	seen := map[int64]bool{}
	for _, choice := range choices {
		var index int64
		if !decodeNonNegativeInt(choice["index"], &index) || isJSONNull(choice["index"]) || index >= int64(count) || seen[index] || finished[index] {
			return false
		}
		seen[index] = true
		if _, ok := decisionString(choice["text"], 0, int(DefaultMaxJSONResponseBytes)); !ok {
			return false
		}
		finish := choice["finish_reason"]
		if len(finish) == 0 || isJSONNull(finish) {
			if !stream {
				return false
			}
		} else {
			var reason string
			if json.Unmarshal(finish, &reason) != nil || reason == "" {
				return false
			}
			if finished != nil {
				finished[index] = true
			}
		}
	}
	return true
}
