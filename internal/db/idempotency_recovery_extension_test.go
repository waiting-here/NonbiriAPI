package db

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"testing"
)

func idempotencyRecoverySourceFixture(t *testing.T) *sql.DB {
	t.Helper()
	database, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	database.SetMaxOpenConns(1)
	t.Cleanup(func() { database.Close() })
	hostileMustExec(t, database, "PRAGMA foreign_keys=ON;"+preIdempotencyRecoverySchema())
	assertRetainedManifest(t, database, preIdempotencyRecoveryManifestHash)
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
	user := hostileInsertUser(t, database, "receipt", 0, 1)
	for _, state := range []string{"accepted", "completed"} {
		status, body := 0, []byte{}
		if state == "completed" {
			status, body = 200, []byte(`{"retained":true}`)
		}
		hostileMustExec(t, database, `INSERT INTO idempotency_records(scope,actor_scope_hash,key_hash,request_hash,state,http_status,response_body,created_at,expires_at)
 VALUES('control_mutation',zeroblob(32),?,zeroblob(32),?,?,?,1,86401)`, []byte(state + "0000000000000000000000000")[:32], state, status, body)
	}
	hostileMustExec(t, database, `INSERT INTO resource_operation_status VALUES(zeroblob(32),zeroblob(32),?,'endpoint','recorded','{"endpoint_id":"17"}',1,86401)`, user)
	return database
}

func TestIdempotencyRecoveryUpgradePreservesDataAndMatchesFreshIndexes(t *testing.T) {
	database := idempotencyRecoverySourceFixture(t)
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
			t.Fatal("index upgrade changed retained data")
		}
	}
	fresh := openGenerationTwoDDLForTest(t)
	defer fresh.Close()
	for _, source := range []*sql.DB{database, fresh} {
		var definition string
		if err := source.QueryRow(`SELECT sql FROM sqlite_schema WHERE name='idx_idempotency_recovery'`).Scan(&definition); err != nil || definition != "CREATE INDEX idx_idempotency_recovery ON idempotency_records(state,expires_at,scope,actor_scope_hash,key_hash)" {
			t.Fatalf("recovery index = %q, %v", definition, err)
		}
	}
}

func TestIdempotencyRecoveryUpgradeRollbackCancellationAndRetry(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		t.Run(map[bool]string{false: "failure", true: "cancellation"}[cancelled], func(t *testing.T) {
			database := idempotencyRecoverySourceFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			injected := errors.New("injected index rollback")
			err := runGenerationTwoExtension(ctx, database, func(ctx context.Context, tx *sql.Tx) error {
				if err := applyIdempotencyRecoveryExtension(ctx, tx); err != nil {
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
				t.Fatalf("rollback error = %v", err)
			}
			assertForeignKeyEnforcement(t, database)
			assertRetainedManifest(t, database, preIdempotencyRecoveryManifestHash)
			if err := extendKnownGenerationTwoSchema(context.Background(), database); err != nil {
				t.Fatal(err)
			}
			assertRetainedManifest(t, database, PinnedGenerationTwoManifestHash)
		})
	}
}
