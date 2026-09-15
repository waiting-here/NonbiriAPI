package db

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestDuelFreshAndUpgradeSchemaIdentity(t *testing.T) {
	ctx := context.Background()
	fresh := openGenerationTwoDDLForTest(t)
	defer fresh.Close()
	tx, err := fresh.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := seedGenerationTwo(ctx, tx, hostileOID("b1e_")); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	want, err := readGenerationManifest(ctx, fresh)
	if err != nil {
		t.Fatal(err)
	}
	seed, err := readGenerationTwoFreshConfigSeedRows(ctx, fresh)
	if err != nil {
		t.Fatal(err)
	}
	seedHash, err := generationTwoFreshConfigSeedDigest(seed)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("schema=%s manifest=%s seed=%s", GenerationTwoSchemaHash(), generationManifestDigest(want), seedHash)
	prior, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer prior.Close()
	prior.SetMaxOpenConns(1)
	if _, err := prior.Exec(generationTwoWithoutDuelsSchema); err != nil {
		t.Fatal(err)
	}
	before, err := readGenerationManifest(ctx, prior)
	if err != nil || generationManifestDigest(before) != preRCOneManifestHash {
		t.Fatal("prior manifest", err)
	}
	tx, err = prior.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := seedGenerationTwo(ctx, tx, hostileOID("b1e_")); err != nil {
		t.Fatal(err)
	}
	for key := range duelConfigDefaults() {
		if _, err := tx.Exec(`DELETE FROM site_config WHERE key=?`, key); err != nil {
			t.Fatal(err)
		}
	}
	if err := ApplyDuelExtension(ctx, tx); err != nil {
		t.Fatal(err)
	}
	got, err := readGenerationManifest(ctx, tx)
	if err != nil || generationManifestDigest(got) != generationManifestDigest(want) {
		t.Fatal("fresh and upgraded structures differ", generationManifestDigest(got), err)
	}
	if err := foreignKeyCheck(ctx, tx); err != nil {
		t.Fatal(err)
	}
	if err := extensionIntegrityCheck(ctx, tx); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func TestDuelBootstrapRollbackAndPartialSourceRejection(t *testing.T) {
	for _, kind := range []string{"full", "partial"} {
		t.Run(kind, func(t *testing.T) {
			path, vault := bootstrapTestPath(t, "source.sqlite"), bootstrapTestVault(t)
			store, err := Open(path, vault)
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			makePreDuelFixture(t, store.DB())
			hostileMustExec(t, store.DB(), `UPDATE site_config SET value='Preserved synthetic site' WHERE key='site_name'`)
			assertRetainedManifest(t, store.DB(), preRCOneManifestHash)
			if kind == "partial" {
				hostileMustExec(t, store.DB(), `CREATE TABLE game_duel_catalogs(incomplete TEXT)`)
				if err := store.Close(); err != nil {
					t.Fatal(err)
				}
				before := snapshotBootstrapSources(t, path)
				other, err := Open(path, vault)
				if other != nil {
					other.Close()
					t.Fatal("partial source accepted")
				}
				if err == nil {
					t.Fatal("missing rejection")
				}
				if !reflect.DeepEqual(before, snapshotBootstrapSources(t, path)) {
					t.Fatal("rejected source changed")
				}
				return
			}
			hostileMustExec(t, store.DB(), `VACUUM`)
			before := retainedTableImages(t, store.DB(), nil)
			var pages int
			if err := store.DB().QueryRow(`PRAGMA page_count`).Scan(&pages); err != nil {
				t.Fatal(err)
			}
			hostileMustExec(t, store.DB(), fmt.Sprintf(`PRAGMA max_page_count=%d`, pages+1))
			err = extendKnownGenerationTwoSchema(context.Background(), store.DB())
			if err == nil || !strings.Contains(err.Error(), "full") {
				t.Fatalf("expected storage-full failure: %v", err)
			}
			assertRetainedManifest(t, store.DB(), preRCOneManifestHash)
			assertRetainedImages(t, store.DB(), before)
			hostileMustExec(t, store.DB(), `PRAGMA max_page_count=2147483647`)
			if err := extendKnownGenerationTwoSchema(context.Background(), store.DB()); err != nil {
				t.Fatal(err)
			}
			assertRetainedManifest(t, store.DB(), PinnedGenerationTwoManifestHash)
			assertRetainedImages(t, store.DB(), before)
		})
	}
}
