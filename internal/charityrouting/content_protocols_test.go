package charityrouting

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/connector"
	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
)

func TestContentProtocolsPersistBindingsAndSelectRuntimeCandidates(t *testing.T) {
	e := newRoutingTestEnv(t)
	e.seedUser(t, true, nil)
	owner := e.seedUser(t, false, nil)
	model := e.createModel(t, 'a')
	modelID, _ := parsePositiveID(model.ID)
	kinds := []contract.Type{contract.TypeGoogleGemini, contract.TypeOpenAIResponses, contract.TypeOpenAIDecisions}
	var selections []BindingSelection
	for _, kind := range kinds {
		_, donationKey, _ := e.seedCandidateWithConnector(t, owner, string(kind), "private-model", kind)
		selections = append(selections, BindingSelection{DonationKeyID: fmt.Sprint(donationKey), UpstreamModelID: "private-model"})
	}
	input := BindingBatch{ExpectedBindingRevision: "0", Selections: selections}
	if _, err := e.service.AddBindingsAdmin(context.Background(), modelID, routingMutation(t, 'b', http.MethodPost, routeAdminBindingBatch, []int64{modelID}, input), input); err != nil {
		t.Fatal(err)
	}
	snapshot, err := e.service.Snapshot(context.Background(), modelID, routingTestNow, connector.NewDefaultRegistry().Types())
	if err != nil || len(snapshot.Candidates()) != 3 {
		t.Fatalf("snapshot=%+v err=%v", snapshot, err)
	}
	for _, kind := range kinds {
		single, err := e.service.Snapshot(context.Background(), modelID, routingTestNow, []contract.Type{kind})
		if err != nil || len(single.Candidates()) != 1 || single.Candidates()[0].ConnectorType != kind {
			t.Fatalf("%s: %+v %v", kind, single, err)
		}
	}
}
