// Package dbfixture materializes isolated Generation 2 SQLite databases for
// tests that need current seeded state but do not exercise fresh-startup
// behavior. Tests of Open, bootstrap, locks or injected startup failures must
// keep their real startup path, using dbtest for directory setup when needed.
// Production packages must not depend on this package.
package dbfixture

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
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

const (
	RaceTemplatePathEnv = "NONBIRI_RACE_TEMPLATE_PATH"
	RaceTemplateSHAEnv  = "NONBIRI_RACE_TEMPLATE_SHA256"
	RaceTemplateIDEnv   = "NONBIRI_RACE_TEMPLATE_ID"
)

var generationTwoTemplate = imageCache{build: loadOrBuildGenerationTwoTemplate}

// BuildGenerationTwoTemplate builds a fresh image for the shared CI fixture
// helper. It never reads an inherited shared template.
func BuildGenerationTwoTemplate() ([]byte, error) {
	return buildGenerationTwoTemplate()
}

func loadOrBuildGenerationTwoTemplate() ([]byte, error) {
	path, hasPath := os.LookupEnv(RaceTemplatePathEnv)
	wantSHA, hasSHA := os.LookupEnv(RaceTemplateSHAEnv)
	identity, hasID := os.LookupEnv(RaceTemplateIDEnv)
	if !hasPath && !hasSHA && !hasID {
		return buildGenerationTwoTemplate()
	}
	if !hasPath || !hasSHA || !hasID {
		return nil, errors.New("race template environment is incomplete")
	}
	return loadRaceTemplate(path, wantSHA, identity)
}

func loadRaceTemplate(path, wantSHA, identity string) ([]byte, error) {
	if !validDigest(wantSHA) || !validDigest(identity) {
		return nil, errors.New("race template digest or identity is invalid")
	}
	if !filepath.IsAbs(path) || filepath.Base(path) != identity+".sqlite" {
		return nil, errors.New("race template path does not match its run identity")
	}
	dir := filepath.Dir(path)
	parent, err := os.Lstat(dir)
	if err != nil || !parent.IsDir() || parent.Mode()&os.ModeSymlink != 0 ||
		(runtime.GOOS != "windows" && parent.Mode().Perm() != 0o700) {
		return nil, fmt.Errorf("race template parent is not private: %v", err)
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 ||
		(runtime.GOOS != "windows" && info.Mode().Perm() != 0o400) {
		return nil, fmt.Errorf("race template is not a private regular file: %v", err)
	}
	for _, suffix := range []string{"-journal", "-wal", "-shm"} {
		if _, err := os.Lstat(path + suffix); !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("race template sidecar %s: %v", suffix, err)
		}
	}
	image, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read race template: %w", err)
	}
	if len(image) < 100 {
		return nil, errors.New("race template is too short")
	}
	gotSHA := sha256.Sum256(image)
	if hex.EncodeToString(gotSHA[:]) != wantSHA {
		return nil, errors.New("race template SHA-256 mismatch")
	}
	return image, nil
}

func validDigest(value string) bool {
	if len(value) != sha256.Size*2 || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

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
	var userCount int
	if err := store.DB().QueryRow(`SELECT COUNT(*) FROM users`).Scan(&userCount); err != nil {
		return fail(fmt.Errorf("inspect Generation 2 template users: %w", err))
	}
	if userCount != 0 {
		return fail(fmt.Errorf("Generation 2 template contains %d user rows", userCount))
	}
	if err := errors.Join(store.Close(), vault.Close()); err != nil {
		return nil, fmt.Errorf("close Generation 2 template: %w", err)
	}
	for _, suffix := range []string{"-journal", "-wal", "-shm"} {
		if _, err := os.Lstat(path + suffix); !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("Generation 2 template sidecar %s: %v", suffix, err)
		}
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
