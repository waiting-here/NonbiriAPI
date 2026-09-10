package db

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func TestModelTokenReserveExtensionPreservesDeployedData(t *testing.T) {
	path, vault := bootstrapTestPath(t, "model-reserve.sqlite"), bootstrapTestVault(t)
	store, err := Open(path, vault)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	seedRetainedBusinessData(t, store, vault)
	makeRetainedSource(t, store.DB(), preModelTokenReserveManifestHash)
	before := retainedTableImages(t, store.DB(), nil)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	for round := 0; round < 2; round++ {
		store, err = Open(path, vault)
		if err != nil {
			t.Fatal(err)
		}
		assertRetainedManifest(t, store.DB(), PinnedGenerationTwoManifestHash)
		assertRetainedImages(t, store.DB(), before)
		if countRows(t, store, `SELECT COUNT(*) FROM charity_model_token_reserves`) != 0 {
			t.Fatal("upgrade invented model overrides")
		}
		if err := store.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestModelTokenReserveExtensionRollsBackAndRetries(t *testing.T) {
	store := openTestStore(t, bootstrapTestPath(t, "model-reserve-full.sqlite"))
	seedRetainedBusinessData(t, store, bootstrapTestVault(t))
	makeRetainedSource(t, store.DB(), preModelTokenReserveManifestHash)
	hostileMustExec(t, store.DB(), `VACUUM`)
	before := retainedTableImages(t, store.DB(), nil)
	var pages int
	if err := store.DB().QueryRow(`PRAGMA page_count`).Scan(&pages); err != nil {
		t.Fatal(err)
	}
	hostileMustExec(t, store.DB(), fmt.Sprintf(`PRAGMA max_page_count=%d`, pages))
	if err := extendKnownGenerationTwoSchema(context.Background(), store.DB()); err == nil || !strings.Contains(err.Error(), "full") {
		t.Fatalf("expected storage-full failure, got %v", err)
	}
	assertRetainedManifest(t, store.DB(), preModelTokenReserveManifestHash)
	assertRetainedImages(t, store.DB(), before)
	hostileMustExec(t, store.DB(), `PRAGMA max_page_count=2147483647`)
	if err := extendKnownGenerationTwoSchema(context.Background(), store.DB()); err != nil {
		t.Fatal(err)
	}
	assertRetainedManifest(t, store.DB(), PinnedGenerationTwoManifestHash)
	assertRetainedImages(t, store.DB(), before)
}

func TestModelTokenReserveConstraintsAndCascade(t *testing.T) {
	store := openTestStore(t, bootstrapTestPath(t, "model-reserve-checks.sqlite"))
	seedRetainedBusinessData(t, store, bootstrapTestVault(t))
	var model int64
	if err := store.DB().QueryRow(`SELECT id FROM charity_models LIMIT 1`).Scan(&model); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []any{nil, int64(0), int64(-1), MaxMoneyMilli + 1, 1.5, "bad", []byte{1}} {
		hostileMustFail(t, store.DB(), `INSERT INTO charity_model_token_reserves(model_id,amount_milli) VALUES(?,?)`, model, invalid)
	}
	hostileMustFail(t, store.DB(), `INSERT INTO charity_model_token_reserves(model_id,amount_milli) VALUES(999999,1)`)
	hostileMustExec(t, store.DB(), `INSERT INTO charity_model_token_reserves(model_id,amount_milli) VALUES(?,?)`, model, MaxMoneyMilli)
	hostileMustFail(t, store.DB(), `INSERT INTO charity_model_token_reserves(model_id,amount_milli) VALUES(?,1)`, model)
	hostileMustFail(t, store.DB(), `UPDATE charity_model_token_reserves SET amount_milli=0 WHERE model_id=?`, model)
	hostileMustExec(t, store.DB(), `UPDATE charity_model_token_reserves SET amount_milli=1 WHERE model_id=?`, model)
	hostileMustExec(t, store.DB(), `DELETE FROM charity_models WHERE id=?`, model)
	if countRows(t, store, `SELECT COUNT(*) FROM charity_model_token_reserves`) != 0 {
		t.Fatal("deleted model retained its override")
	}
}
