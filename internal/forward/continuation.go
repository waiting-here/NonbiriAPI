package forward

import (
	"encoding/base64"
	"encoding/json"

	"github.com/waiting-here/NonbiriAPI/internal/connector/openai"
	"github.com/waiting-here/NonbiriAPI/internal/requestattempt"
)

func continuationID(request *validatedRequest) (string, error) {
	var raw []byte
	if request.chat != nil {
		raw, _ = request.chat.RawField("messages")
	} else if request.inference != nil {
		raw, _ = request.inference.RawField("input")
	}
	defer clear(raw)
	var items []struct {
		Extra json.RawMessage `json:"extra_content"`
	}
	if len(raw) == 0 || json.Unmarshal(raw, &items) != nil {
		return "", nil
	}
	var found string
	for _, item := range items {
		var extra map[string]json.RawMessage
		if json.Unmarshal(item.Extra, &extra) != nil || extra["nonbiri"] == nil {
			continue
		}
		var ns struct {
			ID string `json:"continuation_id"`
		}
		if json.Unmarshal(extra["nonbiri"], &ns) != nil {
			return "", continuationRejection()
		}
		decoded, err := base64.RawURLEncoding.DecodeString(ns.ID)
		if err != nil || len(decoded) != 32 || base64.RawURLEncoding.EncodeToString(decoded) != ns.ID || found != "" && found != ns.ID {
			return "", continuationRejection()
		}
		found = ns.ID
	}
	return found, nil
}
func continuationRejection() error {
	return &openai.ValidationError{Detail: requestattempt.NewDetail("messages", "provider continuation requires the original permitted model connection")}
}
