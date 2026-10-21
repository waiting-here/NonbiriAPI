package openai

import (
	"encoding/json"
	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"strings"
	"unicode/utf8"
)

func parseSystemOneModels(raw []byte, guard *sensitiveGuard) ([]contract.DiscoveredModel, error) {
	if len(raw) > MaxModelsBodyBytes {
		return nil, ErrModelsResponseTruncated
	}
	if !validateEmbeddingJSON(raw) {
		return nil, ErrMalformedModelsJSON
	}
	root, ok := systemOneMap(raw)
	if !ok || !validJSONArray(root["models"]) {
		return nil, ErrModelsDataMissing
	}
	var entries []struct {
		Name string `json:"name"`
	}
	if json.Unmarshal(root["models"], &entries) != nil {
		return nil, ErrMalformedModelsJSON
	}
	if len(entries) > MaxDiscoveredModels {
		return nil, ErrTooManyModels
	}
	result := []contract.DiscoveredModel{}
	seen := map[string]bool{}
	for _, entry := range entries {
		if guard != nil && containsSensitiveDiscoveryText(guard, entry.Name) {
			return nil, errSensitiveModels
		}
		name := strings.TrimSpace(entry.Name)
		if name == "" {
			return nil, ErrEmptyModelID
		}
		if utf8.RuneCountInString(entry.Name) > MaxDiscoveredModelIDRunes {
			return nil, ErrModelIDTooLong
		}
		if !validDiscoveryText(entry.Name) {
			return nil, ErrInvalidModelID
		}
		if !seen[name] {
			result = append(result, contract.DiscoveredModel{ID: name})
			seen[name] = true
		}
	}
	return result, nil
}
