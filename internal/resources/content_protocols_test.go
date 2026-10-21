package resources

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/modeltype"
)

func TestContentProtocolsShareURLWithSeparateCredentialsAndExport(t *testing.T) {
	e := newResourceTestEnvironment(t)
	owner := e.seedUser(t, "content-protocols")
	for i, kind := range []string{"openai-compatible", "google-gemini", "openai-responses", "openai-decisions"} {
		endpoint := e.createEndpointWithConnector(t, owner, resourceTestKey(byte('a'+i)), kind, true)
		key := e.createEndpointKey(t, owner, resourceTestID(t, endpoint.ID), resourceTestKey(byte('f'+i)))
		var actual string
		if err := e.store.DB().QueryRow(`SELECT s.connector_type FROM endpoint_key_secrets s JOIN endpoint_keys k ON k.secret_ref_id=s.id WHERE k.id=?`, resourceTestID(t, key.ID)).Scan(&actual); err != nil || actual != kind {
			t.Fatalf("secret context=%s error=%v", actual, err)
		}
	}
	model := e.createModel(t, owner, resourceTestKey('k'), "provider", "decisions")
	id := resourceTestID(t, model.ID)
	types := modeltype.Set{contract.OperationDecisions}
	input := PatchModelInput{ModelTypes: &types, ExpectedRevision: 1}
	mutation := resourceTestMutation(t, resourceTestKey('l'), "PATCH", routeModel, []int64{id}, patchModelCanonical{ModelTypes: &types, ExpectedRevision: "1"})
	if _, err := e.repository.PatchModel(context.Background(), owner, id, mutation, input); err != nil {
		t.Fatal(err)
	}
	tx, err := e.store.DB().BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	exported, err := e.repository.ExportLifecycleResources(context.Background(), tx, owner, resourceTestNow, 100)
	if err != nil || len(exported.Endpoints) != 4 || len(exported.Models) != 1 || !exported.Models[0].ModelTypes.Supports(contract.OperationDecisions) {
		t.Fatalf("export=%+v err=%v", exported, err)
	}
}

func TestContentProtocolsMainstreamChannelCreationAndOptions(t *testing.T) {
	e := newResourceTestEnvironment(t)
	admin := resourceAdminUser(t, e, "content-channel-admin")
	user := e.seedUser(t, "content-channel-user")
	registrar := &resourceAdminTestRegistrar{}
	if err := RegisterAdminRoutes(registrar, e.repository); err != nil {
		t.Fatal(err)
	}
	for i, kind := range []string{"google-gemini", "openai-responses", "openai-decisions"} {
		body := fmt.Sprintf(`{"name":"%s","category":"api_platform","connector_type":"%s","base_url":"https://example.com/v1","enabled":true}`, kind, kind)
		created := resourceAdminHTTPCall(t, registrar.handlers[http.MethodPost+" "+routeMainstreamChannels], admin, http.MethodPost, routeMainstreamChannels, body, resourceTestKey(byte('a'+i)), "")
		if created.Code != http.StatusCreated {
			t.Fatalf("%s: %d %s", kind, created.Code, created.Body)
		}
		var channel MainstreamChannel
		if json.Unmarshal(created.Body.Bytes(), &channel) != nil || channel.ConnectorType != kind {
			t.Fatal(created.Body.String())
		}
	}
	options, err := e.repository.GetEndpointCreateOptions(context.Background(), user)
	if err != nil || len(options.BaseConnectorTypes) != 12 || len(options.MainstreamChannels) != 3 {
		t.Fatalf("options=%+v err=%v", options, err)
	}
}
