package observability

import (
	"context"
	"database/sql"
	"testing"
	"time"
)

func TestRetainedOriginSourceCompletionRacesAccountDeletion(t *testing.T) {
	repository, user := newObservedDatabase(t)
	at := repository.now().Unix() - 1
	id, root := observedLog(t, repository, user, at)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ctx = WithSource(ctx, Source{EffectiveIP: "192.0.2.15", IPQuality: "trusted_forwarded", UserAgent: "Example/1"})
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = repository.RecordSourceTx(ctx, tx, id, user, "unclassified", at); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	go func() {
		<-start
		tx, e := repository.db.BeginTx(ctx, nil)
		if e != nil {
			results <- e
			return
		}
		defer tx.Rollback()
		if e = repository.RecordSourceTx(ctx, tx, id, user, "charity", at); e == nil {
			e = tx.Commit()
		}
		results <- e
	}()
	go func() {
		<-start
		tx, e := repository.db.BeginTx(ctx, nil)
		if e != nil {
			results <- e
			return
		}
		defer tx.Rollback()
		_, e = tx.ExecContext(ctx, `UPDATE logical_requests SET user_id=NULL WHERE user_id=?`, user)
		if e == nil {
			_, e = tx.ExecContext(ctx, `DELETE FROM users WHERE id=?`, user)
		}
		if e == nil {
			e = tx.Commit()
		}
		results <- e
	}()
	close(start)
	for range 2 {
		if err = <-results; err != nil {
			t.Fatal(err)
		}
	}
	var origin int64
	var discord, kind string
	var active sql.NullInt64
	err = repository.db.QueryRow(`SELECT l.origin_user_id,l.origin_discord_id,l.user_id,s.kind FROM request_logs l JOIN request_source_facts s ON s.request_log_id=l.id WHERE l.id=?`, root).Scan(&origin, &discord, &active, &kind)
	if err != nil || origin != user || discord != "70001" || active.Valid || kind != "charity" {
		t.Fatal(origin, discord, active, kind, err)
	}
	tx, err = repository.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = repository.RecordSourceTx(ctx, tx, id, user+1, "self", at); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err = repository.db.Exec(`DELETE FROM request_logs WHERE id=?`, root); err != nil {
		t.Fatal(err)
	}
	tx, err = repository.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = repository.RecordSourceTx(ctx, tx, id, user, "self", at); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if got := scalar(t, repository.db, `SELECT count(*) FROM request_source_facts WHERE request_log_id=?`, root); got != 0 {
		t.Fatal("late source resurrected retired root", got)
	}
}
