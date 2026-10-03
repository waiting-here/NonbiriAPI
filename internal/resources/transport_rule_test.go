package resources

import (
	"context"
	"errors"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/transportpolicy"
)

func TestModelTransportPersistenceReplayAndExport(t *testing.T) {
	e := newResourceTestEnvironment(t)
	owner := e.seedUser(t, "transport-owner")
	other := e.seedUser(t, "transport-other")
	m := e.createModel(t, owner, resourceTestKey('a'), "transport", "model")
	id := resourceTestID(t, m.ID)
	if m.TransportRule != transportpolicy.Passthrough {
		t.Fatal(m.TransportRule)
	}
	for index, rule := range []transportpolicy.Rule{transportpolicy.ForceNonStream, transportpolicy.ForceStream, transportpolicy.Passthrough} {
		rev := int64(index + 1)
		input := PatchModelInput{TransportRule: &rule, ExpectedRevision: rev}
		mutation := resourceTestMutation(t, resourceTestKey(byte('b'+index)), "PATCH", routeModel, []int64{id}, input)
		changed, err := e.repository.PatchModel(context.Background(), owner, id, mutation, input)
		if err != nil || changed.Value.TransportRule != rule {
			t.Fatalf("%+v %v", changed, err)
		}
		replay, err := e.repository.PatchModel(context.Background(), owner, id, mutation, input)
		if err != nil || !replay.Replayed || replay.Value.TransportRule != rule {
			t.Fatalf("%+v %v", replay, err)
		}
	}
	rule := transportpolicy.ForceStream
	input := PatchModelInput{TransportRule: &rule, ExpectedRevision: 4}
	_, err := e.repository.PatchModel(context.Background(), owner, id, resourceTestMutation(t, resourceTestKey('f'), "PATCH", routeModel, []int64{id}, input), input)
	if err != nil {
		t.Fatal(err)
	}
	rename := "renamed"
	omitted := PatchModelInput{Model: &rename, ExpectedRevision: 5}
	changed, err := e.repository.PatchModel(context.Background(), owner, id, resourceTestMutation(t, resourceTestKey('g'), "PATCH", routeModel, []int64{id}, omitted), omitted)
	if err != nil || changed.Value.TransportRule != rule {
		t.Fatalf("omission %+v %v", changed, err)
	}
	if _, err = e.repository.GetModel(context.Background(), other, id); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	invalid := transportpolicy.Rule("invalid")
	bad := PatchModelInput{TransportRule: &invalid, ExpectedRevision: 6}
	if _, err = e.repository.PatchModel(context.Background(), owner, id, resourceTestMutation(t, resourceTestKey('h'), "PATCH", routeModel, []int64{id}, bad), bad); !errors.Is(err, ErrInvalidRequest) {
		t.Fatal(err)
	}
	tx, err := e.store.DB().Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	exported, err := e.repository.ExportLifecycleResources(context.Background(), tx, owner, resourceTestNow, 100)
	if err != nil || len(exported.Models) != 1 || exported.Models[0].TransportRule != rule {
		t.Fatalf("export %+v %v", exported, err)
	}
}
