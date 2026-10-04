package db

import (
	"errors"
	"testing"
)

func TestRoutingExtensionRejectsModifiedPriorSchema(t *testing.T) {
	path := bootstrapTestPath(t, "routing-extension-invalid.sqlite")
	vault := bootstrapTestVault(t)
	store, err := Open(path, vault)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().Exec(`DROP TABLE dispatch_response_starts; DROP TABLE charity_model_routing; DROP TABLE endpoint_key_limits; DROP INDEX idx_dispatch_claims_key_active; DROP INDEX idx_dispatch_claims_key_rpm; DROP INDEX idx_users_created`); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	before := snapshotBootstrapSources(t, path)
	reopened, err := Open(path, vault)
	if reopened != nil {
		reopened.Close()
		t.Fatal("modified old schema accepted")
	}
	var startup *StartupError
	if !errors.As(err, &startup) {
		t.Fatalf("expected startup rejection: %v", err)
	}
	assertBootstrapSourcesUnchanged(t, path, before)
}
