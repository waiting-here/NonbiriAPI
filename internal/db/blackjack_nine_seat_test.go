package db

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

func makePreNineSeatFixture(t *testing.T, database *sql.DB) {
	t.Helper()
	var ddl string
	if err := database.QueryRow(`SELECT sql FROM sqlite_schema WHERE name='game_blackjack_entries'`).Scan(&ddl); err == sql.ErrNoRows {
		return
	} else if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ddl, blackjackNineSeatConstraint) {
		return
	}
	var count, version int
	if err := database.QueryRow(`SELECT count(*) FROM game_blackjack_entries WHERE seat_no=8`).Scan(&count); err != nil || count != 0 {
		t.Fatal("fixture has ninth-seat data", count, err)
	}
	if err := database.QueryRow(`PRAGMA schema_version`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`PRAGMA writable_schema=ON`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`UPDATE sqlite_schema SET sql=? WHERE name='game_blackjack_entries' AND type='table'`, strings.Replace(ddl, blackjackNineSeatConstraint, blackjackEightSeatConstraint, 1)); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(fmt.Sprintf(`PRAGMA schema_version=%d; PRAGMA writable_schema=RESET`, version+1)); err != nil {
		t.Fatal(err)
	}
}

func TestBlackjackNineSeatUpgradeKeepsAllRows(t *testing.T) {
	path, vault := bootstrapTestPath(t, "eight.sqlite"), bootstrapTestVault(t)
	store, err := Open(path, vault)
	if err != nil {
		t.Fatal(err)
	}
	makePreNineSeatFixture(t, store.DB())
	assertRetainedManifest(t, store.DB(), preBlackjackNineSeatManifestHash)
	hostileMustExec(t, store.DB(), `UPDATE site_config SET value='preserved instance policy' WHERE key='site_name'`)
	before := retainedTableImages(t, store.DB(), nil)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		store, err = Open(path, vault)
		if err != nil {
			t.Fatal(err)
		}
		assertRetainedManifest(t, store.DB(), PinnedGenerationTwoManifestHash)
		assertRetainedImages(t, store.DB(), before)
		if err := store.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestBlackjackNineSeatUnknownSourceDoesNotWrite(t *testing.T) {
	path, vault := bootstrapTestPath(t, "modified.sqlite"), bootstrapTestVault(t)
	store, err := Open(path, vault)
	if err != nil {
		t.Fatal(err)
	}
	makePreNineSeatFixture(t, store.DB())
	hostileMustExec(t, store.DB(), `CREATE INDEX unexpected_blackjack_index ON game_blackjack_entries(created_at)`)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	before := snapshotBootstrapSources(t, path)
	store, err = Open(path, vault)
	if store != nil {
		store.Close()
		t.Fatal("modified prior schema accepted")
	}
	if err == nil || !reflect.DeepEqual(before, snapshotBootstrapSources(t, path)) {
		t.Fatal("source changed after rejection", err)
	}
}

func TestNineSeatUpgradeFromPreviousBinary(t *testing.T) {
	directory := os.Getenv("NONBIRI_NINE_SEAT_FIXTURES")
	if directory == "" {
		t.Skip("previous binary supplies populated fixtures")
	}
	vault, err := secret.New(bytes.Repeat([]byte{0x41}, secret.MasterKeyBytes))
	if err != nil {
		t.Fatal(err)
	}
	defer vault.Close()
	for _, phase := range []string{"seating", "decision", "result"} {
		t.Run(phase, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join(directory, phase+".db"))
			if err != nil {
				t.Fatal(err)
			}
			path := bootstrapTestPath(t, "upgrade.sqlite")
			if err := os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}
			prior, err := openSQLite(path, "ro")
			if err != nil {
				t.Fatal(err)
			}
			assertRetainedManifest(t, prior, preBlackjackNineSeatManifestHash)
			before := retainedTableImages(t, prior, nil)
			prior.Close()
			for range 2 {
				store, err := Open(path, vault)
				if err != nil {
					t.Fatal(err)
				}
				assertRetainedManifest(t, store.DB(), PinnedGenerationTwoManifestHash)
				assertRetainedImages(t, store.DB(), before)
				tx, err := store.DB().BeginTx(context.Background(), nil)
				if err != nil {
					t.Fatal(err)
				}
				if err := ValidateAssetLedger(context.Background(), tx); err != nil {
					t.Fatal(err)
				}
				if err := validateAssetCapacity(context.Background(), tx); err != nil {
					t.Fatal(err)
				}
				tx.Rollback()
				store.Close()
			}
			upgraded, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(directory, phase+"-upgraded.db"), upgraded, 0600); err != nil {
				t.Fatal(err)
			}
		})
	}
}
