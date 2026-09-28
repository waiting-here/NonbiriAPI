package resources

import (
	"context"
	"net/http"
	"testing"
)

func TestPhysicalDeletionCompactsSurvivorsBeforeAddingBindings(t *testing.T) {
	for _, kind := range []string{"key", "endpoint", "report-key"} {
		t.Run(kind, func(t *testing.T) {
			env := newResourceTestEnvironment(t)
			owner := env.seedUser(t, "compaction-owner")
			a := env.createEndpoint(t, owner, resourceTestKey('A'))
			aid := resourceTestID(t, a.ID)
			b := env.createEndpoint(t, owner, resourceTestKey('B'))
			bid := resourceTestID(t, b.ID)
			ak := env.createEndpointKey(t, owner, aid, resourceTestKey('C'))
			akid := resourceTestID(t, ak.ID)
			bk := env.createEndpointKey(t, owner, bid, resourceTestKey('D'))
			bkid := resourceTestID(t, bk.ID)
			createDeletionTestCandidate(t, env, owner, aid, akid, resourceTestKey('E'), "first")
			createDeletionTestCandidate(t, env, owner, bid, bkid, resourceTestKey('F'), "survivor")
			createDeletionTestCandidate(t, env, owner, bid, bkid, resourceTestKey('G'), "new")
			model := env.createModel(t, owner, resourceTestKey('H'), "provider", "model")
			modelID := resourceTestID(t, model.ID)
			before := addDeletionTestBindings(t, env, owner, modelID, 0, resourceTestKey('I'), []BindingSelection{{EndpointKeyID: akid, UpstreamModelID: "first"}, {EndpointKeyID: bkid, UpstreamModelID: "survivor"}})
			switch kind {
			case "key":
				mutation := resourceTestMutation(t, resourceTestKey('J'), http.MethodDelete, routeEndpointKey, []int64{aid, akid}, expectedRevisionCanonical{ExpectedRevision: "1"})
				for i := 0; i < 2; i++ {
					if _, err := env.repository.DeleteEndpointKey(context.Background(), owner, aid, akid, mutation, 1); err != nil {
						t.Fatal(err)
					}
				}
			case "endpoint":
				mutation := resourceTestMutation(t, resourceTestKey('J'), http.MethodDelete, routeEndpoint, []int64{aid}, expectedRevisionCanonical{ExpectedRevision: "1"})
				for i := 0; i < 2; i++ {
					if _, err := env.repository.DeleteEndpoint(context.Background(), owner, aid, mutation, 1); err != nil {
						t.Fatal(err)
					}
				}
			case "report-key":
				tx, err := env.store.DB().Begin()
				if err != nil {
					t.Fatal(err)
				}
				if err := env.repository.DeleteEndpointKeyForReport(context.Background(), tx, owner, akid, resourceTestNow); err != nil {
					tx.Rollback()
					t.Fatal(err)
				}
				if err := tx.Commit(); err != nil {
					t.Fatal(err)
				}
			}
			after, err := env.repository.ListBindings(context.Background(), owner, modelID)
			if err != nil || after.BindingRevision != "2" || len(after.Bindings) != 1 || after.Bindings[0].Ord != 0 || after.Bindings[0].ID != before.Bindings[1].ID {
				t.Fatal("survivor order/identity/revision", after, err)
			}
			added := addDeletionTestBindings(t, env, owner, modelID, 2, resourceTestKey('K'), []BindingSelection{{EndpointKeyID: bkid, UpstreamModelID: "new"}})
			if len(added.Bindings) != 2 || added.Bindings[1].Ord != 1 || added.Bindings[0].ID != before.Bindings[1].ID {
				t.Fatal("new binding after deletion", added)
			}
		})
	}
}
