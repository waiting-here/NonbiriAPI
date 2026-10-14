package requestadaptation

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

func TestSensitiveMarkersPreserveInheritanceAndMaskedProjection(t *testing.T) {
	patch, err := ParsePatch([]byte(`{"expected_revision":"0","fixed_headers":{"mode":"replace","values":{"X-Token":{"action":"replace","value":"header-secret","sensitive_paths":[""]}}},"body_forced":{"mode":"replace","values":{"/vendor":{"action":"replace","value":{"token":"body-secret","mode":"high"},"sensitive_paths":["/token"]}}}}`))
	if err != nil {
		t.Fatal(err)
	}
	doc, err := ApplyPatch(Empty(ScopeEndpoint), patch, ScopeEndpoint)
	if err != nil {
		t.Fatal(err)
	}
	child := Snapshot{Document: Empty(ScopePersonalModel), Revision: "0"}
	effective := EffectivePersonal(Snapshot{Document: doc, Revision: "1"}, child)
	projection := effective.Projection()
	wire, err := json.Marshal(projection)
	if err != nil || bytes.Contains(wire, []byte("header-secret")) || bytes.Contains(wire, []byte("body-secret")) || !bytes.Contains(wire, []byte("sensitive_paths")) {
		t.Fatalf("unsafe projection: %s", wire)
	}
	exported, _ := json.Marshal(projectionValues(projection.BodyForced))
	if bytes.Contains(exported, []byte("body-secret")) || !bytes.Contains(exported, []byte("/token")) {
		t.Fatalf("export: %s", exported)
	}
	effective.Document.BodyForced.SensitivePaths["/vendor"][0] = "/mode"
	if doc.BodyForced.SensitivePaths["/vendor"][0] != "/token" {
		t.Fatal("inherited markers alias storage")
	}
	child.Document.BodyForced.Mode = ModeReplace
	effective = EffectivePersonal(Snapshot{Document: doc}, child)
	if len(effective.Document.BodyForced.SensitivePaths) != 0 {
		t.Fatal("replaced partition retained markers")
	}
	for _, action := range []string{"keep", "replace"} {
		edit := ValueEdit{Action: action}
		if action == "replace" {
			edit.Value = json.RawMessage(`"new-secret"`)
		}
		changed, err := ApplyPatch(doc, Patch{FixedHeaders: &MapEdit{Mode: ModeReplace, Values: map[string]ValueEdit{"X-Token": edit}}}, ScopeEndpoint)
		if err != nil || len(changed.FixedHeaders.SensitivePaths["X-Token"]) != 1 {
			t.Fatalf("omission did not preserve markers: %v", err)
		}
		empty := []string{}
		edit.SensitivePaths = &empty
		changed, err = ApplyPatch(doc, Patch{FixedHeaders: &MapEdit{Mode: ModeReplace, Values: map[string]ValueEdit{"X-Token": edit}}}, ScopeEndpoint)
		if err != nil || len(changed.FixedHeaders.SensitivePaths) != 0 {
			t.Fatalf("explicit unmark failed: %v", err)
		}
	}
}

func TestSensitiveMarkerValidation(t *testing.T) {
	for _, paths := range []string{`null`, `[null]`, `["/missing"]`, `["/count"]`, `["/empty"]`, `["/token","/token"]`, `["/array/01"]`, `[""]`} {
		raw := fmt.Sprintf(`{"expected_revision":"0","body_forced":{"mode":"replace","values":{"/vendor":{"action":"replace","value":{"token":"secret","count":1,"empty":"","array":["s"]},"sensitive_paths":%s}}}}`, paths)
		patch, err := ParsePatch([]byte(raw))
		if err == nil {
			_, err = ApplyPatch(Empty(ScopeEndpoint), patch, ScopeEndpoint)
		}
		if err == nil {
			t.Fatalf("accepted selectors %s", paths)
		}
	}
	for _, value := range []string{"", strings.Repeat("s", MaxSensitiveBytes+1)} {
		doc := Empty(ScopeEndpoint)
		doc.BodyForced.Values["/vendor"], _ = json.Marshal(value)
		doc.BodyForced.SensitivePaths = map[string][]string{"/vendor": {""}}
		if ValidateDocument(doc, ScopeEndpoint) == nil {
			t.Fatal("accepted invalid protected string size")
		}
	}
	doc := Empty(ScopeEndpoint)
	doc.BodyForced.SensitivePaths = map[string][]string{}
	for i := 0; i <= MaxSensitiveValues; i++ {
		key := fmt.Sprintf("/v%d", i)
		doc.BodyForced.Values[key] = json.RawMessage(`"s"`)
		doc.BodyForced.SensitivePaths[key] = []string{""}
	}
	if ValidateDocument(doc, ScopeEndpoint) == nil {
		t.Fatal("unbounded detectors")
	}
}

