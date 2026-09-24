package openai

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/connector/contract"
)

func TestRequestEnvelopeFiltersBeforeOptionalValidation(t *testing.T) {
	input := []byte(`{"model":"[公益]demo/embed","input":"private input","dimensions":"unsupported","user":{"not":"a string"},"custom":7}`)
	original := bytes.Clone(input)
	if _, err := DecodeEmbeddingRequest(bytes.NewReader(input), MaxRequestBodyBytes); err == nil {
		t.Fatal("unfiltered invalid optional fields accepted")
	}
	envelope, err := DecodeRequestEnvelope(bytes.NewReader(input), MaxRequestBodyBytes, contract.OperationEmbeddings)
	if err != nil {
		t.Fatal(err)
	}
	defer envelope.Clear()
	names := []string{"dimensions", "user"}
	filtered, err := envelope.WithoutFields(names)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(filtered)
	request, err := DecodeEmbeddingRequest(bytes.NewReader(filtered), MaxRequestBodyBytes)
	if err != nil {
		t.Fatal(err)
	}
	defer request.Clear()
	if err = request.ExcludeFields(names); err != nil {
		t.Fatal(err)
	}
	clone := request.CloneForAttempt()
	request.Clear()
	defer clone.Clear()
	forwarded, err := clone.marshalUpstream("upstream-model", "safe-identifier")
	if err != nil {
		t.Fatal(err)
	}
	defer clear(forwarded)
	var fields map[string]json.RawMessage
	if json.Unmarshal(forwarded, &fields) != nil {
		t.Fatal("invalid forwarded JSON")
	}
	for _, excluded := range names {
		if _, exists := fields[excluded]; exists {
			t.Fatalf("reintroduced excluded %s", excluded)
		}
	}
	if !bytes.Equal(input, original) || string(fields["custom"]) != "7" || string(fields["model"]) != `"upstream-model"` {
		t.Fatal("original input or unrelated fields changed")
	}
}

func TestRequestEnvelopeRejectsMalformedCoreAndUnboundedInput(t *testing.T) {
	for _, input := range []string{
		`{"model":"a","input":null,"dimensions":"bad"}`,
		`{"model":"a","input":"x","encoding_format":"invalid"}`,
		`{"model":"a","input":"x","stream":true}`,
		`{"model":"a","input":"x","dimensions":1,"dimensions":2}`,
		`{"model":"a","input":"x","other":{"same":1,"same":2}}`,
		`{"model":"a","input":"x"} {}`,
		`[]`,
	} {
		if value, err := DecodeRequestEnvelope(strings.NewReader(input), MaxRequestBodyBytes, contract.OperationEmbeddings); err == nil {
			value.Clear()
			t.Fatalf("accepted invalid core JSON %q", input)
		}
	}
	if _, err := DecodeRequestEnvelope(strings.NewReader(strings.Repeat("x", int(MaxRequestBodyBytes)+1)), MaxRequestBodyBytes, contract.OperationChatCompletions); !errors.Is(err, ErrPayloadTooLarge) {
		t.Fatalf("body bound: %v", err)
	}
}

func TestExcludedRequestFieldsAreFiniteTopLevelNames(t *testing.T) {
	fields, err := NormalizeExcludedRequestFields([]string{"temperature", "temperature", "Temperature", "_custom1"})
	if err != nil || !reflect.DeepEqual(fields, []string{"temperature", "Temperature", "_custom1"}) {
		t.Fatalf("normalization: %v, %v", fields, err)
	}
	for _, name := range []string{"", "1x", "nested.field", "list[0]", "a-b", "中文", "model", "messages", "input", "stream", "stream_options", "tools", "tool_choice", "functions", "function_call", "response_format", "encoding_format", strings.Repeat("x", 65)} {
		if _, err := NormalizeExcludedRequestFields([]string{name}); err == nil {
			t.Fatalf("accepted invalid name %q", name)
		}
	}
	if _, err := NormalizeExcludedRequestFields(make([]string, 33)); err == nil {
		t.Fatal("accepted more than 32 fields")
	}
}

func TestChatExcludedDefaultsSurviveAttemptClones(t *testing.T) {
	request, err := DecodeChatRequest(strings.NewReader(`{"model":"[公益]demo/chat","messages":[{"role":"user","content":"private"}],"temperature":1}`), MaxRequestBodyBytes)
	if err != nil {
		t.Fatal(err)
	}
	defer request.Clear()
	if err = request.ExcludeFields([]string{"temperature", "store", "safety_identifier"}); err != nil {
		t.Fatal(err)
	}
	clone := request.CloneForAttempt()
	defer clone.Clear()
	request.Clear()
	for range 2 {
		body, err := clone.marshalUpstreamWithPolicy("upstream-model", "safe-identifier", contract.AttemptPolicy{ForceStoreFalse: true})
		if err != nil {
			t.Fatal(err)
		}
		for _, forbidden := range []string{`"temperature"`, `"store"`, `"safety_identifier"`} {
			if bytes.Contains(body, []byte(forbidden)) {
				t.Fatalf("reintroduced %s", forbidden)
			}
		}
		clear(body)
	}
}
