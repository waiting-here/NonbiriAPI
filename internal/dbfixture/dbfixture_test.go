package dbfixture

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
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

func sharedTemplateForTest(t *testing.T) (string, string, string) {
	t.Helper()
	image, err := generationTwoTemplate.load()
	if err != nil {
		t.Fatalf("build template: %v", err)
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatalf("secure template parent: %v", err)
	}
	identity := strings.Repeat("a", sha256.Size*2)
	path := filepath.Join(dir, identity+".sqlite")
	if err := os.WriteFile(path, image, 0o600); err != nil {
		t.Fatalf("write template: %v", err)
	}
	if err := os.Chmod(path, 0o400); err != nil {
		t.Fatalf("make template read-only: %v", err)
	}
	digest := sha256.Sum256(image)
	return path, hex.EncodeToString(digest[:]), identity
}

func TestRaceTemplateLoaderRejectsCorruptionAndDifferentRuns(t *testing.T) {
	path, digest, identity := sharedTemplateForTest(t)
	image, err := loadRaceTemplate(path, digest, identity)
	if err != nil || len(image) < 100 {
		t.Fatalf("load intact template: bytes=%d err=%v", len(image), err)
	}
	if _, err := loadRaceTemplate(path, strings.Repeat("b", sha256.Size*2), identity); err == nil {
		t.Fatal("accepted wrong SHA-256")
	}
	if _, err := loadRaceTemplate(path, digest, strings.Repeat("b", sha256.Size*2)); err == nil {
		t.Fatal("accepted another run identity")
	}
	if err := os.WriteFile(path+"-wal", []byte("unexpected sidecar"), 0o600); err != nil {
		t.Fatalf("write sidecar: %v", err)
	}
	if _, err := loadRaceTemplate(path, digest, identity); err == nil {
		t.Fatal("accepted template with sidecar")
	}
	if err := os.Remove(path + "-wal"); err != nil {
		t.Fatalf("remove sidecar: %v", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatalf("unprotect template for corruption test: %v", err)
	}
	image[0] ^= 0xff
	if err := os.WriteFile(path, image, 0o600); err != nil {
		t.Fatalf("corrupt template: %v", err)
	}
	if err := os.Chmod(path, 0o400); err != nil {
		t.Fatalf("reprotect corrupt template: %v", err)
	}
	if _, err := loadRaceTemplate(path, digest, identity); err == nil {
		t.Fatal("accepted corrupt template")
	}
	if err := os.Remove(path); err != nil {
		t.Fatalf("remove corrupt template: %v", err)
	}
	if _, err := loadRaceTemplate(path, digest, identity); err == nil {
		t.Fatal("silently rebuilt missing template")
	}
}

func TestRaceTemplateLoaderRequiresPrivateBoundary(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX mode checks are verified on Linux")
	}
	path, digest, identity := sharedTemplateForTest(t)
	if err := os.Chmod(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("make parent public: %v", err)
	}
	if _, err := loadRaceTemplate(path, digest, identity); err == nil {
		t.Fatal("accepted public template parent")
	}
}

func TestRaceTemplateEnvironmentFailureDoesNotFallback(t *testing.T) {
	path, digest, identity := sharedTemplateForTest(t)
	t.Setenv(RaceTemplatePathEnv, path)
	t.Setenv(RaceTemplateSHAEnv, digest)
	t.Setenv(RaceTemplateIDEnv, identity)
	if _, err := loadOrBuildGenerationTwoTemplate(); err != nil {
		t.Fatalf("load valid environment template: %v", err)
	}
	t.Setenv(RaceTemplateSHAEnv, strings.Repeat("b", sha256.Size*2))
	if _, err := loadOrBuildGenerationTwoTemplate(); err == nil {
		t.Fatal("bad environment checksum silently fell back to bootstrap")
	}
	t.Setenv(RaceTemplateSHAEnv, "")
	if _, err := loadOrBuildGenerationTwoTemplate(); err == nil {
		t.Fatal("partial environment silently fell back to bootstrap")
	}
}

func TestRaceTemplateChildProcess(t *testing.T) {
	if os.Getenv("NONBIRI_RACE_TEMPLATE_TEST_CHILD") != "1" {
		return
	}
	path := os.Getenv("NONBIRI_RACE_TEMPLATE_TEST_COPY")
	if path == "" {
		t.Fatal("missing child copy path")
	}
	if err := materialize(path); err != nil {
		t.Fatalf("child materialize: %v", err)
	}
	store, err := db.Open(path, fixtureVault(t, 0x33))
	if err != nil {
		t.Fatalf("child current-store validation: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("child close: %v", err)
	}
}

func TestRaceTemplateCopiesAcrossProcessesRemainIndependent(t *testing.T) {
	path, digest, identity := sharedTemplateForTest(t)
	copyDir := t.TempDir()
	if err := os.Chmod(copyDir, 0o700); err != nil {
		t.Fatalf("secure copy parent: %v", err)
	}
	paths := []string{filepath.Join(copyDir, "one.sqlite"), filepath.Join(copyDir, "two.sqlite")}
	commands := make([]*exec.Cmd, len(paths))
	outputs := make([]*bytes.Buffer, len(paths))
	for index, copyPath := range paths {
		command := exec.Command(os.Args[0], "-test.run=^TestRaceTemplateChildProcess$")
		outputs[index] = &bytes.Buffer{}
		command.Stdout = outputs[index]
		command.Stderr = outputs[index]
		command.Env = append(os.Environ(),
			RaceTemplatePathEnv+"="+path,
			RaceTemplateSHAEnv+"="+digest,
			RaceTemplateIDEnv+"="+identity,
			"NONBIRI_RACE_TEMPLATE_TEST_CHILD=1",
			"NONBIRI_RACE_TEMPLATE_TEST_COPY="+copyPath)
		if err := command.Start(); err != nil {
			for _, started := range commands[:index] {
				_ = started.Process.Kill()
				_ = started.Wait()
			}
			t.Fatalf("start child %d: %v", index, err)
		}
		commands[index] = command
	}
	var waitErr error
	for index, command := range commands {
		if err := command.Wait(); err != nil {
			if waitErr == nil {
				waitErr = fmt.Errorf("child %d: %w: %s", index, err, outputs[index].String())
				for _, remaining := range commands[index+1:] {
					_ = remaining.Process.Kill()
				}
			}
		}
	}
	if waitErr != nil {
		t.Fatal(waitErr)
	}
	first, err := os.Stat(paths[0])
	if err != nil {
		t.Fatalf("stat first copy: %v", err)
	}
	second, err := os.Stat(paths[1])
	if err != nil {
		t.Fatalf("stat second copy: %v", err)
	}
	if os.SameFile(first, second) {
		t.Fatal("cross-process copies share file identity")
	}
}
