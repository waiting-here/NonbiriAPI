package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/dbfixture"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
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
		if err := run(t.Context(), []string{mode}, &output); err != nil {
			t.Fatal(err)
		}
		var result struct {
			Status string `json:"status"`
		}
		if err := json.Unmarshal(output.Bytes(), &result); err != nil || result.Status != "current" {
			t.Fatal(output.String(), err)
		}
	}
}
