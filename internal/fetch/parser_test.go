package fetch

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func modelsJSON(entries ...string) string {
	return `{"object":"list","data":[` + strings.Join(entries, ",") + `]}`
}

func entry(id, ownedBy string) string {
	if ownedBy == "" {
		return fmt.Sprintf(`{"id":%q,"object":"model"}`, id)
	}
	return fmt.Sprintf(`{"id":%q,"object":"model","owned_by":%q}`, id, ownedBy)
}

// TestParseModelsHappyPath asserts a well-formed list parses with trimmed
// id/provider and a stable order.
func TestParseModelsHappyPath(t *testing.T) {
	body := modelsJSON(
		entry("gpt-4o", "system"),
		entry("  ft:gpt-4o:org::custom  ", "   my-org   "),
		entry("gpt-4o-mini", ""),
	)
	models, err := parseModels([]byte(body))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(models) != 3 {
		t.Fatalf("models = %+v", models)
	}
	if models[0] != (Model{ID: "gpt-4o", Provider: "system"}) {
		t.Errorf("models[0] = %+v", models[0])
	}
	if models[1] != (Model{ID: "ft:gpt-4o:org::custom", Provider: "my-org"}) {
		t.Errorf("models[1] = %+v", models[1])
	}
	if models[2] != (Model{ID: "gpt-4o-mini", Provider: ""}) {
		t.Errorf("models[2] = %+v", models[2])
	}
}

