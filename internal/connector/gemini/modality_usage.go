package gemini

import (
	"bytes"
	"encoding/json"

	"github.com/waiting-here/NonbiriAPI/internal/connector/chatwire"
	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
)

func modalityList(raw []byte, total int64) ([4]int64, string) {
	var tokens [4]int64
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return tokens, contract.ModalityMissing
	}
	var list []json.RawMessage
	if json.Unmarshal(raw, &list) != nil || list == nil || len(list) > 4 {
		return tokens, contract.ModalityInvalid
	}
	seen := [4]bool{}
	remaining := total
	for _, item := range list {
		fields, err := chatwire.Object(item)
		if err != nil {
			return [4]int64{}, contract.ModalityInvalid
		}
		var modality string
		if json.Unmarshal(fields["modality"], &modality) != nil {
			return [4]int64{}, contract.ModalityInvalid
		}
		index := -1
		switch modality {
		case "TEXT":
			index = 0
		case "IMAGE":
			index = 1
		case "AUDIO":
			index = 2
		case "VIDEO":
			index = 3
		default:
			return [4]int64{}, contract.ModalityUnknown
		}
		n, ok := chatwire.Integer(fields["tokenCount"])
		if !ok || seen[index] || n > remaining {
			return [4]int64{}, contract.ModalityInvalid
		}
		seen[index] = true
		tokens[index] = n
		remaining -= n
	}
	if remaining != 0 {
		return [4]int64{}, contract.ModalityIncomplete
	}
	return tokens, ""
}

func normalizeModalities(usage *contract.Usage, root map[string]json.RawMessage, prompt, candidates, thought int64) {
	m := &usage.Modalities
	if raw, exists := root["promptTokensDetails"]; exists {
		input, reason := modalityList(raw, prompt)
		read := [4]int64{}
		if reason == "" && (usage.CacheReadInputTokens > 0 || len(root["cacheTokensDetails"]) > 0) {
			if len(root["cacheTokensDetails"]) == 0 {
				reason = contract.ModalityCacheUnknown
			} else {
				read, reason = modalityList(root["cacheTokensDetails"], usage.CacheReadInputTokens)
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
	if raw, exists := root["candidatesTokensDetails"]; exists {
		output, reason := modalityList(raw, candidates)
		// candidatesTokensDetails excludes thoughts. No standard field classifies
		// those tokens by modality, so a positive thought count needs fallback.
		if reason == "" && thought > 0 {
			reason = contract.ModalityReasoningUnknown
		}
		m.OutputReason = reason
		if reason == "" {
			m.Output = output
			m.OutputPresent = true
		}
	}
}
