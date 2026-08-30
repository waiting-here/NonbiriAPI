//go:build linux && amd64

package db

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestGenerationTwoRejectsParentSymlinkBeforeMkdirAll(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatalf("create symlink target: %v", err)
	}
	link := filepath.Join(root, "parent-link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("create parent symlink: %v", err)
	}
	dbPath := filepath.Join(link, "created-by-mkdirall", "database.db")
	assertBootstrapStartupKind(t, prepareDBDirectory(dbPath), StartupUnsafePath)
	if _, err := os.Lstat(filepath.Join(target, "created-by-mkdirall")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("MkdirAll followed parent symlink: %v", err)
	}
}
