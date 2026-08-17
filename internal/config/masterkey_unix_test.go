//go:build unix

package config

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

func TestMasterKeyFileUnixOwnerReadPolicy(t *testing.T) {
	for _, test := range []struct {
		name string
		mode os.FileMode
		want bool
	}{
		{name: "read_only", mode: 0o400, want: true},
		{name: "read_write", mode: 0o600, want: true},
		{name: "write_only", mode: 0o200, want: false},
		{name: "no_permissions", mode: 0o000, want: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "master.key")
			if err := os.WriteFile(path, bytes.Repeat([]byte{0x65}, secret.MasterKeyBytes), 0o600); err != nil {
				t.Fatalf("write key file: %v", err)
			}
			if err := os.Chmod(path, test.mode); err != nil {
				t.Fatalf("set key-file mode: %v", err)
			}
			key, _, err := loadMasterKeyFile(path)
			clear(key)
			if test.want && err != nil {
				t.Fatalf("owner-readable key file was rejected: %v", err)
			}
			if !test.want && err == nil {
				t.Fatal("owner-unreadable key file was accepted")
			}
		})
	}
}

func TestMasterKeyFileUnixRejectsRegularIdentitySwap(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "master.key")
	moved := filepath.Join(dir, "original.key")
	replacement := filepath.Join(dir, "replacement.key")
	if err := os.WriteFile(path, bytes.Repeat([]byte{0x71}, secret.MasterKeyBytes), 0o600); err != nil {
		t.Fatalf("write original key: %v", err)
	}
	if err := os.WriteFile(replacement, bytes.Repeat([]byte{0x72}, secret.MasterKeyBytes), 0o600); err != nil {
		t.Fatalf("write replacement key: %v", err)
	}
	before, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("inspect original key: %v", err)
	}
	if err := os.Rename(path, moved); err != nil {
		t.Fatalf("move original key: %v", err)
	}
	if err := os.Rename(replacement, path); err != nil {
		t.Fatalf("publish replacement key: %v", err)
	}

	file, _, err := openValidatedMasterKeyFile(path, before)
	if file != nil {
		_ = file.Close()
	}
	if err == nil {
		t.Fatal("identity validation accepted a different regular file substituted after Lstat")
	}
}

func TestMasterKeyFileUnixRejectsSymlinkSwapToValidatedInode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "master.key")
	moved := filepath.Join(dir, "validated.key")
	if err := os.WriteFile(path, bytes.Repeat([]byte{0x83}, secret.MasterKeyBytes), 0o600); err != nil {
		t.Fatalf("write original key: %v", err)
	}
	before, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("inspect original key: %v", err)
	}

	// Reproduce the old Lstat/Open gap exactly: keep the validated inode, then
	// put a symlink to that same inode at the configured pathname. SameFile by
	// itself accepts this substitution after a following open; O_NOFOLLOW must
	// reject it before the target can be opened.
	if err := os.Rename(path, moved); err != nil {
		t.Fatalf("move validated key: %v", err)
	}
	if err := os.Symlink(filepath.Base(moved), path); err != nil {
		t.Fatalf("substitute symlink: %v", err)
	}

	file, _, err := openValidatedMasterKeyFile(path, before)
	if file != nil {
		_ = file.Close()
	}
	if err == nil {
		t.Fatal("no-follow open accepted a symlink to the previously validated inode")
	}
}
