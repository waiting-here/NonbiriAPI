package dbfixture

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

func fixtureVault(t *testing.T, fill byte) *secret.Vault {
	t.Helper()
	key := bytes.Repeat([]byte{fill}, secret.MasterKeyBytes)
	vault, err := secret.New(key)
	clear(key)
	if err != nil {
		t.Fatalf("secret.New: %v", err)
	}
	t.Cleanup(func() { _ = vault.Close() })
	return vault
}

func TestImageCacheBuildsOnceAcrossConcurrentCallers(t *testing.T) {
	var calls atomic.Int64
	cache := &imageCache{build: func() ([]byte, error) {
		calls.Add(1)
		return []byte("template"), nil
	}}
	const callers = 32
	var wg sync.WaitGroup
	errs := make(chan error, callers)
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			image, err := cache.load()
			if err != nil {
				errs <- err
				return
			}
			if string(image) != "template" {
				errs <- errors.New("unexpected cached image")
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("template builds=%d, want 1", got)
	}
}

func TestMaterializeCreatesPrivateIndependentValidatedStores(t *testing.T) {
	root := t.TempDir()
	if runtime.GOOS != "windows" {
		if err := os.Chmod(root, 0o755); err != nil {
			t.Fatalf("make fixture parent permissive: %v", err)
		}
	}
	firstPath := filepath.Join(root, "first.sqlite")
	secondPath := filepath.Join(root, "second.sqlite")
	Materialize(t, firstPath)
	Materialize(t, secondPath)

	if runtime.GOOS != "windows" {
		parentInfo, err := os.Stat(root)
		if err != nil {
			t.Fatalf("stat fixture parent: %v", err)
		}
		if got := parentInfo.Mode().Perm(); got != 0o700 {
			t.Fatalf("fixture parent mode=%04o, want 0700", got)
		}
		fileInfo, err := os.Stat(firstPath)
		if err != nil {
			t.Fatalf("stat fixture database: %v", err)
		}
		if got := fileInfo.Mode().Perm(); got != 0o600 {
			t.Fatalf("fixture database mode=%04o, want 0600", got)
		}
	}
	for _, path := range []string{firstPath, secondPath} {
		for _, suffix := range []string{"-journal", "-wal", "-shm"} {
			if _, err := os.Lstat(path + suffix); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("materialized fixture has sidecar %s: %v", suffix, err)
			}
		}
	}

	first, err := db.Open(firstPath, fixtureVault(t, 0x11))
	if err != nil {
		t.Fatalf("open first materialized store: %v", err)
	}
	t.Cleanup(func() { _ = first.Close() })
	second, err := db.Open(secondPath, fixtureVault(t, 0x22))
	if err != nil {
		t.Fatalf("open second materialized store: %v", err)
	}
	t.Cleanup(func() { _ = second.Close() })
	if _, err := first.DB().Exec(`UPDATE site_config SET value='fixture-one' WHERE key='site_name'`); err != nil {
		t.Fatalf("mutate first materialized store: %v", err)
	}
	var secondName string
	if err := second.DB().QueryRow(`SELECT value FROM site_config WHERE key='site_name'`).Scan(&secondName); err != nil {
		t.Fatalf("read second materialized store: %v", err)
	}
	if secondName == "fixture-one" {
		t.Fatal("materialized stores shared mutable state")
	}
}

func TestMaterializeRejectsExistingTargetWithoutOverwrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "existing.sqlite")
	want := []byte("owner data")
	if err := os.WriteFile(path, want, 0o600); err != nil {
		t.Fatalf("write existing target: %v", err)
	}
	if err := materialize(path); err == nil {
		t.Fatal("materialize unexpectedly replaced an existing target")
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read existing target: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("existing target changed: got %q want %q", got, want)
	}
}

func TestMaterializeConcurrentCopiesAreDistinct(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatalf("secure concurrent fixture parent: %v", err)
	}
	const copies = 12
	var wg sync.WaitGroup
	errs := make(chan error, copies)
	paths := make([]string, copies)
	for i := range paths {
		paths[i] = filepath.Join(root, "copy-"+string(rune('a'+i))+".sqlite")
		wg.Add(1)
		go func(path string) {
			defer wg.Done()
			if err := materialize(path); err != nil {
				errs <- err
			}
		}(paths[i])
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	seen := make([]struct {
		info os.FileInfo
		path string
	}, 0, copies)
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat concurrent copy: %v", err)
		}
		for _, prior := range seen {
			if os.SameFile(prior.info, info) {
				t.Fatalf("copies %s and %s share one file identity", prior.path, path)
			}
		}
		seen = append(seen, struct {
			info os.FileInfo
			path string
		}{info: info, path: path})
	}
}
