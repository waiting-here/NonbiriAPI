package smartbackoff

import (
	"bytes"
	"testing"
)

func TestReceiptProjectionOnlyRemovesRetiredSettings(t *testing.T) {
	for _, body := range []string{
		`{"revision":"9","smart_backoff":null,"smart_backoff_profile":"day","smart_backoff_rules":[]}`,
		`{"id":"1","keys":[{"id":"2","smart_backoff":{"source":"custom_rule","reason":"day_quota","state":"waiting","retry_at":123},"smart_backoff_profile":"day","smart_backoff_rules":[]}]}`,
		`{"key":{"id":"2","smart_backoff_profile":"none","smart_backoff_rules":[]},"donation_revision":"9"}`,
	} {
		source := []byte(body)
		got, err := ProjectReceipt(source)
		if err != nil || bytes.Contains(got, []byte("smart_backoff_profile")) || bytes.Contains(got, []byte("smart_backoff_rules")) || !bytes.Equal(source, []byte(body)) {
			t.Fatal(string(got), err)
		}
		stable, err := ProjectReceipt(got)
		if err != nil || !bytes.Equal(stable, got) {
			t.Fatal("current receipt changed", err)
		}
	}
}
