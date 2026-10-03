package routing

import (
	"context"
	"strconv"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/transportpolicy"
)

func TestTransportRuleFrozenInLogicalAndCandidateSnapshots(t *testing.T) {
	e := newSnapshotTestEnvironment(t)
	user := e.seedUser(t, "transport")
	id := e.seedModel(t, user, "transport", "model")
	e.seedAvailableBinding(t, user, id, "upstream")
	for _, rule := range []transportpolicy.Rule{transportpolicy.Passthrough, transportpolicy.ForceNonStream, transportpolicy.ForceStream} {
		if _, err := e.store.DB().Exec("UPDATE models SET transport_rule=? WHERE id=?", rule, id); err != nil {
			t.Fatal(err)
		}
		p, err := e.routing.Preflight(context.Background(), user, strconv.FormatInt(id, 10))
		if err != nil {
			t.Fatal(err)
		}
		snapshot, err := e.routing.Snapshot(context.Background(), user, Identity{ModelID: strconv.FormatInt(id, 10)})
		if err != nil {
			t.Fatal(err)
		}
		if p.TransportRule() != rule || snapshot.TransportRule() != rule {
			t.Fatal(p.TransportRule(), snapshot.TransportRule())
		}
		if _, err := e.store.DB().Exec("UPDATE models SET transport_rule='passthrough' WHERE id=?", id); err != nil {
			t.Fatal(err)
		}
		if p.TransportRule() != rule || snapshot.TransportRule() != rule {
			t.Fatal("frozen policy changed")
		}
	}
}
