package app

import (
	"bytes"
	"encoding/json"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"strings"
	"testing"
)

func TestSchemaInfoDoesNotLoadEnvironmentOrOpenDatabase(t *testing.T) {
	t.Setenv("NONBIRI_MASTER_KEY", "invalid")
	t.Setenv("NONBIRI_DB_PATH", t.TempDir()+"/absent.sqlite")
	var output bytes.Buffer
	if err := runMaintenance([]string{"maintenance", "schema-info", "--json"}, &output); err != nil {
		t.Fatal(err)
	}
	var descriptor db.StorageSchemaDescriptor
	if err := json.Unmarshal(output.Bytes(), &descriptor); err != nil {
		t.Fatal(err)
	}
	if descriptor.FormatVersion != 1 || descriptor.SchemaVersion != db.CurrentSchemaVersion || descriptor.MinimumSchemaVersion != db.MinimumSchemaVersion || descriptor.ManifestHash == "" {
		t.Fatal(descriptor)
	}
	if len(descriptor.OfflineConversions) != 0 || len(descriptor.SourceSchemaVersions) != 3 || descriptor.SourceSchemaVersions[0] != 33 || descriptor.SourceSchemaVersions[1] != 34 || descriptor.SourceSchemaVersions[2] != 35 || len(descriptor.SourceManifestHashes) != 3 || descriptor.SourceManifestHashes[0] != db.Schema33ManifestHash || descriptor.SourceManifestHashes[1] != db.Schema34ManifestHash || descriptor.SourceManifestHashes[2] != db.Schema35ManifestHash {
		t.Fatal("schema compatibility metadata", descriptor)
	}
}

func TestRetiredMaintenanceCommandsAreRejected(t *testing.T) {
	for _, command := range []string{"fatfish-cleanup", "fatfish-cleanup-plan", "schema-info"} {
		var output bytes.Buffer
		if err := runMaintenance([]string{"maintenance", command}, &output); err == nil || !strings.Contains(err.Error(), "usage:") {
			t.Fatal(command, err)
		}
		if output.Len() != 0 {
			t.Fatal("rejected command wrote output")
		}
	}
}
