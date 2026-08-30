package strictjson

import (
	"errors"
	"strconv"
	"strings"
	"testing"
)

func TestValidateObjectAcceptsOneUTF8Object(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{name: "empty object", input: `{}`},
		{
			name:  "unicode object",
			input: `{"名称":"石头剪刀布","emoji":"😀","nested":{"enabled":true,"missing":null},"values":[0,-12,3.14]}`,
		},
		{name: "trailing whitespace", input: "{\"ok\":true} \n\t\r"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			wantValid(t, []byte(tc.input))
		})
	}
}

func TestValidateObjectRequiresExactlyOneObjectDocument(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{name: "empty input", input: ""},
		{name: "whitespace only", input: " \n\t\r"},
		{name: "array root", input: `[]`},
		{name: "string root", input: `"text"`},
		{name: "number root", input: `42`},
		{name: "negative number root", input: `-0.5`},
		{name: "true root", input: `true`},
		{name: "false root", input: `false`},
		{name: "null root", input: `null`},
		{name: "second object", input: `{"first":1}{"second":2}`},
		{name: "second scalar", input: `{"first":1} null`},
		{name: "trailing garbage", input: `{"first":1} trailing`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			wantInvalid(t, []byte(tc.input))
		})
	}
}

func TestValidateObjectRejectsDuplicateDecodedKeys(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{name: "same object", input: `{"key":1,"key":2}`},
		{name: "nested object", input: `{"outer":{"key":1,"key":2}}`},
		{name: "escaped equivalent", input: `{"a":1,"\u0061":2}`},
		{name: "escaped equivalent nested", input: `{"outer":{"\u006b":1,"k":2}}`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			wantInvalid(t, []byte(tc.input))
		})
	}
}

func TestValidateObjectDepthLimit(t *testing.T) {
	wantValid(t, []byte(nestedObjects(MaxDepth)))
	wantInvalid(t, []byte(nestedObjects(MaxDepth+1)))
}

func TestValidateObjectCountsFieldsAcrossNestedObjects(t *testing.T) {
	wantValid(t, []byte(objectWithNestedFields(MaxFields)))
	wantInvalid(t, []byte(objectWithNestedFields(MaxFields+1)))
}

func TestValidateObjectLimitsEachArrayIndependently(t *testing.T) {
	wantValid(t, []byte(objectWithArray(MaxArrayElements)))
	wantInvalid(t, []byte(objectWithArray(MaxArrayElements+1)))

	// Both inner arrays reach the limit, proving that the bound is per array
	// rather than a single budget shared by all arrays in the document.
	wantValid(t, []byte(objectWithNestedArrays(MaxArrayElements, 2)))
	wantInvalid(t, []byte(objectWithNestedArrays(MaxArrayElements+1, 1)))
}

