package lifecycle

import (
	"encoding/json"
	"reflect"
	"sort"
	"testing"
)

func TestFrozenBoundsAndHeldObjectKinds(t *testing.T) {
	if SchemaVersion != 4 || CollectionLimit != 10_000 || MaxExportBytes != 16<<20 || WorkerBatchLimit != 100 {
		t.Fatalf("frozen bounds changed: schema=%d collection=%d bytes=%d batch=%d",
			SchemaVersion, CollectionLimit, MaxExportBytes, WorkerBatchLimit)
	}
	valid := []HeldObjectKind{
		HeldMaintenanceEvent,
		HeldReportCase,
		HeldAnnouncementAudit,
		HeldDonation,
		HeldRequestLog,
	}
	for _, kind := range valid {
		if !kind.Valid() {
			t.Fatalf("frozen hold kind %q rejected", kind)
		}
	}
	for _, kind := range []HeldObjectKind{"", "session", "ledger", "request_attempt"} {
		if kind.Valid() {
			t.Fatalf("unknown hold kind %q accepted", kind)
		}
	}
}

func TestExportDocumentHasClosedTopLevel(t *testing.T) {
	payload, err := json.Marshal(ExportDocument{})
	if err != nil {
		t.Fatalf("marshal export document: %v", err)
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(payload, &object); err != nil {
		t.Fatalf("decode export document: %v", err)
	}
	got := make([]string, 0, len(object))
	for key := range object {
		got = append(got, key)
	}
	sort.Strings(got)
	want := []string{
		"caller_key", "catalog_pairs", "charity", "credit_ledger", "donations", "endpoints",
		"fishing", "generated_at", "issues", "linklink", "log_summary", "models", "rps",
		"schema_version", "thursday", "usage", "user", "welfare_claims",
	}
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("top-level export keys = %v, want %v", got, want)
	}
}
