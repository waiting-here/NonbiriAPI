package app

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/dbfixture"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

func TestMaintenanceVerifyAuditsWithoutRecoveryOrWrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "verify.sqlite")
	dbfixture.Materialize(t, path)
	for key, value := range map[string]string{
		"NONBIRI_LISTEN_ADDR": "127.0.0.1:9090", "NONBIRI_DB_PATH": path,
		"NONBIRI_ADMIN_USERNAME": "operator", "NONBIRI_ADMIN_PASSWORD": "synthetic-password",
		"NONBIRI_DISCORD_CLIENT_ID": "synthetic-client", "NONBIRI_DISCORD_CLIENT_SECRET": "synthetic-secret",
		"NONBIRI_SITE_BASE_URL": "https://example.test", "NONBIRI_MASTER_KEY": strings.Repeat("53", 32), "NONBIRI_MASTER_KEY_FILE": "",
	} {
		t.Setenv(key, value)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := runMaintenance([]string{"maintenance", "verify"}, &output); err != nil {
		t.Fatal(err)
	}
	if output.String() != "{\"status\":\"verified\"}\n" {
		t.Fatal(output.String())
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("verify changed source", err)
	}
	vault, err := secret.New(bytes.Repeat([]byte{0x53}, 32))
	if err != nil {
		t.Fatal(err)
	}
	defer vault.Close()
	store, err := db.Open(path, vault)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().Exec(`UPDATE observability_state SET access_rows=1 WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if err := runMaintenance([]string{"maintenance", "verify"}, &output); err == nil {
		t.Fatal("verify ignored counter mismatch")
	}
	reopened, err := db.OpenContext(context.Background(), path, vault)
	if err != nil {
		t.Fatal(err)
	}
	var count int
	if err := reopened.DB().QueryRow(`SELECT access_rows FROM observability_state WHERE id=1`).Scan(&count); err != nil || count != 1 {
		t.Fatal("ordinary startup reconciled counter", count, err)
	}
	reopened.Close()
	t.Setenv("NONBIRI_DB_PATH", filepath.Join(filepath.Dir(path), "missing.sqlite"))
	if err := runMaintenance([]string{"maintenance", "verify"}, &output); err == nil {
		t.Fatal("missing source accepted")
	}
	if _, err := os.Stat(os.Getenv("NONBIRI_DB_PATH")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("verify initialized source", err)
	}
}
