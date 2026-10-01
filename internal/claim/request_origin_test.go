package claim

import (
	"context"
	"errors"
	"testing"
)

func TestRetiredRequestRootCannotBeRecreatedAnonymously(t *testing.T) {
	f := newClaimFixture(t)
	user := f.seedUser("source-origin", false)
	request := f.acceptSelf(user, 1)
	var origin int64
	if err := f.db.QueryRow("SELECT origin_user_id FROM request_logs WHERE logical_request_id=?", request.ID).Scan(&origin); err != nil || origin != user {
		t.Fatal(origin, err)
	}
	tx, err := f.db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err = tx.Exec("UPDATE logical_requests SET user_id=NULL WHERE id=?", request.ID); err != nil {
		t.Fatal(err)
	}
	if err = f.service.ensureRequestLogTx(context.Background(), tx, request.ID); err != nil {
		t.Fatal("retained root", err)
	}
	if _, err = tx.Exec("DELETE FROM request_logs WHERE logical_request_id=?", request.ID); err != nil {
		t.Fatal(err)
	}
	if err = f.service.ensureRequestLogTx(context.Background(), tx, request.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("retired root recreated", err)
	}
	var count int
	if err = tx.QueryRow("SELECT count(*) FROM request_logs WHERE logical_request_id=?", request.ID).Scan(&count); err != nil || count != 0 {
		t.Fatal(count, err)
	}
}
