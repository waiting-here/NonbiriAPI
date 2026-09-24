package lifecycle

import (
	"encoding/json"
	"reflect"
	"sort"
	"testing"
)

func TestFrozenBoundsAndHeldObjectKinds(t *testing.T) {
	if SchemaVersion != 10 || CollectionLimit != 10_000 || MaxExportBytes != 16<<20 || WorkerBatchLimit != 100 {
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
		"limited_activities", "image_tasks", "inactivity",
		"game_onboarding_holds", "loans", "game_rankings", "penalties",
		"bidding", "likes", "blackjack", "randomness",
		"caller_key", "catalog_pairs", "charity", "checkins", "game_onboarding", "credit_ledger", "donations", "endpoints",
		"fishing", "generated_at", "issues", "linklink", "log_summary", "models", "rps",
		"schema_version", "thursday", "usage", "user", "welfare_claims",
	}
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("top-level export keys = %v, want %v", got, want)
	}
}

func TestExportEndpointAndDonationSchemasAreClosed(t *testing.T) {
	assertClosedJSONKeys(t, GovernanceExport{}, "limited_activities", "image_tasks", "inactivity")
	assertClosedJSONKeys(t, ImageTaskExport{}, "id", "status", "n", "created_at", "dispatched_at", "completed_at", "billing_state", "charge", "refund", "actual_images")
	assertClosedJSONKeys(t, LimitedActivityExport{}, "wallet", "exchanges")
	assertClosedJSONKeys(t, InactivityExport{}, "activity", "runs")
	assertClosedJSONKeys(t, LoanExport{}, "loan_id", "operation_id", "created_at", "principal", "a", "b", "nominal", "disbursed", "fee", "repayment", "interest", "general_before", "general_after", "game_before", "game_after")
	assertClosedJSONKeys(t, OnboardingExport{}, "game_key", "task_key", "award", "completed_at", "operation_id")
	assertClosedJSONKeys(t, OnboardingHoldExport{}, "id", "game_key", "task_key", "created_at")
	assertClosedJSONKeys(t, RankingExport{}, "statistics_start", "totals", "events")
	assertClosedJSONKeys(t, RankingTotalExport{}, "board", "window", "amount", "achieved_at")
	assertClosedJSONKeys(t, RankingEventExport{}, "game", "settled_at", "loss", "positive_profit")
	assertClosedJSONKeys(t, PenaltyExport{}, "id", "kind", "reason_code", "started_at", "ends_at", "ended_at", "state", "result", "actions")
	assertClosedJSONKeys(t, PenaltyActionExport{}, "action", "occurred_at", "reason_code", "previous_ends_at", "ends_at", "request_id", "operation_id")
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
		"authorized_expires_at", "expires_at", "streak", "ended_reason", "recurring_limits")
	assertClosedJSONKeys(t, RecurringLimitExport{}, "id", "mode", "interval", "alignment", "time_zone", "week_starts_on", "metric", "limit", "used", "reserved", "remaining", "state", "period_start", "period_end", "next_transition_at")
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
