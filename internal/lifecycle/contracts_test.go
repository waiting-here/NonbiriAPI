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

func TestExportV4EndpointAndDonationSchemasAreClosed(t *testing.T) {
	assertClosedJSONKeys(t, EndpointExport{},
		"id", "connector_type", "base_url", "origin", "note", "enabled", "created_at", "updated_at", "keys")
	assertClosedJSONKeys(t, EndpointOriginExport{Kind: "custom"}, "kind")
	assertClosedJSONKeys(t, EndpointOriginExport{
		Kind: "mainstream", ChannelID: "mch_safe", Name: "Safe channel",
	}, "kind", "channel_id", "name")

	assertClosedJSONKeys(t, DonationExport{},
		"id", "status", "description", "review_result", "keys", "created_at", "updated_at")
	assertClosedJSONKeys(t, DonationKeyExport{},
		"id", "endpoint_key_id", "display_head", "display_tail", "safe_source",
		"physical_enabled", "charity_state", "limits", "usage", "token_reserve",
		"authorized_expires_at", "expires_at", "streak", "ended_reason")
	assertClosedJSONKeys(t, DonationSafeSourceExport{Kind: "custom"},
		"kind", "connector_type", "base_url")
	channelID, name := "mch_safe", "Safe channel"
	assertClosedJSONKeys(t, DonationSafeSourceExport{
		Kind: "mainstream", ConnectorType: "openai-compatible", BaseURL: "https://example.invalid",
		ChannelID: &channelID, Name: &name,
	}, "kind", "connector_type", "base_url", "channel_id", "name")
}

func assertClosedJSONKeys(t *testing.T, value any, want ...string) {
	t.Helper()
	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(payload, &object); err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(object))
	for key := range object {
		got = append(got, key)
	}
	sort.Strings(got)
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("JSON keys = %v, want %v; payload=%s", got, want, payload)
	}
}
