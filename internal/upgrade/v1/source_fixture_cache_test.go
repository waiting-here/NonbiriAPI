package v1

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/dbtest"
)

type sourceFixtureIdentity struct {
	DDL      string
	Version  int
	Manifest string
}

type sourceFixtureImageCache struct {
	images sync.Map
}

var historicalSourceImages sourceFixtureImageCache

// Only closed image bytes survive the builder. Each test still converts and
// mutates its own database, with all structure from the frozen source DDL.
func (cache *sourceFixtureImageCache) load(identity sourceFixtureIdentity, build func() []byte) []byte {
	loader, _ := cache.images.LoadOrStore(identity, sync.OnceValue(build))
	return loader.(func() []byte)()
}

func copyHistoricalSourceImage(t *testing.T, image []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "source.sqlite")
	dbtest.EnsureOwnerOnlyParent(t, path)
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	_, writeErr := file.Write(image)
	if err := errors.Join(writeErr, file.Close()); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestHistoricalSourceFixtureCacheSurvivesCleanupAndIsolatesCopies(t *testing.T) {
	var cache sourceFixtureImageCache
	identity := sourceFixtureIdentity{DDL: sourceDDL, Version: 16, Manifest: SourceManifestHash}
	builds := 0
	imageBytes := 0
	var builderPath string
	started := time.Now()
	t.Run("build", func(t *testing.T) {
		cache.load(identity, func() []byte {
			builds++
			path, _ := buildSourceFixtureFromDDL(t, identity.DDL, identity.Version, identity.Manifest)
			builderPath = path
			image, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			imageBytes = len(image)
			return image
		})
	})
	buildTime := time.Since(started)
	if _, err := os.Stat(builderPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("builder temporary file survived cleanup: %v", err)
	}
	var originalName string
	for index := range 3 {
		t.Run(fmt.Sprintf("copy-%d", index), func(t *testing.T) {
			image := cache.load(identity, func() []byte {
				t.Fatal("rebuilt an initialized source image")
				return nil
			})
			path := copyHistoricalSourceImage(t, image)
			database, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			defer database.Close()
			var name string
			if err := database.QueryRow("SELECT value FROM site_config WHERE key='site_name'").Scan(&name); err != nil {
				t.Fatal(err)
			}
			if index == 0 {
				originalName = name
			} else if name != originalName {
				t.Fatal("source copy inherited another copy's mutation")
			}
			if hash, err := db.SchemaManifestHash(t.Context(), database); err != nil || hash != identity.Manifest {
				t.Fatalf("copied source identity=%s: %v", hash, err)
			}
			mustExec(t, database, "UPDATE site_config SET value=? WHERE key='site_name'", fmt.Sprintf("copy-%d", index))
		})
	}
	if builds != 1 {
		t.Fatalf("source image builds=%d, want 1", builds)
	}
	t.Logf("source image builds=%d bytes=%d build=%s; three independent copies after builder cleanup", builds, imageBytes, buildTime)
}
