package gacha

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestActionJSONRetainsRouteLimits(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		valid      bool
	}{
		{"valid", `{"action":"draw","pool_id":"permanent","quantity":1}`, true},
		{"boundary", `{"action":"enter"}` + strings.Repeat(" ", 32768-len(`{"action":"enter"}`)), true},
		{"too-large", `{"action":"enter"}` + strings.Repeat(" ", 32769-len(`{"action":"enter"}`)), false},
		{"unknown", `{"action":"enter","extra":true}`, false},
		{"duplicate", `{"action":"enter","action":"draw"}`, false},
		{"null", `{"action":"enter","quantity":null}`, false},
		{"trailing", `{"action":"enter"} {}`, false},
		{"array", `[]`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", "/api/gacha/actions", strings.NewReader(tc.body))
			var input Action
			if err := decode(httptest.NewRecorder(), r, &input); (err == nil) != tc.valid {
				t.Fatalf("accepted=%v want=%v", err == nil, tc.valid)
			}
		})
	}
}
