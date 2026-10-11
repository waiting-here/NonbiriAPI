package db

import (
	"database/sql"
	"testing"
)

func TestPersonalAdaptationUpgradePreservesEncryptedSettings(t *testing.T) {
	database, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	database.SetMaxOpenConns(1)
	defer database.Close()
	hostileMustExec(t, database, "PRAGMA foreign_keys=ON;"+sessionHeadersStorageSchema())
	personal, _, bindings := seedBindingOrderFixture(t, database, 1)
	hostileMustExec(t, database, `INSERT INTO request_adaptation_audits(scope,resource_id,actor_role,revision,changed_partitions,created_at) VALUES('binding',?,'admin',7,'["fixed_headers"]',6)`, bindings[0])
	for range 2 {
		if err := extendKnownGenerationTwoSchema(t.Context(), database); err != nil {
			t.Fatal(err)
		}
		var cipher, structure, changed string
		var revision int
		if err := database.QueryRow("SELECT secret_ciphertext,structure_json,revision FROM request_adaptations WHERE binding_id=?", bindings[0]).Scan(&cipher, &structure, &revision); err != nil || cipher != "cipher-sentinel" || structure != "{}" || revision != 7 {
			t.Fatal(cipher, structure, revision, err)
		}
		if err := database.QueryRow("SELECT changed_partitions FROM request_adaptation_audits WHERE scope='binding' AND resource_id=?", bindings[0]).Scan(&changed); err != nil || changed != `["fixed_headers"]` {
			t.Fatal(changed, err)
		}
		assertRetainedManifest(t, database, PinnedGenerationTwoManifestHash)
	}
	hostileMustExec(t, database, `INSERT INTO request_adaptations(scope,personal_model_id,revision,secret_context,secret_ciphertext,structure_json,updated_at) VALUES('personal_model',?,1,zeroblob(16),'model-private','{}',6)`, personal)
	hostileMustFail(t, database, `UPDATE request_adaptations SET endpoint_id=1 WHERE personal_model_id=?`, personal)
	hostileMustExec(t, database, "DELETE FROM models WHERE id=?", personal)
	var count int
	if err := database.QueryRow("SELECT count(*) FROM request_adaptations WHERE personal_model_id=?", personal).Scan(&count); err != nil || count != 0 {
		t.Fatal(count, err)
	}
}
