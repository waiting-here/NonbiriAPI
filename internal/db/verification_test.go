package db

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestOrdinaryReopenAvoidsSnapshotsAndFullFileReads(t *testing.T) {
	path := bootstrapTestPath(t, "lean.sqlite")
	vault := bootstrapTestVault(t)
	store, err := Open(path, vault)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	copied := false
	trace := NewStartupTrace(time.Now(), func(p StartupProgress) {
		if p.Stage == StageSnapshotCopy || p.Stage == StageSnapshotValidation {
			copied = true
		}
	})
	reopened, err := OpenContext(trace.Context(context.Background()), path, vault)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if copied || trace.Snapshot().ProcessedBytes != 0 {
		t.Fatalf("ordinary startup performed a full copy/read: %+v", trace.Snapshot())
	}
}
func TestVerifyReadOnlyOwnershipAndNoInitialization(t *testing.T) {
	path := bootstrapTestPath(t, "verify.sqlite")
	vault := bootstrapTestVault(t)
	store, err := Open(path, vault)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyContext(context.Background(), path, vault, nil); !errors.Is(err, ErrDatabaseInUse) {
		t.Fatalf("owned verify: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	called := false
	if err := VerifyContext(ActiveRecoveryContext(context.Background()), path, vault, func(ctx context.Context, d *sql.DB) error {
		called = true
		if IsActiveRecovery(ctx) {
			t.Fatal("explicit verification inherited active-only scope")
		}
		if _, err := d.ExecContext(ctx, `UPDATE site_config SET value='changed' WHERE key='site_name'`); err == nil {
			t.Fatal("verify source is writable")
		}
		if _, err := Open(path, vault); !errors.Is(err, ErrDatabaseInUse) {
			t.Fatalf("verify lost process ownership: %v", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("full audit not called")
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("verify changed source", err)
	}
	missing := filepath.Join(filepath.Dir(path), "missing.sqlite")
	if err := VerifyContext(context.Background(), missing, vault, nil); err == nil {
		t.Fatal("missing source accepted")
	}
	if _, err := os.Stat(missing); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("verify initialized storage", err)
	}
}
