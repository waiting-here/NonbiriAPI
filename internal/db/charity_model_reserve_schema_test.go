package db

import (
	"testing"
)

func TestModelTokenReserveConstraintsAndCascade(t *testing.T) {
	store := openTestStore(t, bootstrapTestPath(t, "model-reserve-checks.sqlite"))
	hostileMustExec(t, store.DB(), `INSERT INTO charity_models(provider,model,full_name,pricing_mode,created_at,updated_at) VALUES('provider','model','[公益]provider/model','per_request',0,0)`)
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
