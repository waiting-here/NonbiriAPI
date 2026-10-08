package db

import (
	"database/sql"
	"testing"
)

func TestModelTypesUpgradePreservesModelsAndRequestHistory(t *testing.T) {
	database, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	database.SetMaxOpenConns(1)
	defer database.Close()
	hostileMustExec(t, database, discordGateStorageSchema())
	user := hostileInsertUser(t, database, "image-route-owner", 0, 0)
	personal := hostileMustLastID(t, hostileMustExec(t, database, `INSERT INTO models(user_id,provider,model,full_name,revision,binding_revision,created_at,updated_at) VALUES(?,'provider','model','provider/model',1,7,2,3)`, user))
	charity := hostileMustLastID(t, hostileMustExec(t, database, `INSERT INTO charity_models(provider,model,full_name,pricing_mode,revision,binding_revision,created_at,updated_at) VALUES('provider','model','[公益]provider/model','per_request',1,11,2,3)`))
	request := hostileOIDVariant("req_", 'X', 'Q')
	hostileInsertLogicalRequest(t, database, request, user, "openai_embeddings", 1)
	log := hostileInsertRequestLog(t, database, request, user, "openai_embeddings")
	hostileMustExec(t, database, "UPDATE sqlite_sequence SET seq=1000 WHERE name='request_logs'")
	for range 2 {
		if err := extendKnownGenerationTwoSchema(t.Context(), database); err != nil {
			t.Fatal(err)
		}
		for _, model := range []struct {
			name         string
			id, revision int64
		}{{"models", personal, 7}, {"charity_models", charity, 11}} {
			var mask, revision, created, updated int64
			if err := database.QueryRow("SELECT model_types,binding_revision,created_at,updated_at FROM "+model.name+" WHERE id=?", model.id).Scan(&mask, &revision, &created, &updated); err != nil || mask != 3 || revision != model.revision || created != 2 || updated != 3 {
				t.Fatalf("%s: mask=%d revision=%d dates=%d/%d err=%v", model.name, mask, revision, created, updated, err)
			}
		}
		var route, state string
		var originalLog, sequence int64
		if err := database.QueryRow("SELECT l.route_kind,l.state,r.id FROM logical_requests l JOIN request_logs r ON r.logical_request_id=l.id WHERE l.id=?", request).Scan(&route, &state, &originalLog); err != nil || route != "openai_embeddings" || state != "accepted" || originalLog != log {
			t.Fatalf("history=%s/%s/%d err=%v", route, state, originalLog, err)
		}
		if err := database.QueryRow("SELECT seq FROM sqlite_sequence WHERE name='request_logs'").Scan(&sequence); err != nil || sequence != 1000 {
			t.Fatalf("sequence=%d err=%v", sequence, err)
		}
		assertRetainedManifest(t, database, PinnedGenerationTwoManifestHash)
	}
	newModel := hostileMustLastID(t, hostileMustExec(t, database, `INSERT INTO models(user_id,provider,model,full_name,created_at,updated_at) VALUES(?,'new','model','new/model',0,0)`, user))
	var mask int
	if err := database.QueryRow("SELECT model_types FROM models WHERE id=?", newModel).Scan(&mask); err != nil || mask != 1 {
		t.Fatalf("new model types=%d err=%v", mask, err)
	}
	for _, invalid := range []any{0, 8, -1, nil, 1.5, []byte{1}} {
		hostileMustFail(t, database, "UPDATE models SET model_types=? WHERE id=?", invalid, newModel)
	}
	for i, route := range []string{"openai_images_generations", "charity_images_generations"} {
		id := hostileOIDVariant("req_", byte('Y'+i), 'Q')
		hostileInsertLogicalRequest(t, database, id, user, route, 1)
		if actual := hostileInsertRequestLog(t, database, id, user, route); actual <= 1000 {
			t.Fatalf("log sequence reused: %d", actual)
		}
	}
}
