package gemini

import (
	"encoding/json"
	"strings"

	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/strictjson"
)

// Only documented, unambiguous error envelopes refine a 400 response. Free
// text and unknown preconditions never become credential or region claims.
func discoveryEnvelopeFailure(raw []byte) contract.DiscoveryFailureKind {
	if strictjson.ValidateObject(raw) != nil {
		return contract.DiscoveryFailureProtocol
	}
	var envelope struct {
		Error struct {
			Code    int    `json:"code"`
			Status  string `json:"status"`
			Message string `json:"message"`
			Details []struct {
				Type   string `json:"@type"`
				Reason string `json:"reason"`
				Domain string `json:"domain"`
			} `json:"details"`
		} `json:"error"`
	}
	if json.Unmarshal(raw, &envelope) != nil || envelope.Error.Code != 400 {
		return contract.DiscoveryFailureProtocol
	}
	if envelope.Error.Status == "FAILED_PRECONDITION" && strings.TrimSpace(envelope.Error.Message) == "User location is not supported for the API use." {
		return contract.DiscoveryFailureRegion
	}
	if envelope.Error.Status == "INVALID_ARGUMENT" {
		for _, detail := range envelope.Error.Details {
			if detail.Type == "type.googleapis.com/google.rpc.ErrorInfo" && detail.Domain == "googleapis.com" && detail.Reason == "API_KEY_INVALID" {
				return contract.DiscoveryFailureAuth
			}
		}
	}
	return contract.DiscoveryFailureProtocol
}
