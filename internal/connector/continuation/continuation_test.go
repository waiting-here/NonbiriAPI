package continuation

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBindingAcceptsOnlyCanonicalBoundedShape(t *testing.T) {
	valid := strings.Repeat("A", 43)
	for _, tc := range []struct {
		raw   string
		valid bool
	}{
		{`{"google":{"parts":[]}}`, true},
		{`{"google":{"parts":[]},"nonbiri":{"continuation_id":"` + valid + `"}}`, true},
		{`{"google":{},"nonbiri":{"continuation_id":"bad"}}`, false},
		{`{"google":{},"nonbiri":{"continuation_id":"` + valid + `= interloper"}}`, false},
		{`{"google":{},"nonbiri":{"continuation_id":"` + valid + `","model":"private"}}`, false},
		{`{"google":{},"other":{}}`, false},
	} {
		var root map[string]json.RawMessage
		_ = json.Unmarshal([]byte(tc.raw), &root)
		if ValidExtra(root, "google") != tc.valid {
			t.Fatal(tc.raw)
		}
	}
	bound, _ := json.Marshal(Bind(map[string]any{"google": map[string]any{"parts": []any{}}}, valid))
	if id, ok := ID(bound); !ok || id != valid {
		t.Fatal(string(bound), id, ok)
	}
}
