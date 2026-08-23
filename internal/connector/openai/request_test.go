package openai

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestChatRequestCloneForAttemptIsIndependent(t *testing.T) {
	original, err := DecodeChatRequest(strings.NewReader(`{"model":"public/model","messages":[{"role":"user","content":"hello"}],"stream":true}`), MaxRequestBodyBytes)
	if err != nil {
		t.Fatal(err)
	}
	defer original.Clear()
	first := original.CloneForAttempt()
	second := original.CloneForAttempt()
	if first == nil || second == nil {
		t.Fatal("attempt clone was nil")
	}
	firstBody, err := first.marshalUpstream("upstream/model", "safety_identifier_value")
	if err != nil {
		t.Fatal(err)
	}
	defer clear(firstBody)
	first.Clear()
	secondBody, err := second.marshalUpstream("upstream/model", "safety_identifier_value")
	if err != nil {
		t.Fatalf("second clone changed after first clear: %v", err)
	}
	defer clear(secondBody)
	originalBody, err := original.marshalUpstream("upstream/model", "safety_identifier_value")
	if err != nil {
		t.Fatalf("original changed after clone clear: %v", err)
	}
	defer clear(originalBody)
	if !bytes.Equal(firstBody, secondBody) || !bytes.Equal(firstBody, originalBody) {
		t.Fatalf("attempt snapshots drifted:\nfirst=%s\nsecond=%s\noriginal=%s", firstBody, secondBody, originalBody)
	}
	second.Clear()
}

func TestDecodeChatRequestPassesUnknownAndOverridesAuthorityFields(t *testing.T) {
	request, err := DecodeChatRequest(strings.NewReader(`{
		"model":"platform/provider/model",
		"messages":[{"role":"user","content":"private prompt"}],
		"stream":true,
		"temperature":0.25,
		"vendor_extension":{"enabled":true},
		"stream_options":{"include_usage":false,"vendor":1},
		"safety_identifier":"caller-forged"
	}`), MaxRequestBodyBytes)
	if err != nil {
		t.Fatalf("DecodeChatRequest: %v", err)
	}
	defer request.Clear()
	if request.Model != "platform/provider/model" || !request.Stream {
		t.Fatalf("request metadata = model %q stream %v", request.Model, request.Stream)
	}

	body, err := request.marshalUpstream("upstream/model", "nbu_server_value")
	if err != nil {
		t.Fatalf("marshalUpstream: %v", err)
	}
	defer clear(body)
	var got map[string]json.RawMessage
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("upstream JSON: %v: %s", err, body)
	}
	var model, safety string
	if err := json.Unmarshal(got["model"], &model); err != nil || model != "upstream/model" {
		t.Fatalf("upstream model=%q err=%v", model, err)
	}
	if err := json.Unmarshal(got["safety_identifier"], &safety); err != nil || safety != "nbu_server_value" {
		t.Fatalf("safety_identifier=%q err=%v", safety, err)
	}
	if !bytes.Contains(got["messages"], []byte("private prompt")) || !bytes.Contains(got["vendor_extension"], []byte("true")) {
		t.Fatalf("unknown/request fields were not passed through: %s", body)
	}
	var streamOptions map[string]json.RawMessage
	if err := json.Unmarshal(got["stream_options"], &streamOptions); err != nil || !bytes.Equal(streamOptions["include_usage"], []byte("true")) || !bytes.Equal(streamOptions["vendor"], []byte("1")) {
		t.Fatalf("stream usage option was not authoritatively merged: %s", got["stream_options"])
	}
}

func TestMarshalStreamRequestAddsUsageOptionWhenAbsent(t *testing.T) {
	request, err := DecodeChatRequest(strings.NewReader(`{"model":"p/m","stream":true}`), MaxRequestBodyBytes)
	if err != nil {
		t.Fatal(err)
	}
	defer request.Clear()
	body, err := request.marshalUpstream("upstream", "nbu_safe")
	if err != nil {
		t.Fatal(err)
	}
	defer clear(body)
	var root map[string]json.RawMessage
	if err := json.Unmarshal(body, &root); err != nil || !bytes.Contains(root["stream_options"], []byte(`"include_usage":true`)) {
		t.Fatalf("stream_options injection failed: err=%v body=%s", err, body)
	}
}

func TestDecodeChatRequestRejectsAmbiguityAndBounds(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{"empty", ""},
		{"array root", `[{"model":"p/m"}]`},
		{"missing model", `{"messages":[]}`},
		{"model null", `{"model":null}`},
		{"model control", "{\"model\":\"p/m\\u000a\"}"},
		{"model edge whitespace", `{"model":" p/m"}`},
		{"stream string", `{"model":"p/m","stream":"true"}`},
		{"stream null", `{"model":"p/m","stream":null}`},
		{"duplicate model", `{"model":"p/one","model":"p/two"}`},
		{"escaped duplicate model", `{"model":"p/one","\u006dodel":"p/two"}`},
		{"trailing object", `{"model":"p/m"}{}`},
		{"trailing scalar", `{"model":"p/m"} true`},
		{"invalid value", `{"model":"p/m","x":}`},
		{"invalid field control", `{"model":"p/m","bad\u000akey":1}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request, err := DecodeChatRequest(strings.NewReader(test.body), MaxRequestBodyBytes)
			if request != nil {
				request.Clear()
			}
			if !errors.Is(err, ErrInvalidRequest) {
				t.Fatalf("error=%v, want ErrInvalidRequest", err)
			}
		})
	}

	tooLong := `{"model":"` + strings.Repeat("界", MaxPlatformModelRunes+1) + `"}`
	if _, err := DecodeChatRequest(strings.NewReader(tooLong), MaxRequestBodyBytes); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("long model error=%v", err)
	}

	invalidUTF8 := append([]byte(`{"model":"p/m","x":"`), 0xff)
	invalidUTF8 = append(invalidUTF8, []byte(`"}`)...)
	if _, err := DecodeChatRequest(bytes.NewReader(invalidUTF8), MaxRequestBodyBytes); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("invalid UTF-8 error=%v", err)
	}

	over := bytes.Repeat([]byte{'x'}, int(MaxRequestBodyBytes)+1)
	if _, err := DecodeChatRequest(bytes.NewReader(over), MaxRequestBodyBytes); !errors.Is(err, ErrPayloadTooLarge) {
		t.Fatalf("oversize error=%v", err)
	}
}

func TestChatRequestClearOverwritesRetainedValues(t *testing.T) {
	request, err := DecodeChatRequest(strings.NewReader(`{"model":"p/m","messages":[{"content":"sensitive-content"}]}`), MaxRequestBodyBytes)
	if err != nil {
		t.Fatal(err)
	}
	var retained []byte
	for _, field := range request.fields {
		if field.name == "messages" {
			retained = field.value
		}
	}
	if len(retained) == 0 {
		t.Fatal("messages field was not retained")
	}
	request.Clear()
	for i, value := range retained {
		if value != 0 {
			t.Fatalf("retained byte %d was not cleared", i)
		}
	}
}
