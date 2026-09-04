package db

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGenerationTwoRejectsNonDirectoryParentBeforeMkdirAll(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "parent-file")
	if err := os.WriteFile(file, []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("create parent file: %v", err)
	}
	dbPath := filepath.Join(file, "database.db")
	assertBootstrapStartupKind(t, prepareDBDirectory(dbPath), StartupUnsafePath)
}

func TestGenerationTwoRejectsSpecialFinalPathWithoutOpeningIt(t *testing.T) {
	root := t.TempDir()
	dbPath := filepath.Join(root, "database.db")
	if err := os.Mkdir(dbPath, 0o700); err != nil {
		t.Fatalf("create final directory fixture: %v", err)
	}
	_, err := Open(dbPath, bootstrapTestVault(t))
	assertBootstrapStartupKind(t, err, StartupUnsafePath)
}
