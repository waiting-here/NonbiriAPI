package db

import (
	"context"
	"database/sql"
	"fmt"
	"math/big"
	"reflect"
	"testing"
)

func governanceSourceFixture(t *testing.T) *sql.DB {
	t.Helper()
	database, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	database.SetMaxOpenConns(1)
	if _, err := database.Exec("PRAGMA foreign_keys=ON;" + generationTwoWithoutGovernanceSchema); err != nil {
		t.Fatal(err)
	}
	assertRetainedManifest(t, database, preGovernanceManifestHash)
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

func TestGovernancePublishedSourceUpgrade(t *testing.T) {
	ctx := context.Background()
	source := governanceSourceFixture(t)
	user := hostileInsertUser(t, source, "steward-source", 0, 1)
	hostileMustExec(t, source, `UPDATE users SET level=5,rpm_limit=23,concurrency_limit=7 WHERE id=?`, user)
	hostileMustExec(t, source, `UPDATE site_config SET value='Existing steward title' WHERE key='level_display_name_5'`)
	hostileMustExec(t, source, `INSERT INTO policy_audits(actor_user_id,actor_role,resource_type,resource_id,policy,old_value,new_value,created_at)
 VALUES(?,'level5','charity_model',1,'force_store_false',0,1,1)`, user)
	for mask := 0; mask < 32; mask++ {
		model := fmt.Sprintf("model-%d", mask)
		result := hostileMustExec(t, source, `INSERT INTO charity_models(provider,model,full_name,enabled,pricing_mode,created_at,updated_at) VALUES('fixture',?, ?,0,'per_request',1,1)`, model, "[公益]fixture/"+model)
		id := hostileMustLastID(t, result)
		hostileMustExec(t, source, `INSERT INTO charity_model_access(model_id,allowed_level_mask) VALUES(?,?)`, id, mask)
	}
	if err := extendKnownGenerationTwoSchema(ctx, source); err != nil {
		t.Fatal(err)
	}
	var level, rpm, concurrency int
	if err := source.QueryRow(`SELECT level,rpm_limit,concurrency_limit FROM users WHERE id=?`, user).Scan(&level, &rpm, &concurrency); err != nil || level != 6 || rpm != 23 || concurrency != 7 {
		t.Fatal(level, rpm, concurrency, err)
	}
	var historical, oldTitle, newTitle string
	if err := source.QueryRow(`SELECT actor_role FROM policy_audits`).Scan(&historical); err != nil || historical != "level5" {
		t.Fatal(historical, err)
	}
	if err := source.QueryRow(`SELECT value FROM site_config WHERE key='level_display_name_6'`).Scan(&oldTitle); err != nil || oldTitle != "Existing steward title" {
		t.Fatal(oldTitle, err)
	}
	if err := source.QueryRow(`SELECT value FROM site_config WHERE key='level_display_name_5'`).Scan(&newTitle); err != nil || newTitle != "见习协管" {
		t.Fatal(newTitle, err)
	}
	rows, err := source.Query(`SELECT allowed_level_mask FROM charity_model_access ORDER BY model_id`)
	if err != nil {
		t.Fatal(err)
	}
	i := 0
	for rows.Next() {
		var got int
		if err := rows.Scan(&got); err != nil {
			t.Fatal(err)
		}
		want := (i & 15) | ((i & 16) << 1) | ((i & 8) << 1)
		if got != want {
			t.Fatalf("mask %d became %d, want %d", i, got, want)
		}
		i++
	}
	if err := rows.Close(); err != nil || i != 32 {
		t.Fatal(i, err)
	}
	fresh := openGenerationTwoDDLForTest(t)
	t.Cleanup(func() { _ = fresh.Close() })
	got, err := readGenerationManifest(ctx, source)
	if err != nil {
		t.Fatal(err)
	}
	want, err := readGenerationManifest(ctx, fresh)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("fresh/upgrade mismatch: %s / %s: %v", generationManifestDigest(got), generationManifestDigest(want), err)
	}
	if err := validateGenerationTwoSeedManifest(ctx, source); err != nil {
		t.Fatal(err)
	}
	var before int64
	if err := source.QueryRow(`SELECT capture_started_at FROM observability_state`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if err := extendKnownGenerationTwoSchema(ctx, source); err != nil {
		t.Fatal(err)
	}
	var after int64
	if err := source.QueryRow(`SELECT capture_started_at FROM observability_state`).Scan(&after); err != nil || after != before {
		t.Fatal("restart changed capture epoch", after, err)
	}
}

func TestGovernanceActivityIntegerGuardsUseAll128Bits(t *testing.T) {
	database := openGenerationTwoDDLForTest(t)
	defer database.Close()
	wide := new(big.Int).Lsh(big.NewInt(1), 120)
	wide.Quo(wide, big.NewInt(1000)).Mul(wide, big.NewInt(1000))
	raw := make([]byte, 16)
	wide.FillBytes(raw)
	// A wide exact integer amount remains valid; one milliunit more does not.
	hostileMustExec(t, database, `INSERT INTO credit_accounts(kind,code,asset_type,balance_sign,balance_mag,created_at,updated_at)
 VALUES('external','external','sketch_paper',1,?,0,0)`, raw)
	bad := make([]byte, 16)
	new(big.Int).Add(wide, big.NewInt(1)).FillBytes(bad)
	if _, err := database.Exec(`UPDATE credit_accounts SET balance_mag=? WHERE asset_type='sketch_paper'`, bad); err == nil {
		t.Fatal("fractional wide activity balance accepted")
	}
	if _, err := database.Exec(`INSERT INTO credit_accounts(kind,code,asset_type,balance_sign,balance_mag,created_at,updated_at)
 VALUES('platform','image_activity_reserve','general',0,X'00000000000000000000000000000000',0,0)`); err == nil {
		t.Fatal("activity reserve accepted general credits")
	}
}
