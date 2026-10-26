package donation

import "encoding/json"

// Legacy receipts wrapped their decision snapshot with the original pending keys.
// Decode the envelope without exposing internal metadata to clients.
type reviewReceipt struct {
	Version      int             `json:"donation_review_receipt"`
	Response     json.RawMessage `json:"response"`
	StandardKeys map[string]bool `json:"standard_keys"`
}

func unwrapReviewReceipt(body []byte) ([]byte, map[string]bool, error) {
	var stored reviewReceipt
	if err := json.Unmarshal(body, &stored); err != nil {
		return nil, nil, ErrInvariant
	}
	if stored.Version == 0 {
		return body, nil, nil
	}
	if stored.Version != 1 || len(stored.Response) == 0 || stored.StandardKeys == nil {
		return nil, nil, ErrInvariant
	}
	return stored.Response, stored.StandardKeys, nil
}
