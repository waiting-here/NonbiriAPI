package openai

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
)

func TestParseModelsPreservesFirstProviderAndOrder(t *testing.T) {
	models, err := ParseModels([]byte(`{"data":[
 {"id":"  model-b  ","owned_by":""},
 {"id":"model-a","owned_by":"  first provider  "},
 {"id":"model-b","owned_by":"later"},
 {"id":" model-a ","owned_by":"different"},
 {"id":"model-c","owned_by":"last"}
 ]}`))
	want := []connectorcontract.DiscoveredModel{
		{ID: "model-b", Provider: ""}, {ID: "model-a", Provider: "first provider"}, {ID: "model-c", Provider: "last"},
	}
	if err != nil || !reflect.DeepEqual(models, want) {
		t.Fatalf("models=%+v err=%v", models, err)
	}
}

func TestParseModelsValidatesDuplicatesAndRawLimits(t *testing.T) {
	wire := func(entries []map[string]any) []byte {
		t.Helper()
		value, err := json.Marshal(map[string]any{"data": entries})
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	valid := map[string]any{"id": "model", "owned_by": "first"}
	for _, test := range []struct {
		name string
		row  map[string]any
		want error
	}{
		{"provider limit", map[string]any{"id": "model", "owned_by": strings.Repeat("p", 129)}, ErrProviderTooLong},
		{"provider control", map[string]any{"id": "model", "owned_by": "bad\nprovider"}, ErrInvalidProvider},
		{"discarded C1 control", map[string]any{"id": "model", "owned_by": "bad\u0085provider"}, ErrInvalidProvider},
		{"trimmed ID control", map[string]any{"id": "model\t"}, ErrInvalidModelID},
		{"ID bound before trimming", map[string]any{"id": strings.Repeat(" ", 512) + "model"}, ErrModelIDTooLong},
		{"invalid duplicate field type", map[string]any{"id": "model", "owned_by": 42}, ErrMalformedModelsJSON},
		{"empty normalized ID", map[string]any{"id": "  "}, ErrEmptyModelID},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := ParseModels(wire([]map[string]any{valid, test.row}))
			if !errors.Is(err, test.want) || got != nil {
				t.Fatalf("models=%+v err=%v", got, err)
			}
		})
	}
	entries := make([]map[string]any, 1000)
	for i := range entries {
		entries[i] = valid
	}
	got, err := ParseModels(wire(entries))
	if err != nil || len(got) != 1 {
		t.Fatalf("1000 originals: %+v %v", got, err)
	}
	if got, err := ParseModels(wire(append(entries, valid))); !errors.Is(err, ErrTooManyModels) || got != nil {
		t.Fatalf("1001 originals: %+v %v", got, err)
	}
	boundary := []map[string]any{{"id": strings.Repeat("界", 512), "owned_by": strings.Repeat("源", 128)}}
	if got, err := ParseModels(wire(boundary)); err != nil || len(got) != 1 {
		t.Fatalf("field boundaries: %+v %v", got, err)
	}
	body := []byte(`{"data":[]}`)
	body = append(body, []byte(strings.Repeat(" ", MaxModelsBodyBytes-len(body)))...)
	if _, err := ParseModels(body); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseModels(append(body, ' ')); !errors.Is(err, ErrModelsResponseTruncated) {
		t.Fatalf("body limit: %v", err)
	}
}
