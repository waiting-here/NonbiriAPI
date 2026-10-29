package resources

import (
	"context"
	"encoding/json"
	"strconv"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/connectionstore/testseed"
	"github.com/waiting-here/NonbiriAPI/internal/managementlog"
)

func TestDiscoveryCompletionRecordsManagementOutcome(t *testing.T) {
	e := newResourceTestEnvironment(t)
	user := e.seedUser(t, "managed-discovery")
	endpoint := resourceTestID(t, e.createEndpoint(t, user, resourceTestKey('a')).ID)
	key := resourceTestID(t, e.createEndpointKey(t, user, endpoint, resourceTestKey('b')).ID)
	// Imported catalog runs predate durable discovery operations. A later
	// successful refresh must be able to replace and audit those runs.
	tx, err := e.store.DB().BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	legacy, err := testseed.SeedDiscoveryCatalogTx(t.Context(), tx, testseed.DiscoveryCatalogInput{EndpointKeyID: key, ListFormat: "openai", At: resourceTestNow})
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	e.store.SetManagementCapture(managementlog.CaptureEventID)
	for i, outcome := range []DiscoveryClaimResult{
		{Succeeded: true, Models: []DiscoveredModel{{UpstreamModelID: "discovered-model"}}},
		{FailureClass: DiscoveryFailureAuth},
		{Succeeded: true},
	} {
		e.discovery.mu.Lock()
		e.discovery.result = outcome
		e.discovery.mu.Unlock()
		ctx := managementlog.WithOperation(context.Background(), managementlog.Metadata{
			Source: managementlog.SourceWeb, ExecutorKind: "human", Domain: "donations", Action: "discover",
			Actor: managementlog.Subject{ID: strconv.FormatInt(user, 10), Name: "Reviewer", Role: "admin"},
		})
		accepted, err := e.repository.refreshDiscovery(ctx, user, endpoint, key, discoveryMutation(resourceTestKey(byte('c'+i)), endpoint, key), true)
		if err != nil {
			t.Fatalf("discovery %d completion: %v", i, err)
		}
		var runState, operationState, logState string
		err = e.store.ReadDB().QueryRow(`SELECT run.state,op.state,log.outcome FROM discovery_runs run JOIN accepted_operations op ON op.id=run.operation_id JOIN management_operations log ON log.correlation_kind='model_discovery' AND log.correlation_id=op.id WHERE op.id=?`, accepted.Value.OperationID).Scan(&runState, &operationState, &logState)
		want := "succeeded"
		if !outcome.Succeeded {
			want = "failed"
		}
		if err != nil || runState != want || operationState != "completed" || logState != want {
			t.Fatalf("discovery %d states: %q/%q/%q, %v", i, runState, operationState, logState, err)
		}
		if i == 0 {
			var raw string
			if err := e.store.ReadDB().QueryRow(`SELECT item.details FROM management_operation_items item JOIN management_operations log ON log.id=item.operation_id WHERE log.correlation_id=? AND item.target_kind='discovery_job'`, accepted.Value.OperationID).Scan(&raw); err != nil {
				t.Fatal(err)
			}
			var details managementlog.Details
			if err := json.Unmarshal([]byte(raw), &details); err != nil {
				t.Fatal(err)
			}
			found := false
			for _, related := range details.Related {
				if related.Kind == "discovery_runs" && related.ID == "run:"+strconv.FormatInt(legacy.RunID, 10) && related.State == "deleted" {
					found = true
				}
			}
			if !found {
				t.Fatalf("legacy discovery cleanup missing from management history: %s", raw)
			}
		}
	}
	var retained int
	if err := e.store.ReadDB().QueryRow(`SELECT COUNT(*) FROM discovery_runs WHERE id=?`, legacy.RunID).Scan(&retained); err != nil || retained != 0 {
		t.Fatalf("legacy discovery retained: %d, %v", retained, err)
	}
}
