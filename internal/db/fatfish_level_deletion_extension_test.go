package db

import (
	"context"
	"crypto/sha256"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestLevelDeletionUpgradePreservesPublishedData(t *testing.T) {
	priorDDL := strings.TrimSuffix(generationTwoSchema, fatFishLevelDeletionSchema)
	if got := fmt.Sprintf("%x", sha256.Sum256([]byte(priorDDL))); got != "eddc288821cd2b64f648a5ba4006626f38dc95d070471fe39fc9a5cf1f56a2f0" {
		t.Fatalf("deployed source DDL drift: %s", got)
	}
	store := openTestStore(t, bootstrapTestPath(t, "level-library-upgrade.sqlite"))
	database := store.DB()
	hostileMustExec(t, database, `DROP TABLE fatfish_deleted_levels`)
	assertRetainedManifest(t, database, preLevelDeletionManifestHash)
	level, version := hostileOID("ffl_"), hostileOID("ffv_")
	hostileMustExec(t, database, `INSERT INTO fatfish_levels VALUES(?,'Retained level','Retained description','{}',7,NULL,1,2)`, level)
	hostileMustExec(t, database, `INSERT INTO fatfish_level_versions VALUES(?,?,zeroblob(32),1,1,'{}',30,3,2)`, version, level)
	hostileMustExec(t, database, `UPDATE site_config SET value='Retained instance text' WHERE key='site_name'`)
	ctx := context.Background()
	manifest, err := readGenerationManifest(ctx, database)
	if err != nil {
		t.Fatal(err)
	}
	before := interactionTableDigests(t, database, manifest)
	for attempt := 0; attempt < 2; attempt++ {
		if err := extendKnownGenerationTwoSchema(ctx, database); err != nil {
			t.Fatal(err)
		}
		assertRetainedManifest(t, database, PinnedGenerationTwoManifestHash)
		if after := interactionTableDigests(t, database, manifest); !reflect.DeepEqual(before, after) {
			t.Fatal("library extension changed existing rows")
		}
	}
	hostileMustExec(t, database, `INSERT INTO fatfish_deleted_levels VALUES(?,3)`, level)
	if _, err := database.Exec(`UPDATE fatfish_level_versions SET content_json='{"changed":true}' WHERE id=?`, version); err == nil {
		t.Fatal("deleted library source made published content mutable")
	}
	if _, err := database.Exec(`INSERT INTO fatfish_deleted_levels VALUES(?,3)`, hostileOID("ffl_")); err == nil {
		t.Fatal("orphan deletion marker accepted")
	}
	if _, err := database.Exec(`UPDATE fatfish_deleted_levels SET deleted_at=-1 WHERE level_id=?`, level); err == nil {
		t.Fatal("invalid deletion time accepted")
	}
	if err := foreignKeyCheck(ctx, database); err != nil {
		t.Fatal(err)
	}
}

func TestLevelDeletionUpgradeRejectsUnknownSource(t *testing.T) {
	store := openTestStore(t, bootstrapTestPath(t, "level-library-drift.sqlite"))
	database := store.DB()
	hostileMustExec(t, database, `DROP TABLE fatfish_deleted_levels; CREATE INDEX unexpected_level_index ON fatfish_levels(title)`)
	if err := extendKnownGenerationTwoSchema(context.Background(), database); err == nil {
		t.Fatal("unrecognized source accepted")
	}
	var count int
	if err := database.QueryRow(`SELECT count(*) FROM sqlite_schema WHERE name='fatfish_deleted_levels'`).Scan(&count); err != nil || count != 0 {
		t.Fatal("rejected source was changed", count, err)
	}
}