func TestSensitiveSelectorsPreserveJSONMemberNames(t *testing.T) {
	doc := Empty(ScopeEndpoint)
	doc.BodyForced.Values["/vendor"] = json.RawMessage(`{"":"empty-name-secret","constructor":"constructor-secret","__proto__":{"token ":"spaced-secret"},"a/b":{"~":"escaped-secret"}}`)
	doc.BodyForced.SensitivePaths = map[string][]string{"/vendor": {"/", "/constructor", "/__proto__/token ", "/a~1b/~0"}}
	if err := ValidateDocument(doc, ScopeEndpoint); err != nil {
		t.Fatal(err)
	}
	_, secrets, err := ApplyBodyWithSecrets([]byte(`{}`), doc, 4096)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, value := range secrets {
		got = append(got, string(value))
	}
	want := []string{"empty-name-secret", "spaced-secret", "escaped-secret", "constructor-secret"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
	for _, path := range []string{"/", "/constructor", "/__proto__/token "} {
		if _, err := ParsePointer(path); err == nil {
			t.Fatalf("mutation restriction weakened for %q", path)
		}
	}
	for _, path := range []string{"token", "/bad~2", "/bad~", "/" + strings.Repeat("a", 513)} {
		if _, ok := sensitiveString(map[string]any{}, path); ok {
			t.Fatalf("accepted invalid selector %q", path)
		}
	}
}

func TestBodySecretsTrackAppliedDefaultsAndOverrides(t *testing.T) {
	cases := []struct {
		name, input, forced string
		want                []string
	}{
		{"missing", `{}`, `{}`, []string{"default-secret"}},
		{"caller", `{"vendor":{"token":"caller","mode":"high"}}`, `{}`, nil},
		{"null is present", `{"vendor":null}`, `{}`, nil},
		{"forced leaf", `{}`, `{"/vendor/token":"ordinary"}`, nil},
		{"forced sibling", `{}`, `{"/vendor/mode":"low"}`, []string{"default-secret"}},
		{"forced parent", `{}`, `{"/vendor":{"token":"ordinary"}}`, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			doc := Empty(ScopeEndpoint)
			doc.BodyDefaults.Values["/vendor"] = json.RawMessage(`{"token":"default-secret","mode":"high"}`)
			doc.BodyDefaults.SensitivePaths = map[string][]string{"/vendor": {"/token"}}
			if err := json.Unmarshal([]byte(tc.forced), &doc.BodyForced.Values); err != nil {
				t.Fatal(err)
			}
			_, secrets, err := ApplyBodyWithSecrets([]byte(tc.input), doc, 4096)
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			for _, value := range secrets {
				got = append(got, string(value))
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
}

func TestSentSecretsOnlyUseFinalWireStringValues(t *testing.T) {
	candidates := [][]byte{[]byte("body-secret"), []byte("header-secret"), []byte("discarded"), []byte("key-only"), []byte("123"), []byte("body-secret")}
	result, err := SentSecrets(candidates, http.Header{"X-Token": {" header-secret\t"}}, []byte(`{"escaped":"body\u002dsecret","key-only":false,"number":123,"nested":[{"value":"high"}]}`))
	if err != nil || len(result) != 2 {
		t.Fatalf("unexpected final materials: %v %v", result, err)
	}
	if string(result[0]) != "header-secret" || string(result[1]) != "body-secret" {
		t.Fatal("wire matching failed")
	}
	result, err = SentSecrets(nil, nil, []byte("not parsed"))
	if err != nil || result != nil {
		t.Fatal("unmarked request incurred JSON work")
	}
	doc := Empty(ScopeEndpoint)
	doc.FixedHeaders.Values["x-token"] = " header-secret\t"
	doc.FixedHeaders.SensitivePaths = map[string][]string{"x-token": {""}}
	headers, err := AddedHeaders(nil, doc)
	if err != nil {
		t.Fatal(err)
	}
	if got := HeaderSecrets(headers, doc); len(got) != 1 || string(got[0]) != "header-secret" {
		t.Fatal("header normalization mismatch")
	}
}

func BenchmarkSentSecrets(b *testing.B) {
	for _, count := range []int{0, 1, 16} {
		for _, size := range []int{1024, 65536} {
			b.Run(fmt.Sprintf("markers%d/bytes%d", count, size), func(b *testing.B) {
				body, _ := json.Marshal(map[string]string{"content": strings.Repeat("x", size)})
				var candidates [][]byte
				for i := 0; i < count; i++ {
					candidates = append(candidates, []byte(fmt.Sprintf("private-token-%d", i)))
				}
				b.ReportAllocs()
				b.SetBytes(int64(len(body)))
				for b.Loop() {
					if _, err := SentSecrets(candidates, nil, body); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}
