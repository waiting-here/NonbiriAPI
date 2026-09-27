package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

func TestRunCreatesClosedCredentialFreeTemplate(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatalf("secure output parent: %v", err)
	}
	path := filepath.Join(dir, "template.sqlite")
	var output bytes.Buffer
	if err := run([]string{"-output", path}, &output); err != nil {
		t.Fatalf("run builder: %v", err)
	}
	if !strings.Contains(output.String(), "built closed template") {
		t.Fatalf("missing build result: %q", output.String())
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		t.Fatalf("template is not a regular file: %v", err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("template mode=%04o, want 0600", info.Mode().Perm())
	}
	for _, suffix := range []string{"-journal", "-wal", "-shm"} {
		if _, err := os.Lstat(path + suffix); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("closed template has sidecar %s: %v", suffix, err)
		}
	}
	key := bytes.Repeat([]byte{0x55}, secret.MasterKeyBytes)
	vault, err := secret.New(key)
	clear(key)
	if err != nil {
		t.Fatalf("create fresh vault: %v", err)
	}
	t.Cleanup(func() { _ = vault.Close() })
	store, err := db.Open(path, vault)
	if err != nil {
		t.Fatalf("reopen closed template: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	var credentials int
	if err := store.DB().QueryRow(`SELECT COUNT(*) FROM endpoint_key_secrets`).Scan(&credentials); err != nil || credentials != 0 {
		t.Fatalf("credential rows=%d err=%v", credentials, err)
	}
	var users int
	if err := store.DB().QueryRow(`SELECT COUNT(*) FROM users`).Scan(&users); err != nil || users != 0 {
		t.Fatalf("user rows=%d err=%v", users, err)
	}
}

func TestRunRejectsExistingTargetWithoutOverwrite(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatalf("secure output parent: %v", err)
	}
	path := filepath.Join(dir, "existing.sqlite")
	want := []byte("owner data")
	if err := os.WriteFile(path, want, 0o600); err != nil {
		t.Fatalf("write existing target: %v", err)
	}
	if err := run([]string{"-output", path}, &bytes.Buffer{}); err == nil {
		t.Fatal("builder replaced existing target")
	}
	got, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("existing target changed: got=%q err=%v", got, err)
	}
}
