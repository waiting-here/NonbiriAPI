package db

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
)

// The independent old manifest digest verifies this reconstruction, including
// all unrelated tables, constraints, indexes, foreign keys and triggers.
func makePreEmbeddingFixture(t *testing.T, database *sql.DB) {
	t.Helper()
	makePreAssetFixture(t, database)
	for _, table := range []string{"logical_requests", "request_logs"} {
		var ddl string
		var version int
		if err := database.QueryRow(`SELECT sql FROM sqlite_schema WHERE type='table' AND name=?`, table).Scan(&ddl); err != nil {
			t.Fatal(err)
		}
		const added = ",'openai_embeddings','charity_embeddings'"
		if !strings.Contains(ddl, added) {
			continue
		}
		if strings.Count(ddl, added) != 1 {
			t.Fatal("unexpected embedding CHECK fixture")
		}
		if err := database.QueryRow(`PRAGMA schema_version`).Scan(&version); err != nil {
			t.Fatal(err)
		}
		hostileMustExec(t, database, `BEGIN; PRAGMA writable_schema=ON`)
		hostileMustExec(t, database, `UPDATE sqlite_schema SET sql=? WHERE type='table' AND name=?`, strings.Replace(ddl, added, "", 1), table)
		hostileMustExec(t, database, fmt.Sprintf(`PRAGMA schema_version=%d; PRAGMA writable_schema=RESET; COMMIT`, version+1))
	}
}

func TestEmbeddingExtensionPreservesReleasedDataAndStorage(t *testing.T) {
	path, vault := bootstrapTestPath(t, "embedding-upgrade.sqlite"), bootstrapTestVault(t)
	store, err := Open(path, vault)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	seedRetainedBusinessData(t, store, vault)
	makeRetainedSource(t, store.DB(), preEmbeddingManifestHash)
	before := retainedTableImages(t, store.DB(), nil)
	manifestBefore, err := readGenerationManifest(context.Background(), store.DB())
	if err != nil {
		t.Fatal(err)
	}
	rootPages := func(database *sql.DB) [2]int {
		t.Helper()
		var roots [2]int
		for i, table := range []string{"logical_requests", "request_logs"} {
			if err := database.QueryRow(`SELECT rootpage FROM sqlite_schema WHERE type='table' AND name=?`, table).Scan(&roots[i]); err != nil {
				t.Fatal(err)
			}
		}
		return roots
	}
	roots := rootPages(store.DB())
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	for round := 0; round < 2; round++ {
		store, err = Open(path, vault)
		if err != nil {
			t.Fatal(err)
		}
		defer store.Close()
		assertRetainedManifest(t, store.DB(), PinnedGenerationTwoManifestHash)
		assertRetainedImages(t, store.DB(), before)
		assertHourlySchemaPragmas(t, store.DB())
		if current := rootPages(store.DB()); current != roots {
			t.Fatalf("request tables rebuilt: %v -> %v", roots, current)
		}
		after, err := readGenerationManifest(context.Background(), store.DB())
		if err != nil {
			t.Fatal(err)
		}
		for _, table := range []string{"logical_requests", "request_logs"} {
			var previous, current string
			for _, object := range manifestBefore.Objects {
				if object.Type == "table" && object.Name == table {
					previous = object.SQL
				}
			}
			for _, object := range after.Objects {
				if object.Type == "table" && object.Name == table {
					current = object.SQL
				}
			}
			if previous == "" || strings.Replace(current, ",'openai_embeddings','charity_embeddings'", "", 1) != previous {
				t.Fatalf("request table %s changed beyond its route CHECK", table)
			}
		}
		if err := store.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestEmbeddingExtensionRollsBackAndResetsConnection(t *testing.T) {
	database, _ := quotaConstraintFixture(t)
	makeRetainedSource(t, database, preEmbeddingManifestHash)
	hostileMustExec(t, database, `PRAGMA foreign_keys=OFF; INSERT INTO endpoint_key_limits(endpoint_key_id,max_rpm) VALUES(999999,1); PRAGMA foreign_keys=ON`)
	before := retainedTableImages(t, database, nil)
	if err := extendKnownGenerationTwoSchema(context.Background(), database); err == nil {
		t.Fatal("extension accepted dangling business data")
	}
	assertRetainedManifest(t, database, preEmbeddingManifestHash)
	assertRetainedImages(t, database, before)
	assertHourlySchemaPragmas(t, database)
	hostileMustExec(t, database, `DELETE FROM endpoint_key_limits WHERE endpoint_key_id=999999`)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := extendKnownGenerationTwoSchema(ctx, database); err == nil {
		t.Fatal("cancelled extension succeeded")
	}
	assertRetainedManifest(t, database, preEmbeddingManifestHash)
	assertHourlySchemaPragmas(t, database)
	if err := extendKnownGenerationTwoSchema(context.Background(), database); err != nil {
		t.Fatal(err)
	}
	assertRetainedManifest(t, database, PinnedGenerationTwoManifestHash)
	assertHourlySchemaPragmas(t, database)
}

func TestEmbeddingRouteChecksAcceptOnlyKnownOperations(t *testing.T) {
	database := openGenerationTwoDDLForTest(t)
	defer database.Close()
	user := hostileInsertUser(t, database, "embedding-route-owner", 0, 0)
	for i, route := range []string{"openai_embeddings", "charity_embeddings"} {
		id := hostileOIDVariant("req_", byte('X'+i), 'Q')
		hostileInsertLogicalRequest(t, database, id, user, route, 1)
		hostileInsertRequestLog(t, database, id, user, route)
	}
	hostileMustFail(t, database, `INSERT INTO logical_requests(id,user_id,route_kind,state,attempt_limit,accounting_state,settlement_destination,ledger_rows_remaining,created_at) VALUES(?,?,'unknown_embeddings','accepted',1,'none','user',?,0)`, hostileOIDVariant("req_", 'Z', 'Q'), user, hostileBlob16(1))
	hostileMustFail(t, database, `UPDATE request_logs SET route_kind='unknown_embeddings'`)
}
