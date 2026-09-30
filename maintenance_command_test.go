package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/dbfixture"
	"github.com/waiting-here/NonbiriAPI/internal/fatfish"
)

func TestOfflineCleanupCommandPlansAppliesAndReplays(t *testing.T) {
	dir := t.TempDir()
	database := filepath.Join(dir, "maintenance.sqlite")
	dbfixture.Materialize(t, database)
	for key, value := range map[string]string{
		"NONBIRI_LISTEN_ADDR": "127.0.0.1:9090", "NONBIRI_DB_PATH": database,
		"NONBIRI_ADMIN_USERNAME": "operator", "NONBIRI_ADMIN_PASSWORD": "synthetic-password",
		"NONBIRI_DISCORD_CLIENT_ID": "synthetic-client", "NONBIRI_DISCORD_CLIENT_SECRET": "synthetic-secret",
		"NONBIRI_SITE_BASE_URL": "https://example.test", "NONBIRI_MASTER_KEY": strings.Repeat("53", 32),
		"NONBIRI_MASTER_KEY_FILE": "",
	} {
		t.Setenv(key, value)
	}
	source := fatfish.CleanupSource{InstanceIdentity: strings.Repeat("1", 64), SourceCommit: strings.Repeat("2", 40), SourceTree: strings.Repeat("3", 40), SourceSchemaHash: strings.Repeat("4", 64)}
	sourcePath := filepath.Join(dir, "source.json")
	raw, err := json.Marshal(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sourcePath, raw, 0600); err != nil {
		t.Fatal(err)
	}
	var plan bytes.Buffer
	if err := runMaintenance([]string{"maintenance", "fatfish-cleanup-plan", "--source", sourcePath}, &plan); err != nil {
		t.Fatal(err)
	}
	var manifest fatfish.CleanupManifest
	if err := json.Unmarshal(plan.Bytes(), &manifest); err != nil || manifest.Source != source || len(manifest.Rows) != 0 {
		t.Fatal(manifest, err)
	}
	manifestPath := filepath.Join(dir, "manifest.json")
	if err := os.WriteFile(manifestPath, plan.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	var first bytes.Buffer
	args := []string{"maintenance", "fatfish-cleanup", "--source", sourcePath, "--manifest", manifestPath, "--operation-key", "synthetic-cleanup-key"}
	if err := runMaintenance(args, &first); err != nil {
		t.Fatal(err)
	}
	var repeated bytes.Buffer
	if err := runMaintenance(args, &repeated); err != nil || !bytes.Equal(first.Bytes(), repeated.Bytes()) {
		t.Fatal("replay differs", err)
	}
	var receipt fatfish.CleanupReceipt
	if err := json.Unmarshal(first.Bytes(), &receipt); err != nil || receipt.Kind != "fatfish-legacy" {
		t.Fatal(receipt, err)
	}
	manifest.Source.SourceCommit = strings.Repeat("5", 40)
	raw, err = json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifestPath, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if err := runMaintenance(args, &repeated); err == nil {
		t.Fatal("different source accepted")
	}
	t.Setenv("NONBIRI_DB_PATH", filepath.Join(dir, "missing.sqlite"))
	if err := runMaintenance([]string{"maintenance", "fatfish-cleanup-plan", "--source", sourcePath}, &plan); err == nil {
		t.Fatal("missing database initialized")
	}
	if _, err := os.Stat(filepath.Join(dir, "missing.sqlite")); !os.IsNotExist(err) {
		t.Fatal("maintenance created a database", err)
	}
}

func TestOfflineCleanupCommandRequiresExplicitInputs(t *testing.T) {
	for _, args := range [][]string{{"cleanup"}, {"maintenance", "fatfish-cleanup"}, {"maintenance", "fatfish-cleanup-plan", "--source", "file", "--operation-key", "key"}} {
		if err := runMaintenance(args, &bytes.Buffer{}); err == nil {
			t.Fatal("ambiguous invocation accepted", args)
		}
	}
}
