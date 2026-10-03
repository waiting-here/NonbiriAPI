package db

import (
	"context"
	"database/sql"
	"testing"
)

func TestTransportUpgradeDefaultsPreservesModelsAndRejectsUnknown(t *testing.T) {
	database, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	database.SetMaxOpenConns(1)
	hostileMustExec(t, database, "PRAGMA foreign_keys=ON;"+preTransportSchema())
	assertRetainedManifest(t, database, preTransportManifestHash)
	tx, err := database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := seedGenerationTwo(context.Background(), tx, hostileOID("b1e_")); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	user := hostileInsertUser(t, database, "transport", 0, 0)
	model := hostileMustLastID(t, hostileMustExec(t, database, "INSERT INTO models(user_id,provider,model,full_name,created_at,updated_at) VALUES(?,'p','m','p/m',1700000000,1700000000)", user))
	charity := hostileMustLastID(t, hostileMustExec(t, database, "INSERT INTO charity_models(provider,model,full_name,pricing_mode,created_by_user_id,created_at,updated_at) VALUES('p','m','[公益]p/m','per_request',?,1700000000,1700000000)", user))
	for range 2 {
		if err := extendKnownGenerationTwoSchema(context.Background(), database); err != nil {
			t.Fatal(err)
		}
	}
	for table, id := range map[string]int64{"models": model, "charity_models": charity} {
		var rule, name string
		var revision int
		if err := database.QueryRow("SELECT transport_rule,model,revision FROM "+table+" WHERE id=?", id).Scan(&rule, &name, &revision); err != nil || rule != "passthrough" || name != "m" || revision != 1 {
			t.Fatal(rule, name, revision, err)
		}
		for _, value := range []string{"force_non_stream", "force_stream", "passthrough"} {
			if _, err := database.Exec("UPDATE "+table+" SET transport_rule=? WHERE id=?", value, id); err != nil {
				t.Fatal(err)
			}
		}
		for _, value := range []any{"unknown", "", nil} {
			if _, err := database.Exec("UPDATE "+table+" SET transport_rule=? WHERE id=?", value, id); err == nil {
				t.Fatal("invalid transport accepted", value)
			}
		}
	}
	if err := validateGenerationTwoManifest(context.Background(), database); err != nil {
		t.Fatal(err)
	}
}
