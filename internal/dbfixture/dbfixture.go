// Package dbfixture materializes isolated Generation 2 SQLite databases for
// tests that need current seeded state but do not exercise fresh-startup
// behavior. Production packages must not depend on it.
package dbfixture

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/dbtest"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

type imageCache struct {
	once  sync.Once
	build func() ([]byte, error)
	image []byte
	err   error
}

func (cache *imageCache) load() ([]byte, error) {
	cache.once.Do(func() {
		cache.image, cache.err = cache.build()
	})
	return cache.image, cache.err
}

var generationTwoTemplate = imageCache{build: buildGenerationTwoTemplate}

// Materialize writes a private, isolated copy of the process-local Generation
// 2 template to path. The target must not already exist. Callers still open the
// result with db.Open so the production current-store validation path remains
// covered by every fixture.
func Materialize(t testing.TB, path string) {
	t.Helper()
	dir := filepath.Dir(path)
	if dir != "." && dir != "" && dir != string(filepath.Separator) {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatalf("dbfixture: create database parent %s: %v", dir, err)
		}
	}
	dbtest.EnsureOwnerOnlyParent(t, path)
	if err := materialize(path); err != nil {
		t.Fatalf("dbfixture: materialize %s: %v", path, err)
	}
}

func materialize(path string) error {
	image, err := generationTwoTemplate.load()
	if err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create isolated database: %w", err)
	}
	keep := false
	defer func() {
		if !keep {
			_ = file.Close()
			_ = os.Remove(path)
		}
	}()
	for written := 0; written < len(image); {
		n, writeErr := file.Write(image[written:])
		if writeErr != nil {
			return fmt.Errorf("write isolated database: %w", writeErr)
		}
		if n == 0 {
			return errors.New("write isolated database: zero-length write")
		}
		written += n
	}
	if err := file.Chmod(0o600); err != nil {
		return fmt.Errorf("secure isolated database: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close isolated database: %w", err)
	}
	keep = true
	return nil
}

func buildGenerationTwoTemplate() (image []byte, resultErr error) {
	dir, err := os.MkdirTemp("", "nonbiri-dbfixture-")
	if err != nil {
		return nil, fmt.Errorf("create template directory: %w", err)
	}
	defer func() {
		if err := os.RemoveAll(dir); err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("remove template directory: %w", err))
		}
	}()
	if err := os.Chmod(dir, 0o700); err != nil {
		return nil, fmt.Errorf("secure template directory: %w", err)
	}

	key := bytes.Repeat([]byte{0x41}, secret.MasterKeyBytes)
	vault, err := secret.New(key)
	clear(key)
	if err != nil {
		return nil, fmt.Errorf("create template secret codec: %w", err)
	}
	path := filepath.Join(dir, "generation-two.sqlite")
	store, err := db.Open(path, vault)
	if err != nil {
		_ = vault.Close()
		return nil, fmt.Errorf("create Generation 2 template: %w", err)
	}
	fail := func(cause error) ([]byte, error) {
		return nil, errors.Join(cause, store.Close(), vault.Close())
	}

	var busy, logFrames, checkpointed int
	if err := store.DB().QueryRow(`PRAGMA wal_checkpoint(TRUNCATE)`).Scan(&busy, &logFrames, &checkpointed); err != nil {
		return fail(fmt.Errorf("checkpoint Generation 2 template: %w", err))
	}
	if busy != 0 || logFrames < 0 || checkpointed < 0 || checkpointed > logFrames {
		return fail(fmt.Errorf("checkpoint Generation 2 template returned (%d,%d,%d)", busy, logFrames, checkpointed))
	}
	var secretCount int
	if err := store.DB().QueryRow(`SELECT COUNT(*) FROM endpoint_key_secrets`).Scan(&secretCount); err != nil {
		return fail(fmt.Errorf("inspect Generation 2 template credentials: %w", err))
	}
	if secretCount != 0 {
		return fail(fmt.Errorf("Generation 2 template contains %d credential rows", secretCount))
	}
	if err := errors.Join(store.Close(), vault.Close()); err != nil {
		return nil, fmt.Errorf("close Generation 2 template: %w", err)
	}

	info, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("inspect Generation 2 template: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, errors.New("Generation 2 template is not a regular file")
	}
	image, err = os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read Generation 2 template: %w", err)
	}
	if len(image) < 100 {
		return nil, errors.New("Generation 2 template is too short")
	}
	return image, nil
}
