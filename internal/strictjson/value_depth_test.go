package strictjson

import (
	"errors"
	"strings"
	"testing"
)

func TestValidateObjectValueDepthBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name  string
		input string
		depth int
		valid bool
	}{
		{"root", `{}`, 1, true},
		{"scalar child", `{"a":0}`, 1, false},
		{"scalar at limit", `{"a":0}`, 2, true},
		{"empty object at limit", `{"a":{}}`, 2, true},
		{"empty array at limit", `{"a":[]}`, 2, true},
		{"array child over limit", `{"a":[null]}`, 2, false},
		{"object child over limit", `{"a":{"b":false}}`, 2, false},
		{"mixed at limit", `{"a":[{"b":true}]}`, 4, true},
		{"mixed over limit", `{"a":[{"b":true}]}`, 3, false},
		{"escaped delimiters", `{"a":"[{\\\"}]"}`, 2, true},
		{"deep scalar at limit", nestedObjects(MaxDepth - 1), MaxDepth, true},
		{"deep scalar over limit", nestedObjects(MaxDepth), MaxDepth, false},
		{"zero limit", `{}`, 0, false},
		{"oversized limit", `{}`, MaxDepth + 1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateObjectWithValueDepthLimit([]byte(tc.input), MaxFields, tc.depth)
			if (err == nil) != tc.valid || err != nil && !errors.Is(err, ErrInvalid) {
				t.Fatalf("valid=%t error=%v", tc.valid, err)
			}
		})
	}
	// Existing validators count containers and keep their previous boundary.
	wantValid(t, []byte(nestedObjects(MaxDepth)))
}

func TestValidateObjectValueDepthPreservesSyntaxLimits(t *testing.T) {
	for _, input := range [][]byte{
		[]byte(`{"a":0,"\u0061":1}`),
		[]byte(`{"a":[{"b":0,"b":1}]}`),
		[]byte(`{} {}`), []byte(`{} true`), []byte(`{"a":01}`),
		[]byte(`{"a":NaN}`), []byte(`{"a":1.}`),
		[]byte(`{"a":"\uD800"}`), []byte(`{"a":"\q"}`),
		[]byte("{\"a\":\"\xff\"}"),
		[]byte(objectWithNestedFields(MaxFields + 1)),
		[]byte(objectWithArray(MaxArrayElements + 1)),
	} {
		if err := ValidateObjectWithValueDepthLimit(input, MaxFields, MaxDepth); !errors.Is(err, ErrInvalid) {
			t.Fatalf("accepted invalid input %s", preview(input))
		}
	}
	for _, input := range []string{
		`{"a":1e9999,"b":-0,"s":"\uD83D\uDE00"}`,
		objectWithNestedFields(MaxFields), objectWithArray(MaxArrayElements),
		strings.Repeat(" ", 10) + `{}` + "\n",
	} {
		if err := ValidateObjectWithValueDepthLimit([]byte(input), MaxFields, MaxDepth); err != nil {
			t.Fatalf("rejected valid input %s", preview([]byte(input)))
		}
	}
	for _, fields := range []int{0, 16385} {
		if err := ValidateObjectWithValueDepthLimit([]byte(`{}`), fields, 1); !errors.Is(err, ErrInvalid) {
			t.Fatalf("accepted invalid field limit %d", fields)
		}
	}
}
