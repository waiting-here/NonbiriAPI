package adminapi

import (
	"encoding/json"
	"strconv"
	"testing"
)

func TestRedemptionConfigCatalogAndWriteBounds(t *testing.T) {
	entries, err := buildSiteConfigCatalog(nil)
	if err != nil {
		t.Fatal(err)
	}
	byKey := make(map[string]siteConfigCatalogEntry)
	for _, entry := range entries {
		byKey[entry.Key] = entry
	}
	for _, tc := range []struct {
		key           string
		def, min, max int
	}{
		{"redemption_ban_threshold", 5, 0, 4096},
		{"redemption_ban_window_seconds", 86400, 1, 316224000},
		{"redemption_ban_duration_seconds", 86400, 1, 316224000},
	} {
		entry := byKey[tc.key]
		if entry.Group != "abuse" || entry.ValueType != "integer" || entry.RawDefault != tc.def || entry.Minimum != tc.min || entry.Maximum != tc.max || entry.Title.Zh == "" || entry.Title.En == "" || entry.Description.Zh == "" || entry.Description.En == "" {
			t.Fatalf("incorrect administrator contract: %+v", entry)
		}
		for _, value := range []int{tc.min - 1, tc.min, tc.max, tc.max + 1} {
			encoded := strconv.Itoa(value)
			stored, failure := validateSiteConfigValue(tc.key, json.RawMessage(encoded))
			valid := value >= tc.min && value <= tc.max
			if valid && (failure.Code != "" || stored != encoded) || !valid && failure.Code == "" {
				t.Fatalf("%s=%s: value=%s error=%+v", tc.key, encoded, stored, failure)
			}
		}
	}
	zero := byKey["redemption_ban_threshold"].ZeroSemantics
	if zero.Zh == "" || zero.En == "" {
		t.Fatal("disabled rule has no explanation")
	}
}