func TestValidateObjectSurrogateAndEscapeRules(t *testing.T) {
	valid := []struct {
		name  string
		input string
	}{
		{name: "valid surrogate pair", input: `{"text":"\uD83D\uDE00"}`},
		// The JSON escape is \\ (a literal backslash), followed by uD800
		// characters; it does not decode to a surrogate code point.
		{name: "escaped surrogate text", input: `{"text":"\\uD800"}`},
		{name: "ordinary escapes", input: `{"text":"quote: \" slash: \\ tab: \t"}`},
	}
	for _, tc := range valid {
		t.Run("valid/"+tc.name, func(t *testing.T) {
			wantValid(t, []byte(tc.input))
		})
	}

	invalid := []struct {
		name  string
		input []byte
	}{
		{name: "isolated high surrogate", input: []byte(`{"text":"\uD800"}`)},
		{name: "isolated low surrogate", input: []byte(`{"text":"\uDC00"}`)},
		{name: "high followed by non-low", input: []byte(`{"text":"\uD800\u0041"}`)},
		{name: "high followed by raw text", input: []byte(`{"text":"\uD800A"}`)},
		{name: "high at end", input: []byte(`{"text":"\uD800`)},
		{name: "invalid short escape", input: []byte(`{"text":"\u123"}`)},
		{name: "invalid hex escape", input: []byte(`{"text":"\u12G4"}`)},
		{name: "invalid letter escape", input: []byte(`{"text":"\q"}`)},
		{name: "trailing backslash", input: []byte(`{"text":"abc\`)},
		{name: "raw control", input: []byte("{\"text\":\"\x01\"}")},
	}
	for _, tc := range invalid {
		t.Run("invalid/"+tc.name, func(t *testing.T) {
			wantInvalid(t, tc.input)
		})
	}
}

func TestValidateObjectRejectsInvalidUTF8(t *testing.T) {
	input := append([]byte(`{"text":"`), 0xff, 0xfe)
	input = append(input, []byte(`"}`)...)
	wantInvalid(t, input)
}

func TestValidateObjectRejectsTruncatedDocuments(t *testing.T) {
	tests := []string{
		`{`,
		`{"key":`,
		`{"key":1`,
		`{"key":[1,2}`,
		`{"key":{"nested":true}`,
		`{"key":"unterminated}`,
	}
	for _, input := range tests {
		wantInvalid(t, []byte(input))
	}
}

func TestValidateObjectAcceptsComplexObject(t *testing.T) {
	input := []byte(`{
		"kind":"rps",
		"moves":["rock","paper","scissors"],
		"unicode":"中文 / é / 😀",
		"escaped":"\\uD800",
		"pair":"\uD834\uDD1E",
		"numbers":[-0,0,1e+6,3.1415],
		"nested":{"flag":false,"nothing":null,"array":[{},[]]}
	}`)
	wantValid(t, input)
}

func wantValid(t *testing.T, input []byte) {
	t.Helper()
	if err := ValidateObject(input); err != nil {
		t.Fatalf("ValidateObject(%s) returned %v, want nil", preview(input), err)
	}
}

func wantInvalid(t *testing.T, input []byte) {
	t.Helper()
	if err := ValidateObject(input); !errors.Is(err, ErrInvalid) {
		t.Fatalf("ValidateObject(%s) returned %v, want %v", preview(input), err, ErrInvalid)
	}
}

func nestedObjects(depth int) string {
	var b strings.Builder
	for i := 0; i < depth; i++ {
		b.WriteString(`{"next":`)
	}
	b.WriteByte('0')
	for i := 0; i < depth; i++ {
		b.WriteByte('}')
	}
	return b.String()
}

func objectWithNestedFields(total int) string {
	if total < 2 {
		panic("total must include the outer nested field")
	}
	var b strings.Builder
	b.WriteString(`{"nested":{`)
	for i := 0; i < total-1; i++ {
		if i != 0 {
			b.WriteByte(',')
		}
		b.WriteString(`"field`)
		b.WriteString(strconv.Itoa(i))
		b.WriteString(`":0`)
	}
	b.WriteString(`}}`)
	return b.String()
}

func objectWithArray(elements int) string {
	var b strings.Builder
	b.WriteString(`{"items":[`)
	for i := 0; i < elements; i++ {
		if i != 0 {
			b.WriteByte(',')
		}
		b.WriteByte('0')
	}
	b.WriteString(`]}`)
	return b.String()
}

func objectWithNestedArrays(innerElements, outerElements int) string {
	var b strings.Builder
	b.WriteString(`{"items":[`)
	for outer := 0; outer < outerElements; outer++ {
		if outer != 0 {
			b.WriteByte(',')
		}
		b.WriteByte('[')
		for inner := 0; inner < innerElements; inner++ {
			if inner != 0 {
				b.WriteByte(',')
			}
			b.WriteByte('0')
		}
		b.WriteByte(']')
	}
	b.WriteString(`]}`)
	return b.String()
}

func preview(input []byte) string {
	const maxPreview = 96
	if len(input) > maxPreview {
		return strconv.Quote(string(input[:maxPreview])) + "..."
	}
	return strconv.Quote(string(input))
}
