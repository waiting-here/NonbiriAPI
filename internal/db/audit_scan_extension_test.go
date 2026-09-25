package db

import (
	"context"
	"database/sql"
	"testing"
)

func TestAuditScanPublishedSourceUpgrade(t *testing.T) {
	ctx := context.Background()
	database, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	database.SetMaxOpenConns(1)
	if _, err = database.Exec("PRAGMA foreign_keys=ON;" + generationTwoWithoutAuditScansSchema); err != nil {
		t.Fatal(err)
	}
	assertRetainedManifest(t, database, preAuditScanManifestHash)
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = seedGenerationTwo(ctx, tx, hostileOID("b1e_")); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	hostileMustExec(t, database, `INSERT INTO admin_alerts(kind,message,created_at) VALUES('donation_failure_disabled','retained',1)`)
	before, err := readGenerationTwoFreshConfigSeedRows(ctx, database)
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err = extendKnownGenerationTwoSchema(ctx, database); err != nil {
			t.Fatal(err)
		}
		assertRetainedManifest(t, database, PinnedGenerationTwoManifestHash)
	}
	after, err := readGenerationTwoFreshConfigSeedRows(ctx, database)
	if err != nil || len(before) != len(after) {
		t.Fatal("configuration changed", err)
	}
	for i := range before {
		if before[i] != after[i] {
			t.Fatal("configuration changed")
		}
	}
	var message string
	if err = database.QueryRow(`SELECT message FROM admin_alerts WHERE id=1`).Scan(&message); err != nil || message != "retained" {
		t.Fatal(message, err)
	}
	if err = foreignKeyCheck(ctx, database); err != nil {
		t.Fatal(err)
	}
}
