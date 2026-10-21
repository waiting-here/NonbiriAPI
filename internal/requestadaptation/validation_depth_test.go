package requestadaptation

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/strictjson"
)

// Retain the previous independent tree walk as an equivalence oracle.
func treeValidatedJSON(data []byte) bool {
	if len(data) == 0 || len(data) > MaxConfigurationBytes || strictjson.ValidateObjectWithFieldLimit(data, 16384) != nil {
		return false
	}
	var value any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if decoder.Decode(&value) != nil {
		return false
	}
	var bounded func(any, int) bool
	bounded = func(value any, depth int) bool {
		if depth > MaxDepth {
			return false
		}
		switch v := value.(type) {
		case map[string]any:
			for _, child := range v {
				if !bounded(child, depth+1) {
					return false
				}
			}
		case []any:
			for _, child := range v {
				if !bounded(child, depth+1) {
					return false
				}
			}
		}
		return true
	}
	return bounded(value, 1)
}

func TestConfigurationDepthValidationEquivalent(t *testing.T) {
	inputs := []string{
		``, `{}`, `null`, `[]`, `{"x":1e9999}`, `{"x":"[\\\"]}"}`,
		`{"a":1,"\u0061":2}`, `{"x":{"a":1,"a":2}}`, `{} null`,
		`{"x":"\uD800"}`, "{\"x\":\"\xff\"}", `{"x":00}`, `{"x":true,}`,
		`{"x":[` + strings.Repeat(`0,`, strictjson.MaxArrayElements) + `0]}`,
		`{"x":"` + strings.Repeat("x", MaxConfigurationBytes-8) + `"}`,
		`{"x":"` + strings.Repeat("x", MaxConfigurationBytes-7) + `"}`,
	}
	for depth := 1; depth <= MaxDepth+2; depth++ {
		for _, leaf := range []string{`0`, `null`, `true`, `"text"`, `{}`, `[]`} {
			inputs = append(inputs, strings.Repeat(`{"x":`, depth)+leaf+strings.Repeat(`}`, depth))
			inputs = append(inputs, `{"x":`+strings.Repeat(`[`, depth)+leaf+strings.Repeat(`]`, depth)+`}`)
		}
	}
	for i, input := range inputs {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			data := []byte(input)
			if got, want := validJSON(data), treeValidatedJSON(data); got != want {
				t.Fatalf("valid=%t, tree walk=%t, bytes=%d", got, want, len(data))
			}
		})
	}
}

func TestDocumentRetainsEncodedSizeAndDepthLimits(t *testing.T) {
	d := Empty(ScopeEndpoint)
	d.BodyForced.Values["/options"] = json.RawMessage(`"` + strings.Repeat("<", MaxConfigurationBytes/6) + `"`)
	if err := ValidateDocument(d, ScopeEndpoint); err == nil {
		t.Fatal("accepted document whose escaped encoding exceeds size limit")
	}
	// Document, section and values occupy three levels before the raw value.
	d.BodyForced.Values["/options"] = json.RawMessage(strings.Repeat(`[`, MaxDepth-4) + `0` + strings.Repeat(`]`, MaxDepth-4))
	if err := ValidateDocument(d, ScopeEndpoint); err != nil {
		t.Fatal(err)
	}
	d.BodyForced.Values["/options"] = json.RawMessage(strings.Repeat(`[`, MaxDepth-3) + `0` + strings.Repeat(`]`, MaxDepth-3))
	if err := ValidateDocument(d, ScopeEndpoint); err == nil {
		t.Fatal("accepted oversized value depth")
	}
}
