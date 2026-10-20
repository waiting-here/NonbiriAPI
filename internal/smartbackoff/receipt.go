package smartbackoff

import (
	"bytes"
	"encoding/json"
)

// ProjectReceipt omits retired settings from the known donation receipt
// envelopes. Stored receipts keep their original expiry and idempotency result.
func ProjectReceipt(body []byte) ([]byte, error) {
	if !bytes.Contains(body, []byte(`"smart_backoff_profile"`)) && !bytes.Contains(body, []byte(`"smart_backoff_rules"`)) {
		return append([]byte(nil), body...), nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		return nil, err
	}
	changed := removeSettings(fields)
	if raw, ok := fields["keys"]; ok {
		var keys []map[string]json.RawMessage
		if err := json.Unmarshal(raw, &keys); err != nil {
			return nil, err
		}
		nested := false
		for _, key := range keys {
			nested = removeSettings(key) || nested
		}
		if nested {
			encoded, err := json.Marshal(keys)
			if err != nil {
				return nil, err
			}
			fields["keys"] = encoded
			changed = true
		}
	}
	if raw, ok := fields["key"]; ok {
		var key map[string]json.RawMessage
		if err := json.Unmarshal(raw, &key); err != nil {
			return nil, err
		}
		if removeSettings(key) {
			encoded, err := json.Marshal(key)
			if err != nil {
				return nil, err
			}
			fields["key"] = encoded
			changed = true
		}
	}
	if !changed {
		return append([]byte(nil), body...), nil
	}
	return json.Marshal(fields)
}
func removeSettings(fields map[string]json.RawMessage) bool {
	changed := false
	for _, key := range []string{"smart_backoff_profile", "smart_backoff_rules"} {
		if _, ok := fields[key]; ok {
			delete(fields, key)
			changed = true
		}
	}
	return changed
}
