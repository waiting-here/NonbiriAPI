package issues

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestIssueDTOFixture(t *testing.T) {
	resourceID := "42"
	closedAt := int64(1_700_000_100)
	got := Page{
		Data: []Issue{
			{
				ID: "iss_AAAAAAAAAAAAAAAAAAAAAQ", State: "current", Source: SourceModelDiscovery,
				ResourceKind: ResourceEndpointKey, SummaryCode: string(RootDiscoveryFailed), SafeDetail: "timeout",
				DeepLink:    &DeepLink{RouteID: "endpoint-detail", ResourceID: &resourceID},
				FirstSeenAt: 1_700_000_000, LastSeenAt: 1_700_000_050, Count: "2",
			},
			{
				ID: "iss_AAAAAAAAAAAAAAAAAAAAAg", State: "closed", Source: SourceRoutingProjection,
				ResourceKind: ResourceModel, SummaryCode: string(RootNoRoutableBinding), SafeDetail: "",
				FirstSeenAt: 1_699_999_900, LastSeenAt: 1_700_000_000, Count: "1", ClosedAt: &closedAt,
			},
		},
		ProjectionIncomplete: true,
	}
	actual, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	expected, err := os.ReadFile(filepath.Join("testdata", "page.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var actualValue, expectedValue any
	if err := json.Unmarshal(actual, &actualValue); err != nil {
		t.Fatalf("decode actual: %v", err)
	}
	if err := json.Unmarshal(expected, &expectedValue); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	if !reflect.DeepEqual(actualValue, expectedValue) {
		t.Fatalf("fixture mismatch\nactual: %s\nwant:   %s", actual, expected)
	}
}
