package adminapi

import (
	"encoding/json"
	"testing"
)

func TestCatalogTextWireCompatibility(t *testing.T) {
	got, err := json.Marshal(catalogText("Chinese", "English"))
	if err != nil || string(got) != "{\"zh\":\"Chinese\",\"en\":\"English\"}" {
		t.Fatalf("catalog text wire=%s, %v", got, err)
	}
}