// TestParseModelsEmptyList asserts an empty data array is a valid (empty)
// result, not a failure.
func TestParseModelsEmptyList(t *testing.T) {
	models, err := parseModels([]byte(`{"object":"list","data":[]}`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(models) != 0 {
		t.Errorf("models = %+v, want empty", models)
	}
}

// TestParseModelsRejectsBadShape exercises every shape violation: non-object
// roots, missing/null/non-array data, non-string ids, and trailing tokens.
func TestParseModelsRejectsBadShape(t *testing.T) {
	cases := map[string]string{
		"array root":       `[{"id":"a"}]`,
		"string root":      `"nope"`,
		"number root":      `42`,
		"missing data":     `{"object":"list"}`,
		"null data":        `{"data":null}`,
		"object data":      `{"data":{"id":"a"}}`,
		"number data":      `{"data":5}`,
		"non-string id":    modelsJSON(`{"id":42}`),
		"missing id field": modelsJSON(`{"object":"model"}`),
		"trailing token":   `{"data":[]} extra`,
		"two objects":      `{"data":[]}{"data":[]}`,
		"raw garbage":      `not json at all`,
	}
	for name, body := range cases {
		if _, err := parseModels([]byte(body)); err == nil {
			t.Errorf("%s: parse accepted %q", name, body)
		}
	}
}

// TestParseModelsRejectsDuplicateIds asserts a duplicate id (after trimming)
// rejects the whole list.
func TestParseModelsRejectsDuplicateIds(t *testing.T) {
	for name, body := range map[string]string{
		"exact duplicate": modelsJSON(entry("gpt-4o", "s"), entry("gpt-4o", "s")),
		"trim duplicate":  modelsJSON(entry("gpt-4o", "s"), entry("  gpt-4o  ", "s")),
	} {
		_, err := parseModels([]byte(body))
		if !errors.Is(err, errDuplicateModelID) {
			t.Errorf("%s: err=%v, want duplicate", name, err)
		}
	}
}

// TestParseModelsRejectsEmptyAndInvalidIds asserts empty (incl. whitespace
// only), control-containing, DEL, and U+FFFD ids are rejected.
func TestParseModelsRejectsEmptyAndInvalidIds(t *testing.T) {
	cases := []string{
		modelsJSON(entry("", "s")),
		modelsJSON(entry("   ", "s")),
		modelsJSON(entry("gpt\t4o", "s")),
		modelsJSON(entry("gpt\n4o", "s")),
		modelsJSON(entry("gpt\x00o", "s")),
		modelsJSON(entry("gpt\x7fo", "s")),
		modelsJSON(entry("gpt\uFFFD4o", "s")),
		modelsJSON(entry("gpt-4o", "sys\tem")),
		modelsJSON(entry("gpt-4o", "s\uFFFD")),
	}
	for i, body := range cases {
		if _, err := parseModels([]byte(body)); err == nil {
			t.Errorf("case %d: parse accepted %q", i, body)
		}
	}
}

// TestParseModelsBoundaries asserts the count, id-length, provider-length, and
// body-byte ceilings, and that exactly-at-limit values are accepted.
func TestParseModelsBoundaries(t *testing.T) {
	// Exactly MaxModels entries is accepted; one more is rejected.
	var exactly strings.Builder
	exactly.WriteString(`{"data":[`)
	for i := 0; i < MaxModels; i++ {
		if i > 0 {
			exactly.WriteByte(',')
		}
		fmt.Fprintf(&exactly, `{"id":"m%d"}`, i)
	}
	exactly.WriteString(`]}`)
	if _, err := parseModels([]byte(exactly.String())); err != nil {
		t.Errorf("exactly MaxModels: %v", err)
	}

	over := `{"data":[` + strings.Repeat(`{"id":"m"},`, MaxModels) + `{"id":"last"}]}`
	if _, err := parseModels([]byte(over)); !errors.Is(err, errTooManyModels) {
		t.Errorf("MaxModels+1: err=%v, want too many", err)
	}

	// Exactly MaxModelIDRunes accepted; one more rune rejected.
	okID := strings.Repeat("a", MaxModelIDRunes)
	if _, err := parseModels([]byte(modelsJSON(entry(okID, "s")))); err != nil {
		t.Errorf("id at limit: %v", err)
	}
	if _, err := parseModels([]byte(modelsJSON(entry(okID+"a", "s")))); !errors.Is(err, errModelIDTooLong) {
		t.Errorf("id over limit: err=%v, want too long", err)
	}

	okProvider := strings.Repeat("b", MaxProviderRunes)
	if _, err := parseModels([]byte(modelsJSON(entry("gpt-4o", okProvider)))); err != nil {
		t.Errorf("provider at limit: %v", err)
	}
	if _, err := parseModels([]byte(modelsJSON(entry("gpt-4o", okProvider+"b")))); !errors.Is(err, errProviderTooLong) {
		t.Errorf("provider over limit: err=%v, want too long", err)
	}

	// Byte ceiling: a body larger than MaxModelsBodyBytes is truncated.
	if _, err := parseModels([]byte(`{"data":[{"id":"` + strings.Repeat("a", MaxModelsBodyBytes) + `"}]}`)); !errors.Is(err, errTruncatedBody) {
		t.Errorf("oversized body: err=%v, want truncated", err)
	}
}

// TestParseModelsMultibyteIDs asserts valid non-ASCII identifiers survive
// (they are opaque strings, not ASCII-only), bounded by runes not bytes.
func TestParseModelsMultibyteIDs(t *testing.T) {
	id := strings.Repeat("模型", MaxModelIDRunes/2) // 2 runes each, byte-heavy
	if len(id) > MaxModelsBodyBytes {
		t.Fatalf("test id too large: %d bytes", len(id))
	}
	models, err := parseModels([]byte(modelsJSON(entry(id, "测试"))))
	if err != nil {
		t.Fatalf("parse multibyte: %v", err)
	}
	if models[0].ID != id || models[0].Provider != "测试" {
		t.Errorf("multibyte round trip = %+v", models)
	}
	// Rune bound: (MaxModelIDRunes/2)+1 multibyte pairs exceeds the rune cap.
	overID := strings.Repeat("模型", MaxModelIDRunes/2+1)
	if len(overID) > MaxModelsBodyBytes {
		t.Fatalf("test id too large: %d bytes", len(overID))
	}
	if _, err := parseModels([]byte(modelsJSON(entry(overID, "s")))); !errors.Is(err, errModelIDTooLong) {
		t.Errorf("multibyte over rune limit: err=%v, want too long", err)
	}
}
