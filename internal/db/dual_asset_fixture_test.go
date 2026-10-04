package db

import (
	"database/sql"
	"testing"
)

func hostileGameAwardEntry(t *testing.T, database *sql.DB, userID int64, operationID string) {
	t.Helper()
	wallet := hostileMustLastID(t, hostileMustExec(t, database, `INSERT INTO credit_accounts(kind,user_id,asset_type,balance_sign,balance_mag,created_at,updated_at)
VALUES('user',?,'game',1,?,0,0)`, userID, hostileBlob16(1)))
	external := hostileMustLastID(t, hostileMustExec(t, database, `INSERT INTO credit_accounts(kind,code,asset_type,balance_sign,balance_mag,created_at,updated_at)
VALUES('external','external','game',-1,?,0,0)`, hostileBlob16(1)))
	hostileMustExec(t, database, `INSERT INTO credit_entries(operation_id,line_no,account_id,account_kind_snapshot,asset_type,delta_sign,delta_mag)
VALUES(?,0,?,'user','game',1,?)`, operationID, wallet, hostileBlob16(1))
	hostileMustExec(t, database, `INSERT INTO credit_entries(operation_id,line_no,account_id,account_kind_snapshot,asset_type,delta_sign,delta_mag,balance_after_sign,balance_after_mag)
VALUES(?,1,?,'external','game',-1,?,-1,?)`, operationID, external, hostileBlob16(1), hostileBlob16(1))
}
