package stewardautomation

import (
	"encoding/json"
	"fmt"
	"testing"
)

func TestAutomationQueueInputAndLegacyImportCanonical(t *testing.T) {
	for _, wire := range []struct {
		field   string
		invalid []string
	}{
		{"concurrency_overflow", []string{`null`, `""`, `"wait"`, `"QUEUE"`, `1`}},
		{"rpm_overflow", []string{`null`, `""`, `"wait"`, `false`}},
		{"concurrency_queue_limit", []string{`null`, `-1`, `2147483648`, `0.5`, `"2"`}},
		{"rpm_queue_limit", []string{`null`, `-1`, `2147483648`, `false`}},
	} {
		for _, invalid := range wire.invalid {
			body := fmt.Sprintf(`{"keys":[{"secret":"fixture-key","%s":%s}]}`, wire.field, invalid)
			var imported importInput
			var created createInput
			if json.Unmarshal([]byte(body), &imported) == nil || json.Unmarshal([]byte(body), &created) == nil {
				t.Fatalf("invalid automation %s=%s accepted", wire.field, invalid)
			}
		}
	}
	var input importInput
	if err := json.Unmarshal([]byte(`{"ownership_confirmed":true,"keys":[{"secret":"legacy"}]}`), &input); err != nil {
		t.Fatal(err)
	}
	canonical, err := canonicalImport(input)
	want := `{"ownership_confirmed":true,"keys":[{"secret":"legacy","note":"","enabled":true,"force_store_false":false,"max_concurrency":0,"max_rpm":0}]}`
	if err != nil || string(canonical) != want {
		t.Fatalf("old import digest changed: %s %v", canonical, err)
	}
}
