package adapters

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/charity"
	"github.com/waiting-here/NonbiriAPI/internal/connector/contract"
)

func TestConsumerUsageExportUsesStringsAndNoPricingIdentities(t *testing.T) {
	value := charity.ConsumerExport{UsageDetailsPresent: true, UsageDetails: contract.Usage{SearchUnitsPresent: true, SearchUnitsMicros: 9007199254740993}}
	body, err := json.Marshal(consumerUsageDetails(value))
	if err != nil || string(body) != `{"search_units":"9007199254.740993"}` {
		t.Fatalf("unit-only export: %s %v", body, err)
	}
	value.UsageDetails.Present = true
	value.UsageDetails.Modalities = contract.ModalityUsage{InputPresent: true, OutputPresent: true, Input: [3][4]int64{{0, 3, 0, 0}}, Output: [4]int64{1, 0, 0, 0}}
	body, err = json.Marshal(consumerUsageDetails(value))
	if err != nil || !strings.Contains(string(body), `"image":"3"`) || !strings.Contains(string(body), `"text":"1"`) || strings.Contains(string(body), "pricing") || strings.Contains(string(body), "donor") {
		t.Fatalf("modality export: %s %v", body, err)
	}
	if consumerUsageDetails(charity.ConsumerExport{}) != nil {
		t.Fatal("legacy details should be absent")
	}
}
