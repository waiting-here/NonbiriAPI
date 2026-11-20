package gemini

import (
	"encoding/json"
	"reflect"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/connector/chatwire"
	"github.com/waiting-here/NonbiriAPI/internal/connector/continuation"
)

func replayParts(extra []byte, expected []any) ([]any, error) {
	if chatwire.Null(extra) {
		return expected, nil
	}
	outer, err := chatwire.Object(extra)
	if err != nil || !continuation.ValidExtra(outer, "google") {
		return nil, chatwire.Rejection("extra_content")
	}
	google, err := chatwire.Object(outer["google"])
	if err != nil {
		return nil, chatwire.Rejection("extra_content")
	}
	if _, present := google["parts"]; !present {
		return expected, nil
	}
	var parts []json.RawMessage
	if !chatwire.Keys(google, "parts") || json.Unmarshal(google["parts"], &parts) != nil || len(parts) == 0 || len(parts) > 4096 {
		return nil, chatwire.Rejection("extra_content")
	}
	type projection struct {
		text, reasoning string
		calls           []any
	}
	project := func(parts any) (projection, error) {
		raw, err := json.Marshal(map[string]any{"candidates": []any{map[string]any{"content": map[string]any{"role": "model", "parts": parts}, "finishReason": "STOP"}}})
		if err != nil {
			return projection{}, err
		}
		defer clear(raw)
		d := NewDecoder("continuation", time.Unix(0, 0), false).(*decoder)
		defer d.Clear()
		if _, err := d.consume(raw); err != nil {
			return projection{}, err
		}
		return projection{text: d.text.String(), reasoning: d.thought.String(), calls: d.calls}, nil
	}
	actual, err := project(parts)
	wanted, expectedErr := project(expected)
	if err != nil || expectedErr != nil {
		return nil, chatwire.Rejection("extra_content")
	}
	// A client may preserve signatures solely in the native parts. Any
	// signature it also supplies on a projected call must agree with them.
	for i, call := range actual.calls {
		if i < len(wanted.calls) && wanted.calls[i].(map[string]any)["extra_content"] == nil {
			delete(call.(map[string]any), "extra_content")
		}
	}
	if !reflect.DeepEqual(actual, wanted) {
		return nil, chatwire.Rejection("extra_content")
	}
	out := make([]any, len(parts))
	for i, part := range parts {
		out[i] = part
	}
	return out, nil
}
