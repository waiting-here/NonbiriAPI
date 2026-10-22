package requestadaptation

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"testing"
)

func TestConfigurationProjectionPreservesJSONAndMasksProtectedItems(t *testing.T) {
	store, database := testStore(t)
	doc := Empty(ScopeEndpoint)
	doc.FixedHeaders.Values = map[string]string{"X-Plain": "configured-header", "X-Empty": "", "X-Protected": "protected-header"}
	doc.FixedHeaders.SensitivePaths = map[string][]string{"X-Protected": {""}}
	values := map[string]json.RawMessage{
		"/text":      json.RawMessage(`"configured-body"`),
		"/empty":     json.RawMessage(`""`),
		"/false":     json.RawMessage(`false`),
		"/true":      json.RawMessage(`true`),
		"/null":      json.RawMessage(`null`),
		"/zero":      json.RawMessage(`0`),
		"/number":    json.RawMessage(`9007199254740993`),
		"/fraction":  json.RawMessage(`1.23e-4`),
		"/array":     json.RawMessage(`[false,null,"",{"nested":1}]`),
		"/object":    json.RawMessage(`{"nested":{"flag":false},"list":[]}`),
		"/protected": json.RawMessage(`"protected-body"`),
		"/partial":   json.RawMessage(`{"token":"protected-token","mode":"also-hidden"}`),
	}
	for _, section := range []*BodySection{&doc.BodyDefaults, &doc.BodyForced} {
		section.Values = cloneBody(values)
		section.SensitivePaths = map[string][]string{"/protected": {""}, "/partial": {"/token"}}
	}
	tx, err := database.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	safe, err := store.SaveTx(t.Context(), tx, Ref{Scope: ScopeEndpoint, ID: 1}, 0, doc, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	snapshot, err := store.Load(t.Context(), Ref{Scope: ScopeEndpoint, ID: 1})
	if err != nil {
		t.Fatal(err)
	}
	view := snapshot.ConfigurationProjection()
	snapshot.Clear()
	doc.Clear()
	if view.Revision != "1" {
		t.Fatalf("revision = %s", view.Revision)
	}
	for name, want := range map[string]string{"X-Plain": `"configured-header"`, "X-Empty": `""`} {
		value := view.FixedHeaders.Values[name]
		if !value.HasValue || value.Mask != "" || string(value.Value) != want {
			t.Fatalf("header %s did not preserve its value", name)
		}
	}
	if value := view.FixedHeaders.Values["X-Protected"]; !value.HasValue || value.Mask == "" || value.Value != nil {
		t.Fatal("protected header exposed")
	}
	for _, section := range []ConfigurationMapProjection{view.BodyDefaults, view.BodyForced} {
		for key, want := range values {
			value := section.Values[key]
			wire, err := json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(wire, &fields); err != nil {
				t.Fatal(err)
			}
			raw, exists := fields["value"]
			if key == "/protected" || key == "/partial" {
				if exists || value.Mask == "" || !value.HasValue {
					t.Fatalf("protected item %s exposed", key)
				}
			} else if !exists || !bytes.Equal(raw, want) || !value.HasValue || value.Mask != "" {
				t.Fatalf("item %s lost its JSON value", key)
			}
		}
	}
	safeWire, _ := json.Marshal(safe)
	var ciphertext, structure string
	if err := database.QueryRow(`SELECT secret_ciphertext,structure_json FROM request_adaptations WHERE endpoint_id=1`).Scan(&ciphertext, &structure); err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	logger.Info("configuration", "configuration", view, "partition", view.BodyDefaults, "value", view.BodyDefaults.Values["/text"])
	formatted := fmt.Sprintf("%v %+v %#v %+v", view, view, view, view.FixedHeaders.Values)
	for _, text := range []string{"configured-header", "configured-body", "protected-header", "protected-body", "protected-token", "also-hidden"} {
		if strings.Contains(string(safeWire)+ciphertext+structure+logs.String()+formatted, text) {
			t.Fatalf("value escaped safe persistence or formatting: %s", text)
		}
	}
}

func TestConfigurationProjectionProtectionChangesKeepAndClearValues(t *testing.T) {
	doc := Empty(ScopeEndpoint)
	apply := func(raw string) ConfigurationProjection {
		t.Helper()
		patch, err := ParsePatch([]byte(raw))
		if err != nil {
			t.Fatal(err)
		}
		next, err := ApplyPatch(doc, patch, ScopeEndpoint)
		if err != nil {
			t.Fatal(err)
		}
		doc.Clear()
		doc = next
		return (Snapshot{Document: doc, Revision: "1"}).ConfigurationProjection()
	}
	defer func() { doc.Clear() }()
	view := apply(`{"expected_revision":"0","fixed_headers":{"mode":"replace","values":{"X-Token":{"action":"replace","value":"retained-header"}}},"body_forced":{"mode":"replace","values":{"/vendor":{"action":"replace","value":{"token":"retained-token","flag":false}}}}}`)
	if string(view.FixedHeaders.Values["X-Token"].Value) != `"retained-header"` || len(view.BodyForced.Values["/vendor"].Value) == 0 {
		t.Fatal("unprotected values missing")
	}
	protect := `{"expected_revision":"1","fixed_headers":{"mode":"replace","values":{"X-Token":{"action":"keep","sensitive_paths":[""]}}},"body_forced":{"mode":"replace","values":{"/vendor":{"action":"keep","sensitive_paths":["/token"]}}}}`
	view = apply(protect)
	if view.FixedHeaders.Values["X-Token"].Value != nil || view.BodyForced.Values["/vendor"].Value != nil {
		t.Fatal("protected values visible")
	}
	view = apply(`{"expected_revision":"1","fixed_headers":{"mode":"replace","values":{"X-Token":{"action":"keep"}}},"body_forced":{"mode":"replace","values":{"/vendor":{"action":"keep"}}}}`)
	if view.FixedHeaders.Values["X-Token"].Value != nil || view.BodyForced.Values["/vendor"].Value != nil {
		t.Fatal("keep removed protection")
	}
	view = apply(`{"expected_revision":"1","fixed_headers":{"mode":"replace","values":{"X-Token":{"action":"keep","sensitive_paths":[]}}},"body_forced":{"mode":"replace","values":{"/vendor":{"action":"keep","sensitive_paths":[]}}}}`)
	if string(view.FixedHeaders.Values["X-Token"].Value) != `"retained-header"` || string(view.BodyForced.Values["/vendor"].Value) != `{"token":"retained-token","flag":false}` {
		t.Fatal("unprotect did not restore saved values")
	}
	apply(protect)
	view = apply(`{"expected_revision":"1","fixed_headers":{"mode":"replace","values":{"X-Token":{"action":"clear"}}},"body_forced":{"mode":"replace","values":{"/vendor":{"action":"clear"}}}}`)
	if len(view.FixedHeaders.Values) != 0 || len(view.BodyForced.Values) != 0 {
		t.Fatal("clear retained saved values")
	}
}
