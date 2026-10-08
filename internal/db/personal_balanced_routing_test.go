package db

import (
	"database/sql"
	"testing"
)

func TestPersonalBalancedRoutingUpgradePreservesModelsAndBindings(t *testing.T) {
	database, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	database.SetMaxOpenConns(1)
	defer database.Close()
	hostileMustExec(t, database, "PRAGMA foreign_keys=ON;"+modelTypesAndImagesStorageSchema())
	assertRetainedManifest(t, database, modelTypesAndImagesManifestHash)
	user := hostileInsertUser(t, database, "balanced-owner", 0, 0)
	endpoint := hostileInsertEndpoint(t, database, user, "https://routing.example/v1")
	secret := hostileInsertSecret(t, database, "https://routing.example/v1", 0)
	key := hostileInsertEndpointKey(t, database, endpoint, secret)
	model := hostileMustLastID(t, hostileMustExec(t, database, `INSERT INTO models
(user_id,provider,model,full_name,route_strategy,revision,binding_revision,model_types,created_at,updated_at)
VALUES(?,'provider','model','provider/model','random',5,7,6,2,3)`, user))
	hostileMustExec(t, database, `INSERT INTO model_pair_catalog(endpoint_key_id,normalized_model_id,manual_supports,updated_at) VALUES(?,'upstream',1,0)`, key)
	binding := hostileMustLastID(t, hostileMustExec(t, database, `INSERT INTO model_bindings(model_id,endpoint_key_id,upstream_model_id,ord,created_at,updated_at) VALUES(?,?,'upstream',3,2,3)`, model, key))
	hostileMustExec(t, database, "UPDATE sqlite_sequence SET seq=1000 WHERE name='models'")
	for range 2 {
		if err := extendKnownGenerationTwoSchema(t.Context(), database); err != nil {
			t.Fatal(err)
		}
		var strategy, upstream string
		var mask, revision, bindingRevision, created, updated, bindingID, order, sequence int64
		if err := database.QueryRow(`SELECT m.route_strategy,m.model_types,m.revision,m.binding_revision,m.created_at,m.updated_at,b.id,b.ord,b.upstream_model_id
FROM models m JOIN model_bindings b ON b.model_id=m.id WHERE m.id=?`, model).
			Scan(&strategy, &mask, &revision, &bindingRevision, &created, &updated, &bindingID, &order, &upstream); err != nil ||
			strategy != "random" || mask != 6 || revision != 5 || bindingRevision != 7 || created != 2 || updated != 3 || bindingID != binding || order != 3 || upstream != "upstream" {
			t.Fatal("model or binding changed during upgrade", strategy, mask, revision, bindingRevision, created, updated, bindingID, order, upstream, err)
		}
		if err := database.QueryRow("SELECT seq FROM sqlite_sequence WHERE name='models'").Scan(&sequence); err != nil || sequence != 1000 {
			t.Fatalf("model sequence=%d err=%v", sequence, err)
		}
		assertRetainedManifest(t, database, PinnedGenerationTwoManifestHash)
	}
	hostileMustExec(t, database, "UPDATE models SET route_strategy='cache_balanced' WHERE id=?", model)
	hostileMustFail(t, database, "UPDATE models SET route_strategy='expiry_weighted' WHERE id=?", model)
	hostileMustFail(t, database, "UPDATE models SET route_strategy='unknown' WHERE id=?", model)
	newModel := hostileMustLastID(t, hostileMustExec(t, database, `INSERT INTO models(user_id,provider,model,full_name,created_at,updated_at) VALUES(?,'new','model','new/model',0,0)`, user))
	if newModel <= 1000 {
		t.Fatalf("model sequence reused: %d", newModel)
	}
}

func TestPersonalBalancedRoutingStorageEnforcesOwnerAndUnlinksDeletedModel(t *testing.T) {
	database := openGenerationTwoDDLForTest(t)
	defer database.Close()
	hostileMustExec(t, database, "PRAGMA foreign_keys=ON; INSERT INTO charity_routing_capacity VALUES(1,0,0,1)")
	user := hostileInsertUser(t, database, "personal-owner", 0, 0)
	other := hostileInsertUser(t, database, "personal-other", 0, 0)
	endpoint := hostileInsertEndpoint(t, database, user, "https://personal.example/v1")
	secret := hostileInsertSecret(t, database, "https://personal.example/v1", 0)
	key := hostileInsertEndpointKey(t, database, endpoint, secret)
	model := hostileMustLastID(t, hostileMustExec(t, database, `INSERT INTO models(user_id,provider,model,full_name,route_strategy,created_at,updated_at) VALUES(?,'a','b','a/b','cache_balanced',0,0)`, user))
	attempt := hostileOID("clm_")
	insert := `INSERT INTO personal_key_affinities(user_id,model_id,endpoint_key_id,physical_id,attempt_id,routing_revision,dispatched_at,expires_at) VALUES(?,?,?,?,?,1,100,400)`
	hostileMustFail(t, database, insert, other, model, key, hostileBlob32(3), attempt)
	hostileMustExec(t, database, insert, user, model, key, hostileBlob32(3), attempt)
	hostileMustFail(t, database, "UPDATE personal_key_affinities SET expires_at=401")
	hostileMustExec(t, database, `INSERT INTO charity_dispatch_receipts
(attempt_id,physical_id,endpoint_key_id,user_id,personal_model_id,routing_revision,state,reserved_at,dispatched_at,expires_at)
VALUES(?,?,?,?,?,1,'dispatched',100,100,400)`, attempt, hostileBlob32(3), key, user, model)
	hostileMustExec(t, database, "INSERT INTO charity_dispatch_buckets VALUES(?,100,1)", hostileBlob32(3))
	hostileMustExec(t, database, "DELETE FROM models WHERE id=?", model)
	var affinityCount, capacity, buckets int
	var receiptModel sql.NullInt64
	if err := database.QueryRow(`SELECT (SELECT count(*) FROM personal_key_affinities),affinities,buckets FROM charity_routing_capacity WHERE id=1`).Scan(&affinityCount, &capacity, &buckets); err != nil || affinityCount != 0 || capacity != 0 || buckets != 1 {
		t.Fatal("model deletion left association or removed load", affinityCount, capacity, buckets, err)
	}
	if err := database.QueryRow("SELECT personal_model_id FROM charity_dispatch_receipts WHERE attempt_id=?", attempt).Scan(&receiptModel); err != nil || receiptModel.Valid {
		t.Fatal("deleted model retained receipt association", receiptModel, err)
	}
	hostileMustFail(t, database, "UPDATE charity_dispatch_receipts SET personal_model_id=?", model)
}
