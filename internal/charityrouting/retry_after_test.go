package charityrouting

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"

	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/donationquota"
)

func TestRetryAfterCandidateExpiryAndUnavailableClassification(t *testing.T) {
	e := newRoutingTestEnv(t)
	e.seedUser(t, true, nil)
	owner := e.seedUser(t, false, nil)
	_, key, _ := e.seedCandidate(t, owner, 'x', "upstream-model")
	model := e.createModel(t, 'E')
	id, _ := parsePositiveID(model.ID)
	_, err := e.service.AddBindingsAdmin(context.Background(), id, routingMutation(t, 'F', http.MethodPost, routeAdminBindingBatch, []int64{id}, map[string]any{"binding": true}), BindingBatch{ExpectedBindingRevision: "0", Selections: []BindingSelection{{DonationKeyID: fmt.Sprint(key), UpstreamModelID: "upstream-model"}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.DB().Exec("INSERT INTO donation_key_cooldowns VALUES(?,?,?)", key, routingTestNow+60, routingTestNow); err != nil {
		t.Fatal(err)
	}
	connectors := []contract.Type{contract.TypeOpenAICompatible}
	if _, err := e.service.Snapshot(context.Background(), id, routingTestNow+59, connectors); !errors.Is(err, donationquota.ErrLimited) {
		t.Fatalf("cooldown must retain 429 classification: %v", err)
	}
	snapshot, err := e.service.Snapshot(context.Background(), id, routingTestNow+60, connectors)
	if err != nil || len(snapshot.Candidates()) != 1 {
		t.Fatal("expiry did not automatically restore", snapshot, err)
	}
	if _, err := e.store.DB().Exec("UPDATE donation_keys SET enabled=0 WHERE id=?", key); err != nil {
		t.Fatal(err)
	}
	if _, err := e.service.Snapshot(context.Background(), id, routingTestNow+60, connectors); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("expiry revived disabled key: %v", err)
	}
}
