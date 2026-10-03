package db

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"reflect"
	"testing"
)

func economyAuditChannelsSourceFixture(t *testing.T) *sql.DB {
	t.Helper()
	database, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	database.SetMaxOpenConns(1)
	t.Cleanup(func() { database.Close() })
	hostileMustExec(t, database, "PRAGMA foreign_keys=ON;"+preEconomyAuditChannelsSchema())
	assertRetainedManifest(t, database, preEconomyAuditChannelsManifestHash)
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
	// Cover every asset, both bucket sizes and all checkpoint fields. Wide
	// decimal measures must survive the rebuild without numeric coercion.
	for _, asset := range []string{"general", "game", "sketch_paper", "sketch_brush"} {
		for _, bucket := range []string{"hour", "day"} {
			hostileMustExec(t, database, "INSERT INTO economy_audit_buckets VALUES(?,'admin_user_adjustment','operation','admin',?,0,0,'340282366920938463463374607431768211455','2','3','4','5',3,1,3)", asset, bucket)
		}
	}
	hostileMustExec(t, database, "UPDATE economy_audit_checkpoint SET last_ledger_seq=3,first_ledger_seq=1,first_occurred_at=123,offset_minutes=0,unclassified_operations=2,opening_known=0,updated_at=456 WHERE id=1")
	return database
}

func TestEconomyAuditChannelsUpgradePreservesAllDataAndIndexes(t *testing.T) {
	database := economyAuditChannelsSourceFixture(t)
	ctx := context.Background()
	manifest, err := readGenerationManifest(ctx, database)
	if err != nil {
		t.Fatal(err)
	}
	before := interactionTableDigests(t, database, manifest)
	for range 2 {
		if err := extendKnownGenerationTwoSchema(ctx, database); err != nil {
			t.Fatal(err)
		}
		assertForeignKeyEnforcement(t, database)
		assertRetainedManifest(t, database, PinnedGenerationTwoManifestHash)
		if after := interactionTableDigests(t, database, manifest); !reflect.DeepEqual(before, after) {
			t.Fatal("upgrade changed retained table data")
		}
	}
	// Fresh and upgraded databases enforce the same channel boundary.
	for _, channel := range []string{"fat_fish", "lake_notes"} {
		hostileMustExec(t, database, "INSERT INTO economy_audit_buckets VALUES('general','audit_fixture','operation',?,'hour',3600,0,'0','0','0','0','0',1,4,4)", channel)
	}
	for _, channel := range []any{"unknown", "", nil} {
		hostileMustFail(t, database, "INSERT INTO economy_audit_buckets VALUES('general','audit_fixture','operation',?,'hour',7200,0,'0','0','0','0','0',1,4,4)", channel)
	}
}

func TestEconomyAuditChannelsUpgradeRollbackCancellationAndRetry(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		t.Run(map[bool]string{false: "failure", true: "cancellation"}[cancelled], func(t *testing.T) {
			database := economyAuditChannelsSourceFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			manifest, err := readGenerationManifest(ctx, database)
			if err != nil {
				t.Fatal(err)
			}
			before := interactionTableDigests(t, database, manifest)
			injected := errors.New("injected rollback")
			err = runGenerationTwoExtension(ctx, database, func(ctx context.Context, tx *sql.Tx) error {
				if err := applyEconomyAuditChannelsExtension(ctx, tx); err != nil {
					return err
				}
				if cancelled {
					cancel()
					return nil
				}
				return injected
			})
			want := injected
			if cancelled {
				want = context.Canceled
			}
			if !errors.Is(err, want) {
				t.Fatalf("rollback error %v", err)
			}
			assertForeignKeyEnforcement(t, database)
			assertRetainedManifest(t, database, preEconomyAuditChannelsManifestHash)
			if after := interactionTableDigests(t, database, manifest); !reflect.DeepEqual(before, after) {
				t.Fatal("rollback changed retained data")
			}
			if err := extendKnownGenerationTwoSchema(context.Background(), database); err != nil {
				t.Fatal(err)
			}
			assertRetainedManifest(t, database, PinnedGenerationTwoManifestHash)
		})
	}
}

func TestEconomyAuditChannelsUpgradeFromReleasedBinary(t *testing.T) {
	source := os.Getenv("NONBIRI_ECONOMY_AUDIT_FIXTURE")
	if source == "" {
		t.Skip("released-source gate supplies a consistent fixture")
	}
	verifyReleasedStorageUpgrade(t, source, preEconomyAuditChannelsManifestHash)
}
