package openai

import (
	"encoding/json"
	"strings"

	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
)

const (
	textModality  = 1 << contract.ModalityText
	imageModality = 1 << contract.ModalityImage
	audioModality = 1 << contract.ModalityAudio
)

// ParseChatUsage normalizes a Chat Completions or legacy Completions report.
func ParseChatUsage(raw []byte) Usage { usage, _ := parseUsage(raw); return usage }

// ParseImageUsage normalizes the image API's standard token usage object.
func ParseImageUsage(raw []byte) Usage { return imageUsage(raw) }

func modalityObject(raw []byte, allowed int, metadata ...string) ([4]int64, string) {
	var tokens [4]int64
	if len(raw) == 0 || isJSONNull(raw) {
		return tokens, contract.ModalityMissing
	}
	if !validateEmbeddingJSON(raw) {
		return tokens, contract.ModalityInvalid
	}
	fields, ok := borrowedObjectFields(raw)
	if !ok {
		return tokens, contract.ModalityInvalid
	}
	found := false
	for _, field := range fields {
		index := -1
		switch field.name {
		case "text_tokens":
			index = 0
		case "image_tokens":
			index = 1
		case "audio_tokens":
			index = 2
		case "video_tokens":
			index = 3
		}
		if index >= 0 {
			if allowed&(1<<index) == 0 {
				return [4]int64{}, contract.ModalityUnknown
			}
			if !decodeNonNegativeInt(field.value, &tokens[index]) {
				return [4]int64{}, contract.ModalityInvalid
			}
			found = true
			continue
		}
		known := false
		for _, name := range metadata {
			if name == field.name {
				known = true
				break
			}
		}
		if !known && strings.HasSuffix(field.name, "_tokens") {
			return [4]int64{}, contract.ModalityUnknown
		}
	}
	if !found {
		return tokens, contract.ModalityMissing
	}
	return tokens, ""
}

func modalityTotal(tokens [4]int64, total int64) bool {
	for _, n := range tokens {
		if n < 0 || n > total {
			return false
		}
		total -= n
	}
	return total == 0
}

func normalizeModalitySides(usage *Usage, inputRaw, outputRaw []byte, inputAllowed, outputAllowed int, cachedRaw []byte, metadata []string) {
	if !usage.Present {
		return
	}
	m := &usage.Modalities
	// Missing objects keep the zero-value report compatible with old snapshots.
	if len(inputRaw) > 0 && !isJSONNull(inputRaw) {
		input, reason := modalityObject(inputRaw, inputAllowed, metadata...)
		total, ok := addChecked(usage.UncachedInputTokens, usage.CacheWriteInputTokens)
		if ok {
			total, ok = addChecked(total, usage.CacheReadInputTokens)
		}
		if reason == "" && (!ok || !modalityTotal(input, total)) {
			reason = contract.ModalityIncomplete
		}
		if reason == "" && usage.CacheWriteInputTokens > 0 {
			reason = contract.ModalityCacheUnknown
		}
		read := [4]int64{}
		if reason == "" && (usage.CacheReadInputTokens > 0 || len(cachedRaw) > 0 && !isJSONNull(cachedRaw)) {
			if len(cachedRaw) == 0 || isJSONNull(cachedRaw) {
				reason = contract.ModalityCacheUnknown
			} else {
				read, reason = modalityObject(cachedRaw, inputAllowed)
				if reason == "" && !modalityTotal(read, usage.CacheReadInputTokens) {
					reason = contract.ModalityMismatch
				}
			}
		}
		if reason == "" {
			for i, n := range read {
				if n > input[i] {
					reason = contract.ModalityMismatch
					break
				}
				input[i] -= n
			}
		}
		m.InputReason = reason
		if reason == "" {
			m.Input[0] = input
			m.Input[2] = read
			m.InputPresent = true
		}
	}
	if len(outputRaw) > 0 && !isJSONNull(outputRaw) {
		output, reason := modalityObject(outputRaw, outputAllowed, metadata...)
		if reason == "" && !modalityTotal(output, usage.OutputTokens) {
			reason = contract.ModalityIncomplete
		}
		m.OutputReason = reason
		if reason == "" {
			m.Output = output
			m.OutputPresent = true
		}
	}
}

func normalizeChatModalities(usage *Usage, root map[string]json.RawMessage) {
	normalizeModalitySides(usage, root["prompt_tokens_details"], root["completion_tokens_details"], textModality|imageModality|audioModality, textModality|audioModality, nil,
		[]string{"cached_tokens", "cache_write_tokens", "reasoning_tokens", "accepted_prediction_tokens", "rejected_prediction_tokens"})
}

// ParseAudioUsage accepts token-based transcription usage. Duration-only
// reports remain unknown; no token counts are inferred from audio duration.
func ParseAudioUsage(raw []byte) Usage {
	usage, root := simpleTokenUsage(raw)
	if root == nil {
		return usage
	}
	if kind, ok := root["type"]; ok && string(kind) != `"tokens"` {
		return Usage{}
	}
	normalizeModalitySides(&usage, root["input_token_details"], nil, textModality|audioModality, 0, nil, nil)
	// The transcription protocol defines this bucket as generated text tokens.
	if usage.Present {
		usage.Modalities.OutputPresent = true
		usage.Modalities.Output[0] = usage.OutputTokens
	}
	return usage
}

// ParseRealtimeUsage accepts a single response's standard text/audio usage.
func ParseRealtimeUsage(raw []byte) Usage {
	usage, root := simpleTokenUsage(raw)
	if root == nil || !usage.Present {
		return usage
	}
	var cachedRaw []byte
	if details := root["input_token_details"]; len(details) > 0 && !isJSONNull(details) {
		fields, ok := borrowedObjectFields(details)
		if !ok {
			return Usage{TotalMismatch: usage.TotalMismatch}
		}
		values := fieldsByName(fields)
		if cached, exists := values["cached_tokens"]; exists {
			if !decodeNonNegativeInt(cached, &usage.CacheReadInputTokens) || usage.CacheReadInputTokens > usage.UncachedInputTokens {
				return Usage{TotalMismatch: usage.TotalMismatch}
			}
			usage.UncachedInputTokens -= usage.CacheReadInputTokens
		}
		cachedRaw = values["cached_tokens_details"]
	}
	normalizeModalitySides(&usage, root["input_token_details"], root["output_token_details"], textModality|imageModality|audioModality, textModality|audioModality, cachedRaw, []string{"cached_tokens", "cached_tokens_details"})
	return usage
}

func simpleTokenUsage(raw []byte) (Usage, map[string]json.RawMessage) {
	if len(raw) == 0 || isJSONNull(raw) || !validateEmbeddingJSON(raw) {
		return Usage{}, nil
	}
	fields, ok := borrowedObjectFields(raw)
	if !ok {
		return Usage{}, nil
	}
	root := fieldsByName(fields)
	var input, output, total int64
	if !decodeNonNegativeInt(root["input_tokens"], &input) || !decodeNonNegativeInt(root["output_tokens"], &output) || !decodeNonNegativeInt(root["total_tokens"], &total) {
		return Usage{}, nil
	}
	sum, ok := addChecked(input, output)
	if !ok {
		return Usage{TotalMismatch: true}, root
	}
	return Usage{Present: true, UncachedInputTokens: input, OutputTokens: output, TotalMismatch: sum != total}, root
}
