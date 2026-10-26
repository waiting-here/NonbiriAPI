package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/dbfixture"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
	"github.com/waiting-here/NonbiriAPI/internal/upgrade/v1"
)

func TestArgumentErrorsDoNotReadOrExposeEnvironment(t *testing.T) {
	t.Setenv("NONBIRI_MASTER_KEY", "private-key-value")
	t.Setenv("NONBIRI_DB_PATH", "")
	for _, args := range [][]string{nil, {"--check", "--apply", "--database", "private-path"}, {"--check"}, {"--check", "--database", "private-path", "--timeout", "0"}, {"--unknown", "private-path"}} {
		var output bytes.Buffer
		err := run(t.Context(), args, &output)
		if err == nil || strings.Contains(err.Error(), "private-") || output.Len() != 0 {
			t.Fatal("unsafe or accepted invalid arguments", err)
		}
	}
}

func TestSchemaInfoNeedsNoDatabaseOrSecretEnvironment(t *testing.T) {
	t.Setenv("NONBIRI_MASTER_KEY", "invalid-private-key")
	t.Setenv("NONBIRI_MASTER_KEY_FILE", "unavailable-private-file")
	t.Setenv("NONBIRI_DB_PATH", "unavailable-private-database")
	var output bytes.Buffer
	if err := run(t.Context(), []string{"--schema-info", "--json"}, &output); err != nil {
		t.Fatal(err)
	}
	var descriptor db.StorageSchemaDescriptor
	if err := json.Unmarshal(output.Bytes(), &descriptor); err != nil || descriptor.SchemaVersion != db.CurrentSchemaVersion || len(descriptor.OfflineConversions) != 18 || descriptor.StartupPolicy != "exact" || descriptor.MinimumSchemaVersion != db.CurrentSchemaVersion {
		t.Fatal(descriptor, err)
	}
	if strings.Contains(output.String(), "private-") {
		t.Fatal("environment appeared in schema metadata")
	}
	for _, args := range [][]string{{"--schema-info"}, {"--json"}, {"--schema-info", "--json", "--apply"}, {"--schema-info", "--json", "--check"}} {
		output.Reset()
		if err := run(t.Context(), args, &output); err == nil || output.Len() != 0 {
			t.Fatal("invalid metadata invocation accepted", args, err)
		}
	}
}

func TestCurrentDatabaseCommandNeedsOnlyEncryptionEnvironment(t *testing.T) {
	key := bytes.Repeat([]byte{0x23}, secret.MasterKeyBytes)
	vault, err := secret.New(key)
	if err != nil {
		t.Fatal(err)
	}
	defer vault.Close()
	path := filepath.Join(t.TempDir(), "current.sqlite")
	dbfixture.Materialize(t, path, vault)
	t.Setenv("NONBIRI_MASTER_KEY", base64.StdEncoding.EncodeToString(key))
	t.Setenv("NONBIRI_MASTER_KEY_FILE", "")
	t.Setenv("NONBIRI_DB_PATH", path)
	t.Setenv("NONBIRI_ADMIN_PASSWORD", "")
	t.Setenv("NONBIRI_DISCORD_CLIENT_ID", "")
	for _, mode := range []string{"--check", "--apply"} {
		var output bytes.Buffer
		var progress bytes.Buffer
		if err := runWithProgress(t.Context(), []string{mode}, &output, &progress); err != nil {
			t.Fatal(err)
		}
		var result v1.Result
		if err := json.Unmarshal(output.Bytes(), &result); err != nil || result.Status != "current" || result.Tool != "nonbiri-upgrade-v1" || result.TargetSchemaVersion != db.CurrentSchemaVersion || result.TargetManifestHash != db.PinnedGenerationTwoManifestHash || len(result.SupportedSourceSchemaVersions) != 18 {
			t.Fatal(output.String(), err)
		}
		decoder := json.NewDecoder(&progress)
		var audit, target bool
		for {
			var event v1.Progress
			err := decoder.Decode(&event)
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil || event.Type != "upgrade_progress" {
				t.Fatal(event, err)
			}
			audit = audit || event.Phase == "audit"
			target = target || event.Phase == "target" && event.State == "completed"
		}
		if !target || audit != (mode == "--check") {
			t.Fatal("progress did not describe explicit audit scope", mode, audit, target)
		}
	}
}
