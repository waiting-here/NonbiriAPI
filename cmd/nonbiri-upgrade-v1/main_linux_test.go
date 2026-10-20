package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/dbfixture"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

func TestCLISurvivesClosedStderrPipe(t *testing.T) {
	const helperEnv = "NONBIRI_TEST_CLOSED_STDERR_PIPE"
	if os.Getenv(helperEnv) == "1" {
		os.Args = []string{"nonbiri-upgrade-v1", "--apply"}
		main()
		os.Exit(0)
	}
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
	t.Setenv(helperEnv, "1")

	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestCLISurvivesClosedStderrPipe$")
	// Use an actual child fd 2: an ordinary failing io.Writer does not exercise
	// Go's Unix SIGPIPE behavior for stdout and stderr.
	command.Stderr = writer
	var output bytes.Buffer
	command.Stdout = &output
	if err := command.Run(); err != nil {
		t.Fatalf("CLI stopped after progress reader disconnected: %v", err)
	}
	var result struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(output.Bytes(), &result); err != nil || result.Status != "current" {
		t.Fatalf("missing final CLI result: %q, %v", output.String(), err)
	}
}
