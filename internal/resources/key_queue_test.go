package resources

import (
	"encoding/json"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
)

func TestEndpointKeyQueueLegacyControlReceiptAndCanonical(t *testing.T) {
	oldBody := []byte(`{"id":"1","endpoint_id":"2","revision":"3","max_concurrency":1,"max_rpm":2}`)
	replay, err := replayMutation[EndpointKey](idempotency.Decision{HTTPStatus: 200, ResponseBody: oldBody})
	if err != nil || !replay.Replayed || string(replay.Body) != string(oldBody) || replay.Value.MaxRPM != 2 {
		t.Fatalf("legacy key receipt: %+v %v", replay, err)
	}
	canonical, err := json.Marshal(createEndpointKeyCanonical{Secret: "legacy", Enabled: true, OwnershipConfirmed: true})
	want := `{"secret":"legacy","note":"","enabled":true,"force_store_false":false,"ownership_confirmed":true}`
	if err != nil || string(canonical) != want {
		t.Fatalf("legacy creation digest changed: %s %v", canonical, err)
	}
}
