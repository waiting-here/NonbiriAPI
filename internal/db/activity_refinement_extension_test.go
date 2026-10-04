package db

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"testing"
)

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
