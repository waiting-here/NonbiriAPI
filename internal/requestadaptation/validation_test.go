package requestadaptation

import (
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

func TestPatchRejectsAmbiguousOrUnsafeConfiguration(t *testing.T) {
	base := `{"expected_revision":"0","body_forced":{"mode":"replace","values":{"/temperature":{"action":"replace","value":0.4}}}}`
	if _, err := ParsePatch([]byte(base)); err != nil {
		t.Fatal(err)
	}
	for _, input := range []string{
		`{"expected_revision":"0","expected_revision":"1","forward_headers":{"mode":"replace","values":[]}}`,
		`{"expected_revision":"0","body_forced":null}`,
		`{"expected_revision":"0","body_forced":{"mode":"replace","values":{"/x":{"action":"replace","value":1,"secret":"leak"}}}}`,
		`{"expected_revision":"0","body_forced":{"mode":"replace","values":{"/x":{"action":"keep","value":1}}}}`,
		`{"expected_revision":"0","fixed_headers":{"mode":"replace","values":{"Authorization":{"action":"replace","value":"x"}}}}`,
		`{"expected_revision":"0","fixed_headers":{"mode":"replace","values":{"X-Research":{"action":"replace","value":null}}}}`,
		`{"expected_revision":"0","body_forced":{"mode":"replace","values":{"/messages":{"action":"replace","value":[]}}}}`,
		`{"expected_revision":"0","body_forced":{"mode":"replace","values":{"/temperature":{"action":"replace","value":1},"/temperature/x":{"action":"replace","value":2}}}}`,
		`{"expected_revision":"0","native_extension_paths":{"mode":"replace","values":["/max_tokens"]}}`,
		`{"expected_revision":"0","body_forced":{"mode":"replace","values":{"/max_tokens":{"action":"replace","value":0}}}}`,
		`{"expected_revision":"0","body_forced":{"mode":"replace","values":{"/max_tokens":{"action":"replace","value":"128"}}}}`,
		`{"expected_revision":"0","body_forced":{"mode":"replace","values":{"/temperature":{"action":"replace","value":"0.5"}}}}`,
		`{"expected_revision":"0","body_defaults":{"mode":"replace","values":{"/max_tokens":{"action":"replace","value":1e3}}}}`,
		`{"expected_revision":"0","body_defaults":{"mode":"replace","values":{"/max_completion_tokens":{"action":"replace","value":1.0}}}}`,
		`{"expected_revision":"0","body_forced":{"mode":"replace","values":{"/temperature":{"action":"replace","value":9}}}}`,
		`{"expected_revision":"0","body_defaults":{"mode":"replace","values":{"/temperature/nested":{"action":"replace","value":1}}}}`,
	} {
		patch, err := ParsePatch([]byte(input))
		if err == nil {
			_, err = ApplyPatch(Empty(ScopeEndpoint), patch, ScopeEndpoint)
		}
		if err == nil {
			t.Errorf("accepted unsafe configuration: %s", input)
		}
	}
}

func TestBodyDefaultsForcedAndNative(t *testing.T) {
	d := Empty(ScopeEndpoint)
	d.BodyDefaults.Values["/options/missing"] = json.RawMessage(`{"x":1}`)
	d.BodyDefaults.Values["/options/present"] = json.RawMessage(`2`)
	d.BodyForced.Values["/options/forced"] = json.RawMessage(`3`)
	got, err := ApplyBody([]byte(`{"options":{"present":null,"forced":0}}`), d, 4096)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"options":{"forced":3,"missing":{"x":1},"present":null}}`
	if string(got) != want {
		t.Fatalf("got %s, want %s", got, want)
	}
	for _, source := range []string{`{"options":null}`, `{"options":[]}`, `{"options":1}`} {
		if _, err := ApplyBody([]byte(source), d, 4096); err == nil {
			t.Errorf("accepted non-object parent %s", source)
		}
	}
	if _, err := ApplyBody([]byte(`{"options":{}}`), d, 5); err == nil {
		t.Fatal("accepted over-limit output")
	}
	filtered, native, err := ExtractNative([]byte(`{"model":"logical","thinking":{"type":"enabled","budget_tokens":2000},"ordinary":2}`), []string{"/thinking"}, 4096)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(filtered), "thinking") || string(native["/thinking"]) != `{"budget_tokens":2000,"type":"enabled"}` {
		t.Fatalf("incorrect extraction: %s %v", filtered, native)
	}
	merged, err := MergeNative([]byte(`{"model":"real"}`), native, 4096)
	if err != nil || !strings.Contains(string(merged), `"thinking":{"budget_tokens":2000,"type":"enabled"}`) {
		t.Fatalf("incorrect native merge: %s %v", merged, err)
	}
}

func TestAddedHeadersFixedWinsAndSensitiveValuesReject(t *testing.T) {
	d := Empty(ScopeEndpoint)
	d.ForwardHeaders.Values = []string{"X-Client", "Origin"}
	d.FixedHeaders.Values["x-client"] = "configured"
	inbound := http.Header{"X-Client": {"one", "two"}, "Origin": {"https://example.test"}, "Authorization": {"secret"}}
	got, err := AddedHeaders(inbound, d)
	if err != nil || !reflect.DeepEqual(got, http.Header{"X-Client": {"configured"}, "Origin": {"https://example.test"}}) {
		t.Fatalf("headers: %v %v", got, err)
	}
	delete(d.FixedHeaders.Values, "x-client")
	if _, err := AddedHeaders(inbound, d); err == nil {
		t.Fatal("accepted ambiguous inbound header")
	}
	inbound.Set("Connection", "Origin")
	if _, err := AddedHeaders(inbound, d); err == nil {
		t.Fatal("accepted hop-by-hop nominated header")
	}
	for _, name := range []string{"Authorization", "Cookie", "Proxy-Token", "X-Forwarded-For", "x-api-key", "anthropic-version", "Content-Type", "X-Csrf-Token"} {
		if ValidHeaderName(name) {
			t.Errorf("accepted protected header %s", name)
		}
	}
}

func TestBodyAdaptationPreservesNullableSamplingFields(t *testing.T) {
	d := Empty(ScopeEndpoint)
	d.BodyForced.Values["/vendor_option"] = json.RawMessage(`true`)
	d.BodyDefaults.Values["/temperature"] = json.RawMessage(`0.7`)
	source := []byte(`{"temperature":null,"top_p":null,"presence_penalty":null,"frequency_penalty":null,"max_tokens":null,"max_completion_tokens":null}`)
	got, err := ApplyBody(source, d, 4096)
	if err != nil {
		t.Fatal("unrelated adaptation rejected optional null values:", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(got, &fields); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"temperature", "top_p", "presence_penalty", "frequency_penalty", "max_tokens", "max_completion_tokens"} {
		if string(fields[name]) != "null" {
			t.Fatalf("optional %s changed: %s", name, fields[name])
		}
	}
	for _, name := range []string{"max_tokens", "max_completion_tokens"} {
		d.BodyForced.Values["/"+name] = json.RawMessage(`null`)
		if ValidateDocument(d, ScopeEndpoint) == nil {
			t.Fatalf("forced output budget %s accepted null", name)
		}
		delete(d.BodyForced.Values, "/"+name)
	}
}
