package openai

import (
	"bytes"
	"encoding/json"
	"reflect"
)

// CompleteResponseTerminal fills only omitted terminal fields. The caller must
// supply completed items in protocol order and verify any open item lifecycle.
func CompleteResponseTerminal(raw []byte, kind, responseID string, items []json.RawMessage) ([]byte, error) {
	var root map[string]json.RawMessage
	if !validateEmbeddingJSON(raw) || json.Unmarshal(raw, &root) != nil || root == nil {
		return nil, errInvalidUpstreamResponse
	}
	var id string
	if json.Unmarshal(root["id"], &id) != nil || id == "" || len(id) > 512 || responseID != "" && responseID != id {
		return nil, errInvalidUpstreamResponse
	}
	status := ""
	switch kind {
	case "response.completed":
		status = "completed"
	case "response.incomplete":
		status = "incomplete"
	default:
		return nil, errInvalidUpstreamResponse
	}
	for name, expected := range map[string]string{"object": "response", "status": status} {
		if value, present := root[name]; present {
			var actual string
			if json.Unmarshal(value, &actual) != nil || actual != expected {
				return nil, errInvalidUpstreamResponse
			}
		} else {
			root[name], _ = json.Marshal(expected)
		}
	}
	if value, present := root["output"]; present {
		var output []json.RawMessage
		if json.Unmarshal(value, &output) != nil || output == nil {
			return nil, errInvalidUpstreamResponse
		}
		// An explicitly empty output remains empty; previously emitted items
		// are already available to the caller and must not be duplicated.
		if len(output) > 0 && len(items) > 0 {
			if len(output) != len(items) {
				return nil, errInvalidUpstreamResponse
			}
			for i := range items {
				if !sameResponseItem(output[i], items[i]) {
					return nil, errInvalidUpstreamResponse
				}
			}
		}
	} else {
		if len(items) == 0 {
			return nil, errInvalidUpstreamResponse
		}
		root["output"], _ = json.Marshal(items)
	}
	return json.Marshal(root)
}

func sameResponseItem(a, b []byte) bool {
	var left, right any
	d := json.NewDecoder(bytes.NewReader(a))
	d.UseNumber()
	if d.Decode(&left) != nil {
		return false
	}
	d = json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	return d.Decode(&right) == nil && reflect.DeepEqual(left, right)
}

type responseItemSnapshot struct {
	id    string
	kind  string
	final json.RawMessage
}

type responseStreamSnapshot struct{ items []responseItemSnapshot }

func (s *responseStreamSnapshot) observe(kind string, root map[string]json.RawMessage) bool {
	if kind != "response.output_item.added" && kind != "response.output_item.done" {
		return true
	}
	var index int
	var item map[string]json.RawMessage
	var id string
	var itemKind string
	if json.Unmarshal(root["item"], &item) != nil || json.Unmarshal(item["id"], &id) != nil || !validOpaqueText(id, 512, true) {
		return false
	}
	if json.Unmarshal(item["type"], &itemKind) != nil || !validOpaqueText(itemKind, 128, true) {
		return false
	}
	if value, present := root["output_index"]; present {
		if isJSONNull(value) || json.Unmarshal(value, &index) != nil || index < 0 || index > 4095 {
			return false
		}
	} else {
		if kind != "response.output_item.done" {
			return false
		}
		index = len(s.items)
		for i, existing := range s.items {
			if existing.id == id {
				index = i
				break
			}
		}
		if index == len(s.items) {
			if index > 4095 {
				return false
			}
			for _, existing := range s.items {
				if existing.final == nil {
					return false
				}
			}
		}
	}
	if index > len(s.items) {
		return false
	}
	if index == len(s.items) {
		for _, existing := range s.items {
			if existing.id == id {
				return false
			}
		}
		s.items = append(s.items, responseItemSnapshot{id: id, kind: itemKind})
	} else if kind == "response.output_item.added" || s.items[index].id != id || s.items[index].kind != itemKind || s.items[index].final != nil {
		return false
	}
	if kind == "response.output_item.done" {
		s.items[index].final = append(json.RawMessage(nil), root["item"]...)
	}
	return true
}

func (s *responseStreamSnapshot) completed() ([]json.RawMessage, bool) {
	items := make([]json.RawMessage, len(s.items))
	for i, item := range s.items {
		if item.final == nil {
			return nil, false
		}
		items[i] = item.final
	}
	return items, true
}

func (s *responseStreamSnapshot) clear() {
	for _, item := range s.items {
		clear(item.final)
	}
	s.items = nil
}
