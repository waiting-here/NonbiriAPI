package resources

import (
	"context"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/connectionstore/testseed"
	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

func TestModelBrowseBatchKeepsPerModelPreviewOrderAndOwnerScope(t *testing.T) {
	env := newResourceTestEnvironment(t)
	owner, other := env.seedUser(t, "model-browse-owner"), env.seedUser(t, "model-browse-other")
	ep := env.createEndpoint(t, owner, resourceTestKey('a'))
	key := env.createEndpointKey(t, owner, resourceTestID(t, ep.ID), resourceTestKey('b'))
	keyID := resourceTestID(t, key.ID)
	seedBrowseModels(t, env, owner, other, keyID, 3)
	foreignEndpoint := env.createEndpoint(t, other, resourceTestKey('c'))
	foreignKey := env.createEndpointKey(t, other, resourceTestID(t, foreignEndpoint.ID), resourceTestKey('d'))
	foreignKeyID := resourceTestID(t, foreignKey.ID)
	tx, err := env.store.DB().Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var modelID int64
	if err := tx.QueryRow(`SELECT id FROM models WHERE user_id=? AND model='model-00000'`, owner).Scan(&modelID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`UPDATE model_bindings SET ord=1 WHERE model_id=?`, modelID); err != nil {
		t.Fatal(err)
	}
	for i, name := range []string{"automatic", "stale", "unsupported"} {
		seedResourceBindingTx(t, tx, modelID, keyID, name, i+2)
	}
	// A foreign key cannot be attached to an owned model, even through storage.
	if _, err := tx.Exec("SAVEPOINT foreign_target"); err != nil {
		t.Fatal(err)
	}
	if _, err := testseed.InsertModelBindingTx(context.Background(), tx, testseed.BindingInput{ModelID: modelID, KeyID: foreignKeyID, UpstreamModelID: "foreign", At: resourceTestNow}); err == nil {
		t.Fatal("cross-owner target accepted")
	}
	if _, err := tx.Exec("ROLLBACK TO foreign_target"); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec("RELEASE foreign_target"); err != nil {
		t.Fatal(err)
	}
	var foreignModelID int64
	if err := tx.QueryRow("SELECT id FROM models WHERE user_id=?", other).Scan(&foreignModelID); err != nil {
		t.Fatal(err)
	}
	seedResourceBindingTx(t, tx, foreignModelID, foreignKeyID, "foreign", 0)
	if _, err := tx.Exec(`DELETE FROM model_bindings WHERE model_id IN (SELECT id FROM models WHERE user_id=? AND model='model-00001')`, owner); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	page, err := env.repository.ListModelsPage(context.Background(), owner, pagination.Default())
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Data) != 3 || page.Data[0].Model != "model-00002" || page.Data[2].Model != "model-00000" {
		t.Fatalf("model page order changed: %+v", page.Data)
	}
	empty := page.Data[1].Browse
	if empty == nil || empty.AvailableBindingCount != "0" || empty.Preview == nil || len(empty.Preview) != 0 {
		t.Fatalf("empty summary=%+v", empty)
	}
	got := page.Data[2].Browse
	if got == nil || got.AvailableBindingCount != "4" || len(got.Preview) != 3 {
		t.Fatalf("bounded summary=%+v", got)
	}
	for i, name := range []string{"manual", "automatic", "stale"} {
		if got.Preview[i].UpstreamModelID != name || got.Preview[i].EndpointKeyID != key.ID || got.Preview[i].Ord != i+1 {
			t.Fatalf("preview %d=%+v", i, got.Preview[i])
		}
	}
	if got.Preview[2].State != "available" {
		t.Fatalf("saved target lost authority after catalog expiration: %+v", got.Preview[2])
	}
}
