// Package continuation validates opaque source affinity carried with provider
// history. Tokens identify no public model, endpoint, credential, or account.
package continuation

import (
	"encoding/base64"
	"encoding/json"
)

func ID(extra []byte) (string, bool) {
	if len(extra) == 0 || string(extra) == "null" {
		return "", true
	}
	var root map[string]json.RawMessage
	if json.Unmarshal(extra, &root) != nil || root == nil {
		return "", false
	}
	raw, present := root["nonbiri"]
	if !present {
		return "", true
	}
	var namespace map[string]json.RawMessage
	if json.Unmarshal(raw, &namespace) != nil || len(namespace) != 1 {
		return "", false
	}
	var id string
	if json.Unmarshal(namespace["continuation_id"], &id) != nil || len(id) != 43 {
		return "", false
	}
	value, err := base64.RawURLEncoding.Strict().DecodeString(id)
	valid := err == nil && len(value) == 32
	clear(value)
	return id, valid
}

func ValidExtra(root map[string]json.RawMessage, provider string) bool {
	if root == nil {
		return false
	}
	for name := range root {
		if name != provider && name != "nonbiri" {
			return false
		}
	}
	raw, err := json.Marshal(root)
	if err != nil {
		return false
	}
	defer clear(raw)
	_, valid := ID(raw)
	return valid
}

func Bind(extra map[string]any, id string) map[string]any {
	if id != "" {
		extra["nonbiri"] = map[string]string{"continuation_id": id}
	}
	return extra
}
