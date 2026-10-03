package openai

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestTransportModePreservesCallerAndReprojectsCapabilities(t *testing.T) {
	for _, caller := range []bool{false, true} {
		mode := "false"
		if caller {
			mode = "true"
		}
		request, err := DecodeChatRequest(strings.NewReader(`{"model":"p/m","stream":`+mode+`,"stream_options":{"include_usage":false},"max_completion_tokens":128000,"messages":[{"role":"user","content":"hello"}]}`), MaxRequestBodyBytes)
		if err != nil {
			t.Fatal(err)
		}
		defer request.Clear()
		converted, err := request.WithStream(!caller)
		if err != nil {
			t.Fatal(err)
		}
		defer converted.Clear()
		raw, err := converted.LogicalBody()
		if err != nil {
			t.Fatal(err)
		}
		var body map[string]json.RawMessage
		if json.Unmarshal(raw, &body) != nil || string(body["max_completion_tokens"]) != "128000" || converted.Stream == caller || request.Stream != caller {
			t.Fatalf("converted: %s %v", raw, err)
		}
		if converted.Stream {
			if string(body["stream_options"]) != `{"include_usage":true}` {
				t.Fatal(string(body["stream_options"]))
			}
		} else if body["stream_options"] != nil {
			t.Fatal("nonstream retained stream options")
		}
		original, _ := request.RawField("stream_options")
		if string(original) != `{"include_usage":false}` {
			t.Fatal("caller options changed")
		}
	}
	request, err := DecodeChatRequest(strings.NewReader(`{"model":"p/m","stream":true,"stream_options":{"vendor":true}}`), MaxRequestBodyBytes)
	if err != nil {
		t.Fatal(err)
	}
	defer request.Clear()
	if _, err := request.WithStream(false); err == nil {
		t.Fatal("untranslatable options dropped")
	}
}
