package v1

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"database/sql"
	_ "embed"
	"fmt"
	"io"
	"os"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/db"
)

//go:embed testdata/schema18.sql.gz
var schema18Compressed []byte

//go:embed testdata/schema19.sql.gz
var schema19Compressed []byte

//go:embed testdata/schema20.sql.gz
var schema20Compressed []byte

func schema20SQLForTest(t *testing.T) string {
	t.Helper()
	reader, err := gzip.NewReader(bytes.NewReader(schema20Compressed))
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(reader)
	closeErr := reader.Close()
	if err != nil || closeErr != nil {
		t.Fatal(err, closeErr)
	}
	if fmt.Sprintf("%x", sha256.Sum256(body)) != "0d8bcda640a9cdd88e192e5fdf64cb58d8fb89fb2a5872fbda9b3dfc9980f419" {
		t.Fatal("schema 20 fixture identity changed")
	}
	return string(body)
}

func schema19SQLForTest(t *testing.T) string {
	t.Helper()
	reader, err := gzip.NewReader(bytes.NewReader(schema19Compressed))
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(reader)
	closeErr := reader.Close()
	if err != nil || closeErr != nil {
		t.Fatal(err, closeErr)
	}
	if fmt.Sprintf("%x", sha256.Sum256(body)) != "940774f9249d558183e0a574655090277534244d845d567300931a8d9b79f58d" {
		t.Fatal("schema 19 fixture identity changed")
	}
	return string(body)
}

func schema18SQLForTest(t *testing.T) string {
	t.Helper()
	reader, err := gzip.NewReader(bytes.NewReader(schema18Compressed))
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(reader)
	closeErr := reader.Close()
	if err != nil || closeErr != nil {
		t.Fatal(err, closeErr)
	}
	if fmt.Sprintf("%x", sha256.Sum256(body)) != "0496a4b1382d77a90197d008a1094ddc7290f0e8d5c9e06c2ef1643aa8f94927" {
		t.Fatal("schema 18 fixture identity changed")
	}
	return string(body)
}

func TestFinalSchemaConversionAcceptsExactStableSources(t *testing.T) {
	for _, version := range []int{17, 18, 19, 20} {
		t.Run(fmt.Sprint(version), func(t *testing.T) {
			ddl, hash := schema17SQLForTest(t), stableSchema17Manifest
			if version == 18 {
				ddl, hash = schema18SQLForTest(t), stableSchema18Manifest
			}
			if version == 19 {
				ddl, hash = schema19SQLForTest(t), stableSchema19Manifest
			}
			if version == 20 {
				ddl, hash = schema20SQLForTest(t), stableSchema20Manifest
			}
			path, vault := sourceFixtureFromDDL(t, ddl, version, hash)
			database, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			defer database.Close()
			mustExec(t, database, "UPDATE site_config SET value='preserved instance terms' WHERE key='legal_terms_override_en'")
			mustExec(t, database, "UPDATE site_config SET value='custom instance' WHERE key='site_name'")
			user := hostileInsertUser(t, database, "preserved", 0, 1)
			hostileInsertAccount(t, database, "user", user, nil, -1, hostileBlob16(77), 1)
			mustExec(t, database, "UPDATE game_ai_settings SET enabled=1,revision=revision+1 WHERE game_key='bidding'")
			beforeFacts := retainedFacts(t, database)
			if err := database.Close(); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if store, err := db.Open(path, vault); err == nil {
				store.Close()
				t.Fatal("normal runtime accepted historical schema")
			}
			if _, err := RunRC7(t.Context(), path, vault, Options{Apply: true}); err == nil {
				t.Fatal("narrow wrapper accepted stable source")
			}
			checked, err := Run(t.Context(), path, vault, Options{})
			if err != nil || checked.Status != "ready" || checked.SourceSchemaVersion != version {
				t.Fatal(checked, err)
			}
			after, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatal("check or rejection wrote source", err)
			}
			result, err := Run(t.Context(), path, vault, Options{Apply: true})
			if err != nil || result.Status != "converted" {
				t.Fatal(result, err)
			}
			store, err := db.Open(path, vault)
			if err != nil {
				t.Fatal(err)
			}
			afterFacts := retainedFacts(t, store.DB())
			for table, digest := range beforeFacts {
				if afterFacts[table] != digest {
					t.Errorf("preserved facts changed in %s", table)
				}
			}
			if len(beforeFacts) != len(afterFacts) {
				t.Fatal("unrelated table set changed")
			}
			var policies int
			if err := store.DB().QueryRow("SELECT count(*) FROM site_config WHERE key GLOB 'mfa_required_level_[1-6]' AND value='0'").Scan(&policies); err != nil || policies != 6 {
				t.Fatal(policies, err)
			}
			if err := validateGenerationTwoManifest(t.Context(), store.DB()); err != nil {
				t.Fatal(err)
			}
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
			result, err = Run(t.Context(), path, vault, Options{Apply: true})
			if err != nil || result.Status != "current" {
				t.Fatal(result, err)
			}
			if err := db.VerifyContext(t.Context(), path, vault, nil); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestOfflineDescriptorContainsOnlyExactSupportedSources(t *testing.T) {
	descriptor := SchemaDescriptor()
	if descriptor.SchemaVersion != 21 || descriptor.MinimumSchemaVersion != 21 || len(descriptor.SourceSchemaVersions) != 0 || len(descriptor.OfflineConversions) != 5 {
		t.Fatal(descriptor)
	}
	for index, source := range descriptor.OfflineConversions {
		if source.SourceSchemaVersion != 16+index || source.TargetSchemaVersion != 21 || len(source.SourceCommit) != 40 || len(source.SourceManifestHash) != 64 || source.Tool != "nonbiri-upgrade-v1" {
			t.Fatal(source)
		}
	}
}

func TestConversionOfStoppedWALSource(t *testing.T) {
	path, vault := sourceFixtureFromDDL(t, schema18SQLForTest(t), 18, stableSchema18Manifest)
	database, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec("PRAGMA journal_mode=WAL"); err != nil {
		database.Close()
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	result, err := Run(t.Context(), path, vault, Options{Apply: true})
	if err != nil || result.Status != "converted" {
		t.Fatal(result, err)
	}
	if err := db.VerifyContext(t.Context(), path, vault, nil); err != nil {
		t.Fatal(err)
	}
}
