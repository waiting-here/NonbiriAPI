package logapi

import (
	"encoding/csv"
	"encoding/json"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/usageinfo"
)

func TestUsageDetailsRoleProjectionsAndExports(t *testing.T) {
	f := newNumberedLogFixture(t)
	u := contract.Usage{Present: true, UncachedInputTokens: 1, CacheWriteInputTokens: 2, CacheReadInputTokens: 3, OutputTokens: 4, SearchUnitsPresent: true, SearchUnitsMicros: 9007199254740993, Modalities: contract.ModalityUsage{InputPresent: true, OutputPresent: true, Input: [3][4]int64{{1, 0, 0, 0}, {0, 2, 0, 0}, {0, 0, 3, 0}}, Output: [4]int64{0, 0, 0, 4}}}
	details, err := usageinfo.WithPricing(usageinfo.Encode(u), usageinfo.Pricing{Mode: "per_token", InputMode: "modality", OutputMode: "aggregate", OutputFallback: "missing_price"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`UPDATE request_logs SET usage_details=? WHERE logical_request_id=?`, details, f.selfID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`UPDATE request_attempts SET input_tokens=1,cache_write_input_tokens=2,cache_read_input_tokens=3,output_tokens=4,usage_unknown=0,usage_details=? WHERE request_log_id=(SELECT id FROM request_logs WHERE logical_request_id=?)`, usageinfo.Encode(u), f.selfID); err != nil {
		t.Fatal(err)
	}
	detail, err := f.repo.GetUser(t.Context(), logUserOne, f.selfID, AttemptFilter{})
	if err != nil {
		t.Fatal(err)
	}
	self := detail.(UserSelfLogDetail)
	if self.Request.Usage.SearchUnits == nil || *self.Request.Usage.SearchUnits != "9007199254.740993" || self.Request.Usage.Modalities.Input.CacheWriteInput.Image != "2" || self.Request.Usage.Pricing.OutputFallback != "missing_price" {
		t.Fatalf("safe usage: %+v", self.Request.Usage)
	}
	if self.Attempts.Data[0].Usage.SearchUnits == nil || self.Attempts.Data[0].Usage.Modalities.Output.Video != "4" {
		t.Fatalf("attempt usage: %+v", self.Attempts.Data[0].Usage)
	}
	admin, err := f.repo.ExportAdmin(t.Context(), ListFilter{})
	if err != nil {
		t.Fatal(err)
	}
	steward, err := f.repo.ExportSteward(t.Context(), 999, ListFilter{}, allowLogStewardRead{})
	if err != nil {
		t.Fatal(err)
	}
	requireManagementLogEqual(t, admin, steward)
	user, err := f.repo.ExportUser(t.Context(), logUserOne, ListFilter{})
	if err != nil {
		t.Fatal(err)
	}
	for _, marshal := range []func() ([]byte, error){func() ([]byte, error) { return MarshalUserJSON(user) }, func() ([]byte, error) { return MarshalAdminJSON(admin) }} {
		body, err := marshal()
		if err != nil || !json.Valid(body) || !strings.Contains(string(body), `"search_units":"9007199254.740993"`) || strings.Contains(string(body), "donor_reward") || strings.Contains(string(body), "pricing_details") {
			t.Fatalf("JSON projection: %s %v", body, err)
		}
	}
	for _, marshal := range []func() ([]byte, error){func() ([]byte, error) { return MarshalUserCSV(user) }, func() ([]byte, error) { return MarshalAdminCSV(admin) }} {
		body, err := marshal()
		if err != nil {
			t.Fatal(err)
		}
		rows, err := csv.NewReader(strings.NewReader(string(body))).ReadAll()
		if err != nil {
			t.Fatal(err)
		}
		column := -1
		for i, name := range rows[0] {
			if name == "search_units" {
				column = i
			}
		}
		if column < 0 || rows[1][column] != "9007199254.740993" {
			t.Fatalf("CSV details: %v", rows)
		}
	}
}

func TestSearchUnitsRemainVisibleWithoutTokenUsage(t *testing.T) {
	f := newLogFixture(t)
	details, err := usageinfo.WithPricing(usageinfo.Encode(contract.Usage{SearchUnitsPresent: true, SearchUnitsMicros: 1}), usageinfo.Pricing{Mode: "per_search_unit"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`UPDATE request_logs SET usage_unknown=1,usage_details=? WHERE logical_request_id=?`, details, f.charityID); err != nil {
		t.Fatal(err)
	}
	detail, err := f.repo.GetUser(t.Context(), logUserOne, f.charityID, AttemptFilter{})
	if err != nil {
		t.Fatal(err)
	}
	usage := detail.(UserCharityLogDetail).Request.Usage
	if !usage.UsageUnknown || usage.SearchUnits == nil || *usage.SearchUnits != "0.000001" || usage.Modalities != nil || usage.Pricing.Mode != "per_search_unit" {
		t.Fatalf("unit-only projection: %+v", usage)
	}
}
