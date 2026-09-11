package openai

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestEmbeddingInputAndParameterContract(t *testing.T) {
	for _, tc := range []struct {
		input string
		count int
	}{
		{`" "`, 1}, {`["one","two"]`, 2}, {`[0,2147483647,7]`, 1}, {`[[1,2],[3]]`, 2},
		{"[" + strings.Repeat("1,", MaxEmbeddingBatch) + "1]", 1},
		{"[" + strings.Repeat(`"a",`, MaxEmbeddingBatch-1) + `"a"]`, MaxEmbeddingBatch},
	} {
		for _, encoding := range []string{"float", "base64"} {
			t.Run(fmt.Sprintf("%d/%s/%d", len(tc.input), encoding, tc.count), func(t *testing.T) {
				body := `{"model":"public/model","input":` + tc.input + `,"encoding_format":"` + encoding + `","dimensions":3,"user":"caller","stream":false,"store":true,"vendor":{"nested":[1,{"enabled":true}]},"safety_identifier":"caller-secret"}`
				r, err := DecodeEmbeddingRequest(strings.NewReader(body), MaxRequestBodyBytes)
				if err != nil {
					t.Fatal(err)
				}
				defer r.Clear()
				if r.InputCount != tc.count || r.EncodingFormat != encoding || r.Dimensions != 3 {
					t.Fatalf("metadata: %d %s %d", r.InputCount, r.EncodingFormat, r.Dimensions)
				}
				clone := r.CloneForAttempt()
				clone.Clear()
				wire, err := r.marshalUpstream("private/physical", "anonymous_origin")
				if err != nil {
					t.Fatal(err)
				}
				defer clear(wire)
				var decoded map[string]json.RawMessage
				if err := json.Unmarshal(wire, &decoded); err != nil {
					t.Fatal(err)
				}
				for field, want := range map[string]string{"model": `"private/physical"`, "user": `"anonymous_origin"`, "safety_identifier": `"anonymous_origin"`, "store": "true", "stream": "false", "dimensions": "3", "input": tc.input} {
					if string(decoded[field]) != want {
						t.Fatalf("%s=%s want %s", field, decoded[field], want)
					}
				}
				if string(decoded["vendor"]) != `{"nested":[1,{"enabled":true}]}` {
					t.Fatal("unknown field changed")
				}
				if strings.Contains(fmt.Sprintf("%v %#v", r, r), "caller-secret") {
					t.Fatal("request formatting exposed input")
				}
			})
		}
	}
}

func TestEmbeddingRejectsInvalidInputsAndNestedDuplicates(t *testing.T) {
	for _, input := range []string{`null`, `""`, `[]`, `[""]`, `[[]]`, `[[1],[]]`, `["a",1]`, `[1,"a"]`, `[[1],"a"]`, `[[1],[null]]`, `[-1]`, `[1.0]`, `[1e0]`, `[2147483648]`, `[true]`, `{"text":"a"}`} {
		if r, err := DecodeEmbeddingRequest(strings.NewReader(`{"model":"p/m","input":`+input+`}`), MaxRequestBodyBytes); err == nil {
			r.Clear()
			t.Fatalf("accepted input %s", input)
		}
	}
	for _, extra := range []string{`,"stream":true`, `,"stream":null`, `,"encoding_format":null`, `,"encoding_format":"bytes"`, `,"dimensions":null`, `,"dimensions":0`, `,"dimensions":1e0`, `,"dimensions":2147483648`, `,"user":null`, `,"user":"\u0000"`, `,"user":"` + strings.Repeat("a", 513) + `"`, `,"vendor":{"key":1,"k\u0065y":2}`, `,"vendor":[{"x":1,"x":2}]`, `,"model":"second"`} {
		if r, err := DecodeEmbeddingRequest(strings.NewReader(`{"model":"p/m","input":"a"`+extra+`}`), MaxRequestBodyBytes); err == nil {
			r.Clear()
			t.Fatalf("accepted fields %s", extra)
		}
	}
	for _, body := range []string{`[]`, `null`, `{}`, `{"input":"a"}`, `{"model":"p/m"}`, `{"model":"p/m","input":"a"}{}`, "{\"model\":\"p/m\",\"input\":\"\xff\"}"} {
		if r, err := DecodeEmbeddingRequest(strings.NewReader(body), MaxRequestBodyBytes); err == nil {
			r.Clear()
			t.Fatal("accepted invalid envelope")
		}
	}
	tooMany := `{"model":"p/m","input":[` + strings.Repeat(`"a",`, MaxEmbeddingBatch) + `"a"]}`
	if _, err := DecodeEmbeddingRequest(strings.NewReader(tooMany), MaxRequestBodyBytes); err == nil {
		t.Fatal("batch overflow accepted")
	}
	deep := `{"model":"p/m","input":"a","extra":` + strings.Repeat("[", 64) + "0" + strings.Repeat("]", 64) + "}"
	if _, err := DecodeEmbeddingRequest(strings.NewReader(deep), MaxRequestBodyBytes); err == nil {
		t.Fatal("depth overflow accepted")
	}
	if _, err := DecodeEmbeddingRequest(bytes.NewReader(bytes.Repeat([]byte(" "), int(MaxRequestBodyBytes)+1)), MaxRequestBodyBytes); !errors.Is(err, ErrPayloadTooLarge) {
		t.Fatalf("body limit: %v", err)
	}
}

func TestEmbeddingDefaultsIdentityAndClear(t *testing.T) {
	r, err := DecodeEmbeddingRequest(strings.NewReader(`{"model":"p/m","input":"a"}`), 0)
	if err != nil {
		t.Fatal(err)
	}
	if r.EncodingFormat != "float" || r.Dimensions != 0 {
		t.Fatal("wrong defaults")
	}
	clone := r.CloneForAttempt()
	defer clone.Clear()
	retained := r.fields[0].value
	r.Clear()
	if !bytes.Equal(retained, make([]byte, len(retained))) || r.InputCount != 0 {
		t.Fatal("clear retained request state")
	}
	wire, err := clone.marshalUpstream("physical", "anonymous")
	if err != nil {
		t.Fatal(err)
	}
	defer clear(wire)
	if string(wire) != `{"model":"physical","input":"a","user":"anonymous"}` {
		t.Fatalf("default wire=%s", wire)
	}
	if _, err := clone.marshalUpstream("physical", "bad\nidentity"); err == nil {
		t.Fatal("unsafe identity accepted")
	}
}
