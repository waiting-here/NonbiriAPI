package lifecycle

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/rolepolicy"
)

func TestGatewayVersionsRemainDistinctInAccountExport(t *testing.T) {
	fixture := newLifecycleTestFixture(t, 100)
	fixture.exports.endpoints = []EndpointExport{
		{ID: "1", ConnectorType: "ai-sdk-gateway-v3", BaseURL: "https://gateway.example/v1", Keys: []EndpointKeyExport{}},
		{ID: "2", ConnectorType: "ai-sdk-gateway-v4", BaseURL: "https://gateway.example/v1", Keys: []EndpointKeyExport{}},
	}
	bindings := []BindingExport{
		{ID: "1", EndpointKeyID: "1", ConnectorType: "ai-sdk-gateway-v3", UpstreamModelID: "vendor/model"},
		{ID: "2", EndpointKeyID: "2", ConnectorType: "ai-sdk-gateway-v4", UpstreamModelID: "vendor/model"},
	}
	fixture.exports.models = []ModelExport{{ID: "1", RolePolicy: rolepolicy.Default(), Bindings: bindings}}
	body, err := mustNewLifecycleCoordinator(t, fixture.config).Export(context.Background(), 7, 100)
	if err != nil {
		t.Fatal(err)
	}
	var document ExportDocument
	if err := json.Unmarshal(body, &document); err != nil {
		t.Fatal(err)
	}
	if document.SchemaVersion != 28 || !reflect.DeepEqual(document.Endpoints, fixture.exports.endpoints) || len(document.Models) != 1 || !reflect.DeepEqual(document.Models[0].Bindings, bindings) {
		t.Fatal("account export changed a Gateway version or binding identity")
	}
}
