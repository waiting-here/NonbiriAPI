package db

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func makePreActivityRefinementFixture(t *testing.T, database *sql.DB) {
	t.Helper()
	conn, err := database.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	defer conn.ExecContext(context.Background(), `PRAGMA writable_schema=RESET`)
	tx, err := conn.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var version int
	if err := tx.QueryRow(`PRAGMA schema_version`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`PRAGMA writable_schema=ON`); err != nil {
		t.Fatal(err)
	}
	for _, change := range activityRefinementChanges {
		var current string
		if err := tx.QueryRow(`SELECT sql FROM sqlite_schema WHERE name=?`, change.table).Scan(&current); err != nil {
			t.Fatal(err)
		}
		if strings.Count(current, change.after) != 1 {
			t.Fatal("unexpected fixture constraint", change.table)
		}
		if _, err := tx.Exec(`UPDATE sqlite_schema SET sql=? WHERE name=?`, strings.Replace(current, change.after, change.before, 1), change.table); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := tx.Exec(fmt.Sprintf(`PRAGMA schema_version=%d; PRAGMA writable_schema=RESET`, version+1)); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	conn.Close()
	assertRetainedManifest(t, database, preActivityRefinementManifestHash)
}

func activityRefinementSourceFixture(t *testing.T) *sql.DB {
	t.Helper()
	ddl := interactionBootstrapSchema(generationTwoWithoutInteractionsSchema)
	if got := fmt.Sprintf("%x", sha256.Sum256([]byte(ddl))); got != "d7c030a1f57f39074922b3f9595b8c3bb589359e7519bc8d4cf4d57a11e25fe2" {
		t.Fatal("deployed source DDL drift", got)
	}
	database, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	database.SetMaxOpenConns(1)
	hostileMustExec(t, database, `PRAGMA foreign_keys=ON;`+ddl)
	assertRetainedManifest(t, database, preActivityRefinementManifestHash)
	tx, err := database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := seedGenerationTwo(context.Background(), tx, hostileOID("b1e_")); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return database
}

func seedBindingOrderFixture(t *testing.T, database *sql.DB, count int) (int64, int64, []int64) {
	t.Helper()
	user := hostileInsertUser(t, database, "binding-order", 0, 0)
	endpoint := hostileInsertEndpoint(t, database, user, "https://upstream.example/v1")
	secret := hostileInsertSecret(t, database, "https://upstream.example/v1", 0)
	key := hostileInsertEndpointKey(t, database, endpoint, secret)
	donation := hostileInsertDonation(t, database, user)
	donationKey := hostileInsertDonationKey(t, database, donation, key)
	personal := hostileMustLastID(t, hostileMustExec(t, database, `INSERT INTO models(user_id,provider,model,full_name,revision,binding_revision,created_at,updated_at) VALUES(?,'provider','model','provider/model',1,7,2,3)`, user))
	charity := hostileMustLastID(t, hostileMustExec(t, database, `INSERT INTO charity_models(provider,model,full_name,pricing_mode,revision,binding_revision,created_at,updated_at) VALUES('provider','model','[公益]provider/model','per_request',1,11,2,3)`))
	ids := make([]int64, count)
	for i := range count {
		id, upstream := int64(i+1), fmt.Sprintf("upstream-%03d", i)
		hostileMustExec(t, database, `INSERT INTO model_pair_catalog(endpoint_key_id,normalized_model_id,automatic_supports,manual_supports,automatic_revision,pair_revision,updated_at) VALUES(?,?,0,1,0,1,0)`, key, upstream)
		hostileMustExec(t, database, `INSERT INTO model_bindings(id,model_id,endpoint_key_id,upstream_model_id,ord,created_at,updated_at) VALUES(?,?,?,?,?,4,5)`, id, personal, key, upstream, i)
		hostileMustExec(t, database, `INSERT INTO charity_model_bindings(id,charity_model_id,donation_key_id,endpoint_key_id,upstream_model_id,ord,created_at,updated_at) VALUES(?,?,?,?,?,?,4,5)`, id, charity, donationKey, key, upstream, i)
		hostileMustExec(t, database, `INSERT INTO request_adaptations(id,scope,binding_id,revision,secret_context,secret_ciphertext,structure_json,updated_at) VALUES(?,'binding',?,7,zeroblob(16),'cipher-sentinel','{}',6)`, id, id)
		ids[i] = id
	}
	return personal, charity, ids
}

func bindingOrderIDs(t *testing.T, database *sql.DB, charity bool, model int64) []int64 {
	t.Helper()
	table, _, column := bindingOrderNames(charity)
	rows, err := database.Query(`SELECT id,ord FROM `+table+` WHERE `+column+`=? ORDER BY ord`, model)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	ids := []int64{}
	for rows.Next() {
		var id int64
		var ord int
		if err := rows.Scan(&id, &ord); err != nil {
			t.Fatal(err)
		}
		if ord != len(ids) {
			t.Fatalf("non-contiguous committed order: %d after %v", ord, ids)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return ids
}

func TestActivityRefinementUpgradePreservesDataAndRepairsOrder(t *testing.T) {
	database := activityRefinementSourceFixture(t)
	personal, charity, ids := seedBindingOrderFixture(t, database, 4)
	hostileMustExec(t, database, `DELETE FROM model_bindings WHERE id IN (?,?)`, ids[0], ids[2])
	hostileMustExec(t, database, `DELETE FROM charity_model_bindings WHERE id IN (?,?)`, ids[0], ids[2])
	level, version := hostileOID("ffl_"), hostileOID("ffv_")
	hostileMustExec(t, database, `INSERT INTO fatfish_levels VALUES(?,'Retained','Original draft','{}',7,NULL,1,2)`, level)
	hostileMustExec(t, database, `INSERT INTO fatfish_level_versions VALUES(?,?,zeroblob(32),1,1,'{}',30,3,2)`, version, level)
	hostileMustExec(t, database, `UPDATE site_config SET value='Retained instance text' WHERE key='site_name'`)
	manifest, err := readGenerationManifest(context.Background(), database)
	if err != nil {
		t.Fatal(err)
	}
	before := interactionTableDigests(t, database, manifest)
	for _, table := range []string{"model_bindings", "charity_model_bindings", "models", "charity_models"} {
		delete(before, table)
	}
	for attempt := 0; attempt < 2; attempt++ {
		if err := extendKnownGenerationTwoSchema(context.Background(), database); err != nil {
			t.Fatal(err)
		}
		assertRetainedManifest(t, database, PinnedGenerationTwoManifestHash)
		after := interactionTableDigests(t, database, manifest)
		for _, table := range []string{"model_bindings", "charity_model_bindings", "models", "charity_models"} {
			delete(after, table)
		}
		if !reflect.DeepEqual(before, after) {
			t.Fatal("migration changed unrelated data, credentials, adaptation or old game content")
		}
		for _, scope := range []struct {
			charity      bool
			id, revision int64
		}{{false, personal, 8}, {true, charity, 12}} {
			if got := bindingOrderIDs(t, database, scope.charity, scope.id); !reflect.DeepEqual(got, []int64{ids[1], ids[3]}) {
				t.Fatal("survivor identity/order changed", got)
			}
			table, parent, column := bindingOrderNames(scope.charity)
			var rev, updated, changed int64
			if err := database.QueryRow(`SELECT binding_revision,updated_at FROM `+parent+` WHERE id=?`, scope.id).Scan(&rev, &updated); err != nil || rev != scope.revision || updated != 3 {
				t.Fatal("revision/time changed unexpectedly", rev, updated, err)
			}
			if err := database.QueryRow(`SELECT count(*) FROM `+table+` WHERE `+column+`=? AND (created_at<>4 OR updated_at<>5)`, scope.id).Scan(&changed); err != nil || changed != 0 {
				t.Fatal("repair changed binding metadata", changed, err)
			}
		}
	}
	newVersion := hostileOIDVariant("ffv_", 'B', 'Q')
	hostileMustExec(t, database, `INSERT INTO fatfish_level_versions VALUES(?,?,zeroblob(32),2,1,'{}',30,3,2,2)`, newVersion, level)
	hostileMustFail(t, database, `INSERT INTO fatfish_level_versions VALUES(?,?,zeroblob(32),4,1,'{}',30,3,2,3)`, hostileOIDVariant("ffv_", 'C', 'Q'), level)
	hostileMustFail(t, database, `UPDATE fatfish_level_versions SET engine_version=2 WHERE id=?`, version)
	if err := foreignKeyCheck(context.Background(), database); err != nil {
		t.Fatal(err)
	}
}

func TestActivityRefinementUpgradeRollsBackAndRejectsDrift(t *testing.T) {
	for _, reason := range []string{"revision-exhausted", "schema-drift"} {
		t.Run(reason, func(t *testing.T) {
			database := activityRefinementSourceFixture(t)
			personal, _, ids := seedBindingOrderFixture(t, database, 2)
			hostileMustExec(t, database, `DELETE FROM model_bindings WHERE id=?`, ids[0])
			if reason == "revision-exhausted" {
				hostileMustExec(t, database, `UPDATE models SET binding_revision=9223372036854775807 WHERE id=?`, personal)
			} else {
				hostileMustExec(t, database, `CREATE INDEX unexpected_refinement_index ON models(provider)`)
			}
			manifest, err := readGenerationManifest(context.Background(), database)
			if err != nil {
				t.Fatal(err)
			}
			before := interactionTableDigests(t, database, manifest)
			if err := extendKnownGenerationTwoSchema(context.Background(), database); err == nil {
				t.Fatal("invalid source accepted")
			}
			afterManifest, err := readGenerationManifest(context.Background(), database)
			if err != nil {
				t.Fatal(err)
			}
			if generationManifestDigest(manifest) != generationManifestDigest(afterManifest) || !reflect.DeepEqual(before, interactionTableDigests(t, database, manifest)) {
				t.Fatal("failed upgrade was not atomic")
			}
			var writable int
			if err := database.QueryRow(`PRAGMA writable_schema`).Scan(&writable); err != nil || writable != 0 {
				t.Fatal("schema editing left enabled", writable, err)
			}
		})
	}
}

func TestBindingReorderAtCapacityPreservesDependentRows(t *testing.T) {
	database := openGenerationTwoConstraintFixture(t)
	personal, charity, ids := seedBindingOrderFixture(t, database, 256)
	manifest, err := readGenerationManifest(context.Background(), database)
	if err != nil {
		t.Fatal(err)
	}
	children := interactionTableDigests(t, database, manifest)["request_adaptations"]
	for _, scope := range []struct {
		charity bool
		id      int64
	}{{false, personal}, {true, charity}} {
		order := append([]int64{}, ids...)
		for i := range order {
			order[i] = ids[len(ids)-1-i]
		}
		for attempt := 0; attempt < 2; attempt++ {
			tx, err := database.Begin()
			if err != nil {
				t.Fatal(err)
			}
			if err := ReorderBindingsTx(context.Background(), tx, scope.charity, scope.id, order, 8); err != nil {
				tx.Rollback()
				t.Fatal(err)
			}
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
			if got := bindingOrderIDs(t, database, scope.charity, scope.id); !reflect.DeepEqual(got, order) {
				t.Fatal("reorder differs", got)
			}
			order = append(append([]int64{}, order[127:]...), order[:127]...)
		}
		before := bindingOrderIDs(t, database, scope.charity, scope.id)
		bad := append([]int64{}, before...)
		bad[len(bad)-1] = 999999
		tx, err := database.Begin()
		if err != nil {
			t.Fatal(err)
		}
		if err := ReorderBindingsTx(context.Background(), tx, scope.charity, scope.id, bad, 9); err == nil {
			tx.Rollback()
			t.Fatal("foreign identity accepted")
		}
		if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
		if got := bindingOrderIDs(t, database, scope.charity, scope.id); !reflect.DeepEqual(got, before) {
			t.Fatal("failed reorder leaked changes")
		}
	}
	if got := interactionTableDigests(t, database, manifest)["request_adaptations"]; got != children {
		t.Fatal("reorder changed child identity/ciphertext/revision")
	}
}

func TestBindingCompactionDeletionPositions(t *testing.T) {
	for _, removed := range [][]int64{{1}, {2}, {4}, {1, 3}, {1, 2, 3, 4}} {
		t.Run(fmt.Sprint(removed), func(t *testing.T) {
			database := openGenerationTwoConstraintFixture(t)
			personal, charity, ids := seedBindingOrderFixture(t, database, 4)
			want := []int64{}
			for _, id := range ids {
				found := false
				for _, removedID := range removed {
					found = found || id == removedID
				}
				if !found {
					want = append(want, id)
				}
			}
			for _, scope := range []struct {
				charity bool
				id      int64
			}{{false, personal}, {true, charity}} {
				table, _, _ := bindingOrderNames(scope.charity)
				tx, err := database.Begin()
				if err != nil {
					t.Fatal(err)
				}
				for _, id := range removed {
					if _, err := tx.Exec(`DELETE FROM `+table+` WHERE id=?`, id); err != nil {
						tx.Rollback()
						t.Fatal(err)
					}
				}
				if err := compactBindingsTx(context.Background(), tx, scope.charity, scope.id); err != nil {
					tx.Rollback()
					t.Fatal(err)
				}
				if err := tx.Commit(); err != nil {
					t.Fatal(err)
				}
				if got := bindingOrderIDs(t, database, scope.charity, scope.id); !reflect.DeepEqual(got, want) {
					t.Fatal("deletion order", got, want)
				}
			}
			var count int
			if err := database.QueryRow(`SELECT count(*) FROM request_adaptations WHERE revision=7 AND secret_ciphertext='cipher-sentinel' AND updated_at=6`).Scan(&count); err != nil || count != len(want) {
				t.Fatal("surviving adaptations changed", count, err)
			}
		})
	}
}
