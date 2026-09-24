package db

import (
	"context"
	"database/sql"
	"testing"
)

func TestAccountProtectionSchemaPins(t *testing.T) {
	database := openGenerationTwoDDLForTest(t)
	defer database.Close()
	manifest, err := readGenerationManifest(context.Background(), database)
	if err != nil {
		t.Fatal(err)
	}
	if hash := GenerationTwoSchemaHash(); hash != PinnedGenerationTwoSchemaHash {
		t.Errorf("schema hash: %s", hash)
	}
	if hash := generationManifestDigest(manifest); hash != PinnedGenerationTwoManifestHash {
		t.Errorf("manifest hash: %s", hash)
	}
}

func TestAccountProtectionPublishedSourceUpgrade(t *testing.T) {
	ctx := context.Background()
	database, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	database.SetMaxOpenConns(1)
	if _, err = database.Exec("PRAGMA foreign_keys=ON;" + generationTwoWithoutAccountProtectionSchema); err != nil {
		t.Fatal(err)
	}
	assertRetainedManifest(t, database, preAccountProtectionManifestHash)
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
	if err = extendKnownGenerationTwoSchema(ctx, database); err != nil {
		t.Fatal(err)
	}
	assertRetainedManifest(t, database, PinnedGenerationTwoManifestHash)
	var message string
	if err = database.QueryRow(`SELECT message FROM admin_alerts WHERE id=1`).Scan(&message); err != nil || message != "retained" {
		t.Fatal(message, err)
	}
	if err = extendKnownGenerationTwoSchema(ctx, database); err != nil {
		t.Fatal(err)
	}
	hostileMustExec(t, database, `INSERT INTO discord_blacklist VALUES('123456789012345678','blocked',1)`)
	if _, err = database.Exec(`INSERT INTO users(discord_id,username,created_at,updated_at) VALUES('123456789012345678','blocked',1,1)`); err == nil {
		t.Fatal("blacklisted registration accepted")
	}
}
