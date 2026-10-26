package db

import (
	"database/sql"
	"errors"
	"fmt"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
	"strings"
	"testing"
	"time"
)

func assertExactStartupRejection(t *testing.T, path string, vault *secret.Vault, actual *int) {
	t.Helper()
	before := snapshotBootstrapSources(t, path)
	// Shared memory coordinates WAL readers; durable main/WAL bytes must not change.
	before["-shm"] = bootstrapTestFileImage{}
	var stages []StartupStage
	trace := NewStartupTrace(time.Now(), func(progress StartupProgress) { stages = append(stages, progress.Stage) })
	opened, err := OpenContext(trace.Context(t.Context()), path, vault)
	if opened != nil {
		opened.Close()
		t.Fatal("mismatched source opened")
	}
	var rejected *StartupError
	if !errors.As(err, &rejected) || rejected.Kind != StartupSchemaMismatch {
		t.Fatal("wrong rejection", err)
	}
	guidance := "nonbiri-upgrade-v1 --check to inspect or --apply to upgrade explicitly"
	if actual != nil && *actual == CurrentSchemaVersion {
		guidance = "database structure does not match this server; a version upgrade cannot repair this mismatch"
	}
	if !strings.Contains(err.Error(), fmt.Sprintf("required schema %d", CurrentSchemaVersion)) || !strings.Contains(err.Error(), guidance) {
		t.Fatal("missing actionable schema guidance", err)
	}
	if actual != nil && (rejected.ActualSchemaVersion == nil || *rejected.ActualSchemaVersion != *actual || !strings.Contains(err.Error(), fmt.Sprintf("actual %d", *actual))) {
		t.Fatal("wrong actual schema", err)
	}
	for _, stage := range stages {
		if stage == StageSourceOpen || stage == StageSchemaUpgrade || stage == StageDomainRecovery {
			t.Fatal("rejection reached mutation/recovery", stage)
		}
	}
	assertBootstrapSourcesUnchanged(t, path, before)
}
func TestExactStartupRejectsHistoricalAndNewerSchemasBeforeWriting(t *testing.T) {
	for _, version := range []int{33, 34, 35, 36, 37, CurrentSchemaVersion + 1} {
		t.Run(fmt.Sprint(version), func(t *testing.T) {
			var path string
			var vault *secret.Vault
			var source *Store
			if version == 33 {
				path, vault, source = schema33Fixture(t)
			} else if version == 34 {
				path, vault, source = schema34CatalogFixture(t)
			} else {
				path = bootstrapTestPath(t, "source.sqlite")
				vault = bootstrapTestVault(t)
				var err error
				source, err = Open(path, vault)
				if err != nil {
					t.Fatal(err)
				}
				if version == 35 {
					restoreSchema35ForTest(t, source)
				}
				if version == 37 {
					dropCharityModelIndexesForTest(t, source.DB())
				}
				if version == 36 {
					restoreSchema36LayoutForTest(t, source.DB())
				}
				hostileMustExec(t, source.DB(), "UPDATE schema_state SET version=?", version)
			}
			seedEndpointKeyHistory(t, source.DB(), vault, 'A')
			if err := source.Close(); err != nil {
				t.Fatal(err)
			}
			assertExactStartupRejection(t, path, vault, &version)
		})
	}
}
func TestExactStartupRejectsWALVersionWithoutCheckpointOrRecovery(t *testing.T) {
	path, vault := bootstrapTestPath(t, "wal-version.sqlite"), bootstrapTestVault(t)
	current, err := Open(path, vault)
	if err != nil {
		t.Fatal(err)
	}
	if err = current.Close(); err != nil {
		t.Fatal(err)
	}
	writer, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	hostileMustExec(t, writer, "PRAGMA wal_autocheckpoint=0")
	hostileMustExec(t, writer, "UPDATE schema_state SET version=36")
	version := 36
	assertExactStartupRejection(t, path, vault, &version)
	var remaining int
	if err = writer.QueryRow("SELECT version FROM schema_state").Scan(&remaining); err != nil || remaining != 36 {
		t.Fatal(remaining, err)
	}
}
func TestExactStartupRejectsUnknownAndPartialCurrentSchemas(t *testing.T) {
	for _, change := range []string{"DROP TABLE schema_state", "DELETE FROM schema_state", "DROP INDEX idx_users_created"} {
		t.Run(change, func(t *testing.T) {
			path, vault := bootstrapTestPath(t, "partial.sqlite"), bootstrapTestVault(t)
			source, err := Open(path, vault)
			if err != nil {
				t.Fatal(err)
			}
			hostileMustExec(t, source.DB(), change)
			if err = source.Close(); err != nil {
				t.Fatal(err)
			}
			var actual *int
			if change == "DROP INDEX idx_users_created" {
				version := CurrentSchemaVersion
				actual = &version
			}
			assertExactStartupRejection(t, path, vault, actual)
		})
	}
}
